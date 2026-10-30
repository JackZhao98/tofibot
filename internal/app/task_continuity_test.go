package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// continuityEngine plays the models of the owner's sequence with fake tools. A
// run waits on its gate (when it has one) so the test decides the order.
type continuityEngine struct {
	mu      sync.Mutex
	store   func() *Store
	gates   map[string]chan struct{}
	started chan string
	seen    map[string][]runtime.Message
	steer   map[string]bool
	tools   map[string][]string
}

func newContinuityEngine() *continuityEngine {
	return &continuityEngine{gates: map[string]chan struct{}{}, started: make(chan string, 32), seen: map[string][]runtime.Message{}, steer: map[string]bool{}, tools: map[string][]string{}}
}

func (e *continuityEngine) gate(id string) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	g := make(chan struct{})
	e.gates[id] = g
	return g
}

func (e *continuityEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	e.mu.Lock()
	e.seen[req.RunID] = append([]runtime.Message(nil), req.Messages...)
	g := e.gates[req.RunID]
	steer := e.steer[req.RunID]
	names := e.tools[req.RunID]
	e.mu.Unlock()
	for i, name := range names {
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
	if g != nil {
		select {
		case <-g:
		case <-ctx.Done():
			return runtime.Result{}, ctx.Err()
		}
	}
	if steer {
		// The model boundary: a newer person message makes the run yield here.
		if err := req.BeforeModelCall(); err != nil {
			return runtime.Result{}, err
		}
	}
	r, err := e.store().GetRun(req.RunID)
	if err == nil && r.Kind == runKindSchedule {
		return scheduleTestEngine{}.Run(ctx, req)
	}
	return runtime.Result{Content: "Done with " + req.RunID[:8]}, nil
}

func (e *continuityEngine) waitStarted(t *testing.T, id string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case got := <-e.started:
			if got == id {
				return
			}
		case <-deadline:
			t.Fatalf("run %s did not start", id)
		}
	}
}

func waitForRunKindCount(t *testing.T, s *Store, where string, args ...any) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var id string
		if s.db.QueryRow(`SELECT id FROM runs WHERE `+where+` ORDER BY created_at LIMIT 1`, args...).Scan(&id) == nil {
			r, _ := s.GetRun(id)
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no run for %s", where)
	return Run{}
}

// The owner's sequence (conversation cf562ffd): a person's task is running, a
// scheduled occurrence comes due, the person interjects, an apology run answers,
// and the Bot then resumes the work the interjection took it away from.
func TestInterruptedOccurrenceResumesAfterTheInterjectionAndEndsDone(t *testing.T) {
	engine := newContinuityEngine()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	engine.store = func() *Store { return s.store }
	if err := s.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
	b, _ := s.store.CreateBot("tofi", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)

	// 1. The person's task is running.
	_, runs, _, err := s.store.AddUserRuns(c.ID, "Make the image square and upload it as the X avatar", "task", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	task := runs[0]
	gTask := engine.gate(task.ID)
	s.enqueue(c, task)
	engine.waitStarted(t, task.ID)

	// 2. The scheduled occurrence fires and queues behind it.
	x, err := s.store.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "Collect the daily topics into the Notion page", Title: "Notion daily topics", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 3600, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s.store, x.ID, time.Now().Add(-time.Minute))
	occurrence := waitForRunKindCount(t, s.store, `kind='schedule' AND conversation_id=?`, c.ID)
	time.Sleep(200 * time.Millisecond)
	if r, _ := s.store.GetRun(occurrence.ID); r.Status != "queued" {
		t.Fatalf("the occurrence is %q beside the person's task", r.Status)
	}
	gOccurrence := engine.gate(occurrence.ID)
	engine.mu.Lock()
	engine.steer[occurrence.ID] = true
	engine.tools[occurrence.ID] = []string{"notion_search", "notion_read", "web_search"}
	engine.mu.Unlock()
	close(gTask)
	engine.waitStarted(t, occurrence.ID)

	// 3. The person interjects while the occurrence works; an apology run is made.
	_, extra, _, _, err := s.store.addUserRunsSteering(c.ID, "That is a calico cat, not a corgi!", "interjection", []runSpec{{BotID: b.ID, Model: b.Model}}, nil)
	if err != nil || len(extra) != 1 {
		t.Fatalf("interjection: runs=%+v err=%v", extra, err)
	}
	apology := extra[0]
	close(gOccurrence)
	waitRunStatus(t, s.store, occurrence.ID, "cancelled")
	waitRunStatus(t, s.store, apology.ID, "done")

	// The occurrence is on the Bot's list as paused work until the apology run ends.
	// 4. The apology run has ended; the Bot resumes the occurrence by itself.
	resume := waitForRunKindCount(t, s.store, `parent_run_id=? AND kind='schedule'`, occurrence.ID)
	waitRunStatus(t, s.store, resume.ID, "done")

	engine.mu.Lock()
	var block string
	for _, m := range engine.seen[resume.ID] {
		if strings.HasPrefix(m.Content, "[unfinished task]") {
			block = m.Content
		}
	}
	engine.mu.Unlock()
	if block == "" || !strings.Contains(block, "Collect the daily topics into the Notion page") || !strings.Contains(block, "notion_read") {
		t.Fatalf("the resumed run does not read the unfinished task record:\n%s", block)
	}

	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE parent_run_id=?`, occurrence.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("resume runs of the occurrence = %d (%v)", n, err)
	}
	items, err := s.store.taskCards(c.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("task cards: %+v %v", items, err)
	}
	item := items[0]
	if item.Status != "done" || item.Source != taskSourceSchedule || item.ResumeCount != 1 || item.Title != "Notion daily topics" || item.ScheduleID != x.ID {
		t.Fatalf("task after the resume: %+v", item)
	}
	got, err := s.store.GetSchedule(x.ID)
	if err != nil || got.ExecutionStatus != "done" {
		t.Fatalf("the occurrence family reports %q (%v), want done", got.ExecutionStatus, err)
	}
	msgs, _, _ := s.store.Messages(c.ID, 0, 100)
	notices := 0
	for _, m := range msgs {
		if m.Notice != nil && m.Notice.Type == "resume" && m.Notice.Reason == resumeNoticeTask {
			notices++
			if m.Notice.TaskTitle != "Notion daily topics" {
				t.Fatalf("resume notice title = %q", m.Notice.TaskTitle)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("resume notices = %d, want 1", notices)
	}
}
