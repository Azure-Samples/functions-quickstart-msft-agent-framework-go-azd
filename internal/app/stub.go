package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
)

// stubChat and stubStore satisfy ChatRunner / SessionSink without
// touching Azure OpenAI or Cosmos DB. They exist so `func start`
// works out of the box for smoke-testing the Function App wiring
// (SSE streaming, tool-chip rendering in the UI, request routing)
// before any real Azure resources are provisioned.
//
// Activated automatically when AZURE_OPENAI_ENDPOINT is unset or
// still contains the placeholder <your-...> pattern from the shipped
// local.settings.json. See selectImplementations() in main.go.

// ─── ChatRunner stub ──────────────────────────────────────────────────

type stubChat struct{}

// NewStubChat returns a ChatRunner that fakes tool calls and streams
// canned replies. Used when AZURE_OPENAI_ENDPOINT is not set — see
// main.go's stub-mode detection.
func NewStubChat() ChatRunner { return stubChat{} }

// LoadSession stores a per-conversation turn counter inside the
// agent.Session state. This lets the stub demonstrate that session
// hydration -> mutation -> persistence actually round-trips, matching
// what a real MAF session does.
func (stubChat) LoadSession(ctx context.Context, raw []byte) (*agent.Session, error) {
	if len(raw) == 0 {
		return &agent.Session{}, nil
	}
	var s agent.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("stub: unmarshal session: %w", err)
	}
	return &s, nil
}

const stubTurnKey = "stub.turn"

// Run fakes a streaming assistant reply. Based on user intent it
// optionally fakes NASA tool calls so the UI exercises the same code
// path a real MAF agent would drive (tool_call frame -> tool_result
// frame with structured payload the UI renders as image thumbnails).
func (stubChat) Run(
	ctx context.Context,
	sess *agent.Session,
	userMessage string,
	emit func(event string, payload any) error,
) error {
	var turn int
	if _, err := sess.Get(stubTurnKey, &turn); err != nil {
		return fmt.Errorf("stub: session.Get: %w", err)
	}
	turn++
	sess.Set(stubTurnKey, turn)

	slog.InfoContext(ctx, "stub agent running", "turn", turn, "message", userMessage)

	// Choose a fake tool call based on keywords so the UI's chip +
	// image rendering paths get exercised without a real LLM.
	lower := strings.ToLower(userMessage)
	switch {
	case containsAny(lower, "asteroid", "near earth", "neo", "hazard"):
		if err := fakeNEO(emit); err != nil {
			return err
		}
	case containsAny(lower, "mars", "rover", "curiosity", "perseverance"):
		if err := fakeMars(emit); err != nil {
			return err
		}
	case containsAny(lower, "apod", "astronomy", "picture of the day", "space image", "nebula", "galaxy"):
		if err := fakeAPOD(ctx, emit); err != nil {
			return err
		}
	}

	// Chunked "streaming" reply — split into words with tiny delays
	// so the UI clearly demonstrates token-by-token rendering.
	reply := fmt.Sprintf(
		"(stub mode, turn %d) You said: %q. Configure AZURE_OPENAI_ENDPOINT + COSMOS_ENDPOINT to switch to the real MAF agent.",
		turn, userMessage,
	)
	for _, word := range strings.Fields(reply) {
		if err := emit("text", map[string]string{"text": word + " "}); err != nil {
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// fakeAPOD emits an Astronomy Picture of the Day. First tries the real
// NASA endpoint via DEMO_KEY (so the demo shows live data); on any
// failure — network down, rate limit exceeded — falls back to a
// hard-coded known-good image so the UI path is always exercised.
func fakeAPOD(ctx context.Context, emit func(event string, payload any) error) error {
	callID := uuid.NewString()
	if err := emit("tool_call", map[string]any{
		"name":      "get_apod",
		"arguments": `{}`,
		"callId":    callID,
	}); err != nil {
		return err
	}

	result, err := fetchAPOD(ctx, APODInput{})
	if err != nil {
		slog.WarnContext(ctx, "stub: real APOD fetch failed, using canned data", "err", err)
		result = APODResult{
			Date:        "canned",
			Title:       "Pillars of Creation (canned fallback)",
			Explanation: "Real NASA APOD fetch failed — showing a stable canned image. Configure NASA_API_KEY and check network connectivity for live data.",
			// Stable NASA-hosted asset, verified reachable.
			URL:       "https://mars.nasa.gov/msl-raw-images/msss/00001/mcam/0001MR0000001000C0_DXXX.jpg",
			MediaType: "image",
		}
	}
	return emit("tool_result", map[string]any{
		"callId": callID,
		"result": result,
	})
}

// fakeNEO emits a canned Near Earth Object list.
func fakeNEO(emit func(event string, payload any) error) error {
	callID := uuid.NewString()
	if err := emit("tool_call", map[string]any{
		"name":      "get_near_earth_objects",
		"arguments": `{}`,
		"callId":    callID,
	}); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return emit("tool_result", map[string]any{
		"callId": callID,
		"result": NEOResult{
			ElementCount: 3,
			Approaches: []NEOApproach{
				{Name: "(2024 QG) [stub]", CloseApproachDate: "2026-07-15", MissDistanceKm: 852341, RelativeVelKps: 12.4, DiameterMinMeters: 22.1, DiameterMaxMeters: 49.4, Hazardous: false, NASAJPLURL: "https://ssd.jpl.nasa.gov/tools/sbdb_lookup.html"},
				{Name: "(2020 SO) [stub]", CloseApproachDate: "2026-07-15", MissDistanceKm: 4123002, RelativeVelKps: 8.9, DiameterMinMeters: 5.2, DiameterMaxMeters: 11.7, Hazardous: false, NASAJPLURL: "https://ssd.jpl.nasa.gov/tools/sbdb_lookup.html"},
				{Name: "(2015 TB145) [stub]", CloseApproachDate: "2026-07-15", MissDistanceKm: 302115, RelativeVelKps: 26.4, DiameterMinMeters: 210, DiameterMaxMeters: 470, Hazardous: true, NASAJPLURL: "https://ssd.jpl.nasa.gov/tools/sbdb_lookup.html"},
			},
		},
	})
}

// fakeMars emits a canned Mars rover photo response. The image URLs
// point at stable NASA-hosted assets so the UI thumbnail path works
// offline.
func fakeMars(emit func(event string, payload any) error) error {
	callID := uuid.NewString()
	if err := emit("tool_call", map[string]any{
		"name":      "get_mars_rover_photo",
		"arguments": `{"rover":"curiosity"}`,
		"callId":    callID,
	}); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return emit("tool_result", map[string]any{
		"callId": callID,
		"result": MarsResult{
			Photos: []MarsPhoto{
				{ID: 1, ImageURL: "https://mars.nasa.gov/msl-raw-images/msss/00001/mcam/0001MR0000001000C0_DXXX.jpg", EarthDate: "2012-08-06", Sol: 1, Camera: "MAST", Rover: "Curiosity", RoverStatus: "active"},
				{ID: 2, ImageURL: "https://mars.nasa.gov/msl-raw-images/msss/00003/mcam/0003ML0000131000E1_DXXX.jpg", EarthDate: "2012-08-08", Sol: 3, Camera: "MAST", Rover: "Curiosity", RoverStatus: "active"},
			},
		},
	})
}

// ─── SessionSink stub ─────────────────────────────────────────────────

type stubStore struct {
	mu    sync.Mutex
	items map[string][]byte
}

func newStubStore() *stubStore {
	return &stubStore{items: map[string][]byte{}}
}

// NewStubStore returns an in-memory SessionSink for offline demos.
// State lives in the process, so it's lost on restart — fine for a
// smoke test, unsuitable for anything else.
func NewStubStore() SessionSink { return newStubStore() }

func (s *stubStore) Load(ctx context.Context, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Return a copy so callers can mutate freely.
	if b, ok := s.items[id]; ok {
		out := make([]byte, len(b))
		copy(out, b)
		return out, nil
	}
	return nil, nil
}

func (s *stubStore) Save(ctx context.Context, id string, sess *agent.Session) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.items[id] = raw
	s.mu.Unlock()
	return nil
}

func (s *stubStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
	return nil
}

// ─── BriefGenerator stub ──────────────────────────────────────────────

type stubBriefGen struct{}

// NewStubBriefGenerator returns a BriefGenerator that returns a fixed
// sample brief with a stable image URL, so the Daily Brief tab renders
// in stub mode without hitting Azure OpenAI or NASA.
func NewStubBriefGenerator() BriefGenerator { return stubBriefGen{} }

func (stubBriefGen) Generate(ctx context.Context, date string) (*Brief, error) {
	slog.InfoContext(ctx, "stub: generating brief", "date", date)
	// Small delay so the UI loading spinner has a moment to render —
	// helps reviewers see that generation is asynchronous.
	time.Sleep(500 * time.Millisecond)
	return &Brief{
		ID:          date,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Headline: BriefSection{
			Title: "Quiet Sun today (stub)",
			Body:  "No significant solar flares or CMEs reported in the last 24 hours. Set AZURE_OPENAI_ENDPOINT + COSMOS_ENDPOINT for live briefings.",
		},
		EarthWatch: BriefSection{
			Title: "Wildfire monitoring active (stub)",
			Body:  "Several ongoing wildfires tracked by EONET across the western hemisphere. Configure Azure resources for real data.",
		},
		PhotoOfDay: BriefSection{
			Title:    "Full-disk Earth (stub fallback)",
			Body:     "Canned EPIC-style image. Real briefings pull the latest DSCOVR/EPIC photo.",
			ImageURL: "https://epic.gsfc.nasa.gov/archive/natural/2023/01/01/png/epic_1b_20230101002712.png",
		},
		DidYouKnow: BriefSection{
			Title: "Kepler-452b sits ~1400 ly away (stub)",
			Body:  "A confirmed exoplanet in Kepler's data, discovered via the transit method. Configure Azure resources for a real, randomized fact each day.",
		},
	}, nil
}
