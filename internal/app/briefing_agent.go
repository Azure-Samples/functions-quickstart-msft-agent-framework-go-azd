package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

// briefing_agent.go — the multi-agent example. An "editor" MAF agent
// treats four specialist agents as tools via agenttool.New, calls
// each one, and composes their replies into a Brief.
//
//   ┌─────────────────────────────────────────────────────────┐
//   │  BriefingEditor                                         │
//   │    ├─ tool: SpaceHeadlineReporter  → get_apod           │
//   │    ├─ tool: EarthWatcher           → get_earth_events   │
//   │    ├─ tool: PhotoCurator           → get_earth_photo    │
//   │    └─ tool: AsteroidBriefer        → get_near_earth_objects
//   └─────────────────────────────────────────────────────────┘
//
// Note that two specialists reuse the same functool instances the
// chat agent registers in agent.go — a functool is safe to attach to
// any number of MAF agents.
//
// The point of agents-as-tools (vs calling the four tools directly)
// is that each specialist owns its own system prompt and can be
// swapped, extended, or replaced with a different provider without
// touching the editor. The editor just sees four descriptions and
// picks which to call, using the same function-calling machinery it
// would use for an ordinary tool.

// BriefingGenerator drives the multi-agent pipeline. Build once per
// process; the underlying Azure OpenAI client is safe to share across
// concurrent Generate calls.
type BriefingGenerator struct {
	editor *agent.Agent
}

// NewBriefingGenerator wires the specialists + editor against the
// same Azure OpenAI endpoint the chat agent uses.
func NewBriefingGenerator(cred azcore.TokenCredential) (*BriefingGenerator, error) {
	endpoint := requireEnv("AZURE_OPENAI_ENDPOINT")
	deployment := requireEnv("AZURE_OPENAI_DEPLOYMENT")

	client := openai.NewClient(
		azure.WithEndpoint(endpoint, "2024-10-21"),
		azure.WithTokenCredential(cred),
	)

	// Each specialist owns one tool and one system prompt. The
	// `Description` field is what the editor sees as the tool
	// description via agenttool.New, so it must clearly state the
	// specialist's capability.
	newSpecialist := func(name, desc, instructions string, t tool.Tool) *agent.Agent {
		return openaiprovider.NewChatCompletionsAgent(client, openaiprovider.AgentConfig{
			Model:        deployment,
			Instructions: instructions,
			Config: agent.Config{
				Name:        name,
				Description: desc,
				Tools:       []tool.Tool{t},
			},
		})
	}

	spaceHeadline := newSpecialist(
		"SpaceHeadlineReporter",
		"Summarizes NASA's Astronomy Picture of the Day as a headline.",
		"Call get_apod (no arguments needed for today's picture), then write a "+
			"2-3 sentence headline framing today's astronomy image for a general audience.",
		apodTool,
	)

	earthWatcher := newSpecialist(
		"EarthWatcher",
		"Highlights one active natural event on Earth from NASA EONET.",
		"You watch Earth. Call get_earth_events (default status=open) and pick the "+
			"single most significant natural event happening right now (large wildfire, "+
			"active volcano, severe storm). Return 2-3 sentences naming what it is and where.",
		earthEventsTool,
	)

	photoCurator := newSpecialist(
		"PhotoCurator",
		"Fetches today's NASA EPIC full-disk Earth photo and captions it.",
		"Call get_earth_photo and write a 1-2 sentence caption for the returned photo. "+
			"The tool's result includes an image_url field that the editor will read directly "+
			"from the tool output — you don't need to repeat it.",
		earthPhotoTool,
	)

	asteroidBriefer := newSpecialist(
		"AsteroidBriefer",
		"Reports on asteroids passing near Earth today.",
		"Call get_near_earth_objects (no arguments) and summarize the closest or most "+
			"notable approach in 2-3 sentences framed as a 'did you know?' item.",
		neoTool,
	)

	editor := openaiprovider.NewChatCompletionsAgent(client, openaiprovider.AgentConfig{
		Model: deployment,
		Instructions: `You are the editor of NASA Today, a short daily space briefing.

You have four specialist reporters available as tools. Call ALL FOUR — in any order — exactly once each:
  1. SpaceHeadlineReporter — for "headline"
  2. EarthWatcher          — for "earthWatch"
  3. PhotoCurator          — for "photoOfDay" (find image_url in that tool's raw result)
  4. AsteroidBriefer       — for "didYouKnow"

After all four have replied, respond with ONLY a JSON object — no prose, no code fences — matching exactly:

{
  "headline":    {"title": "...", "body": "..."},
  "earthWatch":  {"title": "...", "body": "..."},
  "photoOfDay":  {"title": "...", "body": "...", "imageUrl": "..."},
  "didYouKnow":  {"title": "...", "body": "..."}
}

Rules:
- Each title is <= 8 words.
- Each body is 2-4 sentences of plain English.
- For photoOfDay.imageUrl, use the exact URL from the get_earth_photo tool result. If unavailable, omit the field.
- If a specialist fails, set that section's title to "Unavailable" and body to "Not available today.".`,
		Config: agent.Config{
			Name:        "BriefingEditor",
			Description: "Composes the daily NASA Today briefing from specialist reports.",
			Tools: []tool.Tool{
				agenttool.New(spaceHeadline, agenttool.Config{}),
				agenttool.New(earthWatcher, agenttool.Config{}),
				agenttool.New(photoCurator, agenttool.Config{}),
				agenttool.New(asteroidBriefer, agenttool.Config{}),
			},
		},
	})

	return &BriefingGenerator{editor: editor}, nil
}

// Generate runs the editor once and parses its JSON response.
func (g *BriefingGenerator) Generate(ctx context.Context, date string) (*Brief, error) {
	prompt := fmt.Sprintf(
		"Compose today's briefing (date: %s). Call all four specialists, then return the JSON.",
		date,
	)
	// 4 specialists × (1 tool call + compose) plus the editor's final
	// compose step — 120s is roomy for the whole multi-agent loop.
	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	resp, err := g.editor.RunText(runCtx, prompt).Collect()
	if err != nil {
		return nil, fmt.Errorf("briefing editor run: %w", err)
	}
	text := resp.String()
	slog.InfoContext(ctx, "briefing editor completed", "date", date, "chars", len(text))

	brief, err := parseBriefJSON(text)
	if err != nil {
		return nil, fmt.Errorf("parse briefing JSON: %w", err)
	}
	brief.ID = date
	brief.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	fillMissingSections(brief)
	return brief, nil
}

// parseBriefJSON is tolerant of models that wrap JSON in ``` fences
// or add a stray sentence — it grabs the first '{' through the last '}'.
func parseBriefJSON(text string) (*Brief, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object found in editor response")
	}
	var b Brief
	if err := json.Unmarshal([]byte(text[start:end+1]), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// fillMissingSections backfills any section the editor forgot so
// every card in the UI still renders. Reliability > completeness
// for a demo.
func fillMissingSections(b *Brief) {
	fill := func(s *BriefSection) {
		if strings.TrimSpace(s.Title) == "" {
			s.Title = "Unavailable"
		}
		if strings.TrimSpace(s.Body) == "" {
			s.Body = "Not available today."
		}
	}
	fill(&b.Headline)
	fill(&b.EarthWatch)
	fill(&b.PhotoOfDay)
	fill(&b.DidYouKnow)
}
