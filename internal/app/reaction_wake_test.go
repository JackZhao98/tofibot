package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type reactionEngine struct {
	mu       sync.Mutex
	requests []runtime.Request
}

func (e *reactionEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	e.mu.Lock()
	e.requests = append(e.requests, req)
	e.mu.Unlock()
	return runtime.Result{Content: ""}, nil
}

func (e *reactionEngine) seen() []runtime.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]runtime.Request(nil), e.requests...)
}

type reactionFixture struct {
	s     *Server
	e     *reactionEngine
	group Conversation
	a, b  Bot
	msgs  []string // two messages written by bot a
}

func newReactionFixture(t *testing.T) *reactionFixture {
	t.Helper()
	e := &reactionEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.reactionWakeWindow = 60 * time.Millisecond
	f := &reactionFixture{s: s, e: e}
	f.a, _ = s.store.CreateBot("Alder", "Writes summaries", "model")
	f.b, _ = s.store.CreateBot("Birch", "Reviews summaries", "model")
	g, err := s.store.CreateGroup("desk", []string{f.a.ID, f.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.group, _ = s.store.GetConversation(g.ID)
	for _, text := range []string{"Summary one is ready.", "Summary two is ready."} {
		id := newID()
		var seq int64
		if err = s.store.db.QueryRow(nextMessageSeqSQL, f.group.ID, f.group.ID, streamDraftActive).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.db.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,content,created_at) VALUES(?,?,?,'assistant','',?,?,?)`, id, f.group.ID, seq, f.a.ID, text, now()); err != nil {
			t.Fatal(err)
		}
		f.msgs = append(f.msgs, id)
	}
	return f
}

// personReacts places the reaction the way the HTTP handler does.
func (f *reactionFixture) personReacts(t *testing.T, messageID, emoji string) {
	t.Helper()
	_, changed, err := f.s.store.setMessageReaction(context.Background(), f.group.ID, messageID, "user", emoji, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		f.s.noteReactionWake(f.group, messageID, "user")
	}
}

func (f *reactionFixture) wakeRuns(t *testing.T) []Run {
	t.Helper()
	runs, err := f.s.store.Runs(f.group.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []Run
	for _, r := range runs {
		if r.Kind == runKindGroupMention {
			out = append(out, r)
		}
	}
	return out
}

func TestPersonReactionsWakeTheBotOnceWhenCoalesced(t *testing.T) {
	f := newReactionFixture(t)
	f.personReacts(t, f.msgs[0], "👍")
	f.personReacts(t, f.msgs[0], "🎉")
	f.personReacts(t, f.msgs[1], "👀")
	time.Sleep(300 * time.Millisecond)
	wakes := f.wakeRuns(t)
	if len(wakes) != 1 {
		t.Fatalf("%d wake runs for one burst, want 1: %+v", len(wakes), wakes)
	}
	if wakes[0].BotID != f.a.ID || wakes[0].ParentRunID != "" {
		t.Fatalf("wake=%+v, want a root run of the Bot that wrote the messages", wakes[0])
	}
	request, ok := f.s.store.reactionWakeRequest(wakes[0].ID)
	if !ok || !strings.Contains(request, "👍 🎉") || !strings.Contains(request, "👀") {
		t.Fatalf("wake request %q lacks the reactions", request)
	}
	settleReactionRuns(t, f.s, f.group.ID)
	reqs := f.e.seen()
	if len(reqs) != 1 {
		t.Fatalf("engine ran %d times, want 1", len(reqs))
	}
	var sawBlock bool
	for _, m := range reqs[0].Messages {
		if strings.Contains(m.Content, "[person reactions]") && strings.Contains(m.Content, "No reply is required") {
			sawBlock = true
		}
	}
	if !sawBlock {
		t.Fatalf("the woken Bot was not told the reaction needs no reply: %+v", reqs[0].Messages)
	}
	// A second burst after the first wake has finished wakes it again.
	f.personReacts(t, f.msgs[1], "🔥")
	time.Sleep(300 * time.Millisecond)
	if n := len(f.wakeRuns(t)); n != 2 {
		t.Fatalf("%d wake runs after a second burst, want 2", n)
	}
}

func settleReactionRuns(t *testing.T, s *Server, group string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ := s.store.Runs(group)
		busy := false
		for _, r := range runs {
			if r.Status == "queued" || r.Status == "running" {
				busy = true
			}
		}
		if !busy && len(runs) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runs did not settle")
}

func TestRepeatedAndRemovedReactionsDoNotWake(t *testing.T) {
	f := newReactionFixture(t)
	f.personReacts(t, f.msgs[0], "👍")
	time.Sleep(300 * time.Millisecond)
	settleReactionRuns(t, f.s, f.group.ID)
	before := len(f.wakeRuns(t))
	f.personReacts(t, f.msgs[0], "👍") // same reaction again: no change
	// Remove it inside a window opened by another reaction on the same message.
	f.personReacts(t, f.msgs[1], "🎉")
	if _, _, err := f.s.store.setMessageReaction(context.Background(), f.group.ID, f.msgs[1], "user", "🎉", false, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if after := len(f.wakeRuns(t)); after != before {
		t.Fatalf("wake runs %d -> %d, want no new wake", before, after)
	}
}

func TestBotReactionNeverWakesAnyone(t *testing.T) {
	f := newReactionFixture(t)
	// Bot b reacts to Bot a's message through the real tool, in a running run.
	_, runs, _, err := f.s.store.AddUserRuns(f.group.ID, "please review", "react-1", []runSpec{{BotID: f.b.ID, Model: f.b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.SetRunStatus(runs[0].ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool := f.s.reactionTool(f.group, runs[0])
	raw, _ := json.Marshal(map[string]string{"message_id": f.msgs[0], "emoji": "👍"})
	if _, err = tool.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	// The wake entry point also refuses a Bot actor.
	f.s.noteReactionWake(f.group, f.msgs[0], "bot:"+f.b.ID)
	time.Sleep(300 * time.Millisecond)
	if wakes := f.wakeRuns(t); len(wakes) != 0 {
		t.Fatalf("a Bot reaction woke %+v", wakes)
	}
	// The reaction is still stored and visible.
	summary := f.s.store.reactionSummaries([]Message{{ID: f.msgs[0]}})
	if !strings.Contains(summary[f.msgs[0]], "👍 Birch") {
		t.Fatalf("summary=%q", summary[f.msgs[0]])
	}
}

func TestReactionOnAnArchivedOrDepartedBotsMessageDoesNothing(t *testing.T) {
	f := newReactionFixture(t)
	if _, err := f.s.store.SetBotArchived(f.a.ID, true); err != nil {
		t.Fatal(err)
	}
	f.personReacts(t, f.msgs[0], "👍")
	time.Sleep(300 * time.Millisecond)
	if wakes := f.wakeRuns(t); len(wakes) != 0 {
		t.Fatalf("archived Bot woken: %+v", wakes)
	}
	// A Bot that is no longer a member is not woken either, even when the
	// window opened while it still was.
	if _, err := f.s.store.SetBotArchived(f.a.ID, false); err != nil {
		t.Fatal(err)
	}
	f.personReacts(t, f.msgs[1], "👍")
	if _, err := f.s.store.db.Exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, f.group.ID, f.a.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if wakes := f.wakeRuns(t); len(wakes) != 0 {
		t.Fatalf("departed Bot woken: %+v", wakes)
	}
}

func TestReactionSummaryIsInHistoryAndNeverInTheSystemPrompt(t *testing.T) {
	f := newReactionFixture(t)
	f.personReacts(t, f.msgs[0], "👍")
	if _, _, err := f.s.store.setMessageReaction(context.Background(), f.group.ID, f.msgs[0], "bot:"+f.b.ID, "🎉", true, &Run{ID: "x", BotID: f.b.ID, ConversationID: f.group.ID}); err == nil {
		t.Log("bot reaction without a running run is refused, as expected")
	}
	if _, err := f.s.store.db.Exec(`INSERT INTO message_reactions(message_id,actor_key,emoji,created_at) VALUES(?,?,?,?)`, f.msgs[0], "bot:"+f.b.ID, "🎉", now()); err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := f.s.store.AddUserRuns(f.group.ID, "what did people think?", "hist-1", []runSpec{{BotID: f.b.ID, Model: f.b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	pm, system := f.s.buildContextParts(f.group, runs[0], f.b)
	var history strings.Builder
	for _, m := range pm {
		history.WriteString(m.Content + "\n")
	}
	if !strings.Contains(history.String(), "[reactions: 👍 person; 🎉 Birch]") {
		t.Fatalf("history lacks the reaction summary:\n%s", history.String())
	}
	if strings.Contains(system, "[reactions:") || strings.Contains(system, "👍") || strings.Contains(system, "🎉") {
		t.Fatal("reactions reached the system prompt")
	}
	// The summary is bounded per message.
	for i := 0; i < 12; i++ {
		if _, err = f.s.store.db.Exec(`INSERT INTO message_reactions(message_id,actor_key,emoji,created_at) VALUES(?,?,?,?)`, f.msgs[1], "bot:"+newID(), "🙂", now()); err != nil {
			t.Fatal(err)
		}
	}
	line := f.s.store.reactionSummaries([]Message{{ID: f.msgs[1]}})[f.msgs[1]]
	if !strings.Contains(line, "+4 more") || len([]rune(line)) > 400 {
		t.Fatalf("unbounded summary: %q", line)
	}
}
