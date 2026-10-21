package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/provider"
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

// expectAsksTool is expectAsks for another tool of the fixture server.
func expectAsksTool(t *testing.T, f *autoReviewFixture, tool, args string) Question {
	t.Helper()
	done := make(chan error, 1)
	before := f.effects.Load()
	go func() { done <- f.executeTool(context.Background(), tool, args) }()
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

// liveIdentity is the identity a grant made now would bind to.
func liveIdentity(t *testing.T, f *autoReviewFixture, tool string) (fingerprint, credential, digest string) {
	t.Helper()
	fingerprint, credential, ok := f.s.extensions.MCPServerIdentity("fixture")
	digest = f.s.extensions.MCPToolTier("fixture", tool).Digest
	if !ok || digest == "" {
		t.Fatalf("no live identity for %s", tool)
	}
	return
}

// plantAllowance writes a live row directly (what the question route would
// have written), for tests that vary one column.
func plantAllowance(t *testing.T, f *autoReviewFixture, id, botID, server, tool, fingerprint, credential, digest string) {
	t.Helper()
	if _, err := f.s.store.db.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,credential_target,tool_digest,source_question_id,created_at,created_by) VALUES(?,?,?,?,?,?,?,?,'',?,'human')`, id, f.s.reviewAccountID(), botID, server, tool, fingerprint, credential, digest, now()); err != nil {
		t.Fatal(err)
	}
}

// callFor is the gate's proposal for a fixture tool under the live binding.
func callFor(f *autoReviewFixture, tool string) extensions.MCPCallApproval {
	fingerprint, _, _ := f.s.extensions.MCPServerIdentity("fixture")
	return extensions.MCPCallApproval{Server: "fixture", Tool: tool, ConfigVersion: fingerprint, TierBinding: f.s.extensions.MCPToolTier("fixture", tool).Binding}
}

// standingDecidedCard makes a card for the fixture call that an allowance has
// already answered, ready to be claimed.
func standingDecidedCard(t *testing.T, f *autoReviewFixture, allowanceID string) (Run, Question) {
	t.Helper()
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
	if err = applyStandingAllowance(tx, f.c, &q, allowanceID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return r, q
}

func revokeRoute(t *testing.T, f *autoReviewFixture, id string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	f.s.routeStandingAllowances(rec, httptest.NewRequest(http.MethodDelete, "/api/standing-allowances/"+id, nil), "standing-allowances/"+id)
	return rec.Code
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
	f := newAutoReviewFixtureWith(t, true)
	id := approveAlways(t, f)
	// A different Bot still asks.
	other, err := f.s.store.CreateBot("other bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.c, Run{BotID: other.ID}, f.call); err != nil || got != "" {
		t.Fatalf("another Bot matched: %q %v", got, err)
	}
	// A different real write tool on the same server asks: a grant on A does
	// not cover B.
	newFixtureRun(t, f, "")
	expectAsksTool(t, f, "write_other", `{"target":"alpha"}`)
	if f.effects.Load() != 1 {
		t.Fatalf("the other tool ran under the grant: effects=%d", f.effects.Load())
	}
	callB := callFor(f, "write_other")
	if callB.TierBinding == "" || callB.Tool != "write_other" {
		t.Fatalf("no real proposal for the second tool: %+v", callB)
	}
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.c, f.r, callB); err != nil || got != "" {
		t.Fatalf("tool B matched the grant on tool A: %q %v", got, err)
	}
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.c, f.r, f.call); err != nil || got != id {
		t.Fatalf("the exact tuple did not match: %q %v", got, err)
	}
	// Each predicate of the match is load-bearing on its own: a row that
	// differs from the live call in exactly one column never matches.
	fingerprint, credential, digestA := liveIdentity(t, f, "read_public")
	_, _, digestB := liveIdentity(t, f, "write_other")
	if _, err := f.s.store.db.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by='test'`, now()); err != nil {
		t.Fatal(err)
	}
	for name, row := range map[string]struct {
		server, tool, fingerprint, credential, digest string
		call                                          extensions.MCPCallApproval
	}{
		"tool":       {"fixture", "read_public", fingerprint, credential, digestB, callB},
		"server":     {"elsewhere", "read_public", fingerprint, credential, digestA, f.call},
		"credential": {"fixture", "read_public", fingerprint, "other-credential", digestA, f.call},
		"definition": {"fixture", "read_public", fingerprint, credential, "other-digest", f.call},
		"config":     {"fixture", "read_public", "other-config", credential, digestA, f.call},
	} {
		rowID := "planted-" + name
		plantAllowance(t, f, rowID, f.r.BotID, row.server, row.tool, row.fingerprint, row.credential, row.digest)
		if got, err := f.s.matchStandingAllowance(f.s.store.db, f.c, f.r, row.call); err != nil || got != "" {
			t.Fatalf("a row differing only in %s matched: %q %v", name, got, err)
		}
		if _, err := f.s.store.db.Exec(`DELETE FROM mcp_standing_allowances WHERE id=?`, rowID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStandingAllowanceNeverForHighRiskOrReadOnly(t *testing.T) {
	f := newAutoReviewFixture(t)
	if _, err := f.s.extensions.SetToolTierOverride("fixture", "read_public", string(extensions.MCPTierHighRisk)); err != nil {
		t.Fatal(err)
	}
	if view := f.s.extensions.MCPToolTier("fixture", "read_public"); view.Tier != extensions.MCPTierHighRisk {
		t.Fatalf("tier = %s", view.Tier)
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
	// A planted row for the exact identity still never decides.
	fingerprint, credential, digest := liveIdentity(t, f, "read_public")
	plantAllowance(t, f, "planted-high", f.r.BotID, f.call.Server, f.call.Tool, fingerprint, credential, digest)
	view := f.s.extensions.MCPToolTier("fixture", "read_public")
	probe := extensions.MCPCallApproval{Server: "fixture", Tool: "read_public", ConfigVersion: fingerprint, TierBinding: view.Binding}
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.c, f.r, probe); err != nil || got != "" {
		t.Fatalf("planted allowance matched a high-risk tool: %q %v", got, err)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)

	// Read only: the tool executes without any card, and no allowance applies
	// or is offered.
	if _, err := f.s.extensions.SetToolTierOverride("fixture", "read_public", string(extensions.MCPTierReadOnly)); err != nil {
		t.Fatal(err)
	}
	view = f.s.extensions.MCPToolTier("fixture", "read_public")
	if view.Tier != extensions.MCPTierReadOnly {
		t.Fatalf("tier = %s", view.Tier)
	}
	probe.TierBinding = view.Binding
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.c, f.r, probe); err != nil || got != "" {
		t.Fatalf("planted allowance matched a read-only tool: %q %v", got, err)
	}
	if f.s.standingOfferable(f.c, probe) {
		t.Fatal("always-allow offered for a read-only tool")
	}
	newFixtureRun(t, f, "")
	cards := len(mustQuestions(t, f))
	before := f.effects.Load()
	if err := f.executeArgs(context.Background(), `{"target":"read-only"}`); err != nil || f.effects.Load() != before+1 || len(mustQuestions(t, f)) != cards {
		t.Fatalf("read-only tool: err=%v effects=%d cards %d->%d", err, f.effects.Load(), cards, len(mustQuestions(t, f)))
	}
}

func mustQuestions(t *testing.T, f *autoReviewFixture) []Question {
	t.Helper()
	qs, err := f.s.store.ListQuestions(f.c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return qs
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

// Changing the address and changing it back must not bring the old allowance
// back, whether the change went through the configuration writer (hook) or
// was seen only by a later call (file edit).
func TestStandingAllowanceAddressChangedAndRestoredStaysVoid(t *testing.T) {
	original := extensions.MCPServerConfig{URL: "https://synthetic.invalid/mcp"}
	moved := extensions.MCPServerConfig{URL: "https://synthetic.invalid/mcp-moved"}
	write := func(t *testing.T, f *autoReviewFixture, cfg extensions.MCPServerConfig) {
		config, _ := json.Marshal(map[string]any{"mcpServers": map[string]extensions.MCPServerConfig{"fixture": cfg}})
		if err := os.WriteFile(filepath.Join(f.dir, "mcp.json"), config, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("configuration writer", func(t *testing.T) {
		f := newAutoReviewFixture(t)
		approveAlways(t, f)
		if err := f.s.extensions.SaveMCP("fixture", moved, true); err != nil {
			t.Fatal(err)
		}
		if err := f.s.extensions.SaveMCP("fixture", original, true); err != nil {
			t.Fatal(err)
		}
		newFixtureRun(t, f, "")
		expectAsks(t, f, `{"target":"alpha"}`)
		if list, err := f.s.listStandingAllowances(); err != nil || len(list) != 1 || list[0].State != "server_changed" {
			t.Fatalf("list after restore: %+v %v", list, err)
		}
	})
	t.Run("seen by a call", func(t *testing.T) {
		f := newAutoReviewFixture(t)
		approveAlways(t, f)
		write(t, f, moved)
		newFixtureRun(t, f, "")
		expectAsks(t, f, `{"target":"alpha"}`) // observes the change
		write(t, f, original)
		newFixtureRun(t, f, "")
		expectAsks(t, f, `{"target":"beta"}`)
	})
	t.Run("seen by the list", func(t *testing.T) {
		f := newAutoReviewFixture(t)
		approveAlways(t, f)
		write(t, f, moved)
		if list, err := f.s.listStandingAllowances(); err != nil || len(list) != 1 || list[0].State != "server_changed" {
			t.Fatalf("list: %+v %v", list, err)
		}
		write(t, f, original)
		newFixtureRun(t, f, "")
		expectAsks(t, f, `{"target":"alpha"}`)
	})
}

// A server that redefines a granted tool (here its description) gets a card
// again, and Settings says the tool changed.
func TestStandingAllowanceVoidedByToolDefinitionChange(t *testing.T) {
	f := newAutoReviewFixture(t)
	id := approveAlways(t, f)
	f.tool.Description = "A different description the person never saw."
	f.s.extensions = f.newManager() // a restart: fresh catalogs over the same tier file
	newFixtureRun(t, f, "")
	q := expectAsks(t, f, `{"target":"alpha"}`)
	if q.Approval.Standing == nil {
		t.Fatal("the new card offers no allowance for the new definition")
	}
	list, err := f.s.listStandingAllowances()
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].State != "tool_changed" {
		t.Fatalf("settings list: %+v %v", list, err)
	}
	// Granting again supersedes the old row and binds to the new definition.
	newFixtureRun(t, f, "")
	if newID := approveAlways(t, f); newID == "" || newID == id {
		t.Fatalf("regrant id = %q", newID)
	}
	if list, _ = f.s.listStandingAllowances(); len(list) != 1 || list[0].State != "active" {
		t.Fatalf("list after regrant: %+v", list)
	}
}

// "Always ask me" ends the allowance; lowering the tier again does not bring
// it back.
func TestStandingAllowanceRevokedByAlwaysAsk(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	put := func(tier string) {
		req := httptest.NewRequest(http.MethodPut, "/api/extensions/mcp/fixture/tools/read_public", strings.NewReader(`{"tier":"`+tier+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		if !f.s.routeMCPToolTiers(rec, req, "extensions/mcp/fixture/tools/read_public") || rec.Code != 200 {
			t.Fatalf("set tier %q: %d %s", tier, rec.Code, rec.Body.String())
		}
	}
	put("high_risk")
	if allowanceCount(t, f) != 0 {
		t.Fatal("the allowance survived a raise to always-ask")
	}
	var by string
	if err := f.s.store.db.QueryRow(`SELECT revoked_by FROM mcp_standing_allowances`).Scan(&by); err != nil || by != "tier_raised" {
		t.Fatalf("revoked_by = %q %v", by, err)
	}
	if list, err := f.s.listStandingAllowances(); err != nil || len(list) != 0 {
		t.Fatalf("settings still lists it: %+v %v", list, err)
	}
	put("")
	newFixtureRun(t, f, "")
	expectAsks(t, f, `{"target":"alpha"}`)
}

// An override set under the allowance without the route (no revoke) still
// stops it deciding, and Settings shows the tier as the reason.
func TestStandingAllowanceStateIncludesTier(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	if _, err := f.s.extensions.SetToolTierOverride("fixture", "read_public", string(extensions.MCPTierHighRisk)); err != nil {
		t.Fatal(err)
	}
	list, err := f.s.listStandingAllowances()
	if err != nil || len(list) != 1 || list[0].State != "tier_changed" {
		t.Fatalf("list: %+v %v", list, err)
	}
}

func TestStandingAllowanceRevokeAsksAgainAndBitesAtClaim(t *testing.T) {
	f := newAutoReviewFixture(t)
	id := approveAlways(t, f)
	// Revoked after the decision, before the claim: the card returns to a person.
	r, q := standingDecidedCard(t, f, id)
	if code := revokeRoute(t, f, id); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if _, err := f.s.claimMCPApproval(context.Background(), f.c, r, f.call, q.ID); err == nil {
		t.Fatal("claim succeeded on a revoked allowance")
	}
	current, _ := f.s.store.GetQuestion(q.ID)
	if current.Status != questionPending || current.AnsweredBy != "" {
		t.Fatalf("card not returned to a person: %+v", current)
	}
	// The withdrawn card says what happened, not "remembered approval", and
	// does not claim to be decided by the allowance.
	review := current.Approval.Review
	if review == nil || strings.Contains(strings.ToLower(review.Reason), "remembered") || review.Source == mcpDecisionSourceStanding || review.DecisionSource != "" || review.GrantID != id || !strings.Contains(review.Reason, "standing allowance") {
		t.Fatalf("withdrawn card display: %+v", review)
	}
	// And the next call asks.
	f.s.store.db.Exec(`DELETE FROM questions WHERE id=?`, q.ID)
	f.s.store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, r.ID)
	newFixtureRun(t, f, "")
	expectAsks(t, f, `{"target":"alpha"}`)
	// Revoking again is a 404.
	if code := revokeRoute(t, f, id); code != 404 {
		t.Fatalf("second revoke: %d", code)
	}
}

// Nobody can answer a card in an unattended run: after a revoke the card is
// closed and the call refused with the named outcome.
func TestStandingAllowanceRevokedInUnattendedRunClosesTheCard(t *testing.T) {
	f := newAutoReviewFixture(t)
	id := approveAlways(t, f)
	r, q := standingDecidedCard(t, f, id)
	r.Kind = runKindSchedule
	if code := revokeRoute(t, f, id); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	_, err := f.s.claimMCPApproval(context.Background(), f.c, r, f.call, q.ID)
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Code != "mcp_needs_human_unattended" || f.effects.Load() != 1 {
		t.Fatalf("claim on a revoked allowance in an unattended run: %v effects=%d", err, f.effects.Load())
	}
	current, _ := f.s.store.GetQuestion(q.ID)
	if current.Status == questionPending || current.AnsweredBy != "" || current.Approval.Review == nil || current.Approval.Review.Status != "policy_denied" {
		t.Fatalf("the card was left for nobody to answer: %+v", current)
	}
	var claimed string
	_ = f.s.store.db.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&claimed)
	if claimed != "" {
		t.Fatal("a withdrawn answer was claimed")
	}
}

// Archive, delete and server removal revoke (the rows and their uses stay).
func TestStandingAllowanceRevokedWithBotArchiveDeleteAndServer(t *testing.T) {
	f := newAutoReviewFixture(t)
	id := approveAlways(t, f)
	if _, err := f.s.store.db.Exec(`INSERT INTO mcp_standing_allowance_uses(run_id,call_id,conversation_id,allowance_id,question_id,used_at) VALUES('run','call',?,?,'q',?)`, f.c.ID, id, now()); err != nil {
		t.Fatal(err)
	}
	revokedBy := func(allowance string) string {
		var by, at string
		if err := f.s.store.db.QueryRow(`SELECT revoked_by,revoked_at FROM mcp_standing_allowances WHERE id=?`, allowance).Scan(&by, &at); err != nil || at == "" {
			t.Fatalf("allowance %s: revoked_by=%q revoked_at=%q %v", allowance, by, at, err)
		}
		return by
	}
	if err := f.s.deleteMCPServer("fixture"); err != nil {
		t.Fatal(err)
	}
	if by := revokedBy(id); by != "server_removed" {
		t.Fatalf("server removal: revoked_by=%q", by)
	}
	var uses int
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_standing_allowance_uses WHERE allowance_id=?`, id).Scan(&uses)
	if uses == 0 {
		t.Fatal("server removal erased the uses")
	}
	for _, mode := range []string{"archive", "delete"} {
		bot, err := f.s.store.CreateBot("victim "+mode, "", "model")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.store.db.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,credential_target,tool_digest,source_question_id,created_at,created_by) VALUES(?,?,?,?,?,?,'c','d','',?,'human')`, "g-"+mode, f.s.reviewAccountID(), bot.ID, "fixture", "t", "fp", now()); err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.store.db.Exec(`INSERT INTO mcp_standing_allowance_uses(run_id,call_id,conversation_id,allowance_id,question_id,used_at) VALUES(?,NULL,?,?,'q',?)`, "run-"+mode, f.c.ID, "g-"+mode, now()); err != nil {
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
		want := "bot_" + mode + "d"
		if mode == "archive" {
			want = "bot_archived"
		}
		if by := revokedBy("g-" + mode); by != want {
			t.Fatalf("%s: revoked_by=%q want %q", mode, by, want)
		}
		_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_standing_allowance_uses WHERE allowance_id=?`, "g-"+mode).Scan(&uses)
		if uses != 1 {
			t.Fatalf("%s erased the uses: %d", mode, uses)
		}
	}
}

// Unarchiving does not bring a revoked allowance back, and an "Always allow"
// answered after the archive creates nothing.
func TestStandingAllowanceArchiveThenUnarchiveAndRacingGrant(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	// A card the person approves with "always allow" while the archive lands.
	pending := newFixtureRun(t, f, "")
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Allow this external tool call?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Call " + f.call.Tool, Target: "MCP server " + f.call.Server, Impact: "x", Payload: string(f.call.Arguments)}, ExpiresInSeconds: 600})
	q, err := f.s.store.CreateQuestion(f.c.ID, pending, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash,server,tool,config_fingerprint,tier_binding,standing_eligible) VALUES(?,?,?,?,?,?,?,1)`, q.ID, pending.ID, mcpApprovalHash(f.call), f.call.Server, f.call.Tool, f.call.ConfigVersion, f.call.TierBinding); err != nil {
		t.Fatal(err)
	}
	if q, _, err = f.s.store.AnswerQuestion(q.ID, "synthetic-human", true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.db.Exec(`UPDATE runs SET status='done' WHERE bot_id=?`, f.r.BotID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.setBotArchived(f.r.BotID, true, "", false); err != nil {
		t.Fatal(err)
	}
	if allowanceCount(t, f) != 0 {
		t.Fatal("archive left a live allowance")
	}
	if _, err = f.s.grantStandingAllowance(q, "synthetic-human"); !errors.Is(err, errStandingNotAvailable) {
		t.Fatalf("grant after the archive: %v", err)
	}
	if _, err = f.s.store.setBotArchived(f.r.BotID, false, "", false); err != nil {
		t.Fatal(err)
	}
	if allowanceCount(t, f) != 0 {
		t.Fatal("unarchive revived an allowance")
	}
	f.s.store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, pending.ID)
	newFixtureRun(t, f, "")
	expectAsks(t, f, `{"target":"alpha"}`)
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
	if !f.s.standingOfferable(f.c, f.call) {
		t.Fatal("not offered in a visible conversation: the hidden check proves nothing")
	}
	// A card the gate did not mark eligible (standing_eligible=0) cannot be
	// turned into a grant, though it is a real MCP approval with a current
	// binding and a human "yes".
	r := newFixtureRun(t, f, "")
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Allow this external tool call?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Call " + f.call.Tool, Target: "MCP server " + f.call.Server, Impact: "x", Payload: string(f.call.Arguments)}, ExpiresInSeconds: 600})
	q, err := f.s.store.CreateQuestion(f.c.ID, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash,server,tool,config_fingerprint,tier_binding,standing_eligible) VALUES(?,?,?,?,?,?,?,0)`, q.ID, r.ID, mcpApprovalHash(f.call), f.call.Server, f.call.Tool, f.call.ConfigVersion, f.call.TierBinding); err != nil {
		t.Fatal(err)
	}
	if code, _ := answerRoute(t, f, q.ID, `{"value":true,"always_allow":true}`); code != 400 || allowanceCount(t, f) != 0 {
		t.Fatalf("ineligible card granted: %d", code)
	}
	if current, _ := f.s.store.GetQuestion(q.ID); current.Status != questionPending {
		t.Fatalf("the refused answer changed the card: %+v", current)
	}
	// Even a human-answered ineligible card cannot be granted directly.
	answered, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.grantStandingAllowance(answered, "synthetic-human"); !errors.Is(err, errStandingNotAvailable) || allowanceCount(t, f) != 0 {
		t.Fatalf("grant from an ineligible card: %v", err)
	}
	// A card that is not an MCP approval at all.
	plain, _ := normalizeQuestionInput(askQuestionInput{Question: "x", Type: questionApproval, Approval: &ApprovalDetails{Action: "a", Target: "t", Impact: "i"}})
	pq, err := f.s.store.CreateQuestion(f.c.ID, f.r, plain)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := answerRoute(t, f, pq.ID, `{"value":true,"always_allow":true}`); code != 400 || allowanceCount(t, f) != 0 {
		t.Fatalf("non-MCP card granted: %d", code)
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
	id := approveAlways(t, f)
	newFixtureRun(t, f, "")
	if _, err := f.s.store.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, f.c.ID); err != nil {
		t.Fatal(err)
	}
	f.c.UserVisible = false
	if got, err := f.s.matchStandingAllowance(f.s.store.db, f.c, f.r, f.call); err != nil || got != "" {
		t.Fatalf("a hidden conversation matched an allowance: %q %v", got, err)
	}
	if got, err := f.s.standingAllowanceFor(f.s.store.db, f.r, f.call); err != nil || got != id {
		t.Fatalf("the allowance itself should be live: %q %v", got, err)
	}
	before := f.effects.Load()
	err := f.s.approveMCPCall(context.Background(), f.c, f.r, f.call)
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Code != "conversation_not_user_visible" || f.effects.Load() != before {
		t.Fatalf("hidden conversation was not refused: %v", err)
	}
}

// A scheduled root delegates to a Bot in a hidden trace conversation. The
// child is unattended, so the attended-run refusal does not apply; the grant
// must still not decide there.
func TestStandingAllowanceHiddenTraceOfScheduledRootRefuses(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	store := f.s.store
	child, err := store.CreateBot("delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, credential, digest := liveIdentity(t, f, "read_public")
	plantAllowance(t, f, "held-by-delegate", child.ID, "fixture", "read_public", fingerprint, credential, digest)
	if _, err = store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, f.r.ID); err != nil {
		t.Fatal(err)
	}
	spec := ScheduleSpec{Content: "Read the synthetic fact.", Kind: scheduleInterval, IntervalSeconds: 60, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), Timezone: "UTC"}
	schedule, err := store.createScheduleWithSource(f.c.ID, f.r.BotID, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, store, schedule.ID, time.Now().Add(-time.Minute))
	runs, err := store.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim occurrence: %+v %v", runs, err)
	}
	root := runs[0]
	if ok, err := store.SetRunStatus(root.ID, "running", ""); err != nil || !ok {
		t.Fatalf("root running: %v %v", ok, err)
	}
	_, children, err := store.AddBotMessages(f.c.ID, root.BotID, []string{child.ID}, root.ID, "Read only the synthetic public fact for the requester.")
	if err != nil || len(children) != 1 {
		t.Fatalf("delegate: %+v %v", children, err)
	}
	if ok, err := store.yieldScheduledRun(root.ID); err != nil || !ok {
		t.Fatalf("yield: %v %v", ok, err)
	}
	if ok, err := store.SetRunStatus(children[0].ID, "running", ""); err != nil {
		t.Fatalf("child running: %v %v", ok, err)
	}
	f.r, err = store.GetRun(children[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	f.c, err = store.GetConversation(f.r.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if f.c.UserVisible {
		t.Fatal("the delegation trace should be hidden")
	}
	if unattended, err := f.s.mcpRunUnattended(f.r); err != nil || !unattended {
		t.Fatalf("the child of a scheduled root should be unattended: %v %v", unattended, err)
	}
	if got, err := f.s.standingAllowanceFor(store.db, f.r, f.call); err != nil || got != "held-by-delegate" {
		t.Fatalf("the delegate does not hold the allowance: %q %v", got, err)
	}
	before := f.effects.Load()
	// A bounded context: if the gate ever parked on a card nobody can answer,
	// the test fails instead of hanging.
	ctx, cancel := context.WithTimeout(WithCallID(context.Background(), "call-hidden-sched"), 15*time.Second)
	defer cancel()
	err = f.executeArgs(ctx, `{"target":"zeta"}`)
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Code != "conversation_not_user_visible" || f.effects.Load() != before {
		t.Fatalf("hidden scheduled trace: err=%v effects %d->%d", err, before, f.effects.Load())
	}
}

// AutoReview "auto": the allowance replaces only the reviewer's "a person
// decides". A reviewer deny still blocks.
func TestStandingAllowanceAutoModeKeepsReviewerDeny(t *testing.T) {
	setup := func(t *testing.T, verdict string) (*autoReviewFixture, string) {
		f := newAutoReviewFixture(t)
		id := approveAlways(t, f)
		if err := f.s.store.putAutoReviewMode("auto"); err != nil {
			t.Fatal(err)
		}
		f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			return reviewReply(req, verdict), nil
		}
		newFixtureRun(t, f, "")
		return f, id
	}
	uses := func(f *autoReviewFixture, id string) int {
		var n int
		_ = f.s.store.db.QueryRow(`SELECT use_count FROM mcp_standing_allowances WHERE id=?`, id).Scan(&n)
		return n
	}
	t.Run("deny blocks", func(t *testing.T) {
		f, id := setup(t, "deny")
		before := f.effects.Load()
		err := f.executeArgs(WithCallID(context.Background(), "call-auto-deny"), `{"target":"beta"}`)
		if err == nil || f.effects.Load() != before || f.p.calls.Load() == 0 || uses(f, id) != 0 {
			t.Fatalf("a reviewer deny did not block: err=%v effects %d->%d reviewer calls=%d uses=%d", err, before, f.effects.Load(), f.p.calls.Load(), uses(f, id))
		}
		if out, ok := tooloutcome.FromError(err); !ok || out.Certainty != "not_executed" {
			t.Fatalf("deny outcome: %v", err)
		}
		// The allowance never answered the denied card.
		if q := cardForRun(t, f, f.r.ID); q.AnsweredBy != "" || q.Status == questionAnswered || q.Approval.Review == nil || q.Approval.Review.Status != "policy_denied" {
			t.Fatalf("a denied card was decided: %+v", q)
		}
	})
	t.Run("needs a person proceeds", func(t *testing.T) {
		f, id := setup(t, "needs_human")
		before := f.effects.Load()
		if err := f.executeArgs(WithCallID(context.Background(), "call-auto-human"), `{"target":"beta"}`); err != nil || f.effects.Load() != before+1 {
			t.Fatalf("needs_human with an allowance: err=%v effects %d->%d", err, before, f.effects.Load())
		}
		q := cardForRun(t, f, f.r.ID)
		if q.AnsweredBy != standingAllowanceActor || q.Approval.Review == nil || q.Approval.Review.GrantID != id || f.p.calls.Load() == 0 || uses(f, id) != 1 {
			t.Fatalf("auto-mode standing decision: %+v reviewer calls=%d uses=%d", q, f.p.calls.Load(), uses(f, id))
		}
	})
	t.Run("revoked in between asks a person", func(t *testing.T) {
		f, id := setup(t, "needs_human")
		if code := revokeRoute(t, f, id); code != 200 {
			t.Fatalf("revoke: %d", code)
		}
		before := f.effects.Load()
		done := make(chan error, 1)
		go func() { done <- f.executeArgs(context.Background(), `{"target":"beta"}`) }()
		q := pendingCard(t, f)
		if q.AnsweredBy != "" || f.effects.Load() != before {
			t.Fatalf("expected a card for a person: %+v", q)
		}
		_, _, _ = f.s.store.AnswerQuestion(q.ID, "synthetic-human", false)
		_ = waitReviewDone(t, done)
	})
}

// The audit of an allowance-decided call is part of its claim: counted and
// linked inside the claim transaction, with a null call id when the runtime
// gave none.
func TestStandingAllowanceUseRecordedInClaimTransaction(t *testing.T) {
	t.Run("empty call id still leaves a use row", func(t *testing.T) {
		f := newAutoReviewFixture(t)
		id := approveAlways(t, f)
		r, q := standingDecidedCard(t, f, id)
		r.reviewCallID = ""
		res, err := f.s.claimMCPApproval(context.Background(), f.c, r, f.call, q.ID)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			t.Fatalf("claim rows = %d", n)
		}
		var uses, nullCalls int
		var count int
		_ = f.s.store.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(call_id IS NULL),0) FROM mcp_standing_allowance_uses WHERE allowance_id=? AND question_id=?`, id, q.ID).Scan(&uses, &nullCalls)
		_ = f.s.store.db.QueryRow(`SELECT use_count FROM mcp_standing_allowances WHERE id=?`, id).Scan(&count)
		if uses != 1 || nullCalls != 1 || count != 1 {
			t.Fatalf("uses=%d null call ids=%d use_count=%d", uses, nullCalls, count)
		}
	})
	t.Run("a failed audit write fails the claim", func(t *testing.T) {
		f := newAutoReviewFixture(t)
		id := approveAlways(t, f)
		r, q := standingDecidedCard(t, f, id)
		if _, err := f.s.store.db.Exec(`DROP TABLE mcp_standing_allowance_uses`); err != nil {
			t.Fatal(err)
		}
		r.reviewCallID = "call-audit"
		if _, err := f.s.claimMCPApproval(context.Background(), f.c, r, f.call, q.ID); err == nil {
			t.Fatal("the call was claimed without its audit row")
		}
		var claimed string
		var count int
		_ = f.s.store.db.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&claimed)
		_ = f.s.store.db.QueryRow(`SELECT use_count FROM mcp_standing_allowances WHERE id=?`, id).Scan(&count)
		if claimed != "" || count != 0 {
			t.Fatalf("claimed_at=%q use_count=%d after a failed audit write", claimed, count)
		}
	})
}
