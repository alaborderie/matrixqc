package qc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type EngineMetadata struct {
	Engine        string `json:"engine"`
	EngineVersion string `json:"engine_version"`
	ModelSHA256   string `json:"model_sha256"`
}
type Sidecar struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	closeOnce sync.Once
	mu        sync.Mutex
}

func StartSidecar(ctx context.Context, cmd *exec.Cmd) (*Sidecar, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	return &Sidecar{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}, nil
}
func (r *Sidecar) Close() error {
	r.closeOnce.Do(func() {
		r.stdin.Close()
		if r.cmd.Process != nil {
			r.cmd.Process.Kill()
		}
		r.cmd.Wait()
	})
	return nil
}
func (r *Sidecar) Recognize(ctx context.Context, f Frame) ([]Box, error) {
	if f.W <= 0 || f.H <= 0 || len(f.Gray) != f.W*f.H {
		return nil, fmt.Errorf("invalid gray frame")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	type answer struct {
		boxes []Box
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		request := struct {
			ID   int    `json:"frame_id"`
			W    int    `json:"w"`
			H    int    `json:"h"`
			Gray []byte `json:"gray"`
		}{f.ID, f.W, f.H, f.Gray}
		if err := json.NewEncoder(r.stdin).Encode(request); err != nil {
			done <- answer{err: err}
			return
		}
		data, err := r.stdout.ReadBytes('\n')
		if err != nil {
			done <- answer{err: err}
			return
		}
		var response struct {
			ID    *int   `json:"frame_id"`
			Boxes []Box  `json:"boxes"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			done <- answer{err: fmt.Errorf("sidecar JSON: %w", err)}
			return
		}
		if response.Error != "" {
			done <- answer{err: fmt.Errorf("sidecar: %s", response.Error)}
			return
		}
		if response.ID == nil || *response.ID != f.ID || response.Boxes == nil {
			done <- answer{err: fmt.Errorf("sidecar invalid id or missing boxes")}
			return
		}
		if err := validateBoxes(response.Boxes); err != nil {
			done <- answer{err: err}
			return
		}
		done <- answer{boxes: response.Boxes}
	}()
	select {
	case a := <-done:
		if a.err != nil {
			r.Close()
		}
		return a.boxes, a.err
	case <-ctx.Done():
		r.Close()
		<-done
		return nil, fmt.Errorf("sidecar timeout/cancel: %w", ctx.Err())
	}
}

func validateBoxes(boxes []Box) error {
	for _, b := range boxes {
		if math.IsNaN(b.Conf) || math.IsInf(b.Conf, 0) || b.Conf < 0 || b.Conf > 1 {
			return fmt.Errorf("invalid OCR confidence")
		}
		for _, point := range b.Quad {
			for _, v := range point {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return fmt.Errorf("invalid OCR quad")
				}
			}
		}
	}
	return nil
}

// Group Tesseract words by line so an expectation such as "Terms apply" remains
// an actual OCR line, using the least-confident word rather than a flattering mean.
func ParseTSV(data []byte) ([]Box, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = '\t'
	r.FieldsPerRecord = 12
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil || header[0] != "level" {
		return nil, fmt.Errorf("invalid Tesseract TSV header")
	}
	boxes := []Box{}
	indices := map[string]int{}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Tesseract TSV: %w", err)
		}
		if row[0] != "5" || strings.TrimSpace(row[11]) == "" {
			continue
		}
		x, e1 := strconv.ParseFloat(row[6], 64)
		y, e2 := strconv.ParseFloat(row[7], 64)
		w, e3 := strconv.ParseFloat(row[8], 64)
		h, e4 := strconv.ParseFloat(row[9], 64)
		conf, e5 := strconv.ParseFloat(row[10], 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || w < 0 || h < 0 || conf < 0 || conf > 100 {
			return nil, fmt.Errorf("invalid Tesseract TSV word")
		}
		key := strings.Join(row[1:5], "/")
		idx, ok := indices[key]
		if !ok {
			indices[key] = len(boxes)
			boxes = append(boxes, Box{Quad: [4][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}, Text: row[11], Conf: conf / 100})
			continue
		}
		b := &boxes[idx]
		b.Text += " " + row[11]
		b.Conf = math.Min(b.Conf, conf/100)
		left, top := math.Min(b.Quad[0][0], x), math.Min(b.Quad[0][1], y)
		right, bottom := math.Max(b.Quad[2][0], x+w), math.Max(b.Quad[2][1], y+h)
		b.Quad = [4][2]float64{{left, top}, {right, top}, {right, bottom}, {left, bottom}}
	}
	return boxes, validateBoxes(boxes)
}

type Tesseract struct{}

func (r *Tesseract) Recognize(ctx context.Context, f Frame) ([]Box, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tesseract", "stdin", "stdout", "--psm", "11", "-l", "eng", "tsv")
	cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=2")
	pgm := fmt.Appendf(nil, "P5\n%d %d\n255\n", f.W, f.H)
	pgm = append(pgm, f.Gray...)
	cmd.Stdin = bytes.NewReader(pgm)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract: %w: %s", err, stderr.String())
	}
	return ParseTSV(data)
}

func OpenRecognizer(ctx context.Context, engine string) (Recognizer, EngineMetadata, func(), error) {
	closeFn := func() {}
	switch engine {
	case "tesseract":
		version, err := exec.CommandContext(ctx, "tesseract", "--version").Output()
		if err != nil {
			return nil, EngineMetadata{}, closeFn, err
		}
		langs, err := exec.CommandContext(ctx, "tesseract", "--list-langs").Output()
		if err != nil {
			return nil, EngineMetadata{}, closeFn, err
		}
		parts := strings.Split(string(langs), "\"")
		if len(parts) < 3 {
			return nil, EngineMetadata{}, closeFn, fmt.Errorf("cannot locate Tesseract models")
		}
		data, err := os.ReadFile(filepath.Join(parts[1], "eng.traineddata"))
		if err != nil {
			return nil, EngineMetadata{}, closeFn, err
		}
		return &Tesseract{}, EngineMetadata{engine, strings.Split(strings.TrimSpace(string(version)), "\n")[0], fmt.Sprintf("%x", sha256.Sum256(data))}, closeFn, nil
	case "rapidocr":
		script := os.Getenv("MATRIXQC_OCRD")
		if script == "" {
			script = "ocr/ocrd.py"
		}
		python := os.Getenv("MATRIXQC_PYTHON")
		if python == "" {
			python = "python3"
		}
		cmd := exec.CommandContext(ctx, python, "-u", script)
		r, err := StartSidecar(ctx, cmd)
		if err != nil {
			return nil, EngineMetadata{}, closeFn, err
		}
		// Metadata is read without importing/downloading any model, before streaming.
		data, err := exec.CommandContext(ctx, python, script, "--metadata").Output()
		if err != nil {
			r.Close()
			return nil, EngineMetadata{}, closeFn, fmt.Errorf("rapidocr metadata: %w", err)
		}
		var metadata EngineMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			r.Close()
			return nil, metadata, closeFn, err
		}
		return r, metadata, func() { r.Close() }, nil
	default:
		return nil, EngineMetadata{}, closeFn, fmt.Errorf("engine must be rapidocr or tesseract")
	}
}
