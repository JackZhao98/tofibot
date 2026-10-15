package app

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// Private handoff notes reach the colleague's context and never the group.
func TestHandoffNotesReachOnlyTheColleague(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	host, _ := s.CreateBot("Host", "", "m")
	other, _ := s.CreateBot("Other", "", "m")
	g, _ := s.CreateGroup("synthetic group", []string{host.ID, other.ID})
	_, runs, _, err := s.addUserRuns(g.ID, "debate it", "", nil, nil)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	parent := runs[0]
	s.SetRunStatus(parent.ID, "running", "")
	server := &Server{store: s, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	var handoff Tool
	for _, tool := range server.tools(g, parent) {
		if tool.Name == "handoff" {
			handoff = tool
		}
	}
	if handoff.Execute == nil {
		t.Fatal("handoff tool missing")
	}
	const secret = "SYNTHETIC-PRIVATE-NOTE keep it under three sentences"
	childID, err := handoff.Execute(context.Background(), []byte(`{"bot_id":"`+other.ID+`","task":"Other, take the opposite side from mine.","notes":"`+secret+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var leaked int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND (content LIKE '%SYNTHETIC-PRIVATE-NOTE%' OR coalesce(notice_data,'') LIKE '%SYNTHETIC-PRIVATE-NOTE%')`, g.ID).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("private notes were posted in the group")
	}
	child, err := s.GetRun(childID)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := server.buildContextPartsWith(g, child, other, nil)
	found := false
	for _, m := range msgs {
		found = found || strings.Contains(m.Content, secret)
	}
	if !found {
		t.Fatal("the colleague's context lacks the private notes")
	}
}
