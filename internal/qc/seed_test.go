package qc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedMatrixMutations(t *testing.T) {
	data := []byte("file,version_id,language,phone:contact,text:legal\nsynthetic-a.mp4,synthetic-a,en-GB,2025550100,Terms apply\nsynthetic-b.mp4,synthetic-b,en-GB,2025550101,Terms apply\nsynthetic-c.mp4,synthetic-c,en-GB,2025550102,Terms apply\nsynthetic-d.mp4,synthetic-d,en-GB,2025550103,Terms apply\n")
	mutated, labels, err := SeedMatrix(data, true)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseMatrix(mutated)
	if err != nil {
		t.Fatal(err)
	}
	if m.Rows[0].Expectations[0].NormalisedValue != "2025550101" || m.Rows[1].Expectations[0].NormalisedValue != "2025550100" {
		t.Fatal("phone swap missing")
	}
	if m.Rows[2].Expectations[1].NormalisedValue != "matrixqc seeded required legal" {
		t.Fatal("missing legal expectation not seeded")
	}
	if m.Rows[3].Expectations[0].NormalisedValue == "2025550103" || len(labels) != 4 {
		t.Fatal("digit mutation missing", labels)
	}
	for _, l := range labels {
		if !l.Critical || !l.Synthetic {
			t.Fatal("synthetic labels lost provenance", l)
		}
	}
	if _, _, err := SeedMatrix([]byte("file,text:legal\nsynthetic.mp4,Terms apply\n"), true); err == nil {
		t.Fatal("missing phone data accepted")
	}
}

func TestSpliceOneFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("ffmpeg integration")
	}
	dir := cacheDir(t)
	if err := Synth(context.Background(), dir, SynthOptions{42, 8, 1, 640, 360, 24}); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dir, "synthetic-00-clean.mp4")
	b := filepath.Join(dir, "synthetic-03-foreign_phone.mp4")
	info, err := Probe(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "synthetic-spliced.mkv")
	if err := Splice(context.Background(), a, b, out, 12); err != nil {
		t.Fatal(err)
	}
	after, err := Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if after.FrameCount != 24 {
		t.Fatal(after)
	}
	beforeFrames := map[int][]byte{}
	_, err = AnalyzeVideo(context.Background(), a, info, fixedRecognizer{}, PipelineOptions{NoGate: true}, func(f Frame, _ []Box, _ bool) error { beforeFrames[f.ID] = append([]byte(nil), f.Gray...); return nil })
	if err != nil {
		t.Fatal(err)
	}
	changed := []int{}
	_, err = AnalyzeVideo(context.Background(), out, after, fixedRecognizer{}, PipelineOptions{NoGate: true}, func(f Frame, _ []Box, _ bool) error {
		for i, v := range f.Gray {
			if v != beforeFrames[f.ID][i] {
				changed = append(changed, f.ID)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != 12 {
		t.Fatalf("splice changed other frames: %v", changed)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("source was mutated")
	}
}
