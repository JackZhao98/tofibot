package app

import (
	"strings"
	"testing"
	"time"
)

// Group triage with the working members (B15) and the task version for
// steered messages (B16).

func lastPrompt(e *followUpEngine) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.prompts) == 0 {
		return ""
	}
	return e.prompts[len(e.prompts)-1]
}

func (e *followUpEngine) runCount(bot string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs[bot]
}

func (e *followUpEngine) readText(bot string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.read[bot]
}

// startAnalysis posts the first message and waits until Quill works on it.
func startAnalysis(t *testing.T, f *triageFixture, e *followUpEngine) {
	t.Helper()
	f.post(t, f.group.ID, "analyze SOFI", "st-1")
	if got := waitMemberRun(t, e); got != f.stocks.ID {
		t.Fatalf("started %s", got)
	}
}

func TestTriageTellsWhoIsWorkingOnWhatAndASteerAnswerGoesToThatRun(t *testing.T) {
	f, e := newFollowUpFixture(t)
	startAnalysis(t, f, e)
	e.steer = true
	f.post(t, f.group.ID, "only the US listing", "st-2")
	waitTriageCalls(t, e, 2)
	p := lastPrompt(e)
	for _, want := range []string{"Quill", "analyze SOFI", "working since"} {
		if !strings.Contains(p, want) {
			t.Fatalf("triage prompt lacks %q:\n%s", want, p)
		}
	}
	close(e.hold)
	runs := settleTriageRuns(t, f.s, f.group.ID, 3)
	if n := e.runCount(f.stocks.ID); n != 1 {
		t.Fatalf("Quill ran %d times, want 1 (runs %+v)", n, runs)
	}
	if !strings.Contains(e.readText(f.stocks.ID), "only the US listing") {
		t.Fatalf("the running member did not read the message: %q", e.readText(f.stocks.ID))
	}
}

func TestTriageNewRequestWhileAnotherMemberWorksGoesToThePickedMember(t *testing.T) {
	f, e := newFollowUpFixture(t)
	startAnalysis(t, f, e)
	e.target = f.travel.ID
	f.post(t, f.group.ID, "and a flight", "st-3")
	if got := waitMemberRun(t, e); got != f.travel.ID {
		t.Fatalf("started %s", got)
	}
	close(e.hold)
	settleTriageRuns(t, f.s, f.group.ID, 4)
	if strings.Contains(e.readText(f.stocks.ID), "and a flight") {
		t.Fatalf("Quill read the new request: %q", e.readText(f.stocks.ID))
	}
	if e.runCount(f.travel.ID) != 1 {
		t.Fatalf("Atlas ran %d times", e.runCount(f.travel.ID))
	}
}

func TestTriageNewRequestForTheBusyMemberQueuesAsANewRun(t *testing.T) {
	f, e := newFollowUpFixture(t)
	startAnalysis(t, f, e)
	f.post(t, f.group.ID, "also check earnings", "st-4")
	waitTriageCalls(t, e, 2)
	close(e.hold)
	settleTriageRuns(t, f.s, f.group.ID, 4)
	if n := e.runCount(f.stocks.ID); n != 2 {
		t.Fatalf("Quill ran %d times, want 2", n)
	}
}

func TestFailedTriageFallsBackToTheMemberWorkingOnTheLatestMessage(t *testing.T) {
	for _, name := range []string{"model failure", "invalid answer"} {
		t.Run(name, func(t *testing.T) {
			f, e := newFollowUpFixture(t)
			startAnalysis(t, f, e)
			if name == "model failure" {
				e.fail = true
			} else {
				e.target = "nobody"
			}
			f.post(t, f.group.ID, "only the US listing", "st-5")
			waitTriageCalls(t, e, 2)
			close(e.hold)
			settleTriageRuns(t, f.s, f.group.ID, 3)
			if n := e.runCount(f.stocks.ID); n != 1 {
				t.Fatalf("Quill ran %d times, want 1", n)
			}
			if !strings.Contains(e.readText(f.stocks.ID), "only the US listing") {
				t.Fatalf("the working member did not read it: %q", e.readText(f.stocks.ID))
			}
		})
	}
}

func TestASteerAnswerAfterTheRunAnsweredStartsAContinuationRun(t *testing.T) {
	f, e := newFollowUpFixture(t)
	startAnalysis(t, f, e)
	e.steer = true
	e.gate = make(chan struct{})
	f.post(t, f.group.ID, "only the US listing", "st-6")
	waitTriageCalls(t, e, 2)
	close(e.hold) // Quill answers while triage is still deciding
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ := f.s.store.Runs(f.group.ID)
		done := false
		for _, r := range runs {
			done = done || (r.BotID == f.stocks.ID && r.Kind == runKindGroupChat && r.Status == "done")
		}
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(e.gate)
	settleTriageRuns(t, f.s, f.group.ID, 4)
	if n := e.runCount(f.stocks.ID); n != 2 {
		t.Fatalf("Quill ran %d times, want 2", n)
	}
	var all strings.Builder
	for _, m := range e.ctx[f.stocks.ID] {
		all.WriteString(m.Content + "\n")
	}
	if !strings.Contains(all.String(), "only the US listing") {
		t.Fatalf("the continuation run lacks the message:\n%s", all.String())
	}
}

func TestSettingOffGivesTriageNoWorkingMembersAndNoSteerOption(t *testing.T) {
	f, e := newFollowUpFixture(t)
	off := "false"
	if err := f.s.saveAutomationValue(autoGroupFollowUp, &off); err != nil {
		t.Fatal(err)
	}
	startAnalysis(t, f, e)
	e.target = f.travel.ID
	f.post(t, f.group.ID, "and a flight", "st-7")
	waitMemberRun(t, e)
	if p := lastPrompt(e); strings.Contains(p, "working now") || strings.Contains(p, "steer") {
		t.Fatalf("plain triage prompt carries the steering option:\n%s", p)
	}
	close(e.hold)
	settleTriageRuns(t, f.s, f.group.ID, 4)
}

// taskOf returns the person-given task the run holds.
func taskOf(t *testing.T, f *triageFixture, bot string) (id string, version, based int) {
	t.Helper()
	err := f.s.store.db.QueryRow(`SELECT id,spec_version,result_based_on_version FROM work_items WHERE conversation_id=? AND bot_id=? AND kind='task' ORDER BY created_at LIMIT 1`, f.group.ID, bot).Scan(&id, &version, &based)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestSteeredMessageBumpsTheHeldTaskOnceAndTheResultIsBasedOnIt(t *testing.T) {
	cases := []string{"triage steer", "addressed"}
	for _, how := range cases {
		t.Run(how, func(t *testing.T) {
			f, e := newFollowUpFixture(t)
			startAnalysis(t, f, e)
			id, v, _ := taskOf(t, f, f.stocks.ID)
			if v != 1 {
				t.Fatalf("version before = %d", v)
			}
			text := "only the US listing"
			if how == "triage steer" {
				e.steer = true
			} else {
				text = "@Quill " + text
			}
			f.post(t, f.group.ID, text, "st-8")
			if how == "triage steer" {
				waitTriageCalls(t, e, 2)
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, v, _ = taskOf(t, f, f.stocks.ID); v == 2 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if v != 2 {
				t.Fatalf("version after the steered message = %d, want 2", v)
			}
			close(e.hold)
			wantRuns := 3
			if how == "addressed" {
				wantRuns = 2 // no triage for the second message
			}
			settleTriageRuns(t, f.s, f.group.ID, wantRuns)
			_, v, based := taskOf(t, f, f.stocks.ID)
			if v != 2 || based != 2 {
				t.Fatalf("version=%d based_on=%d, want 2 and 2", v, based)
			}
			var revs, updates int
			f.s.store.db.QueryRow(`SELECT COUNT(*) FROM task_revisions WHERE task_id=? AND version=2 AND source='person'`, id).Scan(&revs)
			f.s.store.db.QueryRow(`SELECT COUNT(*) FROM bot_inbox WHERE task_id=? AND kind=?`, id, inboxTaskUpdate).Scan(&updates)
			if revs != 1 || updates != 1 {
				t.Fatalf("task_revisions=%d task_update rows=%d, want 1 and 1", revs, updates)
			}
		})
	}
}
