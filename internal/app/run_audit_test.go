package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRunAuditScopesFailuresAndFinalDelivery(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.CreateBot("one", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateBot("two", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	add := func(id, botID, convID, when string) {
		t.Helper()
		_, e := s.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,error,created_at,updated_at) VALUES(?,?,?,'done','',?,?)`, id, convID, botID, when, when)
		if e != nil {
			t.Fatal(e)
		}
	}
	add("prior", b.ID, c.ID, "2026-09-29T10:00:00Z")
	add("other-bot", other.ID, c.ID, "2026-09-29T10:00:01Z")
	add("other-conversation", b.ID, other.DMConversationID, "2026-09-29T10:00:02Z")
	add("future", b.ID, c.ID, "2026-09-29T12:00:00Z")
	current := Run{ID: "current", BotID: b.ID, ConversationID: c.ID, CreatedAt: "2026-09-29T11:00:00Z"}
	for i, status := range []string{"failed", "completed"} {
		_, err = s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at) VALUES(?,?,'prior',?,'computer_browser','PRIVATE_ARGUMENT',?,?,?,?)`, c.ID, b.ID, fmt.Sprint(i), map[string]string{"failed": "browser action is required", "completed": "PRIVATE_SUCCESS"}[status], status, fmt.Sprintf("2026-09-29T10:00:0%dZ", i), "2026-09-29T10:00:03Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.db.Exec(`INSERT INTO messages(id,conversation_id,seq,role,sender_bot_id,run_id,content,created_at) VALUES('answer',?,1,'assistant',?,'prior','answer','2026-09-29T10:00:04Z')`, c.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	audits, err := s.inspectRecentRuns(context.Background(), c, current, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].FinalAnswers != 1 || audits[0].Failures != 1 || audits[0].Calls != 2 || audits[0].Status != "done" {
		t.Fatalf("wrong audit: %+v", audits)
	}
	data, _ := json.Marshal(audits)
	if !strings.Contains(string(data), "browser action is required") || strings.Contains(string(data), "PRIVATE") {
		t.Fatalf("wrong detail projection: %s", data)
	}
	for _, id := range []string{"other-bot", "other-conversation", "future", "missing"} {
		result, e := s.inspectRecentRuns(context.Background(), c, current, id)
		if e != nil || len(result) != 0 {
			t.Fatalf("leaked %s: %+v %v", id, result, e)
		}
	}
	_, err = s.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.inspectRecentRuns(context.Background(), c, current, "prior"); err == nil {
		t.Fatal("hidden conversation allowed")
	}
}

func TestRunAuditBoundsAndRechecksMembership(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("one", "", "model")
	c, _ := s.GetConversation(b.DMConversationID)
	current := Run{ID: "current", BotID: b.ID, ConversationID: c.ID, CreatedAt: "2026-09-29T11:00:00Z"}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("r%d", i)
		_, err = s.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,error,created_at,updated_at) VALUES(?,?,?,'done','','2026-09-29T10:00:00Z','2026-09-29T10:01:00Z')`, id, c.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 45; j++ {
			_, err = s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,result,status,started_at,updated_at) VALUES(?,?,?,?,'fixture',?,'failed','2026-09-29T10:00:00Z','2026-09-29T10:01:00Z')`, c.ID, b.ID, id, fmt.Sprint(j), strings.Repeat("failure", 1000))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	audits, err := s.inspectRecentRuns(context.Background(), c, current, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 3 {
		t.Fatalf("run bound: %d", len(audits))
	}
	for _, a := range audits {
		if len(a.Tools) != 40 || !a.ToolsTruncated || a.Calls != 45 || !a.Tools[0].Truncated {
			t.Fatalf("call/detail bound: %+v", a)
		}
	}
	data, _ := json.Marshal(audits)
	if len(data) > 40000 {
		t.Fatalf("oversized audit %d", len(data))
	}
	outsider, _ := s.CreateBot("outsider", "", "model")
	current.BotID = outsider.ID
	if _, err = s.inspectRecentRuns(context.Background(), c, current, ""); err == nil {
		t.Fatal("outsider allowed")
	}
	if len((&Server{store: s}).runAuditTools(Conversation{ID: c.ID, UserVisible: false}, current)) != 0 {
		t.Fatal("tool exposed in hidden context")
	}
}
