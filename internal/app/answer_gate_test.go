package app

import (
	"context"
	"encoding/json"
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

// gradeProvider is a scripted grader: replies are used in order, the last one
// repeats.
type gradeProvider struct {
	mu       sync.Mutex
	replies  []string
	err      error
	block    bool
	calls    atomic.Int32
	requests []string
	onCall   func()
}

func (g *gradeProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	n := int(g.calls.Add(1))
	g.mu.Lock()
	if len(req.Messages) > 0 {
		g.requests = append(g.requests, req.System+"\n"+req.Messages[0].Content)
	}
	var reply string
	if len(g.replies) > 0 {
		reply = g.replies[min(n-1, len(g.replies)-1)]
	}
	g.mu.Unlock()
	if g.onCall != nil {
		g.onCall()
	}
	if g.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if g.err != nil {
		return nil, g.err
	}
	return &provider.ChatResponse{Content: reply, Usage: provider.Usage{InputTokens: 900, OutputTokens: 60}}, nil
}

func (g *gradeProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return g.Chat(ctx, req)
}

const (
	gradePass = `{"criteria":[{"id":"C1","result":"met","note":""},{"id":"C2","result":"met","note":""},{"id":"C3","result":"met","note":""},{"id":"C4","result":"met","note":""}]}`
	gradeGap  = `{"criteria":[{"id":"C1","result":"met","note":""},{"id":"C2","result":"met","note":""},{"id":"C3","result":"gap","note":"Hands the work back to the person."},{"id":"C4","result":"met","note":""}]}`
)

// gateHook, when set by a test, receives the server and conversation before the run starts.
var gateHook func(*Server, string)

// gateRunWith runs one scheduled occurrence in a group, the kind of work the
// answer gate reviews (nobody is waiting on it).
func gateRunWith(t *testing.T, engine runtime.Engine, g *gradeProvider, settings string) (*Server, Run) {
	t.Helper()
	retainDeltaEvents(t)
	f := newDedupeFixture(t, engine)
	if g != nil {
		f.server.answerGraderProvider = g
	}
	if settings != "" {
		if code := autoPutCode(t, f.server, settings); code != http.StatusOK {
			t.Fatalf("put=%d", code)
		}
	}
	if gateHook != nil {
		gateHook(f.server, f.group.ID)
	}
	return f.server, runScheduledOnce(t, f)
}

// gateChatRun runs one person-triggered DM turn, which the gate never reviews.
func gateChatRun(t *testing.T, engine runtime.Engine, g *gradeProvider) (*Server, Run) {
	t.Helper()
	retainDeltaEvents(t)
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.answerGraderProvider = g
	b, err := s.store.CreateBot("gate chat fixture", "", "model")
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

func sayAnswerCall(id, text string) fakeCall {
	args, _ := json.Marshal(map[string]string{"purpose": "answer", "text": text})
	return fakeCall{id, "say", string(args)}
}

func revisionRequests(m *fakeModel) int {
	n := 0
	for _, body := range m.requests() {
		if c := strings.Count(body, "A reviewer graded your draft reply"); c > n {
			n = c
		}
	}
	return n
}

func TestGapDraftIsReplacedAndNeverShown(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap, gradePass}}
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{sayAnswerCall("c1", "First draft that hands the work back.")}},
		fakeStep{calls: []fakeCall{sayAnswerCall("c2", "Second draft that does the work.")}})
	s, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "done" || g.calls.Load() != 2 || modelCalls(model) != 2 || revisionRequests(model) != 1 {
		t.Fatalf("run=%+v grader=%d model=%d revisions=%d", run, g.calls.Load(), modelCalls(model), revisionRequests(model))
	}
	// The writer receives the per-criterion gap, by title.
	if !strings.Contains(strings.Join(model.requests(), "\n"), "Does not give up early: Hands the work back to the person.") {
		t.Fatal("the gap list did not reach the writer")
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Second draft that does the work."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestPassingDraftShipsAfterOneGrading(t *testing.T) {
	g := &gradeProvider{replies: []string{gradePass}}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayAnswerCall("c1", "A fine answer.")}})
	s, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "done" || g.calls.Load() != 1 || modelCalls(model) != 1 {
		t.Fatalf("run=%+v grader=%d model=%d", run, g.calls.Load(), modelCalls(model))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|A fine answer."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestLatestDraftShipsAfterTheRoundCap(t *testing.T) {
	for _, tc := range []struct {
		setting string
		graded  int32
		final   string
	}{{"", 2, "Third."}, {`{"answer_gate":1}`, 1, "Second."}} {
		g := &gradeProvider{replies: []string{gradeGap}}
		model := newFakeModel(t,
			fakeStep{calls: []fakeCall{sayAnswerCall("c1", "First.")}},
			fakeStep{calls: []fakeCall{sayAnswerCall("c2", "Second.")}},
			fakeStep{calls: []fakeCall{sayAnswerCall("c3", "Third.")}})
		s, run := gateRunWith(t, model.engine(t), g, tc.setting)
		if run.Status != "done" || g.calls.Load() != tc.graded {
			t.Fatalf("%q: run=%+v graded=%d, want %d", tc.setting, run, g.calls.Load(), tc.graded)
		}
		if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|" + tc.final}) {
			t.Fatalf("%q: messages = %q", tc.setting, kindsAndText(rows))
		}
	}
}

func TestGraderFailureShipsTheDraft(t *testing.T) {
	for name, g := range map[string]*gradeProvider{
		"error":        {err: context.DeadlineExceeded},
		"not json":     {replies: []string{"looks fine"}},
		"bad enum":     {replies: []string{strings.Replace(gradeGap, `"gap"`, `"revise"`, 1)}},
		"missing item": {replies: []string{`{"criteria":[{"id":"C1","result":"gap","note":"x"}]}`}},
		"unknown id":   {replies: []string{strings.Replace(gradeGap, `"C3"`, `"C9"`, 1)}},
	} {
		model := newFakeModel(t, fakeStep{calls: []fakeCall{sayAnswerCall("c1", "Draft that ships.")}})
		s, run := gateRunWith(t, model.engine(t), g, "")
		if run.Status != "done" || modelCalls(model) != 1 {
			t.Fatalf("%s: run=%+v model=%d", name, run, modelCalls(model))
		}
		if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Draft that ships."}) {
			t.Fatalf("%s: messages = %q", name, kindsAndText(rows))
		}
	}
}

func TestGraderTimeoutShipsTheDraft(t *testing.T) {
	old := answerGraderTimeout
	answerGraderTimeout = 150 * time.Millisecond
	defer func() { answerGraderTimeout = old }()
	g := &gradeProvider{block: true}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayAnswerCall("c1", "Ships after the timeout.")}})
	start := time.Now()
	s, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "done" || time.Since(start) > 5*time.Second {
		t.Fatalf("run=%+v after %v", run, time.Since(start))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Ships after the timeout."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func TestSettingZeroTurnsTheGateOff(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayAnswerCall("c1", "No review.")}})
	_, run := gateRunWith(t, model.engine(t), g, `{"answer_gate":0}`)
	if run.Status != "done" || g.calls.Load() != 0 || modelCalls(model) != 1 {
		t.Fatalf("run=%+v grader=%d model=%d", run, g.calls.Load(), modelCalls(model))
	}
}

// The grader sees the request, the visible conversation and the reply, never
// how the reply was produced.
func TestGraderSeesOnlyWhatTheRecipientSees(t *testing.T) {
	g := &gradeProvider{replies: []string{gradePass}}
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"t1", "search_history", `{"query":"synthetic project"}`}}},
		fakeStep{calls: []fakeCall{sayAnswerCall("c1", "The delivered synthetic reply.")}})
	_, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "done" || g.calls.Load() != 1 {
		t.Fatalf("run=%+v grader=%d", run, g.calls.Load())
	}
	in := g.requests[0]
	for _, want := range []string{"post the daily synthetic note", "The delivered synthetic reply.", "## C1", "## C4"} {
		if !strings.Contains(in, want) {
			t.Fatalf("grader input lacks %q", want)
		}
	}
	for _, banned := range []string{"search_history", "tool_call", "thinking"} {
		if strings.Contains(in, banned) {
			t.Fatalf("grader input exposes the process: %q", banned)
		}
	}
}

// While the grader works, the draft is not in the stream; the shipped answer
// then appears whole.
func TestDraftIsHeldFromTheStreamUntilItPasses(t *testing.T) {
	var seenDuringGrading []string
	g := &gradeProvider{replies: []string{gradePass}}
	gateHook = func(s *Server, conv string) {
		g.onCall = func() { seenDuringGrading = deltaTexts(t, s.store, conv) }
	}
	defer func() { gateHook = nil }()
	s, run := gateRunWithStore(t, g)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if len(seenDuringGrading) != 0 {
		t.Fatalf("streamed while grading: %q", seenDuringGrading)
	}
	if all := strings.Join(deltaTexts(t, s.store, run.ConversationID), ""); all != "Held then shown." {
		t.Fatalf("streamed %q", all)
	}
}

// gateRunWithStore runs a streamed say answer; it hands the server and run to
// the grader's hook through the package-level closure variables of the caller.
func gateRunWithStore(t *testing.T, g *gradeProvider) (*Server, Run) {
	t.Helper()
	engine := dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: "g1", Name: "say"})
		for _, f := range []string{`{"purpose":"answer",`, `"text":"Held `, `then shown."}`} {
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: f})
		}
		_, err := callTool(ctx, req, "say", "g1", map[string]any{"purpose": "answer", "text": "Held then shown."})
		return runtime.Result{}, err
	})
	return gateRunWith(t, engine, g, "")
}

func TestGateScope(t *testing.T) {
	visible := Conversation{UserVisible: true}
	group := Conversation{Kind: "group", UserVisible: true}
	for _, tc := range []struct {
		name       string
		c          Conversation
		r          Run
		structured bool
		want       bool
	}{
		{"scheduled occurrence", visible, Run{Kind: runKindSchedule}, true, true},
		{"group schedule", group, Run{Kind: runKindSchedule}, true, true},
		{"person's DM message", visible, Run{}, true, false},
		{"person's group message", group, Run{Kind: runKindGroupChat}, true, false},
		{"group mention", group, Run{Kind: runKindGroupMention}, true, false},
		{"delegated work", group, Run{Kind: runKindTeam}, true, false},
		{"delegated message", group, Run{Kind: runKindMessage}, true, false},
		{"group task", group, Run{Kind: runKindGroupTask}, true, false},
		{"follow-up to a delegation", group, Run{Kind: runKindFollowup}, true, false},
		{"hidden scheduled", Conversation{}, Run{Kind: runKindSchedule}, true, false},
		{"triage", visible, Run{Kind: runKindTriage}, true, false},
		{"plain-text protocol", visible, Run{Kind: runKindSchedule}, false, false},
	} {
		if got := answerGateScope(tc.c, tc.r, tc.structured); got != tc.want {
			t.Fatalf("%s: got %v", tc.name, got)
		}
	}
}

// A person-triggered answer streams from its first character and the grader is
// never called: chat is exactly as it was before the gate.
func TestPersonTriggeredAnswerStreamsAndIsNeverGraded(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	s, run := gateChatRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: "g1", Name: "say"})
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: `{"purpose":"answer","text":"Streams `})
		time.Sleep(150 * time.Millisecond) // the draft flushes on a timer
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: `right away."}`})
		_, err := callTool(ctx, req, "say", "g1", map[string]any{"purpose": "answer", "text": "Streams right away."})
		return runtime.Result{}, err
	}), g)
	if run.Status != "done" || g.calls.Load() != 0 {
		t.Fatalf("run=%+v grader calls=%d", run, g.calls.Load())
	}
	deltas := deltaTexts(t, s.store, run.ConversationID)
	if len(deltas) < 2 || !strings.HasPrefix(deltas[0], "Streams") || strings.Join(deltas, "") != "Streams right away." {
		t.Fatalf("deltas = %q", deltas)
	}
}

// The verdict is derived from per-criterion results alone, and the Go side
// never matches text: no pattern imports, no phrase checks.
func TestGradeDecisionReadsOnlyStructuredResults(t *testing.T) {
	criteria := rubricCriteria(answerRubric)
	if len(criteria) < 4 || criteria[0].ID != "C1" {
		t.Fatalf("criteria = %+v", criteria)
	}
	results, ok := parseGrade(gradeGap, criteria)
	if !ok || len(gradeGaps(results)) != 1 || gradeGaps(results)[0].ID != "C3" {
		t.Fatalf("gaps = %+v ok=%v", gradeGaps(results), ok)
	}
	// The same results with different notes decide the same way.
	other := strings.Replace(gradeGap, "Hands the work back to the person.", "Anything else at all.", 1)
	results2, _ := parseGrade(other, criteria)
	if len(gradeGaps(results2)) != 1 {
		t.Fatal("the decision depends on note text")
	}
	if results, _ = parseGrade(gradePass, criteria); len(gradeGaps(results)) != 0 {
		t.Fatal("all met must pass")
	}
	for _, name := range []string{"answer_review.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); p == "regexp" || p == "unicode" {
				t.Fatalf("%s imports %s", name, p)
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

// The rubric and the grader prompt are general quality criteria: they name no
// remedy, so the writer decides how to improve.
func TestRubricNamesNoRemedy(t *testing.T) {
	text := strings.ToLower(answerRubric + answerGraderSystem)
	for _, remedy := range []string{"memory", "search", "history", "web", "browse", "look up", "lookup", "tool", "internet"} {
		if strings.Contains(text, remedy) {
			t.Fatalf("the rubric or grader prompt names a remedy: %q", remedy)
		}
	}
}

func TestRevisionMessageCarriesTitlesAndNotesOnly(t *testing.T) {
	criteria := rubricCriteria(answerRubric)
	msg := answerRevision([]criterionResult{{ID: "C2", Result: "gap", Note: "Leaves the date out."}}, criteria)
	if !strings.Contains(msg, "Needs no follow-up: Leaves the date out.") || strings.Contains(msg, "C2") {
		t.Fatalf("revision = %q", msg)
	}
}

func TestGraderIsMeteredOncePerGrading(t *testing.T) {
	g := &gradeProvider{replies: []string{gradePass}}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{sayAnswerCall("c1", "Metered.")}})
	s, run := gateRunWith(t, model.engine(t), g, "")
	var n, in int
	if err := s.store.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(input_tokens),0) FROM usage_calls WHERE run_id=? AND kind=?`, run.ID, usageKindReview).Scan(&n, &in); err != nil {
		t.Fatal(err)
	}
	if n != 1 || in != 900 {
		t.Fatalf("metered rows=%d input=%d", n, in)
	}
}

func TestCodexOnlyWorkspaceGradesWithItsSmallestModel(t *testing.T) {
	if codexDefaultModel != "codex-gpt-5.6-luna" {
		t.Fatalf("codex small model = %s", codexDefaultModel)
	}
}
