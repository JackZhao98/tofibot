package app

import (
	"context"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"strings"
	"testing"
	"time"
)

func TestGroupFollowupContextUsesCompletedResultInsteadOfOldAssignment(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, _ := store.CreateBot("Coordinator", "", "model")
	b, _ := store.CreateBot("Reviewer", "", "model")
	group, _ := store.CreateGroup("Acceptance discussion", []string{a.ID, b.ID})
	root, _ := store.AddRun(group.ID, a.ID, "")
	store.SetRunStatus(root.ID, "running", "")
	_, child, err := store.AddHandoff(group.ID, a.ID, b.ID, root.ID, "Check the proposed plan for gaps.")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.AddAssistant(group.ID, b.ID, child.ID, "The plan needs an owner for the last step.")
	if err != nil {
		t.Fatal(err)
	}
	// The result anchor must exclude a later unrelated group message.
	store.AddMessage(group.ID, "user", "", "", "Unrelated later request", "")
	followup := Run{ID: "followup-context", ConversationID: group.ID, BotID: a.ID, Kind: "group_followup", ParentRunID: child.ID, TriggerMessageID: result.ID}
	messages, system := (&Server{store: store}).buildContext(group, followup, a)
	if len(messages) == 0 {
		t.Fatal("empty followup context")
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "[completed colleague task]") || !strings.Contains(last.Content, result.Content) || !strings.Contains(last.Content, b.Name) {
		t.Fatalf("missing result boundary: %+v", last)
	}
	for _, m := range messages {
		if strings.Contains(m.Content, "[current handoff task]") || strings.Contains(m.Content, "Unrelated later request") {
			t.Fatalf("stale assignment or later message entered followup: %+v", m)
		}
	}
	if !strings.Contains(system, "do not duplicate") || !strings.Contains(system, "acknowledge") {
		t.Fatal("group conversational contract missing")
	}
	// A failed followup retry retains its kind/result anchor, not the old task.
	followup.ID = "retry-followup-context"
	followup.ParentRunID = "followup-context"
	retry, _ := (&Server{store: store}).buildContext(group, followup, a)
	if !strings.Contains(retry[len(retry)-1].Content, result.Content) {
		t.Fatal("retry lost colleague result")
	}
}

type leadingAssignmentEngine struct{ child string }

func (e leadingAssignmentEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	if req.BotID == e.child {
		return runtime.Result{Content: "@Coordinator Verified result"}, nil
	}
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "[completed colleague task]") {
			return runtime.Result{Content: "Integrated conclusion"}, nil
		}
	}
	return runtime.Result{Content: "@Reviewer Please verify the task."}, nil
}
func TestLeadingMentionPublishesOneAssignmentAndReturns(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: leadingAssignmentEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("Coordinator", "", "model")
	b, _ := s.store.CreateBot("Reviewer", "", "model")
	s.engine = leadingAssignmentEngine{child: b.ID}
	g, _ := s.store.CreateGroup("Acceptance", []string{a.ID, b.ID})
	_, r, _, err := s.store.AddUserRun(g.ID, a.ID, "Review this synthetic task", "leading-assignment")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(g, r)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ := s.store.Runs(g.ID)
		if len(runs) == 3 && runs[2].Status == "done" {
			messages, _, err := s.store.Messages(g.ID, 0, 30)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 4 {
				t.Fatalf("duplicate assignment or empty bubble: %+v", messages)
			}
			if messages[1].Notice == nil || messages[1].SenderBotID != a.ID || messages[2].SenderBotID != b.ID || messages[3].SenderBotID != a.ID {
				t.Fatalf("wrong speakers: %+v", messages)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("leading mention did not return to its requester")
}
