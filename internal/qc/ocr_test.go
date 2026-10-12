package qc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests a real process boundary, never mock call counts. A dead/malformed/stalled
// sidecar must not turn into a successful empty recognition result.
func TestSidecarErrors(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"exit", "import sys; sys.exit(2)"},
		{"invalid", "import sys; sys.stdin.readline(); print('not json',flush=True)"},
		{"wrong_id", "import sys; sys.stdin.readline(); print('{\"frame_id\":99,\"boxes\":[]}',flush=True)"},
		{"timeout", "import time; time.sleep(30)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			r, err := StartSidecar(ctx, exec.CommandContext(ctx, "python3", "-u", "-c", tc.script))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			_, err = r.Recognize(ctx, Frame{ID: 1, W: 2, H: 2, Gray: []byte{0, 0, 0, 0}})
			if err == nil {
				t.Fatal("sidecar failure returned success")
			}
		})
	}
}

func TestRealOCREngines(t *testing.T) {
	if testing.Short() {
		t.Skip("real OCR integration")
	}
	engines := []string{"tesseract"}
	if os.Getenv("MATRIXQC_OCR_INTEGRATION") == "1" {
		engines = append(engines, "rapidocr")
	}
	dir := cacheDir(t)
	if err := Synth(context.Background(), dir, SynthOptions{42, 8, 1, 640, 360, 24}); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(context.Background(), filepath.Join(dir, "synthetic-00-clean.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range engines {
		t.Run(engine, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rec, metadata, closeFn, err := OpenRecognizer(ctx, engine)
			if err != nil {
				t.Fatal(err)
			}
			defer closeFn()
			if metadata.EngineVersion == "" || len(metadata.ModelSHA256) != 64 {
				t.Fatalf("missing provenance: %+v", metadata)
			}
			found := false
			_, err = AnalyzeVideo(ctx, filepath.Join(dir, "synthetic-00-clean.mp4"), info, rec, PipelineOptions{Threshold: 255}, func(f Frame, b []Box, ocr bool) error {
				if f.ID != 0 {
					return nil
				}
				for _, box := range b {
					if strings.Contains(box.Text, "2025550100") {
						found = true
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatal("real OCR did not read synthetic phone")
			}
		})
	}
}
