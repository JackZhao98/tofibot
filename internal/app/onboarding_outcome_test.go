package app

import (
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

// The production incident: a name-only set_bot_profile call was reported as an
// uncertain effect, and the corrected second call was fenced by that record.
func TestOnboardingProfileNameOnlyThenCompleteCall(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _, err := s.store.CreateOnboardingBot(uuid.NewString(), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Call yourself tofi and help with notes.", "outcome-run")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.store.SetRunStatus(run.ID, "running", "")
	run, _ = s.store.GetRun(run.ID)
	engine := reviewBatchEngine(t, [][]reviewCall{
		{{"set_bot_profile", `{"name":"tofi"}`}},
		{{"set_bot_profile", `{"name":"tofi","instructions":"Keep the owner's notes tidy and concise."}`}},
	}, 0)
	var events []runtime.ToolEvent
	_, err = engine.Run(t.Context(), runtime.Request{BotID: b.ID, RunID: run.ID, Model: "synthetic", Tools: s.tools(c, run), OnToolEvent: func(ev runtime.ToolEvent) error {
		events = append(events, ev)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	var failed, completed *runtime.ToolEvent
	for i := range events {
		switch events[i].Status {
		case "failed":
			if failed == nil {
				failed = &events[i]
			}
		case "completed":
			completed = &events[i]
		}
	}
	if failed == nil || failed.Outcome == nil {
		t.Fatalf("first call has no definite outcome: %+v", events)
	}
	o := failed.Outcome
	if o.Status != tooloutcome.Validation || o.Code != "invalid_arguments" || o.Certainty != "not_executed" || !contains(o.Message, "instructions") {
		t.Fatalf("first call outcome = %+v", o)
	}
	if completed == nil {
		t.Fatalf("second call did not succeed: %+v", events)
	}
	got, _ := s.store.GetBot(b.ID)
	if got.Name != "tofi" || got.Instructions == "" {
		t.Fatalf("profile=%+v", got)
	}
	c, _ = s.store.GetConversation(c.ID)
	if c.Name != "tofi" {
		t.Fatalf("conversation name=%q", c.Name)
	}
	var status string
	if err = s.store.db.QueryRow(`SELECT status FROM bot_onboarding WHERE bot_id=?`, b.ID).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("marker=%q err=%v", status, err)
	}
	stored, _ := s.store.Events(c.ID, 0)
	bots := 0
	for _, e := range stored {
		if e["type"] == "bot" {
			bots++
		}
	}
	if bots < 2 { // creation + profile
		t.Fatalf("bot events=%d", bots)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
