package qc

import (
	"strings"
	"testing"
)

// Catches lossy phone normalization and case/punctuation mismatches.
func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ kind, raw, want string }{
		{"phone", "+1 (202) 555-0100", "+12025550100"},
		{"phone", "020 5550 100", "0205550100"},
		{"url", "HTTPS://Example.INVALID/Offer/", "example.invalid/offer"},
		{"text", "  ＯＦＦＥＲ — Straße!\tété ", "offer strasse été"},
		{"text", "Terms,\n  apply.", "terms apply"},
	} {
		t.Run(tc.kind+tc.raw, func(t *testing.T) {
			if got := Normalize(tc.kind, tc.raw); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseMatrixExpectations(t *testing.T) {
	m, err := ParseMatrix([]byte("file,version_id,language,phone:contact,url:offer,text:legal,forbid:draft\nsynthetic-a.mp4,synthetic-a,fr-FR,+1 202 555 0100,https://example.invalid/,Offre spéciale,FPO\nsynthetic-b.mp4,synthetic-b,en-GB,,,,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rows) != 2 || len(m.Rows[0].Expectations) != 4 || len(m.Rows[1].Expectations) != 0 {
		t.Fatalf("unexpected rows: %+v", m)
	}
	e := m.Rows[0].Expectations[0]
	if e.NormalisedValue != "+12025550100" || e.Polarity != "require" || e.VersionRowID != m.Rows[0].ID || e.ID == "" {
		t.Fatalf("bad expectation: %+v", e)
	}
	if m.Rows[0].Expectations[3].Polarity != "forbid" || len(m.SHA256) != 64 {
		t.Fatal("polarity/hash missing")
	}
}

// Catches silently ignored malformed inputs; every error must locate the cell.
func TestParseMatrixErrors(t *testing.T) {
	for _, tc := range []struct{ name, csv, row, col string }{
		{"empty", "", "row 1", "column"},
		{"missing file", "version_id,language\na,en\n", "row 1", "file"},
		{"unknown column", "file,wat\na.mp4,x\n", "row 1", "wat"},
		{"duplicate header", "file,file\na,a\n", "row 1", "file"},
		{"empty label", "file,text:\na.mp4,a\n", "row 1", "text:"},
		{"blank file", "file,version_id,language\n,a,en\n", "row 2", "file"},
		{"bad glob", "file,version_id,language\n[,a,en\n", "row 2", "file"},
		{"traversal", "file,version_id,language\n../a.mp4,a,en\n", "row 2", "file"},
		{"duplicate version", "file,version_id,language\na.mp4,a,en\nb.mp4,a,en\n", "row 3", "version_id"},
		{"invalid phone", "file,phone:x\na.mp4,abc\n", "row 2", "phone:x"},
		{"invalid url", "file,url:x\na.mp4,not a url\n", "row 2", "url:x"},
		{"bad utf8", "file,text:x\na.mp4,\xff\n", "row 2", "text:x"},
		{"wrong width", "file,text:x\na.mp4,a,b\n", "row 2", "column"},
		{"no rows", "file,version_id,language\n", "row 2", "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMatrix([]byte(tc.csv))
			if err == nil || !strings.Contains(err.Error(), tc.row) || !strings.Contains(err.Error(), tc.col) {
				t.Fatalf("want %s %s error, got %v", tc.row, tc.col, err)
			}
		})
	}
}

func TestJoinC0(t *testing.T) {
	m, err := ParseMatrix([]byte("file,version_id,language\na*.mp4,a,en\nb.mp4,b,en\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{"a.mp4", "b.mp4"}, ""},
		{[]string{"a.mp4", "b.mp4", "orphan.mp4"}, "no row"},
		{[]string{"a.mp4"}, "no file"},
		{[]string{"a.mp4", "a2.mp4", "b.mp4"}, "multiple files"},
		{[]string{"a.mp4", "a.mp4", "b.mp4"}, "duplicate"},
	} {
		joined, problems := Join(m, tc.files)
		if tc.want == "" {
			if len(problems) != 0 || len(joined) != 2 {
				t.Fatalf("join failed: %+v", problems)
			}
		} else {
			found := false
			for _, p := range problems {
				if p.Check == "C0" && strings.Contains(p.Message, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %q: %+v", tc.want, problems)
			}
		}
	}
	ambiguous, _ := ParseMatrix([]byte("file,version_id,language\na*.mp4,a,en\n*.mp4,b,en\n"))
	_, problems := Join(ambiguous, []string{"a.mp4"})
	if len(problems) == 0 || !strings.Contains(problems[0].Message, "multiple rows") {
		t.Fatal(problems)
	}
}
