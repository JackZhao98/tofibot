package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// scriptedProvider answers each call with the next step. Requests are copied
// so tests can compare what the model saw on every call.
type scriptedProvider struct {
	mu       sync.Mutex
	steps    []func(ctx context.Context, req *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error)
	requests []provider.ChatRequest
}

func (p *scriptedProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *scriptedProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	p.mu.Lock()
	copy := *req
	copy.Messages = append([]provider.Message(nil), req.Messages...)
	p.requests = append(p.requests, copy)
	i := len(p.requests) - 1
	p.mu.Unlock()
	if i >= len(p.steps) {
		return nil, errors.New("unexpected extra provider call")
	}
	return p.steps[i](ctx, req, delta)
}

func reply(resp provider.ChatResponse) func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		return &resp, nil
	}
}

func toolCallReply(id, name, args string) func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: id, Name: name, Arguments: args}}})
}

func runLoop(t *testing.T, cfg AgentConfig) (*AgentResult, error) {
	t.Helper()
	cfg.ToolsOnly = true
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	if cfg.Ctx == nil {
		cfg.Ctx = context.Background()
	}
	execCtx := models.NewExecutionContext("loop-latency", "synthetic", t.TempDir())
	defer execCtx.Cancel()
	return RunAgentLoop(cfg, execCtx)
}

func lookupTool(handler func(context.Context, map[string]interface{}) (string, error)) ExtraBuiltinTool {
	return ExtraBuiltinTool{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, HandlerCtx: handler}
}

func TestDropOldToolImagesKeepsNewestTwoAndCountsImages(t *testing.T) {
	messages := []provider.Message{{Role: "user", Content: "look", ImageURLs: []string{"data:user"}}}
	for i := 1; i <= 4; i++ {
		messages = append(messages, provider.Message{Role: "tool", Content: fmt.Sprintf("shot %d", i), ImageURLs: []string{fmt.Sprintf("data:%d", i)}})
	}
	got := dropOldToolImages(messages, keepRecentToolImages)
	if len(got[0].ImageURLs) != 1 || len(got[1].ImageURLs) != 0 || len(got[2].ImageURLs) != 0 || len(got[3].ImageURLs) != 1 || len(got[4].ImageURLs) != 1 {
		t.Fatalf("images = %+v", got)
	}
	if !strings.HasSuffix(got[1].Content, omittedToolImageNote) || strings.Contains(got[4].Content, "omitted") {
		t.Fatalf("markers = %q / %q", got[1].Content, got[4].Content)
	}
	if len(messages[1].ImageURLs) != 1 {
		t.Fatal("input slice was mutated")
	}
	if again := dropOldToolImages(got, keepRecentToolImages); !reflect.DeepEqual(again, got) {
		t.Fatal("second pass changed an already-stripped prefix")
	}
	if with, without := EstimateContextUsage("", messages, nil), EstimateContextUsage("", got, nil); with-without < 2*imageTokenEstimate-100 {
		t.Fatalf("image estimate with=%d without=%d", with, without)
	}
}

// Requests between compaction checkpoints share a byte-identical prefix.
func TestToolResultPrefixStaysStableBetweenCheckpoints(t *testing.T) {
	for _, tc := range []struct {
		name        string
		resultChars int
		maxChanges  int
		minChanges  int
	}{
		{"below-threshold", 2000, 0, 0},
		{"checkpointed", 10000, 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &scriptedProvider{}
			for i := 0; i < 12; i++ {
				p.steps = append(p.steps, toolCallReply(fmt.Sprintf("c%d", i), "lookup", fmt.Sprintf(`{"n":%d}`, i)))
			}
			p.steps = append(p.steps, reply(provider.ChatResponse{Content: "done"}))
			result := strings.Repeat("line of tool output\n", tc.resultChars/20)
			_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return result, nil })}})
			if err != nil {
				t.Fatal(err)
			}
			changes := 0
			for i := 1; i < len(p.requests); i++ {
				prev, next := p.requests[i-1].Messages, p.requests[i].Messages
				if !reflect.DeepEqual(prev, next[:len(prev)]) {
					changes++
				}
			}
			if changes < tc.minChanges || changes > tc.maxChanges {
				t.Fatalf("prefix changed on %d of %d requests", changes, len(p.requests)-1)
			}
			if p.requests[0].PromptCacheKey != "" {
				t.Fatal("unexpected cache key")
			}
		})
	}
}

func TestReasoningItemsReplayAndRejectionDisablesThem(t *testing.T) {
	items := []provider.ReasoningItem{{ID: "rs_1", EncryptedContent: "enc"}}
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "a", Name: "lookup", Arguments: `{}`}}, ReasoningItems: items}),
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "b", Name: "lookup", Arguments: `{"x":1}`}}, ReasoningItems: items, ReasoningReplayRejected: true}),
		reply(provider.ChatResponse{Content: "done"}),
	}}
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", PromptCacheKey: "run-7", ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })}})
	if err != nil {
		t.Fatal(err)
	}
	second := p.requests[1]
	if second.PromptCacheKey != "run-7" || second.OmitReasoningReplay || !reflect.DeepEqual(second.Messages[1].ReasoningItems, items) {
		t.Fatalf("second request = %+v", second)
	}
	third := p.requests[2]
	if !third.OmitReasoningReplay || len(third.Messages[3].ReasoningItems) != 0 {
		t.Fatalf("third request kept replay: omit=%v items=%+v", third.OmitReasoningReplay, third.Messages[3].ReasoningItems)
	}
}

func TestOverlongToolArgumentsAbortAndRetryOnce(t *testing.T) {
	previous := maxToolCallArgumentChars
	maxToolCallArgumentChars = 100
	defer func() { maxToolCallArgumentChars = previous }()
	executed := 0
	resets := 0
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		func(ctx context.Context, _ *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			delta(provider.StreamDelta{Content: "partial "})
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "c", Name: "lookup"}}})
			for i := 0; i < 10; i++ {
				delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, Arguments: `{"text":"` + strings.Repeat("a", 20)}}})
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		func(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			if last := req.Messages[len(req.Messages)-1]; last.Role != "user" || !strings.Contains(last.Content, "Keep tool arguments short") {
				return nil, fmt.Errorf("missing retry note: %+v", last)
			}
			return &provider.ChatResponse{Content: "short answer"}, nil
		},
	}}
	var emitted []provider.Message
	result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnStreamChunk: func(string, string) {}, OnStreamReset: func() { resets++ },
		OnMessage:  func(m provider.Message) { emitted = append(emitted, m) },
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { executed++; return "", nil })}})
	if err != nil || result.Content != "short answer" || len(p.requests) != 2 || executed != 0 || resets != 1 {
		t.Fatalf("result=%+v err=%v calls=%d executed=%d resets=%d", result, err, len(p.requests), executed, resets)
	}
	for _, m := range emitted {
		if len(m.ToolCalls) > 0 {
			t.Fatal("aborted partial tool call entered the transcript")
		}
	}
}

func TestWallCapAndIdleStreamRetryIterationOnce(t *testing.T) {
	block := func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		return nil, fmt.Errorf("stream: %w", &provider.StreamWallCapError{Cap: time.Second})
	}
	idle := func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		return nil, fmt.Errorf("stream: %w", &provider.StreamIdleError{Idle: time.Second})
	}
	final := reply(provider.ChatResponse{Content: "final"})
	for _, tc := range []struct {
		name  string
		steps []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error)
		ok    bool
	}{
		{"wall-cap", append([]func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){block}, final), true},
		{"idle", append([]func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){idle}, final), true},
		{"idle-twice", append([]func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){idle, idle}, final), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &scriptedProvider{steps: tc.steps}
			result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go"})
			if tc.ok && (err != nil || result.Content != "final" || len(p.requests) != 2) {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, len(p.requests))
			}
			// A watchdog abort is not the model's fault: retry without guidance.
			if len(p.requests) == 2 && !reflect.DeepEqual(p.requests[0].Messages, p.requests[1].Messages) {
				t.Fatalf("retry added a note: %+v", p.requests[1].Messages)
			}
			if !tc.ok && (err == nil || len(p.requests) != 2) {
				t.Fatalf("second failure was retried: err=%v calls=%d", err, len(p.requests))
			}
		})
	}
}

func TestContextOverflowCompactsAndRetriesOnce(t *testing.T) {
	overflow := func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		return nil, provider.NewAPIError("openai", 400, "Your input exceeds the context window of this model.")
	}
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		overflow,
		reply(provider.ChatResponse{Content: "summary of earlier work"}),
		func(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			if !strings.Contains(req.Messages[0].Content, "summary of earlier work") {
				return nil, errors.New("retry did not use compacted context")
			}
			return &provider.ChatResponse{Content: "final"}, nil
		},
	}}
	history := []provider.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}, {Role: "user", Content: "c"}, {Role: "assistant", Content: "d"}, {Role: "user", Content: "e"}}
	compacted := 0
	result, err := runLoop(t, AgentConfig{Provider: p, Messages: history, OnCompact: func(int, int) { compacted++ }})
	if err != nil || result.Content != "final" || compacted != 1 {
		t.Fatalf("result=%+v err=%v compacted=%d", result, err, compacted)
	}
}

func TestToolsOnlyResultIsBoundedForModelButFullForObservers(t *testing.T) {
	full := strings.Repeat("x", 100000) + "TAIL"
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		toolCallReply("c", "lookup", `{}`),
		reply(provider.ChatResponse{Content: "done"}),
	}}
	var observed string
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go",
		OnMessage: func(m provider.Message) {
			if m.Role == "tool" {
				observed = m.Content
			}
		},
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return full, nil })}})
	if err != nil {
		t.Fatal(err)
	}
	sent := p.requests[1].Messages[2].Content
	if len(sent) > maxToolsOnlyResultChars+200 || !strings.HasSuffix(sent, "TAIL") || !strings.Contains(sent, "omitted") {
		t.Fatalf("model saw %d chars", len(sent))
	}
	if observed != full {
		t.Fatalf("observer got %d chars, want full result", len(observed))
	}
	multiline := strings.Repeat("row\n", 20000)
	if cut := truncateToolResultForModel(multiline, 1000); len(cut) > 1200 || !strings.Contains(cut, "lines omitted") {
		t.Fatalf("line-aware cut = %d chars", len(cut))
	}
	if cut := truncateToolResultForModel(strings.Repeat("界", 10000), 1000); !strings.Contains(cut, "omitted") || len(cut) > 1200 {
		t.Fatalf("rune-safe cut = %q", cut)
	}
}

func observationIdentity(name, args string) tooloutcome.Identity {
	i := tooloutcome.DefaultIdentity(name, json.RawMessage(args))
	if name == "lookup" {
		i.Risk = tooloutcome.Observation
	}
	return i
}

func TestFailedObservationCanBeRetriedAfterPreconditionFix(t *testing.T) {
	calls := 0
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		toolCallReply("a", "lookup", `{}`),
		toolCallReply("b", "start", `{}`),
		toolCallReply("c", "lookup", `{}`),
		reply(provider.ChatResponse{Content: "done"}),
	}}
	start := ExtraBuiltinTool{Schema: provider.Tool{Name: "start", Parameters: map[string]any{"type": "object"}}, HandlerCtx: func(context.Context, map[string]interface{}) (string, error) { return "started", nil }}
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", ResolveToolIdentity: observationIdentity,
		ExtraTools: []ExtraBuiltinTool{start, lookupTool(func(context.Context, map[string]interface{}) (string, error) {
			calls++
			if calls == 1 {
				return "", tooloutcome.New(tooloutcome.Permanent, "observation_failed", "no_side_effects", "Desktop is not running.", "explain_blocker").Err()
			}
			return "snapshot", nil
		})}})
	if err != nil || calls != 2 {
		t.Fatalf("err=%v lookup calls=%d, want identical observation retried", err, calls)
	}
}

func TestObservationRetryCapResetsAfterSuccessfulAction(t *testing.T) {
	obs := observationIdentity("lookup", `{}`)
	failed := tooloutcome.New(tooloutcome.Permanent, "observation_failed", "no_side_effects", "failed", "explain_blocker")
	record := func(epoch int) ToolRecoveryRecord {
		return ToolRecoveryRecord{Call: provider.ToolCall{ID: "x", Name: "lookup", Arguments: `{}`}, Identity: &obs, Outcome: failed, Epoch: epoch}
	}
	records := []ToolRecoveryRecord{record(0), record(0)}
	if got := toolRecoveryIdentityGuardAt(records, obs, 0); got != nil {
		t.Fatalf("two failures blocked: %+v", got)
	}
	records = append(records, record(0))
	if got := toolRecoveryIdentityGuardAt(records, obs, 0); got == nil || got.Code != "observation_retry_limit" {
		t.Fatalf("third failure not capped: %+v", got)
	}
	if got := toolRecoveryIdentityGuardAt(records, obs, 1); got != nil {
		t.Fatalf("successful action did not reset the window: %+v", got)
	}
	denied := tooloutcome.New(tooloutcome.Denied, "denied", "not_executed", "denied", "explain_blocker")
	if got := toolRecoveryIdentityGuardAt([]ToolRecoveryRecord{{Call: record(0).Call, Identity: &obs, Outcome: denied}}, obs, 0); got == nil {
		t.Fatal("denied observation became retryable")
	}
}

func TestUncertainVMActionFencesOnlyIdenticalReplay(t *testing.T) {
	click := func(args string) tooloutcome.Identity {
		return tooloutcome.OperationIdentity("computer/vm/bot/b1", "desktop.click", json.RawMessage(args))
	}
	prior := click(`{"x":1,"y":2}`)
	lost := tooloutcome.New(tooloutcome.Uncertain, "lost", "unknown", "Response lost.", "verify_effect")
	records := []ToolRecoveryRecord{{Call: provider.ToolCall{ID: "a", Name: "computer_action", Arguments: `{}`}, Identity: &prior, Outcome: lost}}
	if toolRecoveryIdentityGuardAt(records, click(`{"x":1,"y":2}`), 0) == nil {
		t.Fatal("identical uncertain VM action was replayed")
	}
	if got := toolRecoveryIdentityGuardAt(records, click(`{"x":5,"y":9}`), 0); got != nil {
		t.Fatalf("different VM action fenced: %+v", got)
	}
	// An unresolved file write keeps fencing the whole operation.
	write := tooloutcome.OperationIdentity("computer/vm/bot/b1", "files.write", json.RawMessage(`{"path":"a"}`))
	records[0].Identity = &write
	if toolRecoveryIdentityGuardAt(records, tooloutcome.OperationIdentity("computer/vm/bot/b1", "files.write", json.RawMessage(`{"path":"b"}`)), 0) == nil {
		t.Fatal("unresolved uncertain file write lost its operation fence")
	}
	// Outside the computer scope an uncertain opaque effect still fences the operation.
	publish := tooloutcome.OperationIdentity("tool", "publish", json.RawMessage(`{"a":1}`))
	records[0].Identity = &publish
	if toolRecoveryIdentityGuardAt(records, tooloutcome.OperationIdentity("tool", "publish", json.RawMessage(`{"a":2}`)), 0) == nil {
		t.Fatal("uncertain opaque mutation lost its operation fence")
	}
}

func TestDemotedFinalDraftUsesDedicatedCallback(t *testing.T) {
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		reply(provider.ChatResponse{Content: "draft"}),
		reply(provider.ChatResponse{Content: "final"}),
	}}
	var turns, demoted []string
	reviews := 0
	result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go",
		BeforeFinalResponse: func(string) (string, error) {
			reviews++
			if reviews == 1 {
				return "Review.", nil
			}
			return "", nil
		},
		OnAssistantTurn:     func(_ int, c string) { turns = append(turns, c) },
		OnFinalDraftDemoted: func(_ int, c string) { demoted = append(demoted, c) }})
	if err != nil || result.Content != "final" || len(turns) != 0 || !reflect.DeepEqual(demoted, []string{"draft"}) {
		t.Fatalf("result=%+v err=%v turns=%v demoted=%v", result, err, turns, demoted)
	}
}

func TestEstimateContextUsageCountsReplayedReasoning(t *testing.T) {
	plain := []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "a", Name: "lookup", Arguments: `{}`}}}}
	replayed := []provider.Message{{Role: "assistant", ToolCalls: plain[0].ToolCalls, ReasoningItems: []provider.ReasoningItem{{ID: "rs_1", EncryptedContent: strings.Repeat("e", 40000)}}}}
	if with, without := EstimateContextUsage("", replayed, nil), EstimateContextUsage("", plain, nil); with-without < 10000 {
		t.Fatalf("reasoning estimate with=%d without=%d", with, without)
	}
}

func TestCompactMessagesBoundsSummarizerInput(t *testing.T) {
	var prompt string
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		func(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			prompt = req.Messages[0].Content
			return &provider.ChatResponse{Content: "summary"}, nil
		},
	}}
	messages := []provider.Message{{Role: "user", Content: "ORIGINAL GOAL"}}
	for i := 0; i < 300; i++ {
		messages = append(messages, provider.Message{Role: "assistant", Content: fmt.Sprintf("turn %d ", i) + strings.Repeat("界", 5000)})
	}
	messages = append(messages, provider.Message{Role: "user", Content: "LATEST ASK"})
	if _, err := compactMessages(context.Background(), p, "gpt-5.6", "", messages); err != nil {
		t.Fatal(err)
	}
	if len(prompt) > compactionInputChars+2000 || !strings.Contains(prompt, "ORIGINAL GOAL") || !strings.Contains(prompt, "LATEST ASK") || !strings.Contains(prompt, "earlier messages omitted") || !utf8.ValidString(prompt) {
		t.Fatalf("summarizer input %d bytes, head=%q", len(prompt), prompt[:min(len(prompt), 200)])
	}
}

func TestBulkContentToolArgumentsUseLargerCap(t *testing.T) {
	previous, previousBulk := maxToolCallArgumentChars, maxBulkToolCallArgumentChars
	maxToolCallArgumentChars, maxBulkToolCallArgumentChars = 100, 1000
	defer func() { maxToolCallArgumentChars, maxBulkToolCallArgumentChars = previous, previousBulk }()
	args := `{"action":"files.write","path":"a.txt","content":"` + strings.Repeat("a", 500) + `"}`
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		func(_ context.Context, _ *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "c", Name: "computer_files"}}})
			for i := 0; i < len(args); i += 50 {
				delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, Arguments: args[i:min(i+50, len(args))]}}})
			}
			return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c", Name: "computer_files", Arguments: args}}}, nil
		},
		reply(provider.ChatResponse{Content: "written"}),
	}}
	executed := 0
	files := ExtraBuiltinTool{Schema: provider.Tool{Name: "computer_files", Parameters: map[string]any{"type": "object"}}, HandlerCtx: func(context.Context, map[string]interface{}) (string, error) { executed++; return "ok", nil }}
	result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnStreamChunk: func(string, string) {}, ExtraTools: []ExtraBuiltinTool{files}})
	if err != nil || result.Content != "written" || executed != 1 || len(p.requests) != 2 {
		t.Fatalf("result=%+v err=%v executed=%d calls=%d", result, err, executed, len(p.requests))
	}
}

func TestMicroCompactPreviewSurvivesEarlyInvalidByte(t *testing.T) {
	messages := []provider.Message{{Role: "tool", ToolName: "lookup", Content: "\xff" + strings.Repeat("a", 400) + "界"}}
	for i := 0; i < 6; i++ {
		messages = append(messages, provider.Message{Role: "user", Content: "recent"})
	}
	got := microCompact(messages, 6)[0].Content
	if !strings.HasPrefix(got, "\xff"+strings.Repeat("a", 150)) {
		t.Fatalf("preview collapsed: %q", got[:min(len(got), 60)])
	}
	if clipped := clipUTF8("ab界", 4); clipped != "ab" {
		t.Fatalf("split rune kept: %q", clipped)
	}
}
