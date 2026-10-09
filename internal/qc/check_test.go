package qc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckErrorCannotPass(t *testing.T) {
	if testing.Short() {
		t.Skip("real OCR/ffprobe integration runs in Docker full suite")
	}
	dir := cacheDir(t)
	matrix := filepath.Join(dir, "synthetic-matrix.csv")
	if err := os.WriteFile(matrix, []byte("file,version_id,language,text:legal\nsynthetic-broken.mp4,synthetic,en-GB,Terms apply\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "synthetic-broken.mp4"), []byte("not a video"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "report")
	batch, err := Check(context.Background(), CheckOptions{MatrixPath: matrix, FilesDir: dir, OutDir: out, Engine: "tesseract", Match: MatchOptions{12, .8, nil}, Pipeline: PipelineOptions{Threshold: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Deliverables) != 1 || batch.Deliverables[0].Outcome != "error" {
		t.Fatalf("unsupported video passed: %+v", batch)
	}
	if _, err := os.Stat(filepath.Join(out, "results.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSyntheticCleanAndMissingTextReadable(t *testing.T) {
	if testing.Short() {
		t.Skip("real OCR integration")
	}
	dir := cacheDir(t)
	if err := Synth(context.Background(), dir, SynthOptions{42, 8, 1, 854, 480, 24}); err != nil {
		t.Fatal(err)
	}
	batch, err := Check(context.Background(), CheckOptions{MatrixPath: filepath.Join(dir, "synthetic-matrix.csv"), FilesDir: dir, OutDir: filepath.Join(dir, "report"), Engine: "tesseract", Match: MatchOptions{12, .8, nil}, Pipeline: PipelineOptions{Threshold: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range batch.Deliverables {
		if strings.Contains(d.Path, "clean") && d.Outcome != "pass" {
			t.Errorf("clean fixture not readable: %s %s %+v", d.Path, d.Outcome, d.Findings)
		}
		if strings.Contains(d.Path, "missing") && !hasCheck(d.Findings, "C1") {
			t.Errorf("missing fixture has unrelated OCR uncertainty: %s %+v", d.Path, d.Findings)
		}
	}
}

func TestTimecode(t *testing.T) {
	for _, tc := range []struct {
		frame       int
		fps         float64
		start, want string
	}{
		{25, 25, "00:00:00:00", "00:00:01:00"},
		{150, 24, "01:00:00:00", "01:00:06:06"},
		{17982, 29.97, "00:00:00;00", "00:10:00;00"},
	} {
		got, err := Timecode(tc.frame, tc.fps, tc.start)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
	if _, err := Timecode(1, 24, "bad"); err == nil {
		t.Fatal("accepted invalid source timecode")
	}
}

func TestReportOfflineAndEscaped(t *testing.T) {
	batch := Batch{ID: "synthetic-batch", Synthetic: true, Deliverables: []Deliverable{{ID: "d1", Path: "<script>alert(1)</script>", Outcome: "needs_review", ChecksRun: []string{"C0", "C1", "C2", "C3", "C4"}, Findings: []Finding{{ID: "f1", Check: "C4", Message: "Near match", ObservedText: "<img src=https://evil.invalid>", FirstFrame: 15, LastFrame: 15, Timecode: "00:00:00:15", EvidencePath: "evidence/frame.png"}}}}}
	dir := cacheDir(t)
	if err := os.Mkdir(filepath.Join(dir, "evidence"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteEvidence(filepath.Join(dir, "evidence/frame.png"), Frame{ID: 15, W: 2, H: 2, Gray: []byte{0, 100, 100, 0}}, Box{}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := RenderReport(&b, batch, dir); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, s := range []string{"Not checked", "not a certificate", "Synthetic", "00:00:00:15", "data:image/png;base64,", "C4"} {
		if !strings.Contains(html, s) {
			t.Fatalf("report missing %s", s)
		}
	}
	if strings.Contains(html, "<script>alert(1)") || strings.Contains(html, "<img src=https://evil") || strings.Contains(html, "src=\"https://") {
		t.Fatal("unsafe/non-offline report")
	}
}

func TestBenchmarkMetrics(t *testing.T) {
	injected := 15
	labels := []Label{
		{DeliverablePath: "synthetic-clean-a", ExpectedOutcome: "pass", Synthetic: true},
		{DeliverablePath: "synthetic-clean-b", ExpectedOutcome: "pass", Synthetic: true},
		{DeliverablePath: "synthetic-bad", ExpectedOutcome: "fail", ExpectedChecks: []string{"C2"}, Critical: true, Synthetic: true, InjectionFrame: &injected},
	}
	batch := Batch{EngineMetadata: EngineMetadata{Engine: "tesseract"}, CoreSeconds: 10, AnalysedSeconds: 30, StartedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 9, 0, 0, 5, 0, time.UTC), Deliverables: []Deliverable{
		{Path: "synthetic-clean-a", Outcome: "pass", MediaInfo: MediaInfo{FrameCount: 100}, FrameStats: FrameStats{FramesDecoded: 100, FramesOCRd: 10}},
		{Path: "synthetic-clean-b", Outcome: "needs_review", MediaInfo: MediaInfo{FrameCount: 100}, FrameStats: FrameStats{FramesDecoded: 100, FramesOCRd: 10}},
		{Path: "synthetic-bad", Outcome: "pass", MediaInfo: MediaInfo{FrameCount: 100}, FrameStats: FrameStats{FramesDecoded: 100, FramesOCRd: 10, OCRFrameIDs: []int{0}}},
	}}
	metrics, err := ScoreBenchmark(batch, labels)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.SilentPassCritical != 1 || metrics.FalseAlarmRate != .5 || metrics.NeedsReviewRate < .333 || metrics.NeedsReviewRate > .334 || metrics.GateMisses != 1 || metrics.GateSkipRate != .9 || metrics.CoreSecondsPerMinute != 20 || metrics.WallSecondsPerMinute != 10 {
		t.Fatalf("wrong metrics: %+v", metrics)
	}
	if _, err := ScoreBenchmark(batch, append(labels, Label{DeliverablePath: "absent", ExpectedOutcome: "fail"})); err == nil {
		t.Fatal("missing result made benchmark look better")
	}
	var report bytes.Buffer
	if err := WriteBenchmark(&report, []BenchmarkMetrics{metrics}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "20%") || !strings.Contains(report.String(), "unvalidated") || !strings.Contains(report.String(), "silent_pass_critical") {
		t.Fatal("benchmark hid uncertainty")
	}
}

func TestBenchmarkCannotMislabelNonSyntheticRuns(t *testing.T) {
	var report bytes.Buffer
	if err := WriteBenchmark(&report, []BenchmarkMetrics{{Engine: "tesseract", Synthetic: false}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.String(), "AI-authored synthetic campaign and delivery matrices only") {
		t.Fatal("non-synthetic run falsely labelled synthetic")
	}
}
