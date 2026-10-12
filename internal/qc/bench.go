package qc

import (
	"fmt"
	"io"
	"path/filepath"
)

type BenchmarkMetrics struct {
	Engine                                                                                    string
	Synthetic                                                                                 bool
	LabelCount, CleanCount, ErrorCount                                                        int
	SilentPassCritical, GateMisses                                                            int
	FalseAlarmRate, NeedsReviewRate, GateSkipRate, CoreSecondsPerMinute, WallSecondsPerMinute float64
}

func ScoreBenchmark(b Batch, labels []Label) (BenchmarkMetrics, error) {
	metrics := BenchmarkMetrics{Engine: b.Engine, Synthetic: b.Synthetic}
	results := map[string]Deliverable{}
	for _, d := range b.Deliverables {
		key := filepath.Base(d.Path)
		if _, ok := results[key]; ok {
			return metrics, fmt.Errorf("duplicate benchmark result %s", key)
		}
		results[key] = d
	}
	seen := map[string]bool{}
	clean, falseAlarms, reviews, decoded, ocr := 0, 0, 0, 0, 0
	for _, l := range labels {
		key := filepath.Base(l.DeliverablePath)
		if seen[key] {
			return metrics, fmt.Errorf("duplicate label %s", key)
		}
		seen[key] = true
		d, ok := results[key]
		if !ok {
			return metrics, fmt.Errorf("missing benchmark result %s", key)
		}
		metrics.Synthetic = metrics.Synthetic && l.Synthetic
		if d.Outcome == "error" {
			if l.ExpectedOutcome != "error" || !hasString(l.ExpectedChecks, "C0") || !hasString(d.ChecksRun, "C0") || d.FramesDecoded != 0 {
				return metrics, fmt.Errorf("incomplete benchmark: unexpected analysis error for %s; accuracy/gate rates not published", key)
			}
		} else if (d.Outcome != "pass" && d.Outcome != "fail" && d.Outcome != "needs_review") || d.FrameCount <= 0 || d.FramesDecoded != d.FrameCount || d.FramesOCRd <= 0 || d.FramesOCRd > d.FramesDecoded || (b.Gate.NoGate && d.FramesOCRd != d.FramesDecoded) {
			return metrics, fmt.Errorf("incomplete benchmark: invalid processed-frame coverage for %s", key)
		}
		if l.Critical && d.Outcome == "pass" {
			metrics.SilentPassCritical++
		}
		if l.ExpectedOutcome == "pass" {
			clean++
			if d.Outcome != "pass" {
				falseAlarms++
			}
		}
		if d.Outcome == "needs_review" {
			reviews++
		}
		if d.Outcome == "error" {
			metrics.ErrorCount++
		}
		decoded += d.FramesDecoded
		ocr += d.FramesOCRd
		if l.InjectionFrame != nil && !b.Gate.NoGate {
			found := false
			for _, id := range d.OCRFrameIDs {
				if id == *l.InjectionFrame {
					found = true
				}
			}
			if !found {
				metrics.GateMisses++
			}
		}
	}
	if len(labels) == 0 || b.AnalysedSeconds <= 0 || decoded == 0 || b.FinishedAt.Before(b.StartedAt) {
		return metrics, fmt.Errorf("no valid benchmark coverage or timing")
	}
	if len(results) != len(labels) {
		return metrics, fmt.Errorf("result and label coverage differ")
	}
	if clean > 0 {
		metrics.FalseAlarmRate = float64(falseAlarms) / float64(clean)
	}
	metrics.LabelCount = len(labels)
	metrics.CleanCount = clean
	metrics.NeedsReviewRate = float64(reviews) / float64(len(labels))
	metrics.GateSkipRate = 1 - float64(ocr)/float64(decoded)
	minutes := b.AnalysedSeconds / 60
	metrics.CoreSecondsPerMinute = b.CoreSeconds / minutes
	metrics.WallSecondsPerMinute = b.FinishedAt.Sub(b.StartedAt).Seconds() / minutes
	return metrics, nil
}

func hasString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func WriteBenchmark(w io.Writer, metrics []BenchmarkMetrics) error {
	allSynthetic := len(metrics) > 0
	for _, m := range metrics {
		allSynthetic = allSynthetic && m.Synthetic
	}
	heading := "# Local benchmark\n\nInput provenance is not all synthetic. Demand remains unvalidated by this measurement. This does not establish pilot payments, customer demand or permission to use footage."
	if allSynthetic {
		heading = "# Synthetic M1 benchmark\n\nAI-authored synthetic campaign and delivery matrices only; not pilot, customer or shop evidence. Demand is unvalidated and the paid-pilot condition remains unmet. M2/M3 remain on hold."
	}
	if _, err := fmt.Fprintln(w, heading+"\n\nM1 results are measured on synthetic footage only and say nothing about real broadcast supers."); err != nil {
		return err
	}
	for _, m := range metrics {
		if m.NeedsReviewRate > .2 {
			if _, err := fmt.Fprintf(w, "\n**Warning: %s needs-review rate %.1f%% is above the ~20%% synthetic warning threshold.** Review cost and real-file accuracy are unresolved.\n", m.Engine, m.NeedsReviewRate*100); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(w, "\n| Metric | Engine | Measured value |\n|---|---|---|"); err != nil {
		return err
	}
	for _, m := range metrics {
		for _, row := range []struct {
			name  string
			value float64
		}{
			{"label_count", float64(m.LabelCount)}, {"clean_count", float64(m.CleanCount)}, {"error_count", float64(m.ErrorCount)},
			{"silent_pass_critical", float64(m.SilentPassCritical)}, {"false_alarm_rate", m.FalseAlarmRate}, {"needs_review_rate", m.NeedsReviewRate}, {"gate_skip_rate", m.GateSkipRate}, {"gate_misses", float64(m.GateMisses)}, {"core_seconds_per_analysed_minute", m.CoreSecondsPerMinute}, {"wall_seconds_per_analysed_minute", m.WallSecondsPerMinute},
		} {
			if _, err := fmt.Fprintf(w, "| %s | %s | %.6f |\n", row.name, m.Engine, row.value); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w, "\nRates are fractions, not percentages. False alarms include both fail and needs_review on clean labels. Gate misses count labelled injection frames never OCR'd; an OCR miss is separate from a gate miss. CPU includes Go and waited-for child processes (ffprobe, ffmpeg and OCR); wall time includes engine startup.\n\nThese are small deterministic synthetic cases, including near-match typography. The benchmark is not a recall guarantee or evidence of willingness to pay. Shops must independently label real originals, measure preparation and human review time, and authorize footage use before M2 can be released.")
	return err
}
