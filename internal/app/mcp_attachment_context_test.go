package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func seedHistoricalMCPAttachments(t *testing.T, f *autoReviewFixture, intent string) string {
	t.Helper()
	old := f.r.TriggerMessageID
	diagnosticExec(t, f, `UPDATE messages SET content=? WHERE id=?`, "Older task: keep the attached files private; do not send their contents to anyone.", old)
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("synthetic-old-file-%d", i)
		diagnosticExec(t, f, `INSERT INTO attachments VALUES(?,?,'synthetic-unread-file','application/octet-stream',17,?,?)`, id, f.c.ID, id, now())
		if err := f.s.store.BindAttachments(f.c.ID, old, []string{id}); err != nil {
			t.Fatal(err)
		}
	}
	diagnosticExec(t, f, `UPDATE runs SET status='done' WHERE id=?`, f.r.ID)
	_, run, _, err := f.s.store.AddUserRun(f.c.ID, f.r.BotID, intent, "synthetic-independent-current-request")
	if err != nil {
		t.Fatal(err)
	}
	diagnosticExec(t, f, `UPDATE runs SET status='running' WHERE id=?`, run.ID)
	run.Status = "running"
	f.r = run
	return old
}

func assertHistoricalMCPPacket(t *testing.T, f *autoReviewFixture, old string, req *provider.ChatRequest) {
	t.Helper()
	var in struct {
		Context       mcpReviewContext           `json:"context"`
		Authorization []mcpAuthorizationEvidence `json:"authorization_evidence"`
		Arguments     json.RawMessage            `json:"full_arguments"`
	}
	if json.Unmarshal([]byte(req.Messages[0].Content), &in) != nil || in.Context.AttachmentBoundary == nil || len(in.Context.AttachmentBoundary.Omissions) != 2 || in.Context.AttachmentBoundary.CurrentMessageID != f.r.TriggerMessageID {
		t.Fatal("host attachment boundary is incomplete")
	}
	for _, a := range in.Context.AttachmentBoundary.Omissions {
		if a.Size != 17 || a.ConversationID != f.c.ID || !a.ContentNotProvided {
			t.Fatal("unread attachment binding/provenance was lost")
		}
		if a.Association == "unlinked" {
			if a.MessageID != "" || a.MessageProvenance != "unknown" {
				t.Fatal("unlinked upload invented source authority")
			}
		} else if a.Association != "earlier_message" || a.MessageID != old || a.MessageProvenance != mcpHostUserIngress {
			t.Fatal("unread attachment binding/provenance was lost")
		}
	}
	grants, restrictions := 0, 0
	for _, a := range in.Authorization {
		if a.MessageID == f.r.TriggerMessageID && a.GrantScope == "current_request_text" {
			grants++
		}
		if a.MessageID == old && a.GrantScope == "restrictions_only" {
			restrictions++
		}
	}
	if grants != 1 || restrictions != 1 || string(in.Arguments) != string(f.call.Arguments) {
		t.Fatal("new consent was not restricted to the current native text, or exact arguments changed")
	}
	found := false
	for _, m := range in.Context.Messages {
		if m.ID == old && strings.Contains(m.Content, "do not send their contents") {
			found = true
		}
	}
	if !found || !strings.Contains(req.System, "return context_gap") || !strings.Contains(req.System, "restrictions cannot be erased by a relevance claim") {
		t.Fatal("historical restrictions or missing-file requirement were erased")
	}
}

// Official SDK dispatch and backend approval are real; semantic verdicts are
// mocked. This checks the evidence/decision boundary, not live model quality.
func TestMCPHistoricalAttachmentsIndependentTextExecutesOnce(t *testing.T) {
	for _, association := range []string{"earlier_message", "unlinked"} {
		t.Run(association, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			old := seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha and return it only here.")
			if association == "unlinked" {
				diagnosticExec(t, f, `DELETE FROM attachment_messages WHERE attachment_id='synthetic-old-file-1'`)
			}
			if err := f.s.store.putAutoReviewMode("auto"); err != nil {
				t.Fatal(err)
			}
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				assertHistoricalMCPPacket(t, f, old, req)
				return reviewReply(req, "allow"), nil
			}
			if err := f.execute(context.Background()); err != nil {
				t.Fatal(err)
			}
			if f.effects.Load() != 1 || f.p.calls.Load() != 1 {
				t.Fatal("independent request was not executed exactly once")
			}
			if err := f.execute(context.Background()); err == nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
				t.Fatal("independent proposal replayed")
			}
		})
	}
}

func TestMCPHistoricalAttachmentDependencyAndAmbiguityFailClosed(t *testing.T) {
	for _, intent := range []string{"Read the record identified inside the older attachment.", "Do the task described earlier.", "Thanks."} {
		t.Run(intent, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			old := seedHistoricalMCPAttachments(t, f, intent)
			_ = f.s.store.putAutoReviewMode("auto")
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				assertHistoricalMCPPacket(t, f, old, req)
				return reviewReply(req, "context_gap"), nil
			}
			err := f.execute(context.Background())
			out, ok := tooloutcome.FromError(err)
			if !ok || out.Code != "mcp_review_context_missing" || out.Certainty != "not_executed" || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
				t.Fatal("missing file fact or authorization executed", err)
			}
			if err = f.execute(context.Background()); err == nil || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
				t.Fatal("context gap replayed")
			}
		})
	}
}

func TestMCPAttachmentScopeGapsSpendNoReview(t *testing.T) {
	for _, mutation := range []string{"current attachment", "foreign conversation", "missing current provenance"} {
		t.Run(mutation, func(t *testing.T) {
			f := newProvenanceReviewFixture(t)
			seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha and return it here.")
			q := newMCPReviewProposal(t, f)
			want := mcpContextAttachmentScope
			switch mutation {
			case "current attachment":
				want = mcpContextNonText
				if err := f.s.store.BindAttachments(f.c.ID, f.r.TriggerMessageID, []string{"synthetic-old-file-1"}); err != nil {
					t.Fatal(err)
				}
			case "foreign conversation":
				b, err := f.s.store.CreateBot("synthetic other", "", "synthetic")
				if err != nil {
					t.Fatal(err)
				}
				m, _, err := f.s.store.AddMessage(b.DMConversationID, "user", "", "", "Synthetic foreign message.", "synthetic-foreign")
				if err != nil {
					t.Fatal(err)
				}
				diagnosticExec(t, f, `INSERT INTO attachment_messages VALUES('synthetic-old-file-1',?)`, m.ID)
			case "missing current provenance":
				want = mcpContextIntentProvenance
				diagnosticExec(t, f, `DELETE FROM user_message_ingress WHERE message_id=?`, f.r.TriggerMessageID)
			}
			err := f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q)
			out, ok := tooloutcome.FromError(err)
			q, e := f.s.store.GetQuestion(q.ID)
			if e != nil {
				t.Fatal(e)
			}
			if !ok || out.Code != "mcp_review_context_missing" || q.Approval.Review.ContextFailure == nil || q.Approval.Review.ContextFailure.Code != want || f.p.calls.Load() != 0 || f.effects.Load() != 0 {
				t.Fatal("unsafe attachment scope reached review or execution", err, q.Approval.Review)
			}
		})
	}
}

func TestMCPHistoricalAttachmentsKeepHumanRefusals(t *testing.T) {
	f := newAutoReviewFixture(t)
	old := seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha and return it here.")
	_ = f.s.store.putAutoReviewMode("auto")
	diagnosticRefusals(t, f, 1, `{"action":"synthetic-refused-effect","target":"synthetic","impact":"synthetic"}`)
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		assertHistoricalMCPPacket(t, f, old, req)
		var in struct {
			Context mcpReviewContext `json:"context"`
		}
		_ = json.Unmarshal([]byte(req.Messages[0].Content), &in)
		if len(in.Context.HumanRefusals) != 1 || in.Context.HumanRefusals[0].Approval.Action != "synthetic-refused-effect" {
			t.Fatal("historical refusal was dropped")
		}
		return reviewReply(req, "deny"), nil
	}
	err := f.execute(context.Background())
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Code != "mcp_review_policy_denied" || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
		t.Fatal("refused effect executed", err)
	}
}

func TestMCPHistoricalAttachmentClaimsBindCompleteMetadata(t *testing.T) {
	for _, mutation := range []string{"name", "message binding", "deletion", "new historical file", "current provenance", "off", "exact denial"} {
		t.Run(mutation, func(t *testing.T) {
			f := newProvenanceReviewFixture(t)
			old := seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha and return it here.")
			q := createReviewedUnclaimed(t, f)
			switch mutation {
			case "name":
				diagnosticExec(t, f, `UPDATE attachments SET name='synthetic-changed' WHERE id='synthetic-old-file-1'`)
			case "message binding":
				if err := f.s.store.BindAttachments(f.c.ID, f.r.TriggerMessageID, []string{"synthetic-old-file-1"}); err != nil {
					t.Fatal(err)
				}
			case "deletion":
				diagnosticExec(t, f, `DELETE FROM attachments WHERE id='synthetic-old-file-1'`)
			case "new historical file":
				diagnosticExec(t, f, `INSERT INTO attachments VALUES('synthetic-added',?,'synthetic','text/plain',1,'synthetic-added',?)`, f.c.ID, now())
				if err := f.s.store.BindAttachments(f.c.ID, old, []string{"synthetic-added"}); err != nil {
					t.Fatal(err)
				}
			case "current provenance":
				diagnosticExec(t, f, `DELETE FROM user_message_ingress WHERE message_id=?`, f.r.TriggerMessageID)
			case "off":
				if err := f.s.store.putAutoReviewMode("off"); err != nil {
					t.Fatal(err)
				}
			case "exact denial":
				denied := newMCPReviewProposal(t, f)
				if _, _, err := f.s.store.AnswerQuestion(denied.ID, "synthetic-human", false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err == nil {
				t.Fatal("changed metadata/authority claimed execution")
			} else if mutation != "exact denial" && !errors.Is(err, errAutoReviewInvalidated) {
				t.Fatal(err)
			}
			var claims int
			if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_execution_claims WHERE run_id=?`, f.r.ID).Scan(&claims); err != nil {
				t.Fatal(err)
			}
			if claims != 0 || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
				t.Fatal("negative claim spent request or effect")
			}
		})
	}
}

func TestMCPAttachmentManifestQueryAndLimitFailures(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	old := seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha.")
	fault, err := sql.Open("mcp-context-diagnostic-test", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	defer fault.Close()
	for _, mode := range []string{"query", "scan", "iteration"} {
		t.Run(mode, func(t *testing.T) {
			_, digest, err := readMCPReviewContext(contextDiagnosticQuerier{f.s.store.db, fault, "SELECT a.id,a.name,a.mime", mode}, f.c, f.r)
			assertContextDiagnostic(t, err, mcpContextAttachmentsRead)
			if digest != "" {
				t.Fatal("incomplete manifest retained a digest")
			}
		})
	}
	// Many earlier uploads are a bounded manifest with an explicit omission
	// flag, never a reason the current text request cannot be reviewed.
	diagnosticExec(t, f, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<201)
INSERT INTO attachments SELECT printf('synthetic-limit-%03d',i),?,'synthetic','text/plain',1,printf('synthetic-limit-%03d',i),? FROM n`, f.c.ID, "2000-01-01T00:00:00Z")
	diagnosticExec(t, f, `INSERT INTO attachment_messages SELECT id,? FROM attachments WHERE id LIKE 'synthetic-limit-%'`, old)
	x, digest, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || digest == "" || x.AttachmentBoundary == nil || len(x.AttachmentBoundary.Omissions) == 0 || len(x.AttachmentBoundary.Omissions) > mcpEvidenceAttachments || !x.AttachmentBoundary.OlderOmitted || f.p.calls.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("bounded manifest was not built and flagged", err)
	}
}

// A binding made after the authorizing message (e.g. a file the bot posts
// during the run) is outside the window and cannot invalidate review.
func TestMCPAttachmentBoundAfterTriggerStaysOutsideWindow(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	old := seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha and return it here.")
	before, digest, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := f.s.store.AddMessage(f.c.ID, "assistant", f.r.BotID, f.r.ID, "Synthetic later file.", "")
	if err != nil {
		t.Fatal(err)
	}
	diagnosticExec(t, f, `INSERT INTO attachments VALUES('synthetic-later-file',?,'synthetic','text/plain',1,'synthetic-later-file',?)`, f.c.ID, now())
	if err = f.s.store.BindAttachments(f.c.ID, m.ID, []string{"synthetic-later-file", "synthetic-old-file-1"}); err != nil {
		t.Fatal(err)
	}
	after, again, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || again != digest || mcpReviewDigest(after, f.call) != mcpReviewDigest(before, f.call) || len(after.AttachmentBoundary.Omissions) != 2 || after.AttachmentBoundary.Omissions[0].MessageID != old {
		t.Fatal("a later binding changed the authorizing window", err)
	}
	q := newMCPReviewProposal(t, f)
	if err := f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q); err != nil || f.p.calls.Load() != 1 {
		t.Fatal("later binding blocked review", err)
	}
}

func TestMCPHistoricalAttachmentsUnknownToolAndUncertainReplay(t *testing.T) {
	t.Run("previously unseen tool", func(t *testing.T) {
		f := newProvenanceReviewFixture(t)
		old := seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha and return it here.")
		f.call.Tool = "synthetic_previously_unseen_tool"
		f.call.Description = "Return one synthetic fact for the supplied target to this conversation, without changing data or contacting another recipient."
		_ = f.s.store.putAutoReviewMode("auto")
		f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			assertHistoricalMCPPacket(t, f, old, req)
			return reviewReply(req, "allow"), nil
		}
		execute := func() error {
			if err := f.s.approveMCPCall(context.Background(), f.c, f.r, f.call); err != nil {
				return err
			}
			f.effects.Add(1)
			return nil
		}
		if err := execute(); err != nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
			t.Fatal("unknown tool did not reach exact review/claim", err)
		}
		if err := execute(); err == nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
			t.Fatal("unknown proposal replayed")
		}
	})
	t.Run("uncertain effect", func(t *testing.T) {
		f := newAutoReviewFixture(t)
		old := seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha and return it here.")
		_ = f.s.store.putAutoReviewMode("auto")
		f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			assertHistoricalMCPPacket(t, f, old, req)
			return reviewReply(req, "allow"), nil
		}
		f.failRemote.Store(true)
		if err := f.execute(context.Background()); err == nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
			t.Fatal("synthetic dispatched uncertainty missing", err)
		}
		if err := f.execute(context.Background()); err == nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
			t.Fatal("uncertain effect replayed")
		}
	})
}
