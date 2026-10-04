package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDisplayContentPersistsAndReplaysStructuredCard(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.store.CreateBot("秘书", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "读信", "read-mail")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !changed {
		t.Fatalf("running: %v %v", changed, err)
	}
	var display Tool
	for _, tool := range s.displayTools(conv, run) {
		if tool.Name == "display_content" {
			display = tool
		}
	}
	if display.Name == "" {
		t.Fatal("missing display_content")
	}
	input := DisplayCard{Type: "text", Title: "Synthetic report", Body: "Synthetic text content", Source: "Synthetic source"}
	raw, _ := json.Marshal(input)
	result, err := display.Execute(context.Background(), raw)
	if err != nil || !strings.Contains(result, "Displayed card") {
		t.Fatalf("display result=%q error=%v", result, err)
	}
	msgs, _, err := s.store.Messages(conv.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var card Message
	for _, message := range msgs {
		if message.Kind == "ui_card" {
			card = message
		}
	}
	if card.Card == nil || card.Card.Type != "text" || card.Card.Title != input.Title || !strings.Contains(card.Content, input.Body) {
		t.Fatalf("card not hydrated: %+v", card)
	}
	events, err := s.store.Events(conv.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event["type"] == "message" {
			if data, ok := event["data"].(map[string]any); ok && data["id"] == card.ID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("card missing from durable message events")
	}
	textRaw, _ := json.Marshal(DisplayCard{Type: "text", Title: "调查摘要", Body: "第一项已核实。"})
	if _, err = display.Execute(context.Background(), textRaw); err != nil {
		t.Fatalf("text display: %v", err)
	}
	_, err = display.Execute(context.Background(), []byte(`{"type":"mail","subject":"invented","body":"no sender"}`))
	if err == nil {
		t.Fatal("accepted incomplete mail")
	}
	if changed, err := s.store.SetRunStatus(run.ID, "cancelled", ""); err != nil || !changed {
		t.Fatalf("cancelled: %v %v", changed, err)
	}
	if _, err = display.Execute(context.Background(), raw); err == nil {
		t.Fatal("published card from inactive run")
	}
}

func TestDisplayContentAdvertisesTextAndRejectsGenericMailBeforeWrite(t *testing.T) {
	s, c, r, _ := mailFixture(t)
	display := mailTool(t, s, c, r, "display_content")
	schema, err := json.Marshal(display.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil || len(parsed.Properties["type"].Enum) != 1 || parsed.Properties["type"].Enum[0] != "text" {
		t.Fatalf("generic display schema must be text-only: %s %v", schema, err)
	}
	var beforeMessages, beforeEvents int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&beforeMessages); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&beforeEvents); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"mail", " mail ", "mail_list", "\tmail_list\n"} {
		t.Run(kind, func(t *testing.T) {
			raw, _ := json.Marshal(DisplayCard{Type: kind, Body: "Synthetic historical body", From: "fixture@example.test", Subject: "Synthetic subject", Source: "Model-authored source"})
			if _, err := display.Execute(context.Background(), raw); err == nil {
				t.Fatal("generic mail bypass accepted")
			}
			var messages, events int
			if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&messages); err != nil {
				t.Fatal(err)
			}
			if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if messages != beforeMessages || events != beforeEvents {
				t.Fatal("rejected generic mail wrote a card/event")
			}
		})
	}
	if _, err := display.Execute(context.Background(), []byte(`{"type":" text ","body":"Synthetic whitespace-normalized text"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayCardHistoricalMailRemainsReadableOnReplay(t *testing.T) {
	s, c, r, _ := mailFixture(t)
	// Model the already-persisted legacy format without calling display_content.
	legacy := DisplayCard{Type: "mail", From: "fixture@example.test", Subject: "Synthetic legacy subject", Body: "Synthetic legacy body", Source: "Model-authored source"}
	m, err := s.store.AddDisplayCard(context.Background(), c.ID, r, legacy)
	if err != nil {
		t.Fatal(err)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == m.ID {
			if message.Card == nil || message.Card.Type != "mail" || message.Card.Mail != nil || message.Card.Body != legacy.Body || message.Card.Source != legacy.Source {
				t.Fatalf("historical card lost its readable, unverified format: %+v", message)
			}
			return
		}
	}
	t.Fatal("historical card missing on replay")
}

func TestDisplayCardRejectsOversizedContent(t *testing.T) {
	card := DisplayCard{Type: "text", Body: strings.Repeat("字", 32001)}
	if err := card.normalize(); err == nil {
		t.Fatal("accepted oversized content")
	}
}
