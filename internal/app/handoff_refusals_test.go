package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestHandoffRefusalsStateTheNextStep(t *testing.T) {
	for _, err := range []error{errRunHandoffUsed, errAnswerAfterHandoff()} {
		o, ok := tooloutcome.FromError(err)
		if !ok || o.NextAction != "end_turn" || !strings.Contains(o.Message, "end") {
			t.Fatalf("outcome = %+v", o)
		}
	}
}

func TestSecondHandoffInOneRunNamesTheNextStep(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	var second error
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		run, _ := f.server.store.GetRun(req.RunID)
		if run.Kind != "" {
			return reply(ctx, req, "the metaphor: a lighthouse", nil)
		}
		for _, tool := range req.Tools {
			if tool.Name == "handoff" {
				_, _ = tool.Execute(ctx, handoffArgs(f.meta.ID, "write the metaphor"))
				_, second = tool.Execute(ctx, handoffArgs(f.meta.ID, "write another"))
			}
		}
		return ""
	}
	f.post(t, "@tofi get the metaphor from metaphor", f.tofi)
	f.start(engine)
	f.settle(t)
	if second == nil || outcomeErrCode(second) != "handoff_already_used" {
		t.Fatalf("second handoff: %v", second)
	}
}
