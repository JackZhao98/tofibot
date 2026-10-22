package app

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// gateReminderMarker is a stable stretch of the gate's private reminder.
const gateReminderMarker = "you labelled it a gap"

func gateRun(t *testing.T, model *fakeModel, gateOn bool) (*Server, Run) {
	t.Helper()
	retainDeltaEvents(t)
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: model.engine(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if !gateOn {
		if code := autoPutCode(t, s, `{"answer_gate":false}`); code != http.StatusOK {
			t.Fatalf("put=%d", code)
		}
	}
	b, err := s.store.CreateBot("gate fixture", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "What did I tell you earlier about the synthetic project?", "gate-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, run)
	return s, waitRunTerminal(t, s, run.ID)
}

func sayCall(id, resolution, text string) fakeCall {
	args, _ := json.Marshal(map[string]string{"purpose": "answer", "resolution": resolution, "text": text})
	return fakeCall{id, "say", string(args)}
}

func gateReminders(m *fakeModel) int {
	n := 0
	for _, body := range m.requests() {
		if c := strings.Count(body, gateReminderMarker); c > n {
			n = c
		}
	}
	return n
}

func TestGapWithoutLookupIsBouncedOnceAndTheSecondDraftIsFinal(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{sayCall("c1", "gap", "First draft that gives up.")}},
		fakeStep{calls: []fakeCall{sayCall("c2", "resolved", "Second draft with the answer.")}})
	s, run := gateRun(t, model, true)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if got := gateReminders(model); got != 1 {
		t.Fatalf("reminder appeared %d times, want 1", got)
	}
	if n := modelCalls(model); n != 2 {
		t.Fatalf("model calls = %d, want 2", n)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Second draft with the answer."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestGapAfterALookupIsNotBounced(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "search_history", `{"query":"synthetic project"}`}}},
		fakeStep{calls: []fakeCall{sayCall("c2", "gap", "I checked the history and found nothing.")}})
	s, run := gateRun(t, model, true)
	if run.Status != "done" || gateReminders(model) != 0 {
		t.Fatalf("run=%+v reminders=%d", run, gateReminders(model))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|I checked the history and found nothing."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestResolvedAnswerCostsNoExtraCall(t *testing.T) {
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayCall("c1", "resolved", "A direct answer.")}})
	s, run := gateRun(t, model, true)
	if run.Status != "done" || modelCalls(model) != 1 || gateReminders(model) != 0 {
		t.Fatalf("run=%+v calls=%d reminders=%d", run, modelCalls(model), gateReminders(model))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|A direct answer."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestGateBounceBudgetIsOne(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{sayCall("c1", "gap", "First gap.")}},
		fakeStep{calls: []fakeCall{sayCall("c2", "gap", "Second gap, still no lookup.")}},
		fakeStep{content: "a call that must not happen"})
	s, run := gateRun(t, model, true)
	if run.Status != "done" || modelCalls(model) != 2 || gateReminders(model) != 1 {
		t.Fatalf("run=%+v calls=%d reminders=%d", run, modelCalls(model), gateReminders(model))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Second gap, still no lookup."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestGateSettingOffNeverBounces(t *testing.T) {
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayCall("c1", "gap", "A gap that stands.")}})
	s, run := gateRun(t, model, false)
	if run.Status != "done" || modelCalls(model) != 1 || gateReminders(model) != 0 {
		t.Fatalf("run=%+v calls=%d reminders=%d", run, modelCalls(model), gateReminders(model))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|A gap that stands."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// The bounced draft never reaches the stream; a resolved answer streams from
// its first fragment.
func TestGatedDraftIsNotStreamedAndResolvedOneIs(t *testing.T) {
	stream := func(req runtime.Request, id string, frags ...string) {
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: id, Name: "say"})
		for _, f := range frags {
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: f})
		}
	}
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		stream(req, "g1", `{"purpose":"answer",`, `"resolution":"gap",`, `"text":"Bounced `, `draft."}`)
		if _, err := callTool(ctx, req, "say", "g1", map[string]any{"purpose": "answer", "resolution": "gap", "text": "Bounced draft."}); err == nil {
			return runtime.Result{}, context.Canceled
		}
		stream(req, "g2", `{"purpose":"answer",`, `"resolution":"resolved",`, `"text":"Streamed `, `answer."}`)
		_, err := callTool(ctx, req, "say", "g2", map[string]any{"purpose": "answer", "resolution": "resolved", "text": "Streamed answer."})
		return runtime.Result{}, err
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	all := strings.Join(deltaTexts(t, s.store, run.ConversationID), "")
	if strings.Contains(all, "Bounced") || all != "Streamed answer." {
		t.Fatalf("streamed %q", all)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Streamed answer."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestResolvedStreamsBeforeTheTextWhenLabelComesLast(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: "g1", Name: "say"})
		for _, f := range []string{`{"purpose":"answer",`, `"text":"Label `, `last.",`, `"resolution":"resolved"}`} {
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: f})
		}
		_, err := callTool(ctx, req, "say", "g1", map[string]any{"purpose": "answer", "resolution": "resolved", "text": "Label last."})
		return runtime.Result{}, err
	}))
	if all := strings.Join(deltaTexts(t, s.store, run.ConversationID), ""); run.Status != "done" || all != "Label last." {
		t.Fatalf("run=%+v streamed %q", run, all)
	}
}

func TestSayLabelIsRequiredInTheSchema(t *testing.T) {
	schema := sayToolSchema()
	props := schema["properties"].(map[string]any)
	label, ok := props["resolution"].(map[string]any)
	if !ok {
		t.Fatal("say has no resolution property")
	}
	if got := label["enum"].([]string); len(got) != 2 || got[0] != resolutionResolved || got[1] != resolutionGap {
		t.Fatalf("enum = %v", got)
	}
	required := false
	for _, k := range schema["required"].([]string) {
		required = required || k == "resolution"
	}
	if !required {
		t.Fatal("resolution is not required")
	}
	// Prompt budget: the label adds this much to every structured request's tool
	// definitions. Measured 2026-10-21: 426 bytes of schema JSON; raise only by a
	// measured amount.
	raw, _ := json.Marshal(label)
	if len(raw) > 430 {
		t.Fatalf("label schema is %d bytes, ceiling 430", len(raw))
	}
}

// The gate decision depends only on the label, the tool record, the setting and
// the budget: not on any text, and the gate source has no pattern matching.
func TestGateDecisionReadsOnlyLabelAndToolRecord(t *testing.T) {
	for _, tc := range []struct {
		enabled       bool
		label         string
		looked, spent bool
		want          bool
	}{
		{true, resolutionGap, false, false, true},
		{true, resolutionGap, true, false, false},
		{true, resolutionGap, false, true, false},
		{true, resolutionResolved, false, false, false},
		{true, "", false, false, false},
		{false, resolutionGap, false, false, false},
	} {
		if got := answerGateDue(tc.enabled, tc.label, tc.looked, tc.spent); got != tc.want {
			t.Fatalf("%+v: got %v", tc, got)
		}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "answer_gate.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		if p := strings.Trim(imp.Path.Value, `"`); p == "regexp" || p == "unicode" {
			t.Fatalf("answer_gate.go imports %s: the gate must not match text", p)
		}
	}
	src, _ := os.ReadFile("answer_gate.go")
	for _, banned := range []string{"strings.Contains", "strings.ToLower", "strings.HasPrefix", "MatchString"} {
		if strings.Contains(string(src), banned) {
			t.Fatalf("answer_gate.go uses %s", banned)
		}
	}
}

func TestLookupAttemptIsDecidedByToolNameAndRisk(t *testing.T) {
	for _, name := range []string{"search_history", "list_memory", "web_search", "web_fetch", "read_attachment", "computer_browser"} {
		if !lookupAttempt(name, "") {
			t.Fatalf("%s should count as a lookup", name)
		}
	}
	for _, name := range []string{"say", "save_memory", "create_work_item", "get_context_usage"} {
		if lookupAttempt(name, "") {
			t.Fatalf("%s should not count as a lookup", name)
		}
	}
	if !lookupAttempt("mcp_read", "observation") || lookupAttempt("get_context_usage", "observation") {
		t.Fatal("a read-only connected-tool call counts; bookkeeping reads do not")
	}
}
