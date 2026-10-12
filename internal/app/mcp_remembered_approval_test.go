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
	"github.com/google/uuid"
)

// newFixtureRun starts another attended chat run in the fixture conversation
// (or in conv when given) and points the fixture at it.
func newFixtureRun(t *testing.T, f *autoReviewFixture, conv string) Run {
	t.Helper()
	if conv == "" {
		conv = f.c.ID
	}
	if _, err := f.s.store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, f.r.ID); err != nil {
		t.Fatal(err)
	}
	// The client id is an idempotency key: a fresh one per run.
	_, r, _, err := f.s.store.AddUserRun(conv, f.r.BotID, "Read the synthetic public fact for alpha.", "synthetic-native-ingress-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	r.Status = "running"
	f.r = r
	return r
}

// answerRoute answers a card through the owner question route (the only
// path that may create a grant) and returns the decoded response.
func answerRoute(t *testing.T, f *autoReviewFixture, id string, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/questions/"+id+"/answer", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if !f.s.routeQuestions(rec, req, "questions/"+id+"/answer") {
		t.Fatal("question route not matched")
	}
	var out map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func pendingCard(t *testing.T, f *autoReviewFixture) Question {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		qs, err := f.s.store.ListQuestions(f.c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(qs) > 0 && qs[0].RunID == f.r.ID && qs[0].Status == questionPending {
			return qs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending card for the current run")
	return Question{}
}

func cardForRun(t *testing.T, f *autoReviewFixture, run string) Question {
	t.Helper()
	qs, err := f.s.store.ListQuestions(f.c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		if q.RunID == run {
			return q
		}
	}
	t.Fatalf("no card for run %s", run)
	return Question{}
}

func grantCount(t *testing.T, f *autoReviewFixture) int {
	t.Helper()
	var n int
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_remembered_approvals`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// approveAndRemember runs the fixture call, approves its card through the
// route with remember=true and returns the grant id.
func approveAndRemember(t *testing.T, f *autoReviewFixture) string {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := pendingCard(t, f)
	if q.Approval == nil || q.Approval.Remember == nil {
		t.Fatalf("card offers no remember option: %+v", q.Approval)
	}
	code, out := answerRoute(t, f, q.ID, `{"value":true,"remember":true}`)
	if code != 200 || out["remembered"] == nil {
		t.Fatalf("remember answer: %d %s", code, out)
	}
	var grant MCPRememberedApproval
	_ = json.Unmarshal(out["remembered"], &grant)
	if err := waitReviewDone(t, done); err != nil {
		t.Fatalf("approved call did not execute: %v", err)
	}
	return grant.ID
}

// The target key: identifier arguments named by the schema bind the exact
// identifier values and nothing else; without one only identical arguments
// match. It never widens to any arguments.
func TestRememberTargetKeyDerivation(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"string"},"content":{"type":"string"}},"required":["page_id","content"]}`)
	a, ok := mcpRememberTargetFor(schema, json.RawMessage(`{"page_id":"page-q4","content":"v1"}`))
	b, _ := mcpRememberTargetFor(schema, json.RawMessage(`{"content":"v2","page_id":"page-q4"}`))
	c, _ := mcpRememberTargetFor(schema, json.RawMessage(`{"page_id":"page-other","content":"v1"}`))
	if !ok || a.Kind != mcpRememberTargetIdentifier || a.Summary != "page_id = page-q4" || a.Key != b.Key || a.Key == c.Key {
		t.Fatalf("identifier binding: a=%+v b=%+v c=%+v", a, b, c)
	}
	// camelCase and several identifiers: every present identifier binds.
	schema2 := json.RawMessage(`{"type":"object","properties":{"issueKey":{"type":"string"},"to":{"type":"array"},"body":{"type":"string"},"valid":{"type":"boolean"},"photo":{"type":"string"}}}`)
	d, _ := mcpRememberTargetFor(schema2, json.RawMessage(`{"issueKey":"OPS-1","to":["a@example.com"],"body":"x","valid":true,"photo":"p"}`))
	e, _ := mcpRememberTargetFor(schema2, json.RawMessage(`{"issueKey":"OPS-1","to":["a@example.com"],"body":"y","valid":false,"photo":"q"}`))
	g, _ := mcpRememberTargetFor(schema2, json.RawMessage(`{"issueKey":"OPS-1","to":["b@example.com"],"body":"x"}`))
	if d.Kind != mcpRememberTargetIdentifier || d.Key != e.Key || d.Key == g.Key || strings.Contains(d.Summary, "valid") || strings.Contains(d.Summary, "photo") || !strings.Contains(d.Summary, "issueKey = OPS-1") {
		t.Fatalf("multi identifier binding: d=%+v e=%+v g=%+v", d, e, g)
	}
	// No identifier argument: only the identical arguments match.
	schema3 := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}}}`)
	h, _ := mcpRememberTargetFor(schema3, json.RawMessage(`{"query":"faq","limit":5}`))
	i, _ := mcpRememberTargetFor(schema3, json.RawMessage(`{"limit":5,"query":"faq"}`))
	j, _ := mcpRememberTargetFor(schema3, json.RawMessage(`{"query":"faq","limit":6}`))
	if h.Kind != mcpRememberTargetExact || h.Summary != "" || h.Key != i.Key || h.Key == j.Key {
		t.Fatalf("exact binding: h=%+v i=%+v j=%+v", h, i, j)
	}
	// An identifier that is an object is not a scalar target: exact arguments.
	k, _ := mcpRememberTargetFor(schema, json.RawMessage(`{"page_id":{"nested":true},"content":"v1"}`))
	if k.Kind != mcpRememberTargetExact {
		t.Fatalf("object identifier widened the key: %+v", k)
	}
	if _, ok := mcpRememberTargetFor(json.RawMessage(`null`), json.RawMessage(`{"a":1}`)); ok {
		t.Fatal("missing schema produced a target")
	}
	if _, ok := mcpRememberTargetFor(schema, json.RawMessage(`[1]`)); ok {
		t.Fatal("non-object arguments produced a target")
	}
}

// Off mode: the first call asks; "Approve and remember" executes it and
// records a grant; the identical target in a later run of the same chat
// executes without a card, with the decision source and grant id on the
// record; a different target asks again.
func TestRememberedApprovalSameTargetSkipsCardOffMode(t *testing.T) {
	f := newAutoReviewFixture(t)
	first := f.r
	grantID := approveAndRemember(t, f)
	if grantID == "" || f.effects.Load() != 1 || grantCount(t, f) != 1 {
		t.Fatalf("grant=%q effects=%d grants=%d", grantID, f.effects.Load(), grantCount(t, f))
	}
	if q := cardForRun(t, f, first.ID); q.AnsweredBy != "human" {
		t.Fatalf("the first card was not answered by the human: %+v", q)
	}
	// Same target, new run: no person asked.
	second := newFixtureRun(t, f, "")
	if err := f.execute(context.Background()); err != nil || f.effects.Load() != 2 {
		t.Fatalf("remembered call: %v effects=%d", err, f.effects.Load())
	}
	q := cardForRun(t, f, second.ID)
	if q.Status != questionAnswered || q.AnsweredBy != rememberedApprovalActor || string(q.Answer) != "true" || q.Approval.Review == nil || q.Approval.Review.DecisionSource != mcpDecisionSourceRemembered || q.Approval.Review.GrantID != grantID {
		t.Fatalf("remembered decision not recorded: %+v review=%+v", q, q.Approval.Review)
	}
	var recordedGrant, claimed string
	var uses int
	if err := f.s.store.db.QueryRow(`SELECT grant_id,claimed_at FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&recordedGrant, &claimed); err != nil || recordedGrant != grantID || claimed == "" {
		t.Fatalf("approval row audit: grant=%q claimed=%q %v", recordedGrant, claimed, err)
	}
	if err := f.s.store.db.QueryRow(`SELECT use_count FROM mcp_remembered_approvals WHERE id=?`, grantID).Scan(&uses); err != nil || uses != 1 {
		t.Fatalf("use count = %d %v", uses, err)
	}
	// The identical call in the same run is still one-shot: no replay.
	if err := f.execute(context.Background()); err == nil || f.effects.Load() != 2 {
		t.Fatalf("remembered approval replayed: %v effects=%d", err, f.effects.Load())
	}
	// A different target asks a person.
	third := newFixtureRun(t, f, "")
	done := make(chan error, 1)
	go func() { done <- f.executeArgs(context.Background(), `{"target":"beta"}`) }()
	q = pendingCard(t, f)
	if q.RunID != third.ID || q.AnsweredBy != "" || f.effects.Load() != 2 {
		t.Fatalf("different target did not ask: %+v effects=%d", q, f.effects.Load())
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	if err := waitReviewDone(t, done); err == nil || f.effects.Load() != 2 {
		t.Fatalf("declined different target ran: %v", err)
	}
}

// Auto mode: the reviewer still assesses every call. Its needs_human is what
// the grant answers; the reviewer is asked each time and its verdict and
// risk stay on the record.
func TestRememberedApprovalAutoModeStillReviews(t *testing.T) {
	f := newAutoReviewFixture(t)
	setSyntheticMCPEffect(t, f)
	_ = f.s.store.putAutoReviewMode("auto")
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return v5ReviewReply(req, "needs_human", "medium", true), nil
	}
	grantID := approveAndRemember(t, f)
	if f.p.calls.Load() != 1 || f.effects.Load() != 1 {
		t.Fatalf("first call: reviews=%d effects=%d", f.p.calls.Load(), f.effects.Load())
	}
	second := newFixtureRun(t, f, "")
	if err := f.execute(context.Background()); err != nil || f.effects.Load() != 2 || f.p.calls.Load() != 2 {
		t.Fatalf("remembered call: %v effects=%d reviews=%d", err, f.effects.Load(), f.p.calls.Load())
	}
	q := cardForRun(t, f, second.ID)
	if q.AnsweredBy != rememberedApprovalActor || q.Approval.Review.Status != "human_required" || q.Approval.Review.RiskLevel != "medium" || q.Approval.Review.DecisionSource != mcpDecisionSourceRemembered || q.Approval.Review.GrantID != grantID {
		t.Fatalf("auto-mode remembered record: %+v", q.Approval.Review)
	}
	var reviewStatus string
	if err := f.s.store.db.QueryRow(`SELECT status FROM mcp_auto_reviews WHERE question_id=?`, q.ID).Scan(&reviewStatus); err != nil || reviewStatus != "human_required" {
		t.Fatalf("review row = %q %v", reviewStatus, err)
	}
	// A reviewer deny is never overridden by a memory.
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return v5ReviewReply(req, "deny", "high", false), nil
	}
	newFixtureRun(t, f, "")
	if err := f.execute(context.Background()); err == nil || f.effects.Load() != 2 {
		t.Fatalf("memory overrode a deny: %v effects=%d", err, f.effects.Load())
	}
}

// A grant belongs to one conversation: the same Bot, tool and target in
// another conversation asks a person.
func TestRememberedApprovalDoesNotCarryToOtherConversation(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAndRemember(t, f)
	other, err := f.s.store.CreateBot("other member", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.s.store.CreateGroup("other chat", []string{f.r.BotID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	home := f.c
	f.c = group
	newFixtureRun(t, f, group.ID)
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := pendingCard(t, f)
	if q.ConversationID != group.ID || q.AnsweredBy != "" || f.effects.Load() != 1 {
		t.Fatalf("grant crossed conversations: %+v effects=%d", q, f.effects.Load())
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
	f.c = home
}

// The owner's "Always ask me" tier never offers or uses a memory: no
// remember option on the card, the route refuses it, and a planted grant
// does not decide the card.
func TestRememberedApprovalNeverForAlwaysAskTool(t *testing.T) {
	f := newAutoReviewFixture(t)
	if _, err := f.s.extensions.SetToolTierOverride("fixture", "read_public", string(extensions.MCPTierHighRisk)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := pendingCard(t, f)
	if q.Approval.Remember != nil {
		t.Fatalf("Always-ask card offered remember: %+v", q.Approval.Remember)
	}
	if code, _ := answerRoute(t, f, q.ID, `{"value":true,"remember":true}`); code != 400 {
		t.Fatalf("remember on an Always-ask card: %d", code)
	}
	if current, _ := f.s.store.GetQuestion(q.ID); current.Status != questionPending || grantCount(t, f) != 0 {
		t.Fatalf("refused remember answered the card or stored a grant: %+v grants=%d", current, grantCount(t, f))
	}
	// A grant planted for the exact current binding still never decides.
	view := f.s.extensions.MCPToolTier("fixture", "read_public")
	target, _ := mcpRememberTargetFor(f.call.Schema, f.call.Arguments)
	if _, err := f.s.store.db.Exec(`INSERT INTO mcp_remembered_approvals(id,account_id,conversation_id,bot_id,source_question_id,source_run_id,server,tool,config_fingerprint,tier_binding,target_key,target_kind,created_at,created_by,expires_at) VALUES('planted',?,?,?,'','',?,?,?,?,?,?,?,'human',?)`, f.s.reviewAccountID(), f.c.ID, f.r.BotID, f.call.Server, f.call.Tool, f.call.ConfigVersion, view.Binding, target.Key, target.Kind, now(), time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", true); err != nil {
		t.Fatal(err)
	}
	if err := waitReviewDone(t, done); err != nil || f.effects.Load() != 1 {
		t.Fatalf("human approval: %v", err)
	}
	newFixtureRun(t, f, "")
	go func() { done <- f.execute(context.Background()) }()
	q = pendingCard(t, f)
	if q.AnsweredBy != "" || f.effects.Load() != 1 || f.p.calls.Load() != 0 {
		t.Fatalf("planted grant decided an Always-ask call: %+v", q)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
}

// A tier change (owner override) moves the tier binding: the grant no
// longer matches and the next call asks. A configuration edit moves the
// fingerprint the same way.
func TestRememberedApprovalInvalidatedByTierOrConfigChange(t *testing.T) {
	for _, change := range []string{"tier", "config"} {
		t.Run(change, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			approveAndRemember(t, f)
			switch change {
			case "tier":
				if _, err := f.s.extensions.SetToolTierOverride("fixture", "read_public", string(extensions.MCPTierWrite)); err != nil {
					t.Fatal(err)
				}
			case "config":
				config, _ := json.Marshal(map[string]any{"mcpServers": map[string]extensions.MCPServerConfig{"fixture": {URL: "https://synthetic.invalid/mcp", Headers: map[string]string{"X-Tenant": "b"}}}})
				if err := writeFixtureFile(f, "mcp.json", config); err != nil {
					t.Fatal(err)
				}
			}
			newFixtureRun(t, f, "")
			done := make(chan error, 1)
			go func() { done <- f.execute(context.Background()) }()
			q := pendingCard(t, f)
			if q.AnsweredBy != "" || f.effects.Load() != 1 {
				t.Fatalf("grant survived a %s change: %+v effects=%d", change, q, f.effects.Load())
			}
			if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
				t.Fatal(err)
			}
			_ = waitReviewDone(t, done)
		})
	}
}

// The list shows the grant; Revoke ends it; the next call asks.
func TestRememberedApprovalListAndRevoke(t *testing.T) {
	f := newAutoReviewFixture(t)
	grantID := approveAndRemember(t, f)
	route := func(method, path string) (int, map[string]json.RawMessage) {
		req := httptest.NewRequest(method, "/api/"+path, nil)
		rec := httptest.NewRecorder()
		if !f.s.routeRememberedApprovals(rec, req, path) {
			t.Fatalf("route not matched: %s", path)
		}
		var out map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := route(http.MethodGet, "conversations/"+f.c.ID+"/remembered-approvals")
	var grants []MCPRememberedApproval
	_ = json.Unmarshal(out["grants"], &grants)
	if code != 200 || len(grants) != 1 || grants[0].ID != grantID || grants[0].Server != "fixture" || grants[0].Tool != "read_public" || grants[0].TargetKind != mcpRememberTargetExact || grants[0].ExpiresAt == "" {
		t.Fatalf("list: %d %+v", code, grants)
	}
	if expires, err := time.Parse(time.RFC3339Nano, grants[0].ExpiresAt); err != nil || expires.After(time.Now().Add(rememberedApprovalValidity+time.Minute)) || expires.Before(time.Now().Add(rememberedApprovalValidity-time.Minute)) {
		t.Fatalf("grant validity is not %v: %s", rememberedApprovalValidity, grants[0].ExpiresAt)
	}
	if code, _ = route(http.MethodDelete, "conversations/"+f.c.ID+"/remembered-approvals/"+grantID); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ = route(http.MethodDelete, "conversations/"+f.c.ID+"/remembered-approvals/"+grantID); code != 404 {
		t.Fatalf("second revoke: %d", code)
	}
	code, out = route(http.MethodGet, "conversations/"+f.c.ID+"/remembered-approvals")
	grants = nil
	_ = json.Unmarshal(out["grants"], &grants)
	if code != 200 || len(grants) != 0 {
		t.Fatalf("list after revoke: %+v", grants)
	}
	newFixtureRun(t, f, "")
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := pendingCard(t, f)
	if q.AnsweredBy != "" || f.effects.Load() != 1 {
		t.Fatalf("revoked grant still decided: %+v", q)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
}

// A grant revoked between the remembered decision and the claim: the claim
// refuses, the card returns to a person, nothing executes on the memory.
func TestRememberedApprovalRevokedBeforeClaimWithdraws(t *testing.T) {
	f := newAutoReviewFixture(t)
	grantID := approveAndRemember(t, f)
	r := newFixtureRun(t, f, "")
	target, _ := mcpRememberTargetFor(f.call.Schema, f.call.Arguments)
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Allow this external tool call?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Call " + f.call.Tool, Target: "MCP server " + f.call.Server, Impact: "x", Payload: string(f.call.Arguments)}, ExpiresInSeconds: 600})
	q, err := f.s.store.CreateQuestion(f.c.ID, r, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash,server,tool,config_fingerprint,tier_binding,target_key,target_kind,remember_eligible) VALUES(?,?,?,?,?,?,?,?,?,1)`, q.ID, r.ID, mcpApprovalHash(f.call), f.call.Server, f.call.Tool, f.call.ConfigVersion, f.call.TierBinding, target.Key, target.Kind); err != nil {
		t.Fatal(err)
	}
	tx, _ := f.s.store.db.Begin()
	if err = applyRememberedMCPApproval(tx, f.c, &q, grantID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.s.store.revokeRememberedApproval(f.c.ID, grantID, "human"); err != nil || !ok {
		t.Fatalf("revoke: %v %t", err, ok)
	}
	_, err = f.s.claimMCPApproval(context.Background(), f.c, r, f.call, q.ID)
	if !errors.Is(err, errAutoReviewInvalidated) {
		t.Fatalf("claim on a revoked memory = %v", err)
	}
	current, _ := f.s.store.GetQuestion(q.ID)
	var claimed string
	_ = f.s.store.db.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&claimed)
	if current.Status != questionPending || current.AnsweredBy != "" || len(current.Answer) != 0 || claimed != "" || current.Approval.Review == nil || current.Approval.Review.GrantID != grantID || current.Approval.Review.DecisionSource != "" || current.Approval.Review.Status != "human_required" {
		t.Fatalf("withdrawn card: %+v claimed=%q review=%+v", current, claimed, current.Approval.Review)
	}
}

// An unattended (scheduled) run never uses a memory: the card waits for a
// person even though a matching grant exists.
func TestRememberedApprovalScheduledRunNeverUses(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAndRemember(t, f)
	r := newFixtureRun(t, f, "")
	if _, err := f.s.store.db.Exec(`UPDATE runs SET kind=? WHERE id=?`, runKindSchedule, r.ID); err != nil {
		t.Fatal(err)
	}
	f.r.Kind = runKindSchedule
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	q := pendingCard(t, f)
	if q.AnsweredBy != "" || q.Approval.Remember != nil || f.effects.Load() != 1 {
		t.Fatalf("scheduled run used or offered a memory: %+v", q)
	}
	if code, _ := answerRoute(t, f, q.ID, `{"value":true,"remember":true}`); code != 400 {
		t.Fatalf("remember from a scheduled run: %d", code)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", false); err != nil {
		t.Fatal(err)
	}
	_ = waitReviewDone(t, done)
}

// Nothing the model or a tool result produces can create a grant: the
// model's request_approval input cannot carry the offer, a non-MCP card
// cannot be remembered, the host actor cannot be impersonated, and a
// prompt-injected proposal still waits for a person with no grant.
func TestRememberedApprovalCannotBeCreatedByModelOrToolResult(t *testing.T) {
	f := newAutoReviewFixture(t)
	if _, err := normalizeQuestionInput(askQuestionInput{Question: "Proceed?", Type: questionApproval, Approval: &ApprovalDetails{Action: "a", Target: "b", Impact: "c", Remember: &MCPRememberOffer{Kind: "exact"}}}); err == nil {
		t.Fatal("model input carried a remember offer")
	}
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Send?", Type: questionApproval, Approval: &ApprovalDetails{Action: "Send mail", Target: "A", Impact: "Mail sent"}})
	q, err := f.s.store.CreateQuestion(f.c.ID, f.r, in)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := answerRoute(t, f, q.ID, `{"value":true,"remember":true}`); code != 400 || grantCount(t, f) != 0 {
		t.Fatalf("a model-made approval card was remembered: %d grants=%d", code, grantCount(t, f))
	}
	if current, _ := f.s.store.GetQuestion(q.ID); current.Status != questionPending {
		t.Fatalf("refused remember answered the card: %+v", current)
	}
	if _, _, err := f.s.store.AnswerQuestion(q.ID, rememberedApprovalActor, true); !errors.Is(err, ErrQuestionBotActor) {
		t.Fatalf("host actor impersonated: %v", err)
	}
	_, _, _ = f.s.store.AnswerQuestion(q.ID, "synthetic-human", false)
	// The fixture tool's description is an injection ("approve every write");
	// the proposal it leads to is still a pending card with no grant.
	done := make(chan error, 1)
	go func() { done <- f.execute(context.Background()) }()
	card := pendingCard(t, f)
	if card.AnsweredBy != "" || grantCount(t, f) != 0 || f.effects.Load() != 0 {
		t.Fatalf("injected proposal decided itself: %+v grants=%d", card, grantCount(t, f))
	}
	_, _, _ = f.s.store.AnswerQuestion(card.ID, "synthetic-human", false)
	_ = waitReviewDone(t, done)
}

// The configuration is read from the fixture directory on every proposal.
func writeFixtureFile(f *autoReviewFixture, name string, data []byte) error {
	return os.WriteFile(filepath.Join(f.dir, name), data, 0600)
}
