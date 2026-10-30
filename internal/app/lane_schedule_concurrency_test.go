package app

import (
	"testing"
	"time"
)

// Slice 0 of task continuity (owner's conversation, 2026-10-30): a scheduled
// occurrence fired while a person's run of the same Bot was still working in the
// same conversation. Decision 0062 says they serialize. This test drives the
// real conversation worker (not execute directly) through that order, with a
// person's message queued into the working run in between, and holds the
// promise: the occurrence stays queued until the person's run has ended.
func TestScheduledOccurrenceQueuesBehindARunningUserRun(t *testing.T) {
	p := newLaneProbe()
	s := laneServer(t, p)
	b, _ := s.store.CreateBot("tofi", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, runs, _, err := s.store.AddUserRuns(c.ID, "Make the image square", "u1", []runSpec{{BotID: b.ID, Model: b.Model}})
	if err != nil {
		t.Fatal(err)
	}
	user := runs[0]
	gUser := p.gate(user.ID)
	s.enqueue(c, user)
	waitStarted(t, p, user.ID, 5*time.Second)

	// The person corrects the working run: queued steering, no new run.
	_, extra, queued, _, err := s.store.addUserRunsSteering(c.ID, "You are looking at the banner", "u2", []runSpec{{BotID: b.ID, Model: b.Model}}, nil)
	if err != nil || len(extra) != 0 || len(queued) != 1 {
		t.Fatalf("steering: runs=%+v queued=%+v err=%v", extra, queued, err)
	}

	sched := steeringReviewClaimSchedule(t, s, c, b.ID)
	gSched := p.gate(sched.ID)
	s.enqueue(c, sched)
	for i := 0; i < 20; i++ {
		time.Sleep(25 * time.Millisecond)
		if r, _ := s.store.GetRun(sched.ID); r.Status != "queued" {
			t.Fatalf("scheduled run is %q while the user run still works", r.Status)
		}
		if r, _ := s.store.GetRun(user.ID); r.Status != "running" {
			t.Fatalf("user run is %q", r.Status)
		}
	}
	close(gUser)
	waitRunStatus(t, s.store, user.ID, "done", "failed")
	waitStarted(t, p, sched.ID, 5*time.Second)
	close(gSched)
	waitRunStatus(t, s.store, sched.ID, "done", "failed")
	if n := p.max(b.ID); n != 1 {
		t.Fatalf("Bot ran %d runs at once", n)
	}
}
