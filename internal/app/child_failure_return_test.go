package app

import (
	"strings"
	"testing"
)

func failureReturns(t *testing.T, f *returnTargetFixture, child Run) []Run {
	t.Helper()
	rows, err := f.s.db.Query(`SELECT id FROM runs WHERE parent_run_id=? AND kind=?`, child.ID, runKindFollowup)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Run
	for _, id := range ids {
		r, err := f.s.GetRun(id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestFailedChildReturnsToAssignerOnce(t *testing.T) {
	for _, kind := range []string{runKindMessage, runKindGroupTask} {
		t.Run(kind, func(t *testing.T) {
			f := newReturnTargetFixture(t)
			woken := 0
			f.s.onReturnQueued = func(Run) { woken++ }
			a := f.run(f.ab.ID, f.a, Run{}, "", f.ab.ID, "done")
			origin := f.ab.ID
			if kind == runKindMessage {
				a = f.run(f.a.DMConversationID, f.a, Run{}, "", f.a.DMConversationID, "done")
				origin = a.ConversationID
			}
			b := f.run(f.ab.ID, f.b, a, kind, origin, "running")
			if ok, err := f.s.SetRunStatus(b.ID, "failed", "provider unavailable"); err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if _, err := f.s.SetRunStatus(b.ID, "failed", "again"); err != nil {
				t.Fatal(err)
			}
			got := failureReturns(t, f, b)
			if len(got) != 1 || got[0].BotID != f.a.ID || got[0].ConversationID != origin || got[0].Status != "queued" || woken != 1 {
				t.Fatalf("returns=%+v woken=%d", got, woken)
			}
			var request, source string
			if err := f.s.db.QueryRow(`SELECT request,source FROM run_wakes WHERE run_id=?`, got[0].ID).Scan(&request, &source); err != nil || source != wakeSourceChildFailure || !strings.Contains(request, "status=failed") {
				t.Fatalf("context=%q source=%q err=%v", request, source, err)
			}
		})
	}
}

func TestBudgetExhaustedChildReturnsPartialOutput(t *testing.T) {
	f := newReturnTargetFixture(t)
	a := f.run(f.a.DMConversationID, f.a, Run{}, "", f.a.DMConversationID, "done")
	b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "running")
	if _, _, err := f.s.finishRunBudgetOpts(b.ID, f.ab.ID, f.b.ID, "half the findings", "cap", finishOpts{}); err != nil {
		t.Fatal(err)
	}
	got := failureReturns(t, f, b)
	if len(got) != 1 {
		t.Fatalf("returns=%+v", got)
	}
	var request string
	if err := f.s.db.QueryRow(`SELECT request FROM run_wakes WHERE run_id=?`, got[0].ID).Scan(&request); err != nil || !strings.Contains(request, "budget_exhausted") || !strings.Contains(request, "half the findings") {
		t.Fatalf("context=%q err=%v", request, err)
	}
}

func TestFailureReturnSkipsReturnRunsAndCancels(t *testing.T) {
	f := newReturnTargetFixture(t)
	a := f.run(f.a.DMConversationID, f.a, Run{}, "", f.a.DMConversationID, "done")
	cancelled := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "running")
	if _, err := f.s.SetRunStatus(cancelled.ID, "cancelled", "stopped"); err != nil {
		t.Fatal(err)
	}
	if got := failureReturns(t, f, cancelled); len(got) != 0 {
		t.Fatalf("a cancelled child woke its assigner: %+v", got)
	}
	// A failing return run must not create another return.
	b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "done")
	ret := f.followup(b, a)
	f.exec(`UPDATE runs SET status='running' WHERE id=?`, ret.ID)
	if _, err := f.s.SetRunStatus(ret.ID, "failed", "x"); err != nil {
		t.Fatal(err)
	}
	if got := failureReturns(t, f, ret); len(got) != 0 {
		t.Fatalf("return run chained another return: %+v", got)
	}
}
