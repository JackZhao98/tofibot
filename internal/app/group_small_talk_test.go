package app

import (
	"testing"
)

// A group message that names no one goes to the Primary Assistant when it is
// a member, never to a random colleague.
func TestUnaddressedGroupMessageGoesToThePrimaryAssistant(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	primary, _ := s.CreateBot("Host", "", "m")
	ledger, _ := s.CreateBot("Ledger", "", "m")
	news, _ := s.CreateBot("News", "", "m")
	g, err := s.CreateGroup("synthetic group", []string{ledger.ID, news.ID, primary.ID})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := s.PrimaryBotID(); err != nil || id != primary.ID {
		t.Fatalf("primary=%q err=%v", id, err)
	}
	for i, text := range []string{"what is everyone eating tonight?", "weekend plans?", "good night all"} {
		_, runs, _, err := s.addUserRuns(g.ID, text, "", nil, nil)
		if err != nil || len(runs) != 1 {
			t.Fatalf("message %d runs=%v err=%v", i, runs, err)
		}
		if runs[0].BotID != primary.ID {
			t.Fatalf("message %d went to %s, want the Primary Assistant", i, runs[0].BotID)
		}
		s.SetRunStatus(runs[0].ID, "done", "")
	}
}

// Without the Primary Assistant in the group, any active member may answer.
func TestUnaddressedGroupMessageWithoutPrimaryPicksAMember(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.CreateBot("Host", "", "m")
	a, _ := s.CreateBot("A", "", "m")
	b, _ := s.CreateBot("B", "", "m")
	g, _ := s.CreateGroup("synthetic group", []string{a.ID, b.ID})
	_, runs, _, err := s.addUserRuns(g.ID, "hello", "", nil, nil)
	if err != nil || len(runs) != 1 || (runs[0].BotID != a.ID && runs[0].BotID != b.ID) {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
}

// After a handoff, a short final line that only narrates the delegation folds
// like a note; a substantive final answer stays visible.
func TestDelegationNarrationFoldsAfterHandoff(t *testing.T) {
	for _, tc := range []struct {
		name, final, wantKind string
	}{
		{"narration folds", "Passed the rebuttal to News; I will pick it up when they reply.", "progress"},
		{"substance stays", "I side with carmaker: deliveries and factory output still drive every quarter's results, while the technology story mostly lives in the valuation, which rests on autonomy and robots that do not yet earn steady revenue.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			host, _ := s.CreateBot("Host", "", "m")
			news, _ := s.CreateBot("News", "", "m")
			g, _ := s.CreateGroup("synthetic group", []string{host.ID, news.ID})
			_, runs, _, err := s.addUserRuns(g.ID, "debate this", "", nil, nil)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs=%v err=%v", runs, err)
			}
			r := runs[0]
			if _, err := s.SetRunStatus(r.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.AddHandoff(g.ID, host.ID, news.ID, r.ID, "argue the other side"); err != nil {
				t.Fatal(err)
			}
			m, done, err := s.FinishRun(r.ID, g.ID, host.ID, tc.final)
			if err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			var kind string
			if err := s.db.QueryRow(`SELECT kind FROM messages WHERE id=?`, m.ID).Scan(&kind); err != nil {
				t.Fatal(err)
			}
			if kind != tc.wantKind {
				t.Fatalf("kind=%q want %q", kind, tc.wantKind)
			}
		})
	}
}
