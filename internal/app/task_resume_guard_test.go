package app

import (
	"context"
	"strings"
	"testing"
)

// Only the resume run works a paused task: every other run reads the list as information.
func TestTheUnfinishedListTellsOtherRunsNotToActOnIt(t *testing.T) {
	s, b, c, run := continuityStore(t)
	endWithOutcome(t, s, run, c, sayOutcomeBlocked, "X did not open the crop view.")
	block := s.unfinishedTasksContext(c.ID, b.ID)
	for _, want := range []string{"information", "resumes automatically", "unless the person explicitly asks"} {
		if !strings.Contains(block, want) {
			t.Fatalf("the list does not say %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "Finish or report each entry") {
		t.Fatalf("the list still tells every run to finish the entries:\n%s", block)
	}
}

// A resume run queued for a task whose work item was closed meanwhile must not start.
func TestAResumeRunForATaskThatWasClosedElsewhereDoesNotStart(t *testing.T) {
	s, _, _, run := continuityStore(t)
	pauseAndEnd(t, s, run.ID, "cancelled")
	resume, ok := resumeAfter(t, s, run.ID)
	if !ok {
		t.Fatal("no resume run")
	}
	if _, err := s.db.Exec(`UPDATE work_items SET status='done',completed_at=?,closed_at=? WHERE assignee_run_id=?`, now(), now(), resume.ID); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.claimRun(context.Background(), resume.ID); err != nil || claimed {
		t.Fatalf("a finished task's resume run was claimed: %v %v", claimed, err)
	}
	got, err := s.GetRun(resume.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("run = %+v %v", got, err)
	}
}

// The paused line of a cancelled run follows the work item: a finished task shows no pause.
func TestPausedLabelFollowsTheWorkItem(t *testing.T) {
	s, _, c, run := continuityStore(t)
	pauseAndEnd(t, s, run.ID, "cancelled")
	runs := []Run{{ID: run.ID, Status: "cancelled"}}
	s.decoratePausedRuns(runs)
	if runs[0].PausedTaskTitle == "" {
		t.Fatal("a paused task shows no label")
	}
	if _, err := s.db.Exec(`UPDATE work_items SET status='done',completed_at=?,closed_at=? WHERE source_run_id=?`, now(), now(), run.ID); err != nil {
		t.Fatal(err)
	}
	runs = []Run{{ID: run.ID, Status: "cancelled"}}
	s.decoratePausedRuns(runs)
	if runs[0].PausedTaskTitle != "" {
		t.Fatalf("a finished task still shows %q", runs[0].PausedTaskTitle)
	}
	_ = c
}
