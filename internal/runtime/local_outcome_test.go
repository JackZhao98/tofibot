package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type localOutcomeProvider struct{ calls int }

func (p *localOutcomeProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls > 2 {
		return &provider.ChatResponse{Content: "done"}, nil
	}
	return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprint("c", p.calls), Name: "save", Arguments: fmt.Sprintf(`{"n":%d}`, p.calls)}}}, nil
}
func (p *localOutcomeProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// Table: how an unclassified executor error is reported.
func TestUnclassifiedToolErrorClassification(t *testing.T) {
	cases := []struct {
		name          string
		local         bool
		err           error
		wantStatus    string
		wantCertainty string
		secondRuns    bool // a later call with different arguments still executes
	}{
		{"local validation error is definite", true, errors.New("name is required"), tooloutcome.Permanent, "not_executed", true},
		{"local typed invalid arguments", true, tooloutcome.InvalidArguments("fix it"), tooloutcome.Validation, "not_executed", true},
		{"local rejected", true, tooloutcome.Rejected("not allowed"), tooloutcome.Permanent, "not_executed", true},
		{"external dispatched with no reply is uncertain", false, errors.New("connection reset after send"), tooloutcome.Uncertain, "unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths.SetTofiHome(t.TempDir())
			executed := 0
			var first *tooloutcome.Outcome
			_, err := (&engine{provider: &localOutcomeProvider{}, model: "synthetic"}).Run(context.Background(), Request{RunID: "local-outcome", BotID: "synthetic", Messages: []Message{{Role: "user", Content: "go"}}, Tools: []Tool{{Name: "save", Local: tc.local, Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
				executed++
				if executed == 1 {
					return "", tc.err
				}
				return "saved", nil
			}}}, OnToolEvent: func(e ToolEvent) error {
				if e.Status == "failed" && first == nil {
					first = e.Outcome
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if first == nil || first.Status != tc.wantStatus || first.Certainty != tc.wantCertainty {
				t.Fatalf("first outcome = %+v", first)
			}
			if tc.secondRuns != (executed == 2) {
				t.Fatalf("executed=%d, want second call executed=%v", executed, tc.secondRuns)
			}
		})
	}
}
