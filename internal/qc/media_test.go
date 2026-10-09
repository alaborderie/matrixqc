package qc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cacheDir(t *testing.T) string {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	base = filepath.Join(base, "matrixqc", "tests")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(p); err != nil {
			t.Error(err)
		}
	})
	return p
}

func TestProbeFacts(t *testing.T) {
	data := []byte(`{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"h264","width":854,"height":480,"avg_frame_rate":"30000/1001","nb_read_frames":"150","tags":{"timecode":"01:00:00:00"}}],"format":{"format_name":"mov,mp4","duration":"5.005"}}`)
	info, err := ParseProbe(data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Codec != "h264" || info.Width != 854 || info.Height != 480 || info.FrameCount != 150 || info.StartTimecode != "01:00:00:00" || info.FPS < 29.97 || info.FPS > 29.971 {
		t.Fatalf("bad facts: %+v", info)
	}
	for _, bad := range []string{`{}`, `{"streams":[{"codec_type":"video","width":0}]}`, `{"streams":[{"codec_type":"video","width":100,"height":100,"avg_frame_rate":"0/0"}]}`} {
		if _, err := ParseProbe([]byte(bad)); err == nil {
			t.Fatalf("accepted unsupported: %s", bad)
		}
	}
}

func TestSyntheticPlanLabelsAndSeed(t *testing.T) {
	a, err := PlanSynthetic(SynthOptions{Seed: 42, Variants: 8, Seconds: 5, Width: 854, Height: 480, FPS: 24})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PlanSynthetic(SynthOptions{Seed: 42, Variants: 8, Seconds: 5, Width: 854, Height: 480, FPS: 24})
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		t.Fatal("same seed differs")
	}
	c, _ := PlanSynthetic(SynthOptions{Seed: 43, Variants: 8, Seconds: 5, Width: 854, Height: 480, FPS: 24})
	cj, _ := json.Marshal(c)
	if string(aj) == string(cj) {
		t.Fatal("seed ignored")
	}
	checks := map[string]bool{}
	ratios := map[string]bool{}
	nonEnglish := false
	for _, v := range a.Variants {
		if !v.Label.Synthetic || !strings.HasPrefix(v.File, "synthetic-") || !strings.Contains(v.VersionID, "synthetic") {
			t.Fatalf("unlabelled synthetic file: %+v", v)
		}
		for _, c := range v.Label.ExpectedChecks {
			checks[c] = true
		}
		ratios[v.Aspect] = true
		nonEnglish = nonEnglish || strings.HasPrefix(v.Language, "fr")
		if v.Label.SeedKind == "single_frame_foreign_phone" && v.InjectionFrame != 60 {
			t.Fatalf("wrong injection: %+v", v)
		}
	}
	for _, c := range []string{"C0", "C1", "C2", "C3", "C4"} {
		if !checks[c] {
			t.Fatalf("missing label %s", c)
		}
	}
	if len(ratios) != 3 || !nonEnglish {
		t.Fatal("aspect/language coverage missing")
	}
	clean := a.Variants[3]
	clean.ErrorKind = ""
	if label := SyntheticLabel(clean); len(label.ExpectedChecks) != 0 || label.ExpectedOutcome != "pass" {
		t.Fatalf("label disconnected from injection: %+v", label)
	}
}

func TestSynthAndProbeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("ffmpeg integration")
	}
	dir := cacheDir(t)
	opts := SynthOptions{Seed: 42, Variants: 8, Seconds: 1, Width: 640, Height: 360, FPS: 24}
	if err := Synth(context.Background(), dir, opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "synthetic-matrix.csv"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseMatrix(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rows) != 7 {
		t.Fatal("C0 missing-row seed absent")
	}
	info, err := Probe(context.Background(), filepath.Join(dir, "synthetic-00-clean.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.FrameCount != 24 || info.Width != 640 || info.Height != 360 {
		t.Fatal(info)
	}
	var labels []Label
	data, err = os.ReadFile(filepath.Join(dir, "synthetic-labels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &labels); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 8 {
		t.Fatal(labels)
	}
	second := cacheDir(t)
	if err := Synth(context.Background(), second, opts); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		a, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(second, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(a) != sha256.Sum256(b) {
			t.Fatalf("non-deterministic synthetic output: %s", entry.Name())
		}
	}
	if err := Synth(context.Background(), dir, opts); err == nil {
		t.Fatal("overwrote an existing corpus")
	}
}

func TestRejectTmpOutput(t *testing.T) {
	for _, p := range []string{"/tmp/matrixqc-test", "/var/tmp/matrixqc-test"} {
		if err := ValidateOutput(p); err == nil {
			t.Fatalf("allowed scratch %s", p)
		}
	}
	dir := cacheDir(t)
	link := filepath.Join(dir, "escape")
	if err := os.Symlink("/tmp", link); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutput(filepath.Join(link, "new")); err == nil {
		t.Fatal("allowed symlink to tmp")
	}
}

func TestRejectDanglingTmpSymlink(t *testing.T) {
	dir := cacheDir(t)
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink("/tmp/matrixqc-forbidden-nonexistent", link); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutput(link); err == nil {
		t.Fatal("dangling file symlink bypassed scratch policy")
	}
}
