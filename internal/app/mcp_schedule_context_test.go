package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// These fixtures never prepare a transport or dispatch an MCP operation. The
// only effect is a counter increment after the real durable claim succeeds.
type scheduledMCPFixture struct {
	t               *testing.T
	dir             string
	s               *Server
	bot, colleague  Bot
	c               Conversation
	r, root, source Run
	schedule        Schedule
	call            extensions.MCPCallApproval
	p               *reviewStub
	effects         atomic.Int32
}

func newScheduledMCPFixture(t *testing.T, group bool, sourceKind string) *scheduledMCPFixture {
	t.Helper()
	dir := t.TempDir()
	store, bot, c := scheduleTestStoreAt(t, dir)
	f := &scheduledMCPFixture{t: t, dir: dir, bot: bot, c: c}
	f.s = &Server{store: store, accountID: "synthetic-schedule-account", closing: true}
	t.Cleanup(func() { f.s.stopShadowMCPReviews(); _ = f.s.store.Close() })
	var err error
	f.colleague, err = store.CreateBot("synthetic colleague", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if group {
		f.c, err = store.CreateGroup("synthetic schedule group", []string{bot.ID, f.colleague.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, f.source, _, err = store.AddUserRun(f.c.ID, bot.ID, "Every minute read synthetic alpha. Keep the original scope; never send its contents elsewhere.", "synthetic-schedule-native-source")
	if err != nil {
		t.Fatal(err)
	}
	f.setStatus(f.source.ID, "running", "")
	f.source, err = store.GetRun(f.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec := ScheduleSpec{Content: "Read synthetic alpha and report its public value.", Kind: scheduleInterval, IntervalSeconds: 60, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), Timezone: "UTC"}
	var source *scheduleMutationSource
	switch sourceKind {
	case scheduleSourceChat:
		source = f.s.scheduleChatSource(f.c, f.source)
	case scheduleSourceForm:
		source = f.s.scheduleFormSource("create", spec)
	case scheduleSourceUnknown:
	default:
		t.Fatalf("unsupported fixture source %q", sourceKind)
	}
	f.schedule, err = store.createScheduleWithSource(f.c.ID, bot.ID, spec, source)
	if err != nil {
		t.Fatal(err)
	}
	f.setStatus(f.source.ID, "done", "")
	makeDue(t, store, f.schedule.ID, time.Now().Add(-time.Minute))
	runs, err := store.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim occurrence: %+v %v", runs, err)
	}
	f.root = runs[0]
	f.target(f.root)

	// Match the host's metadata fingerprint using public config fields. Writing
	// this synthetic file is enough for MCPCallCurrent; no connection is opened.
	cfg := extensions.MCPServerConfig{URL: "https://fixture.invalid/mcp", Transport: "streamable_http", TrustedReadOnlyTools: []string{"read_public"}}
	config, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"mcpServers": map[string]extensions.MCPServerConfig{"schedule-fixture": cfg}})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "synthetic-mcp.json")
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f.s.extensions = extensions.NewManager(extensions.Config{MCPConfigPath: configPath, SkillsDir: filepath.Join(dir, "synthetic-skills")})
	fingerprint := sha256.Sum256(append([]byte("2026-07-28\x00schedule-fixture\x00"), config...))
	f.call = extensions.MCPCallApproval{Server: "schedule-fixture", Tool: "read_public", Description: "Synthetic public read", ConfigVersion: hex.EncodeToString(fingerprint[:]), Schema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"}},"required":["target"],"additionalProperties":false}`), Arguments: json.RawMessage(`{"target":"alpha"}`)}
	if !f.s.extensions.MCPCallCurrent(f.call) {
		t.Fatal("synthetic static configuration is not current")
	}
	f.p = &reviewStub{t: t, reply: func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		return reviewReply(req, "allow"), nil
	}}
	f.s.autoReviewProvider = f.p

	if err := store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *scheduledMCPFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.s.store.db.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *scheduledMCPFixture) setStatus(id, status, reason string) {
	f.t.Helper()
	if ok, err := f.s.store.SetRunStatus(id, status, reason); err != nil || !ok {
		f.t.Fatalf("set %s %s: %v %v", id, status, ok, err)
	}
}

func (f *scheduledMCPFixture) target(r Run) {
	f.t.Helper()
	if r.Status == "queued" {
		f.setStatus(r.ID, "running", "")
	}
	var err error
	f.r, err = f.s.store.GetRun(r.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	f.c, err = f.s.store.GetConversation(f.r.ConversationID)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *scheduledMCPFixture) delegate(bot Bot, group bool) Run {
	f.t.Helper()
	parent := f.r
	var child Run
	var err error
	if group {
		_, child, err = f.s.store.AddHandoff(f.c.ID, parent.BotID, bot.ID, parent.ID, "Read only the synthetic public fact for the requester.")
	} else {
		var children []Run
		_, children, err = f.s.store.AddBotMessages(f.c.ID, parent.BotID, []string{bot.ID}, parent.ID, "Read only the synthetic public fact for the requester.")
		if err != nil || len(children) != 1 {
			f.t.Fatalf("single-recipient trace: %+v %v", children, err)
		}
		child = children[0]
	}
	if err != nil {
		f.t.Fatal(err)
	}
	if ok, err := f.s.store.yieldScheduledRun(parent.ID); err != nil || !ok {
		f.t.Fatalf("yield actual delegation: %v %v", ok, err)
	}
	f.target(child)
	return f.r
}

func (f *scheduledMCPFixture) finishReturn() Run {
	f.t.Helper()
	child := f.r
	if _, ok, err := f.s.store.FinishRun(child.ID, child.ConversationID, child.BotID, "Synthetic public alpha is 7."); err != nil || !ok {
		f.t.Fatalf("finish child: %v %v", ok, err)
	}
	var followup Run
	var ok bool
	var err error
	if child.ConversationID == f.root.ConversationID {
		followup, ok, err = f.s.store.GroupFollowupForRun(child.ID)
	} else {
		followup, ok, err = f.s.store.DirectMessageFollowupForRun(child.ID)
	}
	if err != nil || !ok {
		f.t.Fatalf("durable return: %v %v", ok, err)
	}
	f.target(followup)
	return f.r
}

func (f *scheduledMCPFixture) question() Question {
	f.t.Helper()
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Allow this synthetic exact public read?", Type: questionApproval, Approval: &ApprovalDetails{Action: f.call.Tool, Target: f.call.Server, Impact: "Synthetic public read", Payload: string(f.call.Arguments)}})
	if err != nil {
		f.t.Fatal(err)
	}
	q, err := f.s.store.CreateQuestion(f.c.ID, f.r, in)
	if err != nil {
		f.t.Fatal(err)
	}
	f.exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) VALUES(?,?,?)`, q.ID, f.r.ID, mcpApprovalHash(f.call))
	return q
}

func (f *scheduledMCPFixture) review(q Question) error {
	return f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q)
}

func (f *scheduledMCPFixture) approvedQuestion() Question {
	f.t.Helper()
	q := f.question()
	if err := f.review(q); err != nil {
		f.t.Fatal(err)
	}
	q, err := f.s.store.GetQuestion(q.ID)
	if err != nil || q.AnsweredBy != autoReviewActor || q.Status != questionAnswered {
		f.t.Fatalf("automatic decision: %+v %v", q, err)
	}
	return q
}

func (f *scheduledMCPFixture) claimEffect(id string) error {
	result, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("synthetic effect did not claim execution: %d", n)
	}
	f.effects.Add(1)
	return nil
}

func (f *scheduledMCPFixture) claimCount() int {
	f.t.Helper()
	var n int
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_execution_claims WHERE run_id=?`, f.r.ID).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *scheduledMCPFixture) assertQuestionEvent(id string) {
	f.t.Helper()
	var n int
	if err := f.s.store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='question' AND json_extract(data,'$.question_id')=?`, f.c.ID, id).Scan(&n); err != nil || n == 0 {
		f.t.Fatalf("durable question event missing: %d %v", n, err)
	}
}

func (f *scheduledMCPFixture) toolEvidence(truncated, uncertain bool) {
	f.t.Helper()
	events := []runtime.ToolEvent{{CallID: "synthetic-context-read", Name: "synthetic_public_read", Arguments: `{"target":"alpha"}`, Status: "queued"}, {CallID: "synthetic-context-read", Name: "synthetic_public_read", Status: "running"}, {CallID: "synthetic-context-read", Name: "synthetic_public_read", Status: "completed", Result: "Synthetic alpha is public.", Truncated: truncated}}
	if uncertain {
		outcome := tooloutcome.New(tooloutcome.Uncertain, "synthetic_uncertain", "unknown", "Synthetic effect result is unknown.", "verify_effect")
		events[2].Outcome = &outcome
	}
	for _, event := range events {
		if err := f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, event); err != nil {
			f.t.Fatal(err)
		}
	}
}

// One recorded step whose outcome is unverified. Browser steps carry the
// microVM tool's own argument shape; MCP steps carry call_mcp_tool's.
func (f *scheduledMCPFixture) uncertainStep(callID, name, arguments string) {
	f.t.Helper()
	outcome := tooloutcome.New(tooloutcome.Uncertain, "synthetic_uncertain", "unknown", "Synthetic step result is unknown.", "verify_effect")
	events := []runtime.ToolEvent{{CallID: callID, Name: name, Arguments: arguments, Status: "queued"}, {CallID: callID, Name: name, Status: "running"}, {CallID: callID, Name: name, Status: "failed", Result: outcome.JSON(), Outcome: &outcome}}
	for _, event := range events {
		if err := f.s.store.RecordToolEvent(f.c.ID, f.r.BotID, f.r.ID, event); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *scheduledMCPFixture) uncertainBrowserNavigate() {
	f.uncertainStep("synthetic-navigate", "computer_browser", `{"action":"browser.navigate","url":"https://fixture.invalid/alpha"}`)
}

func (f *scheduledMCPFixture) uncertainMCPWrite(tool string) {
	f.uncertainStep("synthetic-"+tool, "call_mcp_tool", `{"name":"mcp_schedule-fixture__`+tool+`","arguments":{"target":"alpha"}}`)
}

// Re-point the proposal at a tool outside the owner's trusted read-only list.
// The remote hint is what the fixture's tools/list would have carried.
func (f *scheduledMCPFixture) proposeTool(tool string, readOnlyHint bool) {
	f.call.Tool, f.call.ReadOnlyHint, f.call.Description = tool, readOnlyHint, "Synthetic "+tool
	if !f.s.extensions.MCPCallCurrent(f.call) {
		f.t.Fatal("synthetic static configuration is not current")
	}
}

func TestScheduledMCPContextBoundedLineage(t *testing.T) {
	cases := []struct {
		name   string
		group  bool
		source string
		valid  bool
		setup  func(*scheduledMCPFixture)
	}{
		{"native chat root", false, scheduleSourceChat, true, nil},
		{"native form root", false, scheduleSourceForm, true, nil},
		{"complete durable tool result", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) { f.toolEvidence(false, false) }},
		{"group delegation", true, scheduleSourceChat, true, func(f *scheduledMCPFixture) { f.delegate(f.colleague, true) }},
		{"cross message", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) { f.delegate(f.colleague, false) }},
		{"legacy single bot message", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			parent := f.r
			_, child, err := f.s.store.AddBotMessage(f.c.ID, parent.BotID, f.colleague.ID, parent.ID, "Read public synthetic alpha.")
			if err != nil {
				f.t.Fatal(err)
			}
			if ok, err := f.s.store.yieldScheduledRun(parent.ID); err != nil || !ok {
				f.t.Fatal(ok, err)
			}
			f.target(child)
		}},
		{"legacy forward", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			parent := f.r
			_, child, err := f.s.store.AddForwardHandoff(f.c.ID, parent.BotID, f.colleague.ID, parent.ID, "Read synthetic alpha.")
			if err != nil {
				f.t.Fatal(err)
			}
			if ok, err := f.s.store.yieldScheduledRun(parent.ID); err != nil || !ok {
				f.t.Fatal(ok, err)
			}
			f.target(child)
		}},
		{"fanout second sibling", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			extra, err := f.s.store.CreateBot("second synthetic colleague", "", "model")
			if err != nil {
				f.t.Fatal(err)
			}
			parent := f.r
			_, children, err := f.s.store.AddBotMessages(f.c.ID, parent.BotID, []string{f.colleague.ID, extra.ID}, parent.ID, "Read public synthetic alpha.")
			if err != nil || len(children) != 2 {
				f.t.Fatal(children, err)
			}
			if ok, err := f.s.store.yieldScheduledRun(parent.ID); err != nil || !ok {
				f.t.Fatal(ok, err)
			}
			f.target(children[1])
		}},
		{"cross return after waiting root", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) { f.delegate(f.colleague, false); f.finishReturn() }},
		{"nested group returns", true, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			extra, err := f.s.store.CreateBot("nested synthetic colleague", "", "model")
			if err != nil {
				f.t.Fatal(err)
			}
			f.exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, f.c.ID, extra.ID)
			f.delegate(f.colleague, true)
			f.delegate(extra, true)
			f.finishReturn()
			f.finishReturn()
		}},
		{"nested cross returns", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			extra, err := f.s.store.CreateBot("nested synthetic colleague", "", "model")
			if err != nil {
				f.t.Fatal(err)
			}
			f.delegate(f.colleague, false)
			f.delegate(extra, false)
			f.finishReturn()
			f.finishReturn()
		}},
		{"cross team then group return keeps origin", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			extra, err := f.s.store.CreateBot("team synthetic colleague", "", "model")
			if err != nil {
				f.t.Fatal(err)
			}
			g, err := f.s.store.CreateGroup("synthetic team", []string{f.bot.ID, f.colleague.ID, extra.ID})
			if err != nil {
				f.t.Fatal(err)
			}
			parent := f.r
			args, _ := json.Marshal(map[string]string{"group_id": g.ID, "bot_id": f.colleague.ID, "message": "Read public synthetic alpha."})
			result, err := f.s.teamSend(context.Background(), f.c, parent, args)
			if err != nil {
				f.t.Fatal(err)
			}
			var payload struct {
				Runs []Run `json:"runs"`
			}
			if json.Unmarshal([]byte(result), &payload) != nil || len(payload.Runs) != 1 {
				f.t.Fatal(result)
			}
			if ok, err := f.s.store.yieldScheduledRun(parent.ID); err != nil || !ok {
				f.t.Fatal(ok, err)
			}
			f.target(payload.Runs[0])
			f.delegate(extra, true)
			child := f.r
			if _, ok, err := f.s.store.FinishRun(child.ID, child.ConversationID, child.BotID, "Synthetic public alpha is 7."); err != nil || !ok {
				f.t.Fatal(ok, err)
			}
			followup, ok, err := f.s.store.GroupFollowupForRun(child.ID)
			if err != nil || !ok {
				f.t.Fatal(ok, err)
			}
			f.target(followup)
			if f.r.OriginConversationID == f.r.ConversationID {
				f.t.Fatal("fixture lost inherited team origin")
			}
		}},
		{"unknown original source", false, scheduleSourceUnknown, false, nil},
		{"wrong account", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) { f.s.accountID = "other-synthetic-account" }},
		{"single-recipient notice target mismatch", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			m, err := f.s.store.GetMessage(f.r.TriggerMessageID)
			if err != nil || m.Notice == nil {
				f.t.Fatal(m, err)
			}
			m.Notice.TargetBotIDs = []string{f.root.BotID}
			raw, err := json.Marshal(m.Notice)
			if err != nil {
				f.t.Fatal(err)
			}
			f.exec(`UPDATE messages SET notice_data=? WHERE id=?`, string(raw), m.ID)
		}},
		{"single-recipient trace has ambiguous sibling", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			sibling, err := f.s.store.AddRun(f.c.ID, f.r.BotID, f.root.ID)
			if err != nil {
				f.t.Fatal(err)
			}
			f.exec(`UPDATE runs SET kind=?,origin_conversation_id=?,trigger_message_id=? WHERE id=?`, runKindMessage, f.root.ConversationID, f.r.TriggerMessageID, sibling.ID)
		}},
		{"wrong target bot", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) { f.r.BotID = f.colleague.ID }},
		{"wrong target conversation", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) { f.c.ID = f.colleague.DMConversationID }},
		{"wrong root bot", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.exec(`UPDATE runs SET bot_id=? WHERE id=?`, f.colleague.ID, f.root.ID)
			f.r.BotID = f.colleague.ID
		}},
		{"missing original ingress", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.exec(`DELETE FROM user_message_ingress WHERE message_id=?`, f.source.TriggerMessageID)
		}},
		{"missing occurrence snapshot", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.exec(`DELETE FROM schedule_occurrence_authorizations WHERE root_run_id=?`, f.root.ID)
		}},
		{"missing occurrence root", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) { f.exec(`DELETE FROM schedule_occurrences WHERE run_id=?`, f.root.ID) }},
		{"missing parent", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.exec(`UPDATE runs SET parent_run_id='missing-synthetic-parent' WHERE id=?`, f.r.ID)
			f.r.ParentRunID = "missing-synthetic-parent"
		}},
		{"ancestry cycle", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.exec(`UPDATE runs SET parent_run_id=? WHERE id=?`, f.r.ID, f.root.ID)
		}},
		{"ambiguous occurrence roots", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.exec(`INSERT INTO schedule_occurrences(schedule_id,scheduled_for_utc,run_id,created_at) VALUES(?,?,?,?)`, f.schedule.ID, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), f.r.ID, now())
		}},
		{"reused ancestor trigger", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.exec(`UPDATE runs SET trigger_message_id=? WHERE id=?`, f.root.TriggerMessageID, f.r.ID)
			f.r.TriggerMessageID = f.root.TriggerMessageID
		}},
		{"explicit uncertain retry", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			child := f.delegate(f.colleague, false)
			f.setStatus(child.ID, "failed", "synthetic uncertain result")
			retry, err := f.s.store.RetryRun(child.ID)
			if err != nil {
				f.t.Fatal(err)
			}
			f.target(retry)
		}},
		{"failed ancestor", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.setStatus(f.root.ID, "failed", "synthetic unresolved effect")
		}},
		{"interrupted ancestor", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.setStatus(f.root.ID, "interrupted", "synthetic interrupted effect")
		}},
		{"cancelled ancestor", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.exec(`UPDATE runs SET status='cancelled' WHERE id=?`, f.root.ID)
		}},
		{"ancestry over limit", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			for i := 0; i < maxMCPAuthorizationAncestry; i++ {
				bot := f.colleague
				if f.r.BotID == bot.ID {
					bot = f.bot
				}
				f.delegate(bot, false)
			}
		}},
		{"earlier attachment is a manifest", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			if _, err := f.s.store.AddAttachment(f.c.ID, "synthetic-state.txt", "text/plain", strings.NewReader("required non-text state")); err != nil {
				f.t.Fatal(err)
			}
		}},
		{"truncated tool result is flagged evidence", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) { f.toolEvidence(true, false) }},
		// The scheduled effect fence: an earlier unverified effect fences a
		// later effect, never a read; an earlier unverified observation fences
		// nothing. The proposal read_public is owner-trusted read-only.
		{"uncertain effect does not fence a trusted read", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) { f.toolEvidence(false, true) }},
		{"uncertain effect does not fence a hinted read", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			f.uncertainMCPWrite("write_record")
			f.proposeTool("fetch_record", true)
		}},
		{"uncertain effect fences a later effect", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.uncertainMCPWrite("write_record")
			f.proposeTool("write_record", false)
		}},
		{"uncertain effect fences a different later effect", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.uncertainMCPWrite("write_record")
			f.proposeTool("send_notice", false)
		}},
		{"unknown tool with uncertain outcome fences a later effect", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.toolEvidence(false, true)
			f.proposeTool("write_record", false)
		}},
		{"uncertain browser navigate does not fence a later effect", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			f.uncertainBrowserNavigate()
			f.proposeTool("write_record", false)
		}},
		{"uncertain browser click fences a later effect", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.uncertainStep("synthetic-click", "computer_browser", `{"action":"browser.click","click":"Send","effect":"none"}`)
			f.proposeTool("write_record", false)
		}},
		{"malformed tool outcome", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.toolEvidence(false, false)
			f.exec(`UPDATE tool_activities SET outcome_json='{' WHERE run_id=?`, f.r.ID)
		}},
		{"tool conversation mismatch", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.toolEvidence(false, false)
			f.exec(`UPDATE tool_activities SET conversation_id=? WHERE run_id=?`, f.colleague.DMConversationID, f.r.ID)
		}},
		{"tool bot mismatch", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.toolEvidence(false, false)
			f.exec(`UPDATE tool_activities SET bot_id=? WHERE run_id=?`, f.colleague.ID, f.r.ID)
		}},
		{"later oversized message is outside the window", false, scheduleSourceChat, true, func(f *scheduledMCPFixture) {
			if _, err := f.s.store.AddAssistant(f.c.ID, f.r.BotID, f.r.ID, strings.Repeat("x", 70<<10)); err != nil {
				f.t.Fatal(err)
			}
		}},
		{"imported root", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.exec(`INSERT INTO portability_provenance(kind,target_id,source_json) VALUES('run',?,'{"native":true}')`, f.root.ID)
		}},
		{"malformed return result", false, scheduleSourceChat, false, func(f *scheduledMCPFixture) {
			f.delegate(f.colleague, false)
			f.finishReturn()
			f.exec(`UPDATE messages SET sender_bot_id=? WHERE id=?`, f.bot.ID, f.r.TriggerMessageID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newScheduledMCPFixture(t, tc.group, tc.source)
			if tc.setup != nil {
				tc.setup(f)
			}
			x, digest, err := f.s.readMCPReviewContextFor(f.s.store.db, f.c, f.r, f.s.mcpProposalFence(f.call))
			if tc.valid {
				if err != nil || digest == "" || x.ScheduleLineage == nil || x.ScheduleLineage.Occurrence.RootRunID != f.root.ID {
					t.Fatalf("valid lineage: %+v %q %v", x.ScheduleLineage, digest, err)
				}
				for _, ref := range x.ScheduleLineage.Authorization {
					if ref.ScheduleSource != nil && ref.ScheduleSource.SourceKind == scheduleSourceForm && ref.MessageID != "" {
						t.Fatal("form request ID leaked into message reference")
					}
				}
				if tc.source == scheduleSourceChat && !strings.Contains(x.Intent, "never send") {
					t.Fatal("original source constraint was replaced by generated task")
				}
			} else if err == nil {
				t.Fatal("unsupported lineage was eligible")
			}
			if strings.Contains(tc.name, "fences a") {
				if diagnostic := mcpContextDiagnostic(err); diagnostic.Code != mcpContextToolsUncertain {
					t.Fatalf("fence code: %+v", diagnostic)
				}
			}
			q := f.question()
			reviewErr := f.review(q)
			want := int32(0)
			if tc.valid {
				want = 1
				if reviewErr != nil {
					t.Fatal(reviewErr)
				}
			}
			if f.p.calls.Load() != want || f.effects.Load() != 0 || f.claimCount() != 0 {
				t.Fatalf("reviewer=%d effects=%d claims=%d", f.p.calls.Load(), f.effects.Load(), f.claimCount())
			}
		})
	}
}

func TestScheduledMCPAllToolReviewModesAndOneUse(t *testing.T) {
	for _, tc := range []struct {
		name, mode       string
		reviews, effects int32
	}{{"all tools allow", "auto", 1, 1}, {"shadow", "shadow", 1, 0}, {"off", "off", 0, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newScheduledMCPFixture(t, false, scheduleSourceChat)
			if err := f.s.store.putAutoReviewMode(tc.mode); err != nil {
				t.Fatal(err)
			}
			q := f.question()
			if err := f.review(q); err != nil {
				t.Fatal(err)
			}
			if tc.mode == "shadow" {
				f.s.shadowReviewWG.Wait()
			}
			err := f.claimEffect(q.ID)
			if (err == nil) != (tc.effects == 1) {
				t.Fatalf("claim=%v expected effects=%d", err, tc.effects)
			}
			if err := f.claimEffect(q.ID); err == nil {
				t.Fatal("same decision claimed twice")
			}
			if f.p.calls.Load() != tc.reviews || f.effects.Load() != tc.effects || f.claimCount() != int(tc.effects) {
				t.Fatalf("reviewer=%d effects=%d claims=%d", f.p.calls.Load(), f.effects.Load(), f.claimCount())
			}
		})
	}
}

func TestScheduledMCPGateExecutesSyntheticToolOnce(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	execute := func() error {
		if err := f.s.approveMCPCall(ctx, f.c, f.r, f.call); err != nil {
			return err
		}
		f.effects.Add(1)
		return nil
	}
	// The owner-trusted read repeats on its own fresh card and claim in a
	// scheduled run too; the action-level claim records its first dispatch.
	for i := int32(1); i <= 2; i++ {
		if err := execute(); err != nil || f.effects.Load() != i || f.p.calls.Load() != i {
			t.Fatalf("trusted read %d: %v effects=%d reviewer=%d", i, err, f.effects.Load(), f.p.calls.Load())
		}
	}
	if f.claimCount() != 1 {
		t.Fatalf("read claims=%d", f.claimCount())
	}
	// An effect executes exactly once; its identical repeat is refused before dispatch.
	f.proposeTool("publish_note", false)
	if err := execute(); err != nil {
		t.Fatal(err)
	}
	err := execute()
	if out, ok := tooloutcome.FromError(err); !ok || out.Status != tooloutcome.Denied || out.Code != "approval_already_claimed" || out.Certainty != "not_executed" {
		t.Fatalf("the MCP gate replayed the synthetic effect: %v", err)
	}
	if f.effects.Load() != 3 || f.p.calls.Load() != 3 || f.claimCount() != 2 {
		t.Fatalf("gate effects=%d reviewer=%d claims=%d", f.effects.Load(), f.p.calls.Load(), f.claimCount())
	}
	questions, err := f.s.store.ListQuestions(f.c.ID)
	if err != nil || len(questions) != 3 {
		t.Fatalf("gate decisions: %+v %v", questions, err)
	}
	for _, q := range questions {
		if q.AnsweredBy != autoReviewActor || q.Approval == nil || q.Approval.Review == nil || q.Approval.Review.Status != "approved" {
			t.Fatalf("gate decision: %+v", q)
		}
	}
}

func scheduledMCPMutation(f *scheduledMCPFixture, name string) {
	f.t.Helper()
	var err error
	switch name {
	case "content edit":
		value := "Read only synthetic beta."
		_, err = f.s.store.patchScheduleWithSource(f.schedule.ID, SchedulePatch{Content: &value}, f.s.scheduleFormSource("content_edit", map[string]string{"content": value}))
	case "pause":
		_, err = f.s.store.setScheduleStatusWithSource(f.schedule.ID, schedulePaused, f.s.scheduleFormSource("pause", map[string]string{"status": schedulePaused}))
	case "delete":
		err = f.s.store.deleteScheduleWithSource(f.schedule.ID, f.s.scheduleFormSource("delete", map[string]string{"id": f.schedule.ID}))
	case "archive":
		f.setStatus(f.r.ID, "done", "")
		_, err = f.s.store.SetBotArchived(f.bot.ID, true)
	case "source membership":
		f.exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, f.source.ConversationID, f.source.BotID)
	case "target membership":
		f.exec(`DELETE FROM members WHERE conversation_id=? AND bot_id=?`, f.r.ConversationID, f.r.BotID)
	case "Stop":
		err = f.s.store.CancelRunTree(f.r.ID)
	case "source import marker":
		f.exec(`INSERT INTO portability_provenance(kind,target_id,source_json) VALUES('message',?,'{"source":"native_chat"}')`, f.source.TriggerMessageID)
	case "source reclassified":
		f.exec(`UPDATE messages SET kind='scheduled_task' WHERE id=?`, f.source.TriggerMessageID)
	case "source text changed":
		f.exec(`UPDATE messages SET content='Synthetic changed intent.' WHERE id=?`, f.source.TriggerMessageID)
	case "source run import":
		f.exec(`INSERT INTO portability_provenance(kind,target_id,source_json) VALUES('run',?,'{}')`, f.source.ID)
	case "pause resume":
		_, err = f.s.store.setScheduleStatusWithSource(f.schedule.ID, schedulePaused, f.s.scheduleFormSource("pause", map[string]string{"status": schedulePaused}))
		if err == nil {
			_, err = f.s.store.setScheduleStatusWithSource(f.schedule.ID, scheduleActive, f.s.scheduleFormSource("resume", map[string]string{"status": scheduleActive}))
		}
	default:
		f.t.Fatalf("unknown mutation %q", name)
	}
	if err != nil {
		f.t.Fatal(err)
	}
}

func TestScheduledMCPRevisionAndProvenanceRecheck(t *testing.T) {
	for _, phase := range []string{"completion", "claim"} {
		for _, mutation := range []string{"content edit", "pause", "delete", "archive", "source membership", "target membership", "Stop", "source import marker", "source reclassified", "source text changed", "source run import", "pause resume"} {
			t.Run(phase+"/"+mutation, func(t *testing.T) {
				f := newScheduledMCPFixture(t, false, scheduleSourceChat)
				if mutation == "target membership" {
					f.delegate(f.colleague, false)
				}
				q := f.question()
				if phase == "completion" {
					f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
						scheduledMCPMutation(f, mutation)
						return reviewReply(req, "allow"), nil
					}
					_ = f.review(q)
				} else {
					if err := f.review(q); err != nil {
						t.Fatal(err)
					}
					scheduledMCPMutation(f, mutation)
				}
				if err := f.claimEffect(q.ID); err == nil {
					t.Fatal("stale automatic decision executed")
				}
				q, err := f.s.store.GetQuestion(q.ID)
				if err != nil || q.AnsweredBy == autoReviewActor || string(q.Answer) == "true" || f.effects.Load() != 0 || f.claimCount() != 0 || f.p.calls.Load() != 1 {
					t.Fatalf("stale decision q=%+v err=%v reviewer=%d effects=%d claims=%d", q, err, f.p.calls.Load(), f.effects.Load(), f.claimCount())
				}
				f.assertQuestionEvent(q.ID)
				if mutation == "pause resume" {
					f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
						return reviewReply(req, "allow"), nil
					}
					f.setStatus(f.root.ID, "done", "")
					makeDue(t, f.s.store, f.schedule.ID, time.Now().Add(-time.Minute))
					runs, err := f.s.store.ClaimDueSchedules(time.Now())
					if err != nil || len(runs) != 1 {
						t.Fatal(runs, err)
					}
					f.root = runs[0]
					f.target(f.root)
					fresh := f.approvedQuestion()
					if err := f.claimEffect(fresh.ID); err != nil {
						t.Fatal(err)
					}
					if f.effects.Load() != 1 || f.p.calls.Load() != 2 {
						t.Fatal("fresh native resume revision did not authorize a distinct occurrence")
					}
					if err := f.claimEffect(q.ID); err == nil {
						t.Fatal("new occurrence revived the old decision")
					}
				}
			})
		}
	}
}

func TestScheduledMCPClaimOrderingAndExistingFences(t *testing.T) {
	for _, order := range []string{"revocation first", "claim first", "concurrent transactions"} {
		t.Run(order, func(t *testing.T) {
			f := newScheduledMCPFixture(t, false, scheduleSourceChat)
			q := f.approvedQuestion()
			var claimErr, pauseErr error
			pause := func() {
				_, pauseErr = f.s.store.setScheduleStatusWithSource(f.schedule.ID, schedulePaused, f.s.scheduleFormSource("pause", map[string]string{"status": schedulePaused}))
			}
			switch order {
			case "revocation first":
				pause()
				claimErr = f.claimEffect(q.ID)
			case "claim first":
				claimErr = f.claimEffect(q.ID)
				pause()
			case "concurrent transactions":
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); <-start; claimErr = f.claimEffect(q.ID) }()
				go func() { defer wg.Done(); <-start; pause() }()
				close(start)
				wg.Wait()
			}
			if pauseErr != nil {
				t.Fatal(pauseErr)
			}
			want := int32(0)
			if claimErr == nil {
				want = 1
			}
			if order == "revocation first" && want != 0 || order == "claim first" && want != 1 {
				t.Fatalf("transaction order violated: %v", claimErr)
			}
			if f.effects.Load() != want || f.claimCount() != int(want) {
				t.Fatalf("effects=%d claims=%d", f.effects.Load(), f.claimCount())
			}
			if err := f.claimEffect(q.ID); err == nil {
				t.Fatal("revocation or an existing claim permitted replay")
			}
			if f.effects.Load() != want || f.p.calls.Load() != 1 {
				t.Fatal("claim ordering repeated reviewer or effect")
			}
		})
	}
	for _, fence := range []string{"human allow", "human deny", "expiry", "settings epoch", "restart unclaimed", "restart claimed", "restart interrupted review"} {
		t.Run(fence, func(t *testing.T) {
			f := newScheduledMCPFixture(t, false, scheduleSourceChat)
			q := f.question()
			if strings.HasPrefix(fence, "restart") {
				if err := f.review(q); err != nil {
					t.Fatal(err)
				}
				if fence == "restart claimed" {
					if err := f.claimEffect(q.ID); err != nil {
						t.Fatal(err)
					}
				}
				if fence == "restart interrupted review" {
					f.exec(`UPDATE mcp_auto_reviews SET status='reviewing' WHERE question_id=?`, q.ID)
					f.exec(`UPDATE questions SET status='pending',answer_json=NULL,answered_by=NULL WHERE id=?`, q.ID)
				}
				f.setStatus(f.r.ID, runWaiting, "")
				if err := f.s.store.Close(); err != nil {
					t.Fatal(err)
				}
				store, err := OpenStore(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				f.s.store = store
				f.exec(`UPDATE runs SET status='queued' WHERE id=?`, f.r.ID)
				f.setStatus(f.r.ID, "running", "")
				f.target(f.r)
			} else {
				f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
					switch fence {
					case "human allow", "human deny":
						if _, _, err := f.s.store.AnswerQuestion(q.ID, "synthetic-human", fence == "human allow"); err != nil {
							t.Fatal(err)
						}
					case "expiry":
						f.exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), q.ID)
					case "settings epoch":
						if err := f.s.store.putAutoReviewMode("shadow"); err != nil {
							t.Fatal(err)
						}
						if err := f.s.store.putAutoReviewMode("auto"); err != nil {
							t.Fatal(err)
						}
					}
					return reviewReply(req, "allow"), nil
				}
				_ = f.review(q)
			}
			err := f.claimEffect(q.ID)
			allowed := fence == "human allow" || fence == "restart unclaimed"
			if (err == nil) != allowed {
				t.Fatalf("fence=%s claim=%v", fence, err)
			}
			want := int32(0)
			if allowed || fence == "restart claimed" {
				want = 1
			}
			if f.effects.Load() != want || f.claimCount() != int(want) || f.p.calls.Load() != 1 {
				t.Fatalf("fence=%s reviewer=%d effects=%d claims=%d", fence, f.p.calls.Load(), f.effects.Load(), f.claimCount())
			}
			if strings.HasPrefix(fence, "human") {
				current, err := f.s.store.GetQuestion(q.ID)
				if err != nil || current.AnsweredBy != "synthetic-human" {
					t.Fatal("automatic review replaced the human decision", err)
				}
			}
			if err := f.claimEffect(q.ID); err == nil {
				t.Fatal("existing fence permitted a second effect")
			}
		})
	}
}
