package qc

import (
	"testing"
)

func matchFixture(t *testing.T) (Matrix, VersionRow) {
	t.Helper()
	m, err := ParseMatrix([]byte("file,version_id,language,phone:contact,text:legal\nsynthetic-a.mp4,synthetic-a,en-GB,2025550100,Terms apply\nsynthetic-b.mp4,synthetic-b,en-GB,2025550101,Terms apply\n"))
	if err != nil {
		t.Fatal(err)
	}
	return m, m.Rows[0]
}
func readBox(text string, conf float64) Box {
	return Box{Text: text, Conf: conf, Quad: [4][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}}
}
func hasCheck(findings []Finding, check string) bool {
	for _, f := range findings {
		if f.Check == check {
			return true
		}
	}
	return false
}

// Removing any C1–C4 rule must change observable verdicts/findings in these tests.
func TestMatcherChecks(t *testing.T) {
	m, row := matchFixture(t)
	for _, tc := range []struct {
		name        string
		boxes       []Box
		want, check string
	}{
		{"clean", []Box{readBox("2025550100", .99), readBox("Terms apply", .99)}, "pass", ""},
		{"C1 missing", []Box{readBox("Terms apply", .99)}, "fail", "C1"},
		{"C2 other row", []Box{readBox("2025550101", .99), readBox("Terms apply", .99)}, "fail", "C2"},
		{"C3 placeholder", []Box{readBox("2025550100", .99), readBox("Terms apply", .99), readBox("FPO", .99)}, "fail", "C3"},
		{"C4 near phone", []Box{readBox("2025550109", .99), readBox("Terms apply", .99)}, "needs_review", "C4"},
		{"C4 low confidence", []Box{readBox("2025550100", .3), readBox("Terms apply", .99)}, "needs_review", "C4"},
		{"C4 unreadable", []Box{readBox("", 0)}, "needs_review", "C4"},
		{"C4 nothing read", nil, "needs_review", "C4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matcher := NewMatcher(m, row, MatchOptions{MinFrames: 12, Confidence: .8})
			for f := 0; f < 12; f++ {
				matcher.Observe(f, tc.boxes)
			}
			outcome, findings := matcher.Finish(12)
			if outcome != tc.want || (tc.check != "" && !hasCheck(findings, tc.check)) {
				t.Fatalf("want %s %s, got %s %+v", tc.want, tc.check, outcome, findings)
			}
			if tc.check == "C2" {
				for _, f := range findings {
					if f.Check == "C2" && f.OtherVersionRowID != "row-3" {
						t.Fatal("foreign owner missing", f)
					}
				}
			}
		})
	}
}

func TestConsecutivePresenceAndSingleFrame(t *testing.T) {
	m, row := matchFixture(t)
	matcher := NewMatcher(m, row, MatchOptions{MinFrames: 12, Confidence: .8})
	clean := []Box{readBox("2025550100", .99), readBox("Terms apply", .99)}
	for f := 0; f < 24; f++ {
		boxes := clean
		if f == 11 {
			boxes = []Box{readBox("Terms apply", .99)}
		}
		matcher.Observe(f, boxes)
	}
	outcome, findings := matcher.Finish(24)
	if outcome != "pass" {
		t.Fatalf("12-frame trailing run missing: %s %+v", outcome, findings)
	}
	matcher = NewMatcher(m, row, MatchOptions{MinFrames: 12, Confidence: .8})
	for f := 0; f < 23; f++ {
		boxes := clean
		if f == 11 {
			boxes = []Box{readBox("Terms apply", .99)}
		}
		matcher.Observe(f, boxes)
	}
	outcome, findings = matcher.Finish(23)
	if outcome != "fail" || !hasCheck(findings, "C1") {
		t.Fatalf("accumulated disjoint runs: %s %+v", outcome, findings)
	}
	matcher = NewMatcher(m, row, MatchOptions{MinFrames: 12, Confidence: .8})
	for f := 0; f < 30; f++ {
		boxes := clean
		if f == 15 {
			boxes = append([]Box{readBox("2025550101", .99)}, readBox("Terms apply", .99))
		}
		matcher.Observe(f, boxes)
	}
	outcome, findings = matcher.Finish(30)
	if outcome != "fail" || !hasCheck(findings, "C2") {
		t.Fatal("lost one-frame foreign value", findings)
	}
	for _, f := range findings {
		if f.Check == "C2" && (f.FirstFrame != 15 || f.LastFrame != 15) {
			t.Fatal("wrong single-frame evidence range", f)
		}
	}
}

func TestForbidTokensAndNearText(t *testing.T) {
	m, err := ParseMatrix([]byte("file,version_id,language,text:legal,forbid:draft\nsynthetic.mp4,synthetic,en-GB,Terms apply,INTERNAL ONLY\n"))
	if err != nil {
		t.Fatal(err)
	}
	matcher := NewMatcher(m, m.Rows[0], MatchOptions{MinFrames: 1, Confidence: .8, PlaceholderTokens: []string{"DRAFT"}})
	matcher.Observe(0, []Box{readBox("Terms apply", .99), readBox("INTERNAL ONLY", .99), readBox("DRAFT", .99)})
	outcome, findings := matcher.Finish(1)
	if outcome != "fail" || !hasCheck(findings, "C3") {
		t.Fatalf("forbidden values missed: %+v", findings)
	}
	matcher = NewMatcher(m, m.Rows[0], MatchOptions{MinFrames: 1, Confidence: .8})
	matcher.Observe(0, []Box{readBox("Terms applv", .99)})
	outcome, findings = matcher.Finish(1)
	if outcome != "needs_review" || !hasCheck(findings, "C4") {
		t.Fatalf("near legal silently passed: %s %+v", outcome, findings)
	}
}
