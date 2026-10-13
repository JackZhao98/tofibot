package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

const selfReviewArgs = `{"name":"mcp_fixture__read_public","arguments":{"target":"alpha"}}`

func seedRunningCall(t *testing.T, f *autoReviewFixture, callID, started string) {
	t.Helper()
	diagnosticExec(t, f, `INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at,outcome_json) VALUES(?,?,?,?,'call_mcp_tool',?,'','running',0,?,'')`, f.c.ID, f.r.BotID, f.r.ID, callID, selfReviewArgs, started)
}

func evidenceCallIDs(t *testing.T, f *autoReviewFixture, r Run) []string {
	t.Helper()
	x, _, err := f.s.readMCPReviewContextFor(f.s.store.db, f.c, r, mcpProposalEffect)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range x.ToolResults {
		ids = append(ids, a.CallID)
	}
	return ids
}

// The call under review is written as running before dispatch; it must never
// appear as an earlier identical in-flight call, but a genuine earlier one must.
func TestMCPReviewEvidenceExcludesTheCallUnderReview(t *testing.T) {
	f := newAutoReviewFixture(t)
	seedRunningCall(t, f, "call-self", now())
	r := f.r
	if ids := evidenceCallIDs(t, f, r); len(ids) != 1 || ids[0] != "call-self" {
		t.Fatalf("without a bound call id the row is ordinary evidence: %v", ids)
	}
	r.reviewCallID = "call-self"
	if ids := evidenceCallIDs(t, f, r); len(ids) != 0 {
		t.Fatalf("own running row listed: %v", ids)
	}
	seedRunningCall(t, f, "call-earlier", now())
	if ids := evidenceCallIDs(t, f, r); len(ids) != 1 || ids[0] != "call-earlier" {
		t.Fatalf("earlier identical running call must stay visible: %v", ids)
	}
}

func TestMCPReviewOfSingleWriteWithOwnRunningRowIsNotContextGap(t *testing.T) {
	f := newAutoReviewFixture(t)
	if err := f.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		if strings.Contains(req.Messages[0].Content, `"status":"running"`) {
			return reviewReply(req, "context_gap"), nil
		}
		return reviewReply(req, "allow"), nil
	}
	seedRunningCall(t, f, "call-self", now())
	ctx := runtime.WithToolCallID(context.Background(), "call-self")
	if err := f.execute(ctx); err != nil {
		t.Fatalf("own running row made the review a gap: %v", err)
	}
	if f.effects.Load() != 1 {
		t.Fatal("call did not execute", f.effects.Load())
	}
}
