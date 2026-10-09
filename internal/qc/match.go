package qc

import (
	"fmt"
	"regexp"
	"strings"
)

type Finding struct {
	ID                string        `json:"id"`
	DeliverableID     string        `json:"deliverable_id"`
	Check             string        `json:"check"`
	ExpectationID     string        `json:"expectation_id,omitempty"`
	Outcome           string        `json:"outcome"`
	FirstFrame        int           `json:"first_frame"`
	LastFrame         int           `json:"last_frame"`
	Timecode          string        `json:"timecode"`
	ObservedText      string        `json:"observed_text"`
	Confidence        float64       `json:"confidence"`
	OtherVersionRowID string        `json:"other_version_row_id,omitempty"`
	EvidencePath      string        `json:"evidence_path,omitempty"`
	EvidenceFrame     int           `json:"evidence_frame"`
	Message           string        `json:"message"`
	ExpectedText      string        `json:"expected_text,omitempty"`
	Quad              [4][2]float64 `json:"quad"`
}
type MatchOptions struct {
	MinFrames         int      `json:"min_frames"`
	Confidence        float64  `json:"confidence"`
	PlaceholderTokens []string `json:"placeholder_tokens"`
}
type presence struct {
	run, max, last int
	uncertain      bool
}
type Matcher struct {
	matrix     Matrix
	row        VersionRow
	opts       MatchOptions
	states     map[string]*presence
	findings   []Finding
	indices    map[string]int
	unreadable bool
	sawText    bool
}

var phoneShape = regexp.MustCompile(`\+?[0-9][0-9\s\p{Z}().\p{Pd}]{5,}[0-9]`)

// Keep the whole destination, not a whitelist-truncated prefix. Ambiguous trailing
// punctuation stays in the candidate: a false review is safer than a wrong pass.
var urlShape = regexp.MustCompile(`(?i)(?:https?://)?[a-z0-9][a-z0-9.-]*\.[a-z]{2,}[^\s\p{Z}<>]*`)

func effectiveMatchOptions(opts MatchOptions) MatchOptions {
	if opts.MinFrames <= 0 {
		opts.MinFrames = 12
	}
	if opts.Confidence <= 0 {
		opts.Confidence = .8
	}
	if opts.PlaceholderTokens == nil {
		opts.PlaceholderTokens = []string{}
	}
	return opts
}

func NewMatcher(m Matrix, row VersionRow, opts MatchOptions) *Matcher {
	opts = effectiveMatchOptions(opts)
	matcher := &Matcher{matrix: m, row: row, opts: opts, states: map[string]*presence{}, indices: map[string]int{}, findings: []Finding{}}
	for _, e := range row.Expectations {
		matcher.states[e.ID] = &presence{last: -2}
	}
	base, _, _ := strings.Cut(strings.ToLower(row.Language), "-")
	if base != "en" && base != "fr" {
		matcher.unreadable = true
		matcher.add(0, "C4", "needs_review", Expectation{}, Box{}, "", "Language not validated by the M1 synthetic corpus")
	}
	if len(row.Expectations) == 0 {
		matcher.unreadable = true
		matcher.add(0, "C4", "needs_review", Expectation{}, Box{}, "", "No expected text in this row; nothing can be confirmed")
	}
	return matcher
}

func (m *Matcher) add(frame int, check, outcome string, e Expectation, b Box, owner, message string) {
	key := check + "/" + outcome + "/" + e.ID + "/" + b.Text + "/" + message
	if idx, ok := m.indices[key]; ok {
		finding := &m.findings[idx]
		if finding.LastFrame == frame-1 || finding.LastFrame == frame {
			finding.LastFrame = frame
			finding.Confidence = min(finding.Confidence, b.Conf)
			return
		}
	}
	m.indices[key] = len(m.findings)
	m.findings = append(m.findings, Finding{ID: fmt.Sprintf("finding-%d", len(m.findings)+1), Check: check, Outcome: outcome, ExpectationID: e.ID, FirstFrame: frame, LastFrame: frame, ObservedText: b.Text, Confidence: b.Conf, OtherVersionRowID: owner, Message: message, ExpectedText: e.RawValue, Quad: b.Quad})
}

func candidates(kind, text string) []string {
	switch kind {
	case "phone":
		return phoneShape.FindAllString(text, -1)
	case "url":
		return urlShape.FindAllString(text, -1)
	default:
		return []string{text}
	}
}
func exactText(kind, expected, observed string) bool {
	if kind == "text" {
		return strings.Contains(" "+observed+" ", " "+expected+" ")
	}
	return expected == observed
}
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) > 256 || len(br) > 256 {
		return 99
	}
	prev := make([]int, len(br)+1)
	for i := range prev {
		prev[i] = i
	}
	for i, x := range ar {
		cur := make([]int, len(br)+1)
		cur[0] = i + 1
		for j, y := range br {
			cost := 1
			if x == y {
				cost = 0
			}
			cur[j+1] = min(cur[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}

func (m *Matcher) foreign(kind, value string) (bool, string, bool) {
	nearOwn := false
	for _, e := range m.row.Expectations {
		if e.Kind == kind && e.Polarity == "require" {
			if value == e.NormalisedValue {
				return false, "", false
			}
			if editDistance(value, e.NormalisedValue) <= 2 {
				nearOwn = true
			}
		}
	}
	for _, row := range m.matrix.Rows {
		if row.ID == m.row.ID {
			continue
		}
		for _, e := range row.Expectations {
			if e.Kind == kind && e.Polarity == "require" && e.NormalisedValue == value {
				return true, row.ID, false
			}
		}
	}
	return !nearOwn, "", nearOwn
}

func (m *Matcher) Observe(frame int, boxes []Box) {
	if len(boxes) > 0 {
		m.sawText = true
	}
	matched := map[string]bool{}
	for _, b := range boxes {
		if b.Conf < m.opts.Confidence || strings.TrimSpace(b.Text) == "" {
			m.unreadable = true
			m.add(frame, "C4", "needs_review", Expectation{}, b, "", "Unreadable or low-confidence text")
		}
		normal := Normalize("text", b.Text)
		tokens := append([]string{"FPO", "TBD", "XXX", "LOREM", "PLACEHOLDER"}, m.opts.PlaceholderTokens...)
		for _, token := range tokens {
			if n := Normalize("text", token); n != "" && exactText("text", n, normal) {
				outcome := "fail"
				if b.Conf < m.opts.Confidence {
					outcome = "needs_review"
				}
				m.add(frame, "C3", outcome, Expectation{}, b, "", "Placeholder token: "+token)
			}
		}
		for _, e := range m.row.Expectations {
			if e.Polarity == "forbid" {
				if exactText("text", e.NormalisedValue, normal) {
					outcome := "fail"
					if b.Conf < m.opts.Confidence {
						outcome = "needs_review"
					}
					m.add(frame, "C3", outcome, e, b, "", "Forbidden campaign text")
				}
				continue
			}
			for _, value := range candidates(e.Kind, b.Text) {
				nv := Normalize(e.Kind, value)
				if exactText(e.Kind, e.NormalisedValue, nv) {
					if b.Conf >= m.opts.Confidence {
						matched[e.ID] = true
					} else {
						m.states[e.ID].uncertain = true
						m.add(frame, "C4", "needs_review", e, b, "", "Expected value read below confidence floor")
					}
				} else if nv != "" && editDistance(nv, e.NormalisedValue) <= 2 {
					foreign, _, _ := m.foreign(e.Kind, nv)
					if e.Kind == "text" || !foreign {
						m.states[e.ID].uncertain = true
						m.add(frame, "C4", "needs_review", e, b, "", "Near match; never rounded up to pass")
					}
				}
			}
		}
		for _, kind := range []string{"phone", "url"} {
			for _, value := range candidates(kind, b.Text) {
				nv := Normalize(kind, value)
				foreign, owner, near := m.foreign(kind, nv)
				if near {
					continue
				}
				if foreign {
					outcome := "fail"
					if b.Conf < m.opts.Confidence {
						outcome = "needs_review"
					}
					m.add(frame, "C2", outcome, Expectation{}, b, owner, "Foreign "+kind+" value")
				}
			}
		}
	}
	for _, e := range m.row.Expectations {
		if e.Polarity != "require" {
			continue
		}
		s := m.states[e.ID]
		if matched[e.ID] {
			if s.last == frame-1 {
				s.run++
			} else {
				s.run = 1
			}
			s.max = max(s.max, s.run)
			s.last = frame
		} else {
			s.run = 0
		}
	}
}

func (m *Matcher) Finish(frames int) (string, []Finding) {
	if !m.sawText {
		m.unreadable = true
		m.add(0, "C4", "needs_review", Expectation{}, Box{}, "", "No text read in any analysed frame")
	}
	for _, e := range m.row.Expectations {
		if e.Polarity != "require" {
			continue
		}
		s := m.states[e.ID]
		if s.max < m.opts.MinFrames {
			if s.uncertain || m.unreadable {
				m.add(0, "C4", "needs_review", e, Box{}, "", "Expected value not confirmed; OCR uncertainty prevents a missing-text verdict")
			} else {
				m.add(0, "C1", "fail", e, Box{}, "", fmt.Sprintf("Missing expected value for %d consecutive frames", m.opts.MinFrames))
			}
			// Absence/unconfirmed presence is a whole-analysis conclusion, not a
			// detection invented at frame zero. The attached evidence has its own ID.
			m.findings[len(m.findings)-1].LastFrame = max(0, frames-1)
		}
	}
	if frames == 0 {
		m.add(0, "C4", "needs_review", Expectation{}, Box{}, "", "No frames analysed")
	}
	outcome := "pass"
	for _, f := range m.findings {
		if f.Outcome == "fail" {
			outcome = "fail"
			break
		}
		if f.Outcome == "needs_review" {
			outcome = "needs_review"
		}
	}
	return outcome, m.findings
}
