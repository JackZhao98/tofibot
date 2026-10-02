package app

import "testing"

func TestInboxMonotonicReadAndNewReplies(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("A", "", "m")
	s.AddMessage(b.DMConversationID, "user", "", "", "hello", "")
	reply, _, _ := s.AddMessage(b.DMConversationID, "assistant", b.ID, "", "answer", "")
	convs, err := s.ListConversations()
	if err != nil || convs[0].UnreadCount != 1 {
		t.Fatalf("unread=%v err=%v", convs, err)
	}
	seq, err := s.MarkRead(b.DMConversationID, 9999)
	if err != nil || seq != reply.Seq {
		t.Fatalf("read=%d err=%v", seq, err)
	}
	s.MarkRead(b.DMConversationID, 1)
	convs, _ = s.ListConversations()
	if convs[0].UnreadCount != 0 {
		t.Fatal("read cursor regressed")
	}
	s.AddMessage(b.DMConversationID, "assistant", b.ID, "", "new answer", "")
	convs, _ = s.ListConversations()
	if convs[0].UnreadCount != 1 {
		t.Fatal("future reply marked read too early")
	}
}
