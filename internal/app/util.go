package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// util.go — shared helpers used across chat + briefing:
//
//   - HTTP: httpClient + fetchJSON (bounded reads, 10s deadline)
//   - Env:  requireEnv (fatal), envOr (default)
//   - MAF:  wrapTool + toMap workaround for typed struct outputs

// ─── HTTP ────────────────────────────────────────────────────────────

// httpClient is shared by every tool. A 10s ceiling keeps a slow
// upstream from hanging the Function invocation past the host's
// per-request budget.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// fetchJSON GETs endpoint and decodes the response body into v. On
// non-200 it returns the status + up to 200 bytes of body context —
// with HTML error pages elided so we don't dump markup into an LLM
// tool result.
func fetchJSON(ctx context.Context, endpoint string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		snippet := strings.TrimSpace(string(body))
		if strings.HasPrefix(snippet, "<") {
			snippet = "non-JSON response body"
		}
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return fmt.Errorf("%s: %s", resp.Status, snippet)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ─── Env ─────────────────────────────────────────────────────────────

// requireEnv returns the value of name or exits fatally. Used at
// process startup for settings that have no sensible default.
func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		slog.Error("missing required environment variable", "name", name)
		os.Exit(1)
	}
	return v
}

// envOr returns the value of name or fallback when unset/empty.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// ─── MAF functool workaround ─────────────────────────────────────────

// wrapTool builds a functool with a typed input and a typed struct
// output, transparently downgrading the output to map[string]any so
// MAF's schema normalizer accepts it. The wire-format JSON is
// identical to what a direct struct output would produce.
//
// See toMap below for the underlying MAF quirk this works around.
func wrapTool[In, Out any](cfg functool.Config, h functool.HandlerFor[In, Out]) tool.FuncTool {
	return functool.MustNew[In, map[string]any](cfg, func(ctx context.Context, in In) (map[string]any, error) {
		out, err := h(ctx, in)
		if err != nil {
			return nil, err
		}
		return toMap(out), nil
	})
}

// toMap converts a typed struct into a map[string]any by round-tripping
// through JSON. MAF's functool.Normalize rejects typed struct outputs
// with "cannot apply defaults to a struct" — the underlying jsonschema
// library only accepts map/slice/primitive instances. Using
// map[string]any as the tool's Out type sidesteps this while
// preserving the struct's json tags on the wire.
func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return m
}
