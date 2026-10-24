package app

import (
	"testing"
)

// B7: a Bot starts a command with notify_on_exit, then answers that the command
// is running. The wait wakes it later; the completion review must not send the
// answer back and keep the Bot polling.
func commandWaitSayFixture(t *testing.T) (taskFix, Run, *sayRunState) {
	t.Helper()
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, "")
	r := f.running(f.g.ID, f.a)
	r.structured = true
	if _, err := f.srv.registerCommandExitFor(t.Context(), r, "term-1"); err != nil {
		t.Fatal(err)
	}
	st := &sayRunState{checksPending: true}
	reviews := 0
	st.review = func() bool { reviews++; return true }
	f.srv.sayRuns.Store(r.ID, st)
	return f, r, st
}

func TestNotifyOnExitWaitsCountAsPendingWaits(t *testing.T) {
	f, r, _ := commandWaitSayFixture(t)
	if !f.s.runHasPendingWait(r.ID) {
		t.Fatal("the run holds no pending wait")
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE run_id=? AND status='pending' AND kind IN ('command_exit','timer')`, r.ID); n != 2 {
		t.Fatalf("command wait and paired timer rows with run_id = %d, want 2", n)
	}
}

func TestPendingAnswerWithCommandWaitIsNotSentBackByTheReview(t *testing.T) {
	f, r, st := commandWaitSayFixture(t)
	spent := false
	st.review = func() bool { spent = true; return true }
	err := sayOutcome(f.srv, f.g, r, "b7", `{"purpose":"answer","outcome":"pending","text":"The command is running; I will be woken when it ends."}`)
	if err != nil {
		t.Fatalf("pending answer sent back: %v", outcomeErrCode(err))
	}
	if spent {
		t.Fatal("the review was spent on a pending answer")
	}
	if _, ok := f.s.sayAnswer(r.ID); !ok {
		t.Fatal("the answer was not recorded")
	}
}

func TestDoneAnswerWithCommandWaitIsStillReviewed(t *testing.T) {
	f, r, _ := commandWaitSayFixture(t)
	err := sayOutcome(f.srv, f.g, r, "b7d", `{"purpose":"answer","outcome":"done","text":"The command finished and the build passed."}`)
	if code := outcomeErrCode(err); code != "answer_review" {
		t.Fatalf("code = %q, want answer_review", code)
	}
}
