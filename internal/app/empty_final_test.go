package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"io"
	"path"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

const emptyFinalFirstAnswer = "FIRST_ANSWER: the two requested items are ready."

// emptyFinalEndpoint mirrors production run e8cb6243: a progress turn with
// text alongside a tool call, tool work, a streamed final answer that the
// completion review demotes, more tool calls without text, and then a model
// call that is still in flight when the process shuts down (deploy).
func emptyFinalEndpoint(t *testing.T, reached chan<- struct{}) (*httptest.Server, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	var calls atomic.Int32
	var reviewed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		n := calls.Add(1)
		if n == 4 && len(payload.Messages) > 0 {
			if last, _ := payload.Messages[len(payload.Messages)-1].Content.(string); last == taskCompletionFinalReminder {
				reviewed.Store(true)
			}
		}
		toolCall := func(id string) []any {
			return []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]string{"name": "computer_files", "arguments": `{"action":"files.write","path":"notes-` + id + `","content":"X"}`}}}
		}
		var delta map[string]any
		switch n {
		case 1:
			delta = map[string]any{"content": "Checking the source first.", "tool_calls": toolCall("c1")}
		case 2:
			delta = map[string]any{"content": "", "tool_calls": toolCall("c2")}
		case 3:
			delta = map[string]any{"content": emptyFinalFirstAnswer}
		case 4:
			delta = map[string]any{"content": "", "tool_calls": toolCall("c4")}
		case 5:
			delta = map[string]any{"content": "", "tool_calls": toolCall("c5")}
		default:
			// The reviewed final answer never arrives: the service stops while
			// this model call is in flight.
			select {
			case reached <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &reviewed
}

// Regression for run e8cb6243: a shutdown during the reviewed turn returned an
// empty Result without error, so execute() finished the run "done" with an
// empty assistant message and discarded the demoted final draft.
func TestShutdownDuringReviewedTurnDoesNotFinishDoneWithEmptyAnswer(t *testing.T) {
	reached := make(chan struct{}, 1)
	endpoint, calls, reviewed := emptyFinalEndpoint(t, reached)
	engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: endpoint.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, Provider: "test", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("empty final fixture", "", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Prepare the two requested items", "empty-final")
	if err != nil {
		t.Fatal(err)
	}
	emptyFinalComputer(t, s)
	s.enqueue(c, run)
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatalf("model calls=%d; fixture never reached the in-flight reviewed turn", calls.Load())
	}
	if !reviewed.Load() {
		t.Fatal("the first final answer was not sent back for completion review")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewServer(Config{DataDir: dir, Provider: "test", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.store.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == "done" {
		t.Fatalf("shutdown finished the run as done: %+v", got)
	}
	if got.Status != "interrupted" {
		t.Fatalf("run status=%q error=%q, want interrupted", got.Status, got.Error)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	first := 0
	for _, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			t.Fatalf("empty assistant message published: %+v", messages)
		}
		if m.Content == emptyFinalFirstAnswer {
			first++
		}
	}
	if first != 1 {
		t.Fatalf("demoted first answer published %d times: %+v", first, messages)
	}
}

// emptyFinalComputer accepts every control call; files.write is a
// non-observation tool, so it arms the completion final review.
func emptyFinalComputer(t *testing.T, s *Server) {
	t.Helper()
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-unused-empty-final.sock", Client: &http.Client{Transport: reviewTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"ok":true,"result":{}}`
		if r.Method == http.MethodGet || r.Body == nil {
			body = `{"kind":"firecracker"}`
		} else {
			var action computer.Action
			if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
				return nil, err
			}
			if action.Name == "files.identity" {
				var in struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(action.Args, &in)
				target := path.Join("/workspace/bots", action.BotID, in.Path)
				raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"target": target, "object": "synthetic:" + target, "parent": path.Dir(target), "parent_object": "synthetic-parent", "guard_version": 1}})
				body = string(raw)
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
}

type emptyFinalEngine func(context.Context, runtime.Request) (runtime.Result, error)

func (f emptyFinalEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	return f(ctx, req)
}

func runEmptyFinal(t *testing.T, engine emptyFinalEngine) (*Server, Run, []Message) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b, err := s.store.CreateBot("secretary", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "prepare the report", "empty-final-guard")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, run)
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" {
			t.Fatalf("empty assistant message published: %+v", messages)
		}
	}
	got, err := s.store.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, got, messages
}

func countContent(messages []Message, content string) int {
	n := 0
	for _, m := range messages {
		if m.Content == content {
			n++
		}
	}
	return n
}

// An empty final result after a demoted draft publishes that draft as the
// answer, once, under the stream bubble it already occupies.
func TestEmptyFinalPublishesRetainedDemotedDraft(t *testing.T) {
	s, run, messages := runEmptyFinal(t, func(_ context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta(demotedAnswer)
		if err := req.OnReviewDraft(1, demotedAnswer); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{}, nil
	})
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if n := countContent(messages, demotedAnswer); n != 1 {
		t.Fatalf("demoted draft published %d times: %+v", n, messages)
	}
	last := messages[len(messages)-1]
	if last.Content != demotedAnswer || last.Kind != "" {
		t.Fatalf("final answer = %+v", last)
	}
	if resets := len(eventsOfType(t, s.store, run.ConversationID, "draft_reset")); resets != 0 {
		t.Fatalf("draft reset before publishing the same text: %d", resets)
	}
}

// With no demoted draft, a run that already showed progress ends with a short
// pointer to it rather than repeating the progress text.
func TestEmptyFinalAfterProgressPointsToIt(t *testing.T) {
	const progress = "Collected the first half of the report."
	_, run, messages := runEmptyFinal(t, func(_ context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta(progress)
		if err := req.OnAssistantTurn(1, progress); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{}, nil
	})
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if n := countContent(messages, progress); n != 1 {
		t.Fatalf("progress published %d times: %+v", n, messages)
	}
	last := messages[len(messages)-1]
	if last.Content != noFinalAnswerProgressNote || last.Kind != "" {
		t.Fatalf("final answer = %+v", last)
	}
}

// With nothing visible at all, the run fails with a typed, user-readable code
// so the UI shows a failure card with retry instead of silence.
func TestEmptyFinalWithoutOutputFailsWithTypedCode(t *testing.T) {
	_, run, messages := runEmptyFinal(t, func(context.Context, runtime.Request) (runtime.Result, error) {
		return runtime.Result{}, nil
	})
	if run.Status != "failed" || run.failure() == nil || run.failure().Code != noFinalAnswerCode {
		t.Fatalf("run=%+v failure=%+v", run, run.failure())
	}
	for _, m := range messages {
		if m.Role == "assistant" {
			t.Fatalf("assistant message published: %+v", messages)
		}
	}
	raw, err := json.Marshal(run)
	if err != nil || !strings.Contains(string(raw), `"code":"no_final_answer"`) {
		t.Fatalf("run JSON lacks typed failure: %s %v", raw, err)
	}
}

// FinishRun itself never publishes an empty assistant message.
func TestFinishRunDoesNotPublishEmptyMessage(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("secretary", "", "model")
	r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
	_, _ = s.store.SetRunStatus(r.ID, "running", "")
	m, finished, err := s.store.FinishRun(r.ID, b.DMConversationID, b.ID, "  ")
	if err != nil || !finished || m.ID != "" {
		t.Fatalf("message=%+v finished=%v err=%v", m, finished, err)
	}
	messages, _, _ := s.store.Messages(b.DMConversationID, 0, 50)
	for _, msg := range messages {
		if msg.Role == "assistant" {
			t.Fatalf("empty assistant message published: %+v", msg)
		}
	}
}
