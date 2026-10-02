package app

import (
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestTaskCompletionFinalReviewOnlyRepairsToolBackedDrafts(t *testing.T) {
	worked := false
	review := taskCompletionFinalReview(func() bool { return worked })
	if review == nil {
		t.Fatal("completion review is unavailable")
	}
	if reminder, err := review("Hello."); err != nil || reminder != "" {
		t.Fatalf("tool-free reply unexpectedly repaired: %q %v", reminder, err)
	}
	worked = true
	reminder, err := review("The task is complete.")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"requested deliverable, fields, and categories",
		"actual completed work and tool results",
		"continue the remaining authorized work",
		"searched, checked, completed, unavailable, or verified",
		"or replay an action with an uncertain outcome",
	} {
		if !strings.Contains(reminder, required) {
			t.Fatalf("completion reminder missing %q: %q", required, reminder)
		}
	}
}

func TestCompletionReviewToolWorkExcludesReadOnlyIntrospection(t *testing.T) {
	if completionReviewTool("ask_user_question") {
		t.Fatal("a QuestionCard answer alone must not trigger a second final reply")
	}
	if !completionReviewTool("query_source") {
		t.Fatal("substantive tool work must retain final review")
	}
	s, bot, conversation := scheduleTestStore(t)
	defer s.Close()
	const runID = "completion-review-run"
	record := func(callID, name string) {
		t.Helper()
		for _, status := range []string{"queued", "running", "completed"} {
			if err := s.RecordToolEvent(conversation.ID, bot.ID, runID, runtime.ToolEvent{CallID: callID, Name: name, Status: status}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"get_context_usage", "inspect_recent_runs", "search_history", "ask_user_question"} {
		record("read-"+name, name)
	}
	if found, err := s.hasCompletionReviewToolWork(runID); err != nil || found {
		t.Fatalf("introspection counted as task work: found=%v err=%v", found, err)
	}
	record("source", "query_source")
	if found, err := s.hasCompletionReviewToolWork(runID); err != nil || !found {
		t.Fatalf("substantive tool work missing: found=%v err=%v", found, err)
	}
}

func TestTaskCompletionFinalReviewRejectsNilObservation(t *testing.T) {
	if review := taskCompletionFinalReview(nil); review != nil {
		t.Fatal("nil observation should not install a review callback")
	}
}
