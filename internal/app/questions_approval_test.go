package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestApprovalRequestPersistsDistinctProposalAndRequiresHumanDecision(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	server := &Server{store: s}
	if _, err := server.approvalTool(c, run).Execute(context.Background(), json.RawMessage(`{"question":"Send?","action":"Send mail","target":"A","impact":"Mail sent","draft_id":"`+uuid.NewString()+`"}`)); err == nil {
		t.Fatal("unrelated or missing draft accepted")
	}
	if _, err := server.approvalTool(c, run).Execute(context.Background(), json.RawMessage(`{"question":"Send?","action":"Send mail","target":"A","impact":"Mail sent","payload":"{\"hidden\":true}"}`)); err == nil {
		t.Fatal("model supplied internal raw approval payload")
	}
	for _, in := range []askQuestionInput{
		{Question: "Proceed?", Type: questionApproval},
		{Question: "Proceed?", Type: questionApproval, Approval: &ApprovalDetails{Action: "restart", Target: "vm-01", Impact: "downtime"}, AllowOther: true},
		{Question: "Proceed?", Type: questionApproval, Approval: &ApprovalDetails{Action: "restart", Target: "vm-01", Impact: "downtime", DraftID: "not-a-uuid"}},
	} {
		if _, err := normalizeQuestionInput(in); err == nil {
			t.Fatalf("invalid approval accepted: %+v", in)
		}
	}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Restart now?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Restart service", Target: "vm-01", Impact: "About 40 seconds offline", ApproveLabel: "Restart", DenyLabel: "Wait"}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.GetQuestion(q.ID)
	if err != nil || reloaded.Approval == nil || reloaded.Approval.Target != "vm-01" || reloaded.Card().Approval.ApproveLabel != "Restart" {
		t.Fatalf("reloaded=%+v err=%v", reloaded, err)
	}
	if _, _, err := s.AnswerQuestion(q.ID, "human", QuestionOtherAnswer{OtherText: "later"}); !errors.Is(err, ErrQuestionInvalidAnswer) {
		t.Fatalf("Other accepted: %v", err)
	}
	if _, _, err := s.AnswerQuestion(q.ID, run.BotID, true); !errors.Is(err, ErrQuestionBotActor) {
		t.Fatalf("bot approval accepted: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/questions/"+q.ID+"/answer", strings.NewReader(`{"value":false}`))
	response := httptest.NewRecorder()
	if !server.routeQuestions(response, req, "questions/"+q.ID+"/answer") || response.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Question QuestionCard `json:"question"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Question.QuestionType != questionApproval || body.Question.Approval.Action != "Restart service" || string(body.Question.Answer) != "false" {
		t.Fatalf("answer=%+v", body.Question)
	}
	if got, duplicate, err := s.AnswerQuestion(q.ID, "human", true); err != nil || !duplicate || string(got.Answer) != "false" {
		t.Fatalf("first decision changed: %+v %v %v", got, duplicate, err)
	}
}
