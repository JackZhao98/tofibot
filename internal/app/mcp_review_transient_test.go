package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// A human message arriving during review changes the evidence, not the
// action's eligibility: the model may retry, which starts a fresh review.
func TestMCPContextDigestChangeIsTransientAndRetryReviewsAgain(t *testing.T) {
	f := newAutoReviewFixture(t)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	interrupt := true
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		if interrupt {
			interrupt = false
			m, _, err := f.s.store.AddMessage(f.c.ID, "user", "", "", "Synthetic note while reviewing.", "")
			if err != nil {
				t.Error(err)
			}
			diagnosticExec(t, f, `INSERT INTO user_message_ingress(message_id,created_at) VALUES(?,?)`, m.ID, now())
		}
		return reviewReply(req, "allow"), nil
	}
	err := f.execute(context.Background())
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Status != tooloutcome.Transient || out.NextAction != "retry" || out.Code != "mcp_review_context_missing" || out.Certainty != "not_executed" || !strings.Contains(out.Message, string(mcpContextDigestChanged)) || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
		t.Fatalf("digest change was not a transient closure: %+v effects=%d", out, f.effects.Load())
	}
	if err := f.execute(context.Background()); err != nil || f.effects.Load() != 1 || f.p.calls.Load() != 2 {
		t.Fatalf("retry did not review again: %v effects=%d reviews=%d", err, f.effects.Load(), f.p.calls.Load())
	}
}

// Fresh reviews per action are bounded; after the cap the closure is
// permanent and the recovery guard fences identical retries.
func TestMCPTransientReviewRetriesAreBounded(t *testing.T) {
	f := newAutoReviewFixture(t)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		m, _, err := f.s.store.AddMessage(f.c.ID, "user", "", "", "Synthetic note while reviewing.", "")
		if err != nil {
			t.Error(err)
		}
		diagnosticExec(t, f, `INSERT INTO user_message_ingress(message_id,created_at) VALUES(?,?)`, m.ID, now())
		return reviewReply(req, "allow"), nil
	}
	for i := 1; i <= mcpReviewRetryCards+1; i++ {
		out, ok := tooloutcome.FromError(f.execute(context.Background()))
		want, next := tooloutcome.Transient, "retry"
		if i >= mcpReviewRetryCards {
			want, next = tooloutcome.Permanent, "replan"
		}
		if !ok || out.Status != want || out.NextAction != next || out.Code != "mcp_review_context_missing" {
			t.Fatalf("attempt %d: %+v", i, out)
		}
	}
	if f.effects.Load() != 0 || f.p.calls.Load() != mcpReviewRetryCards {
		t.Fatalf("effects=%d reviews=%d", f.effects.Load(), f.p.calls.Load())
	}
}

func TestMCPContextClosureTransience(t *testing.T) {
	for code, transient := range map[mcpContextFailureCode]bool{mcpContextDigestChanged: true, mcpContextResumeChanged: true, mcpContextNonText: false, mcpContextRestrictionsBudget: false} {
		out, _ := tooloutcome.FromError(mcpReviewBlocked("context_required", "Synthetic.", &MCPContextFailure{Code: code}))
		if transient != (out.Status == tooloutcome.Transient && out.NextAction == "retry") || !transient && (out.Status != tooloutcome.Permanent || out.NextAction != "replan") {
			t.Fatalf("%s: %+v", code, out)
		}
	}
}
