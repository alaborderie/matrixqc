package qc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
)

type Frame struct {
	ID, W, H int
	Gray     []byte
}
type Box struct {
	Quad [4][2]float64 `json:"quad"`
	Text string        `json:"text"`
	Conf float64       `json:"conf"`
}
type Recognizer interface {
	Recognize(context.Context, Frame) ([]Box, error)
}
type PipelineOptions struct {
	NoGate    bool    `json:"no_gate"`
	Threshold float64 `json:"threshold"`
}
type FrameStats struct {
	FramesDecoded int   `json:"frames_decoded"`
	FramesOCRd    int   `json:"frames_ocrd"`
	OCRFrameIDs   []int `json:"ocr_frame_ids"`
}
type Gate struct {
	threshold float64
	previous  []byte
	w, h      int
}

func NewGate(threshold float64) *Gate { return &Gate{threshold: threshold} }
func (g *Gate) ShouldOCR(f Frame) bool {
	changed := len(g.previous) != len(f.Gray) || g.w != f.W || g.h != f.H
	if !changed {
		for ty := 0; ty < 16 && !changed; ty++ {
			for tx := 0; tx < 16 && !changed; tx++ {
				sum, count := 0, 0
				for y := ty * f.H / 16; y < (ty+1)*f.H/16; y++ {
					for x := tx * f.W / 16; x < (tx+1)*f.W/16; x++ {
						d := int(f.Gray[y*f.W+x]) - int(g.previous[y*f.W+x])
						if d < 0 {
							d = -d
						}
						sum += d
						count++
					}
				}
				if count > 0 && float64(sum)/float64(count) > g.threshold {
					changed = true
				}
			}
		}
	}
	if changed {
		g.previous = append(g.previous[:0], f.Gray...)
		g.w = f.W
		g.h = f.H
	}
	return changed
}

func AnalyzeVideo(ctx context.Context, path string, info MediaInfo, rec Recognizer, opts PipelineOptions, visit func(Frame, []Box, bool) error) (FrameStats, error) {
	stats := FrameStats{OCRFrameIDs: []int{}}
	if info.Width <= 0 || info.Height <= 0 || int64(info.Width)*int64(info.Height) > 64*1024*1024 || opts.Threshold < 0 || math.IsNaN(opts.Threshold) || math.IsInf(opts.Threshold, 0) {
		return stats, fmt.Errorf("invalid dimensions or gate threshold")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "2", "-i", path, "-map", "0:v:0", "-an", "-sn", "-dn", "-fps_mode", "passthrough", "-filter_threads", "1", "-f", "rawvideo", "-pix_fmt", "gray", "pipe:1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return stats, err
	}
	if err := cmd.Start(); err != nil {
		return stats, err
	}
	gate := NewGate(opts.Threshold)
	buf := make([]byte, info.Width*info.Height)
	var boxes []Box
	var pipelineErr error
	for id := 0; ; id++ {
		_, err := io.ReadFull(stdout, buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			pipelineErr = fmt.Errorf("decode frame %d: %w", id, err)
			break
		}
		f := Frame{ID: id, W: info.Width, H: info.Height, Gray: buf}
		stats.FramesDecoded++
		ocr := opts.NoGate || gate.ShouldOCR(f)
		if ocr {
			boxes, err = rec.Recognize(ctx, f)
			if err != nil {
				pipelineErr = fmt.Errorf("OCR frame %d: %w", id, err)
				break
			}
			stats.FramesOCRd++
			stats.OCRFrameIDs = append(stats.OCRFrameIDs, id)
		}
		if err := visit(f, boxes, ocr); err != nil {
			pipelineErr = err
			break
		}
	}
	if pipelineErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if pipelineErr != nil {
		return stats, pipelineErr
	}
	if waitErr != nil {
		return stats, fmt.Errorf("ffmpeg decode: %w: %s", waitErr, stderr.String())
	}
	if stats.FramesDecoded != info.FrameCount {
		return stats, fmt.Errorf("decoded %d frames, ffprobe reported %d", stats.FramesDecoded, info.FrameCount)
	}
	return stats, nil
}
