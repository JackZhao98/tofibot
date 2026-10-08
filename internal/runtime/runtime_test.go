package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

type scriptedProvider struct {
	mu       sync.Mutex
	requests []*provider.ChatRequest
	called   bool
	toolCall *provider.ToolCall
}

type reportingToolProvider struct{ calls int }

type stagnantDiscoveryProvider struct {
	calls int
	vary  bool
}

func (p *stagnantDiscoveryProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	content := ""
	if p.calls == 16 || p.calls == 31 {
		content = "I checked the available tools; continuing to investigate."
	}
	if p.calls == 46 {
		if p.vary {
			return &provider.ChatResponse{Content: "Done after updated directory results."}, nil
		}
		content = "I will search again."
	}
	return &provider.ChatResponse{Content: content, ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("discovery-%d", p.calls), Name: "search_mcp_tools", Arguments: `{"query":"same"}`}}}, nil
}

func TestRunContinuesWhenDiscoveryResultsChange(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stagnantDiscoveryProvider{vary: true}
	e := &engine{provider: p, model: "test-model"}
	executed := 0
	result, err := e.Run(context.Background(), Request{RunID: "changing-discovery", BotID: "bot", Messages: []Message{{Role: "user", Content: "find a tool"}},
		Tools: []Tool{{Name: "search_mcp_tools", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			executed++
			return fmt.Sprintf(`{"revision":%d}`, executed), nil
		}}},
		OnAssistantTurn: func(int, string) error { return nil },
	})
	if err != nil || executed != 45 || result.Content != "Done after updated directory results." {
		t.Fatalf("result=%+v err=%v tools=%d", result, err, executed)
	}
}

func (p *stagnantDiscoveryProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestRunStopsAfterTwoStagnantDiscoveryIntervals(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stagnantDiscoveryProvider{}
	e := &engine{provider: p, model: "test-model"}
	executed, reports := 0, 0
	result, err := e.Run(context.Background(), Request{RunID: "stagnant-discovery", BotID: "bot", Messages: []Message{{Role: "user", Content: "find a tool"}},
		Tools: []Tool{{Name: "search_mcp_tools", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			executed++
			return `{"tools":[]}`, nil
		}}},
		OnAssistantTurn: func(int, string) error { reports++; return nil },
	})
	if err != nil || executed != 45 || reports != 2 || p.calls != 46 || !strings.Contains(result.Content, "no new information") {
		t.Fatalf("result=%+v err=%v tools=%d reports=%d model=%d", result, err, executed, reports, p.calls)
	}
}

func (p *reportingToolProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls == 33 {
		return &provider.ChatResponse{Content: "Finished after two reported batches."}, nil
	}
	content := ""
	if p.calls == 16 || p.calls == 31 {
		content = fmt.Sprintf("Progress reported at model call %d; continuing the task.", p.calls)
		foundReminder := false
		for _, message := range req.Messages {
			if strings.Contains(message.Content, "progress update") {
				foundReminder = true
			}
		}
		if !foundReminder {
			return &provider.ChatResponse{Content: "missing progress reminder"}, nil
		}
	}
	return &provider.ChatResponse{Content: content, ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("tick-%d", p.calls), Name: "tick", Arguments: `{}`}}}, nil
}

func (p *reportingToolProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestRunContinuesPastFifteenToolCallsAfterVisibleReport(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &reportingToolProvider{}
	e := &engine{provider: p, model: "test-model"}
	toolCalls := 0
	var reports []string
	result, err := e.Run(context.Background(), Request{
		RunID: "reported-tools", BotID: "bot", Messages: []Message{{Role: "user", Content: "complete a long task"}},
		Tools: []Tool{{Name: "tick", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			toolCalls++
			return "ok", nil
		}}},
		OnAssistantTurn: func(_ int, content string) error { reports = append(reports, content); return nil },
	})
	if err != nil || result.Content != "Finished after two reported batches." || toolCalls != 32 || len(reports) != 2 || p.calls != 33 {
		t.Fatalf("result=%+v err=%v calls=%d reports=%q modelCalls=%d", result, err, toolCalls, reports, p.calls)
	}
}

type batchedReportingProvider struct {
	calls        int
	neverReports bool
	seenDeferred int
}

func (p *batchedReportingProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		count := 17
		if p.neverReports {
			count = 15
		}
		calls := make([]provider.ToolCall, count)
		for i := range calls {
			calls[i] = provider.ToolCall{ID: fmt.Sprintf("batch-%d", i), Name: "tick", Arguments: `{}`}
		}
		return &provider.ChatResponse{ToolCalls: calls}, nil
	}
	if p.calls == 2 {
		for _, message := range req.Messages {
			if message.Role == "tool" && strings.Contains(message.Content, "Tool not executed: a user-visible progress report") {
				p.seenDeferred++
			}
		}
	}
	if p.neverReports {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("ignored-%d", p.calls), Name: "tick", Arguments: `{}`}}}, nil
	}
	if p.calls == 2 {
		return &provider.ChatResponse{Content: "First batch done; continuing.", ToolCalls: []provider.ToolCall{{ID: "after-report", Name: "tick", Arguments: `{}`}}}, nil
	}
	return &provider.ChatResponse{Content: "Done."}, nil
}

func (p *batchedReportingProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestRunKeepsUsefulBatchCallsBeyondFifteen(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &batchedReportingProvider{}
	e := &engine{provider: p, model: "test-model"}
	executed := 0
	result, err := e.Run(context.Background(), Request{RunID: "batch-report", BotID: "bot", Messages: []Message{{Role: "user", Content: "task"}},
		Tools:           []Tool{{Name: "tick", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { executed++; return "ok", nil }}},
		OnAssistantTurn: func(int, string) error { return nil },
	})
	if err != nil || result.Content != "Done." || executed != 18 || p.seenDeferred != 0 {
		t.Fatalf("result=%+v err=%v executed=%d deferred=%d", result, err, executed, p.seenDeferred)
	}
}

func TestRunRetainsStepBudgetWithoutMissingProseAbort(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &batchedReportingProvider{neverReports: true}
	e := &engine{provider: p, model: "test-model"}
	executed := 0
	_, err := e.Run(context.Background(), Request{RunID: "no-report", BotID: "bot", Messages: []Message{{Role: "user", Content: "task"}},
		Tools:           []Tool{{Name: "tick", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { executed++; return "ok", nil }}},
		OnAssistantTurn: func(int, string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "maximum agent steps") || executed != 314 || p.calls != 300 {
		t.Fatalf("err=%v executed=%d modelCalls=%d", err, executed, p.calls)
	}
}

func (p *scriptedProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.requests = append(p.requests, req)
	first := !p.called
	p.called = true
	p.mu.Unlock()
	if first {
		if p.toolCall != nil {
			return &provider.ChatResponse{ToolCalls: []provider.ToolCall{*p.toolCall}}, nil
		}
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "save_memory", Arguments: `{"content":"remember"}`}}}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}

func (p *scriptedProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, onDelta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestRunUsesOnlyRequestToolsAndRunsThroughContext(t *testing.T) {
	home := t.TempDir()
	paths.SetTofiHome(home)
	p := &scriptedProvider{}
	e := &engine{provider: p, model: "test-model"}
	var gotCtx context.Context
	eResult, err := e.Run(context.Background(), Request{
		BotID:    "bot-1",
		RunID:    "run-1",
		System:   "system",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []Tool{{
			Name:        "save_memory",
			Description: "save a memory",
			Parameters:  map[string]any{"type": "object"},
			Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
				gotCtx = ctx
				if !strings.Contains(string(raw), "remember") {
					t.Fatalf("unexpected arguments: %s", raw)
				}
				return "saved", nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if eResult.Content != "done" {
		t.Fatalf("content = %q, want done", eResult.Content)
	}
	if gotCtx == nil {
		t.Fatal("tool did not receive a context")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(p.requests))
	}
	for i, req := range p.requests {
		if len(req.Tools) != 1 || req.Tools[0].Name != "save_memory" {
			t.Fatalf("request %d tools = %#v; runtime exposed tools outside Request.Tools", i, req.Tools)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "transcripts")); !os.IsNotExist(err) {
		t.Fatalf("ToolsOnly run created legacy transcript path: %v", err)
	}
}

func TestRunStopsAtBeforeModelBoundary(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{}
	e := &engine{provider: p, model: "test-model"}
	called := 0
	result, err := e.Run(context.Background(), Request{
		RunID:    "steering-run",
		Messages: []Message{{Role: "user", Content: "initial"}},
		BeforeModelCall: func() error {
			called++
			return context.Canceled
		},
	})
	// A stopped boundary is not a completed run: it must surface as an error
	// so the caller never finishes the run "done" without an answer.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("safe boundary err = %v, want context.Canceled", err)
	}
	if called != 1 || result.Content != "" {
		t.Fatalf("boundary calls=%d result=%+v", called, result)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 0 {
		t.Fatalf("provider was called after steering boundary: %d", len(p.requests))
	}
}

func TestNewRejectsMissingRemoteKey(t *testing.T) {
	if _, err := New(Config{Provider: "openai", Model: "gpt-5-mini"}); err == nil {
		t.Fatal("New() accepted a remote provider without a key")
	}
}

func TestNewWrapsProviderWithDefaultRetry(t *testing.T) {
	e, err := New(Config{Provider: "openai", APIKey: "test-key", Model: "gpt-5-mini"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine, ok := e.(*engine)
	if !ok {
		t.Fatalf("engine type = %T, want *engine", e)
	}
	if _, ok := engine.provider.(*provider.RetryProvider); !ok {
		t.Fatalf("provider type = %T, want *provider.RetryProvider", engine.provider)
	}
}

func TestNewAllowsExplicitLocalProviderWithoutKey(t *testing.T) {
	if _, err := New(Config{Provider: "ollama", BaseURL: "http://127.0.0.1:11434/v1", Model: "llama3"}); err != nil {
		t.Fatalf("New() rejected explicit local provider: %v", err)
	}
}

func TestRunOpenAICompatibleToolRoundTrip(t *testing.T) {
	home := t.TempDir()
	paths.SetTofiHome(home)
	var requests int
	var sawToolResult bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		for _, message := range payload.Messages {
			if message.Role == "tool" && message.Content == "saved by backend" {
				sawToolResult = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"save_memory","arguments":"{\"content\":\"round trip\"}"}}]}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"finished"}}],"usage":{"prompt_tokens":13,"completion_tokens":5}}`)
	}))
	defer server.Close()
	engine, err := New(Config{Provider: "openai_completions", APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), Request{
		BotID:    "bot-1",
		RunID:    "run-round-trip",
		Messages: []Message{{Role: "user", Content: "save this"}},
		Tools: []Tool{{
			Name:        "save_memory",
			Description: "save a memory",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"content": map[string]any{"type": "string"}}},
			Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				return "saved by backend", nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "finished" || result.InputTokens != 24 || result.OutputTokens != 12 {
		t.Fatalf("result = %#v", result)
	}
	if requests != 2 || !sawToolResult {
		t.Fatalf("requests = %d, sawToolResult = %v", requests, sawToolResult)
	}
	if _, err := os.Stat(filepath.Join(home, "transcripts")); !os.IsNotExist(err) {
		t.Fatalf("round-trip ToolsOnly run created legacy transcript path: %v", err)
	}
}

func TestRuntimeSystemPromptOverheadMatchesProviderRequest(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{called: true}
	e := &engine{provider: p, model: "test-model"}
	const limit = 16000
	system := strings.Repeat("文", limit-SystemPromptOverheadRunes())
	tools := []Tool{{Name: "tick", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}
	_, err := e.Run(context.Background(), Request{RunID: "prompt-budget", BotID: "bot", System: system, Messages: []Message{{Role: "user", Content: "hello"}}, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 {
		t.Fatalf("provider calls=%d", len(p.requests))
	}
	actual := p.requests[0].System
	if !strings.HasPrefix(actual, system) || len([]rune(actual)) != limit {
		t.Fatalf("reserved suffix=%d, actual system=%d", SystemPromptOverheadRunes(), len([]rune(actual)))
	}
	t.Logf("runtime agent suffix=%d runes; full provider system=%d", SystemPromptOverheadRunes(), len([]rune(actual)))

	// A tool-free request (summary, triage) cannot owe progress reports.
	plain := &scriptedProvider{called: true}
	if _, err := (&engine{provider: plain, model: "test-model"}).Run(context.Background(), Request{RunID: "plain", BotID: "bot", System: "summarize", Messages: []Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if len(plain.requests) != 1 || plain.requests[0].System != "summarize" {
		t.Fatalf("tool-free system=%q", plain.requests[0].System)
	}
}
