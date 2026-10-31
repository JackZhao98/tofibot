package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCompat is an OpenAI-compatible server: the first request asks for a tool
// call, the request that carries the tool result gets a text answer. Streaming
// answers use SSE with the call id left out, as some local servers do.
type fakeCompat struct {
	*httptest.Server
	mu       sync.Mutex
	bodies   []map[string]any
	auth     []string
	noCallID bool
}

func newFakeCompat(t *testing.T, noCallID bool) *fakeCompat {
	f := &fakeCompat{noCallID: noCallID}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		hasToolResult := false
		for _, m := range body["messages"].([]any) {
			if m.(map[string]any)["role"] == "tool" {
				hasToolResult = true
			}
		}
		id := `"id":"call_9",`
		if f.noCallID {
			id = ""
		}
		stream, _ := body["stream"].(bool)
		switch {
		case !hasToolResult && stream:
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,%s\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\n\n", id)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"tofu\\\"}\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case !hasToolResult:
			fmt.Fprintf(w, `{"choices":[{"message":{"content":"","tool_calls":[{%s"function":{"name":"lookup","arguments":"{\"q\":\"tofu\"}"}}]}}],"usage":{"prompt_tokens":11,"completion_tokens":3}}`, id)
		case stream:
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"It is \"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"firm.\"}}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":4}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"It is firm."}}],"usage":{"prompt_tokens":20,"completion_tokens":4}}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func compatTools() []Tool {
	return []Tool{{Name: "lookup", Description: "look a thing up", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"q": map[string]interface{}{"type": "string"}}}}}
}

func TestProviderForModelRoutesCompatPrefix(t *testing.T) {
	for model, want := range map[string]string{
		"compat:llama3.1:8b":     ProviderOpenAICompat,
		"compat:claude-like":     ProviderOpenAICompat,
		"COMPAT:Qwen":            ProviderOpenAICompat,
		"claude-opus-5":          ProviderAnthropic,
		"codex-gpt-6-sol":        ProviderOpenAICodex,
		"gpt-5.5":                ProviderOpenAI,
		"meta-llama/llama-3-70b": ProviderOpenAI,
	} {
		if got := ProviderForModel(model); got != want {
			t.Errorf("ProviderForModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestCompatToolCallRoundTripWithoutStreaming(t *testing.T) {
	f := newFakeCompat(t, false)
	p, err := New(ProviderOpenAICompat, "local-key", WithBaseURL(f.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, f, func(req *ChatRequest) (*ChatResponse, error) { return p.Chat(context.Background(), req) })
}

func TestCompatToolCallRoundTripStreaming(t *testing.T) {
	f := newFakeCompat(t, false)
	p, err := New(ProviderOpenAICompat, "", WithBaseURL(f.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	roundTrip(t, f, func(req *ChatRequest) (*ChatResponse, error) {
		return p.ChatStream(context.Background(), req, func(d StreamDelta) { streamed.WriteString(d.Content) })
	})
	if streamed.String() != "It is firm." {
		t.Fatalf("streamed text = %q", streamed.String())
	}
	if f.auth[0] != "" {
		t.Fatalf("a key-less endpoint must get no Authorization header, got %q", f.auth[0])
	}
}

func TestCompatSynthesizesMissingToolCallID(t *testing.T) {
	f := newFakeCompat(t, true)
	p, _ := New(ProviderOpenAICompat, "", WithBaseURL(f.URL+"/v1"))
	for name, call := range map[string]func(*ChatRequest) (*ChatResponse, error){
		"chat":   func(r *ChatRequest) (*ChatResponse, error) { return p.Chat(context.Background(), r) },
		"stream": func(r *ChatRequest) (*ChatResponse, error) { return p.ChatStream(context.Background(), r, nil) },
	} {
		resp, err := call(&ChatRequest{Model: "compat:m", Messages: []Message{{Role: "user", Content: "hi"}}, Tools: compatTools()})
		if err != nil || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID == "" {
			t.Fatalf("%s: resp=%+v err=%v", name, resp, err)
		}
	}
}

// roundTrip drives one tool call through the model and back.
func roundTrip(t *testing.T, f *fakeCompat, call func(*ChatRequest) (*ChatResponse, error)) {
	t.Helper()
	req := &ChatRequest{Model: "compat:llama3.1:8b", System: "be brief", Messages: []Message{{Role: "user", Content: "is tofu firm?"}}, Tools: compatTools()}
	first, err := call(req)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasToolCalls() || first.ToolCalls[0].Name != "lookup" || first.ToolCalls[0].ID != "call_9" || first.ToolCalls[0].Arguments != `{"q":"tofu"}` {
		t.Fatalf("first response = %+v", first)
	}
	if first.Usage.InputTokens != 11 || first.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", first.Usage)
	}
	req.Messages = append(req.Messages,
		Message{Role: "assistant", ToolCalls: first.ToolCalls},
		Message{Role: "tool", ToolCallID: "call_9", ToolName: "lookup", Content: "tofu is firm"})
	second, err := call(req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "It is firm." || second.HasToolCalls() {
		t.Fatalf("second response = %+v", second)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for i, body := range f.bodies {
		if body["model"] != "llama3.1:8b" {
			t.Fatalf("request %d sent model %v; the compat: prefix must not leave the server", i, body["model"])
		}
		if tools, _ := body["tools"].([]any); len(tools) != 1 {
			t.Fatalf("request %d tools = %v", i, body["tools"])
		}
	}
	last := f.bodies[len(f.bodies)-1]["messages"].([]any)
	tool := last[len(last)-1].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_9" || tool["content"] != "tofu is firm" {
		t.Fatalf("tool result message = %v", tool)
	}
	assistant := last[len(last)-2].(map[string]any)
	if calls, _ := assistant["tool_calls"].([]any); len(calls) != 1 {
		t.Fatalf("assistant tool_calls = %v", assistant)
	}
}

func TestCompatNeedsAnEndpoint(t *testing.T) {
	if _, err := New(ProviderOpenAICompat, "k"); err == nil {
		t.Fatal("a compatible provider without a base URL must be refused")
	}
}

func TestCompatContextWindowFromServerThenFallback(t *testing.T) {
	t.Cleanup(func() { SetCompatContextWindows(nil) })
	SetCompatContextWindows(map[string]int{"compat:big": 262144, "compat:zero": 0})
	if w := GetContextWindow("compat:big"); w != 262144 || !ContextWindowKnown("compat:big") {
		t.Fatalf("reported window = %d known=%v", w, ContextWindowKnown("compat:big"))
	}
	if ContextWindowKnown("compat:zero") || ContextWindowKnown("compat:unlisted") {
		t.Fatal("a model with no reported window is not known")
	}
	if w := GetContextWindow("compat:unlisted"); w != 500000 {
		t.Fatalf("fallback window = %d, want the existing 500000 heuristic", w)
	}
	SetCompatContextWindows(nil)
	if ContextWindowKnown("compat:big") {
		t.Fatal("a removed endpoint must stop contributing windows")
	}
}
