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

type evidenceProvider struct{ calls, compactions int }

func (p *evidenceProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if strings.Contains(req.System, "structured conversation summaries") {
		p.compactions++
		return &provider.ChatResponse{Content: "Synthetic summary omits write evidence."}, nil
	}
	p.calls++
	name, args := "write", `{"target":"a"}`
	switch p.calls {
	case 2:
		name, args = "inspect", `{}`
	case 3:
		name, args = "ask", `{}`
	case 5:
		args = `{"target":"b"}`
	case 6:
		args = `{"target":"c"}`
	case 7:
		args = `{"target":"d"}`
	case 8:
		return &provider.ChatResponse{Content: "Synthetic evidence retained."}, nil
	}
	r := &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("evidence-%d", p.calls), Name: name, Arguments: args}}}
	if p.calls == 2 {
		r.Usage.InputTokens = 1_000_000
	}
	return r, nil
}
func (p *evidenceProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestEffectEvidenceIsCumulativeAcrossCompactionAndCheckpoint(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &evidenceProvider{}
	identity := func(name string) tooloutcome.Identity {
		i := tooloutcome.OperationIdentity("computer/vm/bot/evidence", "files.write", json.RawMessage(`{"target":"`+name+`"}`))
		i.Risk, i.Target, i.Object, i.GuardVersion = tooloutcome.TargetMutation, "/workspace/"+name, "synthetic-inode/"+name, 1
		return i
	}
	var checkpoint json.RawMessage
	effects := 0
	req := Request{RunID: "evidence", BotID: "synthetic", Messages: []Message{{Role: "user", Content: "Synthetic cumulative evidence"}}, OnSuspend: func(_ string, raw json.RawMessage) error {
		checkpoint = append(json.RawMessage(nil), raw...)
		return nil
	}, Tools: []Tool{
		{Name: "write", Parameters: map[string]any{"type": "object"}, Identity: func(raw json.RawMessage) tooloutcome.Identity {
			var in struct {
				Target string `json:"target"`
			}
			_ = json.Unmarshal(raw, &in)
			return identity(in.Target)
		}, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			effects++
			if effects == 1 {
				for _, name := range []string{"b", "c", "b"} {
					tooloutcome.RecordIdentity(ctx, identity(name))
				}
				return "", tooloutcome.New(tooloutcome.Uncertain, "lost_response", "unknown", "Synthetic response lost.", "verify_effect").Err()
			}
			return "Synthetic distinct target completed.", nil
		}},
		{Name: "inspect", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "Synthetic inspection.", nil }},
		{Name: "ask", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
			return "", SuspendForUserInput(ctx, "evidence-question")
		}},
	}}
	paused, err := (&engine{provider: p, model: "synthetic"}).Run(context.Background(), req)
	if err != nil || !paused.Suspended || effects != 1 || p.compactions != 1 {
		t.Fatalf("pause=%+v effects=%d compactions=%d err=%v", paused, effects, p.compactions, err)
	}
	saved, err := decodeContinuation(checkpoint, req, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ToolRecovery) != 1 || saved.ToolRecovery[0].Identity == nil || saved.ToolRecovery[0].Identity.Target != "/workspace/a" || len(saved.ToolRecovery[0].Evidence) != 2 || saved.ToolRecovery[0].Evidence[0].Target != "/workspace/b" || saved.ToolRecovery[0].Evidence[1].Target != "/workspace/c" {
		t.Fatalf("evidence replaced or duplicated: %+v", saved.ToolRecovery)
	}
	req.Continuation, req.ResumeResult = checkpoint, "synthetic answer"
	completed, err := (&engine{provider: p, model: "synthetic"}).Run(context.Background(), req)
	if err != nil || completed.Suspended || effects != 2 {
		t.Fatalf("replayed original/later evidence: effects=%d result=%+v err=%v", effects, completed, err)
	}
}
