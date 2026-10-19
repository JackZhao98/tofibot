package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// A hidden Bot-to-Bot conversation has no human to answer a card. Every
// human-input tool must refuse with one classified, final outcome and must
// not leave a pending question behind.
func TestHumanInputToolsRefuseHiddenConversation(t *testing.T) {
	store, c, run := questionFixture(t)
	defer store.Close()
	if _, err := store.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	c.UserVisible = false
	vault, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client, _ := recordingComputer(t)
	s := &Server{store: store, secretVault: vault, microVM: client}
	calls := map[string]string{
		"ask_user_question":    `{"question":"Which one?","type":"text"}`,
		"request_approval":     `{"question":"Proceed?","action":"do it","target":"thing","impact":"none"}`,
		"request_secret_input": `{"label":"Service key","purpose":"Synthetic use"}`,
		"ask_user_form":        `{"question":"Details","source_url":"https://forms.example.test/x","fields":[{"id":"name","label":"Name","type":"text","required":true}]}`,
	}
	tools := map[string]Tool{}
	for _, tool := range s.questionTools(c, run) {
		tools[tool.Name] = tool
	}
	// Every tool in the table must be registered, or its refusal is untested.
	if len(tools) != len(calls) {
		t.Fatalf("registered %d human-input tools, table covers %d", len(tools), len(calls))
	}
	for name, args := range calls {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %s is not registered", name)
		}
		// Bounded so a missing refusal fails here instead of parking on a card.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := tool.Execute(ctx, json.RawMessage(args))
		cancel()
		outcome, classified := tooloutcome.FromError(err)
		if !classified || outcome.Status != tooloutcome.Denied || outcome.Code != "conversation_not_user_visible" || outcome.Certainty != "not_executed" || outcome.NextAction != "explain_blocker" {
			t.Fatalf("%s: want classified refusal, got %v", name, err)
		}
	}
	pending, err := store.PendingQuestions(c.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("hidden conversation parked a question: %+v %v", pending, err)
	}
	// The same calls in a visible conversation do create a card, so the
	// refusal above is the visibility rule and not an unrelated failure.
	c.UserVisible = true
	visible := map[string]Tool{}
	for _, tool := range s.questionTools(c, run) {
		visible[tool.Name] = tool
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = visible["ask_user_question"].Execute(ctx, json.RawMessage(calls["ask_user_question"]))
	if _, classified := tooloutcome.FromError(err); classified {
		t.Fatalf("visible conversation was refused: %v", err)
	}
}
