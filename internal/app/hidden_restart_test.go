package app

import (
	"testing"
	"time"
)

func TestHiddenQueuedBotMessageResumesAfterServerRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateBot("sender", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateBot("recipient", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.AddRun(a.DMConversationID, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.SetRunStatus(parent.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start parent: ok=%v err=%v", ok, err)
	}
	_, child, err := store.AddBotMessage(a.DMConversationID, a.ID, b.ID, parent.ID, "Review the synthetic result")
	if err != nil {
		t.Fatal(err)
	}
	trace, err := store.GetConversation(child.ConversationID)
	if err != nil || trace.UserVisible {
		t.Fatalf("expected hidden trace: %+v err=%v", trace, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	server, err := NewServer(Config{DataDir: dir, Provider: "test", Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := server.store.GetRun(child.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == "done" {
			break
		}
		if got.Status != "queued" && got.Status != "running" {
			t.Fatalf("hidden queued run ended as %s: %s", got.Status, got.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("hidden queued run never resumed after restart: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
