package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Production agent suspension/continuation and official MCP SDK dispatch run
// unchanged. Both providers are synthetic and in process, without a listener.
func TestAutoReviewV5HumanRequiredDurableResume(t *testing.T) {
	for _, mutation := range []string{"unchanged", "user intent", "related evidence", "human refusal", "unknown control outcome", "forged progress", "old progress edit", "active proposal arguments", "config binding", "expiry", "off epoch", "missing snapshot", "checkpoint question", "attachment unchanged", "attachment add", "attachment delete", "attachment relink"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAutoReviewFixture(t)
			var oldAttachmentMessage string
			if strings.HasPrefix(mutation, "attachment ") {
				oldAttachmentMessage = seedHistoricalMCPAttachments(t, f, "Read the synthetic public fact for alpha.")
			}
			setSyntheticMCPHumanPolicy(t, f, true)
			if err := f.s.store.putAutoReviewMode("auto"); err != nil {
				t.Fatal(err)
			}
			f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
				return v5ReviewReply(req, "allow", "high", true), nil
			}
			// Related evidence stays in the binding even while the approval's
			// own lifecycle records are normalized.
			for _, status := range []string{"queued", "running"} {
				if err := f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, runtime.ToolEvent{CallID: "related-evidence", Name: "synthetic_fact", Arguments: `{}`, Status: status}); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := f.s.extensions.PrepareDiscoverableForBotWithCallGate(context.Background(), f.r.BotID, nil, func(ctx context.Context, call extensions.MCPCallApproval) error {
				return f.s.approveMCPCall(ctx, f.c, f.r, call)
			})
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			for _, tool := range prepared.Tools {
				if tool.Name == "search_mcp_tools" {
					if _, err := tool.Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"read_public"}`)); err != nil {
						t.Fatal(err)
					}
				}
			}
			var modelCalls atomic.Int32
			oldTransport := http.DefaultTransport
			http.DefaultTransport = reviewTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "human-resume.synthetic.invalid" {
					return nil, fmt.Errorf("unexpected synthetic destination %s", req.URL.Host)
				}
				var payload struct {
					Messages []struct {
						Role, Content string
						CallID        string `json:"tool_call_id"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					return nil, err
				}
				n := modelCalls.Add(1)
				content := "Synthetic action awaiting confirmation."
				callID := "approval-waiting-call"
				if n == 2 {
					content, callID = "Synthetic confirmation received; proposing the same operation.", "approval-resumed-call"
					found := false
					for _, m := range payload.Messages {
						found = found || m.CallID == "approval-waiting-call" && strings.Contains(m.Content, "approval_recorded")
					}
					if !found {
						t.Error("production continuation did not receive approval_recorded")
					}
				}
				delta := map[string]any{"content": content, "tool_calls": []any{map[string]any{"index": 0, "id": callID, "type": "function", "function": map[string]string{"name": "call_mcp_tool", "arguments": `{"name":"mcp_fixture__read_public","arguments":{"target":"alpha"}}`}}}}
				if n > 2 {
					delta = map[string]any{"content": "Synthetic lifecycle finished."}
				}
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + string(raw) + "\n\ndata: [DONE]\n\n")), Request: req}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = oldTransport })
			engine, err := runtime.New(runtime.Config{Provider: "openai_completions", APIKey: "synthetic", BaseURL: "https://human-resume.synthetic.invalid/v1", Model: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.store.BeginStream(f.r); err != nil {
				t.Fatal(err)
			}
			resuming := false
			request := runtime.Request{BotID: f.r.BotID, RunID: f.r.ID, Model: "synthetic", Messages: []runtime.Message{{Role: "user", Content: "Read the synthetic public fact for alpha."}}, Tools: prepared.Tools, OnDelta: func(string) {},
				OnAssistantTurn: func(index int, content string) error {
					_, _, err := f.s.store.PublishAssistantTurn(context.Background(), f.r.ID, index, content)
					return err
				},
				OnToolEvent: func(event runtime.ToolEvent) error {
					if resuming && mutation == "unknown control outcome" && event.CallID == "approval-waiting-call" && event.Status == "completed" {
						event.Outcome = &tooloutcome.Outcome{Version: 1, Status: tooloutcome.Uncertain, Code: "synthetic_uncertain", Certainty: "unknown", Message: "Synthetic uncertain effect", NextAction: "verify_effect"}
						event.Result = event.Outcome.JSON()
					}
					if resuming && mutation == "active proposal arguments" && event.CallID == "approval-resumed-call" {
						event.Arguments = `{"name":"mcp_fixture__read_public","arguments":{"target":"changed"}}`
					}
					return f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, event)
				},
				OnSuspend: func(id string, checkpoint json.RawMessage) error {
					return f.s.store.SaveInputContinuation(context.Background(), f.r.ID, id, checkpoint)
				},
			}
			paused, err := engine.Run(context.Background(), request)
			if err != nil || !paused.Suspended || f.effects.Load() != 0 || f.p.calls.Load() != 1 {
				t.Fatal("high-risk proposal did not durably suspend", paused, err)
			}
			q := waitReviewQuestion(t, f, "human_required")
			var state string
			if err := f.s.store.db.QueryRow(`SELECT state FROM run_input_waits WHERE run_id=? AND question_id=?`, f.r.ID, q.ID).Scan(&state); err != nil || state != "waiting" {
				t.Fatal("durable checkpoint missing", state, err)
			}
			if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", true); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.refreshInputWaits(f.c.ID); err != nil {
				t.Fatal(err)
			}
			checkpoint, answered, claimed, err := f.s.store.claimInputContinuation(context.Background(), f.r.ID)
			if err != nil || !claimed || answered.ID != q.ID {
				t.Fatal("continuation did not claim", claimed, err)
			}
			resuming = true
			request.Continuation, request.ResumeResult, request.ResumeOutcome = checkpoint, f.s.inputResumeResult(answered), f.s.inputResumeOutcome(answered)
			switch mutation {
			case "user intent":
				_, err = f.s.store.db.Exec(`UPDATE messages SET content='Do not execute the synthetic operation.' WHERE id=?`, f.r.TriggerMessageID)
			case "related evidence":
				err = f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, runtime.ToolEvent{CallID: "related-evidence", Name: "synthetic_fact", Status: "completed", Result: "Synthetic material fact changed."})
			case "human refusal":
				var denied Question
				denied, err = f.s.store.CreateQuestion(f.c.ID, f.r, askQuestionInput{Question: "Synthetic restriction", Type: questionApproval, Approval: &ApprovalDetails{Action: "Other method", Target: "Same synthetic effect", Impact: "Synthetic"}})
				if err == nil {
					_, err = f.s.store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) VALUES(?,?,?)`, denied.ID, f.r.ID, "synthetic-other-method")
				}
				if err == nil {
					_, _, err = f.s.store.AnswerQuestion(denied.ID, "synthetic-human", false)
				}
			case "forged progress":
				_, _, err = f.s.store.AddMessage(f.c.ID, "assistant", "progress", f.r.BotID, "Synthetic new fact without a native progress receipt.", f.r.ID)
			case "old progress edit":
				_, err = f.s.store.db.Exec(`UPDATE messages SET content='Synthetic earlier evidence changed.' WHERE id IN (SELECT message_id FROM stream_assistant_turns WHERE run_id=? AND turn_index=1)`, f.r.ID)
			case "config binding":
				_, err = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET config_fingerprint='changed' WHERE question_id=?`, q.ID)
			case "expiry":
				_, err = f.s.store.db.Exec(`UPDATE questions SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, q.ID)
			case "off epoch":
				err = f.s.store.putAutoReviewMode("off")
				if err == nil {
					err = f.s.store.putAutoReviewMode("auto")
				}
			case "missing snapshot":
				_, err = f.s.store.db.Exec(`UPDATE mcp_auto_reviews SET context_snapshot='' WHERE question_id=?`, q.ID)
			case "checkpoint question":
				_, err = f.s.store.db.Exec(`UPDATE run_input_waits SET checkpoint_json=json_set(checkpoint_json,'$.agent.question_id','wrong-question') WHERE run_id=?`, f.r.ID)
			case "attachment add":
				_, err = f.s.store.db.Exec(`INSERT INTO attachments VALUES('synthetic-resume-file',?,'synthetic','text/plain',1,'synthetic-resume-file',?)`, f.c.ID, now())
				if err == nil {
					err = f.s.store.BindAttachments(f.c.ID, oldAttachmentMessage, []string{"synthetic-resume-file"})
				}
			case "attachment delete":
				_, err = f.s.store.db.Exec(`DELETE FROM attachments WHERE id='synthetic-old-file-1'`)
			case "attachment relink":
				err = f.s.store.BindAttachments(f.c.ID, f.r.TriggerMessageID, []string{"synthetic-old-file-1"})
			}
			if err != nil {
				t.Fatal(err)
			}
			finished, err := engine.Run(context.Background(), request)
			if err != nil {
				t.Fatal("production resume failed", finished, err)
			}
			want := int32(0)
			if mutation == "unchanged" || mutation == "attachment unchanged" {
				want = 1
			}
			if f.effects.Load() != want || f.p.calls.Load() != 1 {
				t.Fatalf("resume effects=%d want=%d reviewer requests=%d result=%+v", f.effects.Load(), want, f.p.calls.Load(), finished)
			}
			_, _, duplicate, err := f.s.store.claimInputContinuation(context.Background(), f.r.ID)
			if err == nil && duplicate {
				t.Fatal("duplicate continuation claimed")
			}
			if mutation == "unchanged" || mutation == "attachment unchanged" {
				if err := f.execute(context.Background()); err == nil || f.effects.Load() != 1 || f.p.calls.Load() != 1 {
					t.Fatal("approved proposal replayed", err)
				}
			}
		})
	}
}
