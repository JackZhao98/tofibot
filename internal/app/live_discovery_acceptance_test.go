package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/codexauth"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Only the model API is real. Every source, schema, result and user request is
// synthetic; the browser fallback cannot access a computer or external source.
func TestLiveInstalledCapabilityDiscovery(t *testing.T) {
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if os.Getenv("TOFI_LIVE_DISCOVERY") != "1" || source == "" {
		t.Skip("opt-in synthetic installed-capability discovery acceptance")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("access snapshot could not be read")
	}
	var credential struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if json.Unmarshal(raw, &credential) != nil || credential.AccessToken == "" || credential.ExpiresAt < time.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatal("access snapshot unavailable or expires too soon")
	}
	dir := t.TempDir()
	auth, err := codexauth.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		t.Fatal("cannot save isolated access snapshot")
	}
	engine, err := runtime.New(runtime.Config{Provider: "openai_codex", Model: "codex-gpt-5.6-luna", Credential: auth.Credential, MaxDuration: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	manager := extensions.NewManager(extensions.Config{MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: filepath.Join(dir, "skills")})
	type fixture struct{ name, tool, description, prompt, marker string }
	fixtures := []fixture{
		{name: "specimen-catalog", tool: "inspect_sample", description: "Read the current storage status and verification code for a laboratory specimen by specimen identifier. Synthetic acceptance data only.", prompt: "请查一下虚构实验室里样本LUMET目前的存放状态，并告诉我记录里的核验码。", marker: "SPECIMEN-" + uuid.NewString()},
		{name: "museum-inventory", tool: "read_inventory", description: "Read the current available item count and verification code for a museum exhibit supply by item identifier. Synthetic acceptance data only.", prompt: "请查一下虚构博物馆里NACRE展品底座当前可用数量，并告诉我记录里的核验码。", marker: "INVENTORY-" + uuid.NewString()},
	}
	for _, f := range fixtures {
		f := f
		remote := newAppMCPFixture(t, f.name, newAppTextTool(f.tool, f.description, "identifier"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			value, _ := json.Marshal(map[string]any{"synthetic": true, "status": "stored", "available_count": 37, "verification_code": f.marker, "source": f.name})
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(value)}}}, nil
		})
		if err = manager.SaveMCP(f.name, extensions.MCPServerConfig{URL: remote.URL}, false); err != nil {
			t.Fatal(err)
		}
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("Synthetic discovery", "Answer briefly and accurately. All entities in this exercise are synthetic.", "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateBot("Synthetic peer", "Provide a distinct contribution only when invited.", "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	dm, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup("Synthetic capability group", []string{bot.ID, peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	computerPrompt := syntheticComputerPrompt(t, bot.ID)
	scenarios := append(append([]fixture{}, fixtures...), fixture{name: "self-contained", prompt: "把‘灯亮了，门开了。’改写成一句简短的话，只给改写结果。"})
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			prepared, err := manager.PrepareDiscoverableForBot(ctx, "synthetic-discovery")
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			var mu sync.Mutex
			var completed []runtime.ToolEvent
			browserCalls := 0
			tools := append([]runtime.Tool{}, prepared.Tools...)
			tools = append(tools, runtime.Tool{Name: "computer_browser", Description: "Use the browser to inspect external information. This acceptance tool returns synthetic data and never connects to a computer.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}, "additionalProperties": false}, Execute: func(context.Context, json.RawMessage) (string, error) {
				mu.Lock()
				browserCalls++
				mu.Unlock()
				return `{"synthetic":true,"result":"No verified record in this browser fixture."}`, nil
			}})
			conv := dm
			modelBot := bot
			run := Run{ID: uuid.NewString(), BotID: bot.ID, ConversationID: conv.ID}
			if scenario.name == "museum-inventory" {
				conv = group
				run.ConversationID = group.ID
				run.Kind = runKindGroupChat
				modelBot.Instructions = strings.Repeat("Answer briefly and accurately. ", 1000)
			}
			_, core := (&Server{store: store}).buildContextParts(conv, run, modelBot)
			system, err := assembleRunSystem(runSystemPrompt{Core: core, BotInstructions: modelBot.Instructions, Durable: runtimeDurablePrompt, Computer: computerPrompt, Extensions: prepared.Instructions, Capabilities: runtimeCapabilityPrompt})
			if err != nil {
				t.Fatal(err)
			}
			discoveryPolicy, _ := extensions.DiscoveryInstructionParts(prepared.Instructions)
			for _, required := range []string{core, toolEvidencePolicy, computerPrompt, discoveryPolicy, "specimen-catalog", "museum-inventory"} {
				if !strings.Contains(system, required) {
					t.Fatal("production prompt lost required policy or directory")
				}
			}
			if len([]rune(system)) > maxSystemRunes {
				t.Fatal("production prompt exceeds budget")
			}
			t.Logf("production assembly kind=%s system_runes=%d user_instruction_runes=%d", conv.Kind, len([]rune(system)), len([]rune(modelBot.Instructions)))
			result, err := engine.Run(ctx, runtime.Request{BotID: "synthetic-discovery", RunID: uuid.NewString(), Model: "codex-gpt-5.6-luna", ReasoningEffort: "medium", System: system, Messages: []runtime.Message{{Role: "user", Content: scenario.prompt}}, Tools: tools, OnToolEvent: func(event runtime.ToolEvent) error {
				if event.Status == "completed" {
					mu.Lock()
					completed = append(completed, event)
					mu.Unlock()
				}
				return nil
			}})
			if err != nil {
				t.Fatalf("synthetic model run failed: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			names := make([]string, 0, len(completed))
			called := false
			searched := false
			for _, event := range completed {
				names = append(names, event.Name)
				if event.Name == "search_mcp_tools" {
					searched = true
				}
				if event.Name == "call_mcp_tool" && strings.Contains(event.Arguments, scenario.tool) {
					called = true
				}
			}
			if browserCalls != 0 {
				t.Fatal("model used generic browser despite relevant installed synthetic capability")
			}
			if scenario.tool != "" && (!searched || !called || !strings.Contains(result.Content, scenario.marker)) {
				t.Fatalf("installed capability not used with verified synthetic output; tools=%v", names)
			}
			if scenario.tool == "" && len(completed) != 0 {
				t.Fatalf("self-contained writing triggered unnecessary discovery: %v", names)
			}
			t.Logf("scenario=%s tools=%v answer=%s", scenario.name, names, result.Content)
		})
	}
	fmt.Fprintln(os.Stdout, "Synthetic discovery acceptance completed; no real external source or computer accessed.")
}
