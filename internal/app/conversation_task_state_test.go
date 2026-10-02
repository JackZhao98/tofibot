package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type approvalTaskEngine struct {
	started chan struct{}
}

func (e *approvalTaskEngine) Run(ctx context.Context, request runtime.Request) (runtime.Result, error) {
	for _, tool := range request.Tools {
		if tool.Name != "request_approval" {
			continue
		}
		close(e.started)
		answer, err := tool.Execute(ctx, json.RawMessage(`{"question":"Approve this synthetic work?","action":"Complete synthetic work","target":"isolated fixture","impact":"No external action"}`))
		if err != nil {
			return runtime.Result{}, err
		}
		if answer == "true" {
			return runtime.Result{Content: "Synthetic result delivered"}, nil
		}
		return runtime.Result{Content: "Synthetic action was declined"}, nil
	}
	return runtime.Result{Content: "approval tool unavailable"}, nil
}

func TestWorkspaceTaskStatusApprovalContinuationThroughHTTP(t *testing.T) {
	engine := &approvalTaskEngine{started: make(chan struct{})}
	server, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bot, err := server.store.CreateBot("synthetic status bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := server.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := server.store.AddUserRun(conv.ID, bot.ID, "Please do the synthetic task", "status-fixture")
	if err != nil {
		t.Fatal(err)
	}
	server.enqueue(conv, run)
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("engine did not start")
	}
	var pending []Question
	for until := time.Now().Add(time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		pending, err = server.store.PendingQuestions(conv.ID)
		if err == nil && len(pending) == 1 {
			break
		}
	}
	if len(pending) != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	before, err := server.store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].TaskState == nil || before[0].TaskState.QuestionID != pending[0].ID {
		t.Fatalf("before=%+v", before)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/questions/"+pending[0].ID+"/answer", bytes.NewBufferString(`{"value":true}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("answer=%d %s", response.Code, response.Body.String())
	}
	for until := time.Now().Add(time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		current, e := server.store.GetRun(run.ID)
		if e == nil && current.Status == "done" {
			break
		}
	}
	current, err := server.store.GetRun(run.ID)
	if err != nil || current.Status != "done" {
		t.Fatalf("run=%+v err=%v", current, err)
	}
	after, err := server.store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].TaskState == nil || after[0].TaskState.Status != "completed" || after[0].TaskState.ResultMessageID == "" {
		t.Fatalf("after=%+v", after)
	}
	// A fresh workspace request uses the persisted result, not an in-memory
	// optimistic state from the approval interaction.
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/conversations", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list=%d %s", listResponse.Code, listResponse.Body.String())
	}
	var listed struct {
		Conversations []Conversation `json:"conversations"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Conversations) != 1 || listed.Conversations[0].TaskState == nil || listed.Conversations[0].TaskState.ResultMessageID != after[0].TaskState.ResultMessageID {
		t.Fatalf("refreshed=%+v", listed)
	}
}

func TestConversationTaskStateTracksCrossConversationHandoffUntilReturn(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.CreateBot("A", "", "model")
	b, _ := s.CreateBot("B", "", "model")
	other, _ := s.CreateBot("unrelated", "", "model")
	origin := a.DMConversationID
	state := func() *ConversationTaskState {
		t.Helper()
		items, err := s.ListConversations()
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == origin {
				return item.TaskState
			}
		}
		t.Fatal("origin conversation missing")
		return nil
	}
	_, parent, _, err := s.AddUserRun(origin, a.ID, "Ask B to check the synthetic item", "relay-state")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(parent.ID, "running", ""); !ok || err != nil {
		t.Fatalf("start parent: %v %v", ok, err)
	}
	_, child, err := s.AddForwardHandoff(origin, a.ID, b.ID, parent.ID, "Check the item")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.FinishRun(parent.ID, origin, a.ID, "I asked B to check it."); !ok || err != nil {
		t.Fatalf("finish parent: %v %v", ok, err)
	}
	if got := state(); got == nil || got.Status != "executing" || got.RunID != child.ID {
		t.Fatalf("queued colleague appears finished: %+v", got)
	}
	if ok, err := s.SetRunStatus(child.ID, "running", ""); !ok || err != nil {
		t.Fatalf("start colleague: %v %v", ok, err)
	}
	if got := state(); got == nil || got.Status != "executing" {
		t.Fatalf("running colleague state: %+v", got)
	}
	if ok, err := s.SetRunStatus(child.ID, "failed", "private synthetic diagnostic"); !ok || err != nil {
		t.Fatalf("fail colleague: %v %v", ok, err)
	}
	if got := state(); got == nil || got.Status != "failed" || got.CanRetry || got.FailureReason == "private synthetic diagnostic" {
		t.Fatalf("failed colleague state: %+v", got)
	}
	retry, err := s.RetryRun(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := state(); got == nil || got.Status != "executing" || got.RunID != retry.ID {
		t.Fatalf("retry state: %+v", got)
	}
	if ok, err := s.SetRunStatus(retry.ID, "running", ""); !ok || err != nil {
		t.Fatalf("start retry: %v %v", ok, err)
	}
	if _, ok, err := s.FinishRun(retry.ID, b.DMConversationID, b.ID, "B checked the item"); !ok || err != nil {
		t.Fatalf("finish colleague: %v %v", ok, err)
	}
	if got := state(); got == nil || got.Status != "executing" {
		t.Fatalf("A continuation was lost: %+v", got)
	}
	followup, ok, err := s.DirectMessageFollowupForRun(retry.ID)
	if err != nil || !ok {
		t.Fatalf("A continuation: %v %v", ok, err)
	}
	if ok, err := s.SetRunStatus(followup.ID, "running", ""); !ok || err != nil {
		t.Fatalf("start A continuation: %v %v", ok, err)
	}
	answer, ok, err := s.FinishRun(followup.ID, origin, a.ID, "Here is the checked result")
	if !ok || err != nil {
		t.Fatalf("finish A continuation: %v %v", ok, err)
	}
	if got := state(); got == nil || got.Status != "completed" || got.ResultMessageID != answer.ID {
		t.Fatalf("final result: %+v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := state(); got == nil || got.Status != "completed" || got.ResultMessageID != answer.ID {
		t.Fatalf("reopened result: %+v", got)
	}
	items, err := s.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == other.DMConversationID && item.TaskState != nil {
			t.Fatalf("relay state leaked to unrelated conversation: %+v", item.TaskState)
		}
	}
}

func TestConversationTaskStateFollowsApprovalDecisionAndDelivery(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	state := func() Conversation {
		t.Helper()
		items, err := s.ListConversations()
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == c.ID {
				return item
			}
		}
		t.Fatal("conversation missing")
		return Conversation{}
	}
	if got := state().TaskState; got == nil || got.Status != "executing" || got.RunID != run.ID {
		t.Fatalf("running=%+v", got)
	}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Approve the example action?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Example action", Target: "Example target", Impact: "Example impact"}})
	if err != nil {
		t.Fatal(err)
	}
	question, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := state().TaskState; got == nil || got.Status != "needs_attention" || got.QuestionID != question.ID {
		t.Fatalf("approval=%+v", got)
	}
	if _, _, err := s.AnswerQuestion(question.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	if got := state().TaskState; got == nil || got.Status != "executing" || got.QuestionID != "" {
		t.Fatalf("decision=%+v", got)
	}
	if _, err := s.SetRunStatus(run.ID, "failed", "example failure"); err != nil {
		t.Fatal(err)
	}
	if got := state().TaskState; got == nil || got.Status != "failed" || got.FailureReason != "example failure" {
		t.Fatalf("failure=%+v", got)
	}
	next, err := s.AddRun(c.ID, run.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET origin_conversation_id=? WHERE id=?`, c.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(next.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if got := state().TaskState; got == nil || got.Status != "executing" || got.RunID != next.ID {
		t.Fatalf("retry running=%+v", got)
	}
	result, _, err := s.AddMessage(c.ID, "assistant", run.BotID, next.ID, "Actual delivered result", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(next.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	latest := state()
	if got := latest.TaskState; got == nil || got.Status != "completed" || got.RunID != next.ID || got.ResultMessageID != result.ID {
		t.Fatalf("completion=%+v", got)
	}
	if latest.UnreadCount == 0 {
		t.Fatal("task completion replaced independent unread count")
	}
}

func TestConversationTaskStateSurvivesReopenAndRetryIsSingleUse(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := s.CreateBot("status fixture", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv := bot.DMConversationID
	_, original, _, err := s.AddUserRun(conv, bot.ID, "synthetic task", "same-client-send")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(original.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(original.ID, "failed", "fixture failure"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryRun(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RetryRun(original.ID); err == nil {
		t.Fatal("a second retry could duplicate an external action")
	}
	if _, err = s.SetRunStatus(retry.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	result, _, err := s.AddMessage(conv, "assistant", bot.ID, retry.ID, "persisted delivery", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(retry.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	items, err := reopened.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TaskState == nil || items[0].TaskState.Status != "completed" || items[0].TaskState.RunID != retry.ID || items[0].TaskState.ResultMessageID != result.ID {
		t.Fatalf("reopened state=%+v", items)
	}
}

func TestConversationTaskStateUsesMailDraftOutcome(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	draft, err := s.AddMailDraft(context.Background(), c.ID, run, "example@example.test", "Synthetic review", "No email is sent", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(run.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	state := func() *ConversationTaskState {
		t.Helper()
		items, err := s.ListConversations()
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == c.ID {
				return item.TaskState
			}
		}
		t.Fatal("conversation missing")
		return nil
	}
	if got := state(); got == nil || got.Status != "needs_attention" || got.DraftStatus != "pending" || !got.DraftDemo {
		t.Fatalf("pending draft=%+v", got)
	}
	for _, next := range []struct{ from, to, task string }{
		{"pending", "sending", "executing"},
		{"sending", "unknown", "needs_attention"},
		{"unknown", "sent", "completed"},
	} {
		if _, err := s.ChangeMailDraftStatus(draft.ID, draft.Revision, next.from, next.to); err != nil {
			t.Fatal(err)
		}
		if got := state(); got == nil || got.Status != next.task || got.DraftStatus != next.to || got.DraftID != draft.ID {
			t.Fatalf("draft transition %s=%+v", next.to, got)
		}
	}
}
