package app

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type exchangeFixture struct {
	s          *Server
	group      Conversation
	a, b, c    Bot
	debateRuns []Run
}

// A finished two-member exchange (a debate between b and c) followed by a
// person greeting that the default member a answers.
func newExchangeFixture(t *testing.T, mention string, finish bool) (exchangeFixture, Run) {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := exchangeFixture{s: &Server{store: store, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}}
	f.a, _ = store.CreateBot("alpha", "", "m")
	f.b, _ = store.CreateBot("bravo", "", "m")
	f.c, _ = store.CreateBot("charlie", "", "m")
	f.group, _ = store.CreateGroup("g", []string{f.a.ID, f.b.ID, f.c.ID})
	specs := []runSpec{{BotID: f.b.ID}, {BotID: f.c.ID}}
	if mention == "a" {
		specs = append(specs, runSpec{BotID: f.a.ID})
	}
	ask := "@bravo @charlie synthetic topic: tabs against spaces? Take opposite sides."
	if mention == "text" {
		ask = "@alpha @bravo @charlie synthetic topic: tabs against spaces? Take opposite sides."
	}
	_, runs, _, err := store.AddUserRuns(f.group.ID, ask, "", specs)
	if err != nil {
		t.Fatal(err)
	}
	f.debateRuns = runs
	say := func(run Run, text string) {
		if _, _, err := store.AddMessage(f.group.ID, "assistant", run.BotID, run.ID, text, ""); err != nil {
			t.Fatal(err)
		}
	}
	say(runs[0], "Tabs win because every reader can choose the width. It also scales to any screen. Extra detail follows here.")
	say(runs[1], "Spaces win because alignment is identical everywhere. It also survives copy and paste. More detail follows here.")
	say(runs[0], "Conceded: a formatter makes the choice moot, so settle on the formatter.")
	if finish {
		for _, r := range runs {
			if _, err := store.SetRunStatus(r.ID, "done", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, greet, _, err := store.AddUserRuns(f.group.ID, "Good evening, everyone.", "", nil)
	if err != nil || len(greet) != 1 {
		t.Fatalf("greeting runs: %v %v", greet, err)
	}
	return f, greet[0]
}

func (f exchangeFixture) context(r Run) string {
	msgs, _ := f.s.buildContextPartsWith(f.group, r, f.a, nil)
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role + ": " + m.Content + "\n")
	}
	return b.String()
}

func TestFinishedExchangeTheBotMissedReachesItAsGist(t *testing.T) {
	f, greet := newExchangeFixture(t, "", true)
	if greet.BotID != f.a.ID {
		t.Fatalf("default run is for %s, want the first member", greet.BotID)
	}
	got := f.context(greet)
	if !strings.Contains(got, "[closed group exchange, gist]") {
		t.Fatalf("no gist block:\n%s", got)
	}
	for _, kept := range []string{"search_history", "tabs against spaces", "bravo: Tabs win", "charlie: Spaces win", "settle on the formatter"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("gist lost %q:\n%s", kept, got)
		}
	}
	for _, gone := range []string{"Extra detail follows", "More detail follows", "[sender bravo", "[sender charlie"} {
		if strings.Contains(got, gone) {
			t.Fatalf("verbatim turn survived (%q):\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "Good evening, everyone.") || !strings.Contains(got, "[message_id=") {
		t.Fatalf("the current message must stay verbatim with its id:\n%s", got)
	}
}

func TestExchangeStaysVerbatimWhileUnsettled(t *testing.T) {
	f, greet := newExchangeFixture(t, "", false)
	got := f.context(greet)
	if strings.Contains(got, "[closed group exchange") || !strings.Contains(got, "[sender bravo") {
		t.Fatalf("an unsettled exchange must stay verbatim:\n%s", got)
	}
}

func TestExchangeStaysVerbatimWhenTheBotWasAddressed(t *testing.T) {
	for _, mention := range []string{"a", "text"} {
		f, greet := newExchangeFixture(t, mention, true)
		got := f.context(greet)
		if strings.Contains(got, "[closed group exchange") || !strings.Contains(got, "[sender bravo") {
			t.Fatalf("mention=%s: an exchange the Bot was in must stay verbatim:\n%s", mention, got)
		}
	}
}

func TestExchangeStaysVerbatimWhenTheBotPosted(t *testing.T) {
	f, greet := newExchangeFixture(t, "", true)
	// One of the debate turns was posted by the Bot itself.
	if _, err := f.s.store.db.Exec(`UPDATE messages SET sender_bot_id=? WHERE conversation_id=? AND content LIKE 'Conceded:%'`, f.a.ID, f.group.ID); err != nil {
		t.Fatal(err)
	}
	got := f.context(greet)
	if strings.Contains(got, "[closed group exchange") {
		t.Fatalf("an exchange the Bot posted in must stay verbatim:\n%s", got)
	}
}
