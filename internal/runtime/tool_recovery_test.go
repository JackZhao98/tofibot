package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type repairingProvider struct {
	calls, invalidCalls int
	outcomes            []tooloutcome.Outcome
}

type compactedRecoveryProvider struct {
	calls, compactions int
	finalOutcome       *tooloutcome.Outcome
}

func (p *compactedRecoveryProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if strings.Contains(req.System, "structured conversation summaries") {
		p.compactions++
		return &provider.ChatResponse{Content: "Synthetic summary deliberately omits failed calls."}, nil
	}
	p.calls++
	name, args := "write", `{"target":"synthetic"}`
	switch p.calls {
	case 2:
		name, args = "inspect", `{}`
	case 3:
		name, args = "ask", `{}`
	case 5:
		p.finalOutcome = tooloutcome.Parse(req.Messages[len(req.Messages)-1].Content)
		return &provider.ChatResponse{Content: "Verify the uncertain effect before continuing."}, nil
	}
	r := &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("compacted-%d", p.calls), Name: name, Arguments: args}}}
	if p.calls == 2 {
		r.Usage.InputTokens = 1_000_000
	}
	return r, nil
}
func (p *compactedRecoveryProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestUncertainEffectCannotReplayAfterCompactionAndResumedCheckpoint(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &compactedRecoveryProvider{}
	effects := 0
	var checkpoint json.RawMessage
	req := Request{RunID: "ledger", BotID: "bot", Messages: []Message{{Role: "user", Content: "synthetic task"}}, OnSuspend: func(_ string, raw json.RawMessage) error {
		checkpoint = append(json.RawMessage(nil), raw...)
		return nil
	}, Tools: []Tool{
		{Name: "write", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			effects++
			return "", tooloutcome.New(tooloutcome.Uncertain, "lost_response", "unknown", "Synthetic response lost after success.", "verify_effect").Err()
		}},
		{Name: "inspect", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "Synthetic state still uncertain", nil }},
		{Name: "ask", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
			return "", SuspendForUserInput(ctx, "question-ledger")
		}},
	}}
	paused, err := (&engine{provider: p, model: "synthetic"}).Run(context.Background(), req)
	if err != nil || !paused.Suspended || p.compactions != 1 || effects != 1 {
		t.Fatalf("paused=%+v err=%v compactions=%d effects=%d", paused, err, p.compactions, effects)
	}
	c, err := decodeContinuation(checkpoint, req, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ToolRecovery) != 1 {
		t.Fatalf("recovery ledger lost: %+v", c.ToolRecovery)
	}
	for _, msg := range c.Messages {
		if msg.Role == "tool" && msg.ToolName == "write" {
			t.Fatal("fixture failed to compact the original outcome")
		}
	}
	req.Continuation, req.ResumeResult = checkpoint, "synthetic answer"
	completed, err := (&engine{provider: p, model: "synthetic"}).Run(context.Background(), req)
	if err != nil || completed.Suspended || effects != 1 || p.finalOutcome == nil || p.finalOutcome.Status != tooloutcome.Uncertain {
		t.Fatalf("unsafe replay: %+v %v effects=%d outcome=%+v", completed, err, effects, p.finalOutcome)
	}
}

func (p *repairingProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls > 1 {
		last := req.Messages[len(req.Messages)-1]
		if outcome := tooloutcome.Parse(last.Content); outcome != nil {
			p.outcomes = append(p.outcomes, *outcome)
		}
	}
	if p.calls > p.invalidCalls+1 {
		return &provider.ChatResponse{Content: "Done after schema repair."}, nil
	}
	args := `{"target":"synthetic"}`
	if p.calls <= p.invalidCalls {
		args = `{"target":5}`
	}
	return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("repair-%d", p.calls), Name: "write", Arguments: args}}}, nil
}
func (p *repairingProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestValidationReturnsToModelForRepairWithSeparateBound(t *testing.T) {
	for _, failures := range []int{1, 3} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			paths.SetTofiHome(t.TempDir())
			p := &repairingProvider{invalidCalls: failures}
			e := &engine{provider: p, model: "synthetic"}
			effects := 0
			result, err := e.Run(context.Background(), Request{RunID: "repair", BotID: "bot", Messages: []Message{{Role: "user", Content: "synthetic task"}}, Tools: []Tool{{Name: "write", Parameters: map[string]any{"type": "object", "required": []string{"target"}, "properties": map[string]any{"target": map[string]any{"type": "string"}}, "additionalProperties": false}, Execute: func(context.Context, json.RawMessage) (string, error) { effects++; return "synthetic success", nil }}}})
			if err != nil || result.Content != "Done after schema repair." {
				t.Fatalf("result=%+v %v", result, err)
			}
			want := 1
			if failures == 3 {
				want = 0
			}
			if effects != want {
				t.Fatalf("effects=%d want=%d", effects, want)
			}
			if len(p.outcomes) < failures || p.outcomes[0].Status != tooloutcome.Validation || p.outcomes[0].Certainty != "not_executed" {
				t.Fatalf("model did not receive validation: %+v", p.outcomes)
			}
			if failures == 3 && p.outcomes[len(p.outcomes)-1].Code != "repair_budget_exhausted" {
				t.Fatalf("budget reset: %+v", p.outcomes)
			}
		})
	}
}
