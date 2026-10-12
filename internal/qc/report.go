package qc

import (
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/report.html assets/style.css
var reportAssets embed.FS

func StyleCSS() string { data, _ := reportAssets.ReadFile("assets/style.css"); return string(data) }

type reportFinding struct {
	Finding
	Image template.URL
}
type reportDeliverable struct {
	Deliverable
	Evidence []reportFinding
}
type reportView struct {
	Batch  Batch
	CSS    template.CSS
	Rows   []reportDeliverable
	Counts map[string]int
}

func RenderReport(w io.Writer, batch Batch, dir string) error {
	view := reportView{Batch: batch, CSS: template.CSS(StyleCSS()), Counts: map[string]int{}, Rows: []reportDeliverable{}}
	for _, d := range batch.Deliverables {
		view.Counts[d.Outcome]++
		rd := reportDeliverable{Deliverable: d}
		for _, f := range d.Findings {
			rf := reportFinding{Finding: f}
			if f.EvidencePath != "" {
				clean := filepath.Clean(f.EvidencePath)
				if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
					return fmt.Errorf("invalid relative evidence path")
				}
				data, err := os.ReadFile(filepath.Join(dir, clean))
				if err != nil {
					return err
				}
				rf.Image = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(data))
			}
			rd.Evidence = append(rd.Evidence, rf)
		}
		view.Rows = append(view.Rows, rd)
	}
	sort.SliceStable(view.Rows, func(i, j int) bool { return view.Rows[i].Outcome != "pass" && view.Rows[j].Outcome == "pass" })
	funcs := template.FuncMap{
		"class": func(outcome string) string {
			if outcome == "needs_review" {
				return "review"
			}
			if outcome == "error" {
				return "error"
			}
			return outcome
		},
		"label": func(outcome string) string {
			if outcome == "needs_review" {
				return "Needs review"
			}
			return strings.ToUpper(outcome)
		},
		"icon": func(outcome string) string {
			switch outcome {
			case "pass":
				return "i-ok"
			case "needs_review":
				return "i-eye"
			default:
				return "i-x"
			}
		},
		"join": strings.Join,
	}
	t, err := template.New("report.html").Funcs(funcs).ParseFS(reportAssets, "assets/report.html")
	if err != nil {
		return err
	}
	return t.Execute(w, view)
}
