package qc

import (
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type Expectation struct {
	ID              string `json:"id"`
	VersionRowID    string `json:"version_row_id"`
	Kind            string `json:"kind"`
	Label           string `json:"label"`
	RawValue        string `json:"raw_value"`
	NormalisedValue string `json:"normalised_value"`
	Polarity        string `json:"polarity"`
}
type VersionRow struct {
	ID              string        `json:"id"`
	MatrixVersionID string        `json:"matrix_version_id"`
	RowNumber       int           `json:"row_number"`
	FilePattern     string        `json:"file_pattern"`
	VersionID       string        `json:"version_id"`
	Language        string        `json:"language"`
	Expectations    []Expectation `json:"expectations"`
}
type Matrix struct {
	SHA256 string       `json:"sha256"`
	Rows   []VersionRow `json:"rows"`
}
type JoinProblem struct {
	Check   string `json:"check"`
	Path    string `json:"path"`
	RowID   string `json:"row_id"`
	Message string `json:"message"`
}
type Joined struct {
	Path string
	Row  VersionRow
}

func Normalize(kind, value string) string {
	value = strings.TrimSpace(value)
	switch kind {
	case "phone":
		var b strings.Builder
		if strings.HasPrefix(value, "+") {
			b.WriteByte('+')
		}
		for _, r := range value {
			if r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	case "url":
		value = strings.ToLower(value)
		value = strings.TrimPrefix(strings.TrimPrefix(value, "https://"), "http://")
		return strings.TrimRight(value, "/")
	case "text":
		value = cases.Fold().String(norm.NFKC.String(value))
		value = strings.Map(func(r rune) rune {
			if unicode.IsPunct(r) || unicode.IsSpace(r) {
				return ' '
			}
			return r
		}, value)
		return strings.Join(strings.Fields(value), " ")
	default:
		return ""
	}
}

func ParseMatrix(data []byte) (Matrix, error) {
	m := Matrix{SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return m, fmt.Errorf("row 1 column file: header required: %w", err)
	}
	seen := map[string]bool{}
	for _, h := range header {
		if seen[h] {
			return m, fmt.Errorf("row 1 column %s: duplicate header", h)
		}
		seen[h] = true
		if h == "file" || h == "version_id" || h == "language" {
			continue
		}
		kind, label, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(label) == "" || (kind != "phone" && kind != "url" && kind != "text" && kind != "forbid") {
			return m, fmt.Errorf("row 1 column %s: unknown column or empty label", h)
		}
	}
	if !seen["file"] {
		return m, fmt.Errorf("row 1 column file: required")
	}
	versions := map[string]bool{}
	for n := 2; ; n++ {
		cells, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("row %d column CSV: %w", n, err)
		}
		if len(cells) != len(header) {
			return m, fmt.Errorf("row %d column CSV: expected %d cells, got %d", n, len(header), len(cells))
		}
		row := VersionRow{ID: fmt.Sprintf("row-%d", n), MatrixVersionID: m.SHA256, RowNumber: n, Expectations: []Expectation{}}
		for i, cell := range cells {
			h := header[i]
			bad := func(msg string) (Matrix, error) { return m, fmt.Errorf("row %d column %s: %s", n, h, msg) }
			if !utf8.ValidString(cell) {
				return bad("invalid UTF-8")
			}
			cell = strings.TrimSpace(cell)
			switch h {
			case "file":
				if cell == "" {
					return bad("required")
				}
				if filepath.Base(cell) != cell || cell == "." || cell == ".." || strings.Contains(cell, "\\") {
					return bad("only a filename or basename glob is allowed")
				}
				if _, err := filepath.Match(cell, ""); err != nil {
					return bad("invalid glob")
				}
				row.FilePattern = cell
			case "version_id":
				if cell != "" && versions[cell] {
					return bad("duplicate version id")
				}
				versions[cell] = true
				row.VersionID = cell
			case "language":
				row.Language = cell
			default:
				if cell == "" {
					continue
				}
				kind, label, _ := strings.Cut(h, ":")
				polarity := "require"
				if kind == "forbid" {
					kind = "text"
					polarity = "forbid"
				}
				nv := Normalize(kind, cell)
				if kind == "phone" {
					digits := strings.TrimPrefix(nv, "+")
					if len(digits) < 7 || len(digits) > 15 {
						return bad("phone must contain 7–15 digits")
					}
				}
				if kind == "url" {
					u, err := url.Parse("https://" + nv)
					if err != nil || u.Hostname() == "" || !strings.Contains(u.Hostname(), ".") || strings.ContainsAny(nv, " \t\r\n") {
						return bad("URL host required")
					}
				}
				if nv == "" {
					return bad("empty normalised value")
				}
				row.Expectations = append(row.Expectations, Expectation{ID: fmt.Sprintf("%s-col-%d", row.ID, i+1), VersionRowID: row.ID, Kind: kind, Label: label, RawValue: cell, NormalisedValue: nv, Polarity: polarity})
			}
		}
		m.Rows = append(m.Rows, row)
	}
	if len(m.Rows) == 0 {
		return m, fmt.Errorf("row 2 column file: at least one row required")
	}
	return m, nil
}

func Join(m Matrix, files []string) ([]Joined, []JoinProblem) {
	joined := []Joined{}
	problems := []JoinProblem{}
	counts := map[string]int{}
	seen := map[string]bool{}
	add := func(path, row, msg string) {
		problems = append(problems, JoinProblem{Check: "C0", Path: path, RowID: row, Message: msg})
	}
	for _, file := range files {
		if seen[file] {
			add(file, "", "duplicate file")
			continue
		}
		seen[file] = true
		matches := []VersionRow{}
		for _, row := range m.Rows {
			ok, _ := filepath.Match(row.FilePattern, filepath.Base(file))
			if ok {
				matches = append(matches, row)
				counts[row.ID]++
			}
		}
		switch len(matches) {
		case 0:
			add(file, "", "file has no row")
		case 1:
			joined = append(joined, Joined{Path: file, Row: matches[0]})
		default:
			add(file, "", "file matches multiple rows")
		}
	}
	for _, row := range m.Rows {
		if counts[row.ID] == 0 {
			add("", row.ID, "row has no file")
		}
		if counts[row.ID] > 1 {
			add("", row.ID, "row matches multiple files")
		}
	}
	return joined, problems
}
