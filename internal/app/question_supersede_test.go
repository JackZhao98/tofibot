package app

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func parkedApproval(t *testing.T, s *Store, c Conversation, r Run) Question {
	t.Helper()
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Send it?", Type: questionApproval, Approval: &ApprovalDetails{Action: "send", Target: "a@example.test", Impact: "one email"}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, []byte(`{"version":1,"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	return q
}

func sendFromPerson(t *testing.T, s *Store, c Conversation, r Run, text string) error {
	t.Helper()
	_, _, _, _, err := s.addUserRunsSteering(c.ID, text, "", []runSpec{{BotID: r.BotID, Model: r.Model}}, nil)
	return err
}

func TestSupersedeApprovalDoesNotExecuteAndRunContinues(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	q := parkedApproval(t, s, c, r)
	if err := sendFromPerson(t, s, c, r, "Make it shorter first"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetQuestion(q.ID)
	if got.Status != questionCancelled || !got.Card().Superseded || string(got.Answer) == "true" {
		t.Fatalf("not superseded: %+v", got)
	}
	// Approving afterwards is refused: the action can never be authorized.
	if _, _, err := s.AnswerQuestion(q.ID, "human", true); !errors.Is(err, ErrQuestionNotPending) {
		t.Fatalf("approve after supersede: %v", err)
	}
	if _, ok, err := s.nextQueuedRun(c.ID); err != nil || !ok {
		t.Fatalf("run does not continue: %v %v", ok, err)
	}
	if res := (&Server{store: s}).inputResumeResult(got); res != supersededResult {
		t.Fatalf("model result: %s", res)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='question' AND data LIKE '%"superseded":true%'`, c.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("card event: %d %v", n, err)
	}
}

func TestApproveThenMessageExecutesOnce(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	q := parkedApproval(t, s, c, r)
	if _, _, err := s.AnswerQuestion(q.ID, "human", true); err != nil {
		t.Fatal(err)
	}
	if err := sendFromPerson(t, s, c, r, "also cc Sam"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetQuestion(q.ID)
	if got.Status != questionAnswered || got.Card().Superseded || string(got.Answer) != "true" {
		t.Fatalf("approval was overwritten: %+v", got)
	}
	if res := (&Server{store: s}).inputResumeResult(got); res != "true" {
		t.Fatalf("model result: %s", res)
	}
}

func TestMessageInAnotherConversationLeavesTheCard(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	q := parkedApproval(t, s, c, r)
	other, err := s.CreateBot("other bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	oc, _ := s.GetConversation(other.DMConversationID)
	if _, _, _, _, err = s.addUserRunsSteering(oc.ID, "hello", "", []runSpec{{BotID: other.ID, Model: other.Model}}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetQuestion(q.ID); got.Status != questionPending {
		t.Fatalf("card moved: %s", got.Status)
	}
}

func TestScheduledRunCardIsNotSuperseded(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	if _, err := s.db.Exec(`UPDATE runs SET kind=? WHERE id=?`, runKindSchedule, r.ID); err != nil {
		t.Fatal(err)
	}
	q := parkedApproval(t, s, c, r)
	if err := sendFromPerson(t, s, c, r, "hi"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetQuestion(q.ID); got.Status != questionPending {
		t.Fatalf("unattended card superseded: %s", got.Status)
	}
	// The helper itself refuses an unattended run, whichever path reaches it.
	tx, _ := s.db.Begin()
	if err := supersedeRunQuestionsTx(tx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetQuestion(q.ID); got.Status != questionPending {
		t.Fatalf("helper superseded an unattended card: %s", got.Status)
	}
}

// An approve click and a message that land together resolve to exactly one
// outcome: approved (message queued normally) or superseded, never both.
func TestApproveRacesMessageToExactlyOneOutcome(t *testing.T) {
	for i := 0; i < 20; i++ {
		s, c, r := questionFixture(t)
		q := parkedApproval(t, s, c, r)
		var wg sync.WaitGroup
		var approveErr, sendErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, _, approveErr = s.AnswerQuestion(q.ID, "human", true) }()
		go func() { defer wg.Done(); sendErr = sendFromPerson(t, s, c, r, "wait, change it") }()
		wg.Wait()
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		got, _ := s.GetQuestion(q.ID)
		switch got.Status {
		case questionAnswered:
			if approveErr != nil || string(got.Answer) != "true" || got.Card().Superseded {
				t.Fatalf("round %d: approved but %v %+v", i, approveErr, got)
			}
		case questionCancelled:
			if !errors.Is(approveErr, ErrQuestionNotPending) || !got.Card().Superseded || len(got.Answer) != 0 {
				t.Fatalf("round %d: superseded but approve=%v %+v", i, approveErr, got)
			}
		default:
			t.Fatalf("round %d: status %s", i, got.Status)
		}
		s.Close()
	}
}
