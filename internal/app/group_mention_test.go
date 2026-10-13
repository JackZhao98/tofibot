package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type mentionEngine struct {
	mu    sync.Mutex
	reply func(ctx context.Context, req runtime.Request, text string) string
}

func (e *mentionEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	text := ""
	for _, m := range req.Messages {
		text += m.Content + "\n"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.reply(ctx, req, text)
	if err := ctx.Err(); err != nil {
		return runtime.Result{}, err
	}
	return runtime.Result{Content: out}, nil
}

// respond calls the respond tool the way a model would.
func respond(ctx context.Context, req runtime.Request, args map[string]any) error {
	data, _ := json.Marshal(args)
	for _, tool := range req.Tools {
		if tool.Name == "respond" {
			_, err := tool.Execute(ctx, data)
			return err
		}
	}
	return fmt.Errorf("no respond tool")
}

func wakeOf(bot, request string) []map[string]string {
	return []map[string]string{{"bot_id": bot, "request": request}}
}

func (f *dedupeFixture) start(engine runtime.Engine) {
	f.server.mu.Lock()
	f.server.engine = engine
	f.server.mu.Unlock()
	f.server.enqueue(f.group, f.roots[0])
}

// settle waits until every run is terminal and the run set stops growing.
func (f *dedupeFixture) settle(t *testing.T) []Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	last, stable := -1, 0
	for time.Now().Before(deadline) {
		runs, _ := f.server.store.Runs(f.group.ID)
		active := false
		for _, r := range runs {
			if r.Status == "queued" || r.Status == "running" || r.Status == "waiting" {
				active = true
			}
		}
		if !active && len(runs) == last {
			stable++
			if stable >= 15 {
				return runs
			}
		} else {
			stable = 0
		}
		last = len(runs)
		time.Sleep(20 * time.Millisecond)
	}
	runs, _ := f.server.store.Runs(f.group.ID)
	t.Fatalf("runs did not settle: %+v", runs)
	return nil
}

func countKind(runs []Run, kind, bot string) int {
	n := 0
	for _, r := range runs {
		if r.Kind == kind && (bot == "" || r.BotID == bot) {
			n++
		}
	}
	return n
}

func publishedBy(f *dedupeFixture, bot string) int {
	var n int
	_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND sender_bot_id=? AND role='assistant' AND kind=''`, f.group.ID, bot).Scan(&n)
	return n
}

// The production deadlock. The metaphor Bot ran first and registered a
// follow-up instead of posting "I'll wait". The news Bot then picks the story
// and wakes it with an explicit request; it answers with the metaphor and wakes
// the news Bot with the handover; the page is written once and nothing loops.
func TestDeadlockResolvedByExplicitWakes(t *testing.T) {
	var f dedupeFixture
	pageWrites := 0
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.meta.ID {
			if strings.Contains(text, "[group mention]") {
				if err := respond(ctx, req, map[string]any{"disposition": "reply", "wake": wakeOf(f.tofi.ID, "write the story and this metaphor to the page")}); err != nil {
					return "ERR " + err.Error()
				}
				return "metaphor: a lighthouse"
			}
			_ = respond(ctx, req, map[string]any{"disposition": "follow_up", "wait_for": f.tofi.ID})
			return ""
		}
		if strings.Contains(text, "[group mention]") && strings.Contains(text, "this metaphor") {
			pageWrites++
			_ = respond(ctx, req, map[string]any{"disposition": "reply"})
			return "written"
		}
		_ = respond(ctx, req, map[string]any{"disposition": "reply", "wake": wakeOf(f.meta.ID, "send me your metaphor for the story")})
		return "Story picked: lighthouses."
	}
	f.post(t, "@metaphor write a metaphor for the story tofi picks. @tofi pick the story, then write both to the page.", f.meta, f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if pageWrites != 1 || countKind(runs, runKindGroupMention, f.meta.ID) != 1 || countKind(runs, runKindGroupMention, f.tofi.ID) != 1 || len(runs) != 4 {
		t.Fatalf("pageWrites=%d runs=%+v", pageWrites, runs)
	}
	for _, r := range runs {
		if r.Status != "done" {
			t.Fatalf("run not done: %+v", r)
		}
	}
}

// Production ordering: the news Bot's wake finds the metaphor Bot still queued;
// no duplicate run is created and the queued Bot sees the message on start.
func TestQueuedTargetNotWokenSeesMessage(t *testing.T) {
	var f dedupeFixture
	pageWrites := 0
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.meta.ID {
			if strings.Contains(text, "[colleague messages addressed to you]") && strings.Contains(text, "send me your metaphor") {
				_ = respond(ctx, req, map[string]any{"disposition": "reply", "wake": wakeOf(f.tofi.ID, "write the page with this metaphor")})
				return "metaphor for tofi: a lighthouse"
			}
			return "no idea what to do yet"
		}
		if strings.Contains(text, "[group mention]") {
			pageWrites++
			return "written"
		}
		_ = respond(ctx, req, map[string]any{"disposition": "reply", "wake": wakeOf(f.meta.ID, "send me your metaphor")})
		return "Story picked. @metaphor send me your metaphor"
	}
	f.post(t, "@tofi pick the story and write both to the page. @metaphor write a metaphor for it.", f.tofi, f.meta)
	f.start(engine)
	runs := f.settle(t)
	if pageWrites != 1 || countKind(runs, runKindGroupMention, f.meta.ID) != 0 || countKind(runs, runKindGroupMention, f.tofi.ID) != 1 || len(runs) != 3 {
		t.Fatalf("pageWrites=%d runs=%+v", pageWrites, runs)
	}
}

// A plain @mention in text, a thanks, and an acknowledgement never start runs.
func TestAcknowledgementsAndPlainMentionsWakeNobody(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		_ = respond(ctx, req, map[string]any{"disposition": "reply"})
		return "thanks @metaphor, done @third"
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(runs) != 1 {
		t.Fatalf("runs=%+v", runs)
	}
}

// A wake whose message is only a closing: the woken Bot concludes silently.
func TestWokenBotConcludesWithoutAnswering(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.meta.ID {
			if err := respond(ctx, req, map[string]any{"disposition": "concluded"}); err != nil {
				return "ERR " + err.Error()
			}
			return "you are welcome!"
		}
		_ = respond(ctx, req, map[string]any{"disposition": "reply", "wake": wakeOf(f.meta.ID, "FYI: done, written to the page")})
		return "Page written."
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(runs) != 2 || countKind(runs, runKindGroupMention, f.meta.ID) != 1 || publishedBy(&f, f.meta.ID) != 0 {
		t.Fatalf("runs=%+v metaPosts=%d", runs, publishedBy(&f, f.meta.ID))
	}
}

// follow_up: A waits for B, B posts later (without waking anyone): A is woken
// exactly once. The third Bot is a silent bystander and never runs.
func TestFollowUpWakesWaiterOnceAndBystanderStaysOut(t *testing.T) {
	var f dedupeFixture
	pageWrites := 0
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		switch req.BotID {
		case f.tofi.ID:
			if strings.Contains(text, "[group mention]") {
				pageWrites++
				return "written"
			}
			_ = respond(ctx, req, map[string]any{"disposition": "follow_up", "wait_for": f.meta.ID})
			return ""
		case f.meta.ID:
			return "metaphor: a lighthouse"
		}
		return "bystander ran"
	}
	f.post(t, "@tofi write the page with the metaphor. @metaphor give the metaphor.", f.tofi, f.meta)
	f.start(engine)
	runs := f.settle(t)
	if pageWrites != 1 || countKind(runs, runKindGroupMention, f.tofi.ID) != 1 || len(runs) != 3 {
		t.Fatalf("pageWrites=%d runs=%+v", pageWrites, runs)
	}
	for _, r := range runs {
		if r.BotID == f.third.ID {
			t.Fatalf("bystander ran: %+v", r)
		}
	}
}

func TestFollowUpTimeoutWakesWaiter(t *testing.T) {
	var f dedupeFixture
	timedOut := 0
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	f.server.waitTimeout = 100 * time.Millisecond
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if strings.Contains(text, "timed out") {
			timedOut++
			return "still missing the metaphor"
		}
		_ = respond(ctx, req, map[string]any{"disposition": "follow_up", "wait_for": f.meta.ID})
		return ""
	}
	f.post(t, "@tofi wait for the metaphor", f.tofi)
	f.start(engine)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		engine.mu.Lock()
		n := timedOut
		engine.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	runs := f.settle(t)
	if timedOut != 1 || len(runs) != 2 {
		t.Fatalf("timedOut=%d runs=%+v", timedOut, runs)
	}
}

func TestRespondRejectsBadChoices(t *testing.T) {
	var f dedupeFixture
	var errs []string
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		for _, args := range []map[string]any{
			{"disposition": "reply", "wake": wakeOf(f.tofi.ID, "to myself")},
			{"disposition": "silent"},
			{"disposition": "follow_up", "wait_for": f.tofi.ID},
			{"disposition": "reply", "wake": wakeOf(f.meta.ID, "  ")},
		} {
			if err := respond(ctx, req, args); err == nil {
				errs = append(errs, "accepted "+fmt.Sprint(args))
			}
		}
		return "ok"
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	f.settle(t)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
}

// Adversarial scripted loop: the cap trips and is logged as a bug signal.
func TestWakeCapTripsAndIsLogged(t *testing.T) {
	var buf bytes.Buffer
	var logMu sync.Mutex
	log.SetOutput(&lockedWriter{&logMu, &buf})
	defer log.SetOutput(os.Stderr)
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		other := f.meta.ID
		if req.BotID == f.meta.ID {
			other = f.tofi.ID
		}
		_ = respond(ctx, req, map[string]any{"disposition": "reply", "wake": wakeOf(other, "your turn, again")})
		return "ping"
	}
	f.post(t, "@tofi start", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if n := countKind(runs, runKindGroupMention, ""); n != maxMentionWakesPerLineage || len(runs) != maxMentionWakesPerLineage+1 {
		t.Fatalf("wakes=%d runs=%d", n, len(runs))
	}
	logMu.Lock()
	defer logMu.Unlock()
	if !strings.Contains(buf.String(), "cap reached") {
		t.Fatalf("cap trip not logged: %q", buf.String())
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func TestAddMentionWakeBusyAndIdempotent(t *testing.T) {
	f := newDedupeFixture(t, nil)
	f.post(t, "@tofi a @metaphor b", f.tofi, f.meta)
	poster := f.roots[0]
	msg, err := f.server.store.AddAssistant(f.group.ID, f.tofi.ID, poster.ID, "please")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.server.store.AddMentionWake(f.group.ID, poster, msg.ID, f.meta.ID, "x"); err != nil || ok {
		t.Fatalf("queued target woke: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := f.server.store.AddMentionWake(f.group.ID, poster, msg.ID, f.tofi.ID, "x"); ok {
		t.Fatal("self wake")
	}
	_, _ = f.server.store.SetRunStatus(f.roots[1].ID, "done", "")
	wake, ok, err := f.server.store.AddMentionWake(f.group.ID, poster, msg.ID, f.meta.ID, "x")
	if err != nil || !ok {
		t.Fatalf("finished target not woken: ok=%v err=%v", ok, err)
	}
	msg2, _ := f.server.store.AddAssistant(f.group.ID, f.tofi.ID, poster.ID, "again")
	if _, ok, _ := f.server.store.AddMentionWake(f.group.ID, poster, msg2.ID, f.meta.ID, "y"); ok {
		t.Fatal("queued wake must absorb a second request")
	}
	_, _ = f.server.store.SetRunStatus(wake.ID, "done", "")
	if _, ok, _ := f.server.store.AddMentionWake(f.group.ID, poster, msg.ID, f.meta.ID, "x"); ok {
		t.Fatal("same message woke the same Bot twice")
	}
	if _, ok, _ := f.server.store.AddMentionWake(f.group.ID, poster, msg2.ID, f.meta.ID, "y"); !ok {
		t.Fatal("new request should wake a finished Bot")
	}
}

// restartWithWait builds a data dir holding a finished run that registered a
// follow_up with the given deadline, then starts a fresh server on it.
func restartWithWait(t *testing.T, deadline time.Time) (*Server, Conversation, Bot) {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.CreateBot("tofi", "", "model")
	b, _ := store.CreateBot("metaphor", "", "model")
	g, _ := store.CreateGroup("duo", []string{a.ID, b.ID})
	_, runs, _, err := store.AddUserRuns(g.ID, "@tofi wait for metaphor", "client-1", []runSpec{{BotID: a.ID, Model: a.Model}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRunStatus(runs[0].ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,status,deadline_at,created_at) VALUES(?,?,?,?,?,'pending',?,?)`, runs[0].ID, g.ID, a.ID, b.ID, runs[0].TriggerMessageID, deadline.UTC().Format(time.RFC3339Nano), now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	engine := &mentionEngine{reply: func(context.Context, runtime.Request, string) string { return "ok" }}
	server, err := NewServer(Config{DataDir: dir, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	g, _ = server.store.GetConversation(g.ID)
	return server, g, a
}

func wakeCount(s *Server, conv string) int {
	runs, _ := s.store.Runs(conv)
	return countKind(runs, runKindGroupMention, "")
}

func TestRestartFiresOverdueWaitOnce(t *testing.T) {
	s, g, _ := restartWithWait(t, time.Now().Add(-time.Minute))
	deadline := time.Now().Add(5 * time.Second)
	for wakeCount(s, g.ID) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	s.armPendingWaits() // double start
	s.armPendingWaits()
	time.Sleep(300 * time.Millisecond)
	if n := wakeCount(s, g.ID); n != 1 {
		t.Fatalf("wakes=%d", n)
	}
	var status string
	_ = s.store.db.QueryRow(`SELECT status FROM run_waits`).Scan(&status)
	if status != "expired" {
		t.Fatalf("status=%s", status)
	}
}

func TestRestartRearmsFutureWaitAtDeadline(t *testing.T) {
	s, g, _ := restartWithWait(t, time.Now().Add(600*time.Millisecond))
	time.Sleep(200 * time.Millisecond)
	if n := wakeCount(s, g.ID); n != 0 {
		t.Fatalf("woken before deadline: %d", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for wakeCount(s, g.ID) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if n := wakeCount(s, g.ID); n != 1 {
		t.Fatalf("wakes=%d", n)
	}
}

func TestWaitOfRemovedMemberWakesNobody(t *testing.T) {
	s, g, a := restartWithWait(t, time.Now().Add(time.Hour))
	if _, err := s.store.db.Exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, g.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	s.fireWait(func() string {
		var id string
		_ = s.store.db.QueryRow(`SELECT run_id FROM run_waits`).Scan(&id)
		return id
	}(), "", "x", "expired")
	if n := wakeCount(s, g.ID); n != 0 {
		t.Fatalf("removed member woken: %d", n)
	}
}
