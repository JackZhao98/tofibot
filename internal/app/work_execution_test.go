package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func executionFixture(t *testing.T) (*Store, Bot, WorkItem) {
	t.Helper()
	s, _ := workItemTestStore(t)
	bot, err := s.CreateBot("execution owner", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	item := workItemCreate(t, s, bot.DMConversationID, bot.ID, "task", "Produce the requested result")
	return s, bot, item
}

func readWorkExecution(t *testing.T, s *Store, item WorkItem) WorkItem {
	t.Helper()
	got, err := s.GetWorkItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Execution == nil {
		t.Fatal("execution missing")
	}
	return got
}

func finishWorkExecution(t *testing.T, s *Store, r Run, content string) Message {
	t.Helper()
	if ok, err := s.SetRunStatus(r.ID, "running", ""); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	m, ok, err := s.FinishRun(r.ID, r.ConversationID, r.BotID, content)
	if !ok || err != nil {
		t.Fatalf("finish: %v %v", ok, err)
	}
	return m
}

func TestWorkExecutionStartReplayResultAndCompletion(t *testing.T) {
	s, bot, item := executionFixture(t)
	request := newID()
	r, duplicate, err := s.StartWorkItem(item.ID, request)
	if err != nil || duplicate || r.BotID != bot.ID || r.TriggerMessageID == "" {
		t.Fatalf("start=%+v duplicate=%v err=%v", r, duplicate, err)
	}
	got := readWorkExecution(t, s, item)
	if got.Status != "in_progress" || !got.Execution.Active || got.Execution.Status != "queued" {
		t.Fatalf("queued=%+v %+v", got, got.Execution)
	}
	for _, patch := range []WorkItemPatch{{Status: workItemString("done")}, {BotID: workItemString(bot.ID)}, {Title: workItemString("changed")}} {
		if _, err := s.UpdateWorkItem(item.ID, patch); !errors.Is(err, ErrWorkExecutionConflict) {
			t.Fatalf("active edit accepted: %v", err)
		}
	}
	if _, _, err := s.StartWorkItem(item.ID, newID()); !errors.Is(err, ErrWorkExecutionConflict) {
		t.Fatalf("active duplicate: %v", err)
	}
	replay, duplicate, err := s.StartWorkItem(item.ID, request)
	if err != nil || !duplicate || replay.ID != r.ID {
		t.Fatalf("replay=%+v %v %v", replay, duplicate, err)
	}
	message := finishWorkExecution(t, s, r, "Actual published result")
	got = readWorkExecution(t, s, item)
	if got.Status != "review" || got.Execution.Active || got.Execution.ResultMessageID != message.ID || got.Execution.Result != message.Content {
		t.Fatalf("result=%+v %+v", got, got.Execution)
	}
	open, err := s.ListWorkItems(item.ConversationID, "", false)
	if err != nil || len(open) != 1 || open[0].Status != "review" {
		t.Fatalf("list=%+v %v", open, err)
	}
	if _, err := s.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("done")}); err != nil {
		t.Fatal(err)
	}
	got = readWorkExecution(t, s, item)
	if got.Status != "done" || got.CompletedAt == "" {
		t.Fatalf("completion=%+v", got)
	}
	if _, duplicate, err := s.StartWorkItem(item.ID, request); err != nil || !duplicate {
		t.Fatalf("terminal replay: %v %v", duplicate, err)
	}
	if _, _, err := s.StartWorkItem(item.ID, newID()); !errors.Is(err, ErrWorkExecutionConflict) {
		t.Fatalf("closed started: %v", err)
	}
	if _, err := s.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("todo")}); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil || next.ID == r.ID {
		t.Fatalf("new attempt: %+v %v", next, err)
	}
	got = readWorkExecution(t, s, item)
	if got.Execution.RootRunID != next.ID || got.Execution.Result != "" {
		t.Fatal("prior result leaked into new attempt")
	}
}

func TestWorkExecutionConcurrentRequestsAndAtomicRollback(t *testing.T) {
	s, _, item := executionFixture(t)
	request := newID()
	var wg sync.WaitGroup
	var created atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, duplicate, err := s.StartWorkItem(item.ID, request)
			if err != nil {
				t.Error(err)
			}
			if !duplicate && err == nil {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created %d", created.Load())
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("runs=%d %v", count, err)
	}
	s2, _, other := executionFixture(t)
	if _, err := s2.db.Exec(`CREATE TRIGGER reject_execution BEFORE INSERT ON work_item_executions BEGIN SELECT RAISE(ABORT,'test rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s2.StartWorkItem(other.ID, newID()); err == nil {
		t.Fatal("failure swallowed")
	}
	for _, table := range []string{"runs", "messages", "work_item_executions"} {
		if err := s2.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s: %d %v", table, count, err)
		}
	}
	saved, _ := s2.GetWorkItem(other.ID)
	if saved.Status != "todo" {
		t.Fatal("failed start changed item")
	}
}

func TestWorkExecutionRejectsUnavailableAndDoesNotSteer(t *testing.T) {
	for _, which := range []string{"invalid-request", "goal", "closed", "archived-bot", "archived-conversation", "hidden", "removed-member", "busy", "delegated-busy"} {
		t.Run(which, func(t *testing.T) {
			s, bot, item := executionFixture(t)
			request := newID()
			var existing Run
			switch which {
			case "invalid-request":
				request = "not-a-uuid"
			case "goal":
				item = workItemCreate(t, s, bot.DMConversationID, bot.ID, "goal", "A planning goal")
			case "closed":
				_, _ = s.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("cancelled")})
			case "archived-bot":
				_, _ = s.db.Exec(`UPDATE bots SET archived=1 WHERE id=?`, bot.ID)
			case "archived-conversation":
				_, _ = s.db.Exec(`UPDATE conversations SET archived=1 WHERE id=?`, item.ConversationID)
			case "hidden":
				_, _ = s.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, item.ConversationID)
			case "removed-member":
				_, _ = s.db.Exec(`DELETE FROM members WHERE conversation_id=?`, item.ConversationID)
			case "busy":
				_, existing, _, _ = s.AddUserRun(item.ConversationID, bot.ID, "Already working", "existing")
			case "delegated-busy":
				other, _ := s.CreateBot("other conversation", "", "model")
				_, existing, _, _ = s.AddUserRun(other.DMConversationID, other.ID, "Delegated work", "other-work")
				_, _ = s.db.Exec(`UPDATE runs SET origin_conversation_id=? WHERE id=?`, item.ConversationID, existing.ID)
			}
			if _, _, err := s.StartWorkItem(item.ID, request); err == nil {
				t.Fatal("unavailable task started")
			}
			if which == "busy" || which == "delegated-busy" {
				current, _ := s.GetRun(existing.ID)
				if current.Status != "queued" {
					t.Fatal("existing run was stolen")
				}
			}
		})
	}
}

func TestWorkExecutionRetryFamilyNoPrematureResult(t *testing.T) {
	s, _, item := executionFixture(t)
	root, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.SetRunStatus(root.ID, "failed", "temporary failure")
	got := readWorkExecution(t, s, item)
	if got.Status != "blocked" || got.Execution.Error != "temporary failure" {
		t.Fatalf("failure: %+v %+v", got, got.Execution)
	}
	retry, err := s.RetryRun(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = readWorkExecution(t, s, item)
	if !got.Execution.Active || got.Execution.Status != "queued" || got.Execution.Result != "" {
		t.Fatalf("retry: %+v", got.Execution)
	}
	final := finishWorkExecution(t, s, retry, "Retried result")
	got = readWorkExecution(t, s, item)
	if got.Status != "review" || got.Execution.StatusRunID != retry.ID || got.Execution.ResultMessageID != final.ID {
		t.Fatalf("retry result: %+v %+v", got, got.Execution)
	}
}

func TestWorkExecutionGroupDelegationFollowsReturnAndCancelRoot(t *testing.T) {
	s, a, _ := executionFixture(t)
	b, _ := s.CreateBot("delegate", "", "model")
	c, _ := s.CreateGroup("board team", []string{a.ID, b.ID})
	item := workItemCreate(t, s, c.ID, a.ID, "task", "Assemble the delegated result")
	root, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.SetRunStatus(root.ID, "running", "")
	_, child, err := s.AddHandoff(c.ID, a.ID, b.ID, root.ID, "Do the assigned contribution")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.FinishRun(root.ID, c.ID, a.ID, "Delegated; waiting for contribution"); err != nil || !ok {
		t.Fatalf("root finish=%v %v", ok, err)
	}
	got := readWorkExecution(t, s, item)
	if !got.Execution.Active || got.Execution.Result != "" {
		t.Fatalf("premature result: %+v", got.Execution)
	}
	finishWorkExecution(t, s, child, "Completed delegated contribution")
	followup, found, err := s.GroupFollowupForRun(child.ID)
	if err != nil || !found {
		t.Fatalf("followup: %v %v", found, err)
	}
	got = readWorkExecution(t, s, item)
	if !got.Execution.Active || got.Execution.Result != "" {
		t.Fatal("return was not awaited")
	}
	if err := s.CancelRunTree(root.ID); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetRun(followup.ID)
	got = readWorkExecution(t, s, item)
	if current.Status != "cancelled" || got.Execution.Active || got.Status != "blocked" || got.Execution.Error != "" {
		t.Fatalf("cancel family=%+v %+v", current, got.Execution)
	}
}

func TestWorkExecutionDoesNotUseProgressHiddenOrEmptyAsReceipt(t *testing.T) {
	for _, source := range []string{"progress", "hidden", "empty", "bot_result", "message_ref", "future_private_kind"} {
		t.Run(source, func(t *testing.T) {
			s, _, item := executionFixture(t)
			r, _, _ := s.StartWorkItem(item.ID, newID())
			_, _ = s.SetRunStatus(r.ID, "running", "")
			if source == "progress" {
				if _, _, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "Not a final receipt"); err != nil {
					t.Fatal(err)
				}
			}
			if source == "hidden" {
				other, _ := s.CreateBot("not the origin", "", "model")
				_, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,seq,role,run_id,content,created_at) VALUES(?,?,1,'assistant',?,'Private child data',?)`, newID(), other.DMConversationID, r.ID, now())
				if err != nil {
					t.Fatal(err)
				}
			}
			if source == "bot_result" || source == "message_ref" || source == "future_private_kind" {
				_, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,run_id,content,created_at) VALUES(?,?,2,'assistant',?,?,'Private anchor in original conversation',?)`, newID(), item.ConversationID, source, r.ID, now())
				if err != nil {
					t.Fatal(err)
				}
			}
			_, _ = s.SetRunStatus(r.ID, "done", "")
			got := readWorkExecution(t, s, item)
			if got.Status != "blocked" || got.Execution.Result != "" || got.Execution.ResultMessageID != "" {
				t.Fatalf("false receipt: %+v %+v", got, got.Execution)
			}
		})
	}
}

func TestWorkExecutionRejectsSupersededOrClosedHiddenRetry(t *testing.T) {
	for _, mode := range []string{"superseded", "done", "cancelled", "reassigned"} {
		t.Run(mode, func(t *testing.T) {
			s, bot, item := executionFixture(t)
			delegate, _ := s.CreateBot("hidden delegate", "", "model")
			root, _, err := s.StartWorkItem(item.ID, newID())
			if err != nil {
				t.Fatal(err)
			}
			_, _ = s.SetRunStatus(root.ID, "running", "")
			_, child, err := s.AddBotMessage(item.ConversationID, bot.ID, delegate.ID, root.ID, "Bounded contribution")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = s.SetRunStatus(root.ID, "done", "")
			_, _ = s.SetRunStatus(child.ID, "failed", "temporary failure")
			if mode == "superseded" {
				next, _, err := s.StartWorkItem(item.ID, newID())
				if err != nil {
					t.Fatal(err)
				}
				finishWorkExecution(t, s, next, "Latest attempt result")
			} else if mode == "reassigned" {
				// Exercise the owner guard independently of DM membership rules.
				if _, err := s.db.Exec(`UPDATE work_items SET bot_id=? WHERE id=?`, delegate.ID, item.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString(mode)}); err != nil {
					t.Fatal(err)
				}
			}
			var before, after int
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&before)
			if _, err := s.RetryRun(child.ID); !errors.Is(err, ErrWorkExecutionConflict) {
				t.Fatalf("stale hidden retry accepted: %v", err)
			}
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&after)
			if before != after {
				t.Fatal("rejected retry created a run")
			}
		})
	}
}

func TestWorkExecutionSurvivesRestartAndDeletion(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := s.CreateBot("restart", "", "model")
	item := workItemCreate(t, s, bot.DMConversationID, bot.ID, "task", "Restart behavior")
	r, _, _ := s.StartWorkItem(item.ID, newID())
	_, _ = s.SetRunStatus(r.ID, "running", "")
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := readWorkExecution(t, s, item)
	if got.Execution.Status != "interrupted" || got.Execution.Active || got.Status != "blocked" {
		t.Fatalf("restart=%+v %+v", got, got.Execution)
	}
	if _, err = s.DeleteBot(bot.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM work_item_executions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan attempt: %d %v", count, err)
	}
}

func TestWorkExecutionHTTPStartsWorkerAndReplaysWithoutModelCall(t *testing.T) {
	var calls atomic.Int32
	engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
		calls.Add(1)
		return Result{Content: "Synthetic board task result"}, nil
	})
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, _ := s.store.CreateBot("board worker", "", "model")
	item := workItemCreate(t, s.store, bot.DMConversationID, bot.ID, "task", "Produce synthetic receipt")
	request := newID()
	post := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/work-items/"+item.ID+"/execute", strings.NewReader(`{"request_id":"`+request+`"}`)))
		return w
	}
	w := post()
	if w.Code != 202 {
		t.Fatalf("start HTTP=%d %s", w.Code, w.Body.String())
	}
	var response struct {
		Run      Run      `json:"run"`
		WorkItem WorkItem `json:"work_item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Run.ID == "" {
		t.Fatalf("response=%s %v", w.Body.String(), err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		got := readWorkExecution(t, s.store, item)
		if got.Status == "review" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not return result: %+v", got.Execution)
		}
		time.Sleep(10 * time.Millisecond)
	}
	w = post()
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatalf("replay HTTP=%d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("model calls=%d", calls.Load())
	}
}
