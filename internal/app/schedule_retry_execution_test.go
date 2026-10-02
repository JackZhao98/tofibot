package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Existing family tests finish runs directly. Exercise the model execution
// boundary too: a retry of the returning delegate must obtain its own receipt.
func TestScheduleAgendaRetryExecutionRequiresFreshReceipt(t *testing.T) {
	for _, descendant := range []bool{false, true} {
		name := "root"
		if descendant {
			name = "delegated return"
		}
		t.Run(name, func(t *testing.T) {
			s, bot, c := scheduleTestStore(t)
			defer s.Close()
			delegate, err := s.CreateBot("fixture delegate", "", "model")
			if err != nil {
				t.Fatal(err)
			}
			if descendant {
				c, err = s.CreateGroup("fixture scheduled group", []string{bot.ID, delegate.ID})
				if err != nil {
					t.Fatal(err)
				}
			}
			x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
			failed := root
			if descendant {
				if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
					t.Fatal(err)
				}
				_, child, err := s.AddHandoff(c.ID, bot.ID, delegate.ID, root.ID, "synthetic bounded contribution")
				if err != nil {
					t.Fatal(err)
				}
				finishGroupRun(t, s, root, bot, "synthetic root result")
				if _, err := s.SetRunStatus(child.ID, "running", ""); err != nil {
					t.Fatal(err)
				}
				finishGroupRun(t, s, child, delegate, "synthetic colleague result")
				failed = followupFor(t, s, child.ID)
			}
			// A prior attempt's receipt must not authorize a later retry.
			for _, status := range []string{"queued", "running", "completed"} {
				if err := s.RecordToolEvent(c.ID, bot.ID, failed.ID, runtime.ToolEvent{CallID: "prior-receipt", Name: "complete_scheduled_task", Status: status, Result: "synthetic prior receipt"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.SetRunStatus(failed.ID, "failed", "synthetic failure after receipt"); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				agenda, err := s.GetSchedule(x.ID)
				if err != nil || agenda.ExecutionStatus != "failed" || agenda.LastRunID != failed.ID {
					t.Fatalf("agenda retry target=%+v err=%v", agenda, err)
				}
				retry, err := s.RetryRun(agenda.LastRunID)
				if err != nil {
					t.Fatal(err)
				}
				if retry.Kind != failed.Kind || retry.TriggerMessageID != failed.TriggerMessageID || retry.ParentRunID != failed.ID {
					t.Fatalf("retry lost identity: %+v", retry)
				}
				called := false
				engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
					called = true
					if req.BeforeFinalResponse == nil {
						t.Error("scheduled descendant retry has no receipt review")
					} else if reminder, err := req.BeforeFinalResponse("synthetic unconfirmed result"); err != nil || reminder == "" {
						t.Errorf("prior receipt authorized retry: reminder=%q err=%v", reminder, err)
					}
					sawTask := false
					for _, m := range req.Messages {
						sawTask = sawTask || strings.Contains(m.Content, x.Content)
					}
					if !sawTask {
						t.Error("retry lost original scheduled instruction")
					}
					if descendant && !strings.Contains(req.Messages[len(req.Messages)-1].Content, "[completed colleague task]") {
						t.Error("scheduled context replaced the current colleague result")
					}
					if attempt == 0 {
						return Result{Content: "synthetic unconfirmed result"}, nil
					}
					return (scheduleTestEngine{}).Run(ctx, req)
				})
				server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
				server.execute(c, retry)
				if !called {
					t.Fatal("retry did not reach execution")
				}
				wantStatus, wantLifecycle := "failed", scheduleActive
				if attempt == 1 {
					wantStatus, wantLifecycle = "done", scheduleComplete
				}
				got, err := s.GetSchedule(x.ID)
				if err != nil || got.ExecutionStatus != wantStatus || got.Status != wantLifecycle || got.LastRunID != retry.ID {
					t.Fatalf("retry attempt %d: schedule=%+v err=%v", attempt, got, err)
				}
				occurrences, err := s.ScheduleOccurrences(context.Background(), c.ID, []string{root.ID})
				if err != nil || len(occurrences) != 1 || occurrences[0].ExecutionStatus != wantStatus || occurrences[0].StatusRunID != retry.ID {
					t.Fatalf("retry occurrence=%+v err=%v", occurrences, err)
				}
				var published, completions int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`, retry.ID).Scan(&published); err != nil {
					t.Fatal(err)
				}
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='schedule' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='completed'`, x.ID).Scan(&completions); err != nil {
					t.Fatal(err)
				}
				if published != attempt || completions != attempt {
					t.Fatalf("retry attempt %d: published=%d completions=%d", attempt, published, completions)
				}
				failed = retry
			}
		})
	}
}

func TestScheduleDescendantRetryKeepsCrossConversationContext(t *testing.T) {
	for _, mode := range []string{"scheduled", "ordinary", "missing schedule trigger"} {
		t.Run(mode, func(t *testing.T) {
			s, bot, c := scheduleTestStore(t)
			defer s.Close()
			delegate, err := s.CreateBot("fixture recipient", "", "model")
			if err != nil {
				t.Fatal(err)
			}
			var root Run
			original := ""
			if mode == "ordinary" {
				_, root, _, err = s.AddUserRun(c.ID, bot.ID, "synthetic chat request", "fixture-chat")
			} else {
				var x Schedule
				x, root = occurrenceFixture(t, s, bot, c, scheduleOnce)
				original = x.Content
				// Recovery must use the occurrence's original task, even if the
				// schedule definition has changed since it was claimed.
				_, err = s.db.Exec(`UPDATE schedules SET content='synthetic later schedule definition' WHERE id=?`, x.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			_, child, err := s.AddBotMessage(c.ID, bot.ID, delegate.ID, root.ID, "synthetic bounded message assignment")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetRunStatus(root.ID, "done", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetRunStatus(child.ID, "failed", "synthetic delegation failure"); err != nil {
				t.Fatal(err)
			}
			retry, err := s.RetryRun(child.ID)
			if err != nil {
				t.Fatal(err)
			}
			trace, err := s.GetConversation(retry.ConversationID)
			if err != nil || trace.ID == c.ID || trace.UserVisible {
				t.Fatalf("expected hidden cross-conversation trace: %+v err=%v", trace, err)
			}
			if mode == "missing schedule trigger" {
				if _, err := s.db.Exec(`UPDATE runs SET trigger_message_id='missing-fixture-trigger' WHERE id=?`, root.ID); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			engine := scheduleResultEngine(func(_ context.Context, req Request) (Result, error) {
				called = true
				if !strings.Contains(req.System, "hidden Bot-to-Bot message trace") {
					t.Error("scheduled ancestry replaced message delivery restrictions")
				}
				wantScheduled := mode != "ordinary"
				if req.BeforeFinalResponse == nil {
					t.Fatal("run missing final review callback")
				}
				reminder, reviewErr := req.BeforeFinalResponse("synthetic draft without tool work")
				if reviewErr != nil {
					t.Fatalf("final review failed: %v", reviewErr)
				}
				if wantScheduled && reminder == "" {
					t.Fatal("scheduled run did not request a missing receipt repair")
				}
				if !wantScheduled && reminder != "" {
					t.Fatalf("tool-free ordinary run unexpectedly requested repair: %q", reminder)
				}
				var sawOriginal, sawReceipt, sawMessageUser bool
				used := 0
				for _, m := range req.Messages {
					used += len([]rune(m.Content))
					sawOriginal = sawOriginal || original != "" && strings.Contains(m.Content, original)
					if strings.Contains(m.Content, "synthetic later schedule definition") {
						t.Error("mutable schedule definition replaced original task")
					}
				}
				for _, tool := range req.Tools {
					sawReceipt = sawReceipt || tool.Name == "complete_scheduled_task"
					sawMessageUser = sawMessageUser || tool.Name == "message_user"
				}
				if sawOriginal != wantScheduled || sawReceipt != wantScheduled || !sawMessageUser || used > maxHistoryRunes {
					t.Errorf("original=%v receipt=%v message_user=%v history=%d", sawOriginal, sawReceipt, sawMessageUser, used)
				}
				last := req.Messages[len(req.Messages)-1].Content
				if !strings.Contains(last, "[current handoff task]") || !strings.Contains(last, "synthetic bounded message assignment") {
					t.Error("retry lost current bounded assignment")
				}
				return Result{}, errors.New("synthetic stop after context inspection")
			})
			server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
			server.execute(trace, retry)
			if called != (mode != "missing schedule trigger") {
				t.Fatalf("model called=%v for %s", called, mode)
			}
			got, err := s.GetRun(retry.ID)
			if err != nil || got.Status != "failed" || got.Kind != runKindMessage {
				t.Fatalf("retry=%+v err=%v", got, err)
			}
		})
	}
}

func TestScheduleFirstAttemptDescendantAndSourceFollowupReceipts(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		childReceipt, followupReceipt bool
	}{
		{name: "unconfirmed descendant"},
		{name: "confirmed descendant unconfirmed source", childReceipt: true},
		{name: "confirmed descendant and source", childReceipt: true, followupReceipt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bot, c := scheduleTestStore(t)
			defer s.Close()
			delegate, err := s.CreateBot("fixture delegate", "", "model")
			if err != nil {
				t.Fatal(err)
			}
			x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
			if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
				t.Fatal(err)
			}
			_, child, err := s.AddBotMessage(c.ID, bot.ID, delegate.ID, root.ID, "synthetic bounded contribution")
			if err != nil {
				t.Fatal(err)
			}
			finishGroupRun(t, s, root, bot, "synthetic root result")
			trace, err := s.GetConversation(child.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			if child.ParentRunID != root.ID || child.Kind != runKindMessage || child.Status != "queued" || trace.UserVisible {
				t.Fatalf("expected first-attempt hidden message descendant: %+v trace=%+v", child, trace)
			}
			calls := 0
			engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
				calls++
				if req.BeforeFinalResponse == nil {
					t.Fatal("first-attempt scheduled descendant missing receipt review")
				}
				if reminder, err := req.BeforeFinalResponse("synthetic unconfirmed result"); err != nil || reminder == "" {
					t.Fatalf("another run's receipt authorized first attempt: reminder=%q err=%v", reminder, err)
				}
				confirmed := tc.childReceipt
				if req.RunID != child.ID {
					confirmed = tc.followupReceipt
					if req.BotID != bot.ID || !strings.Contains(req.Messages[len(req.Messages)-1].Content, "[completed colleague task]") {
						t.Fatal("source followup lost its requester or result context")
					}
				}
				if !confirmed {
					return Result{Content: "synthetic unconfirmed result"}, nil
				}
				res, err := (scheduleTestEngine{}).Run(ctx, req)
				if err == nil {
					if reminder, err := req.BeforeFinalResponse(res.Content); err != nil || reminder != "" {
						t.Fatalf("fresh receipt not recognized: reminder=%q err=%v", reminder, err)
					}
				}
				return res, err
			})
			// Park dispatch, as in the existing triage regression, so we can
			// inspect the durable return before executing its first attempt.
			server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}, queues: map[string]*conversationQueue{c.ID: newConversationQueue()}}
			server.execute(trace, child)
			assertAnswer := func(r Run, confirmed bool) {
				t.Helper()
				wantStatus, wantCount := "failed", 0
				if confirmed {
					wantStatus, wantCount = "done", 1
				}
				got, err := s.GetRun(r.ID)
				if err != nil || got.Status != wantStatus || got.Kind != r.Kind {
					t.Fatalf("first attempt=%+v want=%s err=%v", got, wantStatus, err)
				}
				var count int
				var answer string
				if err := s.db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(content),'') FROM messages WHERE conversation_id=? AND run_id=? AND role='assistant' AND sender_bot_id=?`, r.ConversationID, r.ID, r.BotID).Scan(&count, &answer); err != nil {
					t.Fatal(err)
				}
				if count != wantCount || confirmed && answer != "scheduled result" {
					t.Fatalf("first-attempt publication count=%d answer=%q", count, answer)
				}
			}
			assertAnswer(child, tc.childReceipt)
			followup, exists, err := s.DirectMessageFollowupForRun(child.ID)
			if err != nil || exists != tc.childReceipt {
				t.Fatalf("source followup=%+v exists=%v err=%v", followup, exists, err)
			}
			statusRun := child.ID
			if tc.childReceipt {
				if followup.ParentRunID != child.ID || followup.Kind != runKindFollowup || followup.Status != "queued" || followup.ConversationID != c.ID || followup.BotID != bot.ID {
					t.Fatalf("first-attempt source return identity=%+v", followup)
				}
				anchor, err := s.GetMessage(followup.TriggerMessageID)
				if err != nil || anchor.Kind != messageKindBotResult || anchor.Content != "scheduled result" {
					t.Fatalf("hidden contribution did not return privately: %+v err=%v", anchor, err)
				}
				pending, err := s.GetSchedule(x.ID)
				if err != nil || pending.Status != scheduleActive || pending.ExecutionStatus != "queued" || pending.LastRunID != followup.ID {
					t.Fatalf("child receipt completed source prematurely: %+v err=%v", pending, err)
				}
				server.execute(c, followup)
				assertAnswer(followup, tc.followupReceipt)
				statusRun = followup.ID
			}
			wantCalls, wantCompletions := 1, 0
			wantStatus, wantLifecycle := "failed", scheduleActive
			if tc.childReceipt {
				wantCalls++
			}
			if tc.followupReceipt {
				wantStatus, wantLifecycle, wantCompletions = "done", scheduleComplete, 1
			}
			got, err := s.GetSchedule(x.ID)
			if err != nil || got.ExecutionStatus != wantStatus || got.Status != wantLifecycle || got.LastRunID != statusRun || calls != wantCalls {
				t.Fatalf("first-attempt family=%+v calls=%d err=%v", got, calls, err)
			}
			var completions int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='schedule' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='completed'`, x.ID).Scan(&completions); err != nil {
				t.Fatal(err)
			}
			if completions != wantCompletions {
				t.Fatalf("completion events=%d want=%d", completions, wantCompletions)
			}
		})
	}
}

// Exercise the actual dispatch boundary: a durable assignment yields the
// model turn, and only the receipt-confirmed source return completes the task.
func TestScheduleRootHandoffWaitsForSourceReceipt(t *testing.T) {
	s, bot, _ := scheduleTestStore(t)
	defer s.Close()
	delegate, err := s.CreateBot("fixture delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateGroup("fixture root delegation", []string{bot.ID, delegate.ID})
	if err != nil {
		t.Fatal(err)
	}
	x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	handedOff := false
	engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
		if req.RunID != root.ID {
			return (scheduleTestEngine{}).Run(ctx, req)
		}
		for _, tool := range req.Tools {
			if tool.Name != "handoff" {
				continue
			}
			args, err := json.Marshal(map[string]string{"bot_id": delegate.ID, "task": "synthetic bounded contribution"})
			if err != nil {
				return Result{}, err
			}
			if _, err := tool.Execute(ctx, args); err != nil {
				return Result{}, err
			}
			handedOff = true
			if reminder, err := req.BeforeFinalResponse("synthetic assignment sent"); err != nil || reminder != "" {
				t.Errorf("durable delegation incorrectly requested a result receipt: %q %v", reminder, err)
			}
			return Result{Content: "synthetic assignment sent"}, nil
		}
		t.Fatal("scheduled root has no handoff tool")
		return Result{}, nil
	})
	server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}, queues: map[string]*conversationQueue{c.ID: newConversationQueue()}}
	server.execute(c, root)
	gotRoot, err := s.GetRun(root.ID)
	if err != nil || !handedOff || gotRoot.Status != runWaiting || gotRoot.Error != "" {
		t.Fatalf("root after actual handoff=%+v handedOff=%v err=%v", gotRoot, handedOff, err)
	}
	runs, err := s.Runs(c.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("handoff runs=%+v err=%v", runs, err)
	}
	child := runs[1]
	if child.ParentRunID != root.ID || child.Kind != runKindGroupTask || child.Status != "queued" {
		t.Fatalf("actual handoff child=%+v", child)
	}
	server.execute(c, child)
	gotChild, err := s.GetRun(child.ID)
	if err != nil || gotChild.Status != "done" {
		t.Fatalf("confirmed child=%+v err=%v", gotChild, err)
	}
	followup, exists, err := s.GroupFollowupForRun(child.ID)
	if err != nil || !exists || followup.BotID != bot.ID || followup.Status != "queued" {
		t.Fatalf("waiting requester did not resume: %+v exists=%v err=%v", followup, exists, err)
	}
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleActive || got.ExecutionStatus != "queued" || got.LastRunID != followup.ID {
		t.Fatalf("source return must still be awaited: %+v err=%v", got, err)
	}
	var answers, completions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`, root.ID).Scan(&answers); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='schedule' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='completed'`, x.ID).Scan(&completions); err != nil {
		t.Fatal(err)
	}
	if answers != 0 || completions != 0 {
		t.Fatalf("waiting root published answers=%d completions=%d", answers, completions)
	}
	server.execute(c, followup)
	got, err = s.GetSchedule(x.ID)
	if err != nil || got.Status != scheduleComplete || got.ExecutionStatus != "done" || got.LastRunID != followup.ID {
		t.Fatalf("confirmed source result did not complete occurrence: %+v err=%v", got, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='schedule' AND json_extract(data,'$.id')=? AND json_extract(data,'$.status')='completed'`, x.ID).Scan(&completions); err != nil || completions != 1 {
		t.Fatalf("completion events=%d err=%v", completions, err)
	}
}
