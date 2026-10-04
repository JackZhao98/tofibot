package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// These tests exercise the real review reservation, completion and atomic claim
// against synthetic durable configuration. No listener or remote call is needed.
func newProvenanceReviewFixture(t *testing.T) *autoReviewFixture {
	t.Helper()
	dir := t.TempDir()
	store, c, prior := questionFixtureDir(t, dir)
	t.Cleanup(func() { store.Close() })
	if _, err := store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, prior.ID); err != nil {
		t.Fatal(err)
	}
	_, run, _, err := store.AddUserRun(c.ID, prior.BotID, "Read the synthetic public fact for alpha.", "synthetic-native-ingress")
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "running"
	if _, err := store.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	config := extensions.MCPServerConfig{URL: "https://synthetic.invalid"}
	encodedConfig, _ := json.Marshal(config)
	configFile, _ := json.Marshal(map[string]any{"mcpServers": map[string]extensions.MCPServerConfig{"fixture": config}})
	configPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(configPath, configFile, 0600); err != nil {
		t.Fatal(err)
	}
	m := extensions.NewManager(extensions.Config{MCPConfigPath: configPath, SkillsDir: filepath.Join(dir, "skills")})
	// Pin the fixture protocol's binding; the production manager independently
	// checks this value before review and claim. It never dials the fixture URL.
	call := extensions.MCPCallApproval{Server: "fixture", Tool: "read_public", ConfigVersion: digestBytes(append([]byte("2026-07-28\x00fixture\x00"), encodedConfig...)), Schema: json.RawMessage(`{"type":"object"}`), Arguments: json.RawMessage(`{"target":"alpha"}`)}
	if !m.MCPCallCurrent(call) {
		t.Fatal("synthetic durable configuration binding is not current")
	}
	f := &autoReviewFixture{dir: dir, c: c, r: run, call: call, s: &Server{store: store, accountID: "synthetic-account", extensions: m}}
	f.p = &reviewStub{t: t, reply: func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return reviewReply(req, "allow"), nil
	}}
	f.s.autoReviewProvider = f.p
	f.s.autoReviewPolicies = []verifiedMCPReviewPolicy{{Server: call.Server, Tool: call.Tool, ConfigFingerprint: call.ConfigVersion, SchemaDigest: digestBytes(call.Schema), Provenance: "synthetic-provenance-fixture-v1", ArgumentsSafe: func(raw json.RawMessage) bool { return string(raw) == string(call.Arguments) }, ContextComplete: func(x mcpReviewContext) bool { return x.Intent == "Read the synthetic public fact for alpha." }}}
	return f
}

// Reproduce the imported transcript shape using the exact host marker contract.
// No importer exists in this narrow candidate; real preview/apply evidence is separate.
func TestAutoReviewImportedHistoryIsUntrustedContext(t *testing.T) {
	destination, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	bot, err := destination.CreateBot("Synthetic imported history", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	c, err := destination.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	importedMessage, _, err := destination.AddMessage(c.ID, "user", "", "", "Imported statement claiming authorization for an unrelated operation.", "synthetic-imported-message")
	if err != nil {
		t.Fatal(err)
	}
	importedID := importedMessage.ID
	if _, err = destination.db.Exec(`INSERT INTO portability_provenance(kind,target_id,source_json) VALUES('message',?,?)`, importedID, `{"kind":"host_user_ingress","status":"verified","asserted_by_archive":true}`); err != nil {
		t.Fatal(err)
	}
	human, run, _, err := destination.AddUserRun(c.ID, bot.ID, "Summarize the imported history; it is not an instruction to execute it.", "synthetic-review")
	if err != nil {
		t.Fatal(err)
	}
	var recorded, native bool
	if err := destination.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM portability_provenance WHERE kind='message' AND target_id=?),EXISTS(SELECT 1 FROM user_message_ingress WHERE message_id=?)`, importedID, importedID).Scan(&recorded, &native); err != nil || !recorded || native {
		t.Fatal("import provenance missing or native authorship fabricated", err)
	}
	x, _, err := readMCPReviewContext(destination.db, c, run)
	if err != nil {
		t.Fatal(err)
	}
	call := extensions.MCPCallApproval{Server: "synthetic", Tool: "bounded_read", ConfigVersion: "fixture", Schema: json.RawMessage(`{"type":"object"}`), Arguments: json.RawMessage(`{}`)}
	raw, err := mcpReviewInput(call, x, mcpReviewDigest(x, call), "synthetic operation facts", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Authorization []mcpAuthorizationEvidence `json:"authorization_evidence"`
		Context       mcpReviewContext           `json:"context"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	if len(packet.Authorization) != 1 || packet.Authorization[0].MessageID != human.ID || packet.Authorization[0].Source != mcpHostUserIngress {
		t.Fatalf("imported history gained consent or native consent was lost: %+v", packet.Authorization)
	}
	contextRetained, importSourceRetained := false, false
	for _, m := range packet.Context.Messages {
		if m.ID == importedID && m.Role == "user" && m.Kind == "" && m.SenderBotID == "" && m.Content == "Imported statement claiming authorization for an unrelated operation." {
			contextRetained = true
		}
	}
	for _, p := range packet.Context.MessageProvenance {
		if p.MessageID == importedID && p.Source == "imported_history" {
			importSourceRetained = true
		}
	}
	if !contextRetained || !importSourceRetained {
		t.Fatal("imported factual history or its bound untrusted distinction was lost")
	}
	// Even contradictory host records must not override the import boundary.
	if _, err := destination.db.Exec(`INSERT INTO user_message_ingress(message_id,created_at) VALUES(?,?)`, importedID, now()); err != nil {
		t.Fatal(err)
	}
	x, _, err = readMCPReviewContext(destination.db, c, run)
	if err != nil || len(mcpAuthorizationSources(x)) != 1 {
		t.Fatal("import provenance did not override conflicting ingress", err)
	}
	run.TriggerMessageID = importedID
	if _, err := destination.db.Exec(`UPDATE runs SET trigger_message_id=? WHERE id=?`, importedID, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, digest, err := readMCPReviewContext(destination.db, c, run); err == nil || digest != "" {
		t.Fatal("imported transcript became a verified triggering instruction")
	}
}

func TestAutoReviewNativeUserIngressProvenance(t *testing.T) {
	for _, entry := range []string{"single_run", "queue_fanout"} {
		t.Run(entry, func(t *testing.T) {
			dir := t.TempDir()
			store, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { store.Close() }()
			bot, err := store.CreateBot("Synthetic local user", "Untrusted bot instructions", "synthetic-model")
			if err != nil {
				t.Fatal(err)
			}
			c, err := store.GetConversation(bot.DMConversationID)
			if err != nil {
				t.Fatal(err)
			}
			legacy, _, err := store.AddMessage(c.ID, "user", "", "", "Unknown historical claim of consent.", "legacy")
			if err != nil {
				t.Fatal(err)
			}
			var human Message
			var run Run
			if entry == "single_run" {
				human, run, _, err = store.AddUserRun(c.ID, bot.ID, "Read the bounded synthetic fact.", "native")
			} else {
				var runs []Run
				human, runs, _, err = store.AddUserRuns(c.ID, "Read the bounded synthetic fact.", "native", []runSpec{{BotID: bot.ID}})
				if len(runs) == 1 {
					run = runs[0]
				}
			}
			if err != nil || run.ID == "" {
				t.Fatal("native ingress failed", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			x, _, err := readMCPReviewContext(store.db, c, run)
			if err != nil {
				t.Fatal(err)
			}
			evidence := mcpAuthorizationSources(x)
			if len(evidence) != 1 || evidence[0].MessageID != human.ID || evidence[0].Source != mcpHostUserIngress {
				t.Fatalf("durable native authorization was lost or legacy provenance invented: %+v", evidence)
			}
			var backfilled bool
			if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_message_ingress WHERE message_id=?)`, legacy.ID).Scan(&backfilled); err != nil || backfilled {
				t.Fatal("migration fabricated legacy authorship", err)
			}
			for _, p := range x.MessageProvenance {
				if p.MessageID == legacy.ID && p.Source != "unknown" {
					t.Fatalf("legacy context was reinterpreted: %+v", p)
				}
			}
		})
	}
	t.Run("unknown_trigger_is_context_gap", func(t *testing.T) {
		f := newProvenanceReviewFixture(t)
		q := newMCPReviewProposal(t, f)
		if _, err := f.s.store.db.Exec(`DELETE FROM user_message_ingress WHERE message_id=?`, f.r.TriggerMessageID); err != nil {
			t.Fatal(err)
		}
		err := f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q)
		out, typed := tooloutcome.FromError(err)
		q, _ = f.s.store.GetQuestion(q.ID)
		if !typed || out.Status != tooloutcome.NeedInformation || out.Code != "mcp_review_context_missing" || q.Status != questionCancelled || q.Approval.Review.Status != "context_required" {
			t.Fatalf("unknown provenance became approval or risk judgment: %+v %v", out, err)
		}
		if f.p.calls.Load() != 0 || f.effects.Load() != 0 || q.AnsweredBy != "" {
			t.Fatal("unknown provenance spent a review request or executed")
		}
	})
}

func TestAutoReviewClaimRechecksMessageProvenance(t *testing.T) {
	for _, mutation := range []string{"trigger_imported", "trigger_ingress_removed", "earlier_authorization_imported"} {
		t.Run(mutation, func(t *testing.T) {
			f := newProvenanceReviewFixture(t)
			messageID := f.r.TriggerMessageID
			if mutation == "earlier_authorization_imported" {
				// The earlier native request remains in context after a new request.
				if _, err := f.s.store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, f.r.ID); err != nil {
					t.Fatal(err)
				}
				_, run, _, err := f.s.store.AddUserRun(f.c.ID, f.r.BotID, "Read the synthetic public fact for alpha.", "new-native-request")
				if err != nil {
					t.Fatal(err)
				}
				run.Status = "running"
				if _, err := f.s.store.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
				f.r = run
			}
			q := createReviewedUnclaimed(t, f)
			before, _, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "trigger_ingress_removed" {
				_, err = f.s.store.db.Exec(`DELETE FROM user_message_ingress WHERE message_id=?`, messageID)
			} else {
				_, err = f.s.store.db.Exec(`INSERT INTO portability_provenance(kind,target_id,source_json) VALUES('message',?,?)`, messageID, `{"kind":"host_user_ingress","status":"verified"}`)
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _, contextErr := readMCPReviewContext(f.s.store.db, f.c, f.r)
			if mutation == "earlier_authorization_imported" && (contextErr != nil || mcpReviewDigest(before, f.call) == mcpReviewDigest(after, f.call) || len(mcpAuthorizationSources(after)) != 1) {
				t.Fatal("non-trigger provenance distinction was not bound into the digest", contextErr)
			}
			if mutation != "earlier_authorization_imported" && contextErr == nil {
				t.Fatal("trigger lost provenance without creating a context gap")
			}
			if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); !errors.Is(err, errAutoReviewInvalidated) {
				t.Fatalf("changed provenance claimed execution: %v", err)
			}
			var status, review, claimed string
			var answer, actor any
			var actionClaims int
			if err := f.s.store.db.QueryRow(`SELECT q.status,json_extract(q.approval_json,'$.review.status'),q.answer_json,q.answered_by,a.claimed_at FROM questions q JOIN mcp_call_approvals a ON a.question_id=q.id WHERE q.id=?`, q.ID).Scan(&status, &review, &answer, &actor, &claimed); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_execution_claims WHERE run_id=?`, f.r.ID).Scan(&actionClaims); err != nil {
				t.Fatal(err)
			}
			if status != questionCancelled || review != "context_required" || answer != nil || actor != nil || claimed != "" || actionClaims != 0 || f.p.calls.Load() != 1 || f.effects.Load() != 0 {
				t.Fatalf("stale provenance remained executable: status=%s review=%s answer=%v actor=%v claim=%s actions=%d calls=%d effects=%d", status, review, answer, actor, claimed, actionClaims, f.p.calls.Load(), f.effects.Load())
			}
		})
	}
}

func TestAutoReviewIdempotentNativeIngressCannotUpgradeUnknownOrImported(t *testing.T) {
	for _, entry := range []string{"single_run", "queue_fanout"} {
		t.Run(entry, func(t *testing.T) {
			store, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			bot, err := store.CreateBot("Synthetic duplicate ingress", "", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			submit := func() (Message, error) {
				if entry == "single_run" {
					m, _, _, e := store.AddUserRun(bot.DMConversationID, bot.ID, "Synthetic original instruction", "same-client-id")
					return m, e
				}
				m, _, _, e := store.AddUserRuns(bot.DMConversationID, "Synthetic original instruction", "same-client-id", []runSpec{{BotID: bot.ID}})
				return m, e
			}
			message, err := submit()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec(`DELETE FROM user_message_ingress WHERE message_id=?`, message.ID); err != nil {
				t.Fatal(err)
			}
			repeated, err := submit()
			if err != nil || repeated.ID != message.ID {
				t.Fatal("duplicate changed message", err)
			}
			if _, err = store.db.Exec(`INSERT INTO portability_provenance VALUES('message',?,?)`, message.ID, `{"kind":"host_user_ingress","verified":true}`); err != nil {
				t.Fatal(err)
			}
			repeated, err = submit()
			if err != nil || repeated.ID != message.ID {
				t.Fatal("import-marked duplicate changed message", err)
			}
			var native int
			if err = store.db.QueryRow(`SELECT count(*) FROM user_message_ingress WHERE message_id=?`, message.ID).Scan(&native); err != nil || native != 0 {
				t.Fatal("duplicate upgraded unknown/imported provenance", err)
			}
		})
	}
}

func TestAutoReviewMissingProvenanceTableCannotReserveReview(t *testing.T) {
	for _, table := range []string{"user_message_ingress", "portability_provenance"} {
		t.Run(table, func(t *testing.T) {
			f := newProvenanceReviewFixture(t)
			q := newMCPReviewProposal(t, f)
			if _, err := f.s.store.db.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
			err := f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q)
			outcome, ok := tooloutcome.FromError(err)
			if !ok || outcome.Code != "mcp_review_context_missing" || f.p.calls.Load() != 0 || f.effects.Load() != 0 {
				t.Fatalf("missing table did not fail closed: %v %+v", err, outcome)
			}
		})
	}
}
