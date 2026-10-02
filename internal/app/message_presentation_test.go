package app

import (
	"encoding/json"
	"testing"
)

func TestLegacyScheduledMessagePresentation(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	// A real user message may itself have run_id and the exact same content.
	human, _, _, err := s.AddUserRun(c.ID, bot.ID, "synthetic private task", "human-fixture")
	if err != nil {
		t.Fatal(err)
	}
	x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	if _, err = s.db.Exec(`UPDATE messages SET kind='' WHERE id=?`, root.TriggerMessageID); err != nil {
		t.Fatal(err)
	}
	// Reproduce the old durable event, not just its messages-table row.
	if _, err = s.db.Exec(`UPDATE events SET data=json_remove(data,'$.kind') WHERE type='message' AND json_extract(data,'$.id')=?`, root.TriggerMessageID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	latest, more, err := s.Messages(c.ID, 0, 1)
	if err != nil || !more || len(latest) != 1 || latest[0].ID != root.TriggerMessageID || latest[0].Kind != "scheduled_task" {
		t.Fatalf("latest=%+v more=%v err=%v", latest, more, err)
	}
	older, more, err := s.Messages(c.ID, latest[0].Seq, 1)
	if err != nil || more || len(older) != 1 || older[0].ID != human.ID || older[0].Kind != "" {
		t.Fatalf("older=%+v more=%v err=%v", older, more, err)
	}
	search, err := s.Search(c.ID, "synthetic private task", 20)
	if err != nil || len(search) != 2 || search[0].Kind != "" || search[1].Kind != "scheduled_task" {
		t.Fatalf("search=%+v err=%v", search, err)
	}
	conversations, err := s.ListConversations()
	if err != nil || len(conversations) != 1 || conversations[0].LastMessage == nil || conversations[0].LastMessage.ID != human.ID {
		t.Fatalf("conversations=%+v err=%v", conversations, err)
	}
	events, err := s.Events(c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found int
	for _, event := range events {
		if event["type"] != "message" {
			continue
		}
		encoded, _ := json.Marshal(event["data"])
		var m Message
		if err := json.Unmarshal(encoded, &m); err != nil {
			t.Fatal(err)
		}
		if m.ID == root.TriggerMessageID {
			found++
			if m.Kind != "scheduled_task" {
				t.Fatalf("event lost projection: %+v", m)
			}
		} else if m.ID == human.ID && m.Kind != "" {
			t.Fatalf("human event reclassified: %+v", m)
		}
	}
	if found != 1 {
		t.Fatalf("scheduled event count=%d", found)
	}
	var storedKind, storedEventKind string
	if err := s.db.QueryRow(`SELECT kind FROM messages WHERE id=?`, root.TriggerMessageID).Scan(&storedKind); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COALESCE(json_extract(data,'$.kind'),'') FROM events WHERE type='message' AND json_extract(data,'$.id')=?`, root.TriggerMessageID).Scan(&storedEventKind); err != nil {
		t.Fatal(err)
	}
	if storedKind != "" || storedEventKind != "" {
		t.Fatal("presentation must not rewrite stored history")
	}
}

func TestLegacyScheduledMessageRequiresExactOccurrence(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	other, err := s.CreateBot("fixture other", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	if _, err := s.db.Exec(`UPDATE messages SET kind='' WHERE id=?`, root.TriggerMessageID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mutation string
		args           []any
	}{
		{"wrong trigger", `UPDATE runs SET trigger_message_id='other' WHERE id=?`, []any{root.ID}},
		{"wrong conversation", `UPDATE runs SET conversation_id=? WHERE id=?`, []any{other.DMConversationID, root.ID}},
		{"non schedule run", `UPDATE runs SET kind='followup' WHERE id=?`, []any{root.ID}},
		{"missing occurrence", `DELETE FROM schedule_occurrences WHERE run_id=?`, []any{root.ID}},
		{"explicit kind", `UPDATE messages SET kind='user_message' WHERE id=?`, []any{root.TriggerMessageID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Exec(`SAVEPOINT presentation_test`); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := s.db.Exec(`ROLLBACK TO presentation_test; RELEASE presentation_test`); err != nil {
					t.Error(err)
				}
			}()
			if _, err := s.db.Exec(tc.mutation, tc.args...); err != nil {
				t.Fatal(err)
			}
			messages, _, err := s.Messages(c.ID, 0, 50)
			if err != nil || len(messages) != 1 || messages[0].Kind == "scheduled_task" {
				t.Fatalf("false classification=%+v err=%v", messages, err)
			}
		})
	}
}

func TestLegacyScheduledMessageProjectionBatchesAndPayloadIdentity(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	if _, err := s.db.Exec(`UPDATE messages SET kind='' WHERE id=?`, root.TriggerMessageID); err != nil {
		t.Fatal(err)
	}
	legacy := Message{ID: root.TriggerMessageID, ConversationID: c.ID, RunID: root.ID, Role: "user", Content: "unchanged", Seq: 1}
	messages := make([]Message, 203)
	for i := range messages {
		messages[i] = legacy
	}
	// A replay payload must agree with stored identity; don't repair unrelated
	// messages or overwrite an explicit future kind merely because an ID matches.
	messages[200].ConversationID = "other-conversation"
	messages[201].RunID = "other-run"
	messages[202].Kind = "custom-kind"
	if err := s.hydrateScheduledMessageKinds(messages); err != nil {
		t.Fatal(err)
	}
	for i, m := range messages {
		want := "scheduled_task"
		if i == 200 || i == 201 {
			want = ""
		} else if i == 202 {
			want = "custom-kind"
		}
		if m.Kind != want || m.Content != legacy.Content || m.Seq != legacy.Seq {
			t.Fatalf("message %d: %+v", i, m)
		}
	}
}
