package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func otherQuestionDefinition(kind string, enabled bool) askQuestionInput {
	in := askQuestionInput{Question: "Choose an answer", Type: kind, AllowOther: enabled}
	if kind == questionSingleChoice || kind == questionMultiChoice {
		in.Options = []QuestionOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}
	}
	if kind == questionForm {
		in.SourceURL = "https://example.test/profile"
		in.Fields = []UserFormField{{ID: "name", Label: "Name", Type: "text", Required: true}}
	}
	return in
}

func createOtherQuestion(t *testing.T, s *Store, c Conversation, run Run, in askQuestionInput) Question {
	t.Helper()
	in, err := normalizeQuestionInput(in)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func answerOtherQuestionHTTP(s *Server, id, body string) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/questions/"+id+"/answer", strings.NewReader(body))
	s.routeQuestions(res, req, "questions/"+id+"/answer")
	return res
}

func TestQuestionOtherInputAndSchema(t *testing.T) {
	for _, kind := range []string{questionYesNo, questionSingleChoice, questionMultiChoice, questionText, questionForm} {
		for _, enabled := range []bool{false, true} {
			in, err := normalizeQuestionInput(otherQuestionDefinition(kind, enabled))
			if enabled && (kind == questionText || kind == questionForm) {
				if err == nil {
					t.Fatalf("%s accepted allow_other", kind)
				}
				continue
			}
			if err != nil || in.AllowOther != enabled {
				t.Fatalf("%s enabled=%v input=%+v err=%v", kind, enabled, in, err)
			}
			if kind != questionText && kind != questionForm && len(in.Options) != 2 {
				t.Fatal("Other must not invent an option identifier")
			}
		}
	}
	for _, tc := range []struct {
		enabled  bool
		min, max int
		valid    bool
	}{{true, 3, 3, true}, {false, 0, 3, false}, {true, 0, 4, false}, {true, 4, 0, false}, {true, 3, 2, false}} {
		in := otherQuestionDefinition(questionMultiChoice, tc.enabled)
		in.MinSelections, in.MaxSelections = tc.min, tc.max
		if _, err := normalizeQuestionInput(in); (err == nil) != tc.valid {
			t.Fatalf("bounds %+v: %v", tc, err)
		}
	}
	s, c, run := questionFixture(t)
	defer s.Close()
	tools := (&Server{store: s}).questionTools(c, run)
	properties := tools[0].Parameters["properties"].(map[string]any)
	flag, ok := properties["allow_other"].(map[string]any)
	if !ok || flag["type"] != "boolean" || flag["default"] != false {
		t.Fatalf("allow_other schema=%+v", flag)
	}
	if _, ok := tools[1].Parameters["properties"].(map[string]any)["allow_other"]; ok {
		t.Fatal("form tool advertises Other")
	}
}

func TestQuestionOtherToolFlagAndResult(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			s, c, run := questionFixture(t)
			defer s.Close()
			server := &Server{store: s, closing: true}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan string, 1)
			go func() {
				value, err := server.questionTools(c, run)[0].Execute(ctx, json.RawMessage(fmt.Sprintf(`{"question":"Continue?","type":"yes_no","allow_other":%t}`, enabled)))
				if err != nil {
					result <- err.Error()
					return
				}
				result <- value
			}()
			var q Question
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
				pending, err := s.PendingQuestions(c.ID)
				if err == nil && len(pending) == 1 {
					q = pending[0]
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if q.ID == "" || q.AllowOther != enabled || q.Card().AllowOther != enabled {
				t.Fatalf("pending card=%+v", q)
			}
			card, err := json.Marshal(q.Card())
			if err != nil || strings.Contains(string(card), `"allow_other":true`) != enabled || strings.Contains(string(card), `"allow_other":false`) {
				t.Fatalf("card=%s error=%v", card, err)
			}
			body, want := `{"value":true}`, `true`
			if enabled {
				body, want = `{"values":[],"other_text":"Ask me later"}`, `{"values":[],"other_text":"Ask me later"}`
			}
			res := answerOtherQuestionHTTP(server, q.ID, body)
			if res.Code != http.StatusOK {
				t.Fatalf("answer: %d %s", res.Code, res.Body)
			}
			select {
			case got := <-result:
				if got != want {
					t.Fatalf("tool result=%s want=%s", got, want)
				}
			case <-ctx.Done():
				t.Fatal("tool did not return its answer")
			}
		})
	}
}

func TestQuestionOtherDurableRestartAndContinuation(t *testing.T) {
	dir := t.TempDir()
	s, c, run := questionFixtureDir(t, dir)
	defer func() { s.Close() }()
	q := createOtherQuestion(t, s, c, run, otherQuestionDefinition(questionMultiChoice, true))
	if err := s.SaveInputContinuation(context.Background(), run.ID, q.ID, json.RawMessage(`{"version":1,"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.GetQuestion(q.ID)
	if err != nil || !pending.AllowOther || pending.Status != questionPending {
		t.Fatalf("reloaded question=%+v err=%v", pending, err)
	}
	answer := QuestionOtherAnswer{Values: []string{"b"}, OtherText: "  自定义回答  "}
	answered, duplicate, err := s.AnswerQuestion(q.ID, "human", answer)
	if err != nil || duplicate {
		t.Fatalf("answer=%+v duplicate=%v err=%v", answered, duplicate, err)
	}
	want := `{"values":["b"],"other_text":"  自定义回答  "}`
	if string(answered.Answer) != want {
		t.Fatalf("answer=%s", answered.Answer)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetQuestion(q.ID)
	if err != nil || !loaded.AllowOther || string(loaded.Answer) != want || loaded.AnsweredBy != "human" {
		t.Fatalf("durable answer=%+v err=%v", loaded, err)
	}
	waited, err := (&Server{store: s}).WaitQuestion(context.Background(), q.ID)
	if err != nil || string(waited) != want {
		t.Fatalf("wait=%s err=%v", waited, err)
	}
	got, duplicate, err := s.AnswerQuestion(q.ID, "human-2", []string{"a"})
	if err != nil || !duplicate || string(got.Answer) != want || got.AnsweredBy != "human" {
		t.Fatalf("duplicate=%+v %v %v", got, duplicate, err)
	}
	if _, _, err = s.AnswerQuestion(q.ID, run.BotID, answer); !errors.Is(err, ErrQuestionBotActor) {
		t.Fatalf("bot actor=%v", err)
	}
	if _, ok, err := s.nextQueuedRun(c.ID); err != nil || !ok {
		t.Fatalf("ready continuation=%v %v", ok, err)
	}
	_, continued, claimed, err := s.claimInputContinuation(context.Background(), run.ID)
	if err != nil || !claimed || string(continued.Answer) != want {
		t.Fatalf("continuation answer=%+v claimed=%v err=%v", continued, claimed, err)
	}
	if _, _, claimed, err = s.claimInputContinuation(context.Background(), run.ID); err != nil || claimed {
		t.Fatalf("duplicate continuation claimed=%v err=%v", claimed, err)
	}
}

func TestQuestionOtherMigrationDefaultsLegacyOff(t *testing.T) {
	dir := t.TempDir()
	s, c, run := questionFixtureDir(t, dir)
	defer func() { s.Close() }()
	q := createOtherQuestion(t, s, c, run, otherQuestionDefinition(questionYesNo, false))
	if _, _, err := s.AnswerQuestion(q.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	// Simulate a database created before the additive allow_other migration.
	if _, err := s.db.Exec(`ALTER TABLE questions DROP COLUMN allow_other`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetQuestion(q.ID)
	if err != nil || got.AllowOther || string(got.Answer) != "false" {
		t.Fatalf("migrated question=%+v err=%v", got, err)
	}
	if err = migrateQuestions(s.db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestQuestionOtherHTTPContract(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	// The real route and store run without launching unrelated model workers.
	server := &Server{store: s, closing: true}
	cases := []struct {
		name, kind, body, want string
		disabled               bool
		min, max               int
	}{
		{name: "yes_no Other", kind: questionYesNo, body: `{"values":[],"other_text":"Later"}`, want: `{"values":[],"other_text":"Later"}`},
		{name: "single Other", kind: questionSingleChoice, body: `{"values":[],"other_text":"C"}`, want: `{"values":[],"other_text":"C"}`},
		{name: "multi Other only", kind: questionMultiChoice, body: `{"values":[],"other_text":"C"}`, want: `{"values":[],"other_text":"C"}`, min: 1, max: 1},
		{name: "multi combined", kind: questionMultiChoice, body: `{"values":["a","b"],"other_text":"C"}`, want: `{"values":["a","b"],"other_text":"C"}`, min: 3, max: 3},
		{name: "unicode limit", kind: questionSingleChoice, body: `{"values":[],"other_text":"` + strings.Repeat("字", 4000) + `"}`, want: `{"values":[],"other_text":"` + strings.Repeat("字", 4000) + `"}`},
		{name: "canonical empty values", kind: questionSingleChoice, body: `{"other_text":"C"}`, want: `{"values":[],"other_text":"C"}`},
		{name: "ordinary yes", kind: questionYesNo, body: `{"value":true}`, want: `true`},
		{name: "ordinary no", kind: questionYesNo, body: `{"value":false}`, want: `false`, disabled: true},
		{name: "ordinary single", kind: questionSingleChoice, body: `{"values":["a"]}`, want: `"a"`},
		{name: "legacy single", kind: questionSingleChoice, body: `{"values":["b"]}`, want: `"b"`, disabled: true},
		{name: "ordinary multi", kind: questionMultiChoice, body: `{"values":["b","a"]}`, want: `["b","a"]`},
		{name: "legacy multi", kind: questionMultiChoice, body: `{"values":["a"]}`, want: `["a"]`, disabled: true},
		{name: "disabled", kind: questionSingleChoice, body: `{"values":[],"other_text":"C"}`, disabled: true},
		{name: "text Other", kind: questionText, body: `{"other_text":"C"}`, disabled: true},
		{name: "form Other", kind: questionForm, body: `{"other_text":"C"}`, disabled: true},
		{name: "empty", kind: questionSingleChoice, body: `{"other_text":""}`},
		{name: "whitespace", kind: questionSingleChoice, body: `{"other_text":" \t\n　"}`},
		{name: "too long", kind: questionSingleChoice, body: `{"other_text":"` + strings.Repeat("字", 4001) + `"}`},
		{name: "null", kind: questionSingleChoice, body: `{"other_text":null}`},
		{name: "wrong type", kind: questionSingleChoice, body: `{"other_text":true}`},
		{name: "mixed yes", kind: questionYesNo, body: `{"value":true,"other_text":"C"}`},
		{name: "mixed null value", kind: questionSingleChoice, body: `{"value":null,"other_text":"C"}`},
		{name: "mixed empty text", kind: questionSingleChoice, body: `{"text":"","other_text":"C"}`},
		{name: "mixed null text", kind: questionSingleChoice, body: `{"text":null,"other_text":"C"}`},
		{name: "mixed fields", kind: questionSingleChoice, body: `{"fields":{},"other_text":"C"}`},
		{name: "mixed null fields", kind: questionSingleChoice, body: `{"fields":null,"other_text":"C"}`},
		{name: "single plus normal", kind: questionSingleChoice, body: `{"values":["a"],"other_text":"C"}`},
		{name: "yes plus normal", kind: questionYesNo, body: `{"values":["yes"],"other_text":"C"}`},
		{name: "unknown", kind: questionMultiChoice, body: `{"values":["unknown"],"other_text":"C"}`},
		{name: "duplicate", kind: questionMultiChoice, body: `{"values":["a","a"],"other_text":"C"}`},
		{name: "under min", kind: questionMultiChoice, body: `{"values":[],"other_text":"C"}`, min: 2},
		{name: "over max", kind: questionMultiChoice, body: `{"values":["a"],"other_text":"C"}`, max: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := otherQuestionDefinition(tc.kind, !tc.disabled)
			in.MinSelections, in.MaxSelections = tc.min, tc.max
			q := createOtherQuestion(t, s, c, run, in)
			defer s.CancelQuestion(q.ID)
			res := answerOtherQuestionHTTP(server, q.ID, tc.body)
			got, err := s.GetQuestion(q.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if res.Code != http.StatusBadRequest || got.Status != questionPending || len(got.Answer) != 0 {
					t.Fatalf("invalid answer result: status=%d question=%+v body=%s", res.Code, got, res.Body)
				}
				return
			}
			if res.Code != http.StatusOK || string(got.Answer) != tc.want || got.AnsweredBy != "human" {
				t.Fatalf("status=%d answer=%s actor=%s response=%s", res.Code, got.Answer, got.AnsweredBy, res.Body)
			}
			var response struct{ Question QuestionCard }
			if err = json.Unmarshal(res.Body.Bytes(), &response); err != nil || string(response.Question.Answer) != tc.want || response.Question.AllowOther != in.AllowOther {
				t.Fatalf("response=%s err=%v", res.Body, err)
			}
			if retry := answerOtherQuestionHTTP(server, q.ID, tc.body); retry.Code != http.StatusOK {
				t.Fatalf("duplicate answer: %d %s", retry.Code, retry.Body)
			}
		})
	}
}
