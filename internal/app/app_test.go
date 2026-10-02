package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type testEngine struct{}

func (testEngine) Run(context.Context, runtime.Request) (runtime.Result, error) {
	return runtime.Result{Content: "ok"}, nil
}

type captureEngine struct{ req runtime.Request }

func (e *captureEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	if !strings.HasSuffix(req.RunID, ":memory") {
		e.req = req
	}
	return runtime.Result{Content: "ok"}, nil
}

func TestContextBudgetKeepsOriginalSearchable(t *testing.T) {
	e := &captureEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", strings.Repeat("指令", 6000), "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	for i := 0; i < 301; i++ {
		_, _, err = s.store.AddMessage(c.ID, "user", "", "", strings.Repeat("中文历史", 300), "m-"+itoa(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, r, _, _ := s.store.AddUserRun(c.ID, b.ID, "Recall the earlier history", "context-trigger")
	s.execute(c, r)
	used := len([]rune(e.req.System))
	for _, m := range e.req.Messages {
		used += len([]rune(m.Content))
	}
	if used > maxSystemRunes+maxHistoryRunes {
		t.Fatalf("context budget=%d", used)
	}
	hits, _ := s.store.Search(c.ID, "中文历史", 1)
	if len(hits) != 1 {
		t.Fatal("original history was not searchable")
	}
	s.summaryWG.Wait()
	_, covered, summary, err := s.store.LatestSummary(c.ID)
	if err != nil || covered <= 0 || summary == "" {
		t.Fatalf("summary=%d %q %v", covered, summary, err)
	}
}

type blockingEngine struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingEngine) Run(ctx context.Context, _ runtime.Request) (runtime.Result, error) {
	close(e.started)
	select {
	case <-ctx.Done():
		return runtime.Result{}, ctx.Err()
	case <-e.release:
		return runtime.Result{Content: "finished"}, nil
	}
}

func TestCancelDoesNotBecomeDone(t *testing.T) {
	e := &blockingEngine{started: make(chan struct{}), release: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "instructions", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, _ := s.store.AddUserRun(c.ID, b.ID, "hello", "id-1")
	s.enqueue(c, r)
	<-e.started
	s.mu.Lock()
	cancel := s.runs[r.ID]
	s.mu.Unlock()
	cancel()
	if changed, err := s.store.SetRunStatus(r.ID, "cancelled", "cancelled"); err != nil || !changed {
		t.Fatalf("cancel changed=%v err=%v", changed, err)
	}
	close(e.release)
	got := waitRun(t, s, r.ID, "cancelled")
	if got.Status != "cancelled" {
		t.Fatalf("status=%s", got.Status)
	}
	m, _, _ := s.store.Messages(c.ID, 0, 50)
	if len(m) != 1 {
		t.Fatalf("assistant was written after cancel: %d messages", len(m))
	}
}

type handoffEngine struct{ child string }

func (e handoffEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	for _, message := range req.Messages {
		if strings.Contains(message.Content, "[completed colleague task]") {
			return runtime.Result{Content: req.BotID + " integrated colleague result"}, nil
		}
	}
	if req.BotID != e.child {
		for _, tool := range req.Tools {
			if tool.Name == "handoff" {
				_, err := tool.Execute(context.Background(), json.RawMessage(`{"bot_id":"`+e.child+`","task":"please check"}`))
				if err != nil {
					return runtime.Result{}, err
				}
				break
			}
		}
	}
	return runtime.Result{Content: req.BotID + " done"}, nil
}
func TestGroupHandoffUsesRealEngineTools(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("parent", "parent instructions", "model")
	b, _ := s.store.CreateBot("child", "child instructions", "model")
	s.mu.Lock()
	s.engine = handoffEngine{child: b.ID}
	s.codexManaged = false
	s.mu.Unlock()
	c, _ := s.store.CreateGroup("g", []string{a.ID, b.ID})
	_, r, _, _ := s.store.AddUserRun(c.ID, a.ID, "@parent start", "client")
	s.execute(c, r)
	runs, _ := s.store.Runs(c.ID)
	if len(runs) < 2 {
		t.Fatalf("runs=%d", len(runs))
	}
	waitRun(t, s, runs[1].ID, "done")
	followup, ok, err := s.store.GroupFollowupForRun(runs[1].ID)
	if err != nil || !ok {
		t.Fatalf("missing requester followup: ok=%v err=%v", ok, err)
	}
	waitRun(t, s, followup.ID, "done")
	msgs, _, _ := s.store.Messages(c.ID, 0, 50)
	if len(msgs) < 4 {
		t.Fatalf("messages=%d", len(msgs))
	}
	if msgs[1].Role != "assistant" || msgs[1].SenderBotID != a.ID {
		t.Fatalf("handoff sender=%#v", msgs[1])
	}
}
func waitRun(t *testing.T, s *Server, id, status string) Run {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, e := s.store.GetRun(id)
		if e == nil && r.Status == status {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, _ := s.store.GetRun(id)
	t.Fatalf("run %s did not reach %s (got %s)", id, status, r.Status)
	return r
}

func TestStorePagingRestartAndCanonicalDM(t *testing.T) {
	d := t.TempDir()
	s, e := OpenStore(d)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.CreateBot("整理员", "organize", "test")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.CreateBot("校对员", "proof", "test")
	if e != nil {
		t.Fatal(e)
	}
	if a.DMConversationID == b.DMConversationID {
		t.Fatal("DM ids are not unique")
	}
	for i := 0; i < 301; i++ {
		if _, _, e = s.AddMessage(a.DMConversationID, "user", "", "", "message "+string(rune('a'+i%26)), "client-"+itoa(i)); e != nil {
			t.Fatal(e)
		}
	}
	page, more, e := s.Messages(a.DMConversationID, 0, 50)
	if e != nil || len(page) != 50 || !more {
		t.Fatalf("page len=%d more=%v err=%v", len(page), more, e)
	}
	if page[0].Seq != 252 || page[49].Seq != 301 {
		t.Fatalf("wrong page seq %d..%d", page[0].Seq, page[49].Seq)
	}
	r, _ := s.AddRun(a.DMConversationID, a.ID, "")
	s.SetRunStatus(r.ID, "running", "")
	s.Close()
	s, e = OpenStore(d)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.GetRun(r.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Status != "interrupted" {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestConversationAcceptsOneActiveUserRun(t *testing.T) {
	s, e := OpenStore(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	b, _ := s.CreateBot("bot", "", "")
	c, _ := s.GetConversation(b.DMConversationID)
	type result struct{ err error }
	out := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func(i int) { _, _, _, err := s.AddUserRun(c.ID, b.ID, "msg", itoa(i)); out <- result{err} }(i)
	}
	var ok, busy int
	for i := 0; i < 2; i++ {
		if err := (<-out).err; err == nil {
			ok++
		} else if errors.Is(err, ErrConversationBusy) {
			busy++
		}
	}
	if ok != 1 || busy != 1 {
		t.Fatalf("accepted=%d busy=%d", ok, busy)
	}
}
func TestGroupContextIdentifiesCurrentBotAndRejectsSelfHandoff(t *testing.T) {
	s, e := OpenStore(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, _ := s.CreateBot("整理员", "do the work", "model")
	b, _ := s.CreateBot("校对员", "check work", "model")
	c, _ := s.CreateGroup("g", []string{a.ID, b.ID})
	r, _ := s.AddRun(c.ID, a.ID, "")
	_, _ = s.SetRunStatus(r.ID, "running", "")
	_, system := (&Server{store: s}).buildContext(c, r, a)
	if !strings.Contains(system, a.Name) || !strings.Contains(system, a.ID) || !strings.Contains(system, "never impersonate") {
		t.Fatalf("identity guidance missing: %s", system)
	}
	tools := (&Server{store: s}).tools(c, r)
	if _, e := tools[2].Execute(context.Background(), json.RawMessage(`{"bot_id":"`+a.ID+`","task":"self"}`)); e == nil {
		t.Fatal("self handoff accepted")
	}
}
func TestMemoryScopeAndSummary(t *testing.T) {
	s, e := OpenStore(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, _ := s.CreateBot("a", "", "")
	b, _ := s.CreateBot("b", "", "")
	m1, _ := s.AddMemory(a.DMConversationID, a.ID, "private-a")
	_, _ = s.AddMemory(b.DMConversationID, b.ID, "private-b")
	v, _ := s.Memories(a.DMConversationID, a.ID)
	if len(v) != 1 || v[0].Content != m1.Content {
		t.Fatalf("memory scope leaked: %#v", v)
	}
	if _, e = s.SaveSummary(a.DMConversationID, 1, "derived"); e != nil {
		t.Fatal(e)
	}
	_, covered, content, e := s.LatestSummary(a.DMConversationID)
	if e != nil || covered != 1 || content != "derived" {
		t.Fatalf("summary=%d %q %v", covered, content, e)
	}
	if e = s.DeleteMemory(m1.ID); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Memories(a.DMConversationID, a.ID)
	if len(v) != 0 {
		t.Fatal("deleted memory still visible")
	}
}

func TestGroupMembershipHandoffAndEventReplay(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateBot("整理员", "", "")
	b, _ := s.CreateBot("校对员", "", "")
	c, err := s.CreateGroup("工作组", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.ListConversations()
	if err != nil || len(all) != 3 {
		t.Fatalf("conversations=%d err=%v", len(all), err)
	}
	r, _ := s.AddRun(c.ID, a.ID, "")
	_, _ = s.SetRunStatus(r.ID, "running", "")
	ts := (&Server{store: s, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}).tools(c, r)
	if _, err := ts[2].Execute(context.Background(), json.RawMessage(`{"bot_id":"missing","task":"x"}`)); err == nil {
		t.Fatal("handoff accepted non-member")
	}
	childID, err := ts[2].Execute(context.Background(), json.RawMessage(`{"bot_id":"`+b.ID+`","task":"check"}`))
	if err != nil {
		t.Fatal(err)
	}
	if childID == "" {
		t.Fatal("handoff did not create run")
	}
	ev, err := s.Events(c.ID, 0)
	if err != nil || len(ev) != 2 {
		t.Fatalf("events=%d err=%v", len(ev), err)
	}
}
func itoa(i int) string { return strconv.Itoa(i) }
