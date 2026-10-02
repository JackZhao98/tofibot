package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestRunFailureJSONLegacyAndNonFailure(t *testing.T) {
	for _, tc := range []struct{ status, err, code string }{
		{"failed", "LLM call failed: stream read error: stream error: stream ID 13; INTERNAL_ERROR; received from peer", "connection_interrupted"},
		{"failed", "invalid tool configuration", "execution_failed"},
		{"failed", "", "execution_failed"},
		{"running", "old error", ""}, {"done", "", ""}, {"cancelled", "context cancelled", ""},
	} {
		t.Run(tc.status+tc.code, func(t *testing.T) {
			encoded, err := json.Marshal(Run{ID: "legacy", Status: tc.status, Error: tc.err})
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Failure *RunFailure `json:"failure"`
				Error   string      `json:"error"`
			}
			if err = json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error != tc.err {
				t.Fatal("audit error changed")
			}
			if tc.code == "" {
				if payload.Failure != nil {
					t.Fatal("nonfailure has failure feedback")
				}
				return
			}
			if payload.Failure == nil || payload.Failure.Code != tc.code || payload.Failure.Source != "runtime" || payload.Failure.Message == "" {
				t.Fatalf("failure=%+v", payload.Failure)
			}
		})
	}
}

func TestTerminalRunClosesLivePresentationAndPreservesCompletedTools(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			s, r, c := streamFixture(t)
			defer s.Close()
			if _, err := s.BeginStream(r); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.AppendStreamDelta(context.Background(), r.ID, "partial"); err != nil || !ok {
				t.Fatalf("delta %v %v", ok, err)
			}
			for _, call := range []string{"complete", "pending"} {
				for _, step := range []string{"queued", "running"} {
					if err := s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call, Name: "synthetic", Status: step}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: "complete", Name: "synthetic", Status: "completed", Result: "retained receipt"}); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.SetRunStatus(r.ID, status, "stream read error"); err != nil || !ok {
				t.Fatalf("terminal %v %v", ok, err)
			}
			draft, err := s.StreamDraft(r.ID)
			if err != nil || draft.Status != streamDraftCancelled || draft.Content != "partial" {
				t.Fatalf("draft=%+v err=%v", draft, err)
			}
			// Old callbacks and stale status transitions cannot resurrect work or publish.
			for _, next := range []string{"running", "waiting", "queued", "done", "failed"} {
				if ok, err := s.SetRunStatus(r.ID, next, ""); err != nil || ok {
					t.Fatalf("terminal -> %s: %v %v", next, ok, err)
				}
			}
			if _, ok, err := s.AppendStreamDelta(context.Background(), r.ID, "late"); err != nil || ok {
				t.Fatalf("late delta %v %v", ok, err)
			}
			for _, call := range []string{"complete", "pending", "late-new"} {
				if err := s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call, Name: "synthetic", Status: "queued"}); err != nil {
					t.Fatal(err)
				}
			}
			activities, total, err := s.ToolActivitiesForRun(c.ID, r.ID, 0, 100)
			if err != nil || total != 2 {
				t.Fatalf("activities=%+v total=%d err=%v", activities, total, err)
			}
			for _, a := range activities {
				if a.CallID == "complete" && (a.Status != "completed" || a.Result != "retained receipt") {
					t.Fatalf("lost completed result %+v", a)
				}
				if a.CallID == "pending" && a.Status != "interrupted" {
					t.Fatalf("still working %+v", a)
				}
			}
			if _, ok, err := s.FinishRun(r.ID, c.ID, r.BotID, "late reply"); err != nil || ok {
				t.Fatalf("late finish %v %v", ok, err)
			}
			messages, _, err := s.Messages(c.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range messages {
				if m.Role == "assistant" {
					t.Fatal("failure fabricated assistant reply")
				}
			}
		})
	}
}

// Synthetic engine executes one completed tool then loses its provider stream.
// It never invokes a real tool, provider, or retry.
type toolThenStreamFailureEngine struct{}

func (toolThenStreamFailureEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	for _, status := range []string{"queued", "running", "completed"} {
		if err := req.OnToolEvent(runtime.ToolEvent{CallID: "receipt", Name: "synthetic", Status: status, Result: "successful receipt"}); err != nil {
			return runtime.Result{}, err
		}
	}
	return runtime.Result{}, errors.New("LLM call failed: stream read error: stream error: stream ID 13; INTERNAL_ERROR; received from peer")
}
func TestExecuteStreamFailurePublishesRuntimeFeedbackWithoutReplay(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: toolThenStreamFailureEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("failure", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "synthetic request", "failure-feedback")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, r)
	got, err := s.store.GetRun(r.ID)
	if err != nil || got.Status != "failed" || got.failure().Code != "connection_interrupted" {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	events, err := s.store.Events(c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event["type"] != "run" {
			continue
		}
		encoded, _ := json.Marshal(event["data"])
		var payload struct {
			Status  string      `json:"status"`
			Failure *RunFailure `json:"failure"`
		}
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Status == "failed" && payload.Failure != nil && payload.Failure.Code == "connection_interrupted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing durable structured terminal event: %+v", events)
	}
	activities, total, err := s.store.ToolActivitiesForRun(c.ID, r.ID, 0, 100)
	if err != nil || total != 1 || activities[0].Status != "completed" || activities[0].Result != "successful receipt" {
		t.Fatalf("tools=%+v total=%d err=%v", activities, total, err)
	}
	runs, err := s.store.Runs(c.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("unrequested replay: runs=%+v err=%v", runs, err)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 100)
	if err != nil || len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("fabricated model reply: messages=%+v err=%v", messages, err)
	}
}
