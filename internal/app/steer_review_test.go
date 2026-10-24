package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Review item 1: a person's message read by a run (continuation, start) must
// reach the model only as the person's turn, never inside [task updates].
func TestAPersonsMessageNeverRendersAsATaskUpdate(t *testing.T) {
	s, c, old := steerFixture(t, false)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	sendWhileWorking(t, s, c, old, "Secret person words.", "p1")
	succ, created, err := s.store.closeRunForContinuation(old.ID)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	if rows := s.store.peekInboxForRun(old.BotID, c.ID, succ.ID); len(rows) != 0 {
		t.Fatalf("person row listed as an inbox event: %+v", rows)
	}
	if block := s.store.taskUpdatesContext(old.BotID, c.ID, succ.ID); strings.Contains(block, "Secret person words") {
		t.Fatalf("person message in task updates: %q", block)
	}
}

// Review item 2: every read counts toward the cap, and past it nothing is read,
// so a stream of task events cannot keep a run going.
func TestEventReadsCountTowardTheCapAndPastItNothingIsRead(t *testing.T) {
	s, c, old := steerFixture(t, false)
	addEvent := func(i int) {
		tx, err := s.store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := addInboxTx(tx, InboxItem{BotID: old.BotID, ConversationID: c.ID, Kind: inboxTaskUpdate, Body: "update " + string(rune('a'+i)), Wakes: true}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var refills, reads int
	var afterCap string
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		for i := 0; i < maxInjectionsPerRun; i++ {
			addEvent(i)
			text, refill, err := req.DrainInbox(1000)
			if err != nil || text == "" {
				t.Fatalf("read %d: %q %v", i, text, err)
			}
			reads++
			if refill {
				refills++
			}
		}
		addEvent(9)
		afterCap, _, _ = req.DrainInbox(1000)
		return runtime.Result{Content: "done"}, nil
	})
	s.execute(c, old)
	if reads != maxInjectionsPerRun || refills != 0 || afterCap != "" {
		t.Fatalf("reads=%d refills=%d afterCap=%q", reads, refills, afterCap)
	}
}

// Review item 3: near the assistant-turn limit the run reads nothing and a
// waiting person's message closes it through a continuation run.
func TestNearTheTurnLimitAPersonsMessageClosesTheRun(t *testing.T) {
	s, c, old := steerFixture(t, false)
	var err error
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		sendWhileWorking(t, s, c, old, "One more thing.", "late-turn")
		_, _, err = req.DrainInbox(minTurnsForReading - 1)
		return runtime.Result{}, err
	})
	s.execute(c, old)
	if err == nil {
		t.Fatal("the run kept reading at the turn limit")
	}
	if got, _ := s.store.GetRun(old.ID); got.Status != "cancelled" {
		t.Fatalf("run %+v", got)
	}
	if runs, _ := s.store.Runs(c.ID); len(runs) != 2 {
		t.Fatalf("runs=%d", len(runs))
	}
}

// Review item 4: a message newer than the history cutoff of the run that takes
// over is not in that run's context, so starting must not mark it read.
func TestStartingARunLeavesAMessageNewerThanItsHistoryUnread(t *testing.T) {
	s, c, old := steerFixture(t, false)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// A message reaches the working run; then a delegation makes a successor whose
	// history ends at an older message, and the run yields to it.
	m2, runs, _ := sendWhileWorking(t, s, c, old, "A change newer than the successor's history.", "m2")
	if len(runs) != 0 {
		t.Fatal("expected queued")
	}
	succ := queueRun(t, s.store, c.ID, old.BotID, "", "")
	if _, err := s.store.db.Exec(`UPDATE runs SET trigger_message_id=? WHERE id=?`, old.TriggerMessageID, succ.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`INSERT INTO run_steering(new_run_id,old_run_id) VALUES(?,?)`, succ.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.MarkRunSteered(old.ID, "steered by newer user message"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), succ.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var left string
	if err := s.store.db.QueryRow(`SELECT COALESCE(anchor,'') FROM bot_inbox WHERE kind='user_message' AND consumed_at=''`).Scan(&left); err != nil || left != m2.ID {
		t.Fatalf("the newer message was marked read at start: unread anchor=%q %v", left, err)
	}
	rows, err := s.store.drainInboxForRun(succ, maxInjectionsPerRun)
	if err != nil || len(rows) != 1 || rows[0].Anchor != m2.ID {
		t.Fatalf("first boundary: %+v %v", rows, err)
	}
}

// Review item 6: "@A @B" while A works. A reads the message in place and
// answers it, so B waits for A's note the way a co-addressed member always did.
func TestACoAddressedMemberWaitsForAWorkingMembersUnreadMessage(t *testing.T) {
	s, c, old := steerFixture(t, true)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var peer Bot
	members, _ := s.memberBots(c)
	for _, b := range members {
		if b.ID != old.BotID {
			peer = b
		}
	}
	m, runs, _, _, err := s.store.addUserRunsSteering(c.ID, "@a @b one answer please", "co", []runSpec{{BotID: old.BotID, Model: old.Model}, {BotID: peer.ID, Model: peer.Model}}, nil)
	if err != nil || len(runs) != 1 || runs[0].BotID != peer.ID {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if !s.store.runOrderBlocked(runs[0]) {
		t.Fatal("B would start before A has read the message A is answering")
	}
	if rows, err := s.store.drainInboxForRun(old, maxInjectionsPerRun); err != nil || len(rows) != 1 || rows[0].Anchor != m.ID {
		t.Fatalf("drain %+v %v", rows, err)
	}
	if s.store.runOrderBlocked(runs[0]) {
		t.Fatal("B is still blocked after A read the message")
	}
}

// Review item 7: a person's message that has waited past the bound is not held
// back for good by a busy task tree.
func TestAnAgedPersonMessageRanksWithTheTaskTree(t *testing.T) {
	p := newLaneProbe()
	s := laneServer(t, p)
	a, _ := s.store.CreateBot("alpha", "", "model")
	dm, _ := s.store.GetConversation(a.DMConversationID)
	root := queueRun(t, s.store, dm.ID, a.ID, "", "")
	if _, err := s.store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, root.ID); err != nil {
		t.Fatal(err)
	}
	s.lanes.mu.Lock()
	s.lanes.init()
	s.lanes.lastTree[a.ID] = lineageKey(s.store.db, root.ID)
	s.lanes.mu.Unlock()
	tree := queueRun(t, s.store, dm.ID, a.ID, runKindFollowup, root.ID)
	person := queueRun(t, s.store, dm.ID, a.ID, "", "")
	if !s.laneRankOf(tree).before(s.laneRankOf(person)) {
		t.Fatal("a fresh person's message should rank behind the tree's own run")
	}
	old := time.Now().Add(-laneStarveAfter - time.Minute).UTC().Format(storedTimeLayout)
	if _, err := s.store.db.Exec(`UPDATE runs SET created_at=? WHERE id=?`, old, person.ID); err != nil {
		t.Fatal(err)
	}
	person.CreatedAt = old
	if !s.laneRankOf(person).before(s.laneRankOf(tree)) {
		t.Fatal("an aged person's message is still held behind the busy tree")
	}
}
