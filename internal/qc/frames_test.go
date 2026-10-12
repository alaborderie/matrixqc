package qc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Catches comparing against the preceding decoded frame instead of last OCR frame,
// averaging the whole image, and losing small one-frame changes.
func TestTileGate(t *testing.T) {
	gate := NewGate(1)
	f := Frame{W: 32, H: 32, Gray: make([]byte, 1024)}
	if !gate.ShouldOCR(f) {
		t.Fatal("first frame skipped")
	}
	if gate.ShouldOCR(f) {
		t.Fatal("identical frame not skipped")
	}
	f.Gray = append([]byte(nil), f.Gray...)
	f.Gray[0] = 3
	if gate.ShouldOCR(f) {
		t.Fatal("below tile threshold should skip")
	}
	f.Gray[0] = 5
	if !gate.ShouldOCR(f) {
		t.Fatal("small changed tile missed")
	}
	blank := Frame{W: 32, H: 32, Gray: make([]byte, 1024)}
	if !gate.ShouldOCR(blank) {
		t.Fatal("one-frame return missed")
	}
}

type fixedRecognizer struct{ failure bool }

func (r fixedRecognizer) Recognize(ctx context.Context, frame Frame) ([]Box, error) {
	if r.failure {
		return nil, errors.New("OCR failed")
	}
	return []Box{{Text: "2025550100", Conf: .99, Quad: [4][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}}}, nil
}

func TestDecodeAndNoGate(t *testing.T) {
	if testing.Short() {
		t.Skip("ffmpeg integration")
	}
	dir := cacheDir(t)
	if err := Synth(context.Background(), dir, SynthOptions{42, 8, 1, 640, 360, 24}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "synthetic-00-clean.mp4")
	info, err := Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	visited := 0
	stats, err := AnalyzeVideo(context.Background(), path, info, fixedRecognizer{}, PipelineOptions{NoGate: true, Threshold: 1}, func(f Frame, b []Box, ocr bool) error {
		if f.ID != visited || len(f.Gray) != info.Width*info.Height || len(b) != 1 || !ocr {
			t.Fatalf("bad frame %d", f.ID)
		}
		visited++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.FramesDecoded != 24 || stats.FramesOCRd != 24 || visited != 24 {
		t.Fatalf("bad counts: %+v", stats)
	}
	_, err = AnalyzeVideo(context.Background(), path, info, fixedRecognizer{failure: true}, PipelineOptions{Threshold: 1}, func(Frame, []Box, bool) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "OCR failed") {
		t.Fatalf("swallowed failure: %v", err)
	}
	path = filepath.Join(dir, "synthetic-06-single_frame_foreign_phone.mp4")
	info, err = Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	injectionOCRd := false
	stats, err = AnalyzeVideo(context.Background(), path, info, fixedRecognizer{}, PipelineOptions{Threshold: 1}, func(f Frame, _ []Box, ocr bool) error {
		if f.ID == 12 {
			injectionOCRd = ocr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !injectionOCRd {
		t.Fatalf("single frame gated out: %+v", stats)
	}
}

func TestTesseractTSVLines(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n5\t1\t1\t1\t1\t1\t10\t20\t30\t10\t98\tTerms\n5\t1\t1\t1\t1\t2\t45\t20\t30\t10\t91\tapply\n"
	boxes, err := ParseTSV([]byte(tsv))
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 || boxes[0].Text != "Terms apply" || boxes[0].Conf != .91 || boxes[0].Quad[2] != [2]float64{75, 30} {
		t.Fatalf("bad OCR line: %+v", boxes)
	}
	if _, err := ParseTSV([]byte("garbage")); err == nil {
		t.Fatal("accepted broken OCR TSV")
	}
}
