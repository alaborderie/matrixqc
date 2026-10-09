package qc

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

type SiteConfig struct{ PilotURL, ContactEmail string }

//go:embed assets/site
var siteAssets embed.FS

func NewSite(config SiteConfig) (http.Handler, error) {
	if config.PilotURL != "" {
		u, err := url.Parse(config.PilotURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return nil, fmt.Errorf("PILOT_URL must be an absolute http(s) founder-issued URL")
		}
	}
	tmpl, err := template.New("index.html").ParseFS(siteAssets, "assets/site/index.html")
	if err != nil {
		return nil, err
	}
	view := struct {
		SiteConfig
		CSS        template.CSS
		ContactURL string
	}{config, template.CSS(StyleCSS()), ""}
	if config.ContactEmail != "" {
		view.ContactURL = "mailto:" + config.ContactEmail
	}
	var landing strings.Builder
	if err := tmpl.Execute(&landing, view); err != nil {
		return nil, err
	}
	routes := map[string]struct{ file, contentType string }{
		"/style.css":              {"assets/style.css", "text/css; charset=utf-8"},
		"/template.csv":           {"assets/site/template.csv", "text/csv; charset=utf-8"},
		"/sample/matrix.csv":      {"assets/site/sample/synthetic-matrix.csv", "text/csv; charset=utf-8"},
		"/sample/labels.json":     {"assets/site/sample/synthetic-labels.json", "application/json"},
		"/sample/provenance.json": {"assets/site/sample/SYNTHETIC.json", "application/json"},
		"/sample/results.json":    {"assets/site/sample/results.json", "application/json"},
		"/sample/report.html":     {"assets/site/sample/report.html", "text/html; charset=utf-8"},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline' 'self'; script-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(200)
			if r.Method != "HEAD" {
				w.Write([]byte("ok\n"))
			}
			return
		}
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method != "HEAD" {
				w.Write([]byte(landing.String()))
			}
			return
		}
		route, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var data []byte
		var err error
		if r.URL.Path == "/style.css" {
			data = []byte(StyleCSS())
		} else {
			data, err = siteAssets.ReadFile(route.file)
		}
		if err != nil {
			http.Error(w, "embedded asset unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", route.contentType)
		if strings.HasSuffix(r.URL.Path, ".csv") {
			w.Header().Set("Content-Disposition", "attachment; filename="+strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/"), "/", "-"))
		}
		if r.Method != "HEAD" {
			w.Write(data)
		}
	}), nil
}
