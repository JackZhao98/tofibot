package app

import (
	"strings"
	"testing"
)

type scopeFixture struct {
	s       *Store
	g       Conversation
	a, b, c Bot
}

func newScopeFixture(t *testing.T) scopeFixture {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a, _ := s.CreateBot("alpha", "", "m")
	b, _ := s.CreateBot("bravo", "", "m")
	c, _ := s.CreateBot("charlie", "", "m")
	g, err := s.CreateGroup("scope", []string{a.ID, b.ID, c.ID})
	if err != nil {
		t.Fatal(err)
	}
	return scopeFixture{s, g, a, b, c}
}

func (f scopeFixture) post(t *testing.T, text string, bots ...Bot) []Run {
	t.Helper()
	specs := make([]runSpec, 0, len(bots))
	for _, b := range bots {
		specs = append(specs, runSpec{BotID: b.ID, Model: b.Model})
	}
	_, runs, _, err := f.s.AddUserRuns(f.g.ID, text, "client-"+text, specs, true)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func (f scopeFixture) status(t *testing.T, id string) string {
	t.Helper()
	r, err := f.s.GetRun(id)
	if err != nil {
		t.Fatal(err)
	}
	return r.Status
}

// A person message for another member must neither steer a running Bot nor
// cancel the queued co-addressed work of its round.
func TestGroupMessageToAnotherBotLeavesRunningRoundAlone(t *testing.T) {
	f := newScopeFixture(t)
	round := f.post(t, "@alpha do X @bravo do Y", f.a, f.b)
	if changed, err := f.s.SetRunStatus(round[0].ID, "running", ""); err != nil || !changed {
		t.Fatalf("start: %v %v", changed, err)
	}
	later := f.post(t, "@charlie unrelated question", f.c)
	if got := f.status(t, round[1].ID); got != "queued" {
		t.Fatalf("co-addressed queued run was %s, want queued", got)
	}
	if got := f.status(t, round[0].ID); got != "running" {
		t.Fatalf("running run was %s", got)
	}
	if steered, err := f.s.HasSteeringSuccessor(round[0].ID); err != nil || steered {
		t.Fatalf("running run steered by another member's message: %v %v", steered, err)
	}
	var edges int
	f.s.db.QueryRow(`SELECT COUNT(*) FROM run_steering WHERE new_run_id=?`, later[0].ID).Scan(&edges)
	if edges != 0 {
		t.Fatalf("new run linked to %d unrelated runs", edges)
	}
}

// A message that addresses the running Bot still steers it as before.
func TestGroupMessageToTheRunningBotStillSteersIt(t *testing.T) {
	f := newScopeFixture(t)
	round := f.post(t, "@alpha work", f.a)
	f.s.SetRunStatus(round[0].ID, "running", "")
	f.post(t, "@alpha change course", f.a)
	if steered, err := f.s.HasSteeringSuccessor(round[0].ID); err != nil || !steered {
		t.Fatalf("addressed running Bot was not steered: %v %v", steered, err)
	}
}

// Tool results of one Bot's interrupted run are never carried to another Bot.
func TestSteeredCarryoverNeverCrossesBots(t *testing.T) {
	f := newScopeFixture(t)
	srv := &Server{store: f.s}
	old := f.post(t, "@alpha research", f.a)[0]
	succ := f.post(t, "@bravo answer", f.b)[0]
	if _, err := f.s.db.Exec(`UPDATE runs SET status='cancelled',error='steered by newer user message' WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO run_steering(new_run_id,old_run_id) VALUES(?,?)`, succ.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO tool_activities(run_id,conversation_id,bot_id,call_id,name,arguments,result,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, old.ID, f.g.ID, f.a.ID, "call1", "web_search", "{}", "private result", "completed", now(), now()); err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if got := srv.steeredToolCarryover(f.g.ID, succ.ID); got != "" {
		t.Fatalf("another Bot's tool results were carried over: %q", got)
	}
}

// Stop in a group takes the whole lineage, sibling wakes included, and a
// pending wait of that lineage can no longer wake anyone.
func TestGroupStopCancelsSiblingWakesAndExpiresPendingWaits(t *testing.T) {
	f := newScopeFixture(t)
	root := f.post(t, "@alpha gather and use", f.a)[0]
	f.s.SetRunStatus(root.ID, "running", "")
	m, err := f.s.GetMessage(root.TriggerMessageID)
	if err != nil {
		t.Fatal(err)
	}
	w1, ok, err := f.s.AddMentionWake(f.g.ID, root, m.ID+"-1", f.b.ID, "gather the data")
	if err != nil || !ok {
		t.Fatalf("wake b: %v %v", ok, err)
	}
	w2, ok, err := f.s.AddMentionWake(f.g.ID, root, m.ID+"-2", f.c.ID, "use the data")
	if err != nil || !ok {
		t.Fatalf("wake c: %v %v", ok, err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,status,deadline_at,created_at) VALUES(?,?,?,?,?,'pending',?,?)`, w2.ID, f.g.ID, f.c.ID, f.b.ID, lineageKey(f.s.db, w2.ID), "2999-01-01T00:00:00Z", now()); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CancelRunTree(w1.ID); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"stopped wake": w1.ID, "sibling wake": w2.ID, "root": root.ID} {
		if got := f.status(t, id); got != "cancelled" {
			t.Errorf("%s is %s, want cancelled", name, got)
		}
	}
	var status string
	f.s.db.QueryRow(`SELECT status FROM run_waits WHERE run_id=?`, w2.ID).Scan(&status)
	if status != "expired" {
		t.Fatalf("pending wait is %s, want expired", status)
	}
}

// A timed-out wait is anchored on the group's last Bot message, never on the
// person's newest message addressed to someone else.
func TestWaitTimeoutAnchorsOnGroupMessageNotPersonMessage(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("alpha", "", "m")
	b, _ := s.store.CreateBot("bravo", "", "m")
	g, _ := s.store.CreateGroup("anchor", []string{a.ID, b.ID})
	_, runs, _, _ := s.store.AddUserRuns(g.ID, "@alpha ask bravo", "c1", []runSpec{{BotID: a.ID, Model: a.Model}}, true)
	s.store.SetRunStatus(runs[0].ID, "running", "")
	botMsg, _, err := s.store.FinishRun(runs[0].ID, g.ID, a.ID, "I asked bravo for the figures.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`INSERT INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,status,deadline_at,created_at) VALUES(?,?,?,?,?,'pending',?,?)`, runs[0].ID, g.ID, a.ID, b.ID, lineageKey(s.store.db, runs[0].ID), "2999-01-01T00:00:00Z", now()); err != nil {
		t.Fatal(err)
	}
	person, _, _, err := s.store.AddUserRuns(g.ID, "@bravo something else entirely", "c2", []runSpec{{BotID: b.ID, Model: b.Model}}, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = person
	s.fireWait(runs[0].ID, "", waitTimeoutRequest, "expired")
	all, _ := s.store.Runs(g.ID)
	var wake *Run
	for i := range all {
		if all[i].Kind == runKindGroupMention {
			wake = &all[i]
		}
	}
	if wake == nil {
		t.Fatal("timeout woke nobody")
	}
	if wake.TriggerMessageID != botMsg.ID {
		t.Fatalf("wake anchored on %s, want the Bot message %s", wake.TriggerMessageID, botMsg.ID)
	}
}

// A Bot woken in a fan-out sees what its sibling wake already posted.
func TestWokenSiblingSeesWhatTheOtherWakePosted(t *testing.T) {
	f := newDedupeFixture(t, nil)
	f.post(t, "@tofi coordinate", f.tofi)
	root := f.roots[0]
	f.server.store.SetRunStatus(root.ID, "running", "")
	handover, err := f.server.store.AddAssistant(f.group.ID, f.tofi.ID, root.ID, "Plan posted for the helpers.")
	if err != nil {
		t.Fatal(err)
	}
	gather, ok, err := f.server.store.AddMentionWake(f.group.ID, root, handover.ID, f.meta.ID, "gather the figures")
	if err != nil || !ok {
		t.Fatalf("wake gather: %v %v", ok, err)
	}
	handover2, err := f.server.store.AddAssistant(f.group.ID, f.tofi.ID, root.ID, "Second helper, use what the first posts.")
	if err != nil {
		t.Fatal(err)
	}
	use, ok, err := f.server.store.AddMentionWake(f.group.ID, root, handover2.ID, f.third.ID, "use the figures")
	if err != nil || !ok {
		t.Fatalf("wake use: %v %v", ok, err)
	}
	f.server.store.SetRunStatus(gather.ID, "running", "")
	if _, err := f.server.store.AddAssistant(f.group.ID, f.meta.ID, gather.ID, "FIGURES-READY revenue 42"); err != nil {
		t.Fatal(err)
	}
	use, _ = f.server.store.GetRun(use.ID)
	msgs, _ := f.server.buildContext(f.group, use, f.third)
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "FIGURES-READY") {
			found = true
		}
	}
	if !found {
		t.Fatal("the woken sibling cannot see the data its colleague posted")
	}
}
