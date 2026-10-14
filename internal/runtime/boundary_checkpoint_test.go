package runtime

import (
	"encoding/json"
	"testing"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/provider"
)

func boundaryFor(model string) *agent.Continuation {
	return &agent.Continuation{
		Version: 2, Kind: agent.ContinuationBoundary, Attempt: 1,
		Messages:   []provider.Message{{Role: "user", Content: "synthetic"}},
		TotalUsage: provider.Usage{InputTokens: 3, OutputTokens: 1},
		ModelUsage: map[string]agent.ModelUsage{model: {InputTokens: 3, OutputTokens: 1, APICallCount: 1}},
		LLMCalls:   1,
	}
}

func TestCheckpointHookEncodesAndRoundTrips(t *testing.T) {
	req := Request{RunID: "run-1", BotID: "bot-1"}
	var kind string
	var raw json.RawMessage
	req.OnCheckpoint = func(k string, cp json.RawMessage) error { kind, raw = k, cp; return nil }
	if err := checkpointHook(req, "m")("turn", boundaryFor("m")); err != nil {
		t.Fatal(err)
	}
	if kind != "turn" || len(raw) == 0 {
		t.Fatalf("kind=%q raw=%s", kind, raw)
	}
	c, err := BoundaryCheckpoint(raw, "run-1", "bot-1")
	if err != nil || c.Attempt != 1 || c.EffectiveKind() != agent.ContinuationBoundary {
		t.Fatalf("round trip: %+v %v", c, err)
	}
	if _, err := BoundaryCheckpoint(raw, "other-run", "bot-1"); err == nil {
		t.Fatal("checkpoint of another run accepted")
	}
	if _, err := BoundaryCheckpoint(raw, "run-1", "other-bot"); err == nil {
		t.Fatal("checkpoint of another bot accepted")
	}
}

func TestCheckpointHookDiscardsAnInconsistentSnapshot(t *testing.T) {
	req := Request{RunID: "run-1", BotID: "bot-1"}
	called, empty := false, false
	req.OnCheckpoint = func(_ string, cp json.RawMessage) error { called, empty = true, len(cp) == 0; return nil }
	broken := boundaryFor("m")
	broken.LLMCalls = 2 // no longer matches the per-model usage
	if err := checkpointHook(req, "m")("tool", broken); err != nil {
		t.Fatal(err)
	}
	if !called || !empty {
		t.Fatalf("called=%v empty=%v: the store must be told to drop its stale checkpoint", called, empty)
	}
}

func TestBoundaryCheckpointRejectsInputWaitAndFutureEnvelope(t *testing.T) {
	wait := boundaryFor("m")
	wait.Kind, wait.QuestionID, wait.WaitingToolCallID, wait.WaitingToolName = agent.ContinuationInputWait, "q", "c", "ask"
	wait.Messages = []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "c", Name: "ask"}}}}
	raw, err := json.Marshal(continuationEnvelope{Version: continuationVersion, RunID: "r", BotID: "b", Model: "m", Agent: wait})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BoundaryCheckpoint(raw, "r", "b"); err == nil {
		t.Fatal("input wait read as a boundary checkpoint")
	}
	future, _ := json.Marshal(continuationEnvelope{Version: continuationVersion + 1, RunID: "r", BotID: "b", Model: "m", Agent: boundaryFor("m")})
	if _, err := BoundaryCheckpoint(future, "r", "b"); err == nil {
		t.Fatal("envelope from a newer build accepted")
	}
}
