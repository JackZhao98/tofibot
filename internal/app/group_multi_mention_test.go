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

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Under structured output every Bot a person message mentions gets a run of
// its own, in mention order; nobody coordinates, hands off or follows up.

func (f *dedupeFixture) send(t *testing.T, content string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content, "client_message_id": "multi-" + content})
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/"+f.group.ID+"/messages", bytes.NewReader(body))
	res := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func rootRuns(runs []Run) []Run {
	var out []Run
	for _, r := range runs {
		if r.Kind == "" && r.ParentRunID == "" {
			out = append(out, r)
		}
	}
	return out
}

func countMessages(t *testing.T, s *Store, conv, where string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND `+where, append([]any{conv}, args...)...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// recordingEngine answers each Bot's run with a fixed sentence and records what
// that Bot's context held when it started.
type recordingEngine struct {
	mu    sync.Mutex
	say   map[string]string // bot id -> reply
	seen  map[string]string // bot id -> context text
	order []string
}

func (e *recordingEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	text := ""
	for _, m := range req.Messages {
		text += m.Content + "\n"
	}
	e.mu.Lock()
	e.order = append(e.order, req.BotID)
	if e.seen == nil {
		e.seen = map[string]string{}
	}
	e.seen[req.BotID] = text
	out := e.say[req.BotID]
	e.mu.Unlock()
	return runtime.Result{Content: reply(ctx, req, out, nil)}, nil
}

func newMultiFixture(t *testing.T) (dedupeFixture, *recordingEngine) {
	e := &recordingEngine{}
	f := newDedupeFixture(t, e)
	e.say = map[string]string{f.tofi.ID: "Strength first, then the miles.", f.meta.ID: "Lift before you run, like laying a foundation."}
	return f, e
}

func TestEveryMentionedBotAnswersInOrderAndSeesTheEarlierReply(t *testing.T) {
	f, e := newMultiFixture(t)
	f.send(t, "@tofi @metaphor strength or running first? one sentence each")
	runs := f.settle(t)
	if len(runs) != 2 || len(rootRuns(runs)) != 2 {
		t.Fatalf("runs=%+v", runs)
	}
	if runs[0].BotID != f.tofi.ID || runs[1].BotID != f.meta.ID || runs[0].QueueSeq >= runs[1].QueueSeq {
		t.Fatalf("not in mention order: %+v", runs)
	}
	for _, r := range runs {
		if r.Status != "done" {
			t.Fatalf("run %+v", r)
		}
	}
	if len(e.order) != 2 || e.order[0] != f.tofi.ID || e.order[1] != f.meta.ID {
		t.Fatalf("execution order=%v", e.order)
	}
	if !strings.Contains(e.seen[f.meta.ID], "Strength first, then the miles.") {
		t.Fatalf("the second Bot did not see the first reply:\n%s", e.seen[f.meta.ID])
	}
	if strings.Contains(e.seen[f.tofi.ID], "Lift before you run") {
		t.Fatal("the first Bot saw a reply that did not exist yet")
	}
	if !strings.Contains(e.seen[f.meta.ID], "[co-addressed]") || !strings.Contains(e.seen[f.tofi.ID], "[co-addressed]") || strings.Contains(e.seen[f.tofi.ID], "[coordination]") {
		t.Fatal("each addressed Bot is told it is one of several; none is told to coordinate")
	}
	if n := countMessages(t, f.server.store, f.group.ID, `kind='notice'`); n != 0 {
		t.Fatalf("handoff notices = %d", n)
	}
	if countKind(runs, runKindGroupTask, "")+countKind(runs, runKindFollowup, "")+countKind(runs, runKindGroupMention, "") != 0 {
		t.Fatalf("delegation runs exist: %+v", runs)
	}
	if publishedBy(&f, f.tofi.ID) != 1 || publishedBy(&f, f.meta.ID) != 1 {
		t.Fatal("each Bot publishes exactly one reply")
	}
}

func TestMidSentenceMentionsCountLikeLeadingOnes(t *testing.T) {
	f, e := newMultiFixture(t)
	f.send(t, "what do you think, @metaphor and also @tofi?")
	runs := f.settle(t)
	if len(runs) != 2 || runs[0].BotID != f.meta.ID || runs[1].BotID != f.tofi.ID || len(e.order) != 2 {
		t.Fatalf("runs=%+v order=%v", runs, e.order)
	}
}

func TestOneMentionIsUnchanged(t *testing.T) {
	f, e := newMultiFixture(t)
	f.send(t, "@tofi strength or running first?")
	runs := f.settle(t)
	if len(runs) != 1 || runs[0].BotID != f.tofi.ID || runs[0].Status != "done" {
		t.Fatalf("runs=%+v", runs)
	}
	if strings.Contains(e.seen[f.tofi.ID], "[co-addressed]") {
		t.Fatal("a single mention is not a co-addressed message")
	}
}

func TestMultipleMentionsKeepTheCoordinatorWithTheFlagOff(t *testing.T) {
	f, _ := newMultiFixture(t)
	f.legacyMode(t)
	f.send(t, "@tofi @metaphor strength or running first?")
	runs := f.settle(t)
	if roots := rootRuns(runs); len(roots) != 1 || roots[0].BotID != f.tofi.ID {
		t.Fatalf("flag off: only the first-mentioned Bot starts: %+v", runs)
	}
}

func TestAddressedBotCannotBeHandedOffToOrWokenForTheSameMessage(t *testing.T) {
	f, _ := newMultiFixture(t)
	f.post(t, "@tofi @metaphor strength or running first?", f.tofi, f.meta)
	poster := f.roots[0]
	if _, err := f.server.store.SetRunStatus(poster.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	// Handoff: refused with the classified retryable outcome, nothing written.
	tool := findHandoffTool(t, f.server, f.group, poster)
	_, err := tool.Execute(context.Background(), handoffArgs(f.meta.ID, "give your angle"))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "recipient_already_addressed" || o.Status != tooloutcome.Transient || o.NextAction != "retry" || o.Certainty != "not_executed" {
		t.Fatalf("outcome=%+v ok=%v err=%v", o, ok, err)
	}
	var tasks, used int
	_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE kind=?`, runKindGroupTask).Scan(&tasks)
	_ = f.server.store.db.QueryRow(`SELECT handoff_count FROM runs WHERE id=?`, poster.ID).Scan(&used)
	if tasks != 0 || used != 0 {
		t.Fatalf("a refused handoff left tasks=%d handoff_count=%d", tasks, used)
	}
	// Wake: refused by the store even if a stale list reaches it.
	msg, err := f.server.store.AddAssistant(f.group.ID, f.tofi.ID, poster.ID, "my take")
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "done"} {
		if _, err := f.server.store.db.Exec(`UPDATE runs SET status=? WHERE id=?`, status, f.roots[1].ID); err != nil {
			t.Fatal(err)
		}
		if _, woke, err := f.server.store.AddMentionWake(f.group.ID, poster, msg.ID, f.meta.ID, "again"); err != nil || woke {
			t.Fatalf("addressed Bot (root %s) woke: %v %v", status, woke, err)
		}
	}
	// A Bot the person did not mention can still be woken and handed off to.
	if _, woke, err := f.server.store.AddMentionWake(f.group.ID, poster, msg.ID, f.third.ID, "please check"); err != nil || !woke {
		t.Fatalf("unmentioned Bot not woken: %v %v", woke, err)
	}
	if _, _, err := f.server.store.AddHandoff(f.group.ID, f.tofi.ID, f.third.ID, poster.ID, "extra help"); err != nil {
		t.Fatalf("unmentioned Bot handoff: %v", err)
	}
}

// The say call that wakes an addressed Bot is refused before anything is
// recorded; the Bot's next say, without the wake, publishes, and nothing loops.
func TestSayWakingAnAddressedBotIsRefusedThenThePlainAnswerPublishes(t *testing.T) {
	var f dedupeFixture
	var refusals []string
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID != f.tofi.ID {
			return reply(ctx, req, "metaphor: foundations first", nil)
		}
		_, err := callTool(ctx, req, "say", "wake-"+req.RunID, map[string]any{"purpose": "answer", "text": "Strength first.", "wake": wakeOf(f.meta.ID, "add your angle")})
		if err == nil {
			refusals = append(refusals, "accepted")
			return ""
		}
		o, ok := tooloutcome.FromError(err)
		if !ok {
			return "ERR unclassified: " + err.Error()
		}
		refusals = append(refusals, o.Code+"/"+o.Status+"/"+o.NextAction)
		return reply(ctx, req, "Strength first.", nil)
	}
	f.send(t, "@tofi @metaphor strength or running first?")
	runs := f.settle(t)
	if len(runs) != 2 || countKind(runs, runKindGroupMention, "") != 0 {
		t.Fatalf("runs=%+v", runs)
	}
	if len(refusals) != 1 || refusals[0] != "recipient_already_addressed/transient_failure/retry" {
		t.Fatalf("refusals=%v", refusals)
	}
	if publishedBy(&f, f.tofi.ID) != 1 || publishedBy(&f, f.meta.ID) != 1 {
		t.Fatal("both Bots publish their own answer once")
	}
	if n := countMessages(t, f.server.store, f.group.ID, `kind='notice'`); n != 0 {
		t.Fatalf("notices = %d", n)
	}
}

// A restart between the first and second run: the second run, already queued
// by the one ingress transaction, runs once and reads the first reply.
func TestRestartBetweenTheTwoRunsRunsTheSecondExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.CreateBot("tofi", "", "model")
	b, _ := store.CreateBot("metaphor", "", "model")
	g, _ := store.CreateGroup("duo", []string{a.ID, b.ID})
	_, runs, _, err := store.AddUserRuns(g.ID, "@tofi @metaphor strength or running first?", "restart-1", []runSpec{{BotID: a.ID, Model: a.Model}, {BotID: b.ID, Model: b.Model}}, true)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if _, err := store.AddAssistant(g.ID, a.ID, runs[0].ID, "Strength first, then the miles."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRunStatus(runs[0].ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	e := &recordingEngine{say: map[string]string{b.ID: "Lift before you run."}}
	s, err := NewServer(Config{DataDir: dir, Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	f := dedupeFixture{server: s, group: g, tofi: a, meta: b}
	got := f.settle(t)
	s.Close()
	// A second restart must not run it again.
	s2, err := NewServer(Config{DataDir: dir, Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	f.server = s2
	got = f.settle(t)
	if len(got) != 2 || got[1].Status != "done" || len(e.order) != 1 || e.order[0] != b.ID {
		t.Fatalf("runs=%+v executed=%v", got, e.order)
	}
	if !strings.Contains(e.seen[b.ID], "Strength first, then the miles.") {
		t.Fatal("the second run did not read the first reply")
	}
	if publishedBy(&f, b.ID) != 1 {
		t.Fatal("the second Bot published other than once")
	}
}
