package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Narration uses the existing durable message/event transaction. Replays must
// retain identity and event order, while the final answer remains a reply.
func TestWorkNarrationPersistenceAndOrder(t *testing.T) {
	s, run, conversation := streamFixture(t)
	t.Cleanup(func() {
		if s != nil {
			s.Close()
		}
	})
	ctx := context.Background()
	if _, err := s.BeginStream(run); err != nil {
		t.Fatal(err)
	}
	record := func(id string) {
		t.Helper()
		if err := s.RecordToolEvent(conversation.ID, run.BotID, run.ID, runtime.ToolEvent{CallID: id, Name: "computer_shell", Status: "queued"}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolEvent(conversation.ID, run.BotID, run.ID, runtime.ToolEvent{CallID: id, Name: "computer_shell", Status: "completed", Arguments: `{"command":"true"}`, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	record("first")
	note, err := s.publishSay(ctx, run, "say-note:synthetic:1", "progress", "The first check passed.", false)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.publishSay(ctx, run, "say-note:synthetic:1", "progress", note.Content, false)
	if err != nil || replay.ID != note.ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	record("second")
	final, ok, err := s.FinishRun(run.ID, conversation.ID, run.BotID, "Both checks passed.")
	if err != nil || !ok {
		t.Fatalf("finish: %v %v", ok, err)
	}
	var databaseSequence int
	var databaseName, databasePath string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&databaseSequence, &databaseName, &databasePath); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(filepath.Dir(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	messages, _, err := s.Messages(conversation.ID, 0, 50)
	if err != nil || len(messages) != 2 || messages[0].ID != note.ID || messages[0].Kind != "progress" || messages[1].ID != final.ID || messages[1].Kind != "" || messages[0].Seq >= messages[1].Seq {
		t.Fatalf("reload=%+v err=%v", messages, err)
	}
	events, err := s.Events(conversation.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, event := range events {
		raw, _ := json.Marshal(event["data"])
		// Events exposes decoded payloads; only public tool/message events count.
		var value map[string]any
		if str, ok := event["data"].(string); ok {
			raw = []byte(str)
		}
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		if id, ok := value["call_id"].(string); ok && value["status"] == "completed" {
			order = append(order, id)
		}
		if id, ok := value["id"].(string); ok && (id == note.ID || id == final.ID) {
			order = append(order, id)
		}
	}
	want := []string{"first", note.ID, "second", final.ID}
	if len(order) != len(want) {
		t.Fatalf("event order=%v want=%v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("event order=%v want=%v", order, want)
		}
	}
}
