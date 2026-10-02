package extensions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPrepareRemoteMCPPreservesOriginalNamesAndHandlesErrors(t *testing.T) {
	var calls atomic.Int32
	mcpServer := fixtureServer("fixture", "1", 1)
	for _, name := range []string{"a-b", "a_b"} {
		toolName := name
		mcpServer.AddTool(fixtureTool(toolName), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			return fixtureText(req.Params.Name), nil
		})
	}
	mcpServer.AddTool(fixtureTool("bad"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureError("fixture failure"), nil
	})
	mcpServer.AddTool(fixtureTool("slow"), func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return fixtureText("late"), nil
		}
	})
	httpServer := fixtureHTTPServer(mcpServer)
	defer httpServer.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "mcp.json")
	config := map[string]any{"mcpServers": map[string]any{
		"A-B":    map[string]any{"url": httpServer.URL},
		"A_B":    map[string]any{"url": httpServer.URL},
		"broken": map[string]any{"url": "http://127.0.0.1:1/mcp"},
	}}
	b, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := NewManager(Config{MCPConfigPath: configPath, ToolTimeout: 20 * time.Millisecond}).Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Diagnostics) != 1 || p.Diagnostics[0].Server != "broken" {
		t.Fatalf("diagnostics=%#v", p.Diagnostics)
	}
	if len(p.Tools) != 8 {
		t.Fatalf("tools=%d", len(p.Tools))
	}
	var collision, bad string
	collisionNames := map[string]bool{}
	for _, tool := range p.Tools {
		if strings.HasSuffix(tool.Name, "__a_b") || strings.HasSuffix(tool.Name, "__a_b__2") {
			collisionNames[tool.Name] = true
		}
		if strings.HasSuffix(tool.Name, "__bad") {
			bad = tool.Name
		}
	}
	for _, tool := range p.Tools {
		if strings.HasSuffix(tool.Name, "__slow") {
			started := time.Now()
			if _, err = tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil || time.Since(started) > time.Second {
				t.Fatalf("timeout tool err=%v elapsed=%v", err, time.Since(started))
			}
		}
	}
	if len(collisionNames) != 2 {
		t.Fatalf("cross-server collision names=%v", collisionNames)
	}
	for name := range collisionNames {
		collision = name
	}
	if collision == "" || bad == "" {
		t.Fatalf("sanitized tools=%q bad=%q", collision, bad)
	}
	got, err := p.Tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || got == "" {
		t.Fatalf("call got=%q err=%v", got, err)
	}
	for _, tool := range p.Tools {
		if tool.Name == bad {
			got, err = tool.Execute(context.Background(), json.RawMessage(`{}`))
			if err == nil || !strings.Contains(got, "fixture failure") || !strings.Contains(err.Error(), "fixture failure") {
				t.Fatalf("isError got=%q err=%v", got, err)
			}
		}
	}
	if calls.Load() == 0 {
		t.Fatal("fixture was not called")
	}
}

func TestPrepareLegacySSEMCPRejectsWithoutConnecting(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "legacy", 500) }))
	defer remote.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := manager.SaveMCP("legacy", MCPServerConfig{URL: remote.URL, Transport: "sse"}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Tools) != 0 || len(p.Diagnostics) != 1 || p.Diagnostics[0].Code != "unsupported_transport" || requests.Load() != 0 {
		t.Fatalf("tools=%d diagnostics=%v requests=%d", len(p.Tools), p.Diagnostics, requests.Load())
	}
	inspection := manager.InspectMCP(context.Background(), "legacy")
	if inspection.AuthRequired || len(inspection.Diagnostics) != 1 || inspection.Diagnostics[0].Code != "unsupported_transport" {
		t.Fatalf("inspection=%+v", inspection)
	}
}

func TestSkillPromptBoundedWithDiagnostic(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(Config{SkillsDir: filepath.Join(dir, "skills")})
	body := strings.Repeat("界", 40_000)
	if err := mgr.InstallSkill("large", map[string][]byte{"SKILL.md": []byte("---\nname: large\ndescription: large\n---\n" + body)}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetSkillEnabled("bot", "large", true); err != nil {
		t.Fatal(err)
	}
	p, err := mgr.PrepareForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Instructions) > maxSkillPromptBytes || !utf8.ValidString(p.Instructions) {
		t.Fatalf("prompt bytes=%d valid=%v", len(p.Instructions), utf8.ValidString(p.Instructions))
	}
	found := false
	for _, d := range p.Diagnostics {
		if strings.Contains(d.Message, "truncated") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics=%#v", p.Diagnostics)
	}
}

func TestSkillsAreReadOnlyAndRootScoped(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "alpha")
	if err := os.MkdirAll(filepath.Join(skillDir, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: alpha\ndescription: Alpha skill\n---\nUse this carefully."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "docs", "note.txt"), []byte("private note"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "beta"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "beta", "secret.txt"), []byte("sibling secret"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := NewManager(Config{SkillsDir: root}).Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Diagnostics) != 0 || len(p.Tools) != 3 {
		t.Fatalf("diagnostics=%#v tools=%d", p.Diagnostics, len(p.Tools))
	}
	read := p.Tools[1]
	got, err := read.Execute(context.Background(), json.RawMessage(`{"name":"alpha"}`))
	if err != nil || !strings.Contains(got, "Use this carefully") {
		t.Fatalf("read skill got=%q err=%v", got, err)
	}
	readFile := p.Tools[2]
	got, err = readFile.Execute(context.Background(), json.RawMessage(`{"name":"alpha","path":"docs/note.txt"}`))
	if err != nil || got != "private note" {
		t.Fatalf("read file got=%q err=%v", got, err)
	}
	if _, err = readFile.Execute(context.Background(), json.RawMessage(`{"name":"alpha","path":"../SKILL.md"}`)); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err = readFile.Execute(context.Background(), json.RawMessage(`{"name":"alpha","path":"../beta/secret.txt"}`)); err == nil {
		t.Fatal("sibling skill path accepted")
	}
}

func TestPrepareWithNoConfigDoesNoWorkAndBadSkillIsDiagnostic(t *testing.T) {
	p, err := NewManager(Config{}).Prepare(context.Background())
	if err != nil || len(p.Tools) != 0 || len(p.Diagnostics) != 0 {
		t.Fatalf("empty prepare=%#v err=%v", p, err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bad"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad", "SKILL.md"), []byte("not frontmatter"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err = NewManager(Config{SkillsDir: root}).Prepare(context.Background())
	if err != nil || len(p.Diagnostics) != 1 || len(p.Tools) != 3 {
		t.Fatalf("bad skill=%#v err=%v", p, err)
	}
}

func TestPrepareDiscoveryDeadlineDoesNotHangOnRemoteServer(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer httpServer.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"hang": map[string]any{"url": httpServer.URL}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	p, err := NewManager(Config{MCPConfigPath: path, DiscoveryTimeout: 25 * time.Millisecond}).Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("discovery took too long: %v", elapsed)
	}
	defer func() {
		httpServer.CloseClientConnections()
		_ = p.Close()
	}()
	if len(p.Tools) != 0 || len(p.Diagnostics) == 0 {
		t.Fatalf("deadline result=%#v", p)
	}
}
