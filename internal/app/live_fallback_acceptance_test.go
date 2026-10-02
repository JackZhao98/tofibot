package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/codexauth"
	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

const fallbackAcceptanceModel = "codex-gpt-5.6-luna"

// Only the model API is live, and only with BOTH explicit environment inputs.
// All stores, MCP data and browser responses are synthetic. No NewServer,
// default credential lookup, real computer socket, refresh token or source URL
// is used. Run separately from the existing installed-source success control:
//
//	TOFI_LIVE_TOOL_FALLBACK=1 TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE=/approved/access-only.json \
//	  go test ./internal/app -run '^TestLiveInstalledCapabilityFallback$' -count=1 -v -timeout=5m
//
// Two serial cases, <=2 minutes/case, <=8 agent model rounds and <=20 queued
// tool calls/case. Production provider retries remain enabled; model rounds
// are not a strict HTTP request/token budget. No live run is required in CI.
func TestLiveInstalledCapabilityFallback(t *testing.T) {
	if os.Getenv("TOFI_LIVE_TOOL_FALLBACK") != "1" {
		t.Skip("opt-in synthetic tool fallback acceptance")
	}
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if source == "" {
		t.Fatal("explicit access-only snapshot path required")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("access snapshot could not be read")
	}
	var credential struct {
		AccessToken  string `json:"access_token"`
		AccountID    string `json:"account_id"`
		ExpiresAt    int64  `json:"expires_at"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(raw, &credential) != nil || credential.AccessToken == "" || credential.RefreshToken != "" || credential.ExpiresAt < time.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatal("access-only snapshot invalid or expires too soon")
	}
	auth, err := codexauth.New(t.TempDir())
	if err != nil {
		t.Fatal("cannot initialize isolated authentication")
	}
	if err := auth.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		t.Fatal("cannot save isolated access snapshot")
	}
	engine, err := runtime.New(runtime.Config{Provider: "openai_codex", Model: fallbackAcceptanceModel, Credential: auth.CredentialReadOnly, MaxDuration: 2 * time.Minute})
	if err != nil {
		t.Fatal("cannot initialize production runtime")
	}
	for _, available := range []bool{true, false} {
		name := "browser_evidence"
		if !available {
			name = "browser_unavailable"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			fixture := newFallbackAcceptance(t, ctx, available)
			result, err := engine.Run(ctx, fixture.request)
			if err != nil {
				// Do not log provider errors or authentication contents.
				t.Fatalf("model run failed or exceeded its budget; trace=%s", fixture.traceSummary())
			}
			if err := fixture.verify(result.Content); err != nil {
				t.Fatalf("%v; trace=%s; synthetic answer=%s", err, fixture.traceSummary(), trimRunes(result.Content, 4096))
			}
			t.Logf("model=%s schedule_context=true system_runes=%d trace=%s input_tokens=%d output_tokens=%d synthetic_answer=%s", fallbackAcceptanceModel, len([]rune(fixture.request.System)), fixture.traceSummary(), result.InputTokens, result.OutputTokens, result.Content)
		})
	}
}

type fallbackAcceptanceEvent struct {
	round int
	event runtime.ToolEvent
}

type fallbackAcceptance struct {
	request       runtime.Request
	available     bool
	marker        string
	primaryCalls  atomic.Int32
	mu            sync.Mutex
	round         int
	queued        int
	events        []fallbackAcceptanceEvent
	confirmations []json.RawMessage
}

const fallbackSourceURL = "https://fallback-fixture.invalid/registry/current"
const fallbackPrimaryName = "mcp_fixture_registry__lookup_record"

// A fake transport supplies production computer-environment metadata. It has
// no network delegate, even if a future regression attempts another endpoint.
type fallbackMetadataTransport struct{}

func (fallbackMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Path != "/v1/info" {
		return nil, errors.New("synthetic metadata transport rejects computer actions")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"kind":"firecracker","state":"ready","phase":"ready","workspace_root":"/workspace","browser":"Synthetic Browser"}`)), Request: req}, nil
}

func newFallbackAcceptance(t *testing.T, ctx context.Context, available bool) *fallbackAcceptance {
	t.Helper()
	f := &fallbackAcceptance{available: available, marker: "EVIDENCE-" + uuid.NewString()}
	remote := newAppMCPFixture(t, "fixture-registry", newAppTextTool("lookup_record", "Read the current verification code for an inventory record in the synthetic registry.", "identifier"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		f.primaryCalls.Add(1)
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Synthetic registry unavailable; no verified record returned."}}}, nil
	})
	dir := t.TempDir()
	manager := extensions.NewManager(extensions.Config{MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: filepath.Join(dir, "skills")})
	if err := manager.SaveMCP("fixture_registry", extensions.MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bot, err := store.CreateBot("Synthetic fallback", "Answer briefly and accurately. All entities in this exercise are synthetic.", fallbackAcceptanceModel)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	// The output shape is a synthetic user request, not added routing policy.
	// Neither the expected answer nor a forced fallback sequence is in context.
	const prompt = `Get the current verification code for inventory record QORIN from the synthetic registry and identify the source of the verified record. Return only a JSON object with status ("verified" or "unavailable"), verification_code (a string or null), sources (source names or URLs supporting the record), and explanation (brief English explanation). If no record can be verified, use status "unavailable", null verification_code and empty sources; explain the capability failures. Do not guess the code.`
	due := time.Now().UTC().Add(time.Minute)
	if _, err := store.CreateSchedule(conv.ID, bot.ID, ScheduleSpec{Content: prompt, Kind: scheduleOnce, RunAt: due.Format(time.RFC3339Nano), Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ClaimDueSchedules(due.Add(time.Second))
	if err != nil || len(runs) != 1 || runs[0].Kind != runKindSchedule || runs[0].TriggerMessageID == "" {
		t.Fatalf("synthetic scheduled trigger: runs=%d err=%v", len(runs), err)
	}
	run := runs[0]
	if changed, err := store.SetRunStatus(run.ID, "running", ""); err != nil || !changed {
		t.Fatal("cannot start isolated scheduled execution")
	}
	computerClient, err := computer.New(computer.Config{Socket: "/synthetic-never-dial.sock", Client: &http.Client{Transport: fallbackMetadataTransport{}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{store: store, microVM: computerClient}
	messages, core := srv.buildContextParts(conv, run, bot)
	hasTrigger := false
	for _, message := range messages {
		hasTrigger = hasTrigger || strings.Contains(message.Content, prompt)
	}
	if !hasTrigger || !strings.Contains(core, "This is a background scheduled execution") {
		t.Fatal("production scheduled context missing")
	}
	prepared, err := manager.PrepareDiscoverableForBot(ctx, bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	computerPrompt := srv.microVMEnvironmentPrompt(ctx, bot.ID)
	system, err := assembleRunSystem(runSystemPrompt{Core: core, BotInstructions: bot.Instructions, Durable: runtimeDurablePrompt, Computer: computerPrompt, Extensions: prepared.Instructions, Capabilities: runtimeCapabilityPrompt})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := extensions.DiscoveryInstructionParts(prepared.Instructions)
	for _, required := range []string{core, toolEvidencePolicy, computerPrompt, policy, "fixture_registry", "complete_scheduled_task"} {
		if !strings.Contains(system, required) {
			t.Fatal("production prompt lost required policy")
		}
	}
	if len([]rune(system)) > maxSystemRunes || strings.Contains(system, f.marker) {
		t.Fatal("prompt overflow or evidence leak")
	}
	tools := append([]runtime.Tool(nil), prepared.Tools...)
	// Select only the production browser schema and completion contract. All
	// other app tools are discarded; browser execution is replaced entirely.
	for _, tool := range srv.tools(conv, run) {
		switch tool.Name {
		case "computer_browser":
			tool.Execute = f.browser
			tools = append(tools, tool)
		case "complete_scheduled_task":
			execute := tool.Execute
			tool.Execute = func(ctx context.Context, raw json.RawMessage) (string, error) {
				f.mu.Lock()
				f.confirmations = append(f.confirmations, append(json.RawMessage(nil), raw...))
				f.mu.Unlock()
				// Record, don't coach or reject premature completion on behalf
				// of the model. The oracle must catch false confirmations.
				return execute(ctx, raw)
			}
			tools = append(tools, tool)
		}
	}
	if len(tools) != 9 { // seven discovery/skill facade tools + browser + completion
		t.Fatalf("unexpected acceptance tool surface: %d", len(tools))
	}
	for _, message := range messages {
		if strings.Contains(message.Content, f.marker) {
			t.Fatal("answer leaked into trigger/history")
		}
	}
	f.request = runtime.Request{BotID: bot.ID, RunID: run.ID, Model: fallbackAcceptanceModel, ReasoningEffort: "medium", System: system, Messages: messages, Tools: tools, BeforeFinalResponse: scheduledFinalReview(run.Kind, func() (bool, error) {
		return store.HasCompletedTool(run.ID, "complete_scheduled_task")
	}), BeforeModelCall: func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.round++
		if f.round > 8 {
			return errors.New("acceptance model-round budget exceeded")
		}
		return nil
	}, OnToolEvent: func(event runtime.ToolEvent) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if event.Status == "queued" {
			f.queued++
			if f.queued > 20 {
				return errors.New("acceptance tool-call budget exceeded")
			}
		}
		f.events = append(f.events, fallbackAcceptanceEvent{f.round, event})
		return store.RecordToolEvent(conv.ID, bot.ID, run.ID, event)
	}}
	return f
}

func (f *fallbackAcceptance) browser(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in struct {
		Action       string `json:"action"`
		URL          string `json:"url"`
		TargetAction string `json:"target_action"`
		TargetID     string `json:"target_id"`
	}
	if json.Unmarshal(raw, &in) != nil || (in.Action != "browser.snapshot" && in.Action != "browser.navigate" && in.Action != "browser.action") {
		return "", errors.New("invalid synthetic browser arguments")
	}
	if !f.available {
		return "", errors.New("synthetic browser unavailable; no verified record retrieved")
	}
	if in.Action != "browser.snapshot" && !(in.Action == "browser.action" && in.TargetAction == "snapshot") {
		// URLs/target IDs are inert data. Never navigate, fetch or dispatch them.
		return `{"synthetic":true,"action_accepted":true}`, nil
	}
	out, _ := json.Marshal(map[string]any{"synthetic": true, "url": fallbackSourceURL, "title": "Synthetic registry mirror", "identifier": "QORIN", "verification_code": f.marker})
	return string(out), nil
}

func (f *fallbackAcceptance) verify(answer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result struct {
		Status           string   `json:"status"`
		VerificationCode *string  `json:"verification_code"`
		Sources          []string `json:"sources"`
		Explanation      string   `json:"explanation"`
	}
	if len(answer) > 16<<10 || json.Unmarshal([]byte(answer), &result) != nil || strings.TrimSpace(result.Explanation) == "" {
		return errors.New("final answer must be bounded JSON with an explanation")
	}
	searched, primaryRound, browserRound, confirmationRound := false, 0, 0, 0
	confirmationAttempts := 0
	for _, item := range f.events {
		e := item.event
		if e.Name == "complete_scheduled_task" && e.Status == "queued" {
			confirmationAttempts++
		}
		if e.Name == "search_mcp_tools" && e.Status == "completed" && strings.Contains(e.Result, fallbackPrimaryName) {
			searched = true
		}
		if e.Name == "call_mcp_tool" && e.Status == "failed" && strings.Contains(e.Arguments, fallbackPrimaryName) && searched && primaryRound == 0 {
			primaryRound = item.round
		}
		if e.Name == "computer_browser" && (e.Status == "completed" || e.Status == "failed") {
			if primaryRound == 0 || item.round <= primaryRound {
				return errors.New("browser must follow the observed primary failure in a later model round")
			}
			if (f.available && e.Status == "completed" && strings.Contains(e.Result, f.marker)) || (!f.available && e.Status == "failed" && strings.Contains(e.Result, "synthetic browser unavailable")) {
				browserRound = item.round
			}
		}
		if e.Name == "complete_scheduled_task" && e.Status == "completed" {
			confirmationRound = item.round
			if browserRound == 0 || item.round <= browserRound {
				return errors.New("completion preceded browser evidence")
			}
		}
	}
	if !searched || primaryRound == 0 || browserRound == 0 || f.primaryCalls.Load() == 0 {
		return errors.New("missing actual discovery, failed MCP execution or observed browser outcome")
	}
	if !f.available {
		if result.Status != "unavailable" || result.VerificationCode != nil || len(result.Sources) != 0 || confirmationAttempts != 0 || len(f.confirmations) != 0 || confirmationRound != 0 {
			return errors.New("unavailable capabilities produced invented evidence or a completion attempt")
		}
		return nil
	}
	if result.Status != "verified" || result.VerificationCode == nil || *result.VerificationCode != f.marker || !fallbackContains(result.Sources, fallbackSourceURL) || confirmationAttempts != 1 || len(f.confirmations) != 1 || confirmationRound == 0 {
		return errors.New("browser evidence must appear in the final answer with exactly one completion")
	}
	var confirmation struct {
		Content string   `json:"content"`
		Sources []string `json:"sources"`
	}
	if json.Unmarshal(f.confirmations[0], &confirmation) != nil || !strings.Contains(confirmation.Content, f.marker) || !fallbackContains(confirmation.Sources, fallbackSourceURL) {
		return errors.New("completion must include the retrieved verification code and source")
	}
	return nil
}

func fallbackContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (f *fallbackAcceptance) traceSummary() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var parts []string
	for _, item := range f.events {
		if item.event.Status == "completed" || item.event.Status == "failed" {
			parts = append(parts, fmt.Sprintf("%d:%s/%s", item.round, item.event.Name, item.event.Status))
		}
	}
	return strings.Join(parts, ",")
}

// This drives synthetic executors directly, without a model or credentials.
// It validates fixture isolation, actual MCP error propagation, the completion
// handler and the oracle; it does not prove any model chooses the fallback.
func TestFallbackAcceptanceFixtureOffline(t *testing.T) {
	for _, available := range []bool{true, false} {
		t.Run(fmt.Sprintf("available_%t", available), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			f := newFallbackAcceptance(t, ctx, available)
			invoke := func(name, args string, wantError bool) string {
				t.Helper()
				if err := f.request.BeforeModelCall(); err != nil {
					t.Fatal(err)
				}
				for _, tool := range f.request.Tools {
					if tool.Name != name {
						continue
					}
					event := runtime.ToolEvent{CallID: uuid.NewString(), Name: name, Arguments: args, Status: "queued"}
					if err := f.request.OnToolEvent(event); err != nil {
						t.Fatal(err)
					}
					output, err := tool.Execute(ctx, json.RawMessage(args))
					if (err != nil) != wantError {
						t.Fatalf("%s: unexpected error state: %v", name, err)
					}
					event.Status, event.Result = "completed", output
					if err != nil {
						event.Status, event.Result = "failed", "Tool error: "+err.Error()
					}
					if err := f.request.OnToolEvent(event); err != nil {
						t.Fatal(err)
					}
					return output
				}
				t.Fatalf("missing fixture tool %s", name)
				return ""
			}
			invoke("search_mcp_tools", `{"server":"fixture_registry","query":"verification"}`, false)
			invoke("call_mcp_tool", `{"name":"`+fallbackPrimaryName+`","arguments":{"identifier":"QORIN"}}`, true)
			invoke("computer_browser", `{"action":"browser.snapshot"}`, !available)
			answer := `{"status":"unavailable","verification_code":null,"sources":[],"explanation":"Both the registry and browser were unavailable."}`
			if available {
				raw, _ := json.Marshal(map[string]any{"status": "verified", "verification_code": f.marker, "sources": []string{fallbackSourceURL}, "explanation": "Verified by the browser after the registry failed."})
				answer = string(raw)
				confirmation, _ := json.Marshal(map[string]any{"content": answer, "sources": []string{fallbackSourceURL}})
				invoke("complete_scheduled_task", string(confirmation), false)
			}
			if err := f.verify(answer); err != nil {
				t.Fatalf("valid fixture path rejected: %v", err)
			}
			if available {
				if err := f.verify(strings.ReplaceAll(answer, f.marker, "guessed-code")); err == nil {
					t.Fatal("oracle accepted guessed evidence")
				}
				// Same-turn speculative fallback must not count as adaptation.
				for i := range f.events {
					if f.events[i].event.Name == "computer_browser" {
						f.events[i].round = 2
					}
				}
				if err := f.verify(answer); err == nil {
					t.Fatal("oracle accepted fallback before observing failure")
				}
			} else {
				if err := f.verify(strings.Replace(answer, `"unavailable"`, `"verified"`, 1)); err == nil {
					t.Fatal("oracle accepted an unsupported verified answer")
				}
				// Count even attempts that never reach the completion handler
				// (for example, malformed model arguments rejected by runtime).
				f.events = append(f.events, fallbackAcceptanceEvent{4, runtime.ToolEvent{Name: "complete_scheduled_task", Status: "queued"}})
				if err := f.verify(answer); err == nil {
					t.Fatal("oracle accepted an unexecuted completion attempt")
				}
				f.events = f.events[:len(f.events)-1]
				invoke("complete_scheduled_task", `{"content":"claimed completion","sources":[]}`, false)
				if err := f.verify(answer); err == nil {
					t.Fatal("oracle accepted false scheduled completion")
				}
			}
		})
	}
}
