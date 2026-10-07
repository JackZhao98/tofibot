package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

func TestModelProviderRule(t *testing.T) {
	for model, want := range map[string]string{
		"codex-gpt-6-luna": "openai_codex", "codex-auto-review": "openai_codex", "CODEX-x": "openai_codex",
		"claude-opus-5-5": "anthropic", "claude-haiku-4-5-20251001": "anthropic",
		"gpt-6-luna": "openai", "o4-mini": "openai", "anything-else": "openai",
	} {
		if got := ModelProvider(model); got != want {
			t.Errorf("ModelProvider(%q)=%q want %q", model, got, want)
		}
	}
}

type routedFixture struct {
	mu       sync.Mutex
	resolved []string
	hits     []string // "provider:model:key"
	server   *httptest.Server
}

func newRoutedFixture(t *testing.T) *routedFixture {
	f := &routedFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &in)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/openai/responses":
			f.hits = append(f.hits, "openai:"+in.Model+":"+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"openai reply"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`)
		case "/anthropic/v1/messages":
			f.hits = append(f.hits, "anthropic:"+in.Model+":"+r.Header.Get("x-api-key"))
			// The adapter always streams; a stream must end with message_stop.
			w.Header().Set("Content-Type", "text/event-stream")
			for _, frame := range []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"anthropic reply"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			} {
				_, _ = io.WriteString(w, "data: "+frame+"\n\n")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *routedFixture) config() Config {
	return Config{
		Resolve: func(_ context.Context, name string) (string, error) {
			f.mu.Lock()
			f.resolved = append(f.resolved, name)
			f.mu.Unlock()
			switch name {
			case "openai":
				return "openai-key", nil
			case "anthropic":
				return "anthropic-key", nil
			}
			return "", errors.New("model provider is not configured: " + name)
		},
		Endpoint: func(name string) string {
			switch name {
			case "openai":
				return f.server.URL + "/openai"
			case "anthropic":
				return f.server.URL + "/anthropic"
			}
			return ""
		},
	}
}

func TestRoutedEngineResolvesProviderPerRequestModel(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	f := newRoutedFixture(t)
	e, err := New(f.config())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ model, reply, hit string }{
		{"gpt-6-luna", "openai reply", "openai:gpt-6-luna:openai-key"},
		{"claude-opus-5-5", "anthropic reply", "anthropic:claude-opus-5-5:anthropic-key"},
	} {
		result, err := e.Run(context.Background(), Request{RunID: "run-" + tc.model, BotID: "bot", Model: tc.model, Messages: []Message{{Role: "user", Content: "hi"}}})
		if err != nil || result.Content != tc.reply {
			t.Fatalf("%s: result=%+v err=%v", tc.model, result, err)
		}
		f.mu.Lock()
		last := f.hits[len(f.hits)-1]
		f.mu.Unlock()
		if last != tc.hit {
			t.Fatalf("%s hit %s, want %s", tc.model, last, tc.hit)
		}
	}
	_, err = e.Run(context.Background(), Request{RunID: "run-codex", BotID: "bot", Model: "codex-gpt-6-luna", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil || !strings.Contains(err.Error(), "model provider is not configured: openai_codex") {
		t.Fatalf("unconfigured provider err=%v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.resolved, ",") != "openai,anthropic,openai_codex" {
		t.Fatalf("resolved=%v", f.resolved)
	}
}

func TestRoutedApprovalExpiryUsesModelProvider(t *testing.T) {
	f := newRoutedFixture(t)
	e, err := New(f.config())
	if err != nil {
		t.Fatal(err)
	}
	c := &agent.Continuation{Version: 1, QuestionID: "q", WaitingToolCallID: "expired-call", WaitingToolName: "write", Messages: []provider.Message{{Role: "user", Content: "Synthetic task"}, {Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "expired-call", Name: "write", Arguments: `{}`}}}}}
	raw, err := encodeContinuation(Request{RunID: "r", BotID: "b"}, "claude-opus-5-5", c)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(context.Background(), Request{RunID: "r", BotID: "b", Model: "claude-opus-5-5", Continuation: raw, ApprovalExpiryRecovery: true})
	if err != nil || result.Content != "anthropic reply" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hits) != 1 || f.hits[0] != "anthropic:claude-opus-5-5:anthropic-key" {
		t.Fatalf("hits=%v", f.hits)
	}
}
