package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func v5ReviewReply(req *provider.ChatRequest, decision, risk string, confirmation bool) *provider.ChatResponse {
	var packet struct {
		Digest string `json:"context_digest"`
	}
	_ = json.Unmarshal([]byte(req.Messages[0].Content), &packet)
	raw, _ := json.Marshal(map[string]any{"decision": decision, "risk_level": risk, "confirmation_required": confirmation, "reason": "Synthetic semantic risk assessment.", "context_digest": packet.Digest})
	return &provider.ChatResponse{Content: string(raw)}
}

func TestAutoReviewV5UnlistedReadAndAuthorizedMediumWriteExecuteOnce(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(map[bool]string{false: "unlisted read", true: "authorized medium write"}[write], func(t *testing.T) {
			f := newAutoReviewFixture(t)
			setSyntheticMCPHumanPolicy(t, f, true)
			_ = f.s.store.putAutoReviewMode("auto")
			risk := "low"
			if write {
				_, _ = f.s.store.SetRunStatus(f.r.ID, "done", "")
				_, r, _, err := f.s.store.AddUserRun(f.c.ID, f.r.BotID, "Create one synthetic draft named alpha in this conversation. Do not publish or send it.", "synthetic-v5-write")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.s.store.SetRunStatus(r.ID, "running", "")
				f.r, _ = f.s.store.GetRun(r.ID)
				f.call.Tool = "create_draft"
				f.call.Description = "Create one private draft without publishing or sending."
				f.call.Arguments = json.RawMessage(`{"title":"alpha"}`)
				f.execute = func(ctx context.Context) error {
					if err := f.s.approveMCPCall(ctx, f.c, f.r, f.call); err != nil {
						return err
					}
					f.effects.Add(1)
					return nil
				}
				risk = "medium"
			}
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				if strings.Contains(req.Messages[0].Content, "host_human_confirmation_required") || strings.Contains(req.Messages[0].Content, "human_only_effects") {
					t.Error("static generic manual gate remains")
				}
				return v5ReviewReply(req, "allow", risk, false), nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := f.execute(ctx); err != nil {
				t.Fatal(err)
			}
			q := waitReviewQuestion(t, f, "approved")
			if q.Status == questionPending || q.AnsweredBy != autoReviewActor || q.Approval.Review.RiskLevel != risk || q.Approval.Review.PolicyVersion != autoReviewPolicyVersion {
				t.Fatal("automatic proposal demanded a human approval card", q)
			}
			events, err := f.s.store.Events(f.c.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			foundDecision := false
			for _, event := range events {
				if event["type"] != "question" {
					continue
				}
				var card QuestionCard
				raw, _ := json.Marshal(event["data"])
				if err := json.Unmarshal(raw, &card); err != nil {
					t.Fatal(err)
				}
				if card.QuestionID != q.ID {
					continue
				}
				if card.Approval.Review == nil || card.Approval.Review.PolicyVersion != autoReviewPolicyVersion {
					t.Fatal("bare human approval event preceded v5 review")
				}
				if card.Approval.Review.Status == "approved" {
					foundDecision = card.Approval.Review.RiskLevel == risk && !card.Approval.Review.ConfirmationRequired && card.AnsweredBy == autoReviewActor && string(card.Answer) == "true"
				}
			}
			if !foundDecision {
				t.Fatal("durable reconnect event lost v5 decision fields")
			}
			if err := f.execute(ctx); err == nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
				t.Fatal("proposal/reviewer replayed", err)
			}
		})
	}
}

func TestAutoReviewV5RiskConfirmationAndHumanRace(t *testing.T) {
	for _, tc := range []struct {
		name, decision, risk             string
		confirmation, humanAnswer, allow bool
	}{
		{"high requires human", "allow", "high", false, false, false},
		{"explicit confirmation", "allow", "medium", true, false, false},
		{"needs human", "needs_human", "low", true, false, false},
		{"valid high confirmation once", "allow", "high", false, true, true},
		{"human denial wins low allowance", "allow", "low", false, false, false},
		{"human approval cannot override deny", "deny", "low", false, true, false},
		{"human approval cannot repair unknown", "context_gap", "unknown", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			setSyntheticMCPHumanPolicy(t, f, true)
			_ = f.s.store.putAutoReviewMode("auto")
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				q := waitReviewQuestion(t, f, "reviewing")
				if f.effects.Load() != 0 {
					t.Error("effect before risk decision")
				}
				if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", tc.humanAnswer); err != nil {
					t.Error(err)
				}
				return v5ReviewReply(req, tc.decision, tc.risk, tc.confirmation), nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := f.execute(ctx)
			if (err == nil) != tc.allow {
				t.Fatal("risk/human race", err)
			}
			want := int32(0)
			if tc.allow {
				want = 1
			}
			if f.effects.Load() != want || f.p.calls.Load() != 1 {
				t.Fatal("unexpected effects or request count")
			}
			q := waitReviewQuestion(t, f, "")
			if q.AnsweredBy != "synthetic-human" {
				t.Fatal("model impersonated or erased human")
			}
			if err := f.execute(ctx); err == nil || f.effects.Load() != want || f.p.calls.Load() != 1 {
				t.Fatal("human decision replayed", err)
			}
		})
	}
	// High/confirmation must remain pending until a real human decides.
	for _, confirmation := range []bool{false, true} {
		t.Run(map[bool]string{false: "high pending", true: "confirmation pending"}[confirmation], func(t *testing.T) {
			f := newAutoReviewFixture(t)
			_ = f.s.store.putAutoReviewMode("auto")
			risk := "high"
			if confirmation {
				risk = "low"
			}
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				return v5ReviewReply(req, "allow", risk, confirmation), nil
			}
			done := make(chan error, 1)
			go func() { done <- f.execute(context.Background()) }()
			q := waitReviewQuestion(t, f, "human_required")
			if q.Status != questionPending || q.AnsweredBy != "" || f.effects.Load() != 0 {
				t.Fatal("risk/confirmation auto-executed", q)
			}
			_, _, _ = f.s.store.AnswerQuestion(q.ID, "synthetic-human", false)
			if err := waitReviewDone(t, done); err == nil || f.effects.Load() != 0 {
				t.Fatal("confirmation bypassed", err)
			}
		})
	}
}

func TestAutoReviewV5MalformedAndOldContractsFailClosed(t *testing.T) {
	for _, field := range []string{"old contract", "missing risk", "missing confirmation", "null confirmation", "string confirmation", "unknown risk allowance", "contradictory gap", "contradictory human", "tool attempt"} {
		t.Run(field, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			_ = f.s.store.putAutoReviewMode("auto")
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				response := v5ReviewReply(req, "allow", "low", false)
				var data map[string]any
				_ = json.Unmarshal([]byte(response.Content), &data)
				switch field {
				case "old contract":
					delete(data, "risk_level")
					delete(data, "confirmation_required")
				case "missing risk":
					delete(data, "risk_level")
				case "missing confirmation":
					delete(data, "confirmation_required")
				case "null confirmation":
					data["confirmation_required"] = nil
				case "string confirmation":
					data["confirmation_required"] = "false"
				case "unknown risk allowance":
					data["risk_level"] = "unknown"
				case "contradictory gap":
					data["decision"] = "context_gap"
				case "contradictory human":
					data["decision"] = "needs_human"
				case "tool attempt":
					response.ToolCalls = []provider.ToolCall{{Name: "synthetic-forbidden-tool"}}
				}
				raw, _ := json.Marshal(data)
				response.Content = string(raw)
				return response, nil
			}
			// A malformed answer earns exactly one fresh request; the same
			// malformed answer twice closes the proposal as malformed_response.
			if err := f.execute(context.Background()); err == nil || f.effects.Load() != 0 || f.p.calls.Load() != 2 {
				t.Fatal("invalid response executed", err)
			}
			var category, detail string
			if err := f.s.store.db.QueryRow(`SELECT failure_category,failure_detail FROM mcp_auto_reviews`).Scan(&category, &detail); err != nil || category != mcpReviewFailureMalformed || detail == "" {
				t.Fatalf("failure category missing: %q %q %v", category, detail, err)
			}
		})
	}
}

func TestAutoReviewV5AtomicClaimAndRestartRecheckRiskBindings(t *testing.T) {
	for _, mutation := range []string{"high", "confirmation", "deny", "old policy", "missing risk", "missing confirmation", "config", "expiry", "restart old policy"} {
		t.Run(mutation, func(t *testing.T) {
			f := newProvenanceReviewFixture(t)
			setSyntheticMCPHumanPolicy(t, f, true)
			q := createReviewedUnclaimed(t, f)
			switch mutation {
			case "high":
				_, _ = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET risk_level='high' WHERE question_id=?`, q.ID)
			case "confirmation":
				_, _ = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET confirmation_required=1 WHERE question_id=?`, q.ID)
			case "deny":
				_, _ = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET decision='deny' WHERE question_id=?`, q.ID)
			case "old policy", "restart old policy":
				_, _ = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET policy_version='mcp-all-external-v4' WHERE question_id=?`, q.ID)
			case "missing risk":
				_, _ = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET risk_level='' WHERE question_id=?`, q.ID)
			case "missing confirmation":
				_, _ = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET confirmation_required=-1 WHERE question_id=?`, q.ID)
			case "config":
				f.call.ConfigVersion = "changed"
			case "expiry":
				_, _ = f.s.store.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).Format(time.RFC3339Nano), q.ID)
			}
			if mutation == "restart old policy" {
				if err := f.s.store.Close(); err != nil {
					t.Fatal(err)
				}
				store, err := OpenStore(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				f.s.store = store
				t.Cleanup(func() { store.Close() })
			}
			result, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID)
			if err == nil {
				if n, _ := result.RowsAffected(); n != 0 {
					t.Fatal("invalid risk/binding claimed")
				}
			}
			if f.effects.Load() != 0 || f.p.calls.Load() != 1 {
				t.Fatal("invalidated permission executed/re-reviewed")
			}
		})
	}
}

// Exact P1 lifecycle: historical truncation/uncertainty must not poison an
// independent verification proposal, while the original uncertain effect stays claimed.
func TestAutoReviewV5HistoricalGapsPermitIndependentVerificationWithoutReplay(t *testing.T) {
	f := newAutoReviewFixture(t)
	setSyntheticMCPHumanPolicy(t, f, true)
	_ = f.s.store.putAutoReviewMode("auto")
	f.failTransient.Store(true)
	err := f.execute(context.Background())
	outcome, ok := tooloutcome.FromError(err)
	if !ok || outcome.Status != tooloutcome.Uncertain || f.effects.Load() != 1 {
		t.Fatal("original uncertainty fixture", err)
	}
	record := func(id, name, args, result, status string, truncated bool, outcome *tooloutcome.Outcome) {
		t.Helper()
		if err := f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, runtime.ToolEvent{CallID: id, Name: name, Arguments: args, Result: result, Status: status, Truncated: truncated, Outcome: outcome}); err != nil {
			t.Fatal(err)
		}
	}
	record("uncertain-original", "call_mcp_tool", `{"name":"mcp_fixture__read_public","arguments":{"target":"alpha"}}`, "", "queued", false, nil)
	record("uncertain-original", "call_mcp_tool", `{"name":"mcp_fixture__read_public","arguments":{"target":"alpha"}}`, outcome.Message, "failed", false, &outcome)
	record("unrelated-history", "synthetic_search", `{}`, "", "queued", false, nil)
	record("unrelated-history", "synthetic_search", `{}`, "Synthetic partial unrelated history.", "completed", true, nil)
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		var packet struct {
			Context mcpReviewContext `json:"context"`
		}
		_ = json.Unmarshal([]byte(req.Messages[0].Content), &packet)
		truncated, uncertain := false, false
		for _, a := range packet.Context.ToolResults {
			truncated = truncated || a.Truncated
			uncertain = uncertain || a.Outcome != nil && a.Outcome.Certainty == "unknown"
		}
		if !truncated || !uncertain {
			t.Error("history flags were hidden or promoted to complete facts")
		}
		return v5ReviewReply(req, "allow", "low", false), nil
	}
	f.failTransient.Store(false)
	verify := func() error {
		prepared, err := f.s.extensions.PrepareDiscoverableForBotWithCallGate(context.Background(), f.r.BotID, nil, func(ctx context.Context, call extensions.MCPCallApproval) error {
			return f.s.approveMCPCall(ctx, f.c, f.r, call)
		})
		if err != nil {
			return err
		}
		defer prepared.Close()
		var search, invoke runtimeTool
		for _, tool := range prepared.Tools {
			switch tool.Name {
			case "search_mcp_tools":
				search = tool.Execute
			case "call_mcp_tool":
				invoke = tool.Execute
			}
		}
		if _, err := search(context.Background(), json.RawMessage(`{"server":"fixture","query":"read_public"}`)); err != nil {
			return err
		}
		_, err = invoke(context.Background(), json.RawMessage(`{"name":"mcp_fixture__read_public","arguments":{"target":"alpha","verification":true}}`))
		return err
	}
	if err := verify(); err != nil {
		t.Fatal("independent verification blocked by unrelated history", err)
	}
	if f.p.calls.Load() != 2 || f.effects.Load() != 2 {
		t.Fatal("verification did not get one independent review or replayed original")
	}
	if err := verify(); err == nil || f.effects.Load() != 2 || f.p.calls.Load() != 2 {
		t.Fatal("verification replayed", err)
	}
	if err := f.execute(context.Background()); err == nil || f.effects.Load() != 2 || f.p.calls.Load() != 2 {
		t.Fatal("original uncertain effect replayed", err)
	}
	// A proposal related to the uncertain effect still needs its missing facts.
	// Permitting independent verification must not turn history into certainty.
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		if !strings.Contains(req.Messages[0].Content, `"execution_certainty":"unknown"`) {
			t.Error("related uncertainty was omitted")
		}
		return v5ReviewReply(req, "context_gap", "unknown", false), nil
	}
	related := f.call
	related.Tool = "complete_uncertain_effect"
	if err := f.s.approveMCPCall(context.Background(), f.c, f.r, related); err == nil || f.effects.Load() != 2 || f.p.calls.Load() != 3 {
		t.Fatal("related context gap executed", err)
	}
}

// Exact P2 lifecycle: Shadow observes the original running snapshot; the tool
// completes before the reviewer returns. The advice stays attached to that snapshot.
func TestAutoReviewV5ShadowCompletionUsesImmutableProposalSnapshot(t *testing.T) {
	f := newAutoReviewFixture(t)
	_ = f.s.store.putAutoReviewMode("shadow")
	for _, status := range []string{"queued", "running"} {
		if err := f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, runtime.ToolEvent{CallID: "shadow-progress", Name: "call_mcp_tool", Arguments: `{"name":"mcp_fixture__read_public","arguments":{"target":"alpha"}}`, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	snapshotDigest := ""
	f.p.reply = func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		var packet struct {
			Context mcpReviewContext `json:"context"`
			Digest  string           `json:"context_digest"`
		}
		_ = json.Unmarshal([]byte(req.Messages[0].Content), &packet)
		if len(packet.Context.ToolResults) != 1 || packet.Context.ToolResults[0].Status != "running" {
			t.Error("proposal did not snapshot running activity")
		}
		snapshotDigest = packet.Digest
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return v5ReviewReply(req, "allow", "low", false), nil
	}
	if err := f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, runtime.ToolEvent{CallID: "shadow-progress", Name: "call_mcp_tool", Arguments: `{"name":"mcp_fixture__read_public","arguments":{"target":"alpha"}}`, Result: "Synthetic complete result.", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	current, _, err := f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil {
		t.Fatal(err)
	}
	if mcpReviewDigest(current, f.call) == snapshotDigest {
		t.Fatal("fixture did not change execution progress")
	}
	close(release)
	f.s.shadowReviewWG.Wait()
	q := waitReviewQuestion(t, f, "shadow_allow")
	var stored string
	_ = f.s.store.db.QueryRow(`SELECT context_digest FROM mcp_auto_reviews WHERE question_id=?`, q.ID).Scan(&stored)
	if stored != snapshotDigest || q.Status != questionRunDone || q.AnsweredBy != "" || len(q.Answer) != 0 || !q.Approval.ReviewOnly || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
		t.Fatal("progress invalidated advice or advice granted authority", q)
	}
	if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err == nil {
		t.Fatal("Shadow advice claimed execution")
	}
}

func TestAutoReviewV5AlternativeReceivesOriginalHumanRestriction(t *testing.T) {
	f := newAutoReviewFixture(t)
	setSyntheticMCPHumanPolicy(t, f, true)
	_ = f.s.store.putAutoReviewMode("auto")
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		q := waitReviewQuestion(t, f, "reviewing")
		_, _, _ = f.s.store.AnswerQuestion(q.ID, "synthetic-human", false)
		return v5ReviewReply(req, "allow", "low", false), nil
	}
	if err := f.execute(context.Background()); err == nil {
		t.Fatal("original refused method executed")
	}
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		var packet struct {
			Context mcpReviewContext `json:"context"`
		}
		_ = json.Unmarshal([]byte(req.Messages[0].Content), &packet)
		if len(packet.Context.HumanRefusals) != 1 || packet.Context.HumanRefusals[0].Approval.Payload != string(f.call.Arguments) || packet.Context.HumanRefusals[0].Approval.Review != nil {
			t.Error("original refusal was lost or model advice gained human provenance")
		}
		return v5ReviewReply(req, "deny", "low", false), nil
	}
	alternative := f.call
	alternative.Tool = "independent_alternative"
	if err := f.s.approveMCPCall(context.Background(), f.c, f.r, alternative); err == nil || f.effects.Load() != 0 || f.p.calls.Load() != 2 {
		t.Fatal("alternative bypassed the original restriction", err)
	}
	if err := f.execute(context.Background()); err == nil || f.effects.Load() != 0 || f.p.calls.Load() != 2 {
		t.Fatal("original refusal replayed", err)
	}
}
