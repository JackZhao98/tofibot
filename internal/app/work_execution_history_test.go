package app

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestListWorkItemExecutionsOrdersAttemptsAndPreservesLatest(t *testing.T) {
	s, _, item := executionFixture(t)
	first, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	firstMessage := finishWorkExecution(t, s, first, "First published result")
	if _, err := s.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("todo")}); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(second.ID, "failed", "Second attempt failed"); !ok || err != nil {
		t.Fatalf("fail second: %v %v", ok, err)
	}
	third, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	thirdMessage := finishWorkExecution(t, s, third, "Third published result")
	latestBefore := *readWorkExecution(t, s, item).Execution

	history, err := s.ListWorkItemExecutions(item.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history length=%d", len(history))
	}
	if history[0].RootRunID != third.ID || history[0].Status != "done" || history[0].ResultMessageID != thirdMessage.ID || history[0].Result != thirdMessage.Content {
		t.Fatalf("newest attempt=%+v", history[0])
	}
	if history[1].RootRunID != second.ID || history[1].Status != "failed" || history[1].Error != "Second attempt failed" || history[1].Result != "" || history[1].ResultMessageID != "" {
		t.Fatalf("failed attempt=%+v", history[1])
	}
	if history[2].RootRunID != first.ID || history[2].Status != "done" || history[2].ResultMessageID != firstMessage.ID || history[2].Result != firstMessage.Content {
		t.Fatalf("oldest attempt=%+v", history[2])
	}
	latestAfter := *readWorkExecution(t, s, item).Execution
	if !reflect.DeepEqual(latestAfter, latestBefore) || !reflect.DeepEqual(latestAfter, history[0]) {
		t.Fatalf("latest changed: before=%+v after=%+v history=%+v", latestBefore, latestAfter, history[0])
	}
}

func TestListWorkItemExecutionsBoundsAndUnknownItem(t *testing.T) {
	s, _, item := executionFixture(t)
	runs := make([]Run, 0, 101)
	for i := 0; i < 101; i++ {
		run, _, err := s.StartWorkItem(item.ID, newID())
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		if ok, err := s.SetRunStatus(run.ID, "failed", "bounded failure"); !ok || err != nil {
			t.Fatalf("fail %d: %v %v", i, ok, err)
		}
		runs = append(runs, run)
	}
	defaultHistory, err := s.ListWorkItemExecutions(item.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultHistory) != 20 || defaultHistory[0].RootRunID != runs[100].ID || defaultHistory[19].RootRunID != runs[81].ID {
		t.Fatalf("default history=%d first=%+v last=%+v", len(defaultHistory), defaultHistory[0], defaultHistory[19])
	}
	maximumHistory, err := s.ListWorkItemExecutions(item.ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(maximumHistory) != 100 || maximumHistory[0].RootRunID != runs[100].ID || maximumHistory[99].RootRunID != runs[1].ID {
		t.Fatalf("maximum history=%d first=%+v last=%+v", len(maximumHistory), maximumHistory[0], maximumHistory[99])
	}
	if _, err := s.ListWorkItemExecutions(newID(), 1); err != sql.ErrNoRows {
		t.Fatalf("unknown work item error=%v", err)
	}
}

func TestListWorkItemExecutionsScopesConversationAndActor(t *testing.T) {
	s, owner, item := executionFixture(t)
	other, err := s.CreateBot("other conversation", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.listWorkItemExecutions(item.ID, 1, other.DMConversationID, owner.ID); !errors.Is(err, ErrWorkItemScope) {
		t.Fatalf("wrong conversation error=%v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, item.ConversationID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.listWorkItemExecutions(item.ID, 1, item.ConversationID, owner.ID); err == nil || !strings.Contains(err.Error(), "not a conversation member") {
		t.Fatalf("removed actor error=%v", err)
	}
}

func TestListWorkItemExecutionsDoesNotLeakHiddenChildResultsOrErrors(t *testing.T) {
	s, owner, item := executionFixture(t)
	delegate, err := s.CreateBot("private delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	failedRoot, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(failedRoot.ID, "running", ""); !ok || err != nil {
		t.Fatalf("claim failed attempt: %v %v", ok, err)
	}
	_, failedChild, err := s.AddBotMessage(item.ConversationID, owner.ID, delegate.ID, failedRoot.ID, "Private contribution")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(failedRoot.ID, "done", ""); !ok || err != nil {
		t.Fatalf("finish failed root: %v %v", ok, err)
	}
	if ok, err := s.SetRunStatus(failedChild.ID, "failed", "private child error"); !ok || err != nil {
		t.Fatalf("fail hidden child: %v %v", ok, err)
	}
	receiptRoot, _, err := s.StartWorkItem(item.ID, newID())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(receiptRoot.ID, "running", ""); !ok || err != nil {
		t.Fatalf("claim receipt attempt: %v %v", ok, err)
	}
	_, receiptChild, err := s.AddBotMessage(item.ConversationID, owner.ID, delegate.ID, receiptRoot.ID, "Another private contribution")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(receiptRoot.ID, "done", ""); !ok || err != nil {
		t.Fatalf("finish receipt root: %v %v", ok, err)
	}
	if ok, err := s.SetRunStatus(receiptChild.ID, "running", ""); !ok || err != nil {
		t.Fatalf("claim hidden receipt: %v %v", ok, err)
	}
	if ok, err := s.SetRunStatus(receiptChild.ID, "done", ""); !ok || err != nil {
		t.Fatalf("finish hidden receipt: %v %v", ok, err)
	}
	privateReceiptID := newID()
	if _, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,seq,role,run_id,content,created_at) VALUES(?,?,2,'assistant',?,'private child receipt',?)`, privateReceiptID, receiptChild.ConversationID, receiptChild.ID, now()); err != nil {
		t.Fatal(err)
	}

	history, err := s.ListWorkItemExecutions(item.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history length=%d", len(history))
	}
	if history[0].RootRunID != receiptRoot.ID || history[0].Result != "" || history[0].ResultMessageID != "" || !strings.Contains(history[0].Error, "没有在原对话发布") {
		t.Fatalf("hidden receipt leaked: %+v", history[0])
	}
	if history[1].RootRunID != failedRoot.ID || history[1].Error != "协作执行未完成，请在原对话查看进度或停止本轮后重试。" || history[1].Result != "" || history[1].ResultMessageID != "" {
		t.Fatalf("hidden error leaked: %+v", history[1])
	}
	for _, execution := range history {
		if strings.Contains(execution.Error, "private child") || strings.Contains(execution.Result, "private child") || execution.ResultMessageID == privateReceiptID {
			t.Fatalf("private child data exposed: %+v", execution)
		}
	}
}
