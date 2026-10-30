package app

import (
	"testing"
	"time"
)

func pauseAndEnd(t *testing.T, s *Store, runID, status string) {
	pauseAndEndFor(t, s, runID, status, interruptSteered)
}

func pauseAndEndFor(t *testing.T, s *Store, runID, status, reason string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pauseTaskForRunTx(tx, runID, reason); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE runs SET status=? WHERE id=?`, status, runID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func resumeAfter(t *testing.T, s *Store, ended string) (Run, bool) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	r, ok, err := resumePausedTasksTx(tx, ended)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return r, ok
}

func continuityStore(t *testing.T) (*Store, Bot, Conversation, Run) {
	t.Helper()
	s, b, c := scheduleTestStore(t)
	t.Cleanup(func() { s.Close() })
	_, runs, _, err := s.AddUserRuns(c.ID, "Make the image square", "t1", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(runs[0].ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	return s, b, c, runs[0]
}

func TestAnInterruptedTaskResumesEveryTime(t *testing.T) {
	s, _, _, run := continuityStore(t)
	cur := run
	for i := 1; i <= taskAutoResumeLimit+3; i++ {
		pauseAndEnd(t, s, cur.ID, "cancelled")
		next, ok := resumeAfter(t, s, cur.ID)
		if !ok || next.ParentRunID != cur.ID || next.Kind != run.Kind {
			t.Fatalf("resume %d: ok=%v run=%+v", i, ok, next)
		}
		if _, err := s.SetRunStatus(next.ID, "running", ""); err != nil {
			t.Fatal(err)
		}
		cur = next
	}
}

// A Bot that keeps stopping the same task by itself stops restarting it, but the task stays on its list.
func TestATaskTheBotKeepsStoppingResumesAtMostTwiceByItself(t *testing.T) {
	s, _, _, run := continuityStore(t)
	cur := run
	for i := 1; i <= taskAutoResumeLimit; i++ {
		pauseAndEndFor(t, s, cur.ID, "done", interruptEarlyStop)
		next, ok := resumeAfter(t, s, cur.ID)
		if !ok || next.ParentRunID != cur.ID {
			t.Fatalf("resume %d: ok=%v run=%+v", i, ok, next)
		}
		if _, err := s.SetRunStatus(next.ID, "running", ""); err != nil {
			t.Fatal(err)
		}
		cur = next
	}
	pauseAndEndFor(t, s, cur.ID, "done", interruptEarlyStop)
	if r, ok := resumeAfter(t, s, cur.ID); ok {
		t.Fatalf("a third automatic resume started: %+v", r)
	}
	var status, interrupted string
	if err := s.db.QueryRow(`SELECT status,interrupted_at FROM work_items WHERE source='user_request'`).Scan(&status, &interrupted); err != nil || status != "todo" || interrupted == "" {
		t.Fatalf("the task is not left on the list: %s %q %v", status, interrupted, err)
	}
}

func TestAPersonsQueuedMessageRunsBeforeTheResume(t *testing.T) {
	s, b, c, run := continuityStore(t)
	pauseAndEnd(t, s, run.ID, "cancelled")
	_, queued, _, err := s.AddUserRuns(c.ID, "A quick question", "q1", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := resumeAfter(t, s, run.ID); ok {
		t.Fatalf("the Bot resumed ahead of a person's message: %+v", r)
	}
	if _, err = s.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, queued[0].ID); err != nil {
		t.Fatal(err)
	}
	r, ok := resumeAfter(t, s, queued[0].ID)
	if !ok || r.QueueSeq <= queued[0].QueueSeq {
		t.Fatalf("resume after the message ended: ok=%v run=%+v", ok, r)
	}
}

func TestANewerOccurrenceDropsThePausedOne(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "Collect the topics", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 3600, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-2*time.Hour))
	first, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim %v %v", first, err)
	}
	if ok, err := s.SetRunStatus(first[0].ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	pauseAndEnd(t, s, first[0].ID, "cancelled")
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	second, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(second) != 1 {
		t.Fatalf("second claim %v %v", second, err)
	}
	if _, err = s.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, second[0].ID); err != nil {
		t.Fatal(err)
	}
	if r, ok := resumeAfter(t, s, second[0].ID); ok {
		t.Fatalf("an occurrence a newer one replaced was resumed: %+v", r)
	}
	var status, summary string
	if err := s.db.QueryRow(`SELECT status,result_summary FROM work_items WHERE schedule_id=?`, x.ID).Scan(&status, &summary); err != nil || status != "cancelled" || summary != "superseded_by_next_occurrence" {
		t.Fatalf("paused occurrence: %s %q %v", status, summary, err)
	}
}

func TestStopAllEndsThePausedTasksOfTheBot(t *testing.T) {
	s, b, c, run := continuityStore(t)
	pauseAndEnd(t, s, run.ID, "cancelled")
	_, other, _, err := s.AddUserRuns(c.ID, "Something else", "o1", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelRunTree(other[0].ID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM work_items WHERE source='user_request'`).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("paused task after Stop all: %s %v", status, err)
	}
	if r, ok := resumeAfter(t, s, other[0].ID); ok {
		t.Fatalf("resumed after Stop all: %+v", r)
	}
}
