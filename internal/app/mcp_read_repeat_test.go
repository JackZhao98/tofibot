package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// An approval is one-shot per dispatch. A read-only proposal (owner-trusted
// read-only tool, or remote readOnlyHint: the scheduled fence's predicate) is
// repeated on a fresh card with its own review and claim; an effect's
// identical repeat is refused before dispatch and never re-executed.
func TestMCPReadRepeatReviewedAfreshWhileEffectStaysOneShot(t *testing.T) {
	f := newAutoReviewFixture(t)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	hash := mcpApprovalHash(f.call)
	// Owner-trusted read-only tool: identical read twice, each reviewed and claimed.
	for i := int32(1); i <= 2; i++ {
		if err := f.execute(context.Background()); err != nil {
			t.Fatalf("trusted read %d: %v", i, err)
		}
		if f.effects.Load() != i || f.p.calls.Load() != i {
			t.Fatalf("trusted read %d: effects=%d reviews=%d", i, f.effects.Load(), f.p.calls.Load())
		}
	}
	var cards, claimed, actionClaims int
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*),SUM(claimed_at<>'') FROM mcp_call_approvals WHERE run_id=? AND action_hash=?`, f.r.ID, hash).Scan(&cards, &claimed); err != nil || cards != 2 || claimed != 2 {
		t.Fatalf("repeat read cards=%d claimed=%d err=%v", cards, claimed, err)
	}
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_execution_claims WHERE run_id=? AND action_hash=?`, f.r.ID, hash).Scan(&actionClaims); err != nil || actionClaims != 1 {
		t.Fatalf("action-level claim rows=%d err=%v", actionClaims, err)
	}
	// Different arguments are a different proposal, never fenced by the other claim.
	if err := f.executeArgs(context.Background(), `{"target":"beta"}`); err != nil || f.effects.Load() != 3 || f.p.calls.Load() != 3 {
		t.Fatalf("different-argument read: %v effects=%d reviews=%d", err, f.effects.Load(), f.p.calls.Load())
	}
	// The remote readOnlyHint alone is the same predicate.
	setSyntheticMCPHumanPolicy(t, f, true)
	for i := int32(4); i <= 5; i++ {
		if err := f.execute(context.Background()); err != nil || f.effects.Load() != i || f.p.calls.Load() != i {
			t.Fatalf("hinted read %d: %v effects=%d reviews=%d", i, err, f.effects.Load(), f.p.calls.Load())
		}
	}
	// An effect keeps its one-shot approval: the identical repeat is a
	// pre-dispatch refusal, not an unverified effect. The proposal is bound
	// directly (as the gate would bind a tool outside the trusted list with
	// no remote hint), so the cached discovery catalog plays no part.
	write := f.call
	write.Tool, write.ReadOnlyHint, write.Description = "publish_note", false, "Synthetic publish"
	publish := func(args string) error {
		call := write
		call.Arguments = json.RawMessage(args)
		if err := f.s.approveMCPCall(context.Background(), f.c, f.r, call); err != nil {
			return err
		}
		f.effects.Add(1)
		return nil
	}
	if err := publish(`{"target":"alpha"}`); err != nil || f.effects.Load() != 6 || f.p.calls.Load() != 6 {
		t.Fatalf("first effect: %v effects=%d reviews=%d", err, f.effects.Load(), f.p.calls.Load())
	}
	err := publish(`{"target":"alpha"}`)
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Status != tooloutcome.Denied || out.Code != "approval_already_claimed" || out.Certainty != "not_executed" {
		t.Fatalf("identical effect repeat = %v", err)
	}
	if f.effects.Load() != 6 || f.p.calls.Load() != 6 {
		t.Fatalf("identical effect replayed: effects=%d reviews=%d", f.effects.Load(), f.p.calls.Load())
	}
	// A different-argument effect is its own proposal with its own review.
	if err := publish(`{"target":"beta"}`); err != nil || f.effects.Load() != 7 || f.p.calls.Load() != 7 {
		t.Fatalf("different-argument effect: %v effects=%d reviews=%d", err, f.effects.Load(), f.p.calls.Load())
	}
}

// A refused call was never dispatched, so its record cannot fence later
// effects in a scheduled run; only an unknown certainty on a dispatched
// effect does.
func TestMCPRefusedCallDoesNotFenceScheduledEffects(t *testing.T) {
	store, _, run := questionFixture(t)
	defer store.Close()
	insert := func(callID, outcome string) {
		t.Helper()
		if _, err := store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json) VALUES(?,?,?,?,'call_mcp_tool','{"name":"mcp_fixture__notion-fetch","arguments":{"id":"3f5c"}}',?,'failed',0,'2026-10-10T00:00:00Z','2026-10-10T00:00:00Z',?)`, run.ConversationID, run.BotID, run.ID, callID, outcome, outcome); err != nil {
			t.Fatal(err)
		}
	}
	refused, _ := tooloutcome.FromError(tooloutcome.ApprovalAlreadyClaimed("Synthetic refusal."))
	insert("refused", refused.JSON())
	var bounds mcpEvidenceBounds
	records, err := readMCPRunToolEvidence(store.db, run, true, &bounds)
	if err != nil || len(records) != 1 {
		t.Fatalf("refusal fenced the scheduled effect: %v records=%d", err, len(records))
	}
	dispatched := tooloutcome.New(tooloutcome.Uncertain, "mcp_result_unknown", "unknown", "Synthetic lost response.", "verify_effect")
	insert("dispatched", dispatched.JSON())
	if _, err = readMCPRunToolEvidence(store.db, run, true, &bounds); err == nil || mcpContextDiagnostic(err).Code != mcpContextToolsUncertain {
		t.Fatalf("dispatched unknown effect did not fence: %v", err)
	}
}
