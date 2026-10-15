package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// directThroughDispatcher mirrors what the runtime does for a call the model
// made under a concrete run name: no prior search, ResolveDirectName on the
// call_mcp_tool dispatcher, then that dispatcher's own Execute with the
// wrapped payload. The gate is the same approveMCPCall a native call uses.
func directThroughDispatcher(ctx context.Context, f *autoReviewFixture, name, args string) (string, error, bool) {
	p, err := f.s.extensions.PrepareDiscoverableForBotWithCallGate(ctx, f.r.BotID, nil, func(ctx context.Context, call extensions.MCPCallApproval) error {
		return f.s.approveMCPCall(ctx, f.c, f.r, call)
	})
	if err != nil {
		return "", err, false
	}
	defer p.Close()
	for _, tool := range p.Tools {
		if tool.Name != "call_mcp_tool" {
			continue
		}
		canonical, ok := tool.ResolveDirectName(ctx, name)
		if !ok {
			return "", nil, false
		}
		wrapped, _ := json.Marshal(map[string]json.RawMessage{"name": mustRawJSON(canonical), "arguments": json.RawMessage(args)})
		out, err := tool.Execute(ctx, wrapped)
		return out, err, true
	}
	return "", errors.New("no dispatcher"), false
}

func mustRawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

type directRowShape struct {
	Questions, Approvals, Reviews int
	Status, AnsweredBy, Tier      string
}

func directShape(t *testing.T, f *autoReviewFixture) directRowShape {
	t.Helper()
	var s directRowShape
	qs, _ := f.s.store.ListQuestions(f.c.ID)
	s.Questions = len(qs)
	if len(qs) > 0 {
		s.Status, s.AnsweredBy = string(qs[0].Status), qs[0].AnsweredBy
	}
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_approvals`).Scan(&s.Approvals)
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_auto_reviews`).Scan(&s.Reviews)
	return s
}

// A direct-name write call is reviewed exactly like call_mcp_tool: one
// reviewer call, one effect, identical question/approval/review rows, for every
// reviewer verdict.
func TestDirectNameWriteCallMatchesCallMCPToolGate(t *testing.T) {
	for _, decision := range []string{"allow", "needs_human", "deny", "context_gap"} {
		t.Run(decision, func(t *testing.T) {
			native := unattendedReviewFixture(t, "auto", decision)
			direct := unattendedReviewFixture(t, "auto", decision)
			var nativeErr error
			done := make(chan struct{})
			go func() { nativeErr = native.execute(context.Background()); close(done) }()
			var directErr error
			var routed bool
			dd := make(chan struct{})
			go func() {
				_, directErr, routed = directThroughDispatcher(context.Background(), direct, "mcp_fixture__read_public", `{"target":"alpha"}`)
				close(dd)
			}()
			if decision == "needs_human" {
				// These park a human card; compare while pending, then cancel via refusal.
				// Refuse only after the reviewer concluded; refusing earlier
				// legitimately skips the review and would not compare the gates.
				waitReviewQuestion(t, native, "human_required")
				waitReviewQuestion(t, direct, "human_required")
				for _, f := range []*autoReviewFixture{native, direct} {
					qs, _ := f.s.store.ListQuestions(f.c.ID)
					if _, _, err := f.s.store.AnswerQuestion(qs[0].ID, "synthetic-human", false); err != nil {
						t.Fatal(err)
					}
				}
			}
			<-done
			<-dd
			if !routed {
				t.Fatal("direct name was not resolved by the dispatcher")
			}
			if (nativeErr == nil) != (directErr == nil) {
				t.Fatalf("native=%v direct=%v", nativeErr, directErr)
			}
			if no, ok := tooloutcome.FromError(nativeErr); ok {
				do, dok := tooloutcome.FromError(directErr)
				if !dok || do.Code != no.Code || do.Status != no.Status || do.Certainty != no.Certainty {
					t.Fatalf("outcome native=%+v direct=%+v", no, do)
				}
			}
			if native.p.calls.Load() != 1 || direct.p.calls.Load() != 1 {
				t.Fatalf("reviewer calls native=%d direct=%d", native.p.calls.Load(), direct.p.calls.Load())
			}
			if native.effects.Load() != direct.effects.Load() {
				t.Fatalf("effects native=%d direct=%d", native.effects.Load(), direct.effects.Load())
			}
			if ns, ds := directShape(t, native), directShape(t, direct); ns != ds {
				t.Fatalf("row shape native=%+v direct=%+v", ns, ds)
			}
			if decision == "allow" && direct.effects.Load() != 1 {
				t.Fatalf("allow did not execute once: %d", direct.effects.Load())
			}
		})
	}
}

// A provider-marked read-only tool executes without review, direct or not.
func TestDirectNameReadOnlyExecutesWithoutReview(t *testing.T) {
	f := newAutoReviewFixture(t)
	setSyntheticMCPEffect(t, f)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	setSyntheticMCPHumanPolicy(t, f, false)
	out, err, routed := directThroughDispatcher(context.Background(), f, "mcp_fixture__read_public", `{"target":"alpha"}`)
	if err != nil || !routed || out == "" || f.effects.Load() != 1 || f.p.calls.Load() != 0 {
		t.Fatalf("out=%q err=%v routed=%v effects=%d reviews=%d", out, err, routed, f.effects.Load(), f.p.calls.Load())
	}
}

// Names the dispatcher cannot resolve are not rerouted.
func TestDirectNameUnknownIsNotRouted(t *testing.T) {
	f := newAutoReviewFixture(t)
	for _, name := range []string{"mcp_fixture__nope", "mcp_other__read_public", "read_public", ""} {
		if _, err, routed := directThroughDispatcher(context.Background(), f, name, `{}`); routed || err != nil {
			t.Fatalf("%q routed=%v err=%v", name, routed, err)
		}
	}
	if f.effects.Load() != 0 || f.p.calls.Load() != 0 {
		t.Fatal("unknown name had effects")
	}
}

// The unattended rules are the gate's, so a rerouted call gets them verbatim.
func TestDirectNameUnattendedRulesIdentical(t *testing.T) {
	t.Run("high_risk", func(t *testing.T) {
		f := newAutoReviewFixture(t)
		_ = f.s.store.putAutoReviewMode("auto")
		if _, err := f.s.extensions.SetToolTierOverride("fixture", "read_public", string(extensions.MCPTierHighRisk)); err != nil {
			t.Fatal(err)
		}
		f.r.Kind = runKindSchedule
		_, err, routed := directThroughDispatcher(context.Background(), f, "mcp_fixture__read_public", `{"target":"alpha"}`)
		out, ok := tooloutcome.FromError(err)
		if !routed || !ok || out.Code != "mcp_high_risk_unattended" || out.Certainty != "not_executed" || f.effects.Load() != 0 || f.p.calls.Load() != 0 {
			t.Fatalf("routed=%v err=%v effects=%d", routed, err, f.effects.Load())
		}
	})
	t.Run("needs_human parity with native", func(t *testing.T) {
		native := unattendedReviewFixture(t, "auto", "needs_human")
		direct := unattendedReviewFixture(t, "auto", "needs_human")
		native.r.Kind, direct.r.Kind = runKindSchedule, runKindSchedule
		nativeErr := native.execute(context.Background())
		_, directErr, routed := directThroughDispatcher(context.Background(), direct, "mcp_fixture__read_public", `{"target":"alpha"}`)
		no, nok := tooloutcome.FromError(nativeErr)
		do, dok := tooloutcome.FromError(directErr)
		if !routed || !nok || !dok || no.Code != do.Code || no.Certainty != do.Certainty || no.Status != do.Status || native.effects.Load() != 0 || direct.effects.Load() != 0 {
			t.Fatalf("routed=%v native=%v direct=%v", routed, nativeErr, directErr)
		}
		if ns, ds := directShape(t, native), directShape(t, direct); ns != ds {
			t.Fatalf("row shape native=%+v direct=%+v", ns, ds)
		}
	})
}
