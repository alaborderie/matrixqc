package qc

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func SeedMatrix(data []byte, sourceSynthetic bool) ([]byte, []Label, error) {
	m, err := ParseMatrix(data)
	if err != nil {
		return nil, nil, err
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		return nil, nil, err
	}
	phone, legal := -1, -1
	for i, h := range records[0] {
		if phone < 0 && strings.HasPrefix(h, "phone:") {
			phone = i
		}
		if legal < 0 && strings.HasPrefix(h, "text:") {
			legal = i
		}
	}
	if len(m.Rows) < 4 || phone < 0 || legal < 0 {
		return nil, nil, fmt.Errorf("seed needs four rows, a phone column and required legal text column")
	}
	for i := 1; i <= 4; i++ {
		if records[i][phone] == "" || records[i][legal] == "" {
			return nil, nil, fmt.Errorf("row %d: seed requires existing phone and legal expectations", i+1)
		}
	}
	// The critical phone labels assume each original selected value is owned only
	// by its original row. Shared values make a swap a no-op or a legitimate read.
	for _, selected := range []int{0, 1, 3} {
		value := Normalize("phone", records[selected+1][phone])
		for i, row := range m.Rows {
			if i == selected {
				continue
			}
			for _, e := range row.Expectations {
				if e.Kind == "phone" && e.Polarity == "require" && e.NormalisedValue == value {
					return nil, nil, fmt.Errorf("row %d: selected seed phone is shared with row %d; critical labels would be invalid", selected+2, i+2)
				}
			}
		}
	}
	records[1][phone], records[2][phone] = records[2][phone], records[1][phone]
	// Empty matrix cells disable expectations. Require a known absent legal string
	// instead, or the supposed missing-text seed would silently disable C1.
	records[3][legal] = "MATRIXQC SEEDED REQUIRED LEGAL"
	phoneValue := Normalize("phone", records[4][phone])
	last := phoneValue[len(phoneValue)-1]
	records[4][phone] = phoneValue[:len(phoneValue)-1] + string('0'+(last-'0'+1)%10)
	labels := []Label{}
	for i, row := range m.Rows {
		l := Label{DeliverablePath: row.FilePattern, ExpectedOutcome: "pass", ExpectedChecks: []string{}, Synthetic: sourceSynthetic}
		switch i {
		case 0, 1:
			l.ExpectedOutcome = "fail"
			l.ExpectedChecks = []string{"C2"}
			l.SeedKind = "swapped_phone"
			l.Critical = true
		case 2:
			l.ExpectedOutcome = "fail"
			l.ExpectedChecks = []string{"C1"}
			l.SeedKind = "missing_legal"
			l.Critical = true
		case 3:
			l.ExpectedOutcome = "needs_review"
			l.ExpectedChecks = []string{"C4"}
			l.SeedKind = "one_digit_mutation"
			l.Critical = true
		}
		labels = append(labels, l)
	}
	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	if err := writer.WriteAll(records); err != nil {
		return nil, nil, err
	}
	return out.Bytes(), labels, nil
}
func Splice(ctx context.Context, a, b, out string, frame int) error {
	if err := ValidateOutput(out); err != nil {
		return err
	}
	if strings.ToLower(filepath.Ext(out)) != ".mkv" {
		return fmt.Errorf("lossless splice output must be .mkv")
	}
	ai, err := Probe(ctx, a)
	if err != nil {
		return err
	}
	bi, err := Probe(ctx, b)
	if err != nil {
		return err
	}
	if ai.Width != bi.Width || ai.Height != bi.Height || ai.FPS != bi.FPS || frame < 0 || frame >= ai.FrameCount || frame >= bi.FrameCount {
		return fmt.Errorf("splice requires compatible dimensions/rates and a frame in both files")
	}
	filter := fmt.Sprintf("[1:v]select='eq(n,%d)',setpts=PTS-STARTPTS[replacement];[0:v][replacement]overlay=enable='eq(n,%d)':eof_action=repeat:format=yuv420[out]", frame, frame)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-n", "-i", a, "-i", b, "-filter_complex_threads", "1", "-filter_complex", filter, "-map", "[out]", "-map", "0:a?", "-c:v", "ffv1", "-c:a", "copy", "-pix_fmt", "yuv420p", "-threads", "2", "-fps_mode", "passthrough", "-metadata", "comment=MatrixQC injected-frame test; not original customer evidence", out)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("splice: %w: %s", err, output)
	}
	return nil
}
