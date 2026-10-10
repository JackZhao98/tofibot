package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
)

type longProgressProvider struct{ calls int }

func (p *longProgressProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls == 33 {
		return &provider.ChatResponse{Content: "Finished both batches."}, nil
	}
	content := ""
	if p.calls == 16 || p.calls == 31 {
		content = fmt.Sprintf("Completed batch at turn %d; continuing.", p.calls)
	}
	return &provider.ChatResponse{Content: content, ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("step-%d", p.calls), Name: "check_item", Arguments: `{}`}}}, nil
}

func (p *longProgressProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestProgressAfterThirtyModelTurnsIsPersistedAndRunCompletes(t *testing.T) {
	s, run, conversation := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(run); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &longProgressProvider{}
	calls := 0
	var publishErr error
	result, err := agent.RunAgentLoop(agent.AgentConfig{
		Ctx: ctx, Provider: p, Model: "test-model", ToolsOnly: true,
		Messages:                   []provider.Message{{Role: "user", Content: "Complete both batches."}},
		MaxToolCallsBetweenReports: 15,
		ExtraTools: []agent.ExtraBuiltinTool{{Schema: provider.Tool{Name: "check_item", Parameters: map[string]any{"type": "object"}}, HandlerCtx: func(context.Context, map[string]interface{}) (string, error) {
			calls++
			return "checked", nil
		}}},
		OnAssistantTurn: func(index int, content string) {
			_, _, publishErr = s.PublishAssistantTurn(ctx, run.ID, index, content)
			if publishErr != nil {
				cancel()
			}
		},
	}, &models.ExecutionContext{})
	if err != nil || publishErr != nil {
		t.Fatalf("long task failed: loop=%v publication=%v", err, publishErr)
	}
	if calls != 32 || p.calls != 33 || result.Content != "Finished both batches." {
		t.Fatalf("tools=%d requests=%d result=%+v", calls, p.calls, result)
	}
	if _, done, err := s.FinishRun(run.ID, conversation.ID, run.BotID, result.Content); err != nil || !done {
		t.Fatalf("finish: done=%v err=%v", done, err)
	}
	messages, _, err := s.Messages(conversation.ID, 0, 50)
	if err != nil || len(messages) != 3 || messages[0].Kind != "segment" || messages[1].Kind != "segment" || messages[2].Content != result.Content {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}

func TestAssistantTurnPersistenceRespectsRuntimeGuard(t *testing.T) {
	s, run, _ := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(run); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, -1, agent.MaxAssistantTurnIndex + 1} {
		if _, ok, err := s.PublishAssistantTurn(context.Background(), run.ID, index, "out of range"); err == nil || ok {
			t.Fatalf("accepted invalid index %d: ok=%v err=%v", index, ok, err)
		}
	}
	for _, index := range []int{agent.MaxStepsWithProgressReports, agent.MaxAssistantTurnIndex} {
		if _, ok, err := s.PublishAssistantTurn(context.Background(), run.ID, index, "reserved completion progress"); err != nil || !ok {
			t.Fatalf("rejected legal index %d: ok=%v err=%v", index, ok, err)
		}
	}
}
