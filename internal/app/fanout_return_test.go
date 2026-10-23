package app

import (
	"strings"
	"sync"
	"testing"
)

type fanOutFixture struct {
	*returnTargetFixture
	parent     Run
	b, c       Run
	bConv, cCv string
}

func newFanOutFixture(t *testing.T) *fanOutFixture {
	t.Helper()
	f := &fanOutFixture{returnTargetFixture: newReturnTargetFixture(t)}
	f.parent = f.run(f.a.DMConversationID, f.a, Run{}, "", f.a.DMConversationID, "done")
	f.bConv, f.cCv = f.ab.ID, f.bc.ID
	f.b = f.run(f.bConv, f.returnTargetFixture.b, f.parent, runKindMessage, f.parent.ConversationID, "running")
	f.c = f.run(f.cCv, f.returnTargetFixture.c, f.parent, runKindMessage, f.parent.ConversationID, "running")
	return f
}

func (f *fanOutFixture) returns() []Run {
	f.t.Helper()
	rows, err := f.s.db.Query(`SELECT id FROM runs WHERE kind=? AND conversation_id=?`, runKindFollowup, f.parent.ConversationID)
	if err != nil {
		f.t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Run
	for _, id := range ids {
		r, err := f.s.GetRun(id)
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (f *fanOutFixture) succeed(r Run, text string) {
	f.t.Helper()
	if _, ok, err := f.s.FinishRun(r.ID, r.ConversationID, r.BotID, text); err != nil || !ok {
		f.t.Fatalf("finish ok=%v err=%v", ok, err)
	}
}

func (f *fanOutFixture) fail(r Run) {
	f.t.Helper()
	if ok, err := f.s.SetRunStatus(r.ID, "failed", "provider unavailable"); err != nil || !ok {
		f.t.Fatalf("fail ok=%v err=%v", ok, err)
	}
}

func (f *fanOutFixture) context(ret Run) string {
	f.t.Helper()
	conv, err := f.s.GetConversation(ret.ConversationID)
	if err != nil {
		f.t.Fatal(err)
	}
	bot, err := f.s.GetBot(ret.BotID)
	if err != nil {
		f.t.Fatal(err)
	}
	msgs, _ := (&Server{store: f.s}).buildContext(conv, ret, bot)
	var all []string
	for _, m := range msgs {
		all = append(all, m.Content)
	}
	return strings.Join(all, "\n")
}

func TestFanOutSuccessReturnsOnceWithEveryResult(t *testing.T) {
	f := newFanOutFixture(t)
	f.succeed(f.b, "RESULT-FROM-B")
	if got := f.returns(); len(got) != 0 {
		t.Fatalf("first finisher returned early: %+v", got)
	}
	f.succeed(f.c, "RESULT-FROM-C")
	got := f.returns()
	if len(got) != 1 || got[0].ParentRunID != f.c.ID {
		t.Fatalf("returns=%+v", got)
	}
	ctx := f.context(got[0])
	for _, want := range []string{"[completed colleague task]", "RESULT-FROM-B", "RESULT-FROM-C", "A result that is only a plan gets carried out before you conclude.", "Result from B", "Result from C"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx)
		}
	}
}

func TestFanOutSuccessThenFailureReturnsOnce(t *testing.T) {
	f := newFanOutFixture(t)
	f.succeed(f.b, "RESULT-FROM-B")
	f.fail(f.c)
	got := f.returns()
	if len(got) != 1 || got[0].ParentRunID != f.c.ID {
		t.Fatalf("returns=%+v", got)
	}
	ctx := f.context(got[0])
	for _, want := range []string{"[failed colleague task]", "status=failed", "RESULT-FROM-B", "Result from B"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx)
		}
	}
}

func TestFanOutFailureThenSuccessReturnsOnce(t *testing.T) {
	f := newFanOutFixture(t)
	f.fail(f.b)
	if got := f.returns(); len(got) != 0 {
		t.Fatalf("failed first finisher returned early: %+v", got)
	}
	f.succeed(f.c, "RESULT-FROM-C")
	got := f.returns()
	if len(got) != 1 || got[0].ParentRunID != f.c.ID {
		t.Fatalf("returns=%+v", got)
	}
	ctx := f.context(got[0])
	for _, want := range []string{"[completed colleague task]", "RESULT-FROM-C", "Failed task from B", "failure_code=", "A result that is only a plan gets carried out before you conclude."} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx)
		}
	}
}

func TestFanOutBothFailReturnsOnce(t *testing.T) {
	f := newFanOutFixture(t)
	f.fail(f.b)
	f.fail(f.c)
	got := f.returns()
	if len(got) != 1 {
		t.Fatalf("returns=%+v", got)
	}
	ctx := f.context(got[0])
	if strings.Count(ctx, "status=failed") != 2 {
		t.Fatalf("both failures expected:\n%s", ctx)
	}
}

func TestSingleChildReturnUnchanged(t *testing.T) {
	f := newFanOutFixture(t)
	f.exec(`UPDATE runs SET status='cancelled' WHERE id=?`, f.c.ID)
	f.succeed(f.b, "RESULT-FROM-B")
	got := f.returns()
	if len(got) != 1 {
		t.Fatalf("returns=%+v", got)
	}
	ctx := f.context(got[0])
	if strings.Contains(ctx, "assigned this work together") || strings.Count(ctx, "Result from ") != 1 {
		t.Fatalf("single child context changed:\n%s", ctx)
	}
}

func TestFanOutSchedulingIsIdempotent(t *testing.T) {
	f := newFanOutFixture(t)
	f.succeed(f.b, "RESULT-FROM-B")
	f.succeed(f.c, "RESULT-FROM-C")
	for _, child := range []Run{f.b, f.c} {
		done, err := f.s.GetRun(child.ID)
		if err != nil {
			t.Fatal(err)
		}
		m, err := f.s.AddAssistant(f.parent.ConversationID, done.BotID, done.ID, "again")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = scheduleDirectMessageFollowupTx(tx, done, m); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.returns(); len(got) != 1 {
		t.Fatalf("returns=%+v", got)
	}
}

func TestFanOutConcurrentFinishReturnsExactlyOnce(t *testing.T) {
	for i := 0; i < 20; i++ {
		f := newFanOutFixture(t)
		var wg sync.WaitGroup
		for _, r := range []Run{f.b, f.c} {
			wg.Add(1)
			go func(r Run) {
				defer wg.Done()
				if _, _, err := f.s.FinishRun(r.ID, r.ConversationID, r.BotID, "done "+r.BotID); err != nil {
					t.Error(err)
				}
			}(r)
		}
		wg.Wait()
		if got := f.returns(); len(got) != 1 {
			t.Fatalf("iteration %d returns=%+v", i, got)
		}
	}
}

func TestLaterRoundAfterEarlierReturnStillReturns(t *testing.T) {
	f := newFanOutFixture(t)
	f.exec(`UPDATE runs SET status='cancelled' WHERE id=?`, f.c.ID)
	f.succeed(f.b, "RESULT-FROM-B")
	later := f.run(f.cCv, f.returnTargetFixture.c, f.parent, runKindMessage, f.parent.ConversationID, "running")
	f.succeed(later, "RESULT-LATER")
	if got := f.returns(); len(got) != 2 {
		t.Fatalf("a task delegated after an earlier return lost its own: %+v", got)
	}
}
