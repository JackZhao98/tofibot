package app

import (
	"context"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// A read_only tool (owner override, or classification merged with the
// annotations) executes directly with no card and no reviewer, as often as
// it is called; an effect keeps its one-shot approval: the identical repeat
// is refused before dispatch and never re-executed. The remote readOnlyHint
// alone decides nothing: an unclassified hinted read stays an effect.
func TestMCPReadOnlyTierRepeatsFreelyWhileEffectStaysOneShot(t *testing.T) {
	f := newAutoReviewFixture(t)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	hash := mcpApprovalHash(f.call)
	// Unclassified hinted read: reviewed as an effect, one-shot.
	if err := f.execute(context.Background()); err != nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
		t.Fatalf("hinted read first call: %v effects=%d reviews=%d", err, f.effects.Load(), f.p.calls.Load())
	}
	err := f.execute(context.Background())
	if out, ok := tooloutcome.FromError(err); !ok || out.Status != tooloutcome.Denied || out.Code != "approval_already_claimed" || f.effects.Load() != 1 {
		t.Fatalf("hinted read repeat must be one-shot like any effect: %v effects=%d", err, f.effects.Load())
	}
	var cards int
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_approvals WHERE run_id=? AND action_hash=?`, f.r.ID, hash).Scan(&cards); err != nil || cards != 1 {
		t.Fatalf("effect cards=%d err=%v", cards, err)
	}
	// Owner read_only override: repeats execute with no new card and no review.
	setSyntheticMCPHumanPolicy(t, f, false)
	for i := int32(2); i <= 3; i++ {
		if err := f.executeArgs(context.Background(), `{"target":"beta"}`); err != nil || f.effects.Load() != i || f.p.calls.Load() != 1 {
			t.Fatalf("read_only repeat %d: %v effects=%d reviews=%d", i, err, f.effects.Load(), f.p.calls.Load())
		}
	}
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_approvals WHERE run_id=?`, f.r.ID).Scan(&cards); err != nil || cards != 1 {
		t.Fatalf("read_only created cards: %d err=%v", cards, err)
	}
	// The exact action that already has approval history in this run does not
	// take the exempt route: as a read_only proposal it is reviewed afresh on
	// its own card (never replayed on the used approval), then executed.
	if err := f.execute(context.Background()); err != nil || f.effects.Load() != 4 || f.p.calls.Load() != 2 {
		t.Fatalf("history after tier change: %v effects=%d reviews=%d", err, f.effects.Load(), f.p.calls.Load())
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
	records, err := readMCPRunToolEvidence(store.db, run, true, &bounds, nil)
	if err != nil || len(records) != 1 {
		t.Fatalf("refusal fenced the scheduled effect: %v records=%d", err, len(records))
	}
	dispatched := tooloutcome.New(tooloutcome.Uncertain, "mcp_result_unknown", "unknown", "Synthetic lost response.", "verify_effect")
	insert("dispatched", dispatched.JSON())
	if _, err = readMCPRunToolEvidence(store.db, run, true, &bounds, nil); err == nil || mcpContextDiagnostic(err).Code != mcpContextToolsUncertain {
		t.Fatalf("dispatched unknown effect did not fence: %v", err)
	}
}
