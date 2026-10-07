package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestWorkflowGuidesAreScopedAndReadOnly(t *testing.T) {
	tool := workflowGuideTool()
	for _, tc := range []struct {
		topic    string
		expected []string
		absent   string
	}{
		{"research", []string{"publication time", "retrieval time", "source link and date", "primary pages"}, "create_group"},
		{"mail", []string{"#search/", "browser.click on its subject", "read-only requests"}, "create_group"},
		{"web_tasks", []string{"request_approval", "real effect", "confirmation page"}, "create_group"},
		{"software", []string{".local/opt", "remove exactly what you installed", "no sudo"}, "create_group"},
		{"commitments", []string{"Recorded tasks do not execute themselves", "one-time schedule already appears", "Use scheduling tools for future work"}, "publication time"},
		{"team", []string{"reuse suitable existing Bots", "send_group_message once", "resumes you"}, "one-time schedule"},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{"topic": tc.topic})
			result, err := tool.Execute(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.expected {
				if !strings.Contains(strings.ToLower(result), strings.ToLower(want)) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(result, tc.absent) {
				t.Fatal("loaded unrelated workflow")
			}
		})
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"topic":"unknown"}`)); err == nil {
		t.Fatal("unknown topic accepted")
	}
	if completionReviewTool(tool.Name) {
		t.Fatal("guide read triggers substantive final review")
	}
	for _, tool := range []Tool{tool, computerHelpTool()} {
		schema, _ := json.Marshal(map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters})
		t.Logf("%s serialized definition=%d runes (rough tokens at 4 chars/token: %d)", tool.Name, len([]rune(string(schema))), (len(schema)+3)/4)
	}
}

func TestGuidesAndLazyBotDirectoryAvailableAcrossRunKinds(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.store.CreateBot("Guide fixture", "assistant", "model")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.store.CreateBot("Other fixture", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	dm, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.store.CreateGroup("Guide group", []string{bot.ID, peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, beforeRoster := s.buildContextParts(dm, Run{BotID: bot.ID, ConversationID: dm.ID}, bot)
	for i := 0; i < 8; i++ {
		if _, err := s.store.CreateBot("Unrelated peer", "unused role", "model"); err != nil {
			t.Fatal(err)
		}
	}
	_, afterRoster := s.buildContextParts(dm, Run{BotID: bot.ID, ConversationID: dm.ID}, bot)
	if len([]rune(beforeRoster)) != len([]rune(afterRoster)) {
		t.Fatal("unrelated Bot roster expanded DM system prompt")
	}
	client, err := computer.New(computer.Config{Socket: "/tmp/guide-registry-only.sock"})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client // Registration and help reads must not connect to this nonexistent socket.
	for _, conv := range []Conversation{dm, group} {
		for _, kind := range []string{"", runKindGroupChat, runKindGroupTask, runKindSchedule, runKindMessage, runKindFollowup} {
			run := Run{ID: "guide-fixture", BotID: bot.ID, ConversationID: conv.ID, Kind: kind}
			registry := map[string]Tool{}
			for _, tool := range s.tools(conv, run) {
				registry[tool.Name] = tool
			}
			if registry["read_workflow_guide"].Execute == nil || registry["list_bots"].Execute == nil || registry["computer_help"].Execute == nil {
				t.Fatalf("missing guide/directory %s/%s", conv.Kind, kind)
			}
			directory, err := registry["list_bots"].Execute(context.Background(), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(directory, peer.ID) || !strings.Contains(directory, peer.Name) {
				t.Fatal("directory did not expose exact available peer identity")
			}
			_, core := s.buildContextParts(conv, run, bot)
			if conv.Kind == "dm" && strings.Contains(core, peer.ID) {
				t.Fatal("DM preloaded another Bot's roster")
			}
			if !strings.Contains(registry["list_bots"].Description, "do not list on every turn") {
				t.Fatal("missing directory reuse boundary")
			}
		}
	}
	// Guide reads stay non-substantive after a durable continuation too.
	_, run, _, err := s.store.AddUserRun(dm.ID, bot.ID, "hello", "guide-review")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read_workflow_guide", "computer_help"} {
		for _, status := range []string{"queued", "completed"} {
			if err := s.store.RecordToolEvent(dm.ID, bot.ID, run.ID, runtime.ToolEvent{CallID: name, Name: name, Status: status, Result: "guide"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	worked, err := s.store.hasCompletionReviewToolWork(run.ID)
	if err != nil || worked {
		t.Fatalf("guide-only continuation worked=%v err=%v", worked, err)
	}
}
