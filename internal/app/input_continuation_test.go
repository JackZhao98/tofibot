package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Store tests treat checkpoint contents as opaque; runtime tests validate the
// versioned payload. All values and model responses here are synthetic.
func parkedInputFixture(t *testing.T) (*Store, Conversation, Run, Question) {
	t.Helper()
	s, c, r := questionFixture(t)
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Which value?", Type: questionText})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, r, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running"} {
		if err = s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: "wait-call", Name: "ask_user_question", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{"version":1,"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	return s, c, r, q
}

func TestInputContinuationClaimIsAtomicAndSingleUse(t *testing.T) {
	s, c, r, q := parkedInputFixture(t)
	defer s.Close()
	if _, ok, err := s.nextQueuedRun(c.ID); err != nil || ok {
		t.Fatalf("pending selected: %v %v", ok, err)
	}
	if _, _, err := s.AnswerQuestion(q.ID, "human", "answer"); err != nil {
		t.Fatal(err)
	}
	if _, duplicate, err := s.AnswerQuestion(q.ID, "human", "changed"); err != nil || !duplicate {
		t.Fatalf("duplicate=%v %v", duplicate, err)
	}
	if got, ok, err := s.nextQueuedRun(c.ID); err != nil || !ok || got.ID != r.ID {
		t.Fatalf("queue=%+v %v %v", got, ok, err)
	}
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cp, answer, ok, err := s.claimInputContinuation(context.Background(), r.ID)
			if err != nil {
				t.Error(err)
			}
			if ok {
				claimed.Add(1)
				if len(cp) == 0 || string(answer.Answer) != `"answer"` {
					t.Error("lost original answer/checkpoint")
				}
			}
		}()
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("claims=%d", claimed.Load())
	}
	if err := recoverInterruptedRuns(s.db); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(r.ID)
	if got.Status != "interrupted" {
		t.Fatalf("claimed run replayable after crash: %+v", got)
	}
	if _, ok, err := s.nextQueuedRun(c.ID); err != nil || ok {
		t.Fatalf("replayed claimed run: %v %v", ok, err)
	}
}

func TestInputContinuationPreservesWaitingAtRecovery(t *testing.T) {
	s, _, r, q := parkedInputFixture(t)
	defer s.Close()
	if err := recoverInterruptedRuns(s.db); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(r.ID)
	question, _ := s.GetQuestion(q.ID)
	if got.Status != runWaiting || question.Status != questionPending {
		t.Fatalf("lost wait: %+v %+v", got, question)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND call_id='wait-call'`, r.ID).Scan(&status); err != nil || status != "running" {
		t.Fatalf("waiting tool=%s %v", status, err)
	}
}

func TestInputContinuationStopAndSteeringInvalidateCard(t *testing.T) {
	for _, steer := range []bool{false, true} {
		t.Run(fmt.Sprint(steer), func(t *testing.T) {
			s, c, r, q := parkedInputFixture(t)
			defer s.Close()
			if steer {
				_, _, _, err := s.AddUserRuns(c.ID, "New instruction", "steer", []runSpec{{BotID: r.BotID, Model: r.Model}})
				if err != nil {
					t.Fatal(err)
				}
			} else if err := s.CancelRunTree(r.ID); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetRun(r.ID)
			question, _ := s.GetQuestion(q.ID)
			if got.Status != "cancelled" || question.Status != questionCancelled || s.preservesInputWait(r.ID) {
				t.Fatalf("stale wait: %+v %+v", got, question)
			}
			if _, _, err := s.AnswerQuestion(q.ID, "human", "late"); !errors.Is(err, ErrQuestionNotPending) {
				t.Fatalf("late answer=%v", err)
			}
			var status string
			_ = s.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND call_id='wait-call'`, r.ID).Scan(&status)
			if status != "interrupted" {
				t.Fatalf("stale activity: %s", status)
			}
		})
	}
}

func TestInputContinuationExpiryAndCancelResumeWithoutInventedAnswer(t *testing.T) {
	for _, status := range []string{questionExpired, questionCancelled} {
		t.Run(status, func(t *testing.T) {
			s, c, r, q := parkedInputFixture(t)
			defer s.Close()
			if status == questionExpired {
				_, err := s.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), q.ID)
				if err != nil {
					t.Fatal(err)
				}
			} else if _, err := s.CancelQuestion(q.ID); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.nextQueuedRun(c.ID); err != nil || !ok {
				t.Fatalf("ready=%v %v", ok, err)
			}
			_, answer, ok, err := s.claimInputContinuation(context.Background(), r.ID)
			if err != nil || !ok || answer.Status != status {
				t.Fatalf("answer=%+v ok=%v %v", answer, ok, err)
			}
			if got := (&Server{store: s}).inputResumeResult(answer); got != fmt.Sprintf(`{"status":%q}`, status) {
				t.Fatalf("fabricated answer: %s", got)
			}
		})
	}
}

func TestInputContinuationSaveRollsBackOnEventFailure(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	q, err := s.CreateQuestion(c.ID, r, askQuestionInput{Question: "test", Type: questionText})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`CREATE TRIGGER fail_wait_event BEFORE INSERT ON events WHEN NEW.type='run' BEGIN SELECT RAISE(ABORT,'fixture'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{}`)); err == nil {
		t.Fatal("accepted failed checkpoint")
	}
	got, _ := s.GetRun(r.ID)
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM run_input_waits`).Scan(&n)
	if got.Status != "running" || n != 0 {
		t.Fatalf("non-atomic checkpoint: %+v %d", got, n)
	}
}

func TestInputContinuationFormSecretIsReRequestedAfterVaultLoss(t *testing.T) {
	s, _, r, q := formFixture(t)
	if err := s.store.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	answered, _, err := s.AnswerUserForm(q.ID, "human", formValues())
	if err != nil {
		t.Fatal(err)
	}
	before := s.inputResumeResult(answered)
	if !strings.Contains(before, "secret_ref") || strings.Contains(before, "synthetic-private-value") {
		t.Fatalf("unsafe result: %s", before)
	}
	s.clearRunSecrets(r.ID)
	after := s.inputResumeResult(answered)
	if strings.Contains(after, "secret_ref") || !strings.Contains(after, "secret_expired") || !strings.Contains(after, "test@example.test") {
		t.Fatalf("secret not invalidated: %s", after)
	}
}

func TestInputContinuationStopClearsParkedSecrets(t *testing.T) {
	s, _, r, q := formFixture(t)
	if err := s.store.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AnswerUserForm(q.ID, "human", formValues()); err != nil {
		t.Fatal(err)
	}
	if err := s.store.CancelRunTree(r.ID); err != nil {
		t.Fatal(err)
	}
	s.cancelInactiveRuns()
	s.secretVault.mu.Lock()
	defer s.secretVault.mu.Unlock()
	for _, record := range s.secretVault.records {
		if record.RunID == r.ID {
			t.Fatal("stopped wait retained secret")
		}
	}
}

func TestInputContinuationSubsequentWaitReplacesOnlyConsumedCheckpoint(t *testing.T) {
	s, c, r, q := parkedInputFixture(t)
	defer s.Close()
	if _, _, err := s.AnswerQuestion(q.ID, "human", "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshInputWaits(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.claimInputContinuation(context.Background(), r.ID); err != nil || !ok {
		t.Fatalf("claim=%v %v", ok, err)
	}
	next, err := s.CreateQuestion(c.ID, r, askQuestionInput{Question: "Second?", Type: questionText, ExpiresInSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInputContinuation(context.Background(), r.ID, next.ID, json.RawMessage(`{"second":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{}`)); err == nil {
		t.Fatal("replaced unconsumed wait")
	}
	if _, _, err := s.AnswerQuestion(next.ID, "human", "second"); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshInputWaits(c.ID); err != nil {
		t.Fatal(err)
	}
	cp, answer, ok, err := s.claimInputContinuation(context.Background(), r.ID)
	if err != nil || !ok || answer.ID != next.ID || string(cp) != `{"second":true}` {
		t.Fatalf("second resume=%s %+v %v %v", cp, answer, ok, err)
	}
}

// Real production engine, tool schemas, streaming adapter, SQLite, workers and
// HTTP answer route; only the model endpoint is deterministic and local.
func TestInputContinuationRealRuntimeRestartsAndDoesNotReplayTools(t *testing.T) {
	for _, answerOffline := range []bool{false, true} {
		t.Run(fmt.Sprint(answerOffline), func(t *testing.T) {
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
						CallID  string `json:"tool_call_id"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					http.Error(w, "invalid", 400)
					return
				}
				n := calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				var delta map[string]any
				if n == 1 {
					tcs := []any{}
					for i, v := range []struct{ ID, Name, Args string }{{"before", "save_memory", `{"content":"checkpoint fixture completed effect"}`}, {"ask", "ask_user_form", `{"question":"Supply a display name","source_url":"https://forms.example.test/profile","fields":[{"id":"name","label":"Display name","type":"text","required":true}]}`}, {"stale", "save_memory", `{"content":"MUST NOT execute stale tail"}`}} {
						tcs = append(tcs, map[string]any{"index": i, "id": v.ID, "type": "function", "function": map[string]string{"name": v.Name, "arguments": v.Args}})
					}
					delta = map[string]any{"content": "Saved the completed step; waiting for your form.", "tool_calls": tcs}
				} else {
					seen := map[string]string{}
					for _, m := range payload.Messages {
						if m.Role == "tool" {
							seen[m.CallID] = m.Content
						}
					}
					if !strings.Contains(seen["ask"], "Ada") || seen["before"] == "" || seen["stale"] == "" {
						t.Errorf("incomplete resumed transcript: %+v", seen)
					}
					if n != 2 && n != 3 {
						t.Errorf("unexpected extra model call %d", n)
					}
					if n == 3 && (len(payload.Messages) == 0 || payload.Messages[len(payload.Messages)-1].Content != taskCompletionFinalReminder) {
						t.Error("resumed substantive work did not receive exactly one completeness review")
					}
					delta = map[string]any{"content": "Finished with Ada; prior work was preserved."}
				}
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			}))
			defer endpoint.Close()
			engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: endpoint.URL + "/v1", Model: "test-model"})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			s, err := NewServer(Config{DataDir: dir, Provider: "test", Engine: engine})
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.store.CreateBot("restart fixture", "", "test-model")
			if err != nil {
				t.Fatal(err)
			}
			c, _ := s.store.GetConversation(b.DMConversationID)
			_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Complete the synthetic profile", "fixture")
			if err != nil {
				t.Fatal(err)
			}
			s.enqueue(c, run)
			waitForRunStatus(t, s, run.ID, runWaiting)
			questions, err := s.store.ListQuestions(c.ID)
			if err != nil || len(questions) != 1 {
				t.Fatalf("questions=%+v %v", questions, err)
			}
			q := questions[0]
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("did not suspend: %d model calls", calls.Load())
			}
			if answerOffline {
				st, err := OpenStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = (&Server{store: st}).AnswerUserForm(q.ID, "human", map[string]string{"name": "Ada"}); err != nil {
					t.Fatal(err)
				}
				if err = st.Close(); err != nil {
					t.Fatal(err)
				}
			}
			s, err = NewServer(Config{DataDir: dir, Provider: "test", Engine: engine})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if !answerOffline {
				waitForRunStatus(t, s, run.ID, runWaiting)
				for i := 0; i < 2; i++ {
					rec := httptest.NewRecorder()
					s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/questions/"+q.ID+"/answer", strings.NewReader(`{"fields":{"name":"Ada"}}`)))
					if rec.Code != 200 {
						t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
					}
				}
			}
			waitForRunStatus(t, s, run.ID, "done")
			if calls.Load() != 3 {
				t.Fatalf("calls=%d", calls.Load())
			}
			memories, err := s.store.Memories(c.ID, b.ID)
			if err != nil || len(memories) != 1 || strings.Contains(memories[0].Content, "MUST NOT") {
				t.Fatalf("side effects replayed: %+v %v", memories, err)
			}
			messages, _, err := s.store.Messages(c.ID, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			progress, final := 0, 0
			for _, m := range messages {
				if m.Kind == "progress" {
					progress++
				}
				if m.Kind != "progress" && strings.Contains(m.Content, "Finished with Ada") {
					final++
				}
			}
			if progress != 2 || final != 1 {
				t.Fatalf("duplicate/lost turns: %+v", messages)
			}
			var staleStatus string
			_ = s.store.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND call_id='stale'`, run.ID).Scan(&staleStatus)
			if staleStatus != "failed" {
				t.Fatalf("stale queued tool not closed: %s", staleStatus)
			}
		})
	}
}
