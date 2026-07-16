package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/microsoft/agent-framework-go/tool/functool"
)

// briefing_tools.go — two extra NASA tools used by the multi-agent
// briefing pipeline. The other two specialists reuse apodTool and
// neoTool from agent.go, which also demonstrates that a functool
// can be attached to more than one MAF agent.
//
// Both APIs here are keyless (no api.nasa.gov quota to worry about)
// so the briefing keeps rendering even when NASA_API_KEY is the
// DEMO_KEY default.

// ─── EONET: active natural events on Earth (keyless) ─────────────────

type EarthEventsInput struct {
	// One of: "open" (active) or "closed". Defaults to "open".
	Status string `json:"status,omitempty"`
}

type EarthEventCategory struct {
	Title string `json:"title"`
}

type EarthEvent struct {
	ID         string               `json:"id"`
	Title      string               `json:"title"`
	Link       string               `json:"link"`
	Categories []EarthEventCategory `json:"categories"`
}

type EarthEventsResult struct {
	Events []EarthEvent `json:"events"`
}

var earthEventsTool = wrapTool[EarthEventsInput, EarthEventsResult](
	functool.Config{
		Name: "get_earth_events",
		Description: "Fetch active natural events on Earth from NASA's EONET " +
			"(wildfires, volcanoes, storms, icebergs, etc.). Keyless.",
	},
	func(ctx context.Context, in EarthEventsInput) (EarthEventsResult, error) {
		status := in.Status
		if status == "" {
			status = "open"
		}
		slog.InfoContext(ctx, "tool call: get_earth_events", "status", status)
		q := url.Values{}
		q.Set("status", status)
		q.Set("limit", "10")
		// EONET lives on gsfc.nasa.gov, NOT api.nasa.gov, and needs no key.
		var raw struct {
			Events []EarthEvent `json:"events"`
		}
		if err := fetchJSON(ctx, "https://eonet.gsfc.nasa.gov/api/v3/events?"+q.Encode(), &raw); err != nil {
			return EarthEventsResult{}, err
		}
		return EarthEventsResult{Events: raw.Events}, nil
	},
)

// ─── EPIC: latest full-disk Earth photo ──────────────────────────────

type EarthPhotoInput struct{}

type EarthPhotoResult struct {
	Caption string `json:"caption,omitempty"`
	Date    string `json:"date"`
	// ImageURL is a fully-formed URL you can drop into an <img> tag.
	ImageURL string `json:"image_url"`
}

var earthPhotoTool = wrapTool[EarthPhotoInput, EarthPhotoResult](
	functool.Config{
		Name: "get_earth_photo",
		Description: "Fetch the most recent full-disk Earth photo taken by NASA's EPIC " +
			"camera onboard DSCOVR (~1 million miles from Earth). Returns image URL, " +
			"date, and a short caption.",
	},
	func(ctx context.Context, in EarthPhotoInput) (EarthPhotoResult, error) {
		slog.InfoContext(ctx, "tool call: get_earth_photo")
		// Use the keyless epic.gsfc.nasa.gov CDN directly. The
		// api.nasa.gov/EPIC proxy 302-redirects here but strips the
		// api_key en route, so calling the CDN skips a round-trip.
		var raw []struct {
			Caption string `json:"caption"`
			Image   string `json:"image"`
			Date    string `json:"date"` // "YYYY-MM-DD HH:MM:SS"
		}
		if err := fetchJSON(ctx, "https://epic.gsfc.nasa.gov/api/natural", &raw); err != nil {
			return EarthPhotoResult{}, err
		}
		if len(raw) == 0 {
			return EarthPhotoResult{}, fmt.Errorf("epic: no photos returned")
		}
		p := raw[len(raw)-1] // most recent
		// EPIC date is "YYYY-MM-DD HH:MM:SS"; the archive URL wants YYYY/MM/DD.
		datePath := strings.ReplaceAll(strings.Split(p.Date, " ")[0], "-", "/")
		return EarthPhotoResult{
			Caption:  p.Caption,
			Date:     p.Date,
			ImageURL: fmt.Sprintf("https://epic.gsfc.nasa.gov/archive/natural/%s/png/%s.png", datePath, p.Image),
		}, nil
	},
)
