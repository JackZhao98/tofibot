package app

import (
	"context"
	"strings"
	"testing"
)

// A scheduled run that ends on plain text with no receipt (a blocker report)
// is graded like an answer; gaps go back privately and the revised attempt
// delivers through say, which records success (D50).
func TestScheduledBlockerEndingIsGradedRevisedAndDelivered(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap, gradePass}}
	model := newFakeModel(t,
		fakeStep{content: "I could not reach the synthetic source, so nothing was posted."},
		fakeStep{calls: []fakeCall{sayAnswerCall("c1", "Posted the synthetic note after trying another route.")}})
	s, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "done" || g.calls.Load() != 2 || modelCalls(model) != 2 || revisionRequests(model) != 1 {
		t.Fatalf("run=%+v grader=%d model=%d revisions=%d", run, g.calls.Load(), modelCalls(model), revisionRequests(model))
	}
	if !strings.Contains(strings.Join(model.requests(), "\n"), "deliver the result with say, purpose answer") {
		t.Fatal("the revision did not reach the writer")
	}
	// The grader saw the plain-text ending as the reply and nothing of the process.
	if !strings.Contains(g.requests[0], "I could not reach the synthetic source") {
		t.Fatal("the blocker text was not graded")
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Posted the synthetic note after trying another route."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	if ok, err := s.store.scheduleReceipt(run.ID); err != nil || !ok {
		t.Fatalf("success was not recorded: %v %v", ok, err)
	}
}

// A blocker that stands after the revision still records no success.
func TestScheduledBlockerThatStandsRecordsNoSuccess(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	model := newFakeModel(t,
		fakeStep{content: "Blocked on the synthetic source."},
		fakeStep{content: "Still blocked on the synthetic source."})
	s, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "failed" || revisionRequests(model) != 1 {
		t.Fatalf("run=%+v revisions=%d", run, revisionRequests(model))
	}
	if ok, _ := s.store.scheduleReceipt(run.ID); ok {
		t.Fatal("a blocked run recorded success")
	}
}

// The plain-text grading shares the rounds counter with answer grading, so the
// cap holds across both.
func TestBlockerGradingSharesTheRoundCap(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	model := newFakeModel(t,
		fakeStep{content: "Blocked on the synthetic source."},
		fakeStep{calls: []fakeCall{sayAnswerCall("c1", "First attempt.")}},
		fakeStep{calls: []fakeCall{sayAnswerCall("c2", "Second attempt.")}})
	s, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "done" || g.calls.Load() != 2 || modelCalls(model) != 3 {
		t.Fatalf("run=%+v grader=%d model=%d", run, g.calls.Load(), modelCalls(model))
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Second attempt."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// With one round, the blocker uses it and the answer ships ungraded.
func TestBlockerUsesTheOnlyRound(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	model := newFakeModel(t,
		fakeStep{content: "Blocked on the synthetic source."},
		fakeStep{calls: []fakeCall{sayAnswerCall("c1", "Only attempt.")}})
	_, run := gateRunWith(t, model.engine(t), g, `{"answer_gate":1}`)
	if run.Status != "done" || g.calls.Load() != 1 {
		t.Fatalf("run=%+v grader=%d", run, g.calls.Load())
	}
}

// A grader that fails leaves the ending exactly as before: the receipt
// reminder is the only send-back.
func TestBlockerGraderFailureEndsAsBefore(t *testing.T) {
	g := &gradeProvider{err: context.DeadlineExceeded}
	model := newFakeModel(t,
		fakeStep{content: "Blocked on the synthetic source."},
		fakeStep{content: "Blocked on the synthetic source."})
	_, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "failed" || modelCalls(model) != 2 || revisionRequests(model) != 0 {
		t.Fatalf("run=%+v model=%d revisions=%d", run, modelCalls(model), revisionRequests(model))
	}
	if !strings.Contains(strings.Join(model.requests(), "\n"), "no successful completion record") {
		t.Fatal("the receipt reminder was lost")
	}
}

// Plain text in a run a person started is never graded.
func TestPersonTriggeredPlainTextIsNeverGraded(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	model := newFakeModel(t, fakeStep{content: "A plain reply in the direct message."})
	_, _ = gateChatRun(t, model.engine(t), g)
	if g.calls.Load() != 0 || revisionRequests(model) != 0 {
		t.Fatalf("grader calls=%d revisions=%d", g.calls.Load(), revisionRequests(model))
	}
}

// The acknowledgement line sits beside the request of a run a person waits on,
// and nowhere else (D50).
func TestAcknowledgeLineIsForRunsAPersonWaitsOn(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("acker", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Please tidy the synthetic notes.", "ack-1")
	if err != nil {
		t.Fatal(err)
	}
	has := func(r Run, c Conversation) bool {
		pm, system := s.buildContextParts(c, r, b)
		if strings.Contains(system, sayAcknowledgeLine) {
			t.Fatal("the line belongs beside the request, not in the cached system text")
		}
		for _, m := range pm {
			if strings.Contains(m.Content, sayAcknowledgeLine) {
				return true
			}
		}
		return false
	}
	run.structured = true
	if !has(run, c) {
		t.Fatal("a person's direct message gets the line")
	}
	plain := run
	plain.structured = false
	if has(plain, c) {
		t.Fatal("unstructured runs have no say tool and get no line")
	}
	sched := run
	sched.Kind = runKindSchedule
	if has(sched, c) {
		t.Fatal("a scheduled run has no one waiting")
	}
	hidden := c
	hidden.UserVisible = false
	if has(run, hidden) {
		t.Fatal("a hidden conversation has no one waiting")
	}
	for _, kind := range []string{runKindTriage, runKindTeam, runKindMessage, runKindGroupTask, runKindFollowup, runKindGroupMention} {
		other := run
		other.Kind = kind
		if has(other, c) {
			t.Fatalf("run kind %q carries its own rules and gets no line", kind)
		}
	}
	group := Conversation{Kind: "group", UserVisible: true}
	if !acknowledgesBeforeWork(group, Run{Kind: runKindGroupChat, structured: true}) {
		t.Fatal("an unaddressed group message gets the line")
	}
	for _, want := range []string{"first acknowledge", "say, purpose message", "say, purpose answer", "only once it is done"} {
		if !strings.Contains(sayAcknowledgeLine, want) {
			t.Fatalf("the line lacks %q", want)
		}
	}
}
