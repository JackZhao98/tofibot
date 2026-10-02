package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func findHandoffTool(t *testing.T, s *Server, c Conversation, r Run) Tool {
	t.Helper()
	for _, tool := range s.tools(c, r) {
		if tool.Name == "handoff" {
			return tool
		}
	}
	t.Fatal("handoff tool missing")
	return Tool{}
}

func TestHandoffRejectsSelfLoopAndEighthRound(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.CreateBot("alpha", "", "alpha-model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("bravo", "", "bravo-model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateGroup("handoff limits", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: s, queues: map[string]*conversationQueue{}, runs: map[string]context.CancelFunc{}, convMu: map[string]*sync.Mutex{}}

	root, err := s.AddRun(group.ID, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	self := findHandoffTool(t, server, group, root)
	selfArgs, _ := json.Marshal(map[string]string{"bot_id": a.ID, "task": "loop"})
	if _, err = self.Execute(context.Background(), selfArgs); err == nil || !strings.Contains(err.Error(), "current bot") {
		t.Fatalf("self handoff err=%v", err)
	}
	var handoffs int
	if err = s.db.QueryRow(`SELECT handoff_count FROM runs WHERE id=?`, root.ID).Scan(&handoffs); err != nil || handoffs != 0 {
		t.Fatalf("self handoff changed parent handoffs=%d err=%v", handoffs, err)
	}

	current := root
	for round := 1; round < maxHandoffRounds; round++ {
		target := b.ID
		if current.BotID == b.ID {
			target = a.ID
		}
		_, child, handoffErr := s.AddHandoff(group.ID, current.BotID, target, current.ID, "continue the bounded task")
		if handoffErr != nil {
			t.Fatalf("handoff round %d: %v", round, handoffErr)
		}
		if _, handoffErr = s.SetRunStatus(child.ID, "running", ""); handoffErr != nil {
			t.Fatalf("activate round %d: %v", round, handoffErr)
		}
		current = child
	}
	if got := handoffDepth(s, current.ID); got != maxHandoffRounds {
		t.Fatalf("depth=%d want %d", got, maxHandoffRounds)
	}
	lastTarget := a.ID
	if current.BotID == a.ID {
		lastTarget = b.ID
	}
	tool := findHandoffTool(t, server, group, current)
	args, _ := json.Marshal(map[string]string{"bot_id": lastTarget, "task": "should be rejected"})
	if _, err = tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "handoff budget exceeded") {
		t.Fatalf("eighth handoff err=%v", err)
	}
	if err = s.db.QueryRow(`SELECT handoff_count FROM runs WHERE id=?`, current.ID).Scan(&handoffs); err != nil || handoffs != 0 {
		t.Fatalf("eighth handoff changed parent handoffs=%d err=%v", handoffs, err)
	}
}
