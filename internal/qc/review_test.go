package qc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormattedSiblingPhoneOneFrameCannotPass(t *testing.T) {
	for _, phone := range []string{"202.555.0101", "202\u00a0555\u00a00101", "(202) 555-0101"} {
		t.Run(phone, func(t *testing.T) {
			m, row := matchFixture(t)
			matcher := NewMatcher(m, row, MatchOptions{12, .8, nil})
			for f := 0; f < 12; f++ {
				matcher.Observe(f, []Box{readBox("2025550100", .99), readBox("Terms apply", .99)})
			}
			matcher.Observe(12, []Box{readBox(phone, .99), readBox("Terms apply", .99)})
			outcome, findings := matcher.Finish(13)
			if outcome != "fail" || !hasCheck(findings, "C2") {
				t.Fatalf("formatted sibling silently passed: %s %+v", outcome, findings)
			}
			for _, f := range findings {
				if f.Check == "C2" && (f.FirstFrame != 12 || f.LastFrame != 12 || f.OtherVersionRowID != "row-3") {
					t.Fatal("lost one-frame owner", f)
				}
			}
		})
	}
}

func TestURLSuffixNeverTruncatedToPass(t *testing.T) {
	for _, tc := range []struct {
		expected, read string
		exact          bool
	}{
		{"example.invalid/a", "example.invalid/a#wrong", false},
		{"example.invalid/a", "example.invalid/a~wrong", false},
		{"example.invalid", "example.invalid:8080", false},
		{"example.invalid/a#right", "https://example.invalid/a#right", true},
		{"example.invalid:8080/a~right", "example.invalid:8080/a~right", true},
	} {
		t.Run(tc.read, func(t *testing.T) {
			m, err := ParseMatrix([]byte("file,language,url:offer\nsynthetic.mp4,en-GB," + tc.expected + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			matcher := NewMatcher(m, m.Rows[0], MatchOptions{12, .8, nil})
			for f := 0; f < 12; f++ {
				matcher.Observe(f, []Box{readBox(tc.read, .99)})
			}
			outcome, findings := matcher.Finish(12)
			if tc.exact && outcome != "pass" {
				t.Fatalf("exact full URL rejected: %s %+v", outcome, findings)
			}
			if !tc.exact && outcome == "pass" {
				t.Fatalf("wrong destination silently passed: %+v", findings)
			}
		})
	}
}

func TestFindingEscalatesToConfidentFail(t *testing.T) {
	m, row := matchFixture(t)
	matcher := NewMatcher(m, row, MatchOptions{1, .8, nil})
	matcher.Observe(0, []Box{readBox("2025550101", .3), readBox("Terms apply", .99)})
	matcher.Observe(1, []Box{readBox("2025550101", .99), readBox("Terms apply", .99)})
	outcome, findings := matcher.Finish(2)
	confident := false
	for _, f := range findings {
		if f.Check == "C2" && f.Outcome == "fail" && f.FirstFrame == 1 && f.Confidence == .99 {
			confident = true
		}
	}
	if outcome != "fail" || !confident {
		t.Fatalf("confident foreign value underclassified: %s %+v", outcome, findings)
	}
}

func TestFindingDoesNotBridgeTemporalGaps(t *testing.T) {
	m, row := matchFixture(t)
	matcher := NewMatcher(m, row, MatchOptions{1, .8, nil})
	for f := 0; f <= 100; f++ {
		boxes := []Box{readBox("2025550100", .99), readBox("Terms apply", .99)}
		if f == 10 || f == 100 {
			boxes = append(boxes, readBox("FPO", .99))
		}
		matcher.Observe(f, boxes)
	}
	_, findings := matcher.Finish(101)
	spans := 0
	for _, f := range findings {
		if f.Check == "C3" {
			spans++
			if f.FirstFrame != f.LastFrame {
				t.Fatal("invented continuous span", f)
			}
		}
	}
	if spans != 2 {
		t.Fatalf("gap merged: %+v", findings)
	}
}

func TestTextlessLeaderDoesNotInvalidateConfirmedText(t *testing.T) {
	m, row := matchFixture(t)
	matcher := NewMatcher(m, row, MatchOptions{12, .8, nil})
	matcher.Observe(0, nil)
	for f := 1; f <= 12; f++ {
		matcher.Observe(f, []Box{readBox("2025550100", .99), readBox("Terms apply", .99)})
	}
	outcome, findings := matcher.Finish(13)
	if outcome != "pass" {
		t.Fatalf("textless leader permanently penalized: %s %+v", outcome, findings)
	}
}

func TestSeedRejectsNoOpSwapAndAmbiguousMutationOwner(t *testing.T) {
	for _, phones := range [][]string{{"2025550100", "2025550100", "2025550102", "2025550103"}, {"2025550100", "2025550101", "2025550102", "2025550100"}} {
		csv := "file,language,phone:x,text:legal\n"
		for i, p := range phones {
			csv += string(rune('a'+i)) + ".mp4,en-GB," + p + ",Terms apply\n"
		}
		t.Run(strings.Join(phones, "-"), func(t *testing.T) {
			if _, _, err := SeedMatrix([]byte(csv), false); err == nil {
				t.Fatalf("seeded invalid critical labels for %v", phones)
			}
		})
	}
}

func completeBenchFixture() (Batch, []Label) {
	now := time.Now()
	b := Batch{Synthetic: true, Gate: PipelineOptions{NoGate: true}, StartedAt: now, FinishedAt: now.Add(time.Second), AnalysedSeconds: 2, Deliverables: []Deliverable{
		{Path: "synthetic-clean", Outcome: "pass", MediaInfo: MediaInfo{FrameCount: 24}, FrameStats: FrameStats{FramesDecoded: 24, FramesOCRd: 24}},
		{Path: "synthetic-bad", Outcome: "fail", MediaInfo: MediaInfo{FrameCount: 24}, FrameStats: FrameStats{FramesDecoded: 24, FramesOCRd: 24}},
	}}
	return b, []Label{{DeliverablePath: "synthetic-clean", ExpectedOutcome: "pass", Synthetic: true}, {DeliverablePath: "synthetic-bad", ExpectedOutcome: "fail", Critical: true, Synthetic: true}}
}

func TestBenchmarkRejectsUnexpectedIncompleteAnalysis(t *testing.T) {
	b, labels := completeBenchFixture()
	b.Deliverables[1].Outcome = "error"
	b.Deliverables[1].FramesDecoded = 1
	b.Deliverables[1].FramesOCRd = 0
	if _, err := ScoreBenchmark(b, labels); err == nil {
		t.Fatal("incomplete critical analysis published favorable metrics")
	}
}

func TestBenchmarkNoGateCannotClaimFailedFrameAsSkipped(t *testing.T) {
	b, labels := completeBenchFixture()
	b.Deliverables[1].FramesOCRd = 23
	if _, err := ScoreBenchmark(b, labels); err == nil {
		t.Fatal("no-gate run reported skipped failed OCR frame")
	}
}

func TestSeedFilenameIsNotSyntheticProvenance(t *testing.T) {
	data := []byte("file,language,phone:x,text:legal\nsynthetic-a.mp4,en-GB,2025550100,Terms apply\nsynthetic-b.mp4,en-GB,2025550101,Terms apply\nsynthetic-c.mp4,en-GB,2025550102,Terms apply\nsynthetic-d.mp4,en-GB,2025550103,Terms apply\n")
	_, labels, err := SeedMatrix(data, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range labels {
		if l.Synthetic {
			t.Fatal("filename invented synthetic provenance")
		}
	}
}

func TestBenchmarkRequiresBatchAndLabelSyntheticAgreement(t *testing.T) {
	b, labels := completeBenchFixture()
	b.Synthetic = false
	metrics, err := ScoreBenchmark(b, labels)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteBenchmark(&out, []BenchmarkMetrics{metrics}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "AI-authored synthetic campaign and delivery matrices only") {
		t.Fatal("label provenance overrode conflicting batch provenance")
	}
}

func TestCheckRejectsReportSymlinkBeforeOtherOperations(t *testing.T) {
	dir := cacheDir(t)
	if err := os.Symlink("/tmp/matrixqc-forbidden-report.html", filepath.Join(dir, "report.html")); err != nil {
		t.Fatal(err)
	}
	_, err := Check(context.Background(), CheckOptions{OutDir: dir, MatrixPath: "nonexistent", FilesDir: "nonexistent"})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("report destination was not validated before read/write: %v", err)
	}
}

func TestBatchAndReportRecordEffectivePassSettings(t *testing.T) {
	for _, opts := range []MatchOptions{{MinFrames: 1, Confidence: .01, PlaceholderTokens: []string{"DRAFT"}}, {}} {
		dir := cacheDir(t)
		batch, _ := Check(context.Background(), CheckOptions{OutDir: dir, MatrixPath: "nonexistent", Match: opts, Pipeline: PipelineOptions{NoGate: true, Threshold: 1}})
		data, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		settings, ok := payload["match"].(map[string]interface{})
		if !ok {
			t.Fatalf("missing pass settings: %s", data)
		}
		wantMin, wantConfidence := opts.MinFrames, opts.Confidence
		if wantMin == 0 {
			wantMin = 12
		}
		if wantConfidence == 0 {
			wantConfidence = .8
		}
		if settings["min_frames"] != float64(wantMin) || settings["confidence"] != wantConfidence {
			t.Fatalf("wrong effective settings %+v", settings)
		}
		var html bytes.Buffer
		if err := RenderReport(&html, batch, dir); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(html.String(), "Minimum consecutive frames") || !strings.Contains(html.String(), "confidence floor") || !strings.Contains(html.String(), "Gate: off") {
			t.Fatal("report omits effective settings")
		}
		if len(opts.PlaceholderTokens) > 0 && !strings.Contains(html.String(), "DRAFT") {
			t.Fatal("report hides campaign tokens")
		}
	}
}
