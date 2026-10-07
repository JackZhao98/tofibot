package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ordinaryResearchContext(t *testing.T, kind, input string) (*Server, Bot, Conversation, Run) {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bot, err := store.CreateBot("Synthetic researcher", "Answer accurately.", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "group" {
		peer, err := store.CreateBot("Synthetic peer", "", "synthetic")
		if err != nil {
			t.Fatal(err)
		}
		conv, err = store.CreateGroup("Synthetic research", []string{bot.ID, peer.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, run, _, err := store.AddUserRun(conv.ID, bot.ID, input, "ordinary-research")
	if err != nil {
		t.Fatal(err)
	}
	if kind == "group" {
		run.Kind = runKindGroupChat
	}
	if _, err := store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	return &Server{store: store}, bot, conv, run
}

// These contract tests exercise production context assembly, not model judgment.
// Restrictions are user messages, never special-case production routing rules.
func TestOrdinaryResearchPolicyPreservesRestrictionsAndLongRoles(t *testing.T) {
	for _, kind := range []string{"dm", "group"} {
		for _, restriction := range []string{
			"Find the current inventory status. Do not use any browser or computer.",
			"Access to that target was denied. Stop that action and report the blocker.",
			"Approval expired. Report what remains incomplete without repeating the write.",
			"The write's outcome is uncertain. Verify its effect before doing anything that could duplicate it.",
		} {
			t.Run(kind+"/"+strings.Split(restriction, ".")[0], func(t *testing.T) {
				s, bot, conv, run := ordinaryResearchContext(t, kind, restriction)
				messages, core := s.buildContextParts(conv, run, bot)
				found := false
				for _, message := range messages {
					found = found || strings.Contains(message.Content, restriction)
				}
				if !found {
					t.Fatal("user restriction lost from ordinary context")
				}
				for _, role := range []string{bot.Instructions, strings.Repeat("User-authored role. ", 1500)} {
					system, err := assembleRunSystem(runSystemPrompt{Core: core, BotInstructions: role})
					if err != nil {
						t.Fatal(err)
					}
					for _, required := range []string{toolEvidencePolicy,
						"Missing/disconnected", "empty/stale/irrelevant", "when exposed",
						"precise blocker and evidence gap", "Never bypass a denied target/action or browser prohibition",
						"independent authorization and its own approvals", "Expired approval stops its action",
						"Verify uncertain action outcomes before retrying or switching routes"} {
						if !strings.Contains(system, required) {
							t.Fatalf("missing mandatory research boundary %q", required)
						}
					}
					if len([]rune(system))+runtime.SystemPromptOverheadRunes() > maxSystemRunes || strings.Contains(system, "complete_scheduled_task") {
						t.Fatal("ordinary context overflowed or loaded scheduled execution")
					}
				}
			})
		}
	}
	for _, required := range []string{"denied target/action", "browser prohibition", "independent authorization", "approvals", "Expired approval", "uncertain", "precise blocker and evidence gap"} {
		if !strings.Contains(computerBrowserGuide, required) {
			t.Fatalf("optional help weakened boundary %q", required)
		}
		if !strings.Contains(toolEvidencePolicy, required) {
			t.Fatalf("always-present policy lost boundary %q", required)
		}
	}
	// The research guide extends the always-present block without repeating it.
	for _, repeated := range []string{"denied target/action", "browser prohibition", "independent authorization", "Expired approval", "precise blocker and evidence gap"} {
		if strings.Contains(researchGuide, repeated) {
			t.Fatalf("research guide repeats system boundary %q", repeated)
		}
	}
}

// The scripted provider chooses the calls. This proves the production ToolsOnly
// surface and explicit desktop-start recipe work with synthetic source failures;
// it does not prove a live model chooses fallback or honors semantic restrictions.
func TestOrdinaryResearchFallbackToolPaths(t *testing.T) {
	for _, kind := range []string{"dm", "group"} {
		for _, scenario := range []string{"missing-mcp", "disconnected-mcp", "empty-data", "stale-data", "source-error", "stopped-desktop", "unavailable-vm", "startup-failure", "unconfigured-vm"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				s, bot, conv, run := ordinaryResearchContext(t, kind, "Find the current verification code for synthetic inventory record VELIN and identify its source.")
				manager := extensions.NewManager(extensions.Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), SkillsDir: t.TempDir(), DiscoveryTimeout: time.Second})
				var calls []reviewCall
				if scenario == "disconnected-mcp" {
					remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						http.Error(w, "Synthetic source disconnected", http.StatusServiceUnavailable)
					}))
					t.Cleanup(remote.Close)
					if err := manager.SaveMCP("fixture", extensions.MCPServerConfig{URL: remote.URL}, false); err != nil {
						t.Fatal(err)
					}
					calls = append(calls, reviewCall{"search_mcp_tools", `{"server":"fixture","query":"lookup"}`})
				} else if scenario == "empty-data" || scenario == "stale-data" || scenario == "source-error" {
					data := `{"records":[]}`
					if scenario == "stale-data" {
						data = `{"code":"OBSOLETE","as_of":"2000-01-01T00:00:00Z"}`
					}
					remote := newAppMCPFixture(t, "fixture", newAppTextTool("lookup", "Read current inventory verification code", "identifier"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
						return &mcp.CallToolResult{IsError: scenario == "source-error", Content: []mcp.Content{&mcp.TextContent{Text: data}}}, nil
					})
					if err := manager.SaveMCP("fixture", extensions.MCPServerConfig{URL: remote.URL, TrustedReadOnlyTools: []string{"lookup"}}, false); err != nil {
						t.Fatal(err)
					}
					calls = append(calls, reviewCall{"search_mcp_tools", `{"server":"fixture","query":"lookup"}`}, reviewCall{"call_mcp_tool", `{"name":"mcp_fixture__lookup","arguments":{"identifier":"VELIN"}}`})
				} else {
					calls = append(calls, reviewCall{"list_mcp_servers", `{}`})
				}
				prepared, err := manager.PrepareDiscoverableForBotWithCallGate(ctx, bot.ID, nil, func(_ context.Context, call extensions.MCPCallApproval) error {
					// Synthetic authorization is exact and read-scoped; no live
					// approval or credential machinery participates in this test.
					if call.Server != "fixture" || call.Tool != "lookup" || string(call.Arguments) != `{"identifier":"VELIN"}` {
						return errors.New("synthetic approval rejects unexpected action")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				defer prepared.Close()
				var actions []string
				started := scenario != "stopped-desktop" && scenario != "startup-failure"
				const marker = "FRESH-SYNTHETIC-EVIDENCE"
				const source = "https://research-fixture.invalid/inventory/VELIN"
				if scenario != "unconfigured-vm" {
					client, err := computer.New(computer.Config{Socket: "/synthetic-never-dial.sock", Client: &http.Client{Transport: reviewTransport(func(req *http.Request) (*http.Response, error) {
						body := `{"kind":"firecracker","state":"ready","phase":"ready","workspace_root":"/workspace"}`
						if req.URL.Path == "/v1/info" {
							if scenario == "unavailable-vm" {
								body = `{"kind":"firecracker","state":"unavailable","error":"synthetic VM unavailable"}`
							}
						} else if req.URL.Path == "/v1/action" {
							var action computer.Action
							if err := json.NewDecoder(req.Body).Decode(&action); err != nil {
								return nil, err
							}
							if action.Name != "desktop.hold" && action.Name != "timezone.set" {
								actions = append(actions, action.Name)
							}
							body = `{"ok":true,"result":{}}`
							switch action.Name {
							case "desktop.hold", "timezone.set":
							case "desktop.start":
								if scenario == "startup-failure" {
									body = `{"ok":false,"error":"synthetic desktop startup failed"}`
								} else {
									started = true
								}
							case "browser.snapshot":
								if !started {
									body = `{"ok":false,"error":"desktop is not running"}`
								} else if len(actions) > 3 {
									body = `{"ok":true,"result":{"url":"` + source + `","code":"` + marker + `","as_of":"` + time.Now().UTC().Format(time.RFC3339) + `"}}`
								}
							case "desktop.click", "desktop.type", "desktop.key":
							default:
								return nil, errors.New("synthetic transport rejects unexpected VM action")
							}
						} else {
							return nil, errors.New("synthetic transport rejects unexpected endpoint")
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
					})}})
					if err != nil {
						t.Fatal(err)
					}
					s.microVM = client
				}
				messages, core := s.buildContextParts(conv, run, bot)
				system, err := assembleRunSystem(runSystemPrompt{Core: core, BotInstructions: bot.Instructions, Computer: s.microVMEnvironmentPrompt(ctx, bot.ID), Extensions: prepared.Instructions})
				if err != nil || !strings.Contains(system, toolEvidencePolicy) || strings.Contains(system, marker) {
					t.Fatalf("ordinary production prompt invalid: %v", err)
				}
				// The browser recipe is inline in the VM environment text, so the
				// script starts the desktop without a computer_help round trip.
				if scenario != "unconfigured-vm" && !strings.Contains(system, computerBrowserEssentials) {
					t.Fatal("VM environment lost its inline browser recipe")
				}
				success := scenario != "unavailable-vm" && scenario != "startup-failure" && scenario != "unconfigured-vm"
				if scenario != "unavailable-vm" && scenario != "unconfigured-vm" {
					calls = append(calls, reviewCall{"computer_desktop", `{"action":"desktop.start"}`})
					if success {
						calls = append(calls, reviewCall{"computer_browser", `{"action":"browser.snapshot"}`}, reviewCall{"computer_desktop", `{"action":"desktop.click","x":100,"y":100}`}, reviewCall{"computer_desktop", `{"action":"desktop.type","text":"VELIN current verification code"}`}, reviewCall{"computer_desktop", `{"action":"desktop.key","key":"Return"}`}, reviewCall{"computer_browser", `{"action":"browser.snapshot"}`})
					}
				}
				tools := append([]runtime.Tool(nil), prepared.Tools...)
				tools = append(tools, s.microVMTools(run)...)
				for _, tool := range tools {
					if tool.Name == "web_search" || tool.Name == "complete_scheduled_task" || (scenario == "unconfigured-vm" && tool.Name == "computer_browser") {
						t.Fatal("fabricated or scheduled tool exposed to ordinary chat")
					}
				}
				var events []runtime.ToolEvent
				_, err = reviewEngine(t, calls, 0).Run(ctx, runtime.Request{BotID: bot.ID, RunID: run.ID, System: system, Messages: messages, Tools: tools, OnToolEvent: func(event runtime.ToolEvent) error {
					if event.Status == "completed" || event.Status == "failed" {
						events = append(events, event)
					}
					return nil
				}})
				if err != nil || len(events) != len(calls) {
					t.Fatalf("scripted runtime failed: %v events=%d calls=%d", err, len(events), len(calls))
				}
				if scenario == "disconnected-mcp" {
					var discovery struct {
						Tools       []any                   `json:"tools"`
						Diagnostics []extensions.Diagnostic `json:"diagnostics"`
					}
					if json.Unmarshal([]byte(events[0].Result), &discovery) != nil || len(discovery.Tools) != 0 || len(discovery.Diagnostics) != 1 {
						t.Fatalf("disconnected source diagnostic missing: %+v", events[0])
					}
				}
				if scenario == "empty-data" || scenario == "stale-data" || scenario == "source-error" {
					if events[1].Name != "call_mcp_tool" || !strings.Contains(events[1].Result, map[string]string{"empty-data": "records", "stale-data": "2000-01-01", "source-error": "records"}[scenario]) {
						t.Fatalf("synthetic source outcome missing: %+v", events[1])
					}
				}
				last := events[len(events)-1]
				if success && (last.Name != "computer_browser" || last.Status != "completed" || !strings.Contains(last.Result, marker) || !strings.Contains(last.Result, source)) {
					t.Fatalf("fallback did not obtain source evidence: %+v actions=%v", last, actions)
				}
				if scenario == "startup-failure" && (last.Status != "failed" || !strings.Contains(last.Result, "synthetic desktop startup failed")) {
					t.Fatalf("startup failure lost concrete blocker: %+v", last)
				}
				if (scenario == "unavailable-vm" || scenario == "unconfigured-vm") && len(actions) != 0 {
					t.Fatalf("unavailable VM was used: %v", actions)
				}
				if scenario == "unavailable-vm" && !strings.Contains(messages[len(messages)-2].Content, "synthetic VM unavailable") {
					t.Fatal("unavailable VM lost its concrete reported blocker")
				}
				t.Logf("scripted %s source-to-browser trace: tools=%d VM actions=%v verified=%v", scenario, len(events), actions, success)
			})
		}
	}
}

// Carry the new DM/group prompt through existing runtime replay fences. A
// scripted repeat must not acquire permission from generic fallback guidance.
func TestOrdinaryResearchFallbackPreservesOutcomeFences(t *testing.T) {
	for _, kind := range []string{"dm", "group"} {
		for _, status := range []string{tooloutcome.Denied, tooloutcome.Expired, tooloutcome.Uncertain} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				s, bot, conv, run := ordinaryResearchContext(t, kind, "Perform only the independently authorized synthetic action and verify its result.")
				_, system := s.buildContext(conv, run, bot)
				effects, observations := 0, 0
				tools := []Tool{
					{Name: "synthetic_action", Parameters: objectSchema(map[string]any{"target": map[string]any{"type": "string"}}, []string{"target"}), Execute: func(context.Context, json.RawMessage) (string, error) {
						effects++
						certainty := "not_executed"
						if status == tooloutcome.Uncertain {
							certainty = "unknown"
						}
						return "", tooloutcome.New(status, "synthetic_boundary", certainty, "Synthetic target/action boundary", "explain_blocker").Err()
					}},
					{Name: "synthetic_inspect", Parameters: objectSchema(nil, nil), Execute: func(context.Context, json.RawMessage) (string, error) {
						observations++
						return "Independently authorized observation; action remains blocked.", nil
					}},
				}
				repeat := `{"target":"synthetic"}`
				if status == tooloutcome.Expired {
					repeat = `{"target":"changed"}`
				}
				var outcomes []runtime.ToolEvent
				_, err := reviewEngine(t, []reviewCall{{"synthetic_action", `{"target":"synthetic"}`}, {"synthetic_inspect", `{}`}, {"synthetic_action", repeat}}, 0).Run(context.Background(), runtime.Request{BotID: bot.ID, RunID: run.ID, System: system, Tools: tools, OnToolEvent: func(event runtime.ToolEvent) error {
					if event.Status == "failed" {
						outcomes = append(outcomes, event)
					}
					return nil
				}})
				if err != nil || effects != 1 || observations != 1 || len(outcomes) != 2 || outcomes[1].Outcome == nil || outcomes[1].Outcome.Status != status {
					t.Fatalf("fallback weakened outcome fence: err=%v effects=%d observations=%d outcomes=%+v", err, effects, observations, outcomes)
				}
			})
		}
	}
}
