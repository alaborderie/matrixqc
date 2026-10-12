package qc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Batch struct {
	ID              string `json:"id"`
	MatrixVersionID string `json:"matrix_version_id"`
	EngineMetadata
	Gate            PipelineOptions `json:"gate"`
	Match           MatchOptions    `json:"match"`
	StartedAt       time.Time       `json:"started_at"`
	FinishedAt      time.Time       `json:"finished_at"`
	CoreSeconds     float64         `json:"core_seconds"`
	AnalysedSeconds float64         `json:"analysed_seconds"`
	Synthetic       bool            `json:"synthetic"`
	Deliverables    []Deliverable   `json:"deliverables"`
}
type Deliverable struct {
	ID           string `json:"id"`
	BatchID      string `json:"batch_id"`
	VersionRowID string `json:"version_row_id,omitempty"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	MediaInfo
	FrameStats
	Outcome   string    `json:"outcome"`
	Findings  []Finding `json:"findings"`
	ChecksRun []string  `json:"checks_run"`
}
type CheckOptions struct {
	MatrixPath, FilesDir, OutDir, Engine string
	Match                                MatchOptions
	Pipeline                             PipelineOptions
}

func cpuSeconds() float64 {
	var self, children syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &self)
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children)
	return float64(self.Utime.Sec+self.Stime.Sec+children.Utime.Sec+children.Stime.Sec) + float64(self.Utime.Usec+self.Stime.Usec+children.Utime.Usec+children.Stime.Usec)/1e6
}

func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func SourceSynthetic(dir string) bool {
	if dir == "" {
		return false
	}
	var provenance struct {
		Synthetic bool `json:"synthetic"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "SYNTHETIC.json"))
	return err == nil && json.Unmarshal(data, &provenance) == nil && provenance.Synthetic
}

func Check(ctx context.Context, opts CheckOptions) (Batch, error) {
	opts.Match = effectiveMatchOptions(opts.Match)
	batch := Batch{StartedAt: time.Now().UTC(), Gate: opts.Pipeline, Match: opts.Match, Deliverables: []Deliverable{}}
	cpuStart := cpuSeconds()
	if err := ValidateOutput(opts.OutDir); err != nil {
		return batch, err
	}
	if err := ValidateOutput(filepath.Join(opts.OutDir, "report.html")); err != nil {
		return batch, err
	}
	data, err := os.ReadFile(opts.MatrixPath)
	if err != nil {
		return batch, err
	}
	matrix, err := ParseMatrix(data)
	if err != nil {
		return batch, err
	}
	batch.ID = fmt.Sprintf("batch-%s-%d", matrix.SHA256[:12], batch.StartedAt.UnixNano())
	batch.MatrixVersionID = matrix.SHA256
	batch.Synthetic = SourceSynthetic(opts.FilesDir)
	entries, err := os.ReadDir(opts.FilesDir)
	if err != nil {
		return batch, err
	}
	files := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".mov", ".mp4", ".mxf", ".mkv", ".avi", ".webm", ".m4v":
			files = append(files, entry.Name())
		}
	}
	joined, problems := Join(matrix, files)
	if err := os.MkdirAll(opts.OutDir, 0755); err != nil {
		return batch, err
	}
	if err := os.MkdirAll(filepath.Join(opts.OutDir, "evidence"), 0755); err != nil {
		return batch, err
	}
	rec, metadata, closeFn, err := OpenRecognizer(ctx, opts.Engine)
	if err != nil {
		return batch, err
	}
	batch.EngineMetadata = metadata
	defer closeFn()
	blockedRows := map[string]bool{}
	blockedFiles := map[string]bool{}
	problemDeliverables := map[string]int{}
	for _, p := range problems {
		if p.RowID != "" {
			blockedRows[p.RowID] = true
		}
		if p.Path != "" {
			blockedFiles[p.Path] = true
		}
		key := p.Path
		if key == "" {
			key = p.RowID
		}
		idx, ok := problemDeliverables[key]
		if !ok {
			idx = len(batch.Deliverables)
			problemDeliverables[key] = idx
			batch.Deliverables = append(batch.Deliverables, Deliverable{ID: fmt.Sprintf("deliverable-%d", idx+1), BatchID: batch.ID, Path: key, VersionRowID: p.RowID, Outcome: "error", ChecksRun: []string{"C0"}, Findings: []Finding{}})
		}
		d := &batch.Deliverables[idx]
		d.Findings = append(d.Findings, Finding{ID: fmt.Sprintf("%s-finding-%d", d.ID, len(d.Findings)+1), DeliverableID: d.ID, Check: "C0", Outcome: "error", Message: p.Message})
	}
	for _, j := range joined {
		if blockedRows[j.Row.ID] || blockedFiles[j.Path] {
			continue
		}
		d := Deliverable{ID: fmt.Sprintf("deliverable-%d", len(batch.Deliverables)+1), BatchID: batch.ID, VersionRowID: j.Row.ID, Path: j.Path, ChecksRun: []string{"C0"}, Findings: []Finding{}, Outcome: "error"}
		path := filepath.Join(opts.FilesDir, j.Path)
		hash, err := FileSHA256(path)
		if err == nil {
			d.SHA256 = hash
			d.MediaInfo, err = Probe(ctx, path)
		}
		if err != nil {
			d.Findings = append(d.Findings, Finding{ID: d.ID + "-decode", DeliverableID: d.ID, Check: "C4", Outcome: "error", Message: err.Error()})
			batch.Deliverables = append(batch.Deliverables, d)
			continue
		}
		if _, err := Timecode(0, d.FPS, d.StartTimecode); err != nil {
			d.Findings = append(d.Findings, Finding{ID: d.ID + "-timecode", DeliverableID: d.ID, Check: "C4", Outcome: "error", Message: err.Error()})
			batch.Deliverables = append(batch.Deliverables, d)
			continue
		}
		matcher := NewMatcher(matrix, j.Row, opts.Match)
		last := Frame{}
		evidenceCount := 0
		writeFindings := func(frame Frame) error {
			for i := evidenceCount; i < len(matcher.findings); i++ {
				finding := &matcher.findings[i]
				name := fmt.Sprintf("%s-%s.png", d.ID, finding.ID)
				if err := WriteEvidence(filepath.Join(opts.OutDir, "evidence", name), frame, Box{Quad: finding.Quad}); err != nil {
					return err
				}
				finding.EvidencePath = filepath.Join("evidence", name)
				finding.EvidenceFrame = frame.ID
			}
			evidenceCount = len(matcher.findings)
			return nil
		}
		d.FrameStats, err = AnalyzeVideo(ctx, path, d.MediaInfo, rec, opts.Pipeline, func(frame Frame, boxes []Box, _ bool) error {
			last = frame
			matcher.Observe(frame.ID, boxes)
			return writeFindings(frame)
		})
		d.ChecksRun = []string{"C0", "C1", "C2", "C3", "C4"}
		if err != nil {
			d.Outcome = "error"
			d.Findings = append(d.Findings, Finding{ID: d.ID + "-ocr-error", DeliverableID: d.ID, Check: "C4", Outcome: "error", FirstFrame: max(0, d.FramesDecoded-1), LastFrame: max(0, d.FramesDecoded-1), Message: err.Error()})
		} else {
			d.Outcome, _ = matcher.Finish(d.FramesDecoded)
			if err := writeFindings(last); err != nil {
				return batch, err
			}
			d.Findings = matcher.findings
			for i := range d.Findings {
				finding := &d.Findings[i]
				finding.ID = d.ID + "-" + finding.ID
				finding.DeliverableID = d.ID
				finding.Timecode, _ = Timecode(finding.FirstFrame, d.FPS, d.StartTimecode)
			}
		}
		batch.AnalysedSeconds += float64(d.FramesDecoded) / d.FPS
		batch.Deliverables = append(batch.Deliverables, d)
	}
	// Include the persistent OCR child's CPU time, not just Go orchestration.
	closeFn()
	batch.FinishedAt = time.Now().UTC()
	batch.CoreSeconds = cpuSeconds() - cpuStart
	if err := WriteJSON(filepath.Join(opts.OutDir, "results.json"), batch); err != nil {
		return batch, err
	}
	var html strings.Builder
	if err := RenderReport(&html, batch, opts.OutDir); err != nil {
		return batch, err
	}
	if err := ValidateOutput(filepath.Join(opts.OutDir, "report.html")); err != nil {
		return batch, err
	}
	if err := os.WriteFile(filepath.Join(opts.OutDir, "report.html"), []byte(html.String()), 0644); err != nil {
		return batch, err
	}
	return batch, nil
}

// SMPTE constant-rate timecode, including 29.97/59.94 drop-frame notation.
func Timecode(frame int, fps float64, start string) (string, error) {
	drop := strings.Contains(start, ";")
	var h, m, s, f int
	if _, err := fmt.Sscanf(strings.ReplaceAll(start, ";", ":"), "%d:%d:%d:%d", &h, &m, &s, &f); err != nil {
		return "", fmt.Errorf("unsupported source timecode %q", start)
	}
	nominal := int(math.Round(fps))
	if frame < 0 || nominal < 1 || nominal > 120 || h < 0 || h >= 24 || m < 0 || m >= 60 || s < 0 || s >= 60 || f < 0 || f >= nominal {
		return "", fmt.Errorf("invalid timecode or frame rate")
	}
	base := (h*3600+m*60+s)*nominal + f
	separator := ":"
	if drop {
		if math.Abs(fps-29.97) > .01 && math.Abs(fps-59.94) > .01 {
			return "", fmt.Errorf("drop-frame requires 29.97 or 59.94 fps")
		}
		dropped := nominal / 15
		minutes := h*60 + m
		if m%10 != 0 && s == 0 && f < dropped {
			return "", fmt.Errorf("invalid dropped timecode number")
		}
		base -= dropped * (minutes - minutes/10)
		actual := base + frame
		tenMinutes := nominal*600 - dropped*9
		minute := nominal*60 - dropped
		tens, remainder := actual/tenMinutes, actual%tenMinutes
		base = actual + dropped*9*tens
		if remainder >= dropped {
			base += dropped * ((remainder - dropped) / minute)
		}
		separator = ";"
	} else {
		base += frame
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%02d", base/(nominal*3600)%24, base/(nominal*60)%60, base/nominal%60, separator, base%nominal), nil
}

func WriteEvidence(path string, f Frame, b Box) error {
	if err := ValidateOutput(path); err != nil {
		return err
	}
	if f.W <= 0 || f.H <= 0 || len(f.Gray) != f.W*f.H {
		return fmt.Errorf("invalid evidence frame")
	}
	rect := image.Rect(0, 0, f.W, f.H)
	if b.Quad[2][0] > b.Quad[0][0] && b.Quad[2][1] > b.Quad[0][1] {
		crop := image.Rect(int(b.Quad[0][0])-8, int(b.Quad[0][1])-8, int(b.Quad[2][0])+8, int(b.Quad[2][1])+8).Intersect(rect)
		if !crop.Empty() {
			rect = crop
		}
	}
	img := &image.Gray{Pix: f.Gray, Stride: f.W, Rect: image.Rect(0, 0, f.W, f.H)}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	err = png.Encode(out, img.SubImage(rect))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
