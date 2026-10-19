package app

import (
	"context"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func hiddenMCPFixture(t *testing.T, mode string, requiresHuman bool) *autoReviewFixture {
	t.Helper()
	f := newAutoReviewFixture(t)
	setSyntheticMCPHumanPolicy(t, f, requiresHuman)
	if err := f.s.store.putAutoReviewMode(mode); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, f.c.ID); err != nil {
		t.Fatal(err)
	}
	f.c.UserVisible = false
	return f
}

func requireNoQuestions(t *testing.T, f *autoReviewFixture) {
	t.Helper()
	questions, err := f.s.store.ListQuestions(f.c.ID)
	if err != nil || len(questions) != 0 {
		t.Fatalf("a card exists in a conversation nobody can see: %+v %v", questions, err)
	}
}

// A reviewed call needs a card; with no person to see it, the gate refuses
// before creating one instead of parking the run.
func TestMCPApprovalRefusesHiddenConversation(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "auto"} {
		t.Run(mode, func(t *testing.T) {
			f := hiddenMCPFixture(t, mode, true)
			// Bounded so a missing refusal fails instead of waiting on a card.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := f.s.approveMCPCall(ctx, f.c, f.r, f.call)
			out, ok := tooloutcome.FromError(err)
			if !ok || out.Status != tooloutcome.Denied || out.Code != "conversation_not_user_visible" || out.Certainty != "not_executed" || out.NextAction != "explain_blocker" {
				t.Fatalf("want classified refusal, got %v", err)
			}
			requireNoQuestions(t, f)
			if f.effects.Load() != 0 {
				t.Fatal("call executed")
			}
		})
	}
}

// Shadow advice is only a card. Hidden, the card is skipped and the host's
// execution policy for an exempt read proceeds unchanged.
func TestMCPShadowAdviceSkippedInHiddenConversation(t *testing.T) {
	f := hiddenMCPFixture(t, "shadow", false)
	if err := f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.s.shadowReviewWG.Wait()
	requireNoQuestions(t, f)
	if f.effects.Load() != 1 || f.p.calls.Load() != 0 {
		t.Fatalf("host policy changed: effects=%d reviews=%d", f.effects.Load(), f.p.calls.Load())
	}
}
