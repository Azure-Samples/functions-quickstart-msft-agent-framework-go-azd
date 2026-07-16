package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/tool/functool"
)

// chat_tools.go — the three NASA tools attached to the chat agent
// (chat_agent.go). Each tool is defined by a functool.Config +
// typed input/output structs — MAF derives the JSON schema from the
// input struct so the LLM knows what arguments to send.
//
// All three are backed by https://api.nasa.gov, keyless via the shared
// DEMO_KEY (rate-limited: ~30 req/hr, 50 req/day). Set NASA_API_KEY in
// env to raise the limit — get one instantly at https://api.nasa.gov.
//
// Design notes:
//   - Struct inputs generate JSON schemas the LLM understands; each
//     field is optional so the model can call with no args ("today").
//   - Struct outputs let the UI pick out image URLs and render them
//     as inline thumbnails (see chat_handler.go for result marshaling
//     and ui/index.html for the rendering).
//   - fetchJSON (util.go) caps response reads and enforces a 10s
//     deadline so a slow upstream doesn't hang the function invocation.

// nasaAPIKey returns the configured key or "DEMO_KEY". DEMO_KEY works
// for every endpoint below but shares a global rate limit — for any
// real usage set NASA_API_KEY to your own free key.
func nasaAPIKey() string {
	if k := os.Getenv("NASA_API_KEY"); k != "" {
		return k
	}
	return "DEMO_KEY"
}

// ─── Tool 1: Astronomy Picture of the Day ────────────────────────────

type APODInput struct {
	// Date in YYYY-MM-DD format. Leave empty for today's picture.
	Date string `json:"date,omitempty"`
}

type APODResult struct {
	Date        string `json:"date"`
	Title       string `json:"title"`
	Explanation string `json:"explanation"`
	URL         string `json:"url"`
	HDURL       string `json:"hdurl,omitempty"`
	MediaType   string `json:"media_type"`
	Copyright   string `json:"copyright,omitempty"`
}

var apodTool = wrapTool[APODInput, APODResult](
	functool.Config{
		Name: "get_apod",
		Description: "Get NASA's Astronomy Picture of the Day (APOD). " +
			"Returns title, explanation, and image URL. Optionally pass a date " +
			"in YYYY-MM-DD format to retrieve a historical picture; otherwise " +
			"returns today's.",
	},
	fetchAPOD,
)

// fetchAPOD is the underlying implementation so stub.go can reuse it.
// Exposing the raw function (vs. hiding it behind functool) lets the
// stub mode drive real NASA endpoints without spinning up an LLM.
func fetchAPOD(ctx context.Context, in APODInput) (APODResult, error) {
	slog.InfoContext(ctx, "tool call: get_apod", "date", in.Date)
	q := url.Values{}
	q.Set("api_key", nasaAPIKey())
	if in.Date != "" {
		q.Set("date", in.Date)
	}
	var out APODResult
	if err := fetchJSON(ctx, "https://api.nasa.gov/planetary/apod?"+q.Encode(), &out); err != nil {
		return APODResult{}, err
	}
	return out, nil
}

// ─── Tool 2: Near Earth Objects ──────────────────────────────────────

type NEOInput struct {
	// Start date in YYYY-MM-DD format. Defaults to today.
	StartDate string `json:"start_date,omitempty"`
	// End date in YYYY-MM-DD format. Max 7 days from start. Defaults to start_date.
	EndDate string `json:"end_date,omitempty"`
}

type NEOApproach struct {
	Name              string  `json:"name"`
	CloseApproachDate string  `json:"close_approach_date"`
	MissDistanceKm    float64 `json:"miss_distance_km"`
	RelativeVelKps    float64 `json:"relative_velocity_kps"`
	DiameterMinMeters float64 `json:"diameter_min_meters"`
	DiameterMaxMeters float64 `json:"diameter_max_meters"`
	Hazardous         bool    `json:"potentially_hazardous"`
	NASAJPLURL        string  `json:"nasa_jpl_url"`
}

type NEOResult struct {
	ElementCount int           `json:"element_count"`
	Approaches   []NEOApproach `json:"approaches"`
}

var neoTool = wrapTool[NEOInput, NEOResult](
	functool.Config{
		Name: "get_near_earth_objects",
		Description: "List asteroids (Near Earth Objects) approaching Earth in a date range. " +
			"Each entry includes miss distance (km), relative velocity (km/s), estimated " +
			"diameter (m), and whether NASA classifies it as potentially hazardous. " +
			"Date range is max 7 days; both dates default to today.",
	},
	func(ctx context.Context, in NEOInput) (NEOResult, error) {
		slog.InfoContext(ctx, "tool call: get_near_earth_objects",
			"start", in.StartDate, "end", in.EndDate)
		q := url.Values{}
		q.Set("api_key", nasaAPIKey())
		if in.StartDate != "" {
			q.Set("start_date", in.StartDate)
		}
		if in.EndDate != "" {
			q.Set("end_date", in.EndDate)
		}
		var raw struct {
			ElementCount int `json:"element_count"`
			// NEOs are keyed by date. We flatten across dates.
			NearEarthObjects map[string][]struct {
				Name                   string `json:"name"`
				NASAJPLURL             string `json:"nasa_jpl_url"`
				IsPotentiallyHazardous bool   `json:"is_potentially_hazardous_asteroid"`
				EstimatedDiameter      struct {
					Meters struct {
						Min float64 `json:"estimated_diameter_min"`
						Max float64 `json:"estimated_diameter_max"`
					} `json:"meters"`
				} `json:"estimated_diameter"`
				CloseApproachData []struct {
					CloseApproachDate string `json:"close_approach_date"`
					RelativeVelocity  struct {
						KilometersPerSecond string `json:"kilometers_per_second"`
					} `json:"relative_velocity"`
					MissDistance struct {
						Kilometers string `json:"kilometers"`
					} `json:"miss_distance"`
				} `json:"close_approach_data"`
			} `json:"near_earth_objects"`
		}
		u := "https://api.nasa.gov/neo/rest/v1/feed?" + q.Encode()
		if err := fetchJSON(ctx, u, &raw); err != nil {
			return NEOResult{}, err
		}
		out := NEOResult{ElementCount: raw.ElementCount}
		for _, list := range raw.NearEarthObjects {
			for _, neo := range list {
				a := NEOApproach{
					Name:              neo.Name,
					Hazardous:         neo.IsPotentiallyHazardous,
					NASAJPLURL:        neo.NASAJPLURL,
					DiameterMinMeters: neo.EstimatedDiameter.Meters.Min,
					DiameterMaxMeters: neo.EstimatedDiameter.Meters.Max,
				}
				if len(neo.CloseApproachData) > 0 {
					cad := neo.CloseApproachData[0]
					a.CloseApproachDate = cad.CloseApproachDate
					a.MissDistanceKm, _ = parseFloat(cad.MissDistance.Kilometers)
					a.RelativeVelKps, _ = parseFloat(cad.RelativeVelocity.KilometersPerSecond)
				}
				out.Approaches = append(out.Approaches, a)
			}
		}
		return out, nil
	},
)

// ─── Tool 3: Mars Rover Photo ────────────────────────────────────────

type MarsInput struct {
	// Rover name: curiosity | perseverance | opportunity | spirit. Default: curiosity.
	Rover string `json:"rover,omitempty"`
	// Martian sol (day). Mutually exclusive with earth_date.
	Sol int `json:"sol,omitempty"`
	// Earth date in YYYY-MM-DD format. Mutually exclusive with sol.
	EarthDate string `json:"earth_date,omitempty"`
	// Optional camera abbreviation (FHAZ, RHAZ, MAST, CHEMCAM, NAVCAM, ...).
	Camera string `json:"camera,omitempty"`
}

type MarsPhoto struct {
	ID          int    `json:"id"`
	ImageURL    string `json:"img_src"`
	EarthDate   string `json:"earth_date"`
	Sol         int    `json:"sol"`
	Camera      string `json:"camera"`
	Rover       string `json:"rover"`
	RoverStatus string `json:"rover_status"`
}

type MarsResult struct {
	Photos []MarsPhoto `json:"photos"`
}

var marsTool = wrapTool[MarsInput, MarsResult](
	functool.Config{
		Name: "get_mars_rover_photo",
		Description: "Fetch curated Mars rover photos from the NASA Image and Video " +
			"Library. Optionally narrow by rover (curiosity, perseverance, opportunity, " +
			"spirit) or camera keyword. Returns a handful of photos with image URLs, " +
			"titles, and dates. (Note: date/sol filtering is not supported by the " +
			"backing library; those inputs are used as free-text hints.)",
	},
	func(ctx context.Context, in MarsInput) (MarsResult, error) {
		rover := strings.ToLower(strings.TrimSpace(in.Rover))
		if rover == "" {
			rover = "curiosity"
		}
		slog.InfoContext(ctx, "tool call: get_mars_rover_photo",
			"rover", rover, "camera", in.Camera, "earth_date", in.EarthDate)

		// NASA's official mars-photos API (Heroku-hosted) was decommissioned;
		// substitute the Image Library, which has broad rover coverage and
		// stays online.
		query := rover + " rover"
		if in.Camera != "" {
			query += " " + in.Camera
		}
		q := url.Values{}
		q.Set("q", query)
		q.Set("media_type", "image")
		if in.EarthDate != "" && len(in.EarthDate) >= 4 {
			q.Set("year_start", in.EarthDate[:4])
			q.Set("year_end", in.EarthDate[:4])
		}
		var raw struct {
			Collection struct {
				Items []struct {
					Data []struct {
						Title       string `json:"title"`
						DateCreated string `json:"date_created"`
						Description string `json:"description"`
						Center      string `json:"center"`
						NASAID      string `json:"nasa_id"`
					} `json:"data"`
					Links []struct {
						Href   string `json:"href"`
						Rel    string `json:"rel"`
						Render string `json:"render"`
					} `json:"links"`
				} `json:"items"`
			} `json:"collection"`
		}
		if err := fetchJSON(ctx, "https://images-api.nasa.gov/search?"+q.Encode(), &raw); err != nil {
			return MarsResult{}, err
		}
		out := MarsResult{}
		const maxPhotos = 5
		for _, it := range raw.Collection.Items {
			if len(out.Photos) >= maxPhotos {
				break
			}
			if len(it.Data) == 0 {
				continue
			}
			d := it.Data[0]
			var img string
			for _, l := range it.Links {
				if l.Render == "image" {
					img = l.Href
					break
				}
			}
			if img == "" {
				continue
			}
			date := d.DateCreated
			if i := strings.Index(date, "T"); i > 0 {
				date = date[:i]
			}
			out.Photos = append(out.Photos, MarsPhoto{
				ImageURL:  img,
				EarthDate: date,
				Camera:    d.Title,
				Rover:     rover,
			})
		}
		return out, nil
	},
)

// parseFloat is a tiny helper for the NEO tool — NASA returns numeric
// fields as JSON strings, so we scan them back into float64.
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}
