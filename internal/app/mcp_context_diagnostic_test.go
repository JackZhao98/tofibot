package app

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const diagnosticPrivateSentinel = "PRIVATE_SYNTHETIC_CONTENT_DO_NOT_EXPOSE"

// Inject database failures without a listener, production DB, content import,
// or a new production hook. Row iteration errors are distinct from Scan errors.
type contextDiagnosticDriver struct{}
type contextDiagnosticConn struct{}
type contextDiagnosticRows struct{ mode string }

func init()                                                      { sql.Register("mcp-context-diagnostic-test", contextDiagnosticDriver{}) }
func (contextDiagnosticDriver) Open(string) (driver.Conn, error) { return contextDiagnosticConn{}, nil }
func (contextDiagnosticConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New(diagnosticPrivateSentinel)
}
func (contextDiagnosticConn) Close() error { return nil }
func (contextDiagnosticConn) Begin() (driver.Tx, error) {
	return nil, errors.New(diagnosticPrivateSentinel)
}
func (contextDiagnosticConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if query == "query" {
		return nil, errors.New(diagnosticPrivateSentinel)
	}
	return &contextDiagnosticRows{mode: query}, nil
}
func (*contextDiagnosticRows) Columns() []string { return []string{"synthetic"} }
func (*contextDiagnosticRows) Close() error      { return nil }
func (r *contextDiagnosticRows) Next(values []driver.Value) error {
	if r.mode == "iteration" {
		return errors.New(diagnosticPrivateSentinel)
	}
	values[0] = diagnosticPrivateSentinel // One column cannot Scan the reader's tuple.
	return nil
}

type contextDiagnosticQuerier struct {
	reviewQuerier
	fault       *sql.DB
	match, mode string
}

func (q contextDiagnosticQuerier) QueryRow(query string, args ...any) *sql.Row {
	if strings.Contains(query, q.match) {
		return q.fault.QueryRow("query")
	}
	return q.reviewQuerier.QueryRow(query, args...)
}
func (q contextDiagnosticQuerier) Query(query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, q.match) {
		return q.fault.Query(q.mode)
	}
	return q.reviewQuerier.Query(query, args...)
}

func assertContextDiagnostic(t *testing.T, err error, code mcpContextFailureCode) *MCPContextFailure {
	t.Helper()
	d := mcpContextDiagnostic(err)
	if err == nil || d == nil || d.Code != code {
		t.Fatalf("diagnostic: got %#v, want %s", d, code)
	}
	raw, e := json.Marshal(d)
	if e != nil || strings.Contains(string(raw), diagnosticPrivateSentinel) || strings.Contains(err.Error(), diagnosticPrivateSentinel) {
		t.Fatal("diagnostic exposed source error or content")
	}
	return d
}

func TestMCPContextDiagnosticDatabaseBranches(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	fault, err := sql.Open("mcp-context-diagnostic-test", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	defer fault.Close()
	for _, tc := range []struct {
		match string
		code  mcpContextFailureCode
	}{
		{"SELECT trigger_message_id,parent_run_id,kind", mcpContextRunRead},
		{"SELECT m.content,m.role", mcpContextIntentRead},
		{"SELECT instructions", mcpContextInstructionsRead},
		{"SELECT COUNT(*) FROM attachments", mcpContextAttachmentsRead},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			_, digest, err := readMCPReviewContext(contextDiagnosticQuerier{f.s.store.db, fault, tc.match, "query"}, f.c, f.r)
			assertContextDiagnostic(t, err, tc.code)
			if digest != "" {
				t.Fatal("failed context retained an execution digest")
			}
		})
	}
	for _, tc := range []struct {
		match                  string
		query, scan, iteration mcpContextFailureCode
	}{
		{"SELECT m.id,m.seq", mcpContextMessagesQuery, mcpContextMessagesScan, mcpContextMessagesIteration},
		{"FROM tool_activities WHERE run_id", mcpContextToolsQuery, mcpContextToolsScan, mcpContextToolsIteration},
		{"SELECT q.id,q.run_id,a.action_hash", mcpContextRefusalsQuery, mcpContextRefusalsScan, mcpContextRefusalsIteration},
	} {
		for _, mode := range []string{"query", "scan", "iteration"} {
			code := map[string]mcpContextFailureCode{"query": tc.query, "scan": tc.scan, "iteration": tc.iteration}[mode]
			t.Run(string(code), func(t *testing.T) {
				_, digest, err := readMCPReviewContext(contextDiagnosticQuerier{f.s.store.db, fault, tc.match, mode}, f.c, f.r)
				assertContextDiagnostic(t, err, code)
				if digest != "" {
					t.Fatal("failed context retained an execution digest")
				}
			})
		}
	}
	if f.p.calls.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("diagnosis called a provider or tool")
	}
}

func diagnosticExec(t *testing.T, f *autoReviewFixture, query string, args ...any) {
	t.Helper()
	if _, err := f.s.store.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func diagnosticTools(t *testing.T, f *autoReviewFixture, count int, result string) {
	t.Helper()
	diagnosticExec(t, f, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at,outcome_json)
SELECT ?,?,?,printf('synthetic-%d',i),'synthetic','{}',?,'completed',?,? ,'' FROM n`, count, f.c.ID, f.r.BotID, f.r.ID, result, now(), now())
}
func diagnosticRefusals(t *testing.T, f *autoReviewFixture, count int, raw string) {
	t.Helper()
	diagnosticExec(t, f, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO questions(id,run_id,conversation_id,bot_id,type,prompt,status,answer_json,answered_by,created_at,updated_at,approval_json)
SELECT printf('synthetic-refusal-%d',i),?,?,?,'approval','synthetic','answered','false','synthetic-human',?,?,? FROM n`, count, f.r.ID, f.c.ID, f.r.BotID, now(), now(), raw)
	diagnosticExec(t, f, `INSERT INTO mcp_call_approvals(question_id,run_id,action_hash)
SELECT id,run_id,id FROM questions WHERE id LIKE 'synthetic-refusal-%'`)
}

func TestMCPContextDiagnosticDataBranches(t *testing.T) {
	for _, tc := range []struct {
		name            string
		code            mcpContextFailureCode
		mutate          func(*testing.T, *autoReviewFixture)
		observed, limit int
		atLeast         bool
	}{
		{"run binding", mcpContextRunBinding, func(t *testing.T, f *autoReviewFixture) { f.r.TriggerMessageID = "synthetic-other" }, -1, 0, false},
		{"no trigger", mcpContextUserUnavailable, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE runs SET trigger_message_id='' WHERE id=?`, f.r.ID)
			f.r.TriggerMessageID = ""
		}, -1, 0, false},
		{"non chat kind", mcpContextUserUnavailable, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE runs SET kind='synthetic-unsupported' WHERE id=?`, f.r.ID)
			f.r.Kind = "synthetic-unsupported"
		}, -1, 0, false},
		{"parent run", mcpContextUserUnavailable, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE runs SET parent_run_id=? WHERE id=?`, f.r.ID, f.r.ID)
			f.r.ParentRunID = f.r.ID
		}, -1, 0, false},
		{"intent role", mcpContextIntentInvalid, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE messages SET role='assistant' WHERE id=?`, f.r.TriggerMessageID)
		}, -1, 0, false},
		{"intent kind", mcpContextIntentInvalid, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE messages SET kind='synthetic-not-user' WHERE id=?`, f.r.TriggerMessageID)
		}, -1, 0, false},
		{"intent sender", mcpContextIntentInvalid, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE messages SET sender_bot_id=? WHERE id=?`, f.r.BotID, f.r.TriggerMessageID)
		}, -1, 0, false},
		{"empty intent", mcpContextIntentInvalid, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE messages SET content=' ' WHERE id=?`, f.r.TriggerMessageID)
		}, -1, 0, false},
		{"unknown ingress", mcpContextIntentProvenance, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `DELETE FROM user_message_ingress WHERE message_id=?`, f.r.TriggerMessageID)
		}, -1, 0, false},
		{"imported ingress", mcpContextIntentProvenance, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `INSERT INTO portability_provenance VALUES('message',?,'{}')`, f.r.TriggerMessageID)
		}, -1, 0, false},
		{"current attachment", mcpContextNonText, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `INSERT INTO attachments VALUES('synthetic',?,'synthetic','text/plain',0,'synthetic',?)`, f.c.ID, now())
			if err := f.s.store.BindAttachments(f.c.ID, f.r.TriggerMessageID, []string{"synthetic"}); err != nil {
				t.Fatal(err)
			}
		}, 1, 0, false},
		{"malformed outcome", mcpContextToolsOutcome, func(t *testing.T, f *autoReviewFixture) {
			diagnosticTools(t, f, 1, "")
			diagnosticExec(t, f, `UPDATE tool_activities SET outcome_json=?`, diagnosticPrivateSentinel)
		}, -1, 0, false},
		{"invalid arguments utf8", mcpContextToolsUTF8, func(t *testing.T, f *autoReviewFixture) {
			diagnosticTools(t, f, 1, "")
			diagnosticExec(t, f, `UPDATE tool_activities SET arguments=CAST(x'ff' AS TEXT)`)
		}, -1, 0, false},
		{"invalid result utf8", mcpContextToolsUTF8, func(t *testing.T, f *autoReviewFixture) {
			diagnosticTools(t, f, 1, "")
			diagnosticExec(t, f, `UPDATE tool_activities SET result=CAST(x'ff' AS TEXT)`)
		}, -1, 0, false},
		{"malformed refusal", mcpContextRefusalsInvalid, func(t *testing.T, f *autoReviewFixture) { diagnosticRefusals(t, f, 1, diagnosticPrivateSentinel) }, -1, 0, false},
		{"null refusal", mcpContextRefusalsInvalid, func(t *testing.T, f *autoReviewFixture) { diagnosticRefusals(t, f, 1, `null`) }, -1, 0, false},
		{"tool binding", mcpContextToolsBinding, func(t *testing.T, f *autoReviewFixture) {
			diagnosticTools(t, f, 1, "")
			diagnosticExec(t, f, `UPDATE tool_activities SET conversation_id='synthetic-elsewhere'`)
		}, -1, 0, false},
		{"group round", mcpContextGroupRound, func(t *testing.T, f *autoReviewFixture) {
			diagnosticExec(t, f, `UPDATE runs SET kind=? WHERE id=?`, runKindGroupChat, f.r.ID)
			f.r.Kind = runKindGroupChat
		}, -1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProvenanceReviewFixture(t)
			tc.mutate(t, f)
			_, digest, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
			d := assertContextDiagnostic(t, err, tc.code)
			if digest != "" {
				t.Fatal("failed context has a digest")
			}
			if tc.observed >= 0 && (d.Observed == nil || *d.Observed != tc.observed || d.Limit == nil || *d.Limit != tc.limit || d.ObservedAtLeast != tc.atLeast) {
				t.Fatalf("wrong bounded metric: %+v", d)
			}
			if tc.observed == -1 && (d.Observed != nil || d.Limit != nil) {
				t.Fatal("diagnostic invented a measurement")
			}
			if f.p.calls.Load() != 0 || f.effects.Load() != 0 {
				t.Fatal("failed context called a provider/tool")
			}
		})
	}
}

func TestMCPContextDiagnosticPreflightPersistsWithoutReview(t *testing.T) {
	for _, mode := range []string{"auto", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			f := newProvenanceReviewFixture(t)
			q := newMCPReviewProposal(t, f)
			if err := f.s.store.putAutoReviewMode(mode); err != nil {
				t.Fatal(err)
			}
			diagnosticExec(t, f, `DELETE FROM user_message_ingress WHERE message_id=?`, f.r.TriggerMessageID)
			err := f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q)
			if mode == "auto" {
				out, ok := tooloutcome.FromError(err)
				if !ok || out.Code != "mcp_review_context_missing" || out.Certainty != "not_executed" {
					t.Fatal("preflight changed execution outcome", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			q, err = f.s.store.GetQuestion(q.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := "context_required"
			if mode == "shadow" {
				wantStatus = "shadow_context_required"
			}
			if q.Approval.Review.Status != wantStatus || q.Approval.Review.ContextFailure == nil || q.Approval.Review.ContextFailure.Code != mcpContextIntentProvenance || q.AnsweredBy != "" || q.Answer != nil {
				t.Fatal("durable diagnostic/decision mismatch", q.Approval.Review)
			}
			if (q.Status == questionCancelled) != (mode == "auto") {
				t.Fatal("diagnostic changed proposal lifecycle")
			}
			var reviews, claims int
			if err = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_auto_reviews WHERE question_id=?`, q.ID).Scan(&reviews); err != nil {
				t.Fatal(err)
			}
			if err = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_execution_claims WHERE run_id=?`, f.r.ID).Scan(&claims); err != nil {
				t.Fatal(err)
			}
			if reviews != 0 || claims != 0 || f.p.calls.Load() != 0 || f.effects.Load() != 0 {
				t.Fatal("preflight spent review/claim/effect")
			}
			res := httptest.NewRecorder()
			if !f.s.routeQuestions(res, httptest.NewRequest(http.MethodGet, "/api/questions?conversation_id="+f.c.ID, nil), "questions") || res.Code != 200 || !strings.Contains(res.Body.String(), `"context_failure":{"code":"intent_provenance_unverified"}`) {
				t.Fatal("supported question API lost diagnostic")
			}
			events, e := f.s.store.Events(f.c.ID, 0)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, event := range events {
				if event["type"] == "question" {
					raw, _ := json.Marshal(event["data"])
					var card QuestionCard
					if json.Unmarshal(raw, &card) == nil && card.QuestionID == q.ID && card.Approval.Review != nil && card.Approval.Review.ContextFailure != nil {
						found = card.Approval.Review.ContextFailure.Code == mcpContextIntentProvenance
					}
				}
			}
			if !found {
				t.Fatal("durable replay event lost diagnostic")
			}
			if err = f.s.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, e := OpenStore(f.dir)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			persisted, e := reopened.GetQuestion(q.ID)
			if e != nil || persisted.Approval.Review.ContextFailure == nil || persisted.Approval.Review.ContextFailure.Code != mcpContextIntentProvenance {
				t.Fatal("restart lost diagnostic", e)
			}
		})
	}
}

func TestMCPContextDiagnosticUnknownErrorAndSuccess(t *testing.T) {
	d := mcpContextDiagnostic(fmt.Errorf("%s: %w", diagnosticPrivateSentinel, errors.New(diagnosticPrivateSentinel)))
	raw, _ := json.Marshal(d)
	if d.Code != mcpContextUnclassified || strings.Contains(string(raw), diagnosticPrivateSentinel) || mcpContextDiagnostic(nil) != nil {
		t.Fatal("untyped errors leaked or invented a cause")
	}
	for _, code := range []mcpContextFailureCode{mcpContextEncoding, mcpContextSnapshotEncoding, mcpContextDigestChanged} {
		assertContextDiagnostic(t, mcpContextFail(code), code)
	}
	f := newProvenanceReviewFixture(t)
	diagnosticTools(t, f, 3, "synthetic schema/result")
	diagnosticExec(t, f, `UPDATE tool_activities SET status='failed',outcome_json=? WHERE call_id='synthetic-1'`, tooloutcome.New(tooloutcome.Validation, "stale_schema", "not_executed", "Synthetic stale schema.", "refresh_tools").JSON())
	diagnosticExec(t, f, `UPDATE tool_activities SET name='search_mcp_tools' WHERE call_id='synthetic-2'`)
	diagnosticExec(t, f, `UPDATE tool_activities SET name='call_mcp_tool',status='running',result='' WHERE call_id='synthetic-3'`)
	_, digest, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || digest == "" {
		t.Fatal("synthetic stale/refresh/new proposal alone caused a context failure", err)
	}
	if f.p.calls.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("reader diagnosis called a provider/tool")
	}
}
