package app

import "testing"

type groupReturnFixture struct {
	store       *Store
	group       Conversation
	a, b, c     Bot
	root, child Run
}

func newGroupReturnFixture(t *testing.T) groupReturnFixture {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateBot("A", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("B", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateBot("C", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateGroup("return", []string{a.ID, b.ID, c.ID})
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRun(g.ID, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(root.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start root: %v %v", ok, err)
	}
	_, child, err := s.AddHandoff(g.ID, a.ID, b.ID, root.ID, "B review the task")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(child.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start child: %v %v", ok, err)
	}
	return groupReturnFixture{store: s, group: g, a: a, b: b, c: c, root: root, child: child}
}

func finishGroupRun(t *testing.T, s *Store, r Run, bot Bot, content string) Message {
	t.Helper()
	m, done, err := s.FinishRun(r.ID, r.ConversationID, bot.ID, content)
	if err != nil || !done {
		t.Fatalf("finish %s: done=%v err=%v", r.ID, done, err)
	}
	return m
}

func followupFor(t *testing.T, s *Store, parent string) Run {
	t.Helper()
	r, ok, err := s.GroupFollowupForRun(parent)
	if err != nil || !ok {
		t.Fatalf("followup for %s: ok=%v err=%v", parent, ok, err)
	}
	return r
}

func TestGroupReturnNestedDelegationUnwindsToOriginalRequester(t *testing.T) {
	f := newGroupReturnFixture(t)
	defer f.store.Close()
	finishGroupRun(t, f.store, f.root, f.a, "A delegated to B")
	_, grandchild, err := f.store.AddHandoff(f.group.ID, f.b.ID, f.c.ID, f.child.ID, "C verify B's work")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.SetRunStatus(grandchild.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start grandchild: %v %v", ok, err)
	}
	// B delegated onward, so completing B must not prematurely wake A.
	finishGroupRun(t, f.store, f.child, f.b, "B sent work to C")
	if _, ok, err := f.store.GroupFollowupForRun(f.child.ID); err != nil || ok {
		t.Fatalf("premature B followup: ok=%v err=%v", ok, err)
	}
	result := finishGroupRun(t, f.store, grandchild, f.c, "C's verified result")
	if result.Content != "C's verified result" {
		t.Fatal("child result was not persisted")
	}
	bFollowup := followupFor(t, f.store, grandchild.ID)
	if bFollowup.BotID != f.b.ID || bFollowup.TriggerMessageID != result.ID || bFollowup.ParentRunID != grandchild.ID {
		t.Fatalf("bad B continuation: %+v", bFollowup)
	}
	if ok, err := f.store.SetRunStatus(bFollowup.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start B continuation: %v %v", ok, err)
	}
	finishGroupRun(t, f.store, bFollowup, f.b, "B's final report to A")
	aFollowup := followupFor(t, f.store, bFollowup.ID)
	if aFollowup.BotID != f.a.ID || aFollowup.ParentRunID != bFollowup.ID {
		t.Fatalf("nested return did not unwind to A: %+v", aFollowup)
	}
	if ok, err := f.store.SetRunStatus(aFollowup.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start A continuation: %v %v", ok, err)
	}
	finishGroupRun(t, f.store, aFollowup, f.a, "A final integrated answer")
	if _, ok, err := f.store.GroupFollowupForRun(aFollowup.ID); err != nil || ok {
		t.Fatalf("final continuation bounced again: ok=%v err=%v", ok, err)
	}
}

func TestGroupReturnContinuationCanDelegateAgain(t *testing.T) {
	f := newGroupReturnFixture(t)
	defer f.store.Close()
	finishGroupRun(t, f.store, f.root, f.a, "A delegated to B")
	finishGroupRun(t, f.store, f.child, f.b, "B result")
	aContinuation := followupFor(t, f.store, f.child.ID)
	if ok, err := f.store.SetRunStatus(aContinuation.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start A continuation: %v %v", ok, err)
	}
	_, cRun, err := f.store.AddHandoff(f.group.ID, f.a.ID, f.c.ID, aContinuation.ID, "C check the integrated answer")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.SetRunStatus(cRun.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start C continuation: %v %v", ok, err)
	}
	finishGroupRun(t, f.store, aContinuation, f.a, "A delegated the follow-up to C")
	if _, ok, err := f.store.GroupFollowupForRun(aContinuation.ID); err != nil || ok {
		t.Fatalf("A continuation returned before C: ok=%v err=%v", ok, err)
	}
	finishGroupRun(t, f.store, cRun, f.c, "C follow-up result")
	cReturn := followupFor(t, f.store, cRun.ID)
	if cReturn.BotID != f.a.ID {
		t.Fatalf("C did not return to A: %+v", cReturn)
	}
	if ok, err := f.store.SetRunStatus(cReturn.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start returned A continuation: %v %v", ok, err)
	}
	finishGroupRun(t, f.store, cReturn, f.a, "A completed the second follow-up")
	if _, ok, err := f.store.GroupFollowupForRun(cReturn.ID); err != nil || ok {
		t.Fatalf("second A continuation bounced again: ok=%v err=%v", ok, err)
	}
}

func TestGroupReturnRetryIsIdempotent(t *testing.T) {
	f := newGroupReturnFixture(t)
	defer f.store.Close()
	finishGroupRun(t, f.store, f.root, f.a, "A delegated to B")
	if ok, err := f.store.SetRunStatus(f.child.ID, "failed", "temporary failure"); err != nil || !ok {
		t.Fatalf("fail child: %v %v", ok, err)
	}
	retry, err := f.store.RetryRun(f.child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.SetRunStatus(retry.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start retry: %v %v", ok, err)
	}
	finishGroupRun(t, f.store, retry, f.b, "retry result")
	followupFor(t, f.store, retry.ID)
	if _, done, err := f.store.FinishRun(retry.ID, f.group.ID, f.b.ID, "duplicate"); err != nil || done {
		t.Fatalf("duplicate finish: done=%v err=%v", done, err)
	}
	rows, err := f.store.db.Query(`SELECT COUNT(*) FROM runs WHERE parent_run_id=? AND kind=?`, retry.ID, runKindFollowup)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing idempotence count")
	}
	var count int
	if err = rows.Scan(&count); err != nil || count != 1 {
		t.Fatalf("followup count=%d err=%v", count, err)
	}
}

func TestGroupReturnCancellationAndMembershipDoNotFakeCompletion(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		f := newGroupReturnFixture(t)
		defer f.store.Close()
		finishGroupRun(t, f.store, f.root, f.a, "A delegated to B")
		if ok, err := f.store.SetRunStatus(f.child.ID, "cancelled", "cancelled"); err != nil || !ok {
			t.Fatalf("cancel child: %v %v", ok, err)
		}
		if _, ok, err := f.store.GroupFollowupForRun(f.child.ID); err != nil || ok {
			t.Fatalf("cancelled child followup: ok=%v err=%v", ok, err)
		}
	})
	t.Run("removed requester", func(t *testing.T) {
		f := newGroupReturnFixture(t)
		defer f.store.Close()
		finishGroupRun(t, f.store, f.root, f.a, "A delegated to B")
		if _, err := f.store.db.Exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, f.group.ID, f.a.ID); err != nil {
			t.Fatal(err)
		}
		finishGroupRun(t, f.store, f.child, f.b, "late result")
		if _, ok, err := f.store.GroupFollowupForRun(f.child.ID); err != nil || ok {
			t.Fatalf("removed requester followup: ok=%v err=%v", ok, err)
		}
	})
}

func TestGroupReturnQueuedFollowupSurvivesStoreReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.CreateBot("A", "", "model")
	b, _ := s.CreateBot("B", "", "model")
	g, _ := s.CreateGroup("return", []string{a.ID, b.ID})
	root, _ := s.AddRun(g.ID, a.ID, "")
	s.SetRunStatus(root.ID, "running", "")
	_, child, err := s.AddHandoff(g.ID, a.ID, b.ID, root.ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunStatus(root.ID, "done", "")
	s.SetRunStatus(child.ID, "running", "")
	finishGroupRun(t, s, child, b, "durable result")
	queued := followupFor(t, s, child.ID)
	if queued.Status != "queued" {
		t.Fatalf("followup status=%s", queued.Status)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := followupFor(t, reopened, child.ID)
	if got.ID != queued.ID || got.Status != "queued" {
		t.Fatalf("queued followup changed after reopen: before=%+v after=%+v", queued, got)
	}
}
