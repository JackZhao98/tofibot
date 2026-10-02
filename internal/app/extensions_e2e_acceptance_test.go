package app

// This is a local protocol acceptance test. MCP servers are synthetic
// official-SDK fixtures and the engine is a request-capturing stub; no provider or
// user credential is contacted.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type extensionAcceptanceEngine struct {
	mu    sync.Mutex
	req   runtime.Request
	onRun func(context.Context, runtime.Request)
}

func (e *extensionAcceptanceEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	e.mu.Lock()
	e.req = req
	e.mu.Unlock()
	if e.onRun != nil {
		e.onRun(ctx, req)
	}
	return runtime.Result{Content: "captured"}, nil
}

func extensionAcceptanceHTTP(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://acceptance.local"+path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func TestExtensionsHTTPProtocolAndSkillAcceptance(t *testing.T) {
	streamHTTP := newAppMCPFixture(t, "stream", newAppTextTool("stream_echo", "Echo a synthetic value.", "value"), func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &args)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args["value"].(string)}}}, nil
	})
	engine := &extensionAcceptanceEngine{}
	root := t.TempDir()
	s, err := NewServer(Config{DataDir: root, Engine: engine, MCPConfigPath: filepath.Join(root, "mcp.json"), SkillsDir: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("extension acceptance", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	for _, in := range []map[string]any{
		{"name": "stream", "url": streamHTTP.URL, "transport": "streamable_http"},
		{"name": "legacy", "url": streamHTTP.URL + "/sse", "transport": "sse"},
	} {
		if code, body := extensionAcceptanceHTTP(t, h, http.MethodPost, "/api/extensions/mcp", in); code != http.StatusOK {
			t.Fatalf("install code=%d body=%s", code, body)
		}
	}
	for _, name := range []string{"stream", "legacy"} {
		status, body := extensionAcceptanceHTTP(t, h, http.MethodPost, "/api/extensions/mcp/"+name+"/test", nil)
		var inspection struct {
			OK           bool `json:"ok"`
			ToolCount    int  `json:"tool_count"`
			AuthRequired bool `json:"auth_required"`
		}
		wantOK := name == "stream"
		wantTools := 0
		if wantOK {
			wantTools = 1
		}
		if err := json.Unmarshal(body, &inspection); err != nil || status != http.StatusOK || inspection.OK != wantOK || inspection.ToolCount != wantTools || inspection.AuthRequired {
			t.Fatalf("inspection for %s: status=%d body=%s err=%v", name, status, body, err)
		}
	}
	prepared, err := s.extensions.PrepareForBot(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.Diagnostics) != 1 || len(prepared.Tools) < 1 {
		t.Fatalf("prepared diagnostics=%#v tools=%d", prepared.Diagnostics, len(prepared.Tools))
	}
	called := 0
	for _, tool := range prepared.Tools {
		if !strings.HasSuffix(tool.Name, "__stream_echo") {
			continue
		}
		got, callErr := tool.Execute(context.Background(), json.RawMessage(`{"value":"ok"}`))
		if callErr != nil || got != "ok" {
			t.Fatalf("tool=%s got=%q err=%v", tool.Name, got, callErr)
		}
		called++
	}
	if called != 1 {
		t.Fatalf("MCP echo tools called=%d", called)
	}
	if code, _ := extensionAcceptanceHTTP(t, h, http.MethodPost, "/api/extensions/mcp/legacy/test", nil); code != http.StatusOK {
		t.Fatalf("legacy test code=%d", code)
	}
	if code, _ := extensionAcceptanceHTTP(t, h, http.MethodDelete, "/api/extensions/mcp/legacy", nil); code != http.StatusOK {
		t.Fatalf("delete code=%d", code)
	}
	if code, _ := extensionAcceptanceHTTP(t, h, http.MethodPost, "/api/extensions/mcp/legacy/test", nil); code != http.StatusOK {
		t.Fatalf("missing test should return diagnostics, code=%d", code)
	}

	skillBody := "Use the acceptance marker SKILL_PROMPT_MARKER when answering."
	if code, body := extensionAcceptanceHTTP(t, h, http.MethodPost, "/api/extensions/skills", map[string]any{"name": "acceptance-skill", "files": map[string]string{"SKILL.md": "---\nname: acceptance-skill\ndescription: acceptance\n---\n" + skillBody}}); code != http.StatusCreated {
		t.Fatalf("skill install code=%d body=%s", code, body)
	}
	engine.onRun = func(ctx context.Context, req runtime.Request) {
		readSkill := false
		for _, tool := range req.Tools {
			if tool.Name == "read_skill" {
				body, readErr := tool.Execute(ctx, json.RawMessage(`{"name":"acceptance-skill"}`))
				if readErr != nil || !strings.Contains(body, "SKILL_PROMPT_MARKER") {
					t.Fatalf("on-demand skill body=%q error=%v", body, readErr)
				}
				readSkill = true
			}
		}
		if !readSkill {
			t.Fatal("run did not expose on-demand skill reader")
		}
	}
	_, run, _, err := s.store.AddUserRun(b.DMConversationID, b.ID, "hello", "skill-acceptance-1")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	s.execute(c, run)
	engine.mu.Lock()
	system := engine.req.System
	engine.mu.Unlock()
	if strings.Contains(system, "SKILL_PROMPT_MARKER") || !strings.Contains(system, "acceptance-skill") {
		t.Fatalf("skill metadata missing or full body injected: %q", system)
	}

	engine.onRun = nil
	_, run, _, err = s.store.AddUserRun(b.DMConversationID, b.ID, "hello", "skill-acceptance-2")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, run)
	engine.mu.Lock()
	system = engine.req.System
	engine.mu.Unlock()
	if strings.Contains(system, "SKILL_PROMPT_MARKER") {
		t.Fatal("disabled skill remained in prompt")
	}
}

func TestExternalMCPReferenceServerOptIn(t *testing.T) {
	if os.Getenv("TOFI_ACCEPTANCE_EXTERNAL_MCP") != "1" {
		t.Skip("opt-in external MCP reference server acceptance")
	}
	root := t.TempDir()
	s, err := NewServer(Config{DataDir: root, MCPConfigPath: filepath.Join(root, "mcp.json"), SkillsDir: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("external mcp acceptance", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	if code, body := extensionAcceptanceHTTP(t, h, http.MethodPost, "/api/extensions/mcp", map[string]any{"name": "reference", "url": "https://example-server.modelcontextprotocol.io/mcp", "bot_allowlists": map[string][]string{b.ID: {"echo", "add"}}}); code != http.StatusOK {
		t.Fatalf("install external code=%d body=%s", code, body)
	}
	prepared, err := s.extensions.PrepareForBot(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.Diagnostics) != 0 {
		t.Fatalf("external diagnostics=%#v", prepared.Diagnostics)
	}
	called := false
	for _, tool := range prepared.Tools {
		if !strings.HasSuffix(tool.Name, "__echo") && !strings.HasSuffix(tool.Name, "__add") {
			continue
		}
		args := `{"message":"tofi synthetic acceptance"}`
		if strings.HasSuffix(tool.Name, "__add") {
			args = `{"a":2,"b":3}`
		}
		got, callErr := tool.Execute(context.Background(), json.RawMessage(args))
		if callErr != nil {
			t.Fatal(callErr)
		}
		t.Logf("external reference tool %s returned %q", tool.Name, got)
		called = true
		break
	}
	if !called {
		t.Fatalf("external tools=%d, expected echo or add", len(prepared.Tools))
	}
	if code, body := extensionAcceptanceHTTP(t, h, http.MethodDelete, "/api/extensions/mcp/reference", nil); code != http.StatusOK {
		t.Fatalf("remove external code=%d body=%s", code, body)
	}
}

// This is opt-in because it reaches the public GitMCP service. It sends only
// a synthetic repository-documentation query and never reads local files or
// supplies credentials.
func TestExternalGitMCPReadOnlyAcceptance(t *testing.T) {
	if os.Getenv("TOFI_ACCEPTANCE_GITMCP") != "1" {
		t.Skip("opt-in external GitMCP acceptance")
	}
	root := t.TempDir()
	s, err := NewServer(Config{DataDir: root, MCPConfigPath: filepath.Join(root, "mcp.json"), SkillsDir: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("gitmcp acceptance", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	if code, body := extensionAcceptanceHTTP(t, h, http.MethodPost, "/api/extensions/mcp", map[string]any{
		"name": "gitmcp", "url": "https://gitmcp.io/modelcontextprotocol/python-sdk/mcp",
		"bot_allowlists": map[string][]string{b.ID: {}},
	}); code != http.StatusOK {
		t.Fatalf("install GitMCP code=%d body=%s", code, body)
	}
	prepared, err := s.extensions.PrepareForBot(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	t.Logf("GitMCP diagnostics=%#v tools=%d", prepared.Diagnostics, len(prepared.Tools))
	if len(prepared.Tools) == 0 {
		t.Fatalf("GitMCP discovered no tools: %#v", prepared.Diagnostics)
	}
	for _, tool := range prepared.Tools {
		t.Logf("GitMCP tool=%s schema=%s", tool.Name, tool.Parameters)
	}
	var listSkills runtime.Tool
	var readSkill, readSkillFile runtime.Tool
	for _, tool := range prepared.Tools {
		switch tool.Name {
		case "list_skills":
			listSkills = tool
		case "read_skill":
			readSkill = tool
		case "read_skill_file":
			readSkillFile = tool
		}
	}
	if listSkills.Name == "" || readSkill.Name == "" || readSkillFile.Name == "" {
		t.Fatalf("GitMCP exposed no skill documentation tools")
	}
	got, callErr := listSkills.Execute(context.Background(), json.RawMessage(`{}`))
	if callErr != nil {
		t.Fatalf("GitMCP list_skills failed: %v", callErr)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("GitMCP returned an empty documentation result")
	}
	t.Logf("GitMCP list_skills returned %s", got)
	var skills []map[string]any
	if err := json.Unmarshal([]byte(got), &skills); err == nil && len(skills) > 0 {
		name, _ := skills[0]["name"].(string)
		got, callErr = readSkill.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)))
	} else {
		// The public repo currently publishes no named skills, so its read-only
		// documentation tools have no valid name to call. Record that outcome
		// without turning an upstream content change into a product failure.
		t.Skip("GitMCP discovery succeeded but this public repo currently publishes no skills to read")
	}
	if callErr != nil {
		t.Fatalf("GitMCP read_skill failed: %v", callErr)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("GitMCP returned an empty skill documentation result")
	}
	if code, body := extensionAcceptanceHTTP(t, h, http.MethodDelete, "/api/extensions/mcp/gitmcp", nil); code != http.StatusOK {
		t.Fatalf("remove GitMCP code=%d body=%s", code, body)
	}
}
