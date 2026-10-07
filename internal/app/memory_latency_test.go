package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type blockedSummaryEngine struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	fail    bool
}

func (e *blockedSummaryEngine) Run(ctx context.Context, _ runtime.Request) (runtime.Result, error) {
	e.calls.Add(1)
	select {
	case e.started <- struct{}{}:
	default:
	}
	select {
	case <-e.release:
		if e.fail {
			return runtime.Result{}, context.DeadlineExceeded
		}
		return runtime.Result{Content: "updated summary"}, nil
	case <-ctx.Done():
		return runtime.Result{}, ctx.Err()
	}
}

func summaryFixture(t *testing.T) (*Store, Bot, Conversation, Run) {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateBot("reader", "", "m")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 90; i++ {
		if _, _, err := st.AddMessage(c.ID, "user", "", "", "history", ""); err != nil {
			t.Fatal(err)
		}
	}
	trigger, _, err := st.AddMessage(c.ID, "user", "", "", "current reply", "")
	if err != nil {
		t.Fatal(err)
	}
	return st, b, c, Run{ID: "current-run", BotID: b.ID, TriggerMessageID: trigger.ID}
}

func awaitSummaryStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("summary did not start")
	}
}

func TestBackgroundSummaryDoesNotBlockReplyContextAndCoalesces(t *testing.T) {
	st, b, c, r := summaryFixture(t)
	defer st.Close()
	s := &Server{store: st}
	s.startSummaryWorkers()
	defer s.stopSummaryWorkers()
	e := &blockedSummaryEngine{started: make(chan struct{}, 2), release: make(chan struct{})}
	s.scheduleLongTermSummary(e, c, r, b)
	awaitSummaryStart(t, e.started)
	s.scheduleLongTermSummary(e, c, r, b)
	if got := e.calls.Load(); got != 1 {
		t.Fatalf("concurrent summary calls=%d", got)
	}
	ready := make(chan []runtime.Message, 1)
	go func() { messages, _ := s.buildContextParts(c, r, b); ready <- messages }()
	select {
	case messages := <-ready:
		if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "current reply") && !strings.Contains(messages[len(messages)-2].Content, "current reply") {
			t.Fatal("recent context missing while summary runs")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reply context waited for background summary")
	}
	close(e.release)
	s.summaryWG.Wait()
	_, covered, _, err := st.LatestSummary(c.ID)
	if err != nil || covered == 0 {
		t.Fatalf("background summary not saved: covered=%d err=%v", covered, err)
	}
}

func TestBackgroundSummaryFailureCooldownAndStop(t *testing.T) {
	st, b, c, r := summaryFixture(t)
	defer st.Close()
	s := &Server{store: st}
	s.startSummaryWorkers()
	e := &blockedSummaryEngine{started: make(chan struct{}, 2), release: make(chan struct{}), fail: true}
	s.scheduleLongTermSummary(e, c, r, b)
	awaitSummaryStart(t, e.started)
	close(e.release)
	s.summaryWG.Wait()
	s.scheduleLongTermSummary(e, c, r, b)
	if got := e.calls.Load(); got != 1 {
		t.Fatalf("cooldown allowed retry: calls=%d", got)
	}
	s.summaryMu.Lock()
	s.summaryNext[c.ID] = time.Now().Add(-time.Second)
	s.summaryMu.Unlock()
	blocking := &blockedSummaryEngine{started: make(chan struct{}, 1), release: make(chan struct{})}
	s.scheduleLongTermSummary(blocking, c, r, b)
	awaitSummaryStart(t, blocking.started)
	s.stopSummaryWorkers()
	if _, _, _, err := st.LatestSummary(c.ID); err == nil {
		t.Fatal("cancelled summary wrote after stop")
	}
	s.scheduleLongTermSummary(blocking, c, r, b)
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("stopped worker restarted: calls=%d", got)
	}
}

func TestSummaryCoverageNeverMovesBackward(t *testing.T) {
	st, _, c, _ := summaryFixture(t)
	defer st.Close()
	if _, err := st.SaveSummary(c.ID, 20, "newer"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSummary(c.ID, 19, "stale"); err == nil {
		t.Fatal("accepted older coverage")
	}
	if _, err := st.SaveSummary(c.ID, 20, "duplicate"); err == nil {
		t.Fatal("accepted equal coverage")
	}
	_, covered, content, err := st.LatestSummary(c.ID)
	if err != nil || covered != 20 || content != "newer" {
		t.Fatalf("summary changed: covered=%d content=%q err=%v", covered, content, err)
	}
}
