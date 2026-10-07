package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func refuseMCPAction(t *testing.T, f *autoReviewFixture, r Run, hash string) {
	t.Helper()
	q, err := f.s.store.CreateQuestion(f.c.ID, r, askQuestionInput{Question: "Synthetic refusal", Type: questionApproval, Approval: &ApprovalDetails{Action: "Call " + f.call.Tool, Target: "MCP server " + f.call.Server, Impact: "synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	diagnosticExec(t, f, `INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) VALUES(?,?,?)`, q.ID, r.ID, hash)
	if _, _, err = f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
}

// A human refusal of the exact action in another run of the conversation is
// final for automatic paths, whatever the bounded window shows.
func TestMCPHumanRefusalOfExactActionIsConversationWide(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	q := createReviewedUnclaimed(t, f)
	other, err := f.s.store.AddRun(f.c.ID, f.r.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	diagnosticExec(t, f, `UPDATE runs SET status='running' WHERE id=?`, other.ID)
	other.Status = "running"
	refuseMCPAction(t, f, other, mcpApprovalHash(f.call))
	diagnosticExec(t, f, `UPDATE runs SET status='done' WHERE id=?`, other.ID)
	_, err = f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID)
	if out, ok := tooloutcome.FromError(err); !ok || out.Status != tooloutcome.Denied || out.Code != "approval_denied" {
		t.Fatalf("automatic approval bypassed a conversation refusal: %v", err)
	}
	// A later run's identical proposal is denied in code before any review.
	diagnosticExec(t, f, `UPDATE runs SET status='done' WHERE id=?`, f.r.ID)
	_, later, _, err := f.s.store.AddUserRun(f.c.ID, f.r.BotID, "Read the synthetic public fact for alpha again.", "synthetic-again")
	if err != nil {
		t.Fatal(err)
	}
	diagnosticExec(t, f, `UPDATE runs SET status='running' WHERE id=?`, later.ID)
	later.Status = "running"
	f.r = later
	calls := f.p.calls.Load()
	next := newMCPReviewProposal(t, f)
	err = f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, next)
	if out, ok := tooloutcome.FromError(err); !ok || out.Status != tooloutcome.Denied || f.p.calls.Load() != calls {
		t.Fatalf("refused action was reviewed again: %v calls=%d", err, f.p.calls.Load())
	}
	if got, _ := f.s.store.GetQuestion(next.ID); got.Status != questionCancelled {
		t.Fatal("refused re-proposal stayed open", got.Status)
	}
}

func TestMCPReviewNeverDropsHumanRefusals(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	seedLongMCPConversation(t, f)
	x, digest, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || digest == "" {
		t.Fatal(err)
	}
	if len(x.HumanRefusals) != 30 || x.Bounds == nil || !x.Bounds.RefusalPayloadsTruncated {
		t.Fatalf("refusals dropped to fit: %d %+v", len(x.HumanRefusals), x.Bounds)
	}
	// Restrictions that cannot fit close the proposal; none is silently dropped.
	diagnosticExec(t, f, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<160)
INSERT INTO questions(id,run_id,conversation_id,bot_id,type,prompt,status,answer_json,answered_by,created_at,updated_at,approval_json)
SELECT printf('synthetic-many-refusal-%d',i),?,?,?,'approval','synthetic','answered','false','synthetic-human',?,?,? FROM n`, f.r.ID, f.c.ID, f.r.BotID, now(), now(), `{"action":"synthetic","target":"synthetic","impact":"synthetic","payload":"`+strings.Repeat("p", 4000)+`"}`)
	diagnosticExec(t, f, `INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) SELECT id,run_id,id FROM questions WHERE id LIKE 'synthetic-many-refusal-%'`)
	_, digest, err = readMCPReviewContext(f.s.store.db, f.c, f.r)
	assertContextDiagnostic(t, err, mcpContextRestrictionsBudget)
	if digest != "" {
		t.Fatal("over-budget restrictions retained a digest")
	}
}

func TestMCPReviewKeepsEveryLaterHumanMessage(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	var seq int64
	if err := f.s.store.db.QueryRow(`SELECT seq FROM messages WHERE id=?`, f.r.TriggerMessageID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	later := func(prefix string, start int64, count int, content string) {
		diagnosticExec(t, f, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO messages(id,conversation_id,seq,role,content,created_at) SELECT printf('%s-%d',?,i),?,?+i,'user',?,? FROM n`, count, prefix, f.c.ID, seq+start, content, now())
	}
	later("synthetic-later", 1000, 15, "Synthetic later restriction.")
	x, _, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || len(x.LaterUserMessages) != 15 {
		t.Fatalf("later human messages capped: %d %v", len(x.LaterUserMessages), err)
	}
	later("synthetic-later-long", 2000, 75, strings.Repeat("停", 2000))
	_, digest, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	assertContextDiagnostic(t, err, mcpContextRestrictionsBudget)
	if digest != "" {
		t.Fatal("over-budget restrictions retained a digest")
	}
}
