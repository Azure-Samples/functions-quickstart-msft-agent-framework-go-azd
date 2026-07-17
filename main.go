// Command azd-nasa-agent is the Azure Functions entry point.
//
// It intentionally stays tiny — its only job is to detect whether
// external Azure resources are configured, build the agent + store
// (real or stub) once, and register the HTTP routes with the worker
// runtime. Everything else — MAF wiring, NASA tools, Cosmos I/O,
// stub implementations, HTTP handlers, embedded UI — lives inside
// the internal/app package so the repository root stays uncluttered.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure-Samples/functions-quickstart-msft-agent-framework-go-azd/internal/app"
	"github.com/azure/azure-functions-golang-worker/middleware/otelfunc"
	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/worker"
)

// useStubs returns true when either Azure endpoint is unset or still
// contains the "<your-...>" placeholder shipped in local.settings.json.
// This makes `func start` boot cleanly on a fresh checkout so users
// can verify the wiring before provisioning any Azure resources.
func useStubs() bool {
	unset := func(v string) bool { return v == "" || strings.Contains(v, "<your-") }
	return unset(os.Getenv("AZURE_OPENAI_ENDPOINT")) ||
		unset(os.Getenv("COSMOS_ENDPOINT"))
}

func main() {
	ctx := context.Background()

	if useStubs() {
		slog.Warn("running in STUB MODE: no Azure OpenAI or Cosmos DB required. " +
			"Set AZURE_OPENAI_ENDPOINT and COSMOS_ENDPOINT to switch to the real agent.")
		app.SetImplementations(app.NewStubChat(), app.NewStubStore())
		app.SetBriefingImplementations(app.NewStubBriefGenerator())
	} else {
		// DefaultAzureCredential picks up the User-Assigned Managed
		// Identity injected via AZURE_CLIENT_ID when deployed, and
		// falls back to az-cli / VS Code / env credentials locally.
		// One credential, used for BOTH Azure OpenAI and Cosmos DB —
		// no API keys anywhere.
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			slog.Error("azidentity: failed to obtain credential", "err", err)
			os.Exit(1)
		}
		agent, err := app.NewChatAgent(cred)
		if err != nil {
			slog.Error("agent: failed to build", "err", err)
			os.Exit(1)
		}
		store, err := app.NewSessionStore(ctx, cred)
		if err != nil {
			slog.Error("store: failed to build", "err", err)
			os.Exit(1)
		}
		briefGen, err := app.NewBriefingGenerator(cred)
		if err != nil {
			slog.Error("briefing generator: failed to build", "err", err)
			os.Exit(1)
		}
		app.SetImplementations(agent, store)
		app.SetBriefingImplementations(briefGen)
	}

	fa := sdk.FunctionApp()

	// End-to-end tracing: the Function invocation span wraps the whole
	// request; MAF nests its own spans inside because it reads the
	// active trace from ctx. Result in App Insights: one collapsible
	// trace per user turn, showing HTTP -> agent run -> tool calls ->
	// Cosmos I/O.
	fa.Use(otelfunc.Middleware())

	// One HTTP function handles GET/POST/DELETE on the same route so
	// clients get a REST-ish surface (`/api/chat/{id}`). Route params
	// aren't injected into r.PathValue by the Go worker today; the
	// handler parses conversationId out of r.URL.Path itself.
	fa.HTTP("chat", app.ChatHandler,
		sdk.WithMethods("GET", "POST", "DELETE"),
		sdk.WithAuth("anonymous"),
		sdk.WithRoute("chat/{conversationId}"),
	)

	// A tiny embedded chat UI so reviewers can hit the sample in a
	// browser — no curl gymnastics required to see streaming + tools.
	fa.HTTP("ui", app.UIHandler,
		sdk.WithMethods("GET"),
		sdk.WithAuth("anonymous"),
		sdk.WithRoute("ui"),
	)

	// Daily briefing: one GET endpoint. Each call re-runs the
	// multi-agent editor + specialists synchronously so the demo
	// shows the pipeline end-to-end on every request.
	fa.HTTP("brief_today", app.BriefTodayHandler,
		sdk.WithMethods("GET"),
		sdk.WithAuth("anonymous"),
		sdk.WithRoute("brief/today"),
	)

	worker.Start(fa)
}
