package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"context"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type questionHTTPTestEngine struct {
	started chan struct{}
	done    chan struct{}
}

func (e *questionHTTPTestEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	for _, tool := range req.Tools {
		if tool.Name != "ask_user_question" {
			continue
		}
		close(e.started)
		answer, err := tool.Execute(ctx, json.RawMessage(`{"question":"Continue?","type":"text"}`))
		close(e.done)
		if err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{Content: answer}, nil
	}
	return runtime.Result{Content: "missing question tool"}, nil
}

func questionFixture(t *testing.T) (*Store, Conversation, Run) {
	t.Helper()
	return questionFixtureDir(t, t.TempDir())
}

func questionFixtureDir(t *testing.T, dir string) (*Store, Conversation, Run) {
	t.Helper()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateQuestions(s.db); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("question bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	return s, c, run
}

func TestQuestionAnswerIsDurableAndIdempotent(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Choose", Type: questionMultiChoice, Options: []QuestionOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, MinSelections: 1, MaxSelections: 2})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	got, duplicate, err := s.AnswerQuestion(q.ID, "human-1", []string{"a"})
	if err != nil || duplicate || got.Status != questionAnswered {
		t.Fatalf("first answer: q=%+v duplicate=%v err=%v", got, duplicate, err)
	}
	got, duplicate, err = s.AnswerQuestion(q.ID, "human-2", []string{"b"})
	if err != nil || !duplicate || string(got.Answer) != `["a"]` {
		t.Fatalf("duplicate answer: q=%+v duplicate=%v err=%v", got, duplicate, err)
	}
	if _, _, err = s.AnswerQuestion(q.ID, run.BotID, []string{"a"}); !errors.Is(err, ErrQuestionBotActor) {
		t.Fatalf("bot answer err=%v", err)
	}
}

func TestQuestionAnswerValidation(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Continue?", Type: questionYesNo})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AnswerQuestion(q.ID, "human", "yes"); !errors.Is(err, ErrQuestionInvalidAnswer) {
		t.Fatalf("string yes accepted: %v", err)
	}
	if _, _, err = s.AnswerQuestion(q.ID, "human", true); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionTypesAndInputLimits(t *testing.T) {
	for _, typ := range []string{questionText, questionYesNo, questionSingleChoice, questionMultiChoice} {
		in := askQuestionInput{Question: "Q", Type: typ}
		if typ == questionSingleChoice || typ == questionMultiChoice {
			in.Options = []QuestionOption{{ID: "a", Label: "A"}}
		}
		if _, err := normalizeQuestionInput(in); err != nil {
			t.Fatalf("type %s: %v", typ, err)
		}
	}
	if _, err := normalizeQuestionInput(askQuestionInput{Question: strings.Repeat("x", 4001), Type: questionText}); err == nil {
		t.Fatal("oversized prompt accepted")
	}
	in := askQuestionInput{Question: "Q", Type: questionSingleChoice, Options: make([]QuestionOption, 33)}
	if _, err := normalizeQuestionInput(in); err == nil {
		t.Fatal("too many options accepted")
	}
}

func TestQuestionCancelAndExpiryAreNormalToolResults(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Continue?", Type: questionText, ExpiresInSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan string, 1)
	go func() {
		answer, waitErr := (&Server{store: s}).WaitQuestion(context.Background(), q.ID)
		if waitErr != nil {
			result <- waitErr.Error()
			return
		}
		result <- string(answer)
	}()
	time.Sleep(30 * time.Millisecond)
	if _, err = s.CancelQuestion(q.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got != `{"status":"cancelled"}` {
			t.Fatalf("cancel result=%s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not wake waiter")
	}

	in.ExpiresInSeconds = 1
	q, err = s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	answer, waitErr := (&Server{store: s}).WaitQuestion(context.Background(), q.ID)
	if waitErr != nil || string(answer) != `{"status":"expired"}` {
		t.Fatalf("expiry answer=%s err=%v", answer, waitErr)
	}
}

func TestQuestionHTTPAnswerResumesToolAndPreservesFirstAnswer(t *testing.T) {
	e := &questionHTTPTestEngine{started: make(chan struct{}), done: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("question http", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "ask", "question-http")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, run)
	<-e.started
	var pending []Question
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		pending, err = s.store.PendingQuestions(c.ID)
		if err == nil && len(pending) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	body := bytes.NewBufferString(`{"text":"first"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/questions/"+pending[0].ID+"/answer", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("answer status=%d body=%s", res.Code, res.Body.String())
	}
	body = bytes.NewBufferString(`{"text":"second"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/questions/"+pending[0].ID+"/answer", body)
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d", res.Code)
	}
	<-e.done
	q, err := s.store.GetQuestion(pending[0].ID)
	if err != nil || string(q.Answer) != `"first"` {
		t.Fatalf("question=%+v err=%v", q, err)
	}
	select {
	case <-time.After(time.Second):
		t.Fatal("run did not finish")
	case <-func() chan struct{} {
		ch := make(chan struct{})
		go func() {
			for {
				r, _ := s.store.GetRun(run.ID)
				if r.Status == "done" {
					close(ch)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		return ch
	}():
	}
}
