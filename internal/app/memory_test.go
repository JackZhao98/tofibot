package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type memoryEngine struct {
	content string
	err     error
	called  int
}

func (e *memoryEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	e.called++
	if e.err != nil {
		return runtime.Result{}, e.err
	}
	if len(req.Tools) != 0 {
		return runtime.Result{Content: "unexpected tool"}, nil
	}
	return runtime.Result{Content: e.content}, nil
}

func TestPrepareLongTermContextPersistsBoundedSummaryAtBoundary(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: &memoryEngine{content: "facts: user prefers concise answers; open task: ship memory"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "instructions", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	for i := 0; i < 270; i++ {
		if _, _, err := s.store.AddMessage(c.ID, "user", "", "", "fact "+string(rune('a'+i%26)), ""); err != nil {
			t.Fatal(err)
		}
	}
	m, r, _, err := s.store.AddUserRun(c.ID, b.ID, "current", "current")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := s.prepareLongTermContext(context.Background(), s.engine, c, r, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("memory tools=%d", len(tools))
	}
	_, covered, summary, err := s.store.LatestSummary(c.ID)
	if err != nil || covered != m.Seq-80 || summary == "" || len([]rune(summary)) > maxSummaryRunes {
		t.Fatalf("summary covered=%d want=%d content=%q err=%v", covered, m.Seq-80, summary, err)
	}
}

func TestSummaryFailureLeavesPreviousViewAndRawHistory(t *testing.T) {
	engine := &memoryEngine{content: "first"}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "instructions", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	for i := 0; i < 270; i++ {
		_, _, _ = s.store.AddMessage(c.ID, "user", "", "", "history", "")
	}
	_, r, _, _ := s.store.AddUserRun(c.ID, b.ID, "current", "trigger")
	_, err = s.prepareLongTermContext(context.Background(), engine, c, r, b)
	if err != nil {
		t.Fatal(err)
	}
	_, oldCovered, old, _ := s.store.LatestSummary(c.ID)
	_, _ = s.store.SetRunStatus(r.ID, "done", "")
	engine.err = errors.New("provider unavailable")
	_, r2, _, _ := s.store.AddUserRun(c.ID, b.ID, "next", "next")
	_, _ = s.prepareLongTermContext(context.Background(), engine, c, r2, b)
	_, covered, got, _ := s.store.LatestSummary(c.ID)
	if covered != oldCovered || got != old {
		t.Fatalf("failed update replaced summary: covered %d/%d content %q/%q", covered, oldCovered, got, old)
	}
	hits, _ := s.store.Search(c.ID, "history", 1)
	if len(hits) != 1 {
		t.Fatal("raw history unavailable")
	}
}

func TestLongTermMemoryToolsEnforceScopeAndUpdate(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "instructions", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	other, _ := s.store.CreateBot("other", "instructions", "model")
	mem, _ := s.store.AddMemory(c.ID, b.ID, "old fact")
	_, r, _, _ := s.store.AddUserRun(c.ID, b.ID, "hello", "tool")
	var update Tool
	for _, tool := range (&Server{store: s.store}).longTermMemoryTools(c, r) {
		if tool.Name == "update_memory" {
			update = tool
		}
	}
	data, _ := json.Marshal(map[string]string{"id": mem.ID, "title": "Corrected fact", "description": "Current durable fact", "content": "corrected fact"})
	if _, err := update.Execute(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	got, _ := s.store.GetMemory(mem.ID)
	if got.Content != "corrected fact" || got.Revision != 2 {
		t.Fatalf("memory=%+v", got)
	}
	foreign, _ := s.store.AddMemory(c.ID, other.ID, "private")
	data, _ = json.Marshal(map[string]string{"id": foreign.ID, "content": "leak"})
	if _, err := update.Execute(context.Background(), data); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("foreign update err=%v", err)
	}
}

func TestMemoryMutationAndEventRollbackTogether(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)

	if _, err = s.store.db.Exec(`CREATE TRIGGER fail_memory_event BEFORE INSERT ON events WHEN NEW.type='memory' BEGIN SELECT RAISE(ABORT,'memory event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/conversations/"+c.ID+"/memories", strings.NewReader(`{"content":"new fact"}`))
	response := httptest.NewRecorder()
	s.conversation(response, request, c.ID)
	if response.Code != 500 {
		t.Fatalf("add status=%d body=%s", response.Code, response.Body.String())
	}
	var count int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE conversation_id=?`, c.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed add committed memory: count=%d err=%v", count, err)
	}
	if _, err = s.store.db.Exec(`DROP TRIGGER fail_memory_event`); err != nil {
		t.Fatal(err)
	}
	memory, err := s.store.AddMemory(c.ID, b.ID, "old fact")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`CREATE TRIGGER fail_memory_event BEFORE INSERT ON events WHEN NEW.type='memory' BEGIN SELECT RAISE(ABORT,'memory event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.UpdateMemory(memory.ID, "new fact"); err == nil {
		t.Fatal("update ignored event failure")
	}
	unchanged, err := s.store.GetMemory(memory.ID)
	if err != nil || unchanged.Content != "old fact" || unchanged.Revision != 1 {
		t.Fatalf("failed update committed: memory=%+v err=%v", unchanged, err)
	}
	if _, err = s.store.db.Exec(`DROP TRIGGER fail_memory_event`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`CREATE TRIGGER fail_memory_delete_event BEFORE INSERT ON events WHEN NEW.type='memory_deleted' BEGIN SELECT RAISE(ABORT,'memory delete event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.store.DeleteMemory(memory.ID); err == nil {
		t.Fatal("delete ignored event failure")
	}
	if _, err = s.store.GetMemory(memory.ID); err != nil {
		t.Fatalf("failed delete removed memory: %v", err)
	}
}
