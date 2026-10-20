package app

import (
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
