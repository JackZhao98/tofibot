package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type scheduleTestEngine struct{}

func (scheduleTestEngine) Run(ctx context.Context, req Request) (Result, error) {
	for _, tool := range req.Tools {
		if tool.Name != "complete_scheduled_task" {
			continue
		}
		const callID = "schedule-test-complete"
		if err := req.OnToolEvent(runtime.ToolEvent{CallID: callID, Name: tool.Name, Status: "queued"}); err != nil {
			return Result{}, err
		}
		if err := req.OnToolEvent(runtime.ToolEvent{CallID: callID, Name: tool.Name, Status: "running"}); err != nil {
			return Result{}, err
		}
		if _, err := tool.Execute(ctx, json.RawMessage(`{"content":"scheduled result"}`)); err != nil {
			return Result{}, err
		}
		if err := req.OnToolEvent(runtime.ToolEvent{CallID: callID, Name: tool.Name, Status: "completed", Result: "accepted"}); err != nil {
			return Result{}, err
		}
	}
	return Result{Content: "scheduled result"}, nil
}

func scheduleTestStore(t *testing.T) (*Store, Bot, Conversation) {
	return scheduleTestStoreAt(t, t.TempDir())
}

func scheduleTestStoreAt(t *testing.T, dir string) (*Store, Bot, Conversation) {
	t.Helper()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateSchedules(s.db); err != nil {
		s.Close()
		t.Fatal(err)
	}
	b, err := s.CreateBot("scheduled bot", "", "model")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s, b, c
}

func makeDue(t *testing.T, s *Store, id string, at time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE schedules SET next_at_utc=? WHERE id=?`, scheduleTime(at), id); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleCRUDAndValidation(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()

	if _, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "x", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 59, Timezone: "UTC"}); err == nil {
		t.Fatal("expected interval lower bound")
	}
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "check", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "America/Los_Angeles"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListSchedules(c.ID)
	if err != nil || len(items) != 1 || items[0].ID != x.ID {
		t.Fatalf("list schedules: %#v, %v", items, err)
	}
	if _, err = s.PauseSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResumeSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListSchedules(c.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted schedule still listed: %#v, %v", items, err)
	}
}

func TestClaimDueScheduleIsTransactionalAndIdempotent(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "send this", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(-time.Minute)
	makeDue(t, s, x.ID, due)
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim: %#v, %v", runs, err)
	}
	if runs[0].Status != "queued" {
		t.Fatalf("run status=%s", runs[0].Status)
	}
	firstRun := runs[0]
	runs, err = s.ClaimDueSchedules(time.Now().Add(time.Hour))
	if err != nil || len(runs) != 0 {
		t.Fatalf("one-time schedule claimed twice: %#v, %v", runs, err)
	}
	var messages, occurrences int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=?`, c.ID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM schedule_occurrences WHERE schedule_id=?`, x.ID).Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || occurrences != 1 {
		t.Fatalf("messages=%d occurrences=%d", messages, occurrences)
	}
	var triggerKind string
	if err = s.db.QueryRow(`SELECT kind FROM messages WHERE conversation_id=?`, c.ID).Scan(&triggerKind); err != nil {
		t.Fatal(err)
	}
	if triggerKind != "scheduled_task" {
		t.Fatalf("scheduled trigger kind=%q", triggerKind)
	}
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleActive || got.ExecutionStatus != "queued" || got.LastRunID == "" {
		t.Fatalf("schedule status=%#v err=%v", got, err)
	}
	if _, err = s.db.Exec(`UPDATE runs SET status='failed',error='fixture failure' WHERE id=?`, firstRun.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryRun(firstRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSchedule(x.ID)
	if err != nil || got.LastRunID != retry.ID || got.ExecutionStatus != "queued" {
		t.Fatalf("schedule retry status=%#v retry=%#v err=%v", got, retry, err)
	}
}

func TestConcurrentClaimsProduceOneOccurrence(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "interval", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 60, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs, e := s.ClaimDueSchedules(time.Now())
			if e != nil {
				t.Errorf("claim: %v", e)
			}
			mu.Lock()
			count += len(runs)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("claimed %d runs", count)
	}
}

func TestDueOrderingUsesFixedNanosecondPrecision(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	first, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "zero", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "half", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err = s.db.Exec(`UPDATE schedules SET next_at_utc=? WHERE id=?`, scheduleTime(base), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE schedules SET next_at_utc=? WHERE id=?`, scheduleTime(base.Add(500*time.Millisecond)), second.ID); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ClaimDueSchedules(base.Add(100 * time.Millisecond))
	if err != nil || len(runs) != 1 {
		t.Fatalf("zero-fraction due ordering: %#v %v", runs, err)
	}
	runs, err = s.ClaimDueSchedules(base.Add(600 * time.Millisecond))
	if err != nil || len(runs) != 1 {
		t.Fatalf("nonzero-fraction due ordering: %#v %v", runs, err)
	}
}

func TestClaimQueuesWhileConversationBusy(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	active, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(active.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "queued behind active", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("schedule was not durably queued: %#v %v", runs, err)
	}
}

func TestDailyTimezoneDSTPolicy(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	spring, err := nextDaily(time.Date(2026, 3, 7, 23, 0, 0, 0, time.UTC), loc, "02:30")
	if err != nil {
		t.Fatal(err)
	}
	springLocal := spring.In(loc)
	if springLocal.Year() != 2026 || springLocal.Month() != time.March || springLocal.Day() != 9 || springLocal.Hour() != 2 || springLocal.Minute() != 30 {
		t.Fatalf("spring gap should skip to next valid day: %s", spring.In(loc))
	}
	fall, err := nextDaily(time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC), loc, "01:30")
	if err != nil || fall.In(loc).Day() != 1 || fall.In(loc).Hour() != 1 || fall.In(loc).Minute() != 30 {
		t.Fatalf("fall ambiguity should produce one wall-clock occurrence: %s %v", fall.In(loc), err)
	}
	next, err := nextDailyAfterOccurrence(fall, loc, "01:30")
	if err != nil || next.In(loc).Day() != 2 || next.In(loc).Hour() != 1 || next.In(loc).Minute() != 30 {
		t.Fatalf("fall repeated minute must not fire twice: %s %v", next.In(loc), err)
	}
}

func TestOneTimeLocalDSTGapRejectedAndFoldDeterministic(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRunAt("2026-03-08 02:30", loc); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("spring-gap one-time request should be rejected: %v", err)
	}
	fold, err := parseRunAt("2026-11-01 01:30", loc)
	if err != nil {
		t.Fatal(err)
	}
	_, offset := fold.Zone()
	if offset != -7*60*60 {
		t.Fatalf("ambiguous local time should choose earlier PDT occurrence, got %s", fold)
	}
}

func TestWorkerDispatchesCommittedQueuedRunOnceAndDoesNotClaimWithoutModel(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "worker", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	// A model-less server leaves due work durable.
	server := &Server{store: s, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	worker, err := StartScheduleWorker(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-time.After(20 * time.Millisecond):
	}
	worker.Stop()
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleActive {
		t.Fatalf("model-less worker claimed schedule: %#v %v", got, err)
	}

	server.engine = scheduleTestEngine{}
	worker, err = StartScheduleWorker(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { worker.Stop(); server.stopConversationWorkers() }()
	deadline := time.Now().Add(2 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		var done int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs r JOIN schedule_occurrences o ON o.run_id=r.id WHERE o.schedule_id=? AND r.status='done'`, x.ID).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if done == 1 {
			completed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed {
		t.Fatal("scheduled run did not finish before timeout")
	}

	var runs int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM runs r JOIN schedule_occurrences o ON o.run_id=r.id WHERE o.schedule_id=?`, x.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("worker duplicated run: %d", runs)
	}
}

func TestScheduleWorkerDoesNotClaimWithCancelledContext(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "cancelled worker", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &Server{store: s, engine: scheduleTestEngine{}, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
	worker, err := StartScheduleWorker(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	worker.Stop()
	server.stopConversationWorkers()
	var claimed int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schedule_occurrences WHERE schedule_id=?`, x.ID).Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if claimed != 0 {
		t.Fatalf("cancelled worker claimed %d occurrences", claimed)
	}
}

func TestScheduleStatusAndDeleteRollbackWhenEventWriteFails(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "atomic", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_schedule_events BEFORE INSERT ON events WHEN NEW.type='schedule' BEGIN SELECT RAISE(ABORT,'schedule event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PauseSchedule(x.ID); err == nil {
		t.Fatal("pause succeeded despite event transaction failure")
	}
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleActive {
		t.Fatalf("pause rollback status=%s err=%v", got.Status, err)
	}
	if err = s.DeleteSchedule(x.ID); err == nil {
		t.Fatal("delete succeeded despite event transaction failure")
	}
	got, err = s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleActive {
		t.Fatalf("delete rollback status=%s err=%v", got.Status, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_schedule_events`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PauseSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
}

func TestQueuedScheduleRestartIsNotReclaimed(t *testing.T) {
	d := t.TempDir()
	s, b, c := scheduleTestStoreAt(t, d)
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "restart", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 60, Timezone: "UTC"})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	claimed, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(claimed) != 1 {
		s.Close()
		t.Fatalf("initial claim=%+v err=%v", claimed, err)
	}
	s.Close()
	s, err = OpenStore(d)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = migrateSchedules(s.db); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.ClaimDueSchedules(time.Now().Add(2 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("queued occurrence reclaimed after restart: %+v", reclaimed)
	}
	var occurrences int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM schedule_occurrences WHERE schedule_id=?`, x.ID).Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if occurrences != 1 {
		t.Fatalf("occurrences=%d, want one committed occurrence", occurrences)
	}
}

func TestRunningScheduleRestartDoesNotReplaySameOccurrence(t *testing.T) {
	d := t.TempDir()
	s, b, c := scheduleTestStoreAt(t, d)
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "running", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	claimed, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(claimed) != 1 {
		s.Close()
		t.Fatalf("initial claim=%+v err=%v", claimed, err)
	}
	if _, err = s.SetRunStatus(claimed[0].ID, "running", ""); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(d)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = migrateSchedules(s.db); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.ClaimDueSchedules(time.Now().Add(2 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("interrupted once occurrence replayed: %+v", reclaimed)
	}
	var status string
	if err = s.db.QueryRow(`SELECT status FROM runs WHERE id=?`, claimed[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "interrupted" {
		t.Fatalf("restarted running status=%s", status)
	}
}

func TestScheduleAgendaViewsAndBotProvenance(t *testing.T) {
	s, b, dm := scheduleTestStore(t)
	defer s.Close()
	other, err := s.CreateBot("other bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateGroup("planning", []string{b.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(time.Hour).Format(time.RFC3339)
	history, err := s.CreateSchedule(dm.ID, b.ID, ScheduleSpec{Content: "past", Kind: scheduleOnce, RunAt: when, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	upcoming, err := s.CreateSchedule(group.ID, b.ID, ScheduleSpec{Content: "next", Kind: scheduleOnce, RunAt: when, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, history.ID, time.Now().Add(-time.Minute))
	claimed, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim history schedule: %#v %v", claimed, err)
	}
	if _, err = s.SetRunStatus(claimed[0].ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.FinishRun(claimed[0].ID, dm.ID, b.ID, "done"); err != nil || !done {
		t.Fatalf("finish history schedule: done=%v err=%v", done, err)
	}
	finished, err := s.GetSchedule(history.ID)
	if err != nil || finished.Status != scheduleComplete || finished.ExecutionStatus != "done" {
		t.Fatalf("finished one-time schedule=%#v err=%v", finished, err)
	}
	items, err := s.ListBotSchedules(b.ID, true)
	if err != nil || len(items) != 1 || items[0].ID != history.ID {
		t.Fatalf("bot history=%#v err=%v", items, err)
	}
	items, err = s.ListScheduleAgenda(group.ID, b.ID, false)
	if err != nil || len(items) != 1 || items[0].ID != upcoming.ID {
		t.Fatalf("group upcoming=%#v err=%v", items, err)
	}
	all, err := s.ListBotSchedules(b.ID, false)
	if err != nil || len(all) != 1 || all[0].ConversationName != group.Name || all[0].ConversationKind != "group" || all[0].BotName != b.Name {
		t.Fatalf("provenance=%#v err=%v", all, err)
	}
}

func TestScheduleHistoryKeepsFailedOnceAndRecurringUpcoming(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	failed, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "fails", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, failed.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE runs SET status='failed',error='failed' WHERE id=?`, runs[0].ID); err != nil {
		t.Fatal(err)
	}
	recurring, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "repeat", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 60, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE schedules SET next_at_utc=? WHERE id=?`, scheduleTime(time.Now().Add(-time.Minute)), recurring.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimDueSchedules(time.Now()); err != nil {
		t.Fatal(err)
	}
	upcoming, err := s.ListScheduleAgenda(c.ID, "", false)
	if err != nil || len(upcoming) != 2 {
		t.Fatalf("upcoming=%#v err=%v", upcoming, err)
	}
	history, err := s.ListScheduleAgenda(c.ID, "", true)
	if err != nil || len(history) != 0 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
}

func TestScheduleAgendaSeesDelegatedRunInFlight(t *testing.T) {
	s, a, _ := scheduleTestStore(t)
	defer s.Close()
	b, err := s.CreateBot("delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateGroup("delegation", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.CreateSchedule(g.ID, a.ID, ScheduleSpec{Content: "delegate", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatal(err)
	}
	root := runs[0]
	if _, err = s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddHandoff(g.ID, a.ID, b.ID, root.ID, "continue delegated work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(root.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(child.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.ExecutionStatus != "running" || got.LastRunID != child.ID {
		t.Fatalf("delegated execution=%#v err=%v", got, err)
	}
}

func TestScheduleExecutionAggregatesActiveFailureAndRetryBranches(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "aggregate", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatal(err)
	}
	root := runs[0]
	if _, err = s.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, root.ID); err != nil {
		t.Fatal(err)
	}
	insert := func(id, status string) {
		t.Helper()
		_, err := s.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, c.ID, b.ID, status, root.ID, b.Model, "schedule", c.ID, root.TriggerMessageID, 0, now(), now())
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("branch-queued", "queued")
	insert("branch-failed", "failed")
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.ExecutionStatus != "queued" || got.LastRunID != "branch-queued" {
		t.Fatalf("active branch hidden: %#v err=%v", got, err)
	}
	if _, err = s.db.Exec(`UPDATE runs SET status='done' WHERE id='branch-queued'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSchedule(x.ID)
	if err != nil || got.ExecutionStatus != "failed" || got.LastRunID != "branch-failed" {
		t.Fatalf("failed branch hidden: %#v err=%v", got, err)
	}
	retry, err := s.RetryRun("branch-failed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(retry.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.FinishRun(retry.ID, c.ID, b.ID, "retry done"); err != nil || !done {
		t.Fatalf("finish retry: done=%v err=%v", done, err)
	}
	got, err = s.GetSchedule(x.ID)
	if err != nil || got.ExecutionStatus != "done" || got.LastRunID != retry.ID {
		t.Fatalf("successful retry did not supersede failure: %#v err=%v", got, err)
	}
}
