package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type dedupeFixture struct {
	server            *Server
	group             Conversation
	tofi, meta, third Bot
	trigger           Message
	roots             []Run
}

func newDedupeFixture(t *testing.T, engine runtime.Engine) dedupeFixture {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	tofi, _ := store.CreateBot("tofi", "", "model")
	meta, _ := store.CreateBot("metaphor", "", "model")
	third, _ := store.CreateBot("third", "", "model")
	group, _ := store.CreateGroup("trio", []string{tofi.ID, meta.ID, third.ID})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{DataDir: dir, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	group, _ = server.store.GetConversation(group.ID)
	return dedupeFixture{server: server, group: group, tofi: tofi, meta: meta, third: third}
}

// legacyMode selects the coordinator design (structured output off), where only
// the first-mentioned Bot of a multi-mention message starts a root run. The
// tests that build several roots by hand pin that mode's delegation rules.
func (f *dedupeFixture) legacyMode(t *testing.T) {
	t.Helper()
	if err := f.server.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
}

func (f *dedupeFixture) post(t *testing.T, content string, bots ...Bot) {
	t.Helper()
	specs := make([]runSpec, 0, len(bots))
	for _, b := range bots {
		specs = append(specs, runSpec{BotID: b.ID, Model: b.Model})
	}
	m, runs, _, err := f.server.store.AddUserRuns(f.group.ID, content, "client-"+content, specs, true)
	if err != nil {
		t.Fatal(err)
	}
	f.trigger, f.roots = m, runs
}

func handoffArgs(bot, task string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"bot_id": bot, "task": task})
	return b
}

func TestDelegationRefusedForBotMentionedInSameMessage(t *testing.T) {
	f := newDedupeFixture(t, nil)
	f.legacyMode(t)
	f.post(t, "@tofi write the page. @metaphor give tofi a sentence.", f.tofi, f.meta)
	tofiRoot, metaRoot := f.roots[0], f.roots[1]
	if _, err := f.server.store.SetRunStatus(tofiRoot.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool := findHandoffTool(t, f.server, f.group, tofiRoot)
	_, err := tool.Execute(context.Background(), handoffArgs(f.meta.ID, "give me a sentence"))
	if err == nil {
		t.Fatal("delegation to a mentioned Bot with an unfinished root must be refused")
	}
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "bot_already_handling_message" || o.Certainty != "not_executed" {
		t.Fatalf("outcome=%+v ok=%v err=%v", o, ok, err)
	}
	var tasks, handoffs int
	_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE kind=?`, runKindGroupTask).Scan(&tasks)
	_ = f.server.store.db.QueryRow(`SELECT handoff_count FROM runs WHERE id=?`, tofiRoot.ID).Scan(&handoffs)
	if tasks != 0 || handoffs != 0 {
		t.Fatalf("refused delegation left state: tasks=%d handoffs=%d", tasks, handoffs)
	}
	_ = metaRoot
}

func TestDelegationAllowsFinishedMentionedBotOnceAndUnmentionedBot(t *testing.T) {
	f := newDedupeFixture(t, nil)
	f.legacyMode(t)
	third := f.third
	f.post(t, "@tofi do part one. @metaphor do part two.", f.tofi, f.meta)
	tofiRoot, metaRoot := f.roots[0], f.roots[1]
	if _, err := f.server.store.SetRunStatus(tofiRoot.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.store.SetRunStatus(metaRoot.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	// A Bot the user did not mention can still be brought in.
	if _, _, err := f.server.store.AddHandoff(f.group.ID, f.meta.ID, third.ID, metaRoot.ID, "extra help"); err != nil {
		t.Fatalf("unmentioned Bot delegation: %v", err)
	}
	// Reset the one-shot handoff budget of this run for the next assertion.
	if _, err := f.server.store.db.Exec(`UPDATE runs SET handoff_count=0 WHERE id=?`, metaRoot.ID); err != nil {
		t.Fatal(err)
	}
	// A finished mentioned Bot receives one handover, then no more.
	if _, _, err := f.server.store.AddHandoff(f.group.ID, f.meta.ID, f.tofi.ID, metaRoot.ID, "handover: result"); err != nil {
		t.Fatalf("first handover: %v", err)
	}
	if _, err := f.server.store.db.Exec(`UPDATE runs SET handoff_count=0 WHERE id=?`, metaRoot.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.server.store.AddHandoff(f.group.ID, f.meta.ID, f.tofi.ID, metaRoot.ID, "again"); err == nil {
		t.Fatal("second delegation to the same mentioned Bot must be refused")
	}
}

// Incident shape: both Bots are mentioned and both try to delegate to each
// other. The page must be written exactly once and only one handover occurs.
type incidentEngine struct {
	tofi, meta string
	mu         sync.Mutex
	pageWrites int
	refusals   []string
	handovers  int
}

func (e *incidentEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	has := func(marker string) bool {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, marker) {
				return true
			}
		}
		return false
	}
	call := func(bot, task string) error {
		for _, tool := range req.Tools {
			if tool.Name == "handoff" {
				_, err := tool.Execute(ctx, handoffArgs(bot, task))
				return err
			}
		}
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if req.BotID == e.tofi {
		if has("[current handoff task]") {
			e.pageWrites++
			return runtime.Result{Content: "page written with the handed-over sentence"}, nil
		}
		if has("[completed colleague task]") {
			e.pageWrites++
			return runtime.Result{Content: "page written"}, nil
		}
		if err := call(e.meta, "give me a metaphor"); err != nil {
			e.refusals = append(e.refusals, err.Error())
		}
		return runtime.Result{Content: "I will write the page once the metaphor arrives."}, nil
	}
	if has("[completed colleague task]") {
		return runtime.Result{Content: "thanks, done"}, nil
	}
	if err := call(e.tofi, "metaphor ready: magnifying glass. write the page"); err != nil {
		e.refusals = append(e.refusals, err.Error())
	} else {
		e.handovers++
	}
	return runtime.Result{}, nil
}

func TestIncidentTwoMentionedBotsDelegatingToEachOther(t *testing.T) {
	engine := &incidentEngine{}
	f := newDedupeFixture(t, engine)
	f.legacyMode(t)
	engine.tofi, engine.meta = f.tofi.ID, f.meta.ID
	f.post(t, "@tofi create a page with both intros. @metaphor give tofi a one-line metaphor.", f.tofi, f.meta)
	f.server.mu.Lock()
	f.server.engine = engine
	f.server.mu.Unlock()
	f.server.enqueue(f.group, f.roots[0])
	deadline := time.Now().Add(10 * time.Second)
	for {
		runs, _ := f.server.store.Runs(f.group.ID)
		doneCount := 0
		for _, r := range runs {
			if r.Status == "done" {
				doneCount++
			}
		}
		if len(runs) == 4 && doneCount == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runs did not settle: %+v", runs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	runs, _ := f.server.store.Runs(f.group.ID)
	var tasksToTofi, tasksToMeta int
	for _, r := range runs {
		if r.Kind == runKindGroupTask && r.BotID == f.tofi.ID {
			tasksToTofi++
		}
		if r.Kind == runKindGroupTask && r.BotID == f.meta.ID {
			tasksToMeta++
		}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.pageWrites != 1 || tasksToTofi != 1 || tasksToMeta != 0 || engine.handovers != 1 {
		t.Fatalf("pageWrites=%d tasksToTofi=%d tasksToMeta=%d handovers=%d refusals=%v", engine.pageWrites, tasksToTofi, tasksToMeta, engine.handovers, engine.refusals)
	}
	if len(engine.refusals) != 1 || !strings.Contains(engine.refusals[0], "already working on its own part") {
		t.Fatalf("refusals=%v", engine.refusals)
	}
}

// Timing case: the metaphor Bot finishes while tofi's root run is still
// running. The handover is delivered into tofi's running turn through the
// steering edge, so the page is written once, with the sentence.
type timingEngine struct {
	t          *testing.T
	f          *dedupeFixture
	metaRoot   Run
	mu         sync.Mutex
	pageWrites int
	outcome    string
	taskText   string
}

func (e *timingEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	text := ""
	for _, m := range req.Messages {
		text += m.Content + "\n"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if req.BotID == e.f.tofi.ID {
		if strings.Contains(text, "[current handoff task]") {
			e.pageWrites++
			e.taskText = text
			return runtime.Result{Content: "page written with the sentence"}, nil
		}
		// Tofi's root turn is mid-run when the metaphor Bot hands over.
		if _, err := e.f.server.store.SetRunStatus(e.metaRoot.ID, "running", ""); err != nil {
			return runtime.Result{}, err
		}
		tool := findHandoffTool(e.t, e.f.server, e.f.group, e.metaRoot)
		out, err := tool.Execute(ctx, handoffArgs(e.f.tofi.ID, "metaphor: a magnifying glass. Write the page with it."))
		if err != nil {
			return runtime.Result{}, err
		}
		e.outcome = out
		if _, done, ferr := e.f.server.store.FinishRun(e.metaRoot.ID, e.f.group.ID, e.f.meta.ID, ""); ferr != nil || !done {
			e.t.Errorf("finish meta root: %v %v", done, ferr)
		}
		if req.BeforeModelCall == nil {
			e.t.Error("engine request has no steering boundary")
			return runtime.Result{}, nil
		}
		return runtime.Result{}, req.BeforeModelCall()
	}
	return runtime.Result{Content: "noted, thanks"}, nil
}

func TestHandoverIsSteeredIntoActiveMentionedRun(t *testing.T) {
	engine := &timingEngine{t: t}
	f := newDedupeFixture(t, engine)
	f.legacyMode(t)
	engine.f = &f
	f.post(t, "@tofi create a page with the metaphor. @metaphor give tofi a one-line metaphor.", f.tofi, f.meta)
	engine.metaRoot = f.roots[1]
	f.server.mu.Lock()
	f.server.engine = engine
	f.server.mu.Unlock()
	f.server.enqueue(f.group, f.roots[0])
	waitForRunStatus(t, f.server, f.roots[0].ID, "cancelled")
	runs, _ := f.server.store.Runs(f.group.ID)
	var task, followup Run
	for _, r := range runs {
		switch r.Kind {
		case runKindGroupTask:
			task = r
		case runKindFollowup:
			followup = r
		}
	}
	if task.ID == "" {
		t.Fatalf("no handover task: %+v", runs)
	}
	waitForRunStatus(t, f.server, task.ID, "done")
	deadline := time.Now().Add(5 * time.Second)
	for followup.ID == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		runs, _ = f.server.store.Runs(f.group.ID)
		for _, r := range runs {
			if r.Kind == runKindFollowup {
				followup = r
			}
		}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !strings.HasPrefix(engine.outcome, "delivered to tofi's current turn") {
		t.Fatalf("delegating Bot outcome=%q", engine.outcome)
	}
	if engine.pageWrites != 1 || !strings.Contains(engine.taskText, "magnifying glass") || !strings.Contains(engine.taskText, "not from the user") {
		t.Fatalf("pageWrites=%d task=%q", engine.pageWrites, engine.taskText)
	}
	if task.BotID != f.tofi.ID || followup.BotID != f.meta.ID {
		t.Fatalf("task=%+v followup=%+v", task, followup)
	}
	// The handover is a Bot notice without host user-ingress provenance, so
	// it can never be authorization evidence for MCP review.
	var ingress int
	_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM user_message_ingress WHERE message_id=?`, task.TriggerMessageID).Scan(&ingress)
	handover, err := f.server.store.GetMessage(task.TriggerMessageID)
	if err != nil || ingress != 0 || handover.Role == "user" {
		t.Fatalf("handover message=%+v ingress=%d err=%v", handover, ingress, err)
	}
	evidence := mcpAuthorizationSources(mcpReviewContext{
		Messages:          []Message{f.trigger, handover},
		MessageProvenance: []mcpMessageProvenance{{MessageID: f.trigger.ID, Source: mcpHostUserIngress}, {MessageID: handover.ID, Source: "unknown"}},
	})
	if len(evidence) != 1 || evidence[0].MessageID != f.trigger.ID {
		t.Fatalf("authorization evidence must be the user message only: %+v", evidence)
	}
}
