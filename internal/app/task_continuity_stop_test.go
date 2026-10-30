package app

import (
	"strings"
	"testing"
)

func endWithOutcome(t *testing.T, s *Store, run Run, c Conversation, outcome, text string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO run_say_answers(run_id,call_id,text,wake,sources,quiet,created_at,outcome) VALUES(?,?,?,?,?,?,?,?)`, run.ID, "say-1", text, "", "", 0, now(), outcome); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.FinishRunStructured(run.ID, c.ID, run.BotID, text, nil); err != nil || !ok {
		t.Fatalf("finish: ok=%v err=%v", ok, err)
	}
}

func TestAnAnswerThatReportsPendingWorkPausesTheTaskAndTheBotResumesIt(t *testing.T) {
	s, _, c, run := continuityStore(t)
	endWithOutcome(t, s, run, c, sayOutcomePending, "The image is square; the upload is left.")
	var status, reason string
	if err := s.db.QueryRow(`SELECT status,interrupt_reason FROM work_items WHERE source='user_request'`).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	// The run ended with nothing else waiting, so the Bot has already started the task again.
	var resume Run
	if err := s.db.QueryRow(`SELECT id,parent_run_id,status FROM runs WHERE parent_run_id=?`, run.ID).Scan(&resume.ID, &resume.ParentRunID, &resume.Status); err != nil || resume.Status != "queued" {
		t.Fatalf("no queued resume run: %+v %v", resume, err)
	}
	if reason != interruptEarlyStop {
		t.Fatalf("reason = %q", reason)
	}
	var holder string
	var count int
	if err := s.db.QueryRow(`SELECT assignee_run_id,resume_count FROM work_items WHERE source='user_request'`).Scan(&holder, &count); err != nil || holder != resume.ID || count != 1 {
		t.Fatalf("holder=%q count=%d err=%v", holder, count, err)
	}
	var state string
	if err := s.db.QueryRow(`SELECT resume_state FROM work_items WHERE source='user_request'`).Scan(&state); err != nil || !strings.Contains(state, "the upload is left") {
		t.Fatalf("the report is not in the record: %s %v", state, err)
	}
}

func TestABlockerReportWaitsForThePersonAndContinueResolvesAgainstTheList(t *testing.T) {
	s, b, c, run := continuityStore(t)
	endWithOutcome(t, s, run, c, sayOutcomeBlocked, "X did not open the crop view.")
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE parent_run_id=?`, run.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a blocked task started a run by itself: %d %v", n, err)
	}
	var id, status string
	if err := s.db.QueryRow(`SELECT id,status FROM work_items WHERE source='user_request'`).Scan(&id, &status); err != nil || status != "blocked" {
		t.Fatalf("task: %s %s %v", id, status, err)
	}
	block := s.unfinishedTasksContext(c.ID, b.ID)
	if !strings.HasPrefix(block, "[unfinished tasks]") || !strings.Contains(block, id) || !strings.Contains(block, `"state":"blocked"`) {
		t.Fatalf("the list block does not name the task:\n%s", block)
	}
	if other := s.unfinishedTasksContext(c.ID, "another-bot"); other != "" {
		t.Fatalf("another Bot sees the list: %s", other)
	}
}

func TestTakingUpAPausedTaskBindsItToTheRunAndReturnsItsRecord(t *testing.T) {
	s, b, c, run := continuityStore(t)
	pauseAndEnd(t, s, run.ID, "cancelled")
	var id string
	if err := s.db.QueryRow(`SELECT id FROM work_items WHERE source='user_request'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(c.ID, "Continue what you left unfinished", "c1", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if !s.bindPausedTask(id, runs[0].ID, c.ID, b.ID) {
		t.Fatal("the paused task was not bound")
	}
	if s.bindPausedTask(id, runs[0].ID, c.ID, b.ID) {
		t.Fatal("a task that is no longer paused was bound again")
	}
	if record := s.taskRecordText(id); !strings.Contains(record, "Make the image square") {
		t.Fatalf("record: %s", record)
	}
	// The run that holds the task is finished by the person's request, so nothing resumes it a second time.
	if r, ok := resumeAfter(t, s, run.ID); ok {
		t.Fatalf("a task a run already holds was resumed: %+v", r)
	}
}
