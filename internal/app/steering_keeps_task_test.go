package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// steeringEngine plays the live sequence: the original request works with
// tools, a message from the person is read at a boundary, and the first run
// answers only that message with outcome done. The run the Bot starts for the
// original request answers it.
type steeringEngine struct {
	mu      sync.Mutex
	gate    chan struct{}
	started chan string
	first   string
	covers  bool
	read    string
	resumed []runtime.Message
	resume  string
}

func (e *steeringEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	e.mu.Lock()
	first := e.first
	if first == "" {
		first = req.RunID
		e.first = first
	}
	e.mu.Unlock()
	answer := func(text string, extra map[string]any) error {
		args := map[string]any{"purpose": "answer", "outcome": "done", "text": text}
		for k, v := range extra {
			args[k] = v
		}
		var err error
		for try := 0; try < 3; try++ { // the completion review sends a first answer back once
			if _, err = callTool(ctx, req, "say", "say-"+req.RunID+"-"+string(rune('a'+try)), args); err == nil {
				return nil
			}
		}
		return err
	}
	if req.RunID != first {
		e.mu.Lock()
		e.resumed = append([]runtime.Message(nil), req.Messages...)
		e.mu.Unlock()
		return runtime.Result{}, answer(e.resume, nil)
	}
	for i, name := range []string{"web_search", "web_search", "web_search", "web_fetch", "web_fetch"} {
		call := name + "-" + string(rune('a'+i))
		for _, status := range []string{"queued", "running", "completed"} {
			ev := runtime.ToolEvent{CallID: call, Name: name, Status: status}
			if status == "completed" {
				ev.Result = "fake result of " + name
			}
			if err := req.OnToolEvent(ev); err != nil {
				return runtime.Result{}, err
			}
		}
	}
	e.started <- req.RunID
	select {
	case <-e.gate:
	case <-ctx.Done():
		return runtime.Result{}, ctx.Err()
	}
	text, _, err := req.DrainInbox(1000)
	if err != nil {
		return runtime.Result{}, err
	}
	e.mu.Lock()
	e.read = text
	covers := e.covers
	e.mu.Unlock()
	extra := map[string]any(nil)
	if covers {
		extra = map[string]any{"covers_original": true}
	}
	return runtime.Result{}, answer("I don't know your name yet.", extra)
}

func steeringKeepsTaskFixture(t *testing.T, covers bool) (*Server, *steeringEngine, Bot, Conversation, Run) {
	t.Helper()
	e := &steeringEngine{gate: make(chan struct{}), started: make(chan string, 4), covers: covers, resume: "Index closes, bitcoin and the SF forecast in one paragraph."}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.store.putUserStructuredOutput(true); err != nil {
		t.Fatal(err)
	}
	b, _ := s.store.CreateBot("tofi", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, runs, _, err := s.store.AddUserRuns(c.ID, "look up three things and combine them into one paragraph: today's US index closes, bitcoin price, SF weather tomorrow", "original", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, runs[0])
	select {
	case <-e.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the original run did not start")
	}
	if _, _, queued, _, err := s.store.addUserRunsSteering(c.ID, "quick question: do you remember my name?", "aside", []runSpec{{BotID: b.ID, Model: b.Model}}, nil); err != nil || len(queued) != 1 {
		t.Fatalf("the aside was not queued as steering: %v %v", queued, err)
	}
	close(e.gate)
	return s, e, b, c, runs[0]
}

// The live failure: a message that arrived during the work was answered alone,
// the run ended done and the original request vanished. The original request
// is paused, resumed by itself and answered.
func TestAnswerThatCoversOnlyTheSteeringMessageLeavesTheOriginalRequestToResume(t *testing.T) {
	s, e, _, c, first := steeringKeepsTaskFixture(t, false)
	waitRunStatus(t, s.store, first.ID, "done")
	resume := waitForRunKindCount(t, s.store, `parent_run_id=?`, first.ID)
	waitRunStatus(t, s.store, resume.ID, "done")

	e.mu.Lock()
	read, resumed := e.read, e.resumed
	e.mu.Unlock()
	if !strings.Contains(read, "complete the original request") || !strings.Contains(read, "do you remember my name") {
		t.Fatalf("the steering turn does not tell the model to finish the original request:\n%s", read)
	}
	var block string
	for _, m := range resumed {
		if strings.HasPrefix(m.Content, "[unfinished task]") {
			block = m.Content
		}
	}
	if !strings.Contains(block, "bitcoin price") || !strings.Contains(block, "web_fetch") {
		t.Fatalf("the resumed run does not read the original request and its progress:\n%s", block)
	}
	items, err := s.store.taskCards(c.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("task cards: %+v %v", items, err)
	}
	if items[0].Status != "done" || items[0].Source != taskSourceUser || items[0].ResumeCount != 1 || items[0].InterruptReason != interruptSteered {
		t.Fatalf("the original request after the resume: %+v", items[0])
	}
	answers := 0
	for _, m := range assistantRows(t, s.store, c.ID) {
		if m.Content == e.resume {
			answers++
		}
	}
	if answers != 1 {
		t.Fatalf("the original request got %d final answers", answers)
	}
}

// An answer that states it covers the original request too ends the work.
func TestAnswerThatCoversTheOriginalRequestIsNotResumed(t *testing.T) {
	s, _, _, c, first := steeringKeepsTaskFixture(t, true)
	waitRunStatus(t, s.store, first.ID, "done")
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=? AND id<>?`, c.ID, first.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("extra runs = %d (%v)", n, err)
	}
	if items, _ := s.store.taskCards(c.ID); len(items) != 0 {
		t.Fatalf("a paused task was left behind: %+v", items)
	}
}
