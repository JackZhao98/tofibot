package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type triageRegressionEngine struct {
	mu       sync.Mutex
	requests []runtime.Request
	target   string
}

func (e *triageRegressionEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	e.mu.Lock()
	e.requests = append(e.requests, req)
	count := len(e.requests)
	e.mu.Unlock()
	if count == 1 {
		return runtime.Result{Content: e.target}, nil
	}
	return runtime.Result{Content: "child complete"}, nil
}

func (e *triageRegressionEngine) firstRequest() runtime.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.requests[0]
}

func TestTriageUsesRosterAndPreservesOriginalTask(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	parent, err := s.CreateBot("coordinator", "coordinate work", "parent-model")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateBot("researcher", "research assigned material", "child-model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateGroup("team", []string{parent.ID, child.ID})
	if err != nil {
		t.Fatal(err)
	}
	e := &triageRegressionEngine{target: child.ID}
	server := &Server{store: s, engine: e, queues: map[string]*conversationQueue{}, runs: map[string]context.CancelFunc{}, convMu: map[string]*sync.Mutex{}}
	// Keep the child queued so this test only observes the triage request.
	server.queues[c.ID] = newConversationQueue()
	_, runs, _, err := s.AddUserRuns(c.ID, "Please research the attached filing", "triage-client", []runSpec{{BotID: parent.ID, Model: "triage-model", Kind: runKindTriage}})
	if err != nil {
		t.Fatal(err)
	}
	server.execute(c, runs[0])
	req := e.firstRequest()
	if len(req.Tools) != 0 {
		t.Fatalf("triage tools=%d, want no executable tools", len(req.Tools))
	}
	for _, want := range []string{parent.Name, parent.ID, parent.Instructions, child.Name, child.ID, child.Instructions} {
		if !strings.Contains(req.System, want) {
			t.Fatalf("triage system missing %q: %s", want, req.System)
		}
	}
	var sawTask bool
	for _, message := range req.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "Please research the attached filing") {
			sawTask = true
		}
	}
	if !sawTask {
		t.Fatalf("triage did not receive original task: %#v", req.Messages)
	}
	allRuns, err := s.Runs(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var childRun Run
	for _, run := range allRuns {
		if run.ParentRunID == runs[0].ID {
			childRun = run
		}
	}
	if childRun.ID == "" {
		t.Fatal("triage did not create selected child run")
	}
	childContext, _ := server.buildContext(c, childRun, child)
	var childSawTask, childSawRoutingNotice bool
	for _, message := range childContext {
		if strings.Contains(message.Content, "Please research the attached filing") {
			childSawTask = true
		}
		if strings.Contains(message.Content, "已交给") {
			childSawRoutingNotice = true
		}
	}
	if !childSawTask || childSawRoutingNotice {
		t.Fatalf("child context task=%v generic routing notice=%v: %#v", childSawTask, childSawRoutingNotice, childContext)
	}
}

func TestDuplicateClientIDAndFailedFanoutDoNotInterruptExistingRun(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("a", "", "model")
	b, _ := s.CreateBot("b", "", "model")
	c, err := s.CreateGroup("team", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, first, _, err := s.AddUserRuns(c.ID, "first", "same-client", []runSpec{{BotID: a.ID, Model: a.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(first[0].ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, duplicate, dup, err := s.AddUserRuns(c.ID, "retry", "same-client", []runSpec{{BotID: b.ID, Model: b.Model}}, true)
	if err != nil || !dup || len(duplicate) != 1 || duplicate[0].ID != first[0].ID {
		t.Fatalf("duplicate message runs=%+v dup=%v err=%v", duplicate, dup, err)
	}
	got, _ := s.GetRun(first[0].ID)
	if got.Status != "running" {
		t.Fatalf("duplicate client request interrupted existing run: %s", got.Status)
	}
	if _, _, _, err = s.AddUserRuns(c.ID, "invalid fanout", "new-client", []runSpec{{BotID: ""}}, true); err == nil {
		t.Fatal("invalid fanout unexpectedly succeeded")
	}
	got, _ = s.GetRun(first[0].ID)
	if got.Status != "running" {
		t.Fatalf("rolled-back fanout interrupted existing run: %s", got.Status)
	}
}

func TestSteeringCancelsQueuedRunWithoutCancellingRunningParent(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("a", "", "model")
	b, _ := s.CreateBot("b", "", "model")
	g, err := s.CreateGroup("team", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, parentRuns, _, err := s.AddUserRuns(g.ID, "initial", "initial", []runSpec{{BotID: a.ID, Model: a.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(parentRuns[0].ID, "running", ""); err != nil || !ok {
		t.Fatalf("start parent: %v %v", ok, err)
	}
	_, child, err := s.AddHandoff(g.ID, a.ID, b.ID, parentRuns[0].ID, "queued child")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.AddUserRuns(g.ID, "new steering", "new", []runSpec{{BotID: a.ID, Model: a.Model}}, true)
	if err != nil {
		t.Fatal(err)
	}
	gotParent, _ := s.GetRun(parentRuns[0].ID)
	gotChild, _ := s.GetRun(child.ID)
	if gotParent.Status != "running" || gotChild.Status != "cancelled" {
		t.Fatalf("parent=%s child=%s; running parent must survive queued cancellation", gotParent.Status, gotChild.Status)
	}
}

type lateResultEngine struct {
	started chan struct{}
	release chan struct{}
}

func (e *lateResultEngine) Run(_ context.Context, _ runtime.Request) (runtime.Result, error) {
	close(e.started)
	<-e.release
	return runtime.Result{Content: "late result"}, nil
}

func TestCancelledRunDropsLateEngineResult(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source, _ := s.CreateBot("source", "", "source-model")
	target, _ := s.CreateBot("target", "", "target-model")
	sourceConv, _ := s.GetConversation(source.DMConversationID)
	targetConv, _ := s.GetConversation(target.DMConversationID)
	parent, err := s.AddRun(sourceConv.ID, source.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, run, err := s.AddForwardHandoff(sourceConv.ID, source.ID, target.ID, parent.ID, "forwarded work")
	if err != nil {
		t.Fatal(err)
	}
	e := &lateResultEngine{started: make(chan struct{}), release: make(chan struct{})}
	server := &Server{store: s, engine: e, queues: map[string]*conversationQueue{}, runs: map[string]context.CancelFunc{}, convMu: map[string]*sync.Mutex{}}
	done := make(chan struct{})
	go func() {
		server.execute(targetConv, run)
		close(done)
	}()
	<-e.started
	server.mu.Lock()
	cancel := server.runs[run.ID]
	server.mu.Unlock()
	if cancel == nil {
		t.Fatal("run cancel function was not registered")
	}
	cancel()
	if _, err = s.SetRunStatus(run.ID, "cancelled", "cancelled by test"); err != nil {
		t.Fatal(err)
	}
	close(e.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("late engine run did not return")
	}
	messages, _, err := s.Messages(sourceConv.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Kind == "forward_result" {
			t.Fatalf("late forward result was persisted: %#v", messages)
		}
	}
}

func TestCancelRunTreeCancelsCrossDMDescendantsOnly(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source, _ := s.CreateBot("source", "", "model")
	target, _ := s.CreateBot("target", "", "model")
	unrelated, _ := s.CreateBot("unrelated", "", "model")
	sourceConv, _ := s.GetConversation(source.DMConversationID)
	targetConv, _ := s.GetConversation(target.DMConversationID)
	parent, err := s.AddRun(sourceConv.ID, source.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddForwardHandoff(sourceConv.ID, source.ID, target.ID, parent.ID, "cross DM task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(child.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	grandchild, err := s.AddRun(targetConv.ID, target.ID, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherConv, _ := s.GetConversation(unrelated.DMConversationID)
	unrelatedRun, err := s.AddRun(otherConv.ID, unrelated.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(unrelatedRun.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelRunTree(parent.ID); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id     string
		status string
	}{{parent.ID, "cancelled"}, {child.ID, "cancelled"}, {grandchild.ID, "cancelled"}} {
		got, e := s.GetRun(item.id)
		if e != nil || got.Status != item.status {
			t.Fatalf("run %s status=%s err=%v", item.id, got.Status, e)
		}
	}
	got, err := s.GetRun(unrelatedRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" {
		t.Fatalf("unrelated run was cancelled: %s", got.Status)
	}
}

func TestQueuedRunRemainsDurableWithoutConfiguredModel(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("bot", "", "model")
	c, _ := s.GetConversation(b.DMConversationID)
	server := &Server{store: s, queues: map[string]*conversationQueue{}, runs: map[string]context.CancelFunc{}, convMu: map[string]*sync.Mutex{}}
	_, run, _, err := s.AddUserRun(c.ID, b.ID, "queued", "no-model-client")
	if err != nil {
		t.Fatal(err)
	}
	server.enqueue(c, run)
	defer server.stopConversationWorkers()
	time.Sleep(50 * time.Millisecond)
	got, err := s.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "queued" {
		t.Fatalf("model-less worker consumed queued run: %s", got.Status)
	}
}
