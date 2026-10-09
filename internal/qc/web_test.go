package qc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDemoRoutesAndPilotFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config SiteConfig
		cta    string
		priced bool
	}{
		{"unset", SiteConfig{}, "Pilot link not configured", false},
		{"contact", SiteConfig{ContactEmail: "founder@example.invalid"}, "mailto:founder@example.invalid", false},
		{"configured", SiteConfig{PilotURL: "https://pilot.example.invalid/start"}, "https://pilot.example.invalid/start", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site, err := NewSite(tc.config)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/", "/healthz", "/template.csv", "/sample/matrix.csv", "/sample/report.html"} {
				rr := httptest.NewRecorder()
				site.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
				if rr.Code != 200 {
					t.Fatalf("%s status %d", path, rr.Code)
				}
				if path == "/" {
					body := rr.Body.String()
					if !strings.Contains(body, tc.cta) || !strings.Contains(body, "synthetic") || !strings.Contains(body, "unvalidated") {
						t.Fatal("CTA/provenance missing", body)
					}
					if strings.Contains(body, "$49") != tc.priced {
						t.Fatal("price offered without configured founder link")
					}
				}
			}
			for _, path := range []string{"/upload", "/login", "/billing", "/not-found", "/sample/../private"} {
				rr := httptest.NewRecorder()
				site.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
				if rr.Code != 404 {
					t.Fatalf("unexpected endpoint %s %d", path, rr.Code)
				}
			}
		})
	}
}

func TestInvalidPilotLinkRejected(t *testing.T) {
	for _, link := range []string{"javascript:alert(1)", "/local-link", "https://", "ftp://example.invalid"} {
		if _, err := NewSite(SiteConfig{PilotURL: link}); err == nil {
			t.Fatalf("unsafe pilot link accepted %q", link)
		}
	}
}
