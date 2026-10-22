package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func visibleOrFoldedKinds(t *testing.T, s *Store, contentLike string) map[string]int {
	t.Helper()
	rows, err := s.db.Query(`SELECT kind,COUNT(*) FROM messages WHERE role='assistant' AND content LIKE ? GROUP BY kind`, "%"+contentLike+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			out[k] = n
		}
	}
	return out
}

// Text a structured run keeps private is stored under a kind no chat view
// renders ("internal"), whatever the run's final state: after a handoff with no
// final answer it must not be a progress row the chat could show.
func TestInternalTextAfterAHandoffIsNeverAChatKind(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	const trailing = "Posted my line; waiting on the other member and I will add it when it returns."
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		run, _ := f.server.store.GetRun(req.RunID)
		if run.Kind != "" {
			return reply(ctx, req, "the other line", nil)
		}
		if _, err := callTool(ctx, req, "say", "m1", map[string]any{"purpose": "message", "text": "My line."}); err != nil {
			return "ERR " + err.Error()
		}
		for _, tool := range req.Tools {
			if tool.Name == "handoff" {
				if _, err := tool.Execute(ctx, handoffArgs(f.meta.ID, "give the other line")); err != nil {
					return "ERR " + err.Error()
				}
			}
		}
		return trailing
	}
	f.post(t, "@tofi get a line from metaphor", f.tofi)
	f.start(engine)
	f.settle(t)
	kinds := visibleOrFoldedKinds(t, f.server.store, "waiting on the other member")
	if kinds[messageKindInternal] != 1 || len(kinds) != 1 {
		t.Fatalf("the trailing text was stored as %v, want one internal row", kinds)
	}
}

// A plain turn between tool calls is internal; a say note is still a progress
// row (its design: visible while the run works, folded when it is done); the
// flag-off <progress> note keeps its kind.
func TestInternalAndProgressKindsStayDistinct(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if err := req.OnAssistantTurn(1, "Looking at the numbers before I answer."); err != nil {
			return runtime.Result{}, err
		}
		if _, err := say(ctx, req, "n1", "note", "Halfway through the numbers."); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "a1", "answer", "The answer.")
		return runtime.Result{}, err
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if k := visibleOrFoldedKinds(t, s.store, "Looking at the numbers"); k[messageKindInternal] != 1 || len(k) != 1 {
		t.Fatalf("plain turn kinds = %v", k)
	}
	if k := visibleOrFoldedKinds(t, s.store, "Halfway through"); k["progress"] != 1 || len(k) != 1 {
		t.Fatalf("say note kinds = %v", k)
	}
	s2, run2 := dmRunMode(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if err := req.OnAssistantTurn(1, "<progress>Checking the clock.</progress>"); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{Content: "Done."}, nil
	}), false)
	if run2.Status != "done" {
		t.Fatalf("run=%+v", run2)
	}
	if k := visibleOrFoldedKinds(t, s2.store, "Checking the clock"); k["progress"] != 1 || len(k) != 1 {
		t.Fatalf("flag-off progress kinds = %v", k)
	}
}

// Internal rows are not unread messages and cannot be reacted to; they do not
// become a conversation's preview line.
func TestInternalRowsDoNotCountAsUnreadOrPreview(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if err := req.OnAssistantTurn(1, "Private working text."); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "a1", "answer", "The visible answer.")
		return runtime.Result{}, err
	}))
	convs, err := s.store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range convs {
		if c.ID != run.ConversationID {
			continue
		}
		if c.UnreadCount != 1 || (c.LastMessage != nil && strings.Contains(c.LastMessage.Content, "Private working")) {
			t.Fatalf("unread=%d last=%+v", c.UnreadCount, c.LastMessage)
		}
		return
	}
	t.Fatal("conversation missing")
}

// After a handoff the refusal of a status answer ends the turn on the spot: the
// model gets no further call in which to narrate.
func TestRefusedAnswerAfterAHandoffEndsTheTurn(t *testing.T) {
	var f dedupeFixture
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "say", `{"purpose":"message","text":"My line."}`}}},
		fakeStep{calls: []fakeCall{{"c2", "handoff", fmt.Sprintf(`{"bot_id":"%s","task":"give the other line"}`, "META")}}},
		fakeStep{calls: []fakeCall{{"c3", "say", `{"purpose":"answer","resolution":"resolved","text":"The other line is on its way; I will add it."}`}}},
		fakeStep{content: "Waiting on the other member now."})
	byBot := engineByBot{}
	f = newDedupeFixture(t, byBot)
	// The scripted handoff names the real member id.
	for i := range model.steps {
		for j := range model.steps[i].calls {
			model.steps[i].calls[j].args = strings.ReplaceAll(model.steps[i].calls[j].args, "META", f.meta.ID)
		}
	}
	byBot[f.tofi.ID] = model.engine(t)
	f.post(t, "@tofi get a line from metaphor", f.tofi)
	f.start(byBot)
	f.settle(t)
	// The request that would follow the refusal carries its text; there is none.
	for _, body := range model.requests() {
		if strings.Contains(body, "This turn is over; nothing more is said") {
			t.Fatal("the model was called again after the refusal")
		}
	}
}
