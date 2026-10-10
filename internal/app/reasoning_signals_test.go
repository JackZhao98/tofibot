package app

import (
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestContextBreakdownAndReplayRejectionAreRecorded(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Reasoning Bot", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "go", "reasoning-one", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	run := runs[0]
	if err = s.recordContextEstimate(run.ID, bot.Model, 600); err != nil {
		t.Fatal(err)
	}
	if err = s.recordContextBreakdown(run.ID, runtime.ContextBreakdown{System: 100, Messages: 200, Reasoning: 300}); err != nil {
		t.Fatal(err)
	}
	var sys, msgs, reasoning int
	if err = s.db.QueryRow(`SELECT estimated_system,estimated_messages,estimated_reasoning FROM run_usage WHERE run_id=?`, run.ID).Scan(&sys, &msgs, &reasoning); err != nil || sys != 100 || msgs != 200 || reasoning != 300 {
		t.Fatalf("breakdown=%d/%d/%d err=%v", sys, msgs, reasoning, err)
	}

	s.PublishReasoningReplayOff(run)
	var found map[string]any
	for _, ev := range eventsOfType(t, s, run.ConversationID, "run") {
		if ev["reasoning_replay"] == "off" {
			found = ev
		}
	}
	if found == nil || found["id"] != run.ID || found["status"] == nil {
		t.Fatalf("no reasoning_replay run event with a run snapshot: %+v", found)
	}
}
