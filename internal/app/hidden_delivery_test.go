package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type hiddenDeliveryFixture struct {
	s         *Store
	a, b, c   Bot
	trace     Conversation
	root, run Run
}

func newHiddenDeliveryFixture(t *testing.T) hiddenDeliveryFixture {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	create := func(name string) Bot {
		bot, err := s.CreateBot(name, "", "model")
		if err != nil {
			t.Fatal(err)
		}
		return bot
	}
	f := hiddenDeliveryFixture{s: s, a: create("A"), b: create("B"), c: create("C")}
	_, f.root, _, err = s.AddUserRun(f.a.DMConversationID, f.a.ID, "Review the evidence", "")
	if err != nil {
		t.Fatal(err)
	}
	hiddenDeliveryStatus(t, s, f.root, "running")
	_, f.run, err = s.AddBotMessage(f.root.ConversationID, f.a.ID, f.b.ID, f.root.ID, "Review the evidence with a colleague")
	if err != nil {
		t.Fatal(err)
	}
	f.trace, err = s.GetConversation(f.run.ConversationID)
	if err != nil || f.trace.UserVisible || f.trace.Kind != "group" {
		t.Fatalf("trace=%+v err=%v", f.trace, err)
	}
	finishGroupRun(t, s, f.root, f.a, "Assignment sent")
	if _, _, err = s.AddMessage(f.b.DMConversationID, "user", "", "", "PRIVATE_DM_SENTINEL", ""); err != nil {
		t.Fatal(err)
	}
	return f
}

func hiddenDeliveryStatus(t *testing.T, s *Store, r Run, status string) {
	t.Helper()
	if ok, err := s.SetRunStatus(r.ID, status, ""); err != nil || !ok {
		t.Fatalf("set %s status %s: %v %v", r.ID, status, ok, err)
	}
}

func hiddenDeliveryTool(tools []Tool) (Tool, bool) {
	for _, tool := range tools {
		if tool.Name == "message_user" {
			return tool, true
		}
	}
	return Tool{}, false
}

func assertHiddenDeliveryContext(t *testing.T, system string, messages []Message, tools []Tool) {
	t.Helper()
	if !strings.Contains(system, "no user participant") || !strings.Contains(system, "ordinary final reply stays only in this trace") ||
		!strings.Contains(system, "Group members:") || !strings.Contains(system, "call handoff with a listed member's exact id") ||
		!strings.Contains(system, "Never edit any Bot's profile") || strings.Contains(system, "Address the user's request yourself") {
		t.Fatalf("incorrect hidden delivery instructions: %s", system)
	}
	_, exposed := hiddenDeliveryTool(tools)
	if strings.Contains(system, "message_user") != exposed {
		t.Fatalf("delivery guidance/tool mismatch: exposed=%v system=%s", exposed, system)
	}
	for _, tool := range tools {
		if tool.Name == "set_bot_profile" {
			t.Fatal("hidden run gained profile rights")
		}
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "PRIVATE_DM_SENTINEL") {
			t.Fatal("hidden context read the Bot's private DM")
		}
	}
}

// Exercise the real durable dispatch/return/retry and Server.execute paths.
// The synthetic engine never contacts a model or external service.
func TestHiddenDeliveryNestedReturnAndRetry(t *testing.T) {
	for _, mode := range []string{"nested message", "group task"} {
		t.Run(mode, func(t *testing.T) {
			f := newHiddenDeliveryFixture(t)
			hiddenDeliveryStatus(t, f.s, f.run, "running")
			var child Run
			var err error
			if mode == "nested message" {
				_, child, err = f.s.AddBotMessage(f.trace.ID, f.b.ID, f.c.ID, f.run.ID, "Verify the evidence")
			} else {
				_, child, err = f.s.AddHandoff(f.trace.ID, f.b.ID, f.a.ID, f.run.ID, "Verify the evidence")
			}
			if err != nil {
				t.Fatal(err)
			}
			finishGroupRun(t, f.s, f.run, f.b, "PRIVATE_DELEGATION")
			childConv, err := f.s.GetConversation(child.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			server := parkedScheduleServer(f.s, f.trace)
			server.queues[childConv.ID] = newConversationQueue()
			server.queues[f.root.ConversationID] = newConversationQueue()
			checkRequest := func(req Request) Tool {
				// runtime.Message and stored Message have distinct types.
				messages := make([]Message, len(req.Messages))
				for i, m := range req.Messages {
					messages[i].Content = m.Content
				}
				assertHiddenDeliveryContext(t, req.System, messages, req.Tools)
				tool, ok := hiddenDeliveryTool(req.Tools)
				if !ok {
					t.Fatalf("missing explicit delivery tool for %s", req.RunID)
				}
				return tool
			}
			server.engine = scheduleResultEngine(func(_ context.Context, req Request) (Result, error) {
				checkRequest(req)
				return Result{Content: "PRIVATE_CHILD_RESULT"}, nil
			})
			server.execute(childConv, child)
			var resumed Run
			var ok bool
			if mode == "nested message" {
				resumed, ok, err = f.s.DirectMessageFollowupForRun(child.ID)
			} else {
				resumed, ok, err = f.s.GroupFollowupForRun(child.ID)
			}
			if err != nil || !ok || resumed.Kind != runKindFollowup || resumed.ConversationID != f.trace.ID || resumed.BotID != f.b.ID {
				t.Fatalf("hidden continuation=%+v ok=%v err=%v", resumed, ok, err)
			}
			server.engine = scheduleResultEngine(func(_ context.Context, req Request) (Result, error) {
				checkRequest(req)
				for _, m := range req.Messages {
					if strings.Contains(m.Content, "[completed colleague task]") && (!strings.Contains(m.Content, "hidden Bot-to-Bot trace") || strings.Contains(m.Content, "Respond in this group")) {
						t.Fatalf("public continuation prompt: %s", m.Content)
					}
				}
				return Result{}, errors.New("synthetic retryable failure before delivery")
			})
			server.execute(f.trace, resumed)
			retry, err := f.s.RetryRun(resumed.ID)
			if err != nil || retry.Kind != runKindFollowup {
				t.Fatalf("retry=%+v err=%v", retry, err)
			}
			server.engine = scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
				tool := checkRequest(req)
				if _, err := tool.Execute(ctx, json.RawMessage(`{"content":"Explicit human result"}`)); err != nil {
					return Result{}, err
				}
				return Result{Content: "PRIVATE_FINAL_ACK"}, nil
			})
			server.execute(f.trace, retry)
			finished, err := f.s.GetRun(retry.ID)
			if err != nil || finished.Status != "done" {
				t.Fatalf("finished retry=%+v err=%v", finished, err)
			}
			for _, bot := range []Bot{f.a, f.b, f.c} {
				messages, _, err := f.s.Messages(bot.DMConversationID, 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, m := range messages {
					if strings.Contains(m.Content, "PRIVATE_") && m.Content != "PRIVATE_DM_SENTINEL" {
						t.Fatalf("private result escaped to DM: %+v", m)
					}
					if m.Kind == messageKindUser {
						count++
						if bot.ID != f.b.ID || m.Content != "Explicit human result" || m.RunID != retry.ID || m.SenderBotID != f.b.ID {
							t.Fatalf("wrong explicit delivery: %+v", m)
						}
					}
				}
				if (bot.ID == f.b.ID && count != 1) || (bot.ID != f.b.ID && count != 0) {
					t.Fatalf("explicit message count for %s=%d", bot.Name, count)
				}
				var leakedEvents int
				if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='message' AND (data LIKE '%PRIVATE_CHILD_RESULT%' OR data LIKE '%PRIVATE_FINAL_ACK%' OR data LIKE '%PRIVATE_DELEGATION%')`, bot.DMConversationID).Scan(&leakedEvents); err != nil || leakedEvents != 0 {
					t.Fatalf("private event leak=%d err=%v", leakedEvents, err)
				}
			}
			var privateFinals int
			if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND run_id=? AND content='PRIVATE_FINAL_ACK'`, f.trace.ID, retry.ID).Scan(&privateFinals); err != nil || privateFinals != 1 {
				t.Fatalf("hidden final missing: %d %v", privateFinals, err)
			}
		})
	}
}

func TestHiddenDeliveryRechecksPublicationBoundaries(t *testing.T) {
	for _, boundary := range []string{"cancelled context", "cancelled run", "waiting run", "finished run", "wrong bot", "removed member", "archived bot", "archived trace", "visible trace", "removed DM member", "archived DM", "foreign canonical DM", "group canonical DM", "blank content"} {
		t.Run(boundary, func(t *testing.T) {
			f := newHiddenDeliveryFixture(t)
			hiddenDeliveryStatus(t, f.s, f.run, "running")
			server := &Server{store: f.s}
			tool, ok := hiddenDeliveryTool(server.tools(f.trace, f.run))
			if !ok {
				t.Fatal("initial delivery tool missing")
			}
			exec := func(query string, args ...any) {
				if _, err := f.s.db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bot, content := f.b.ID, "must not publish"
			switch boundary {
			case "cancelled context":
				cancel()
			case "cancelled run", "waiting run", "finished run":
				status := map[string]string{"cancelled run": "cancelled", "waiting run": runWaiting, "finished run": "done"}[boundary]
				exec(`UPDATE runs SET status=? WHERE id=?`, status, f.run.ID)
			case "wrong bot":
				bot = f.a.ID // Also a real member: membership alone is insufficient.
				forged := f.run
				forged.BotID = bot
				if _, exposed := hiddenDeliveryTool(server.tools(f.trace, forged)); exposed {
					t.Fatal("forged Bot acquired another Bot's delivery tool")
				}
			case "removed member":
				exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, f.trace.ID, f.b.ID)
			case "archived bot":
				exec(`UPDATE bots SET archived=1 WHERE id=?`, f.b.ID)
			case "archived trace":
				exec(`UPDATE conversations SET archived=1 WHERE id=?`, f.trace.ID)
			case "visible trace":
				exec(`UPDATE conversations SET user_visible=1 WHERE id=?`, f.trace.ID)
			case "removed DM member":
				exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, f.b.DMConversationID, f.b.ID)
			case "archived DM":
				exec(`UPDATE conversations SET archived=1 WHERE id=?`, f.b.DMConversationID)
			case "foreign canonical DM":
				// Preserve the unique canonical pointer and B's membership, but
				// make the destination belong to A. Membership alone must not
				// authorize delivery into a different Bot's DM.
				exec(`UPDATE conversations SET bot_id=? WHERE id=?`, f.a.ID, f.b.DMConversationID)
			case "group canonical DM":
				exec(`UPDATE bots SET dm_conversation_id=? WHERE id=?`, f.trace.ID, f.b.ID)
			case "blank content":
				content = " "
			}
			if boundary != "cancelled context" {
				if _, err := f.s.AddBotUserMessage(bot, f.run.ID, content); err == nil {
					t.Fatal("store accepted invalid delivery")
				}
			}
			if boundary != "wrong bot" {
				raw, _ := json.Marshal(map[string]string{"content": content})
				if _, err := tool.Execute(ctx, raw); err == nil {
					t.Fatal("cached tool accepted invalid delivery")
				}
			}
			var count int
			if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind=?`, messageKindUser).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected delivery left messages=%d err=%v", count, err)
			}
			if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='message' AND json_extract(data,'$.kind')=?`, messageKindUser).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected delivery left events=%d err=%v", count, err)
			}
		})
	}
}

func TestHiddenDeliveryVisibilityAndKindScope(t *testing.T) {
	f := newHiddenDeliveryFixture(t)
	hiddenDeliveryStatus(t, f.s, f.run, "running")
	server := &Server{store: f.s}
	group, err := f.s.CreateGroup("Visible group", []string{f.a.ID, f.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.b.DMConversationID, group.ID, f.trace.ID} {
		c, err := f.s.GetConversation(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"", runKindMessage, runKindGroupTask, runKindFollowup, runKindGroupChat, runKindSchedule, runKindTeam, runKindTriage} {
			t.Run(c.Kind+"/"+id+"/"+kind, func(t *testing.T) {
				// Alter only the isolated fixture to test forged kinds and origins.
				// Real nested task/followup production is covered above.
				if _, err := f.s.db.Exec(`UPDATE runs SET conversation_id=?,kind=? WHERE id=?`, c.ID, kind, f.run.ID); err != nil {
					t.Fatal(err)
				}
				r, err := f.s.GetRun(f.run.ID)
				if err != nil {
					t.Fatal(err)
				}
				tools := server.tools(c, r)
				_, exposed := hiddenDeliveryTool(tools)
				want := !c.UserVisible && (kind == runKindMessage || kind == runKindGroupTask || kind == runKindFollowup)
				if exposed != want {
					t.Fatalf("exposed=%v want=%v", exposed, want)
				}
				messages, system := server.buildContextParts(c, r, f.b)
				if strings.Contains(system, "message_user") != exposed {
					t.Fatalf("prompt/tool mismatch: %s", system)
				}
				if !want {
					if _, err := f.s.AddBotUserMessage(f.b.ID, r.ID, "visible request text cannot authorize delivery"); err == nil {
						t.Fatal("out-of-scope run published to human")
					}
				}
				if !c.UserVisible {
					stored := make([]Message, len(messages))
					for i, m := range messages {
						stored[i].Content = m.Content
					}
					assertHiddenDeliveryContext(t, system, stored, tools)
				}
			})
		}
	}
}
