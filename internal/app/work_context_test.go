package app

import (
	"strings"
	"testing"
)

func TestWorkContextScopedAndBounded(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("a", "", "model")
	b, _ := s.CreateBot("b", "", "model")
	c, _ := s.CreateGroup("team", []string{a.ID, b.ID})
	s.CreateWorkItem(c.ID, b.ID, WorkItemInput{Kind: "goal", Title: "Shared result"})
	s.CreateWorkItem(c.ID, a.ID, WorkItemInput{Kind: "task", Title: "My assignment"})
	s.CreateWorkItem(c.ID, b.ID, WorkItemInput{Kind: "task", Title: "Another assignment"})
	s.CreateWorkItem(a.DMConversationID, a.ID, WorkItemInput{Kind: "task", Title: "Other conversation"})
	x := s.openWorkContext(c.ID, a.ID)
	if !strings.Contains(x, "Shared result") || !strings.Contains(x, "My assignment") || strings.Contains(x, "Another assignment") || strings.Contains(x, "Other conversation") {
		t.Fatalf("invalid scope %s", x)
	}
	for i := 0; i < 30; i++ {
		s.CreateWorkItem(c.ID, a.ID, WorkItemInput{Kind: "task", Title: strings.Repeat("x", 240)})
	}
	if x = s.openWorkContext(c.ID, a.ID); len([]rune(x)) > 2800 {
		t.Fatalf("unbounded context: %d", len([]rune(x)))
	}
}
