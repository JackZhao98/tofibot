package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func allowanceCount(t *testing.T, f *autoReviewFixture) int {
	t.Helper()
	var n int
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_standing_allowances WHERE revoked_at=''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// approveAlways runs the fixture call, approves its card with "always allow"
// through the owner route and returns the allowance id.
func approveAlways(t *testing.T, f *autoReviewFixture) string {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := pendingCard(t, f)
	if q.Approval == nil || q.Approval.Standing == nil {
		t.Fatalf("card offers no always-allow: %+v", q.Approval)
	}
	code, out := answerRoute(t, f, q.ID, `{"value":true,"always_allow":true}`)
	if code != 200 || out["allowance"] == nil {
		t.Fatalf("always-allow answer: %d %s", code, out)
	}
	var a MCPStandingAllowance
	_ = json.Unmarshal(out["allowance"], &a)
	if err := waitReviewDone(t, done); err != nil {
		t.Fatalf("approved call did not execute: %v", err)
	}
	return a.ID
}

// expectAsks runs the call and asserts a person is asked (no decision), then
// declines it.
func expectAsks(t *testing.T, f *autoReviewFixture, args string) Question {
	t.Helper()
	done := make(chan error, 1)
	before := f.effects.Load()
	go func() { done <- f.executeArgs(context.Background(), args) }()
	q := pendingCard(t, f)
	if q.AnsweredBy != "" || f.effects.Load() != before {
		t.Fatalf("expected a card for a person, got %+v effects=%d", q, f.effects.Load())
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
	return q
}

func TestStandingAllowanceCreatedAndSkipsCardInDMAndScheduledRun(t *testing.T) {
	f := newAutoReviewFixture(t)
	id := approveAlways(t, f)
	if id == "" || allowanceCount(t, f) != 1 || f.effects.Load() != 1 {
		t.Fatalf("allowance=%q count=%d effects=%d", id, allowanceCount(t, f), f.effects.Load())
	}
	// A different payload in a new DM run: no card, executes, different args.
	second := newFixtureRun(t, f, "")
	if err := f.executeArgs(WithCallID(context.Background(), "call-dm"), `{"target":"beta"}`); err != nil || f.effects.Load() != 2 {
		t.Fatalf("standing call in DM: %v effects=%d", err, f.effects.Load())
	}
	q := cardForRun(t, f, second.ID)
	if q.AnsweredBy != standingAllowanceActor || q.Approval.Review == nil || q.Approval.Review.DecisionSource != mcpDecisionSourceStanding || q.Approval.Review.GrantID != id {
		t.Fatalf("standing decision not recorded: %+v", q)
	}
	// Scheduled run (unattended): also passes.
	sched := newFixtureRun(t, f, "")
	if _, err := f.s.store.db.Exec(`UPDATE runs SET kind=? WHERE id=?`, runKindSchedule, sched.ID); err != nil {
		t.Fatal(err)
	}
	f.r.Kind = runKindSchedule
	if err := f.executeArgs(WithCallID(context.Background(), "call-sched"), `{"target":"gamma"}`); err != nil || f.effects.Load() != 3 {
		t.Fatalf("standing call in scheduled run: %v effects=%d", err, f.effects.Load())
	}
	var uses int
	var last string
	if err := f.s.store.db.QueryRow(`SELECT use_count,last_used_at FROM mcp_standing_allowances WHERE id=?`, id).Scan(&uses, &last); err != nil || uses != 2 || last == "" {
		t.Fatalf("use_count=%d last=%q %v", uses, last, err)
	}
	// Audit: the activity of the scheduled call is marked with the grant.
	if _, err := f.s.store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, f.c.ID, f.r.BotID, sched.ID, "call-sched", "call_mcp_tool", "completed", now(), now()); err != nil {
		t.Fatal(err)
	}
	acts, err := f.s.store.ToolActivities(f.c.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		if a.CallID == "call-sched" {
			found = a.AllowedBy != nil && a.AllowedBy.Kind == mcpDecisionSourceStanding && a.AllowedBy.ID == id
		}
	}
	if !found {
		t.Fatalf("tool activity lacks the allowance mark: %+v", acts)
	}
}

func TestStandingAllowanceScopeOtherBotToolServer(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	// A different Bot still asks.
	other, err := f.s.store.CreateBot("other bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	oc, _ := f.s.store.GetConversation(other.DMConversationID)
	_ = oc
	r := newFixtureRun(t, f, "")
	if _, err := f.s.store.db.Exec(`UPDATE runs SET bot_id=? WHERE id=?`, other.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	f.r.BotID = other.ID
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.r, f.call); err != nil || got != "" {
		t.Fatalf("another Bot matched: %q %v", got, err)
	}
	f.r.BotID = r.BotID
	// A different tool and a different server never match.
	for name, call := range map[string]extensions.MCPCallApproval{
		"tool":   func() extensions.MCPCallApproval { c := f.call; c.Tool = "other_tool"; return c }(),
		"server": func() extensions.MCPCallApproval { c := f.call; c.Server = "elsewhere"; return c }(),
	} {
		if got, err := f.s.matchStandingAllowance(f.s.store.db, f.r, call); err != nil || got != "" {
			t.Fatalf("%s matched: %q %v", name, got, err)
		}
	}
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.r, f.call); err != nil || got == "" {
		t.Fatalf("the exact tuple did not match: %q %v", got, err)
	}
}

func TestStandingAllowanceNeverForHighRiskOrReadOnly(t *testing.T) {
	f := newAutoReviewFixture(t)
	if _, err := f.s.extensions.SetToolTierOverride("fixture", "read_public", string(extensions.MCPTierHighRisk)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := pendingCard(t, f)
	if q.Approval.Standing != nil {
		t.Fatalf("high-risk card offered always-allow: %+v", q.Approval.Standing)
	}
	if code, _ := answerRoute(t, f, q.ID, `{"value":true,"always_allow":true}`); code != 400 {
		t.Fatalf("always_allow on a high-risk card: %d", code)
	}
	if current, _ := f.s.store.GetQuestion(q.ID); current.Status != questionPending || allowanceCount(t, f) != 0 {
		t.Fatalf("refused answer changed the card or stored a grant: %+v", current)
	}
	// A planted row for the exact binding still never decides.
	view := f.s.extensions.MCPToolTier("fixture", "read_public")
	_ = view
	if _, err := f.s.store.db.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,source_question_id,created_at,created_by) VALUES('planted',?,?,?,?,?,'',?,'human')`, f.s.reviewAccountID(), f.r.BotID, f.call.Server, f.call.Tool, f.call.ConfigVersion, now()); err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.r, f.call); err != nil || got != "" {
		t.Fatalf("planted allowance matched a high-risk tool: %q %v", got, err)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
}

func TestStandingAllowanceVoidedByServerIdentityChange(t *testing.T) {
	f := newAutoReviewFixture(t)
	id := approveAlways(t, f)
	newFixtureRun(t, f, "")
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]extensions.MCPServerConfig{"fixture": {URL: "https://synthetic.invalid/mcp-moved"}}})
	if err := os.WriteFile(filepath.Join(f.dir, "mcp.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	q := expectAsks(t, f, `{"target":"alpha"}`)
	if q.Approval.Standing == nil {
		t.Fatalf("the card for the changed server offers no new allowance")
	}
	list, err := f.s.listStandingAllowances()
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].State != "server_changed" {
		t.Fatalf("settings list: %+v %v", list, err)
	}
}

func TestStandingAllowanceRevokeAsksAgainAndBitesAtClaim(t *testing.T) {
	f := newAutoReviewFixture(t)
	id := approveAlways(t, f)
	// Revoked after the decision, before the claim: the card returns to a person.
	r := newFixtureRun(t, f, "")
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Allow this external tool call?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Call " + f.call.Tool, Target: "MCP server " + f.call.Server, Impact: "x", Payload: string(f.call.Arguments)}, ExpiresInSeconds: 600})
	q, err := f.s.store.CreateQuestion(f.c.ID, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash,server,tool,config_fingerprint,tier_binding,standing_eligible) VALUES(?,?,?,?,?,?,?,1)`, q.ID, r.ID, mcpApprovalHash(f.call), f.call.Server, f.call.Tool, f.call.ConfigVersion, f.call.TierBinding); err != nil {
		t.Fatal(err)
	}
	tx, _ := f.s.store.db.Begin()
	if err = applyStandingAllowance(tx, f.c, &q, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/standing-allowances/"+id, nil)
	rec := httptest.NewRecorder()
	if !f.s.routeStandingAllowances(rec, req, "standing-allowances/"+id) || rec.Code != 200 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if _, err = f.s.claimMCPApproval(context.Background(), f.c, r, f.call, q.ID); err == nil {
		t.Fatal("claim succeeded on a revoked allowance")
	}
	current, _ := f.s.store.GetQuestion(q.ID)
	if current.Status != questionPending || current.AnsweredBy != "" {
		t.Fatalf("card not returned to a person: %+v", current)
	}
	// And the next call asks.
	f.s.store.db.Exec(`DELETE FROM questions WHERE id=?`, q.ID)
	f.s.store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, r.ID)
	newFixtureRun(t, f, "")
	expectAsks(t, f, `{"target":"alpha"}`)
	// Revoking again is a 404.
	rec = httptest.NewRecorder()
	f.s.routeStandingAllowances(rec, httptest.NewRequest(http.MethodDelete, "/api/standing-allowances/"+id, nil), "standing-allowances/"+id)
	if rec.Code != 404 {
		t.Fatalf("second revoke: %d", rec.Code)
	}
}

func TestStandingAllowanceRemovedWithBotArchiveDeleteAndServer(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	if err := f.s.deleteMCPServer("fixture"); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_standing_allowances`).Scan(&n)
	if n != 0 {
		t.Fatalf("server removal left %d allowances", n)
	}
	for _, mode := range []string{"archive", "delete"} {
		bot, err := f.s.store.CreateBot("victim "+mode, "", "model")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.store.db.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,source_question_id,created_at,created_by) VALUES(?,?,?,?,?,?,'',?,'human')`, "g-"+mode, f.s.reviewAccountID(), bot.ID, "fixture", "t", "fp", now()); err != nil {
			t.Fatal(err)
		}
		if mode == "archive" {
			_, err = f.s.store.setBotArchived(bot.ID, true, "", false)
		} else {
			_, err = f.s.store.DeleteBot(bot.ID)
		}
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_standing_allowances WHERE bot_id=?`, bot.ID).Scan(&n)
		if n != 0 {
			t.Fatalf("%s left %d allowances", mode, n)
		}
	}
}

func TestStandingAllowanceHiddenConversationAndModelCannotCreate(t *testing.T) {
	f := newAutoReviewFixture(t)
	// The model-visible request_approval input cannot carry the offer.
	if _, err := normalizeQuestionInput(askQuestionInput{Question: "x", Type: questionApproval, Approval: &ApprovalDetails{Action: "a", Target: "t", Impact: "i", Standing: &MCPStandingOffer{Server: "s", Tool: "t"}}}); err == nil {
		t.Fatal("model-supplied standing offer accepted")
	}
	// Not offered where no person can see the card.
	hidden := f.c
	hidden.UserVisible = false
	if f.s.standingOfferable(hidden, f.call) {
		t.Fatal("offered in a hidden conversation")
	}
	// A card the gate did not mark eligible cannot be turned into a grant.
	plain, _ := normalizeQuestionInput(askQuestionInput{Question: "x", Type: questionApproval, Approval: &ApprovalDetails{Action: "a", Target: "t", Impact: "i"}})
	q, err := f.s.store.CreateQuestion(f.c.ID, f.r, plain)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := answerRoute(t, f, q.ID, `{"value":true,"always_allow":true}`); code != 400 || allowanceCount(t, f) != 0 {
		t.Fatalf("non-MCP card granted: %d", code)
	}
	if !strings.Contains(standingAllowanceActor, "standing") {
		t.Fatal("actor constant")
	}
}

// WithCallID binds a provider call id the way the runtime does.
func WithCallID(ctx context.Context, id string) context.Context {
	return runtime.WithToolCallID(ctx, id)
}

// A hidden conversation keeps its refusal even for a Bot that holds a
// standing allowance for the exact tool.
func TestStandingAllowanceHiddenConversationStillRefuses(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	newFixtureRun(t, f, "")
	if _, err := f.s.store.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, f.c.ID); err != nil {
		t.Fatal(err)
	}
	f.c.UserVisible = false
	before := f.effects.Load()
	err := f.s.approveMCPCall(context.Background(), f.c, f.r, f.call)
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Code != "conversation_not_user_visible" || f.effects.Load() != before {
		t.Fatalf("hidden conversation was not refused: %v", err)
	}
}
