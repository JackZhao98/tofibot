package app

import (
	"context"
	"errors"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

const demotedAnswer = "Inbox checked: two important mails."

// reviewDraftEngine streams a final draft, sends it back for review, then
// ends the reviewed turn through finish.
type reviewDraftEngine struct {
	finish func(ctx context.Context, req runtime.Request) (runtime.Result, error)
}

func (e reviewDraftEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	req.OnDelta(demotedAnswer)
	if err := req.OnReviewDraft(1, demotedAnswer); err != nil {
		return runtime.Result{}, err
	}
	return e.finish(ctx, req)
}

func runReviewDraft(t *testing.T, finish func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error)) (*Server, Run, []Message) {
	t.Helper()
	engine := &reviewDraftEngine{}
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
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "check my inbox", "review-draft")
	if err != nil {
		t.Fatal(err)
	}
	engine.finish = finish(s, run)
	s.execute(c, run)
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	return s, run, messages
}

func assistantContents(messages []Message) []string {
	var out []string
	for _, m := range messages {
		if m.Role == "assistant" {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestDemotedDraftIsPublishedWhenReviewedTurnEndsWithoutAnswer(t *testing.T) {
	cases := map[string]struct {
		finish func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error)
		status string
		want   []string
	}{
		"provider error": {func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error) {
			return func(context.Context, runtime.Request) (runtime.Result, error) {
				return runtime.Result{}, errors.New("provider stream idle")
			}
		}, "failed", []string{demotedAnswer}},
		"user stop": {func(s *Server, r Run) func(context.Context, runtime.Request) (runtime.Result, error) {
			return func(context.Context, runtime.Request) (runtime.Result, error) {
				_, _ = s.store.SetRunStatus(r.ID, "cancelled", "stopped by user")
				return runtime.Result{}, context.Canceled
			}
		}, "cancelled", []string{demotedAnswer}},
		"steered": {func(s *Server, r Run) func(context.Context, runtime.Request) (runtime.Result, error) {
			return func(context.Context, runtime.Request) (runtime.Result, error) {
				_, _ = s.store.MarkRunSteered(r.ID, "steered by newer user message")
				return runtime.Result{}, context.Canceled
			}
		}, "cancelled", []string{demotedAnswer}},
		"budget exhausted": {func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error) {
			return func(_ context.Context, req runtime.Request) (runtime.Result, error) {
				req.OnDelta("Still verif")
				return runtime.Result{BudgetExhausted: true, BudgetReason: "time"}, nil
			}
		}, "failed", []string{demotedAnswer}},
		"budget partial equals draft": {func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error) {
			return func(context.Context, runtime.Request) (runtime.Result, error) {
				return runtime.Result{BudgetExhausted: true, BudgetReason: "time", Content: demotedAnswer}, nil
			}
		}, "failed", []string{demotedAnswer}},
		"suspended": {func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error) {
			return func(context.Context, runtime.Request) (runtime.Result, error) {
				return runtime.Result{Suspended: true}, nil
			}
		}, "running", []string{demotedAnswer}},
		"reviewed answer": {func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error) {
			return func(_ context.Context, req runtime.Request) (runtime.Result, error) {
				req.OnDelta("Verified: two important mails.")
				return runtime.Result{Content: "Verified: two important mails."}, nil
			}
		}, "done", []string{"Verified: two important mails."}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, run, messages := runReviewDraft(t, tc.finish)
			got, err := s.store.GetRun(run.ID)
			if err != nil || got.Status != tc.status {
				t.Fatalf("run=%+v err=%v", got, err)
			}
			contents := assistantContents(messages)
			if len(contents) < len(tc.want) {
				t.Fatalf("assistant messages = %q, want %q", contents, tc.want)
			}
			for i, want := range tc.want {
				if contents[i] != want {
					t.Fatalf("assistant messages = %q, want %q first", contents, tc.want)
				}
			}
			published := 0
			for _, content := range contents {
				if content == demotedAnswer {
					published++
				}
			}
			if wantDraft := tc.want[0] == demotedAnswer; (published == 1) != wantDraft || published > 1 {
				t.Fatalf("demoted draft published %d times: %q", published, contents)
			}
		})
	}
}

func TestDemotedDraftResetWaitsForReviewedTurn(t *testing.T) {
	var resetsBeforeDelta int
	s, run, _ := runReviewDraft(t, func(s *Server, r Run) func(context.Context, runtime.Request) (runtime.Result, error) {
		return func(_ context.Context, req runtime.Request) (runtime.Result, error) {
			resetsBeforeDelta = len(eventsOfType(t, s.store, r.ConversationID, "draft_reset"))
			req.OnDelta("Verified.")
			return runtime.Result{Content: "Verified."}, nil
		}
	})
	if resetsBeforeDelta != 0 {
		t.Fatalf("draft reset before the reviewed turn streamed: %d", resetsBeforeDelta)
	}
	if got := len(eventsOfType(t, s.store, run.ConversationID, "draft_reset")); got != 1 {
		t.Fatalf("draft_reset events = %d", got)
	}
}

func TestExecutePassesConversationForPromptCache(t *testing.T) {
	var got string
	_, run, _ := runReviewDraft(t, func(*Server, Run) func(context.Context, runtime.Request) (runtime.Result, error) {
		return func(_ context.Context, req runtime.Request) (runtime.Result, error) {
			got = req.ConversationID
			return runtime.Result{Content: "done"}, nil
		}
	})
	if got == "" || got != run.ConversationID {
		t.Fatalf("request conversation = %q, want %q", got, run.ConversationID)
	}
}
