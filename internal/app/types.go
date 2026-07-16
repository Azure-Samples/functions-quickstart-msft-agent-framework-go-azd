package app

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
)

// ChatRunner is the surface the HTTP handler depends on. Both the real
// MAF-backed implementation (agent.go) and the stub used for offline
// smoke tests (stub.go) satisfy it. Extracting the interface keeps
// handler.go free of runtime-mode branching.
type ChatRunner interface {
	// LoadSession returns a Session hydrated from the given bytes,
	// or a fresh Session when raw is empty.
	LoadSession(ctx context.Context, raw []byte) (*agent.Session, error)

	// Run drives one turn of the agent against userMessage, mutating
	// sess in place and emitting SSE frames via emit(event, payload).
	// Frame vocabulary: "text", "tool_call", "tool_result".
	Run(ctx context.Context, sess *agent.Session, userMessage string, emit func(event string, payload any) error) error
}

// SessionSink abstracts the persistence layer so handler.go can be
// tested (and run offline) against an in-memory map. The real
// implementation lives in store.go; the stub lives in stub.go.
type SessionSink interface {
	Load(ctx context.Context, conversationID string) ([]byte, error)
	Save(ctx context.Context, conversationID string, sess *agent.Session) error
	Delete(ctx context.Context, conversationID string) error
}

// Brief is the JSON shape written to Cosmos and returned to the UI.
// Kept intentionally flat and small — the UI renders four cards and
// the brief text itself is what a reader cares about; the raw source
// data behind each section is discarded to keep the doc lean.
type Brief struct {
	ID          string       `json:"id"`          // YYYY-MM-DD (UTC)
	GeneratedAt string       `json:"generatedAt"` // RFC3339
	Headline    BriefSection `json:"headline"`
	EarthWatch  BriefSection `json:"earthWatch"`
	PhotoOfDay  BriefSection `json:"photoOfDay"`
	DidYouKnow  BriefSection `json:"didYouKnow"`
}

// BriefSection is the shape of one card. ImageURL is optional and
// only populated for the photo-of-day section today.
type BriefSection struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	ImageURL string `json:"imageUrl,omitempty"`
}

// BriefGenerator produces a Brief for a given date. The real
// implementation drives the multi-agent editor pipeline
// (briefing_agent.go); the stub returns fixed sample content
// (stub.go) so /api/brief/today works with no Azure resources.
type BriefGenerator interface {
	Generate(ctx context.Context, date string) (*Brief, error)
}
