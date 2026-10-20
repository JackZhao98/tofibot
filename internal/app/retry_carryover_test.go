package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// A retry of a failed run must see the tool work the failed attempt already
// completed, so side effects are not replayed.
func TestRetryCarriesCompletedToolResults(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	if changed, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !changed {
		t.Fatalf("start: changed=%v err=%v", changed, err)
	}
	for _, event := range []runtime.ToolEvent{
		{CallID: "send-1", Name: "send_report", Arguments: `{"to":"synthetic"}`, Status: "queued"},
		{CallID: "send-1", Name: "send_report", Result: "Report delivered.", Status: "completed"},
	} {
		if err := s.store.RecordToolEvent(c.ID, old.BotID, old.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if changed, err := s.store.SetRunStatus(old.ID, "failed", "stream read error"); err != nil || !changed {
		t.Fatalf("fail: changed=%v err=%v", changed, err)
	}
	retry, err := s.store.RetryRun(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := s.retryToolCarryover(retry)
	for _, want := range []string{"send-1", "Report delivered.", "Do not replay", "connection_interrupted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("retry carryover missing %q:\n%s", want, got)
		}
	}
	bot, err := s.store.GetBot(retry.BotID)
	if err != nil {
		t.Fatal(err)
	}
	parts, _ := s.buildContextParts(c, retry, bot)
	inContext := false
	for _, m := range parts {
		inContext = inContext || strings.Contains(m.Content, "Report delivered.")
	}
	if !inContext {
		t.Fatal("the retry's model context does not carry the completed tool results")
	}
	if fresh := s.retryToolCarryover(old); fresh != "" {
		t.Fatalf("the first attempt has nothing to carry: %s", fresh)
	}
	// The retry chain keeps carrying through a second failure.
	if changed, err := s.store.SetRunStatus(retry.ID, "running", ""); err != nil || !changed {
		t.Fatal(err)
	}
	if _, err := s.store.SetRunStatus(retry.ID, "failed", "stream read error"); err != nil {
		t.Fatal(err)
	}
	again, err := s.store.RetryRun(retry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.retryToolCarryover(again), "send-1") {
		t.Fatal("second retry lost the first attempt's completed results")
	}
}
