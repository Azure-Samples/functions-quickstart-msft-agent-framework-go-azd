package app

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed ui/index.html
var uiFS embed.FS

// uiTemplate is parsed once at process start. html/template auto-escapes
// interpolated values, but here we only inject a well-formed URL path
// (a constant) — the security-sensitive rendering happens client-side
// where the UI treats server-supplied text as .textContent, never .innerHTML.
var uiTemplate = template.Must(template.ParseFS(uiFS, "ui/index.html"))

type uiData struct {
	// ChatBaseURL is the prefix for the chat API endpoints. Passing it
	// through the template makes the UI portable across custom route
	// prefixes and reverse-proxied deployments.
	ChatBaseURL string
	// BriefBaseURL is the prefix for the daily-brief API endpoints.
	BriefBaseURL string
}

func UIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = uiTemplate.Execute(w, uiData{
		ChatBaseURL:  "/api/chat",
		BriefBaseURL: "/api/brief",
	})
}
