package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestScheduleRecurringWaitsForDelegatedFamily(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	delegate, err := s.CreateBot("fixture delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.CreateGroup("fixture group", []string{bot.ID, delegate.ID})
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.CreateSchedule(c.ID, bot.ID, ScheduleSpec{Content: "fixture task", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 60, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	claimAt := time.Now().UTC()
	makeDue(t, s, x.ID, claimAt.Add(-time.Minute))
	roots, err := s.ClaimDueSchedules(claimAt)
	if err != nil || len(roots) != 1 {
		t.Fatalf("initial claim=%+v err=%v", roots, err)
	}
	root := roots[0]
	if _, err = s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddHandoff(c.ID, bot.ID, delegate.ID, root.ID, "fixture delegation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(root.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running"} {
		if status == "running" {
			if _, err = s.SetRunStatus(child.ID, status, ""); err != nil {
				t.Fatal(err)
			}
		}
		runs, err := s.ClaimDueSchedules(claimAt.Add(2 * time.Hour))
		if err != nil || len(runs) != 0 {
			t.Fatalf("overlapping occurrence while delegate %s: %+v err=%v", status, runs, err)
		}
	}
	if err := s.CancelRunTree(root.ID); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ClaimDueSchedules(claimAt.Add(2 * time.Hour))
	if err != nil || len(runs) != 1 {
		t.Fatalf("future recurrence after family cancellation=%+v err=%v", runs, err)
	}
}

func TestScheduleDailyDoesNotChooseSecondFoldInstant(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// The earlier 01:30 has elapsed; the repeated 01:30 must not be selected.
	for _, after := range []time.Time{
		time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
	} {
		got, err := nextDaily(after, loc, "01:30")
		want := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
		if err != nil || !got.Equal(want) {
			t.Fatalf("after=%s next=%s want=%s err=%v", after, got, want, err)
		}
	}
}

func TestScheduleDoesNotClaimRemovedMember(t *testing.T) {
	s, bot, _ := scheduleTestStore(t)
	defer s.Close()
	other, err := s.CreateBot("remaining member", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateGroup("fixture group", []string{bot.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.CreateSchedule(c.ID, bot.ID, ScheduleSpec{Content: "fixture task", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	if _, err := s.db.Exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, c.ID, bot.ID); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 0 {
		t.Fatalf("removed member claim=%+v err=%v", runs, err)
	}
	if _, err = s.ResumeSchedule(x.ID); err == nil {
		t.Fatal("resumed schedule for removed member")
	}
}

type scheduleResultEngine func(context.Context, Request) (Result, error)

func (f scheduleResultEngine) Run(ctx context.Context, req Request) (Result, error) {
	return f(ctx, req)
}

func TestScheduleFailureNeverPublishesCompletion(t *testing.T) {
	for _, tc := range []struct {
		name              string
		engine            scheduleResultEngine
		rejectDelivery    bool
		cancelBeforeFinal bool
	}{
		{name: "text without confirmation", engine: func(context.Context, Request) (Result, error) {
			return Result{Content: "fixture unconfirmed result"}, nil
		}},
		{name: "confirmation without final answer", engine: func(ctx context.Context, req Request) (Result, error) {
			_, err := (scheduleTestEngine{}).Run(ctx, req)
			return Result{}, err
		}},
		{name: "confirmation with sender prefix but no final answer", engine: func(ctx context.Context, req Request) (Result, error) {
			_, err := (scheduleTestEngine{}).Run(ctx, req)
			return Result{Content: "[sender scheduled bot id=" + req.BotID + "]"}, err
		}},
		{name: "failed tool with model text", engine: func(_ context.Context, req Request) (Result, error) {
			if err := recordScheduleFixtureTool(req, "fixture_source", "failed"); err != nil {
				return Result{}, err
			}
			return Result{Content: "fixture source unavailable"}, nil
		}},
		{name: "model error after confirmation", engine: func(ctx context.Context, req Request) (Result, error) {
			if _, err := (scheduleTestEngine{}).Run(ctx, req); err != nil {
				return Result{}, err
			}
			return Result{Content: "fixture late answer"}, errors.New("fixture model failure")
		}},
		{name: "delivery transaction failure", rejectDelivery: true, engine: (scheduleTestEngine{}).Run},
		{name: "cancelled before late final", cancelBeforeFinal: true, engine: (scheduleTestEngine{}).Run},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bot, c := scheduleTestStore(t)
			defer s.Close()
			x, err := s.CreateSchedule(c.ID, bot.ID, ScheduleSpec{Content: "fixture task", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
			if err != nil {
				t.Fatal(err)
			}
			makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
			runs, err := s.ClaimDueSchedules(time.Now())
			if err != nil || len(runs) != 1 {
				t.Fatalf("claim=%+v err=%v", runs, err)
			}
			if tc.rejectDelivery {
				if _, err := s.db.Exec(`CREATE TRIGGER reject_fixture_delivery BEFORE INSERT ON events WHEN NEW.type='message' BEGIN SELECT RAISE(ABORT,'fixture delivery failed'); END`); err != nil {
					t.Fatal(err)
				}
			}
			engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
				res, err := tc.engine.Run(ctx, req)
				if tc.cancelBeforeFinal {
					if cancelErr := s.CancelRunTree(runs[0].ID); cancelErr != nil {
						return Result{}, cancelErr
					}
				}
				return res, err
			})
			server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
			server.execute(c, runs[0])
			wantStatus := "failed"
			if tc.cancelBeforeFinal {
				wantStatus = "cancelled"
			}
			got, err := s.GetSchedule(x.ID)
			if err != nil || got.Status == scheduleComplete || got.ExecutionStatus != wantStatus {
				t.Fatalf("failed execution reported success: %+v err=%v", got, err)
			}
			var published int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`, runs[0].ID).Scan(&published); err != nil {
				t.Fatal(err)
			}
			if published != 0 {
				t.Fatalf("published %d messages for failed execution", published)
			}
			claimed, err := s.ClaimDueSchedules(time.Now().Add(time.Hour))
			if err != nil || len(claimed) != 0 {
				t.Fatalf("replayed failed once: %+v err=%v", claimed, err)
			}
		})
	}
}

func recordScheduleFixtureTool(req Request, name, terminal string) error {
	for _, status := range []string{"queued", "running", terminal} {
		if err := req.OnToolEvent(runtime.ToolEvent{CallID: name, Name: name, Status: status, Result: "synthetic fixture"}); err != nil {
			return err
		}
	}
	return nil
}

func TestScheduleSuccessfulFallbackPreservesToolFailure(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, bot.ID, ScheduleSpec{Content: "fixture task", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim=%+v err=%v", runs, err)
	}
	engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
		if err := recordScheduleFixtureTool(req, "fixture_source", "failed"); err != nil {
			return Result{}, err
		}
		if err := recordScheduleFixtureTool(req, "fixture_fallback", "completed"); err != nil {
			return Result{}, err
		}
		return (scheduleTestEngine{}).Run(ctx, req)
	})
	server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	server.execute(c, runs[0])
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleComplete || got.ExecutionStatus != "done" {
		t.Fatalf("fallback completion=%+v err=%v", got, err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND name='fixture_source'`, runs[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("source failure lost: %s", status)
	}
}

func scheduleFamilyFixture(t *testing.T) (*Store, Bot, Conversation, Schedule, Run) {
	t.Helper()
	s, bot, c := scheduleTestStore(t)
	t.Cleanup(func() { s.Close() })
	x, err := s.CreateSchedule(c.ID, bot.ID, ScheduleSpec{Content: "fixture", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim=%+v err=%v", runs, err)
	}
	if _, err := s.SetRunStatus(runs[0].ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	return s, bot, c, x, runs[0]
}

func insertScheduleFamilyChild(t *testing.T, s *Store, root Run, id, status string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, root.ConversationID, root.BotID, status, root.ID, root.Model, "handoff", root.ConversationID, root.TriggerMessageID, 2, now(), now()); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleCompletionRequiresSuccessfulFamily(t *testing.T) {
	for _, status := range []string{"failed", "interrupted", "cancelled", "queued", "running", "done"} {
		t.Run(status, func(t *testing.T) {
			s, bot, c, x, root := scheduleFamilyFixture(t)
			insertScheduleFamilyChild(t, s, root, "fixture-child", status)
			if _, done, err := s.FinishRun(root.ID, c.ID, bot.ID, "fixture parent result"); err != nil || !done {
				t.Fatalf("finish=%v err=%v", done, err)
			}
			got, err := s.GetSchedule(x.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := scheduleActive
			if status == "done" {
				want = scheduleComplete
			}
			if got.Status != want || got.ExecutionStatus != status {
				t.Fatalf("family %s: lifecycle=%s execution=%s; want lifecycle=%s", status, got.Status, got.ExecutionStatus, want)
			}
			var completions int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='schedule' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='completed'`, x.ID).Scan(&completions); err != nil {
				t.Fatal(err)
			}
			if status != "done" && completions != 0 {
				t.Fatalf("published %d false completion events", completions)
			}
		})
	}
}

func TestScheduleCompletionAfterExplicitRetryChain(t *testing.T) {
	for _, initialStatus := range []string{"failed", "interrupted"} {
		t.Run(initialStatus, func(t *testing.T) {
			s, bot, c, x, root := scheduleFamilyFixture(t)
			insertScheduleFamilyChild(t, s, root, "fixture-retry-child", initialStatus)
			if _, done, err := s.FinishRun(root.ID, c.ID, bot.ID, "fixture parent result"); err != nil || !done {
				t.Fatalf("finish=%v err=%v", done, err)
			}
			first, err := s.RetryRun("fixture-retry-child")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetRunStatus(first.ID, "failed", "fixture retry failure"); err != nil {
				t.Fatal(err)
			}
			last, err := s.RetryRun(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetRunStatus(last.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			// A different failed branch must continue to block completion even
			// after this branch's explicit retries succeed.
			insertScheduleFamilyChild(t, s, root, "fixture-other-child", "failed")
			if _, done, err := s.FinishRun(last.ID, c.ID, bot.ID, "fixture recovered result"); err != nil || !done {
				t.Fatalf("finish retry=%v err=%v", done, err)
			}
			got, err := s.GetSchedule(x.ID)
			if err != nil || got.Status != scheduleActive || got.ExecutionStatus != "failed" {
				t.Fatalf("other failure hidden: %+v err=%v", got, err)
			}
			other, err := s.RetryRun("fixture-other-child")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetRunStatus(other.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			if _, done, err := s.FinishRun(other.ID, c.ID, bot.ID, "fixture all branches recovered"); err != nil || !done {
				t.Fatalf("finish other retry=%v err=%v", done, err)
			}
			got, err = s.GetSchedule(x.ID)
			if err != nil || got.Status != scheduleComplete || got.ExecutionStatus != "done" {
				t.Fatalf("successful retries did not complete: %+v err=%v", got, err)
			}
		})
	}
}

func TestScheduleCompletionEventFailureRollsBackFinalRetry(t *testing.T) {
	s, bot, c, x, root := scheduleFamilyFixture(t)
	if _, err := s.SetRunStatus(root.ID, "failed", "fixture failure"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryRun(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(retry.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_fixture_completion BEFORE INSERT ON events WHEN NEW.type='schedule' BEGIN SELECT RAISE(ABORT,'fixture completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.FinishRun(retry.ID, c.ID, bot.ID, "fixture retry result"); err == nil || done {
		t.Fatalf("completion should roll back: done=%v err=%v", done, err)
	}
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleActive || got.ExecutionStatus != "running" {
		t.Fatalf("partial completion: %+v err=%v", got, err)
	}
	var published int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`, retry.ID).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if published != 0 {
		t.Fatalf("partial delivery: %d messages", published)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_fixture_completion`); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.FinishRun(retry.ID, c.ID, bot.ID, "fixture retry result"); err != nil || !done {
		t.Fatalf("finish retry=%v err=%v", done, err)
	}
	got, err = s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleComplete || got.ExecutionStatus != "done" {
		t.Fatalf("retry did not complete: %+v err=%v", got, err)
	}
	if _, done, err := s.FinishRun(retry.ID, c.ID, bot.ID, "fixture duplicate result"); err != nil || done {
		t.Fatalf("duplicate finish=%v err=%v", done, err)
	}
	var completions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='schedule' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='completed'`, x.ID).Scan(&completions); err != nil {
		t.Fatal(err)
	}
	if completions != 1 {
		t.Fatalf("completion event count=%d", completions)
	}
}
