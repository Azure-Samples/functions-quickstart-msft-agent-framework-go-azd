package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// briefing_handler.go — HTTP entry point for the daily brief.
//
//   GET /api/brief/today  -> runs the multi-agent editor and returns
//                            the composed brief as JSON.
//
// The endpoint is intentionally synchronous with no caching: each
// call re-runs the editor + specialists so the demo shows the
// multi-agent pipeline end-to-end on every request. Adding a cache
// (Cosmos, a Timer trigger to pre-warm, etc.) is a productionization
// exercise separate from the "show me MAF multi-agent" story.

var globalBriefGen BriefGenerator

// SetBriefingImplementations wires the runtime dependency used by
// BriefTodayHandler. Called from main.go alongside SetImplementations.
func SetBriefingImplementations(gen BriefGenerator) {
	globalBriefGen = gen
}

// BriefTodayHandler serves GET /api/brief/today.
func BriefTodayHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	date := time.Now().UTC().Format("2006-01-02")
	slog.InfoContext(r.Context(), "generating brief", "date", date)

	brief, err := globalBriefGen.Generate(r.Context(), date)
	if err != nil {
		slog.ErrorContext(r.Context(), "generate brief", "err", err, "date", date)
		http.Error(w, "failed to generate brief: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(brief); err != nil {
		slog.ErrorContext(r.Context(), "encode brief", "err", err, "date", date)
	}
}
