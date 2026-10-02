package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScheduledFinalReviewRequiresActualReceipt(t *testing.T) {
	confirmed := false
	var readErr error
	checks := 0
	review := scheduledFinalReview(runKindSchedule, func() (bool, error) {
		checks++
		return confirmed, readErr
	})
	reminder, err := review("I claim the task is complete")
	if err != nil || !strings.Contains(reminder, "complete_scheduled_task") || !strings.Contains(reminder, "fields, and categories") || !strings.Contains(reminder, "continue the remaining authorized work") || !strings.Contains(reminder, "concrete blocker") || !strings.Contains(reminder, "Do not replay actions") || checks != 1 {
		t.Fatalf("missing receipt review=%q checks=%d err=%v", reminder, checks, err)
	}
	confirmed = true
	if reminder, err = review("verified result"); err != nil || reminder != "" {
		t.Fatalf("confirmed result should not be reviewed: %q %v", reminder, err)
	}
	readErr = errors.New("receipt store unavailable")
	if reminder, err = review("text cannot replace receipt lookup"); !errors.Is(err, readErr) || reminder != "" {
		t.Fatalf("receipt failure should abort: %q %v", reminder, err)
	}
}

func TestScheduledFinalReviewReadsPersistedReceiptInExecute(t *testing.T) {
	s, bot, conversation := scheduleTestStore(t)
	defer s.Close()
	schedule, err := s.CreateSchedule(conversation.ID, bot.ID, ScheduleSpec{Content: "synthetic review fixture", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, schedule.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim=%+v err=%v", runs, err)
	}
	checked := false
	engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
		if req.BeforeFinalResponse == nil {
			t.Fatal("scheduled execution missing final review")
		}
		if reminder, err := req.BeforeFinalResponse("claim without receipt"); err != nil || reminder == "" {
			t.Fatalf("unconfirmed final accepted: reminder=%q err=%v", reminder, err)
		}
		result, err := (scheduleTestEngine{}).Run(ctx, req)
		if err != nil {
			return result, err
		}
		if reminder, err := req.BeforeFinalResponse(result.Content); err != nil || reminder != "" {
			t.Fatalf("persisted receipt was not recognized: reminder=%q err=%v", reminder, err)
		}
		checked = true
		return result, nil
	})
	server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	server.execute(conversation, runs[0])
	if !checked {
		t.Fatal("execution did not reach final review")
	}
	got, err := s.GetSchedule(schedule.ID)
	if err != nil || got.Status != scheduleComplete {
		t.Fatalf("confirmed execution did not finish: %+v err=%v", got, err)
	}
}

func TestScheduledFinalReviewLeavesOtherRunKindsUnchanged(t *testing.T) {
	for _, kind := range []string{"", runKindGroupChat, runKindMessage, runKindTriage} {
		if got := scheduledFinalReview(kind, func() (bool, error) { t.Fatal("ordinary run checked scheduled receipt"); return false, nil }); got != nil {
			t.Fatalf("unexpected scheduled review for %q", kind)
		}
	}
}

func TestScheduledBrowserFallbackStartsStoppedDesktopBeforeSearching(t *testing.T) {
	s, bot, conversation := scheduleTestStore(t)
	defer s.Close()
	server := &Server{store: s}
	for _, run := range []Run{
		{BotID: bot.ID, Kind: runKindSchedule},
		{BotID: bot.ID, Kind: runKindMessage, scheduleTask: &Message{Content: "synthetic scheduled assignment"}},
	} {
		_, system := server.buildContextParts(conversation, run, bot)
		for _, required := range []string{
			"scheduled computer_browser call starts its shared desktop and Chrome",
			"browser.snapshot",
			"visible desktop click/type/scroll",
			"report that concrete blocker instead of claiming",
		} {
			if !strings.Contains(system, required) {
				t.Fatalf("scheduled kind=%q missing browser fallback guidance %q", run.Kind, required)
			}
		}
	}
}
