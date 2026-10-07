package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/codexauth"
	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func contextSurfaceServer(t *testing.T) (*Server, Bot, Conversation) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bot, err := s.store.CreateBot("Synthetic secretary", "Answer briefly.", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	return s, bot, conv
}

func contextSurfaceRun(t *testing.T, s *Server, conv Conversation, bot Bot, content string) Run {
	t.Helper()
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, content, "")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func toolNamed(tools []Tool, name string) (Tool, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

func memoryBlock(messages []runtime.Message) string {
	for _, m := range messages {
		if strings.HasPrefix(m.Content, "[memory data]") {
			return m.Content
		}
	}
	return ""
}

func TestSystemPromptIsCacheStableAndTimeTrails(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "What time is it?")
	first, system := s.buildContext(conv, run, bot)
	_, again := s.buildContext(conv, run, bot)
	if system != again {
		t.Fatal("system prompt changed between identical builds")
	}
	if regexp.MustCompile(`\d{2}:\d{2}|\d{4}-\d{2}-\d{2}`).MatchString(system) {
		t.Fatalf("system prompt carries volatile time: %s", system)
	}
	last := first[len(first)-2]
	if last.Role != "user" || !strings.HasPrefix(last.Content, "[run context]") || !strings.Contains(first[len(first)-1].Content, "What time is it?") {
		t.Fatalf("run context must trail history just before the current request: %+v", first[len(first)-2:])
	}
	if !regexp.MustCompile(`Current time: \d{4}-\d{2}-\d{2} \d{2}:\d{2} `).MatchString(last.Content) || regexp.MustCompile(`\d{2}:\d{2}:\d{2}`).MatchString(last.Content) || !strings.Contains(last.Content, "UTC+00:00") {
		t.Fatalf("run context lacks minute-precision time: %q", last.Content)
	}
	if !strings.Contains(system, "run context shows UTC time") || !strings.Contains(system, "User timezone is not configured") {
		t.Fatal("system prompt no longer points at the run context time")
	}
}

func TestResearchGuidanceAppearsOnce(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "Find the current synthetic inventory figure.")
	_, system := s.buildContext(conv, run, bot)
	if strings.Count(system, toolEvidencePolicy) != 1 || len(toolEvidencePolicy) > 900 {
		t.Fatalf("evidence policy count=%d len=%d", strings.Count(system, toolEvidencePolicy), len(toolEvidencePolicy))
	}
	for _, repeated := range []string{"Never bypass a denied target/action", "precise blocker and evidence gap", "Promise future work"} {
		if strings.Contains(researchGuide, repeated) || strings.Contains(commitmentGuide, repeated) {
			t.Fatalf("optional guide repeats system rule %q", repeated)
		}
	}
	if strings.Count(system, "Promise future work") != 1 || strings.Contains(system, "computer_help(browser);") {
		t.Fatal("system prompt duplicated a rule or forced computer_help")
	}
}

func TestSaveMemoryDeduplicatesNormalizedText(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "remember this")
	save, _ := toolNamed(s.tools(conv, run), "save_memory")
	call := func(content string) string {
		raw, _ := json.Marshal(map[string]string{"title": "Tea", "description": "Drink preference", "content": content})
		out, err := save.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	id := call("User prefers green tea.")
	if dup := call("  user PREFERS   green tea!  "); dup != "already saved; existing memory id "+id {
		t.Fatalf("duplicate save=%q", dup)
	}
	if other := call("User prefers black coffee."); other == id || strings.HasPrefix(other, "already saved") {
		t.Fatalf("distinct memory deduplicated: %q", other)
	}
	ms, err := s.store.Memories(bot.DMConversationID, bot.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("memories=%d err=%v", len(ms), err)
	}
}

func TestMemoryContextIsNewestFirstWithAliases(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	var ids []string
	for _, content := range []string{"oldest synthetic fact", "middle synthetic fact", "newest synthetic fact"} {
		m, err := s.store.AddMemory(conv.ID, bot.ID, content)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
		time.Sleep(2 * time.Millisecond)
	}
	run := contextSurfaceRun(t, s, conv, bot, "hello")
	messages, _ := s.buildContextParts(conv, run, bot)
	block := memoryBlock(messages)
	newest, oldest := strings.Index(block, "[m:"+ids[2][:8]+"] newest"), strings.Index(block, "[m:"+ids[0][:8]+"] oldest")
	if newest < 0 || oldest < 0 || newest > oldest {
		t.Fatalf("memory block not newest-first with aliases: %q", block)
	}
	update, _ := toolNamed(s.longTermMemoryTools(conv, run), "update_memory")
	raw, _ := json.Marshal(map[string]string{"id": "[m:" + ids[0][:8] + "]", "title": "Fact", "description": "Corrected", "content": "corrected synthetic fact"})
	if _, err := update.Execute(context.Background(), raw); err != nil {
		t.Fatalf("update by alias: %v", err)
	}
	if m, err := s.store.GetMemory(ids[0]); err != nil || m.Content != "corrected synthetic fact" {
		t.Fatalf("alias update=%+v err=%v", m, err)
	}

	for i := 0; i < 6; i++ {
		if _, err := s.store.AddMemory(conv.ID, bot.ID, fmt.Sprintf("bulk %d %s", i, strings.Repeat("x", 2000))); err != nil {
			t.Fatal(err)
		}
	}
	messages, _ = s.buildContextParts(conv, run, bot)
	block = memoryBlock(messages)
	if !strings.Contains(block, "use list_memory") || strings.Contains(block, "search_history") || len([]rune(block)) > maxMemoryRunes+64 {
		t.Fatalf("overflow marker=%q", block[len(block)-120:])
	}
	if !strings.Contains(block, "bulk 5") || strings.Contains(block, "corrected synthetic fact") {
		t.Fatal("budget did not keep the newest memories")
	}
}

func TestDMMemorySavedByToolIsInjectedNextRun(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "Remember my synthetic locker code label")
	save, _ := toolNamed(s.tools(conv, run), "save_memory")
	raw, _ := json.Marshal(map[string]string{"title": "Locker", "description": "Label", "content": "Locker label is ORCHID-7."})
	if _, err := save.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SetRunStatus(run.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	next := contextSurfaceRun(t, s, conv, bot, "What is my locker label?")
	messages, _ := s.buildContextParts(conv, next, bot)
	if !strings.Contains(memoryBlock(messages), "Locker label is ORCHID-7.") {
		t.Fatal("DM memory saved by the tool is missing from the next run")
	}
	list, _ := toolNamed(s.longTermMemoryTools(conv, next), "list_memory")
	out, err := list.Execute(context.Background(), nil)
	if err != nil || !strings.Contains(out, "ORCHID-7") {
		t.Fatalf("list_memory=%s err=%v", out, err)
	}
}

func TestListMemoryIsBounded(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	for i := 0; i < 60; i++ {
		if _, err := s.store.AddMemory(conv.ID, bot.ID, fmt.Sprintf("synthetic memory %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	run := contextSurfaceRun(t, s, conv, bot, "list")
	list, _ := toolNamed(s.longTermMemoryTools(conv, run), "list_memory")
	page := func(raw string) memoryListPage {
		out, err := list.Execute(context.Background(), json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		var p memoryListPage
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := page(`{}`)
	if first.Total != 60 || len(first.Memories) != 50 || first.NextOffset != 50 || first.Memories[0].Content != "synthetic memory 59" {
		t.Fatalf("first page total=%d len=%d next=%d", first.Total, len(first.Memories), first.NextOffset)
	}
	second := page(`{"offset":50}`)
	if len(second.Memories) != 10 || second.NextOffset != 0 || second.Memories[9].Content != "synthetic memory 00" {
		t.Fatalf("second page len=%d next=%d", len(second.Memories), second.NextOffset)
	}
}

func TestSummarizedHistoryKeepsOnlySmallOverlap(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	var covered int64
	for i := 0; i < 40; i++ {
		m, _, err := s.store.AddMessage(conv.ID, "user", "", "", fmt.Sprintf("history line %02d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 29 {
			covered = m.Seq
		}
	}
	if _, err := s.store.SaveSummary(conv.ID, covered, "synthetic summary of lines 00-29"); err != nil {
		t.Fatal(err)
	}
	run := contextSurfaceRun(t, s, conv, bot, "continue")
	messages, _ := s.buildContextParts(conv, run, bot)
	var transcript strings.Builder
	for _, m := range messages {
		transcript.WriteString(m.Content + "\n")
	}
	text := transcript.String()
	for i := 0; i < 40; i++ {
		line := fmt.Sprintf("history line %02d", i)
		want := i >= 20
		if strings.Contains(text, line) != want {
			t.Fatalf("line %02d present=%v want %v", i, !want, want)
		}
	}
	if !strings.Contains(text, "synthetic summary of lines 00-29") {
		t.Fatal("summary missing")
	}
}

func TestToolSurfaceIsGatedBySetup(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "hello")
	tools := s.tools(conv, run)
	for _, hidden := range []string{"list_computers", "computer_action", "generate_image"} {
		if _, ok := toolNamed(tools, hidden); ok {
			t.Fatalf("%s exposed without its setup", hidden)
		}
	}
	codex, err := codexauth.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.codex = codex
	if _, ok := toolNamed(s.tools(conv, run), "generate_image"); ok {
		t.Fatal("generate_image exposed without a connected image service")
	}
	if _, err := s.store.db.Exec(`INSERT INTO computers(id,name,platform,token_hash,capabilities,last_seen,created_at) VALUES('mac-1','Synthetic Mac','darwin',x'00','[]',?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	tools = s.tools(conv, run)
	for _, shown := range []string{"list_computers", "computer_action"} {
		if _, ok := toolNamed(tools, shown); !ok {
			t.Fatalf("%s hidden with a paired computer", shown)
		}
	}
	names := func(tools []Tool) string {
		out := make([]string, 0, len(tools))
		for _, tool := range tools {
			out = append(out, tool.Name)
		}
		return strings.Join(out, ",")
	}
	if names(s.tools(conv, run)) != names(tools) {
		t.Fatal("tool order is not deterministic")
	}
}

func TestMCPDiscoveryToolsNeedAConfiguredServer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	manager := extensions.NewManager(extensions.Config{MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: dir, DiscoveryTimeout: time.Second})
	s := &Server{extensions: manager}
	prepared, err := manager.PrepareDiscoverableForBot(ctx, "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.Tools) == 0 {
		t.Fatal("fixture exposes no discovery tools")
	}
	for _, tool := range s.withoutIdleMCPTools(prepared.Tools) {
		if mcpDiscoveryToolNames[tool.Name] {
			t.Fatalf("%s exposed with zero MCP servers", tool.Name)
		}
	}
	if err := manager.SaveMCP("fixture", extensions.MCPServerConfig{URL: "http://127.0.0.1:1/mcp"}, false); err != nil {
		t.Fatal(err)
	}
	configured, err := manager.PrepareDiscoverableForBot(ctx, "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer configured.Close()
	if _, ok := toolNamed(s.withoutIdleMCPTools(configured.Tools), "search_mcp_tools"); !ok {
		t.Fatal("search_mcp_tools hidden with a configured MCP server")
	}
}

func TestBuiltInToolDescriptionsFitProviderLimit(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	peer, err := s.store.CreateBot("Synthetic peer", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.store.CreateGroup("Synthetic group", []string{bot.ID, peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("", "tfb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	client, err := computer.New(computer.Config{Socket: socketDir + "/missing.sock"})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
	if _, err := s.store.db.Exec(`INSERT INTO computers(id,name,platform,token_hash,capabilities,last_seen,created_at) VALUES('mac-1','Synthetic Mac','darwin',x'00','[]',?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	run := contextSurfaceRun(t, s, conv, bot, "hello")
	var all []Tool
	for _, r := range []Run{run, {ID: run.ID, BotID: bot.ID, ConversationID: conv.ID, Kind: runKindSchedule}} {
		all = append(all, s.tools(conv, r)...)
		all = append(all, s.longTermMemoryTools(conv, r)...)
	}
	groupRun := Run{ID: run.ID, BotID: bot.ID, ConversationID: group.ID, Kind: runKindGroupChat}
	all = append(all, s.tools(group, groupRun)...)
	all = append(all, s.contextUsageTool(run.ID), s.userFormTool(conv, run), workflowGuideTool(), computerHelpTool(), s.imageGenerationTool(conv, run, func(context.Context, provider.ImageRequest) ([]byte, error) { return nil, nil }))
	all = append(all, capabilityTool(all))
	for _, tool := range all {
		if len(tool.Description) > 1024 {
			t.Errorf("%s description is %d bytes", tool.Name, len(tool.Description))
		}
	}
}

func TestWorkspaceCapabilitiesIsBounded(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "hello")
	registered := append(s.tools(conv, run), s.longTermMemoryTools(conv, run)...)
	tool := capabilityTool(registered)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	schemaBytes, _ := json.Marshal(registered[0].Parameters)
	if strings.Contains(out, `"parameters"`) || strings.Contains(out, string(schemaBytes)) || !strings.Contains(out, `"save_memory"`) {
		t.Fatalf("inventory resends schemas: %d bytes", len(out))
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"names":["save_memory","no_such_tool"]}`))
	if err != nil || !strings.Contains(out, `"parameters"`) || !strings.Contains(out, "no_such_tool") || len(out) > maxCapabilitySchemaBytes+1024 {
		t.Fatalf("schema lookup=%s err=%v", out, err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"names":["a","b","c","d","e","f"]}`)); err == nil {
		t.Fatal("unbounded schema request accepted")
	}
}

func TestMicroVMStatusDoesNotBlockRunStart(t *testing.T) {
	socketDir, err := os.MkdirTemp("", "tfb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := socketDir + "/control.sock"
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "firecracker", "state": "ready", "phase": "ready", "workspace_root": "/workspace"})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Close() })
	client, err := computer.New(computer.Config{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client}
	started := time.Now()
	if status := s.microVMStatus(context.Background()); status != "VM status unknown" {
		t.Fatalf("cold status=%q", status)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cold status blocked %s", elapsed)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if status := s.microVMStatus(context.Background()); status == "VM status ready/ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh never populated the cache")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
