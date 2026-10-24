package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// followUpEngine routes the triage request to a fixed member, holds the
// member's run until released, then lets that run read its inbox once.
type followUpEngine struct {
	mu      sync.Mutex
	target  string
	triage  int
	hold    chan struct{}
	started chan string
	read    map[string]string // run's bot id -> text read at the boundary
	ctx     map[string][]runtime.Message
	brief   string
	// steer makes the triage answer say the message adds to the target's work.
	steer bool
	// fail makes the triage model fail; gate holds a triage call until closed.
	fail    bool
	gate    chan struct{}
	prompts []string
	runs    map[string]int
}

func (e *followUpEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	if isTriageRequest(req) {
		e.mu.Lock()
		e.triage++
		target, brief, steer, fail, gate := e.target, e.brief, e.steer, e.fail, e.gate
		e.prompts = append(e.prompts, req.System)
		e.mu.Unlock()
		if gate != nil {
			<-gate
		}
		if fail {
			return runtime.Result{}, errors.New("background model unavailable")
		}
		if steer {
			return runtime.Result{Content: `{"bot_id":"` + target + `","steer":true}`}, nil
		}
		if brief != "" {
			return runtime.Result{Content: `{"bot_id":"` + target + `","brief":"` + brief + `"}`}, nil
		}
		return runtime.Result{Content: target}, nil
	}
	e.mu.Lock()
	if e.ctx == nil {
		e.ctx = map[string][]runtime.Message{}
		e.read = map[string]string{}
	}
	if e.runs == nil {
		e.runs = map[string]int{}
	}
	e.runs[req.BotID]++
	e.ctx[req.BotID] = req.Messages
	hold := e.hold
	e.mu.Unlock()
	if e.started != nil {
		select {
		case e.started <- req.BotID:
		default:
		}
	}
	if hold != nil {
		<-hold
	}
	text, _, _ := req.DrainInbox(1000)
	e.mu.Lock()
	e.read[req.BotID] += text
	e.mu.Unlock()
	return runtime.Result{Content: "ok"}, nil
}

func (e *followUpEngine) triageCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.triage
}

func newFollowUpFixture(t *testing.T) (*triageFixture, *followUpEngine) {
	t.Helper()
	f := newTriageFixture(t)
	e := &followUpEngine{target: f.stocks.ID, hold: make(chan struct{}), started: make(chan string, 8)}
	f.s.engine = e
	return f, e
}

func waitMemberRun(t *testing.T, e *followUpEngine) string {
	t.Helper()
	select {
	case id := <-e.started:
		return id
	case <-time.After(10 * time.Second):
		t.Fatal("the member run did not start")
	}
	return ""
}

func TestACorrectionDuringAMembersRunIsReadByThatRunAndNotTriaged(t *testing.T) {
	f, e := newFollowUpFixture(t)
	f.post(t, f.group.ID, "news please", "fu-1")
	if got := waitMemberRun(t, e); got != f.stocks.ID {
		t.Fatalf("started %s", got)
	}
	e.steer = true // triage says the message corrects the running work
	f.post(t, f.group.ID, "I meant the other company", "fu-2")
	waitTriageCalls(t, e, 2)
	close(e.hold)
	runs := settleTriageRuns(t, f.s, f.group.ID, 3)
	if e.triageCalls() != 2 {
		t.Fatalf("triage ran %d times, want 2", e.triageCalls())
	}
	for _, r := range runs {
		if r.BotID == f.travel.ID {
			t.Fatalf("the correction started a run for another member: %+v", runs)
		}
	}
	if !strings.Contains(e.read[f.stocks.ID], "I meant the other company") {
		t.Fatalf("the working member did not read the correction: %q", e.read[f.stocks.ID])
	}
}

func TestAfterTheMemberAnsweredTheNextUnaddressedMessageIsTriaged(t *testing.T) {
	f, e := newFollowUpFixture(t)
	close(e.hold)
	f.post(t, f.group.ID, "news please", "fu-3")
	settleTriageRuns(t, f.s, f.group.ID, 2)
	e.target = f.travel.ID
	f.post(t, f.group.ID, "and a flight", "fu-4")
	runs := settleTriageRuns(t, f.s, f.group.ID, 4)
	if e.triageCalls() != 2 {
		t.Fatalf("triage ran %d times, want 2", e.triageCalls())
	}
	var travel bool
	for _, r := range runs {
		travel = travel || (r.BotID == f.travel.ID && r.Kind == runKindGroupChat)
	}
	if !travel {
		t.Fatalf("the second message did not reach the triaged member: %+v", runs)
	}
}

func TestFollowUpSettingOffTriagesTheCorrection(t *testing.T) {
	f, e := newFollowUpFixture(t)
	off := "false"
	if err := f.s.saveAutomationValue(autoGroupFollowUp, &off); err != nil {
		t.Fatal(err)
	}
	f.post(t, f.group.ID, "news please", "fu-5")
	waitMemberRun(t, e)
	e.target = f.travel.ID
	f.post(t, f.group.ID, "I meant the other company", "fu-6")
	close(e.hold)
	settleTriageRuns(t, f.s, f.group.ID, 4)
	if e.triageCalls() != 2 {
		t.Fatalf("triage ran %d times, want 2", e.triageCalls())
	}
}

func TestAnAddressedMessageIsNotAFollowUpToAnotherMember(t *testing.T) {
	f, e := newFollowUpFixture(t)
	f.post(t, f.group.ID, "news please", "fu-7")
	waitMemberRun(t, e)
	f.post(t, f.group.ID, "@Atlas plan an itinerary", "fu-8")
	close(e.hold)
	runs := settleTriageRuns(t, f.s, f.group.ID, 3)
	var atlas bool
	for _, r := range runs {
		atlas = atlas || r.BotID == f.travel.ID
	}
	if !atlas || e.triageCalls() != 1 {
		t.Fatalf("addressed message mishandled: triage=%d runs=%+v", e.triageCalls(), runs)
	}
}

func TestTheMemberMostRecentlyWorkingOnAPersonMessageTakesTheFollowUp(t *testing.T) {
	f, e := newFollowUpFixture(t)
	f.post(t, f.group.ID, "@Quill earnings", "fu-9")
	f.post(t, f.group.ID, "@Atlas itinerary", "fu-10")
	waitMemberRun(t, e)
	waitMemberRun(t, e)
	e.target, e.steer = f.travel.ID, true
	f.post(t, f.group.ID, "one more detail", "fu-11")
	waitTriageCalls(t, e, 1)
	close(e.hold)
	settleTriageRuns(t, f.s, f.group.ID, 3)
	if !strings.Contains(e.read[f.travel.ID], "one more detail") || strings.Contains(e.read[f.stocks.ID], "one more detail") {
		t.Fatalf("read: stocks=%q travel=%q", e.read[f.stocks.ID], e.read[f.travel.ID])
	}
}

func TestTheTriagedMemberSeesTheEarlierTurnsAShortFollowUpRefersTo(t *testing.T) {
	f, e := newFollowUpFixture(t)
	close(e.hold)
	f.post(t, f.group.ID, "Tell me about SOFI quarterly results", "fu-12")
	settleTriageRuns(t, f.s, f.group.ID, 2)
	e.target = f.travel.ID
	f.post(t, f.group.ID, "and the news?", "fu-13")
	settleTriageRuns(t, f.s, f.group.ID, 4)
	var all strings.Builder
	for _, m := range e.ctx[f.travel.ID] {
		all.WriteString(m.Content + "\n")
	}
	got := all.String()
	if !strings.Contains(got, "SOFI quarterly results") || !strings.Contains(got, "and the news?") {
		t.Fatalf("the member context lacks the earlier turn or the message:\n%s", got)
	}
}

func TestATriageBriefBecomesTheTaskTitleAndReachesTheMembersTaskRecord(t *testing.T) {
	f, e := newFollowUpFixture(t)
	e.brief = "News about the company discussed earlier"
	f.post(t, f.group.ID, "news?", "fu-14")
	waitMemberRun(t, e)
	var runID, title, desc string
	if err := f.s.store.db.QueryRow(`SELECT assignee_run_id,title,description FROM work_items WHERE conversation_id=?`, f.group.ID).Scan(&runID, &title, &desc); err != nil {
		t.Fatal(err)
	}
	if title != e.brief || desc != "news?" {
		t.Fatalf("title=%q description=%q", title, desc)
	}
	if rec := f.s.store.taskRecordContext(runID); !strings.Contains(rec, e.brief) {
		t.Fatalf("task record lacks the brief: %s", rec)
	}
	close(e.hold)
	settleTriageRuns(t, f.s, f.group.ID, 2)
}

func TestATriageAnswerWithoutABriefKeepsTheLiteralTitle(t *testing.T) {
	f, e := newFollowUpFixture(t)
	f.post(t, f.group.ID, "news?", "fu-15")
	waitMemberRun(t, e)
	var runID, title string
	if err := f.s.store.db.QueryRow(`SELECT assignee_run_id,title FROM work_items WHERE conversation_id=?`, f.group.ID).Scan(&runID, &title); err != nil {
		t.Fatal(err)
	}
	if title != "news?" {
		t.Fatalf("title=%q", title)
	}
	if rec := f.s.store.taskRecordContext(runID); strings.Contains(rec, "Brief of the request") {
		t.Fatalf("task record shows a brief that was not made: %s", rec)
	}
	close(e.hold)
	settleTriageRuns(t, f.s, f.group.ID, 2)
}

func waitTriageCalls(t *testing.T, e *followUpEngine, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e.triageCalls() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("triage ran %d times, want %d", e.triageCalls(), n)
}
