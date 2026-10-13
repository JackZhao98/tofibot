package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func unattendedReviewFixture(t *testing.T, mode, decision string) *autoReviewFixture {
	t.Helper()
	f := newAutoReviewFixture(t)
	setSyntheticMCPEffect(t, f)
	if err := f.s.store.putAutoReviewMode(mode); err != nil {
		t.Fatal(err)
	}
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return reviewReply(req, decision), nil
	}
	return f
}

func scheduleReviewer(f *scheduledMCPFixture, decision, risk string, confirmation bool) {
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return v5ReviewReply(req, decision, risk, confirmation), nil
	}
}

func requireNeedsHumanUnattended(t *testing.T, f *scheduledMCPFixture, err error) {
	t.Helper()
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Status != tooloutcome.Denied || out.Code != "mcp_needs_human_unattended" || out.Certainty != "not_executed" ||
		!strings.Contains(out.Message, "Synthetic semantic risk assessment.") || !strings.Contains(out.Message, "needs the owner's go-ahead in chat") || !strings.Contains(out.Message, "do not retry or route around it") {
		t.Fatalf("outcome = %v", err)
	}
	questions, _ := f.s.store.ListQuestions(f.c.ID)
	for _, q := range questions {
		if q.Status == questionPending {
			t.Fatalf("pending card left for nobody: %+v", q)
		}
	}
	if f.effects.Load() != 0 || f.claimCount() != 0 {
		t.Fatalf("call executed: %d", f.effects.Load())
	}
}

// The tool returns a not_executed denial (a tool result, not a run failure),
// so the Bot keeps working and writes the summary that tells the owner.
func TestUnattendedNeedsHumanAndContextGapAreNotExecuted(t *testing.T) {
	for _, tc := range []struct {
		decision, risk string
		confirmation   bool
	}{{"needs_human", "high", true}, {"context_gap", "unknown", false}} {
		for _, delegated := range []bool{false, true} {
			t.Run(tc.decision+map[bool]string{false: "", true: "/delegated child"}[delegated], func(t *testing.T) {
				f := newScheduledMCPFixture(t, false, scheduleSourceChat)
				f.proposeTool("publish_note", false)
				scheduleReviewer(f, tc.decision, tc.risk, tc.confirmation)
				if delegated {
					f.delegate(f.colleague, false)
				}
				err := f.s.approveMCPCall(context.Background(), f.c, f.r, f.call)
				requireNeedsHumanUnattended(t, f, err)
				// The identical call stays refused with the same named code, without a new review.
				calls := f.p.calls.Load()
				requireNeedsHumanUnattended(t, f, f.s.approveMCPCall(context.Background(), f.c, f.r, f.call))
				if f.p.calls.Load() != calls {
					t.Fatal("repeat spent another review")
				}
			})
		}
	}
}

func TestUnattendedReviewerDenyStaysDeny(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	f.proposeTool("publish_note", false)
	scheduleReviewer(f, "deny", "high", false)
	out, ok := tooloutcome.FromError(f.s.approveMCPCall(context.Background(), f.c, f.r, f.call))
	if !ok || out.Code != "mcp_review_policy_denied" || f.claimCount() != 0 {
		t.Fatalf("deny = %+v", out)
	}
}

func TestUnattendedReviewerUnavailableNotExecuted(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	f.proposeTool("publish_note", false)
	f.p.reply = func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		return nil, context.DeadlineExceeded
	}
	out, ok := tooloutcome.FromError(f.s.approveMCPCall(context.Background(), f.c, f.r, f.call))
	if !ok || out.Certainty != "not_executed" || out.Code != "mcp_review_unavailable" || f.claimCount() != 0 {
		t.Fatalf("unavailable = %+v", out)
	}
}

// An owner-triggered run keeps the card.
func TestInteractiveNeedsHumanStillCreatesCard(t *testing.T) {
	f := unattendedReviewFixture(t, "auto", "needs_human")
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := waitReviewQuestion(t, f, "human_required")
	if q.Status != questionPending {
		t.Fatalf("interactive card = %+v", q)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", true); err != nil {
		t.Fatal(err)
	}
	if err := waitReviewDone(t, done); err != nil || f.effects.Load() != 1 {
		t.Fatalf("approved interactive card: %v effects=%d", err, f.effects.Load())
	}
}

// A schedule run whose reviewer allows still executes.
func TestUnattendedAllowStillExecutes(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	f.proposeTool("publish_note", false)
	scheduleReviewer(f, "allow", "medium", false)
	if err := f.s.approveMCPCall(context.Background(), f.c, f.r, f.call); err != nil || f.claimCount() != 1 {
		t.Fatalf("scheduled allow: %v claims=%d", err, f.claimCount())
	}
}
