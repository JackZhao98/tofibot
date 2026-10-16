package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Only the Bot's latest replies are replayed verbatim; older ones keep a gist,
// and its own replies carry no message id label to imitate.
func TestOwnOlderRepliesReplayAsGist(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("A", "", "m")
	c, _ := s.GetConversation(b.DMConversationID)
	for i := 1; i <= 6; i++ {
		if _, _, err := s.AddMessage(c.ID, "user", "", "", fmt.Sprintf("synthetic question %d", i), ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.AddMessage(c.ID, "assistant", b.ID, "", fmt.Sprintf("Synthetic answer number %d is here. It ends with a long routine closing offer that should not be copied.", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{store: s, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	msgs, _ := server.buildContextPartsWith(c, Run{ID: "r", ConversationID: c.ID, BotID: b.ID}, b, nil)
	var own []string
	for _, m := range msgs {
		if m.Role == "assistant" {
			own = append(own, m.Content)
		}
	}
	if len(own) != 6 {
		t.Fatalf("own replies in history = %d, want 6", len(own))
	}
	for i, content := range own {
		if strings.Contains(content, "[message_id=") {
			t.Fatalf("own reply %d carries a message id label: %q", i+1, content)
		}
		older := i < 3
		if older != strings.HasPrefix(content, "(earlier reply, gist) ") {
			t.Fatalf("reply %d: gist=%v, content %q", i+1, older, content)
		}
		if older && strings.Contains(content, "closing offer") {
			t.Fatalf("older reply kept its phrasing: %q", content)
		}
		if !older && !strings.Contains(content, "closing offer") {
			t.Fatalf("recent reply lost its text: %q", content)
		}
	}
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "synthetic question 1") && !strings.Contains(m.Content, "[message_id=") {
			t.Fatal("user messages must keep their id labels for replies")
		}
	}
}
