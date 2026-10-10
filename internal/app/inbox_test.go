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

// Progress notes fold away once the task ends, so they must not inflate the badge.
func TestInboxUnreadSkipsProgressNotes(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("A", "", "m")
	s.AddMessage(b.DMConversationID, "user", "", "", "hello", "")
	note, _, _ := s.AddMessage(b.DMConversationID, "assistant", b.ID, "", "checking the page", "")
	answer, _, _ := s.AddMessage(b.DMConversationID, "assistant", b.ID, "", "changed it to 2:30", "")
	s.db.Exec(`UPDATE messages SET kind='progress' WHERE id=?`, note.ID)
	s.db.Exec(`UPDATE messages SET kind='segment' WHERE id=?`, answer.ID)
	convs, err := s.ListConversations()
	if err != nil || convs[0].UnreadCount != 1 {
		t.Fatalf("unread=%d err=%v", convs[0].UnreadCount, err)
	}
}
