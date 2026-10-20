package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Queueing behind another run's shared-desktop use must not spend the waiting
// run's active-time budget.
func TestWaitingForDesktopDoesNotSpendRunBudget(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var delta map[string]any
		switch n {
		case 1, 2:
			name := map[int32]string{1: "queue", 2: "continue_work"}[n]
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("c%d", n), "type": "function", "function": map[string]string{"name": name, "arguments": `{}`}}}}
		default:
			delta = map[string]any{"content": "finished"}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	defer endpoint.Close()
	engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: endpoint.URL + "/v1", Model: "test-model", MaxDuration: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	holder := Run{BotID: "holder", ID: "run-holder"}
	if err := s.waitComputerOwner(context.Background(), holder); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(500 * time.Millisecond); s.releaseComputerOwner(holder.BotID, holder.ID) }()
	continued := 0
	_, err = engine.Run(context.Background(), runtime.Request{RunID: "queued-run", OnDelta: func(string) {}, Tools: []runtime.Tool{
		{Name: "queue", Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
			return "ok", s.waitComputerOwner(ctx, Run{BotID: "waiter", ID: "run-waiter"})
		}},
		{Name: "continue_work", Execute: func(context.Context, json.RawMessage) (string, error) { continued++; return "recorded", nil }},
	}})
	if err != nil || continued != 1 {
		t.Fatalf("err=%v continued=%d: queueing spent the run budget", err, continued)
	}
}
