package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// coordEngine scripts the coordinator pattern: the first-mentioned Bot hands
// each helper its part, helpers return a result, the coordinator writes once.
type coordEngine struct {
	t       *testing.T
	s       *Server
	group   Conversation
	lead    string
	helpers []string // in mention order
	mu      sync.Mutex

	contexts   []string // coordination blocks the lead saw, in order
	pageWrites []string
	reviewErrs []string
	reviewed   map[string]string // run kind -> authorization message id
	helperText map[string]string // helper bot -> assignment text seen
}

func (e *coordEngine) allText(req runtime.Request) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Content + "\n")
	}
	return b.String()
}

func (e *coordEngine) review(req runtime.Request) {
	run, err := e.s.store.GetRun(req.RunID)
	if err != nil {
		e.reviewErrs = append(e.reviewErrs, err.Error())
		return
	}
	x, _, err := e.s.readMCPReviewContextFor(e.s.store.db, e.group, run, mcpProposalEffect)
	if err != nil {
		e.reviewErrs = append(e.reviewErrs, run.Kind+": "+err.Error()+" "+string(contextFailureCode(err)))
		return
	}
	sources := mcpAuthorizationSources(x)
	if len(sources) != 1 {
		e.reviewErrs = append(e.reviewErrs, run.Kind+": authorization sources != 1")
		return
	}
	e.reviewed[run.Kind+"/"+run.BotID] = sources[0].MessageID
}

func (e *coordEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	text := e.allText(req)
	e.mu.Lock()
	defer e.mu.Unlock()
	if req.BotID != e.lead {
		// Helper: a group_task run; its MCP write would review against the user message.
		e.review(req)
		e.helperText[req.BotID] = text
		return runtime.Result{Content: "metaphor from " + req.BotID + ": a lighthouse"}, nil
	}
	block := ""
	if i := strings.Index(text, "[coordination]"); i >= 0 {
		block = text[i:]
		block = block[:strings.Index(block, "[/coordination]")]
	}
	e.contexts = append(e.contexts, block)
	if strings.Contains(block, "Every Bot the user mentioned has returned") {
		e.review(req)
		e.pageWrites = append(e.pageWrites, text)
		return runtime.Result{Content: "page written"}, nil
	}
	next := ""
	for _, h := range e.helpers {
		if strings.Contains(block, "id="+h+")") {
			next = h
			break
		}
	}
	if next == "" {
		return runtime.Result{Content: "no coordination"}, nil
	}
	for _, tool := range req.Tools {
		if tool.Name == "handoff" {
			if _, err := tool.Execute(ctx, handoffArgs(next, "do your part for the user's request")); err != nil {
				e.reviewErrs = append(e.reviewErrs, "handoff: "+err.Error())
			}
		}
	}
	return runtime.Result{}, nil
}

type coordFixture struct {
	server *Server
	group  Conversation
	bots   []Bot
	engine *coordEngine
}

func newCoordFixture(t *testing.T, names ...string) *coordFixture {
	t.Helper()
	engine := &coordEngine{t: t, reviewed: map[string]string{}, helperText: map[string]string{}}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.accountID = "synthetic-account"
	f := &coordFixture{server: s, engine: engine}
	var ids []string
	for _, n := range names {
		b, err := s.store.CreateBot(n, "", "model")
		if err != nil {
			t.Fatal(err)
		}
		f.bots = append(f.bots, b)
		ids = append(ids, b.ID)
	}
	g, err := s.store.CreateGroup("desk", ids)
	if err != nil {
		t.Fatal(err)
	}
	f.group, _ = s.store.GetConversation(g.ID)
	engine.s, engine.group, engine.lead = s, f.group, f.bots[0].ID
	for _, b := range f.bots[1:] {
		engine.helpers = append(engine.helpers, b.ID)
	}
	return f
}

func (f *coordFixture) send(t *testing.T, content string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content, "client_message_id": "coord-" + content})
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+f.group.ID+"/messages", bytes.NewReader(body))
	res := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func (f *coordFixture) settle(t *testing.T, want int) []Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runs, _ := f.server.store.Runs(f.group.ID)
		done := 0
		for _, r := range runs {
			if r.Status == "done" {
				done++
			}
		}
		if len(runs) == want && done == want {
			time.Sleep(100 * time.Millisecond) // nothing else may appear
			again, _ := f.server.store.Runs(f.group.ID)
			if len(again) != want {
				t.Fatalf("extra runs appeared: %+v", again)
			}
			return again
		}
		if time.Now().After(deadline) {
			t.Fatalf("runs did not settle at %d: %+v", want, runs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func kinds(runs []Run) map[string]int {
	out := map[string]int{}
	for _, r := range runs {
		out[r.Kind+"/"+r.BotID]++
	}
	return out
}

const productionPrompt = "@新闻官 pick today's top AI story. @比喻君 write a metaphor for 新闻官's pick. Finally 新闻官 writes both to the Notion page"

func TestMultiMentionStartsOnlyCoordinatorAndChainsHelper(t *testing.T) {
	f := newCoordFixture(t, "新闻官", "比喻君", "第三者")
	f.send(t, productionPrompt)
	runs := f.settle(t, 3) // root, helper group_task, followup
	k := kinds(runs)
	news, meta := f.bots[0].ID, f.bots[1].ID
	if k["/"+news] != 1 || k[runKindGroupTask+"/"+meta] != 1 || k[runKindFollowup+"/"+news] != 1 || k["/"+meta] != 0 {
		t.Fatalf("runs=%v", k)
	}
	e := f.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pageWrites) != 1 || !strings.Contains(e.pageWrites[0], "metaphor from "+meta) {
		t.Fatalf("page writes=%d", len(e.pageWrites))
	}
	if !strings.Contains(e.contexts[0], "You coordinate") || !strings.Contains(e.contexts[0], "比喻君") || !strings.Contains(e.contexts[0], "write a metaphor for 新闻官's pick.") || strings.Contains(e.contexts[0], "第三者") {
		t.Fatalf("root context=%q", e.contexts[0])
	}
	if len(e.reviewErrs) != 0 {
		t.Fatalf("review errors: %v", e.reviewErrs)
	}
	var trigger string
	for _, r := range runs {
		if r.Kind == "" {
			trigger = r.TriggerMessageID
		}
	}
	for _, key := range []string{runKindGroupTask + "/" + meta, runKindFollowup + "/" + news} {
		if e.reviewed[key] != trigger {
			t.Fatalf("%s reviewed against %q, want the user message %q (all=%v)", key, e.reviewed[key], trigger, e.reviewed)
		}
	}
	for _, r := range runs {
		if r.Kind == runKindGroupTask || r.Kind == runKindFollowup {
			u, _ := f.server.store.GetMessage(r.TriggerMessageID)
			if u.Role == "user" {
				t.Fatalf("delegated run triggered by a user message: %+v", r)
			}
		}
	}
}

func TestThreeMentionedBotsChain(t *testing.T) {
	f := newCoordFixture(t, "新闻官", "比喻君", "校对君", "旁观者")
	f.send(t, "@新闻官 pick a story. @比喻君 write a metaphor. @校对君 proofread both. Finally 新闻官 publishes.")
	runs := f.settle(t, 5) // root, task, followup, task, followup
	k := kinds(runs)
	news, meta, proof := f.bots[0].ID, f.bots[1].ID, f.bots[2].ID
	if k["/"+news] != 1 || k[runKindGroupTask+"/"+meta] != 1 || k[runKindGroupTask+"/"+proof] != 1 || k[runKindFollowup+"/"+news] != 2 {
		t.Fatalf("runs=%v", k)
	}
	e := f.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.contexts) != 3 {
		t.Fatalf("lead contexts=%d", len(e.contexts))
	}
	if !strings.Contains(e.contexts[0], "比喻君") || !strings.Contains(e.contexts[0], "校对君") {
		t.Fatalf("root context=%q", e.contexts[0])
	}
	if strings.Contains(e.contexts[1], "比喻君") || !strings.Contains(e.contexts[1], "校对君") || !strings.Contains(e.contexts[1], "still open") {
		t.Fatalf("first followup context=%q", e.contexts[1])
	}
	if !strings.Contains(e.contexts[2], "Every Bot the user mentioned has returned") {
		t.Fatalf("last followup context=%q", e.contexts[2])
	}
	if len(e.pageWrites) != 1 || !strings.Contains(e.pageWrites[0], "metaphor from "+proof) {
		t.Fatalf("page writes=%v", len(e.pageWrites))
	}
	if len(e.reviewErrs) != 0 {
		t.Fatalf("review errors: %v", e.reviewErrs)
	}
}

func TestSingleAndNoMentionUnchanged(t *testing.T) {
	f := newCoordFixture(t, "新闻官", "比喻君")
	f.send(t, "@新闻官 hello")
	runs := f.settle(t, 1)
	if runs[0].Kind != "" || runs[0].BotID != f.bots[0].ID {
		t.Fatalf("single mention run=%+v", runs[0])
	}
	if got := f.server.coordinationContext(f.group, runs[0], f.bots[0]); got != "" {
		t.Fatalf("single mention gets no coordination block: %q", got)
	}
	g := newCoordFixture(t, "新闻官", "比喻君")
	g.send(t, "anyone around?")
	runs = g.settle(t, 1)
	if runs[0].Kind != runKindGroupChat {
		t.Fatalf("no-mention run=%+v", runs[0])
	}
	if got := g.server.coordinationContext(g.group, runs[0], g.bots[0]); got != "" {
		t.Fatalf("no mention gets no coordination block: %q", got)
	}
}

// A message from before this routing, whose mentioned Bots already hold their
// own root runs, must not tell the Bots to coordinate through handoff.
func TestCoordinationContextSkipsLegacyParallelRoots(t *testing.T) {
	f := newDedupeFixture(t, nil)
	f.post(t, "@tofi create the page. @metaphor give tofi a metaphor.", f.tofi, f.meta)
	if got := f.server.coordinationContext(f.group, f.roots[0], f.tofi); got != "" {
		t.Fatalf("legacy parallel roots got a coordination block: %q", got)
	}
}
