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
	input := DisplayCard{Type: "mail", From: "sender@example.com", To: "jack@example.com", Subject: "报价", Body: "实际信件内容", Summary: "需要决定报价", Source: "Gmail message 123"}
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
	if card.Card == nil || card.Card.Subject != input.Subject || !strings.Contains(card.Content, "实际信件内容") {
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

func TestDisplayCardRejectsOversizedContent(t *testing.T) {
	card := DisplayCard{Type: "text", Body: strings.Repeat("字", 32001)}
	if err := card.normalize(); err == nil {
		t.Fatal("accepted oversized content")
	}
}
