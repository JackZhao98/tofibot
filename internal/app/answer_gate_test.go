package app

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// gateReminderMarker is a stable stretch of the gate's private reminder.
const gateReminderMarker = "this run has not yet tried any lookup route"

func gateRun(t *testing.T, model *fakeModel, gateOn bool) (*Server, Run) {
	return gateRunWith(t, model.engine(t), gateOn, nil)
}

// gateRunWith runs one turn; setup may install a test input-check provider.
func gateRunWith(t *testing.T, engine runtime.Engine, gateOn bool, setup func(*Server)) (*Server, Run) {
	t.Helper()
	retainDeltaEvents(t)
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if setup != nil {
		setup(s)
	}
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
		outside       bool
		looked, spent bool
		want          bool
	}{
		{true, resolutionGap, false, false, false, true},
		{true, resolutionGap, false, true, false, false},
		{true, resolutionGap, false, false, true, false},
		{true, resolutionResolved, false, false, false, false},
		{true, "", false, false, false, false},
		{false, resolutionGap, false, false, false, false},
		{true, resolutionResolved, true, false, false, true},
		{true, resolutionResolved, true, true, false, false},
		{true, resolutionResolved, true, false, true, false},
		{false, resolutionResolved, true, false, false, false},
	} {
		if got := answerGateDue(tc.enabled, tc.label, tc.outside, tc.looked, tc.spent); got != tc.want {
			t.Fatalf("%+v: got %v", tc, got)
		}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "answer_gate.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	_ = file
	for _, name := range []string{"answer_gate.go", "answer_probe.go"} {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); p == "regexp" || p == "unicode" {
				t.Fatalf("%s imports %s: the gate must not match text", name, p)
			}
		}
		src, _ := os.ReadFile(name)
		for _, banned := range []string{"strings.Contains", "strings.ToLower", "strings.HasPrefix", "MatchString"} {
			if strings.Contains(string(src), banned) {
				t.Fatalf("%s uses %s", name, banned)
			}
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

// probeProvider is a scripted input check.
type probeProvider struct {
	reply    string
	err      error
	block    chan struct{} // when set, Chat waits for it (or the context)
	calls    atomic.Int32
	returned chan struct{}
	once     sync.Once
}

func (p *probeProvider) Chat(ctx context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls.Add(1)
	defer p.once.Do(func() {
		if p.returned != nil {
			close(p.returned)
		}
	})
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return &provider.ChatResponse{Content: p.reply, Usage: provider.Usage{InputTokens: 120, OutputTokens: 8}}, nil
}

func (p *probeProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// afterProbe delays the main model until the input check has returned and its
// verdict is stored, the ordering the check is designed for.
type afterProbe struct {
	inner runtime.Engine
	p     *probeProvider
}

func (e afterProbe) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	select {
	case <-e.p.returned:
		time.Sleep(150 * time.Millisecond)
	case <-time.After(5 * time.Second):
	}
	return e.inner.Run(ctx, req)
}

func probeServer(p *probeProvider) func(*Server) {
	return func(s *Server) { s.answerProbeProvider = p }
}

func TestInputCheckAloneTriggersTheGate(t *testing.T) {
	p := &probeProvider{reply: `{"outside_information":"needed"}`, returned: make(chan struct{})}
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{sayCall("c1", "resolved", "Confident draft with no lookup.")}},
		fakeStep{calls: []fakeCall{sayCall("c2", "resolved", "Second draft after looking.")}})
	s, run := gateRunWith(t, afterProbe{model.engine(t), p}, true, probeServer(p))
	if run.Status != "done" || gateReminders(model) != 1 || modelCalls(model) != 2 {
		t.Fatalf("run=%+v reminders=%d calls=%d", run, gateReminders(model), modelCalls(model))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Second draft after looking."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	if all := strings.Join(deltaTexts(t, s.store, run.ConversationID), ""); strings.Contains(all, "Confident draft") {
		t.Fatalf("the bounced draft streamed: %q", all)
	}
}

func TestInputCheckNotNeededLeavesAResolvedAnswerAlone(t *testing.T) {
	p := &probeProvider{reply: `{"outside_information":"not_needed"}`, returned: make(chan struct{})}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayCall("c1", "resolved", "Plain answer.")}})
	_, run := gateRunWith(t, afterProbe{model.engine(t), p}, true, probeServer(p))
	if run.Status != "done" || modelCalls(model) != 1 || gateReminders(model) != 0 {
		t.Fatalf("run=%+v calls=%d reminders=%d", run, modelCalls(model), gateReminders(model))
	}
}

// A check that has not returned when the answer arrives is ignored: the run
// finishes without waiting for it.
func TestLateInputCheckNeverDelaysAResolvedAnswer(t *testing.T) {
	p := &probeProvider{reply: `{"outside_information":"needed"}`, block: make(chan struct{})}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayCall("c1", "resolved", "Answer that does not wait.")}})
	start := time.Now()
	s, run := gateRunWith(t, model.engine(t), true, probeServer(p))
	if run.Status != "done" || modelCalls(model) != 1 || gateReminders(model) != 0 {
		t.Fatalf("run=%+v calls=%d reminders=%d", run, modelCalls(model), gateReminders(model))
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the run took %v while the check was still pending", d)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Answer that does not wait."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	close(p.block)
}

func TestInputCheckFailureFallsBackToTheLabelOnlyRule(t *testing.T) {
	for _, p := range []*probeProvider{
		{err: errors.New("provider unavailable"), returned: make(chan struct{})},
		{reply: "not the structured answer", returned: make(chan struct{})},
		{reply: `{"outside_information":"maybe"}`, returned: make(chan struct{})},
	} {
		resolved := newFakeModel(t, fakeStep{calls: []fakeCall{sayCall("c1", "resolved", "Stands.")}})
		_, run := gateRunWith(t, afterProbe{resolved.engine(t), p}, true, probeServer(p))
		if run.Status != "done" || modelCalls(resolved) != 1 {
			t.Fatalf("resolved with a failed check: run=%+v calls=%d", run, modelCalls(resolved))
		}
		gap := newFakeModel(t,
			fakeStep{calls: []fakeCall{sayCall("c1", "gap", "First gap.")}},
			fakeStep{calls: []fakeCall{sayCall("c2", "resolved", "Second.")}})
		q := &probeProvider{err: p.err, reply: p.reply, returned: make(chan struct{})}
		_, run = gateRunWith(t, afterProbe{gap.engine(t), q}, true, probeServer(q))
		if run.Status != "done" || gateReminders(gap) != 1 {
			t.Fatalf("a gap with a failed check must still bounce: run=%+v reminders=%d", run, gateReminders(gap))
		}
	}
}

func TestSettingOffTurnsOffTheInputCheckCallAndTheGate(t *testing.T) {
	p := &probeProvider{reply: `{"outside_information":"needed"}`, returned: make(chan struct{})}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayCall("c1", "resolved", "No check, no gate.")}})
	_, run := gateRunWith(t, model.engine(t), false, probeServer(p))
	if run.Status != "done" || p.calls.Load() != 0 || modelCalls(model) != 1 || gateReminders(model) != 0 {
		t.Fatalf("run=%+v probe calls=%d model calls=%d", run, p.calls.Load(), modelCalls(model))
	}
}

func TestInputCheckRunsOncePerTurnAndIsMetered(t *testing.T) {
	p := &probeProvider{reply: `{"outside_information":"not_needed"}`, returned: make(chan struct{})}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayCall("c1", "resolved", "Metered.")}})
	s, run := gateRunWith(t, afterProbe{model.engine(t), p}, true, probeServer(p))
	var n, in int
	if err := s.store.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(input_tokens),0) FROM usage_calls WHERE run_id=? AND kind=?`, run.ID, usageKindReview).Scan(&n, &in); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 1 || n != 1 || in != 120 {
		t.Fatalf("probe calls=%d metered rows=%d input=%d", p.calls.Load(), n, in)
	}
}

func TestProbeVerdictReadsOnlyTheEnumField(t *testing.T) {
	for raw, want := range map[string]int32{
		`{"outside_information":"needed"}`:                       probeNeeded,
		"```json\n{\"outside_information\":\"not_needed\"}\n```": probeNotNeeded,
		`{"outside_information":"Needed"}`:                       probeFailed,
		`needed`:                                                 probeFailed,
		``:                                                       probeFailed,
	} {
		if got := probeVerdict(raw); got != want {
			t.Fatalf("%q -> %d, want %d", raw, got, want)
		}
	}
}

func TestCodexOnlyWorkspaceChecksWithItsSmallestModel(t *testing.T) {
	if codexDefaultModel != "codex-gpt-5.6-luna" {
		t.Fatalf("codex small model = %s", codexDefaultModel)
	}
}

// A verdict that arrives after the answer's text began streaming is not used:
// the draft was already shown, so bouncing it would retract visible text.
func TestVerdictArrivingAfterTheTextBeganDoesNotBounceIt(t *testing.T) {
	p := &probeProvider{reply: `{"outside_information":"needed"}`, block: make(chan struct{})}
	var execErr error
	s, run := gateRunWith(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: "g1", Name: "say"})
		for _, f := range []string{`{"purpose":"answer",`, `"resolution":"resolved",`, `"text":"Already `, `on screen."}`} {
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: f})
		}
		close(p.block) // the check answers only now
		time.Sleep(400 * time.Millisecond)
		_, execErr = callTool(ctx, req, "say", "g1", map[string]any{"purpose": "answer", "resolution": "resolved", "text": "Already on screen."})
		return runtime.Result{}, nil
	}), true, probeServer(p))
	if run.Status != "done" || execErr != nil {
		t.Fatalf("run=%+v execErr=%v", run, execErr)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Already on screen."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}
