package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

// chat_agent.go — the single-agent surface. Wires an Azure OpenAI
// backed MAF ChatAgent with three NASA function tools (defined in
// chat_tools.go) and translates its streaming output into SSE frames
// for chat_handler.go to write.

// ChatAgent bundles the MAF agent with helpers for session hydration.
// One place owns the "how do I build an agent" wiring so main() stays
// focused on Function App concerns.
type ChatAgent struct {
	agent *agent.Agent
}

// NewChatAgent builds an Azure-OpenAI-backed MAF agent authenticated
// with AAD. The endpoint + deployment name come from env vars set by
// the Bicep infra (or from local.settings.json for local dev).
func NewChatAgent(cred azcore.TokenCredential) (*ChatAgent, error) {
	endpoint := requireEnv("AZURE_OPENAI_ENDPOINT")
	deployment := requireEnv("AZURE_OPENAI_DEPLOYMENT")

	client := openai.NewClient(
		azure.WithEndpoint(endpoint, "2024-10-21"),
		azure.WithTokenCredential(cred),
	)

	a := openaiprovider.NewChatCompletionsAgent(
		client,
		openaiprovider.AgentConfig{
			Model: deployment,
			Instructions: "You are a friendly assistant running inside an Azure Function, " +
				"specialized in NASA data.\n\n" +
				"You have three tools, and you MUST use them for the corresponding topics — " +
				"never answer from your own knowledge, never fabricate URLs, dates, or facts:\n" +
				"  • ANY question about the Astronomy Picture of the Day, today's / a specific day's " +
				"space image, APOD, nebulae, galaxies, or 'space picture' → call get_apod.\n" +
				"  • ANY question about asteroids, near-earth objects, NEOs, or things flying " +
				"past Earth → call get_near_earth_objects.\n" +
				"  • ANY question about Mars, Mars rovers, Curiosity, Perseverance, or Mars " +
				"photos → call get_mars_rover_photo.\n\n" +
				"After a tool returns, cite the returned title/date verbatim. Do NOT re-list the " +
				"image URLs the tool returned — the UI renders them inline as thumbnails.\n\n" +
				"If a user follows up with 'show me today's picture' or similar within the same " +
				"conversation, call the tool again to get fresh data — do not paraphrase an " +
				"earlier turn.",
			Config: agent.Config{
				Name:  "AzureFunctionsChat",
				Tools: []tool.Tool{apodTool, neoTool, marsTool},
			},
		},
	)

	return &ChatAgent{agent: a}, nil
}

// LoadSession returns a MAF Session hydrated from stored JSON, or a
// fresh one for the first message in a conversation.
//
// The key insight for serverless: agent.Session implements
// json.Marshaler/Unmarshaler, so the entire conversational state
// round-trips through any document store. There is no need to keep
// a chat process alive between turns — Cosmos is the source of truth.
func (c *ChatAgent) LoadSession(ctx context.Context, raw []byte) (*agent.Session, error) {
	if len(raw) == 0 {
		return c.agent.CreateSession(ctx)
	}
	var s agent.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &s, nil
}

// Agent exposes the underlying MAF agent for callers that need the
// raw handle (e.g. tests). Handlers should prefer Run, which owns
// the streaming type-switch that turns MAF events into SSE frames.
func (c *ChatAgent) Agent() *agent.Agent { return c.agent }

// Run drives one turn against userMessage and forwards each
// ResponseUpdate's contents to the appropriate SSE frame. This is
// the method that makes *ChatAgent satisfy ChatRunner. The frame
// vocabulary matches what the UI in ui/index.html expects:
//
//	text        -> a delta of assistant text
//	tool_call   -> the model requested a tool invocation
//	tool_result -> the tool returned (autocall middleware handled it)
//
// The MAF autocall middleware transparently invokes tools and feeds
// results back to the model before the loop ends, so we don't have
// to do anything special beyond observing the FunctionCall/Result
// content types as they stream by.
func (c *ChatAgent) Run(
	ctx context.Context,
	sess *agent.Session,
	userMessage string,
	emit func(event string, payload any) error,
) error {
	for update, err := range c.agent.RunText(
		ctx,
		userMessage,
		agent.WithSession(sess),
		agent.Stream(true),
	) {
		if err != nil {
			return fmt.Errorf("agent run: %w", err)
		}
		if update == nil {
			continue
		}
		for _, content := range update.Contents {
			switch cc := content.(type) {
			case *message.TextContent:
				if cc.Text == "" {
					continue
				}
				if err := emit("text", map[string]string{"text": cc.Text}); err != nil {
					return err
				}
			case *message.FunctionCallContent:
				if err := emit("tool_call", map[string]string{
					"name":      cc.Name,
					"arguments": cc.Arguments,
					"callId":    cc.CallID,
				}); err != nil {
					return err
				}
			case *message.FunctionResultContent:
				// Passing cc.Result through as-is (rather than
				// fmt.Sprint) lets json.Marshal render structured
				// tool payloads. The UI in ui/index.html inspects
				// the resulting object for image URLs and renders
				// them inline — critical for the NASA APOD tool.
				slog.InfoContext(ctx, "tool_result",
					"callId", cc.CallID,
					"result_type", fmt.Sprintf("%T", cc.Result),
				)
				payload := map[string]any{"callId": cc.CallID}
				if cc.Error != nil {
					payload["error"] = cc.Error.Error()
				} else if cc.Result != nil {
					payload["result"] = cc.Result
				}
				if err := emit("tool_result", payload); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
