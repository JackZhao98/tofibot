package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

const publishedAgenda = "0–25 分钟讨论；25–30 分钟确认决策、负责人和截止时间。"

type intermediateHandoffEngine struct {
	child         string
	childRequests chan runtime.Request
}

func (e *intermediateHandoffEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.BotID == e.child {
		e.childRequests <- req
		return runtime.Result{Content: "已检查完整议程，保留结尾的行动项确认。"}, nil
	}
	for _, message := range req.Messages {
		if strings.Contains(message.Content, "[completed colleague task]") {
			return runtime.Result{Content: "已整合评审结果并完成议程。"}, nil
		}
	}
	if req.OnAssistantTurn == nil {
		return runtime.Result{}, errors.New("missing assistant turn callback")
	}
	req.OnDelta("0–25 分钟讨论；")
	req.OnDelta("25–30 分钟确认决策、负责人和截止时间。")
	if err := req.OnAssistantTurn(1, publishedAgenda); err != nil {
		return runtime.Result{}, err
	}
	for _, tool := range req.Tools {
		if tool.Name == "handoff" {
			args, _ := json.Marshal(map[string]string{"bot_id": e.child, "task": "检查刚才的议程，不要遗漏结尾的行动项。"})
			if _, err := tool.Execute(ctx, args); err != nil {
				return runtime.Result{}, err
			}
			req.OnDelta("已交接给评审。")
			return runtime.Result{Content: "已交接给评审。"}, nil
		}
	}
	return runtime.Result{}, errors.New("missing handoff tool")
}

func TestGroupHandoffReceivesPublishedIntermediateContribution(t *testing.T) {
	e := &intermediateHandoffEngine{childRequests: make(chan runtime.Request, 1)}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.store.CreateBot("策划", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("评审", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	e.child = b.ID
	group, err := s.store.CreateGroup("讨论", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(group.ID, a.ID, "请发布议程再交接评审", "publication-handoff")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(group, run)
	select {
	case req := <-e.childRequests:
		found := false
		for _, message := range req.Messages {
			found = found || strings.Contains(message.Content, publishedAgenda)
		}
		if !found {
			t.Fatal("child context omitted the parent's completed intermediate contribution")
		}
		waitForRunStatus(t, s, req.RunID, "done")
	case <-time.After(3 * time.Second):
		t.Fatal("child was not executed")
	}
	waitForRunStatus(t, s, run.ID, "done")
	runs, _ := s.store.Runs(group.ID)
	if len(runs) < 3 {
		t.Fatalf("expected durable requester followup, runs=%d", len(runs))
	}
	followup, ok, err := s.store.GroupFollowupForRun(runs[1].ID)
	if err != nil || !ok {
		t.Fatalf("missing requester followup: ok=%v err=%v", ok, err)
	}
	waitForRunStatus(t, s, followup.ID, "done")
	messages, _, err := s.store.Messages(group.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var agenda, notice, final Message
	for _, message := range messages {
		if message.Content == publishedAgenda {
			agenda = message
		}
		if message.Kind == "notice" {
			notice = message
		}
		if message.Content == "已交接给评审。" {
			final = message
		}
	}
	if agenda.ID == "" || final.ID == "" || agenda.ID == final.ID || agenda.Seq >= notice.Seq || agenda.SenderBotID != a.ID || final.SenderBotID != a.ID {
		t.Fatalf("intermediate/final identity or publication order incorrect: agenda=%+v notice=%+v final=%+v", agenda, notice, final)
	}
}

type ignoredPublicationErrorEngine struct{}

func (ignoredPublicationErrorEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	req.OnDelta("completed intermediate")
	_ = req.OnAssistantTurn(1, "completed intermediate")
	return runtime.Result{Content: "late final after publication failure"}, nil
}

func TestExecutePublicationFailureDoesNotPublishLateFinal(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: ignoredPublicationErrorEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("writer", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "test", "publication-error")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`CREATE TRIGGER reject_message_event BEFORE INSERT ON events WHEN NEW.type='message' BEGIN SELECT RAISE(ABORT,'publication event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	s.execute(c, run)
	got, err := s.store.GetRun(run.ID)
	if err != nil || got.Status != "failed" || !strings.Contains(got.Error, "publication event rejected") {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil || len(messages) != 1 {
		t.Fatalf("late result persisted: messages=%+v err=%v", messages, err)
	}
	draft, err := s.store.StreamDraft(run.ID)
	if err != nil || draft.Status != streamDraftCancelled {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
}

func TestExecuteFinalPersistenceFailureMarksRunFailed(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("writer", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "test", "final-error")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`CREATE TRIGGER reject_final_event BEFORE INSERT ON events WHEN NEW.type='message' BEGIN SELECT RAISE(ABORT,'final event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	s.execute(c, run)
	got, err := s.store.GetRun(run.ID)
	if err != nil || got.Status != "failed" || !strings.Contains(got.Error, "final event rejected") {
		t.Fatalf("run=%+v err=%v", got, err)
	}
}
