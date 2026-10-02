package app

import (
	"errors"
	"fmt"
	"testing"
)

func TestDefaultGroupAssignmentRandomActiveStarterAndIdempotency(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("alpha", "", "alpha-model")
	b, _ := s.CreateBot("bravo", "", "bravo-model")
	c, _ := s.CreateBot("charlie", "", "charlie-model")
	group, _ := s.CreateGroup("routing", []string{a.ID, b.ID, c.ID})
	if _, err = s.SetBotArchived(c.ID, true); err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for i := 0; i < 40; i++ {
		client := fmt.Sprintf("input-%d", i)
		_, runs, duplicate, err := s.AddUserRuns(group.ID, "unaddressed message", client, nil)
		if err != nil || duplicate || len(runs) != 1 {
			t.Fatalf("starter=%+v duplicate=%v err=%v", runs, duplicate, err)
		}
		r := runs[0]
		if r.Kind != runKindGroupChat || (r.BotID != a.ID && r.BotID != b.ID) || r.Model == "" {
			t.Fatalf("invalid starter: %+v", r)
		}
		selected[r.BotID] = true
		_, again, duplicate, err := s.AddUserRuns(group.ID, "ignored retry", client, nil)
		if err != nil || !duplicate || len(again) != 1 || again[0].ID != r.ID {
			t.Fatalf("retry=%+v %v", again, err)
		}
		if err = s.CancelRunTree(r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(selected) != 2 {
		t.Fatal("40 independent inputs never varied their starter")
	}
}

func TestDefaultGroupAssignmentRejectsWhenAllMembersArchived(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("alpha", "", "model")
	b, _ := s.CreateBot("bravo", "", "model")
	group, _ := s.CreateGroup("routing", []string{a.ID, b.ID})
	if _, err = s.SetBotArchived(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetBotArchived(b.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.AddUserRuns(group.ID, "no one", "empty", nil); !errors.Is(err, ErrNoActiveMembers) {
		t.Fatalf("err=%v", err)
	}
}
