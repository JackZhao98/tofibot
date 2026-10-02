package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Reopen the actual server, not only the Store: queued work must be restored
// into conversation workers, while an uncertain running attempt stays stopped.
func TestScheduleServerRestartDeliveryMatrix(t *testing.T) {
	for _, phase := range []string{"before claim", "queued", "running"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			store, bot, conversation := scheduleTestStoreAt(t, dir)
			x, err := store.CreateSchedule(conversation.ID, bot.ID, ScheduleSpec{Content: "synthetic restart acceptance", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
			if err != nil {
				t.Fatal(err)
			}
			makeDue(t, store, x.ID, time.Now().Add(-time.Minute))
			var original Run
			if phase != "before claim" {
				runs, err := store.ClaimDueSchedules(time.Now())
				if err != nil || len(runs) != 1 {
					t.Fatalf("claim=%+v err=%v", runs, err)
				}
				original = runs[0]
				if phase == "running" {
					if _, err := store.SetRunStatus(original.ID, "running", ""); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
				for _, tool := range req.Tools {
					if tool.Name == "complete_scheduled_task" {
						calls.Add(1)
						break
					}
				}
				return (scheduleTestEngine{}).Run(ctx, req)
			})
			server, err := NewServer(Config{DataDir: dir, Engine: engine})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			if phase == "running" {
				waitForRunStatus(t, server, original.ID, "interrupted")
				// Additional scheduler passes must not replay uncertain work.
				for range 3 {
					server.scheduler.tick()
				}
				if calls.Load() != 0 {
					t.Fatal("uncertain attempt automatically replayed")
				}
			} else {
				deadline := time.Now().Add(3 * time.Second)
				for {
					got, err := server.store.GetSchedule(x.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.Status == scheduleComplete {
						original, err = server.store.GetRun(got.LastRunID)
						if err != nil {
							t.Fatal(err)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("restart failed to deliver: %+v", got)
					}
					time.Sleep(10 * time.Millisecond)
				}
				for range 3 {
					server.scheduler.tick()
				}
				if calls.Load() != 1 {
					t.Fatalf("model executions=%d", calls.Load())
				}
			}
			var occurrences, replies, receipts int
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM schedule_occurrences WHERE schedule_id=?`, x.ID).Scan(&occurrences); err != nil {
				t.Fatal(err)
			}
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND role='assistant' AND run_id=?`, conversation.ID, original.ID).Scan(&replies); err != nil {
				t.Fatal(err)
			}
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE run_id=? AND name='complete_scheduled_task' AND status='completed'`, original.ID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			want := 1
			if phase == "running" {
				want = 0
			}
			if occurrences != 1 || replies != want || receipts != want {
				t.Fatalf("occurrences=%d replies=%d receipts=%d", occurrences, replies, receipts)
			}
		})
	}
}

// Preserve the existing missed-trigger policy: coalesce downtime to one
// occurrence, then advance beyond the claim time rather than replay a backlog.
func TestScheduleDowntimePauseResumeCoalesces(t *testing.T) {
	dir := t.TempDir()
	store, bot, conversation := scheduleTestStoreAt(t, dir)
	x, err := store.CreateSchedule(conversation.ID, bot.ID, ScheduleSpec{Content: "synthetic recurring acceptance", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 60, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(-6 * time.Hour)
	makeDue(t, store, x.ID, due)
	if _, err := store.PauseSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	claimTime := time.Now().UTC()
	if runs, err := store.ClaimDueSchedules(claimTime); err != nil || len(runs) != 0 {
		t.Fatalf("paused claim=%+v err=%v", runs, err)
	}
	if _, err := store.ResumeSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ClaimDueSchedules(claimTime)
	if err != nil || len(runs) != 1 {
		t.Fatalf("resumed claim=%+v err=%v", runs, err)
	}
	got, err := store.GetSchedule(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := parseStoredTime(got.NextAtUTC)
	if err != nil || !next.After(claimTime) || next.After(claimTime.Add(time.Minute)) {
		t.Fatalf("next=%s err=%v", got.NextAtUTC, err)
	}
	for range 3 {
		if more, err := store.ClaimDueSchedules(claimTime.Add(2 * time.Hour)); err != nil || len(more) != 0 {
			t.Fatalf("queued family duplicated=%+v err=%v", more, err)
		}
	}
	items, err := store.ScheduleOccurrences(context.Background(), conversation.ID, []string{runs[0].ID})
	if err != nil || len(items) != 1 || items[0].ScheduledForUTC != scheduleTime(due) {
		t.Fatalf("occurrence=%+v err=%v", items, err)
	}
}
