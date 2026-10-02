package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func scheduleFailureFixture(t *testing.T) (*Server, Bot, Conversation, Schedule, Run) {
	t.Helper()
	s, bot, c := scheduleTestStore(t)
	t.Cleanup(func() { s.Close() })
	x, err := s.CreateSchedule(c.ID, bot.ID, ScheduleSpec{Content: "synthetic diagnostic fixture", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim=%+v err=%v", runs, err)
	}
	return &Server{store: s, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}, bot, c, x, runs[0]
}

func assertScheduleFailureNotPublished(t *testing.T, s *Store, x Schedule, r Run, status string) {
	t.Helper()
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.Status == scheduleComplete || got.ExecutionStatus != status {
		t.Fatalf("schedule=%+v err=%v; want unsuccessful %s", got, err, status)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`,
		`SELECT COUNT(*) FROM events WHERE type='message' AND json_extract(data,'$.run_id')=? AND json_extract(data,'$.role')='assistant'`,
		`SELECT COUNT(*) FROM events WHERE type='run' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='done'`,
	} {
		var count int
		if err := s.db.QueryRow(query, r.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("false publication count=%d err=%v query=%s", count, err, query)
		}
	}
	var completions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='schedule' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='completed'`, x.ID).Scan(&completions); err != nil || completions != 0 {
		t.Fatalf("schedule completion events=%d err=%v", completions, err)
	}
	draft, err := s.StreamDraft(r.ID)
	if err != nil || draft.Status != streamDraftCancelled {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
}

func TestScheduleFailureDetailPreservesOnlyBoundedFinalOutput(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
		long                bool
	}{
		{name: "blocker", content: "  Required evidence is unavailable.\nNo result was verified.  ", want: "Required evidence is unavailable.\nNo result was verified."},
		{name: "success claim remains unconfirmed", content: "The task is complete.", want: "The task is complete."},
		{name: "empty"},
		{name: "whitespace", content: " \n\t ", want: ""},
		{name: "sender prefix only", content: "PREFIX", want: ""},
		{name: "sender prefix cleaned", content: "PREFIX\nCould not verify the result.", want: "Could not verify the result."},
		{name: "bounded unicode", content: strings.Repeat("未确认🙂", 2000), long: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, bot, c, x, r := scheduleFailureFixture(t)
			content := strings.ReplaceAll(tc.content, "PREFIX", "[sender "+bot.Name+" id="+bot.ID+"]")
			server.engine = scheduleResultEngine(func(_ context.Context, req Request) (Result, error) {
				// Drafts and tool details must not substitute for an empty final.
				req.OnDelta("earlier draft must not become the diagnostic")
				if err := recordScheduleFixtureTool(req, "fixture_source", "failed"); err != nil {
					t.Fatal(err)
				}
				return Result{Content: content}, nil
			})
			server.execute(c, r)
			got, err := server.store.GetRun(r.ID)
			if err != nil || got.Status != "failed" {
				t.Fatalf("run=%+v err=%v", got, err)
			}
			want := scheduleMissingReceiptError
			if tc.want != "" {
				want += scheduleUnconfirmedLabel + tc.want
			}
			if tc.long {
				if utf8.RuneCountInString(got.Error) != maxScheduleFailureRunes || !utf8.ValidString(got.Error) || !strings.HasPrefix(got.Error, scheduleMissingReceiptError+scheduleUnconfirmedLabel+"未确认🙂") || !strings.HasSuffix(got.Error, "\n[… truncated]") {
					t.Fatalf("invalid bounded diagnostic: runes=%d", utf8.RuneCountInString(got.Error))
				}
			} else if got.Error != want {
				t.Fatalf("error=%q want=%q", got.Error, want)
			}
			var eventError string
			if err := server.store.db.QueryRow(`SELECT json_extract(data,'$.error') FROM events WHERE type='run' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='failed' ORDER BY id DESC LIMIT 1`, r.ID).Scan(&eventError); err != nil || eventError != got.Error {
				t.Fatalf("failure event error=%q err=%v", eventError, err)
			}
			assertScheduleFailureNotPublished(t, server.store, x, r, "failed")
		})
	}
}

func TestScheduleFailureDetailLeavesOtherFailurePathsUnchanged(t *testing.T) {
	for _, name := range []string{"transport", "stream persistence", "assistant persistence", "receipt lookup", "receipt persistence", "final persistence", "cancelled", "context cancelled", "steered"} {
		t.Run(name, func(t *testing.T) {
			server, _, c, x, r := scheduleFailureFixture(t)
			wantStatus, wantError := "failed", "fixture failure"
			server.engine = scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
				res := Result{Content: "unconfirmed late output must not be attached"}
				execSQL := func(query string) {
					t.Helper()
					if _, err := server.store.db.Exec(query); err != nil {
						t.Fatal(err)
					}
				}
				switch name {
				case "transport":
					return res, errors.New(wantError)
				case "stream persistence":
					execSQL(`CREATE TRIGGER reject_fixture_delta BEFORE INSERT ON events WHEN NEW.type='delta' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`)
					req.OnDelta("partial draft")
				case "assistant persistence":
					execSQL(`CREATE TRIGGER reject_fixture_message BEFORE INSERT ON events WHEN NEW.type='message' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`)
					if err := req.OnAssistantTurn(1, "intermediate output"); err == nil {
						t.Fatal("expected publication error")
					}
				case "receipt lookup":
					execSQL(`ALTER TABLE tool_activities RENAME TO fixture_unavailable_activities`)
					wantError = "no such table"
				case "receipt persistence":
					execSQL(`CREATE TRIGGER reject_fixture_receipt BEFORE UPDATE ON tool_activities WHEN NEW.name='complete_scheduled_task' AND NEW.status='completed' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`)
					_, err := (scheduleTestEngine{}).Run(ctx, req)
					if err == nil {
						t.Fatal("expected receipt persistence error")
					}
					return res, err
				case "final persistence":
					if _, err := (scheduleTestEngine{}).Run(ctx, req); err != nil {
						t.Fatal(err)
					}
					execSQL(`CREATE TRIGGER reject_fixture_final BEFORE INSERT ON events WHEN NEW.type='message' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`)
				case "cancelled":
					if err := server.store.CancelRunTree(r.ID); err != nil {
						t.Fatal(err)
					}
					wantStatus, wantError = "cancelled", ""
				case "context cancelled":
					server.runs[r.ID]()
					wantStatus, wantError = "cancelled", context.Canceled.Error()
					return res, ctx.Err()
				case "steered":
					if _, _, _, err := server.store.AddUserRuns(c.ID, "synthetic steering", "fixture-steering", []runSpec{{BotID: r.BotID, Model: r.Model}}); err != nil {
						t.Fatal(err)
					}
					if err := req.BeforeModelCall(); !errors.Is(err, context.Canceled) {
						t.Fatalf("steering error=%v", err)
					}
					wantStatus, wantError = "cancelled", "steered by newer user message"
				}
				return res, nil
			})
			server.execute(c, r)
			got, err := server.store.GetRun(r.ID)
			if err != nil || got.Status != wantStatus || !strings.Contains(got.Error, wantError) || strings.Contains(got.Error, "UNCONFIRMED") || strings.Contains(got.Error, "late output") {
				t.Fatalf("run=%+v err=%v; want status=%s error containing %q", got, err, wantStatus, wantError)
			}
			assertScheduleFailureNotPublished(t, server.store, x, r, wantStatus)
		})
	}
}
