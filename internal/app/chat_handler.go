// Package app owns everything except process bootstrap: agent
// wiring, HTTP handlers, session store, stubs, and the embedded UI.
// main.go stays tiny — it only detects stub-mode, builds the
// dependencies, calls SetImplementations, and starts the worker.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Package-level singletons wired by main via SetImplementations.
// Building the agent (token acquisition, TLS, HTTP client) and the
// Cosmos client (endpoint discovery, connection pool) is expensive;
// they're built once and every invocation closes over these vars.
var (
	globalAgent ChatRunner
	globalStore SessionSink
)

// SetImplementations wires the runtime dependencies used by
// ChatHandler and UIHandler. Must be called before FunctionApp
// registration in main.
func SetImplementations(runner ChatRunner, sink SessionSink) {
	globalAgent = runner
	globalStore = sink
}

// ChatHandler multiplexes on HTTP method so all three verbs share a
// single Functions route registration (chat/{conversationId}).
func ChatHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	convID := conversationIDFromRequest(r)
	if convID == "" {
		http.Error(w, "conversationId is required in the route", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		handlePost(ctx, w, r, convID)
	case http.MethodGet:
		handleGet(ctx, w, convID)
	case http.MethodDelete:
		handleDelete(ctx, w, convID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type postBody struct {
	Message string `json:"message"`
}

// handlePost is the interesting path: load session -> run agent with
// streaming -> forward tokens as Server-Sent Events -> save session.
//
// The response is streamed via http.Flusher, which the Go worker's
// HTTP-streaming path (Functions host's HttpUri capability) supports
// natively. See samples/httpStreaming in the worker repo for the
// underlying pattern.
//
// SSE frame vocabulary emitted here:
//
//	event: text        (default; may be omitted)  data: {"text": "..."}
//	event: tool_call   data: {"name": "...", "arguments": "..."}
//	event: tool_result data: {"callId": "...", "result": "...", "error": "..."}
//	event: error       data: {"error": "..."}
//	event: done        data: {}
//
// The UI in ui/index.html consumes exactly this vocabulary.
func handlePost(ctx context.Context, w http.ResponseWriter, r *http.Request, convID string) {
	var body postBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported by this response writer", http.StatusInternalServerError)
		return
	}

	raw, err := globalStore.Load(ctx, convID)
	if err != nil {
		slog.ErrorContext(ctx, "load session", "err", err, "conversation_id", convID)
		http.Error(w, "failed to load conversation", http.StatusInternalServerError)
		return
	}
	sess, err := globalAgent.LoadSession(ctx, raw)
	if err != nil {
		slog.ErrorContext(ctx, "hydrate session", "err", err, "conversation_id", convID)
		http.Error(w, "failed to hydrate conversation", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// SSE requires that data payloads contain no embedded newlines.
	// json.Marshal side-steps that entirely and keeps the client
	// parsing trivial (JSON.parse per frame).
	emit := func(event string, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if event != "" && event != "text" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	slog.InfoContext(ctx, "agent run starting", "conversation_id", convID)

	// The ChatRunner (real MAF-backed or stub) owns the type-switch
	// that turns model deltas / tool calls into SSE frames. The
	// handler just plumbs bytes.
	if err := globalAgent.Run(ctx, sess, body.Message, emit); err != nil {
		slog.ErrorContext(ctx, "agent run", "err", err, "conversation_id", convID)
		_ = emit("error", map[string]string{"error": err.Error()})
		return
	}

	// Persist AFTER a successful run so failed turns don't corrupt the
	// stored history with a half-finished assistant message.
	if err := globalStore.Save(ctx, convID, sess); err != nil {
		slog.ErrorContext(ctx, "save session", "err", err, "conversation_id", convID)
		_ = emit("error", map[string]string{"error": "failed to persist conversation: " + err.Error()})
		return
	}

	_ = emit("done", struct{}{})
}

// handleGet returns the raw persisted session document. We wrap the
// bytes in a stable envelope so the client sees a consistent shape
// even if MAF's internal session schema evolves.
func handleGet(ctx context.Context, w http.ResponseWriter, convID string) {
	raw, err := globalStore.Load(ctx, convID)
	if err != nil {
		slog.ErrorContext(ctx, "load session", "err", err, "conversation_id", convID)
		http.Error(w, "failed to load conversation", http.StatusInternalServerError)
		return
	}
	if raw == nil {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Wrap so the client sees a stable envelope even if MAF's
	// internal session schema evolves.
	fmt.Fprintf(w, `{"conversationId":%q,"session":%s}`, convID, raw)
}

func handleDelete(ctx context.Context, w http.ResponseWriter, convID string) {
	if err := globalStore.Delete(ctx, convID); err != nil {
		slog.ErrorContext(ctx, "delete session", "err", err, "conversation_id", convID)
		http.Error(w, "failed to delete conversation", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// conversationIDFromRequest pulls {conversationId} out of the URL.
// The Go worker doesn't populate r.PathValue for Functions route
// params today — r.URL.Path arrives as e.g. "/api/chat/<id>" — so we
// take the trailing segment ourselves.
func conversationIDFromRequest(r *http.Request) string {
	p := strings.TrimRight(r.URL.Path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
