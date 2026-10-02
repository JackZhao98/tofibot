package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBotDebugPreviewUsesRealPromptToolsAndHasNoSideEffects(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.store.CreateBot("alpha", "private role", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("beta", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.store.CreateGroup("team", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	var beforeMessages, beforeRuns int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&beforeMessages); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&beforeRuns); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bots/"+a.ID+"/debug-preview?conversation_id="+group.ID, nil)
	rec := httptest.NewRecorder()
	s.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got debugPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ConversationID != group.ID || !strings.Contains(got.SystemPrompt, "private role") || !strings.Contains(got.SystemPrompt, "Group members") {
		t.Fatalf("preview prompt=%q", got.SystemPrompt)
	}
	names := map[string]bool{}
	for _, tool := range got.Tools {
		names[tool.Name] = true
		if tool.Name == "" || tool.Parameters == nil {
			t.Fatalf("invalid tool preview=%+v", tool)
		}
	}
	if !names["save_memory"] || !names["workspace_capabilities"] {
		t.Fatalf("real tool registry missing expected tools: %v", names)
	}
	if len(got.LastRunTools) != 0 || got.LastRunAt != "" {
		t.Fatalf("unexpected last-run snapshot before execution: %+v %q", got.LastRunTools, got.LastRunAt)
	}
	s.recordToolSnapshot(a.ID, group.ID, []Tool{{Name: "mcp_example", Description: "discovered tool", Parameters: objectSchema(map[string]any{"query": map[string]any{"type": "string"}}, []string{"query"})}})
	rec = httptest.NewRecorder()
	s.route(rec, req)
	var withSnapshot debugPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &withSnapshot); err != nil {
		t.Fatal(err)
	}
	if len(withSnapshot.LastRunTools) != 1 || withSnapshot.LastRunTools[0].Name != "mcp_example" || withSnapshot.LastRunAt == "" {
		t.Fatalf("last-run tool snapshot=%+v at=%q", withSnapshot.LastRunTools, withSnapshot.LastRunAt)
	}
	var afterMessages, afterRuns int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&afterMessages)
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&afterRuns)
	if beforeMessages != afterMessages || beforeRuns != afterRuns {
		t.Fatalf("preview changed durable work: messages %d->%d runs %d->%d", beforeMessages, afterMessages, beforeRuns, afterRuns)
	}
}

func TestBotDebugPreviewRejectsNonMemberGroup(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("beta", "", "model")
	c, _ := s.store.CreateBot("outsider", "", "model")
	group, err := s.store.CreateGroup("team", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/bots/"+c.ID+"/debug-preview?conversation_id="+group.ID, nil)
	rec := httptest.NewRecorder()
	s.route(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider preview status=%d body=%s", rec.Code, rec.Body.String())
	}
}
