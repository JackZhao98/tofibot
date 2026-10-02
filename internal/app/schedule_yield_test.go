package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func parkedScheduleServer(s *Store, c Conversation) *Server {
	return &Server{store: s, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}, queues: map[string]*conversationQueue{c.ID: newConversationQueue()}}
}

func assertScheduledState(t *testing.T, s *Store, x Schedule, root Run, status string, complete bool) {
	t.Helper()
	got, err := s.GetSchedule(x.ID)
	if err != nil || got.ExecutionStatus != status || (got.Status == scheduleComplete) != complete {
		t.Fatalf("schedule status=%+v err=%v; want %s complete=%v", got, err, status, complete)
	}
	items, err := s.ScheduleOccurrences(context.Background(), root.ConversationID, []string{root.ID})
	if err != nil || len(items) != 1 || items[0].ExecutionStatus != status {
		t.Fatalf("exact occurrence=%+v err=%v want=%s", items, err, status)
	}
}

func TestScheduledMessageYieldAndEarlyReturn(t *testing.T) {
	for _, group := range []bool{false, true} {
		for _, early := range []bool{false, true} {
			name := "dm"
			if group {
				name = "visible group"
			}
			if early {
				name += " child finishes before source"
			}
			t.Run(name, func(t *testing.T) {
				s, bot, c := scheduleTestStore(t)
				defer s.Close()
				delegate, err := s.CreateBot("fixture colleague", "", "model")
				if err != nil {
					t.Fatal(err)
				}
				if group {
					c, err = s.CreateGroup("fixture group", []string{bot.ID, delegate.ID})
					if err != nil {
						t.Fatal(err)
					}
				}
				x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
				server := parkedScheduleServer(s, c)
				var child Run
				var trace Conversation
				server.engine = scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
					if req.RunID != root.ID {
						return (scheduleTestEngine{}).Run(ctx, req)
					}
					_, child, err = s.AddBotMessage(c.ID, bot.ID, delegate.ID, root.ID, "synthetic bounded task")
					if err != nil {
						return Result{}, err
					}
					trace, err = s.GetConversation(child.ConversationID)
					if err != nil {
						return Result{}, err
					}
					server.queues[trace.ID] = newConversationQueue()
					if early {
						server.execute(trace, child)
					}
					if reminder, err := req.BeforeFinalResponse("assignment only"); err != nil || reminder != "" {
						t.Fatalf("real pending assignment rejected: %q %v", reminder, err)
					}
					return Result{Content: "assignment only"}, nil
				})
				server.execute(c, root)
				gotRoot, err := s.GetRun(root.ID)
				wantRoot := runWaiting
				if early {
					wantRoot = "done"
				}
				if err != nil || gotRoot.Status != wantRoot {
					t.Fatalf("source=%+v err=%v", gotRoot, err)
				}
				assertScheduledState(t, s, x, root, "queued", false)
				if !early {
					server.execute(trace, child)
				}
				followup, ok, err := s.DirectMessageFollowupForRun(child.ID)
				if err != nil || !ok || followup.BotID != bot.ID || followup.ConversationID != c.ID {
					t.Fatalf("return=%+v ok=%v err=%v", followup, ok, err)
				}
				anchor, err := s.GetMessage(followup.TriggerMessageID)
				if err != nil || anchor.Kind != messageKindBotResult {
					t.Fatalf("visible source leaked hidden reply: %+v %v", anchor, err)
				}
				var publicReplies int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='message' AND json_extract(data,'$.content')='scheduled result'`, c.ID).Scan(&publicReplies); err != nil || publicReplies != 0 {
					t.Fatalf("child published source answer=%d err=%v", publicReplies, err)
				}
				server.execute(c, followup)
				assertScheduledState(t, s, x, root, "done", true)
				if _, ok, err := s.DirectMessageFollowupForRun(followup.ID); err != nil || ok {
					t.Fatalf("source result looped: %v %v", ok, err)
				}
			})
		}
	}
}

func TestScheduledWaitingFailureRetryRestartAndCancellation(t *testing.T) {
	for _, mode := range []string{"retry", "restart", "cancel root", "cancel child"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			s, bot, c := scheduleTestStoreAt(t, dir)
			defer func() { s.Close() }()
			delegate, err := s.CreateBot("fixture colleague", "", "model")
			if err != nil {
				t.Fatal(err)
			}
			x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
			server := parkedScheduleServer(s, c)
			var child Run
			server.engine = scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
				if req.RunID != root.ID {
					return (scheduleTestEngine{}).Run(ctx, req)
				}
				_, child, err = s.AddBotMessage(c.ID, bot.ID, delegate.ID, root.ID, "bounded fixture task")
				return Result{}, err
			})
			server.execute(c, root)
			gotRoot, err := s.GetRun(root.ID)
			if err != nil || gotRoot.Status != runWaiting {
				t.Fatalf("waiting root=%+v %v", gotRoot, err)
			}
			if _, err := s.SetBotArchived(bot.ID, true); err != ErrArchiveBusy {
				t.Fatalf("waiting source archival=%v", err)
			}
			if _, err := s.DeleteBot(bot.ID); err != ErrDeleteBusy {
				t.Fatalf("waiting source deletion=%v", err)
			}
			trace, err := s.GetConversation(child.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "cancel") {
				id := root.ID
				if mode == "cancel child" {
					id = child.ID
				}
				if err := s.CancelRunTree(id); err != nil {
					t.Fatal(err)
				}
				server.execute(trace, child)
				assertScheduledState(t, s, x, root, "cancelled", false)
				if _, ok, err := s.DirectMessageFollowupForRun(child.ID); err != nil || ok {
					t.Fatalf("cancelled family resumed: %v %v", ok, err)
				}
				return
			}
			if mode == "restart" {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = OpenStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				server = parkedScheduleServer(s, c)
				server.engine = scheduleTestEngine{}
				gotRoot, err = s.GetRun(root.ID)
				if err != nil || gotRoot.Status != runWaiting {
					t.Fatalf("restart lost waiting source: %+v %v", gotRoot, err)
				}
			} else {
				if _, err := s.SetRunStatus(child.ID, "failed", "fixture capability unavailable"); err != nil {
					t.Fatal(err)
				}
				assertScheduledState(t, s, x, root, "failed", false)
				child, err = s.RetryRun(child.ID)
				if err != nil {
					t.Fatal(err)
				}
				server.engine = scheduleTestEngine{}
			}
			server.execute(trace, child)
			followup, ok, err := s.DirectMessageFollowupForRun(child.ID)
			if err != nil || !ok || followup.BotID != bot.ID {
				t.Fatalf("return after %s=%+v %v %v", mode, followup, ok, err)
			}
			server.execute(c, followup)
			assertScheduledState(t, s, x, root, "done", true)
		})
	}
}

func TestScheduledFailedDelegateDoesNotBlockFutureRecurrence(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	delegate, err := s.CreateBot("fixture colleague", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	_, root := occurrenceFixture(t, s, bot, c, scheduleInterval)
	if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddBotMessage(c.ID, bot.ID, delegate.ID, root.ID, "fixture contribution")
	if err != nil {
		t.Fatal(err)
	}
	if yielded, err := s.yieldScheduledRun(root.ID); err != nil || !yielded {
		t.Fatalf("yield=%v %v", yielded, err)
	}
	if runs, err := s.ClaimDueSchedules(time.Now().Add(time.Hour)); err != nil || len(runs) != 0 {
		t.Fatalf("overlapped pending child=%+v %v", runs, err)
	}
	if _, err := s.SetRunStatus(child.ID, "failed", "fixture failure"); err != nil {
		t.Fatal(err)
	}
	if runs, err := s.ClaimDueSchedules(time.Now().Add(time.Hour)); err != nil || len(runs) != 1 {
		t.Fatalf("failed child blocked future occurrence=%+v %v", runs, err)
	}
}

func TestScheduledNestedMessagesReturnThroughEachRequester(t *testing.T) {
	s, a, source := scheduleTestStore(t)
	defer s.Close()
	b, err := s.CreateBot("fixture integrator", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateBot("fixture contributor", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	x, root := occurrenceFixture(t, s, a, source, scheduleOnce)
	server := parkedScheduleServer(s, source)
	var middle, leaf Run
	server.engine = scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
		var from Run
		var target string
		var created *Run
		switch req.RunID {
		case root.ID:
			from, target, created = root, b.ID, &middle
		case middle.ID:
			from, target, created = middle, c.ID, &leaf
		default:
			return (scheduleTestEngine{}).Run(ctx, req)
		}
		_, child, err := s.AddBotMessage(from.ConversationID, from.BotID, target, from.ID, "synthetic nested contribution")
		if err != nil {
			return Result{}, err
		}
		*created = child
		server.queues[child.ConversationID] = newConversationQueue()
		return Result{Content: "assignment only"}, nil
	})
	server.execute(source, root)
	middleConversation, err := s.GetConversation(middle.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	server.execute(middleConversation, middle)
	for _, waiting := range []Run{root, middle} {
		got, err := s.GetRun(waiting.ID)
		if err != nil || got.Status != runWaiting {
			t.Fatalf("nested requester=%+v err=%v", got, err)
		}
	}
	leafConversation, err := s.GetConversation(leaf.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	server.execute(leafConversation, leaf)
	returned, ok, err := s.DirectMessageFollowupForRun(leaf.ID)
	if err != nil || !ok || returned.BotID != b.ID || returned.ConversationID != middle.ConversationID {
		t.Fatalf("leaf skipped immediate requester: %+v %v %v", returned, ok, err)
	}
	gotRoot, err := s.GetRun(root.ID)
	if err != nil || gotRoot.Status != runWaiting {
		t.Fatalf("leaf prematurely settled root: %+v %v", gotRoot, err)
	}
	server.execute(middleConversation, returned)
	final, ok, err := s.DirectMessageFollowupForRun(returned.ID)
	if err != nil || !ok || final.BotID != a.ID || final.ConversationID != source.ID {
		t.Fatalf("integrated return=%+v %v %v", final, ok, err)
	}
	for _, result := range []Run{returned, final} {
		anchor, err := s.GetMessage(result.TriggerMessageID)
		wantKind := "forward_result" // Visible only inside the hidden colleague trace.
		if result.ConversationID == source.ID {
			wantKind = messageKindBotResult
		}
		if err != nil || anchor.Kind != wantKind {
			t.Fatalf("nested reply became public: %+v %v", anchor, err)
		}
	}
	assertScheduledState(t, s, x, root, "queued", false)
	// A child's receipt cannot certify the integrating source's final answer.
	server.engine = scheduleResultEngine(func(context.Context, Request) (Result, error) {
		return Result{Content: "unconfirmed final answer"}, nil
	})
	server.execute(source, final)
	assertScheduledState(t, s, x, root, "failed", false)
	retry, err := s.RetryRun(final.ID)
	if err != nil {
		t.Fatal(err)
	}
	server.engine = scheduleTestEngine{}
	server.execute(source, retry)
	assertScheduledState(t, s, x, root, "done", true)
	if _, ok, err := s.DirectMessageFollowupForRun(retry.ID); err != nil || ok {
		t.Fatalf("completed root looped: %v %v", ok, err)
	}
}

func TestScheduledReturnAndWaitingSettlementAreAtomic(t *testing.T) {
	s, a, source := scheduleTestStore(t)
	defer s.Close()
	b, err := s.CreateBot("fixture colleague", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	x, root := occurrenceFixture(t, s, a, source, scheduleOnce)
	if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddBotMessage(source.ID, a.ID, b.ID, root.ID, "fixture task")
	if err != nil {
		t.Fatal(err)
	}
	if yielded, err := s.yieldScheduledRun(root.ID); err != nil || !yielded {
		t.Fatalf("yield=%v %v", yielded, err)
	}
	if _, err := s.SetRunStatus(child.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	// Abort after inserting the child answer, private result and queued return,
	// precisely when the waiting source would otherwise become done.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_wait_settlement BEFORE UPDATE OF status ON runs WHEN OLD.status='waiting' AND NEW.status='done' BEGIN SELECT RAISE(ABORT,'fixture reject settlement'); END`); err != nil {
		t.Fatal(err)
	}
	if _, finished, err := s.FinishRun(child.ID, child.ConversationID, b.ID, "contribution"); err == nil || finished {
		t.Fatalf("rejected settlement committed: %v %v", finished, err)
	}
	for _, expected := range []struct{ id, status string }{{root.ID, runWaiting}, {child.ID, "running"}} {
		got, err := s.GetRun(expected.id)
		if err != nil || got.Status != expected.status {
			t.Fatalf("partial status: %+v %v", got, err)
		}
	}
	if _, ok, err := s.DirectMessageFollowupForRun(child.ID); err != nil || ok {
		t.Fatalf("partial queued return: %v %v", ok, err)
	}
	var leaked int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE content='contribution'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("partial result: %d %v", leaked, err)
	}
	assertScheduledState(t, s, x, root, "running", false)
	if _, err := s.db.Exec(`DROP TRIGGER reject_wait_settlement`); err != nil {
		t.Fatal(err)
	}
	if _, finished, err := s.FinishRun(child.ID, child.ConversationID, b.ID, "contribution"); err != nil || !finished {
		t.Fatalf("successful return: %v %v", finished, err)
	}
	assertScheduledState(t, s, x, root, "queued", false)
}
