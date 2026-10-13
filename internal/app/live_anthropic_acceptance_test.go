package app

// Opt-in LIVE acceptance fixture (real Anthropic API). Skipped unless
// TOFI_LIVE_KEY_FILE names a file holding an Anthropic API key. Driven by
// ui/scripts/test-live-acceptance.mjs.
//
// Real: Server, store, MCP manager, tier store, AutoReview pipeline (the
// reviewer is the normal background provider selection, never a stub), the
// routed runtime engine, scheduler and the Web UI. The Claude key is installed
// through the normal provider-settings route (PUT /api/providers/anthropic/key,
// with the workspace ID setting), read from the file at runtime; it is never
// logged, echoed or written anywhere. Local fake: a Notion-like MCP server that
// records every tools/call.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveAcceptanceFixture(t *testing.T) {
	keyFile := os.Getenv("TOFI_LIVE_KEY_FILE")
	manifest := os.Getenv("TOFI_LIVE_MANIFEST")
	if keyFile == "" || manifest == "" {
		t.Skip("opt-in live acceptance: set TOFI_LIVE_KEY_FILE and TOFI_LIVE_MANIFEST (ui/scripts/test-live-acceptance.mjs)")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	key := strings.TrimSpace(string(raw))
	workspace := os.Getenv("TOFI_LIVE_WORKSPACE_ID")
	model := os.Getenv("TOFI_LIVE_MODEL")
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	// Ledger pages are longer, for the tool-heavy scenario.
	e2eFetchPad = func(id string) string {
		if !strings.HasPrefix(id, "ledger-") {
			return ""
		}
		return "\n" + strings.Repeat("Row: synthetic ledger entry with a short note about "+id+".\n", 40)
	}
	defer func() { e2eFetchPad = func(string) string { return "" } }()

	dir, err := os.MkdirTemp("", "tofi-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cfgDir, err := os.MkdirTemp("", "tofi-live-cfg-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cfgDir)
	notion := newE2ENotion()
	defer notion.Close()

	server, err := NewServer(Config{DataDir: dir, Environment: "acceptance", Provider: "anthropic", DefaultModel: model, MCPConfigPath: filepath.Join(cfgDir, "mcp.json"), UIDir: "../../ui/dist"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if server.autoReviewProvider != nil {
		t.Fatal("the live run must use the real reviewer")
	}
	if err = server.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	h := server.Handler()
	do := func(method, path string, body any) (int, string) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, rd)
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	// Normal path: key + workspace ID through the provider-settings route.
	if code, out := do("PUT", "/api/providers/anthropic/key", map[string]string{"key": key, "workspace_id": workspace}); code != 200 {
		t.Fatalf("install Claude key: %d %s", code, liveRedact(out, key))
	}
	key = ""
	if code, out := do("PUT", "/api/model-settings", map[string]string{"model": model, "reasoning_effort": "low"}); code != 200 {
		_, cat := do("GET", "/api/models", nil)
		var parsed struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		_ = json.Unmarshal([]byte(cat), &parsed)
		ids := []string{}
		for _, m := range parsed.Models {
			ids = append(ids, m.ID)
		}
		t.Fatalf("model settings: %d %s (catalog: %v)", code, out, ids)
	}
	for _, step := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/extensions/mcp", map[string]string{"name": "notion", "url": notion.URL, "transport": "streamable_http"}},
		{"POST", "/api/extensions/mcp/notion/test", nil},
	} {
		if code, out := do(step.method, step.path, step.body); code != 200 {
			t.Fatalf("%s %s: %d %s", step.method, step.path, code, out)
		}
	}
	_, catalog := do("GET", "/api/models", nil)

	stop := make(chan struct{})
	var once sync.Once
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acceptance/state":
			calls, lists := notion.snapshot()
			if calls == nil {
				calls = []e2eMCPCall{}
			}
			writeJSON(w, 200, map[string]any{"mcp_calls": calls, "mcp_lists": lists, "model": model, "catalog": json.RawMessage(catalog), "background_model": server.backgroundModel(backgroundReview)})
		case "/acceptance/bot":
			var in struct{ Name, Instructions, Model, Effort string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Model == "" {
				in.Model = model
			}
			if in.Instructions == "" {
				in.Instructions = "You are a concise workspace assistant. Use the connected Notion tools when the user asks about pages."
			}
			bot, err := server.store.CreateBotWithReasoning(in.Name, in.Instructions, in.Model, in.Effort)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, 200, bot)
		case "/acceptance/due":
			var in struct {
				ScheduleID string `json:"schedule_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
			res, err := server.store.db.Exec(`UPDATE schedules SET next_at_utc=? WHERE id=? AND status='active'`, past, in.ScheduleID)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			n, _ := res.RowsAffected()
			writeJSON(w, 200, map[string]int64{"updated": n})
		case "/acceptance/sql":
			var in struct{ Query string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.Query)), "select") {
				http.Error(w, "select only", 400)
				return
			}
			rows, err := server.store.db.Query(in.Query)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			defer rows.Close()
			cols, _ := rows.Columns()
			out := []map[string]any{}
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				row := map[string]any{}
				for i, c := range cols {
					if b, ok := vals[i].([]byte); ok {
						vals[i] = string(b)
					}
					row[c] = vals[i]
				}
				out = append(out, row)
			}
			writeJSON(w, 200, map[string]any{"rows": out})
		case "/acceptance/stop":
			writeJSON(w, 200, map[string]bool{"stopped": true})
			once.Do(func() { close(stop) })
		default:
			h.ServeHTTP(w, r)
		}
	}))
	defer func() { httpServer.CloseClientConnections(); httpServer.Close() }()
	data, _ := json.Marshal(map[string]any{"origin": httpServer.URL, "model": model})
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("live acceptance fixture %s model=%s\n", httpServer.URL, model)
	select {
	case <-stop:
	case <-time.After(90 * time.Minute):
		t.Fatal("live acceptance fixture timed out")
	}
}

func liveRedact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}
