package app

import (
	"database/sql"
	"testing"
)

type returnTargetFixture struct {
	t       *testing.T
	s       *Store
	a, b, c Bot
	ab, bc  Conversation
}

func newReturnTargetFixture(t *testing.T) *returnTargetFixture {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f := &returnTargetFixture{t: t, s: s}
	for i, target := range []*Bot{&f.a, &f.b, &f.c} {
		*target, err = s.CreateBot(string(rune('A'+i)), "", "model")
		if err != nil {
			t.Fatal(err)
		}
	}
	f.ab, err = s.CreateGroup("AB trace", []string{f.a.ID, f.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.bc, err = s.CreateGroup("BC trace", []string{f.b.ID, f.c.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE conversations SET user_visible=0 WHERE id IN (?,?)`, f.ab.ID, f.bc.ID)
	return f
}

func (f *returnTargetFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.s.db.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// Build durable graph fixtures directly, without invoking the execution or
// return-enqueue paths whose integration is tested separately.
func (f *returnTargetFixture) run(conv string, bot Bot, parent Run, kind, origin, status string) Run {
	f.t.Helper()
	r, err := f.s.AddRun(conv, bot.ID, parent.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	m, err := f.s.AddAssistant(conv, bot.ID, r.ID, "immutable assignment")
	if err != nil {
		f.t.Fatal(err)
	}
	r.Kind, r.OriginConversationID, r.TriggerMessageID, r.Status = kind, origin, m.ID, status
	f.exec(`UPDATE runs SET kind=?,origin_conversation_id=?,trigger_message_id=?,status=? WHERE id=?`, kind, origin, m.ID, status, r.ID)
	return r
}

func (f *returnTargetFixture) followup(child, caller Run) Run {
	f.t.Helper()
	bot, err := f.s.GetBot(caller.BotID)
	if err != nil {
		f.t.Fatal(err)
	}
	r := f.run(caller.ConversationID, bot, child, runKindFollowup, caller.ConversationID, "done")
	m, err := f.s.AddAssistant(caller.ConversationID, child.BotID, child.ID, "completed child result")
	if err != nil {
		f.t.Fatal(err)
	}
	r.TriggerMessageID = m.ID
	f.exec(`UPDATE runs SET trigger_message_id=? WHERE id=?`, m.ID, r.ID)
	return r
}

func (f *returnTargetFixture) expect(r Run, want *Run, groupOnly bool) {
	f.t.Helper()
	tx, err := f.s.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	resolve := runReturnTargetTx
	if groupOnly {
		resolve = groupReturnTargetTx
	}
	got, ok, err := resolve(tx, r)
	if err != nil {
		f.t.Fatal(err)
	}
	if want == nil {
		if ok {
			f.t.Fatalf("unexpected requester: %+v", got)
		}
		return
	}
	if !ok || got.ID != want.ID || got.BotID != want.BotID || got.ConversationID != want.ConversationID {
		f.t.Fatalf("requester=%+v ok=%v; want run=%s bot=%s conversation=%s", got, ok, want.ID, want.BotID, want.ConversationID)
	}
}

func TestRunReturnTargetCrossConversationRetry(t *testing.T) {
	for _, kind := range []string{runKindMessage, "", runKindTeam} {
		t.Run("kind="+kind, func(t *testing.T) {
			f := newReturnTargetFixture(t)
			a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
			conv := f.ab.ID
			if kind == "" {
				conv = f.b.DMConversationID
			}
			b := f.run(conv, f.b, a, kind, a.ConversationID, "failed")
			retry, err := f.s.RetryRun(b.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.expect(retry, &a, false)
			f.expect(retry, nil, true)
			f.exec(`UPDATE runs SET status='interrupted' WHERE id=?`, retry.ID)
			retryAgain, err := f.s.RetryRun(retry.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.expect(retryAgain, &a, false)
		})
	}
}

func TestRunReturnTargetNestedCrossConversationReturns(t *testing.T) {
	f := newReturnTargetFixture(t)
	a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
	b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, runWaiting)
	c := f.run(f.bc.ID, f.c, b, runKindMessage, b.ConversationID, "done")
	f.expect(c, &b, false)
	bReturn := f.followup(c, b)
	// Model the atomic waiting settlement performed when C's return is queued.
	f.exec(`UPDATE runs SET status='done' WHERE id=?`, b.ID)
	f.expect(bReturn, &a, false)
	f.expect(bReturn, nil, true)
	f.exec(`UPDATE runs SET status='failed' WHERE id=?`, bReturn.ID)
	bRetry, err := f.s.RetryRun(bReturn.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.expect(bRetry, &a, false)
	f.exec(`UPDATE runs SET status='done' WHERE id=?`, bRetry.ID)
	aReturn := f.followup(bRetry, a)
	f.exec(`UPDATE runs SET status='done' WHERE id=?`, a.ID)
	f.expect(aReturn, nil, false)
	f.expect(aReturn, nil, true)
}

func TestRunReturnTargetNestedTeamOriginReturnsThroughRequesters(t *testing.T) {
	f := newReturnTargetFixture(t)
	f.exec(`UPDATE conversations SET user_visible=1 WHERE id=?`, f.ab.ID)
	second, err := f.s.CreateGroup("second team", []string{f.a.ID, f.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	root := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
	chain := []Run{root}
	for i := 0; i < maxTeamDispatches; i++ {
		conv, bot := f.ab.ID, f.b
		if i%2 != 0 {
			conv, bot = second.ID, f.a
		}
		child := f.run(conv, bot, chain[len(chain)-1], runKindTeam, root.ConversationID, runWaiting)
		chain = append(chain, child)
	}
	leaf := chain[len(chain)-1]
	f.exec(`UPDATE runs SET status='failed' WHERE id=?`, leaf.ID)
	retry, err := f.s.RetryRun(leaf.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.expect(retry, &chain[len(chain)-2], false)
	f.expect(retry, nil, true)
	f.exec(`UPDATE runs SET status='done' WHERE id=?`, retry.ID)
	completed := retry
	for i := len(chain) - 2; i >= 0; i-- {
		// The exact immediate requester's run is settled, even when the same
		// Bot appeared earlier in another group or owns the root DM.
		caller := chain[i]
		f.expect(completed, &caller, false)
		returned := f.followup(completed, caller)
		f.exec(`UPDATE runs SET status='done' WHERE id=?`, caller.ID)
		if i > 0 {
			f.expect(returned, &chain[i-1], false)
			currentRoot, err := f.s.GetRun(root.ID)
			if err != nil || currentRoot.Status != runWaiting {
				t.Fatalf("root settled before its own return: %+v err=%v", currentRoot, err)
			}
		} else {
			f.expect(returned, nil, false)
		}
		completed = returned
	}
	for _, original := range chain {
		stored, err := f.s.GetRun(original.ID)
		if err != nil || stored.OriginConversationID != root.ConversationID {
			t.Fatalf("inherited origin changed: %+v err=%v", stored, err)
		}
	}
}

func TestRunReturnTargetTeamOriginBoundary(t *testing.T) {
	for _, scenario := range []string{"inherited", "empty parent origin", "mismatched", "empty", "replaced with caller", "message cannot inherit", "forward cannot inherit", "cancelled", "failed", "interrupted", "running"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReturnTargetFixture(t)
			a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
			b := f.run(f.ab.ID, f.b, a, runKindTeam, a.ConversationID, runWaiting)
			c := f.run(f.bc.ID, f.c, b, runKindTeam, a.ConversationID, "done")
			var want *Run
			switch scenario {
			case "inherited":
				want = &b
			case "empty parent origin":
				f.exec(`UPDATE runs SET origin_conversation_id='' WHERE id=?`, b.ID)
				c.OriginConversationID = b.ConversationID
				want = &b
			case "mismatched":
				c.OriginConversationID = f.c.DMConversationID
			case "empty":
				c.OriginConversationID = ""
			case "replaced with caller":
				c.OriginConversationID = b.ConversationID
			case "message cannot inherit":
				c.Kind = runKindMessage
			case "forward cannot inherit":
				c.Kind = ""
			case "cancelled", "failed", "interrupted", "running":
				f.exec(`UPDATE runs SET status=? WHERE id=?`, scenario, b.ID)
				if scenario == "running" {
					want = &b
				}
			}
			f.expect(c, want, false)
		})
	}
}

func TestRunReturnTargetRequesterStatus(t *testing.T) {
	for _, sameConversation := range []bool{false, true} {
		for _, status := range []string{"queued", "running", runWaiting, "done", "cancelled", "failed", "interrupted"} {
			name := "cross/" + status
			if sameConversation {
				name = "group/" + status
			}
			t.Run(name, func(t *testing.T) {
				f := newReturnTargetFixture(t)
				conv, kind := f.a.DMConversationID, runKindMessage
				if sameConversation {
					conv, kind = f.ab.ID, runKindGroupTask
				}
				a := f.run(conv, f.a, Run{}, runKindSchedule, conv, status)
				b := f.run(f.ab.ID, f.b, a, kind, conv, "done")
				var want *Run
				if status == "done" || status == runWaiting || (!sameConversation && status == "running") {
					want = &a
				}
				f.expect(b, want, sameConversation)
			})
		}
	}
}

func TestRunReturnTargetRejectsMalformedRetryIdentity(t *testing.T) {
	for _, field := range []string{"conversation", "bot", "kind", "empty trigger", "trigger", "origin"} {
		t.Run(field, func(t *testing.T) {
			f := newReturnTargetFixture(t)
			a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
			b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "failed")
			retry, err := f.s.RetryRun(b.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "conversation":
				retry.ConversationID = f.bc.ID
			case "bot":
				retry.BotID = f.c.ID
			case "kind":
				retry.Kind = ""
			case "empty trigger":
				retry.TriggerMessageID = ""
				f.exec(`UPDATE runs SET trigger_message_id='' WHERE id=?`, b.ID)
			case "trigger":
				retry.TriggerMessageID = a.TriggerMessageID
			case "origin":
				retry.OriginConversationID = f.bc.ID
			}
			f.expect(retry, nil, false)
		})
	}
}

func TestRunReturnTargetRejectsMalformedReturn(t *testing.T) {
	for _, mutation := range []string{"trigger run", "trigger conversation", "trigger sender", "trigger kind", "missing trigger", "unfinished predecessor", "wrong bot", "wrong conversation", "cancelled requester"} {
		t.Run(mutation, func(t *testing.T) {
			f := newReturnTargetFixture(t)
			a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
			b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "done")
			c := f.run(f.bc.ID, f.c, b, runKindMessage, b.ConversationID, "done")
			r := f.followup(c, b)
			switch mutation {
			case "trigger run":
				f.exec(`UPDATE messages SET run_id=? WHERE id=?`, b.ID, r.TriggerMessageID)
			case "trigger conversation":
				f.exec(`UPDATE messages SET conversation_id=?,seq=1000 WHERE id=?`, f.bc.ID, r.TriggerMessageID)
			case "trigger sender":
				f.exec(`UPDATE messages SET sender_bot_id=? WHERE id=?`, f.a.ID, r.TriggerMessageID)
			case "trigger kind":
				f.exec(`UPDATE messages SET kind='notice' WHERE id=?`, r.TriggerMessageID)
			case "missing trigger":
				r.TriggerMessageID = "missing"
			case "unfinished predecessor":
				f.exec(`UPDATE runs SET status=? WHERE id=?`, runWaiting, c.ID)
			case "wrong bot":
				r.BotID = f.a.ID
			case "wrong conversation":
				r.ConversationID = f.a.DMConversationID
			case "cancelled requester":
				f.exec(`UPDATE runs SET status='cancelled' WHERE id=?`, b.ID)
			}
			f.expect(r, nil, false)
		})
	}
}

func TestRunReturnTargetRejectsBrokenAssignment(t *testing.T) {
	for _, mutation := range []string{"origin", "missing parent", "same bot", "unsupported kind"} {
		t.Run(mutation, func(t *testing.T) {
			f := newReturnTargetFixture(t)
			a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
			b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "done")
			switch mutation {
			case "origin":
				b.OriginConversationID = f.bc.ID
			case "missing parent":
				b.ParentRunID = "missing"
			case "same bot":
				b.BotID = a.BotID
			case "unsupported kind":
				b.Kind = runKindGroupChat
			}
			f.expect(b, nil, false)
		})
	}
}

func TestRunReturnTargetBoundAndCycle(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		name := "64 loads"
		if cycle {
			name = "retry cycle"
		}
		t.Run(name, func(t *testing.T) {
			f := newReturnTargetFixture(t)
			a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
			b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "failed")
			last := b
			for i := 0; i < 62; i++ {
				retry, err := f.s.RetryRun(last.ID)
				if err != nil {
					t.Fatal(err)
				}
				f.exec(`UPDATE runs SET status='failed' WHERE id=?`, retry.ID)
				last = retry
			}
			if cycle {
				f.exec(`UPDATE runs SET parent_run_id=? WHERE id=?`, last.ID, b.ID)
				f.expect(last, nil, false)
				return
			}
			f.expect(last, &a, false)
			retry, err := f.s.RetryRun(last.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.expect(retry, nil, false)
		})
	}
}

func TestRunReturnTargetPropagatesStorageError(t *testing.T) {
	f := newReturnTargetFixture(t)
	a := f.run(f.a.DMConversationID, f.a, Run{}, runKindSchedule, f.a.DMConversationID, runWaiting)
	b := f.run(f.ab.ID, f.b, a, runKindMessage, a.ConversationID, "done")
	tx, err := f.s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := runReturnTargetTx(tx, b); ok || err != sql.ErrTxDone {
		t.Fatalf("ok=%v error=%v; want ErrTxDone", ok, err)
	}
}
