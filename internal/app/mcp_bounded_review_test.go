package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// A fresh native request after a long, heavy history: 300 earlier messages,
// large tool results of this run, many refusals and memories.
func seedLongMCPConversation(t *testing.T, f *autoReviewFixture) {
	t.Helper()
	diagnosticExec(t, f, `UPDATE runs SET status='done' WHERE id=?`, f.r.ID)
	diagnosticExec(t, f, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<300)
INSERT INTO messages(id,conversation_id,seq,role,content,created_at) SELECT printf('synthetic-long-%03d',i),?,1000+i,CASE i%2 WHEN 0 THEN 'assistant' ELSE 'user' END,printf('%d %s',i,?),? FROM n`, f.c.ID, strings.Repeat("长", 5000), now())
	diagnosticExec(t, f, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<300)
INSERT INTO memories(id,conversation_id,content,revision,created_at,updated_at) SELECT printf('synthetic-memory-%d',i),?,?,1,?,? FROM n`, f.c.ID, strings.Repeat("m", 2000), now(), now())
	_, run, _, err := f.s.store.AddUserRun(f.c.ID, f.r.BotID, "Read the synthetic public fact for alpha.", "synthetic-long-request")
	if err != nil {
		t.Fatal(err)
	}
	diagnosticExec(t, f, `UPDATE runs SET status='running' WHERE id=?`, run.ID)
	run.Status = "running"
	f.r = run
	diagnosticTools(t, f, 120, strings.Repeat("r", 32000))
	diagnosticRefusals(t, f, 30, `{"action":"synthetic","target":"synthetic","impact":"synthetic","payload":"`+strings.Repeat("p", 20000)+`"}`)
}

func TestMCPBoundedReviewLongConversationIsReviewable(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	seedLongMCPConversation(t, f)
	x, digest, err := readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || digest == "" {
		t.Fatal("long history made the request unreviewable", err)
	}
	raw, _ := json.Marshal(x)
	b := x.Bounds
	if len(raw) > mcpEvidenceBudget || b == nil || !b.OlderMessagesOmitted || len(b.TruncatedMessages) == 0 || !b.OlderToolRecordsOmitted || !b.ToolRecordsTruncated || !b.OlderRefusalsOmitted || !b.RefusalPayloadsTruncated {
		t.Fatalf("bounded packet size=%d flags=%+v", len(raw), b)
	}
	if last := x.Messages[len(x.Messages)-1]; last.ID != f.r.TriggerMessageID || len(x.Messages) > mcpEvidenceMessages || len(x.ToolResults) > mcpEvidenceToolRows || len(x.HumanRefusals) > mcpEvidenceRefusals {
		t.Fatal("window lost its trigger or exceeded its bounds")
	}
	if strings.Contains(string(raw), `"memories"`) || strings.Contains(string(raw), `conversation_summary`) {
		t.Fatal("volatile memory or summary entered the packet")
	}
	if sources := mcpAuthorizationSources(x); len(sources) == 0 || sources[len(sources)-1].MessageID != f.r.TriggerMessageID {
		t.Fatal("the trigger lost its authorization reference")
	}
	var input int
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		input = len(req.Messages[0].Content)
		if !strings.Contains(req.System, "host_evidence_bounds") || !strings.Contains(req.Messages[0].Content, `"older_messages_omitted":true`) {
			t.Error("reviewer was not told the packet is bounded")
		}
		return reviewReply(req, "allow"), nil
	}
	q := createReviewedUnclaimed(t, f)
	result, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := result.RowsAffected(); n != 1 || f.p.calls.Load() != 1 || input == 0 || input > 100<<10 {
		t.Fatalf("claim=%d reviewer=%d input=%d", n, f.p.calls.Load(), input)
	}
}

// Writes that cannot change authorization land while the reviewer is working.
func TestMCPBoundedReviewConcurrentWritesStillApprove(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	other, err := f.s.store.AddRun(f.c.ID, f.r.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	f.p.reply = func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		if _, _, err := f.s.store.AddMessage(f.c.ID, "assistant", f.r.BotID, f.r.ID, "Synthetic progress while reviewing.", ""); err != nil {
			t.Error(err)
		}
		diagnosticExec(t, f, `UPDATE messages SET kind='progress' WHERE run_id=? AND role='assistant'`, f.r.ID)
		diagnosticExec(t, f, `INSERT INTO summaries(conversation_id,version,covered_seq,content,created_at) VALUES(?,99,1,'Synthetic summary.',?)`, f.c.ID, now())
		diagnosticExec(t, f, `INSERT INTO memories(id,conversation_id,content,revision,created_at,updated_at) VALUES('synthetic-new-memory',?,'Synthetic memory.',1,?,?)`, f.c.ID, now(), now())
		diagnosticExec(t, f, `INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at,outcome_json) VALUES(?,?,?,'other','synthetic','{}','Synthetic.','completed',?,?,'')`, f.c.ID, f.r.BotID, other.ID, now(), now())
		diagnosticExec(t, f, `INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at,outcome_json) VALUES(?,?,?,'lookup','search_mcp_tools','{}','{}','completed',?,?,'')`, f.c.ID, f.r.BotID, f.r.ID, now(), now())
		return reviewReply(req, "allow"), nil
	}
	q := createReviewedUnclaimed(t, f)
	if q.Approval.Review == nil || q.Approval.Review.Status != "approved" {
		t.Fatal("concurrent non-authorizing writes invalidated review", q.Approval.Review)
	}
	result, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID)
	if err != nil {
		t.Fatal("concurrent non-authorizing writes invalidated claim", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		t.Fatal("claim did not execute once")
	}
	// A human message after the trigger is a restriction and stays bound.
	g := newProvenanceReviewFixture(t)
	q = createReviewedUnclaimed(t, g)
	later, _, err := g.s.store.AddMessage(g.c.ID, "user", "", "", "Stop; do not read alpha.", "synthetic-later-human")
	if err != nil {
		t.Fatal(err)
	}
	diagnosticExec(t, g, `INSERT INTO user_message_ingress(message_id,created_at) VALUES(?,?)`, later.ID, now())
	if _, err := g.s.claimMCPApproval(context.Background(), g.c, g.r, g.call, q.ID); err == nil {
		t.Fatal("a later human restriction did not invalidate the decision")
	}
}

func TestMCPBoundedReviewGroupChatRunsAreReviewable(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	owner := f.r.BotID
	colleague, err := f.s.store.CreateBot("synthetic group colleague", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.s.store.CreateGroup("synthetic group", []string{f.r.BotID, colleague.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := f.s.store.AddUserRuns(g.ID, "Read the synthetic public fact for alpha.", "synthetic-group-request", nil)
	if err != nil || len(runs) != 1 || runs[0].Kind != runKindGroupChat {
		t.Fatalf("group round: %+v %v", runs, err)
	}
	diagnosticExec(t, f, `UPDATE runs SET status='running' WHERE id=?`, runs[0].ID)
	f.c, f.r = g, runs[0]
	f.r.Status = "running"
	q := createReviewedUnclaimed(t, f)
	if result, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err != nil {
		t.Fatal(err)
	} else if n, _ := result.RowsAffected(); n != 1 {
		t.Fatal("group round did not claim")
	}
	// An invited member shares the round's verified user trigger.
	invited, err := f.s.store.InviteGroupMembers(f.r.ID, []string{otherGroupBot(owner, colleague.ID, runs[0].BotID)})
	if err != nil || len(invited) != 1 {
		t.Fatalf("invite: %+v %v", invited, err)
	}
	diagnosticExec(t, f, `UPDATE runs SET status='running' WHERE id=?`, invited[0].ID)
	f.r = invited[0]
	f.r.Status = "running"
	x, digest, err := f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || digest == "" || x.IntentMessageID != runs[0].TriggerMessageID || len(mcpAuthorizationSources(x)) == 0 {
		t.Fatal("invited group member was not reviewable", err)
	}
	q = createReviewedUnclaimed(t, f)
	if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err != nil {
		t.Fatal(err)
	}
	// A forged hop that does not share the round's trigger stays a typed gap.
	diagnosticExec(t, f, `UPDATE runs SET trigger_message_id=? WHERE id=?`, "synthetic-other", runs[0].ID)
	_, _, err = f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	assertContextDiagnostic(t, err, mcpContextGroupRound)
}

func otherGroupBot(a, b, used string) string {
	if used == a {
		return b
	}
	return a
}

func TestMCPBoundedReviewDelegatedChatChild(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	colleague, err := f.s.store.CreateBot("synthetic delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	root := f.r
	_, children, err := f.s.store.AddBotMessages(f.c.ID, root.BotID, []string{colleague.ID}, root.ID, "Read only the synthetic public fact for the requester.")
	if err != nil || len(children) != 1 {
		t.Fatalf("delegate: %+v %v", children, err)
	}
	child := children[0]
	if ok, err := f.s.store.SetRunStatus(child.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if child, err = f.s.store.GetRun(child.ID); err != nil {
		t.Fatal(err)
	}
	conversation, err := f.s.store.GetConversation(child.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	f.c, f.r = conversation, child
	x, digest, err := f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || digest == "" || x.Delegation == nil || x.Delegation.RootRunID != root.ID || x.IntentMessageID != root.TriggerMessageID {
		t.Fatal("delegated child of a native chat request was not reviewable", err)
	}
	if sources := mcpAuthorizationSources(x); len(sources) == 0 || sources[len(sources)-1].MessageID != root.TriggerMessageID {
		t.Fatal("delegated authorization did not come from the root request")
	}
	q := createReviewedUnclaimed(t, f)
	if _, err := f.s.claimMCPApproval(context.Background(), f.c, f.r, f.call, q.ID); err != nil {
		t.Fatal(err)
	}
	// A root that is not a human request or schedule keeps a typed gap.
	diagnosticExec(t, f, `UPDATE runs SET kind=? WHERE id=?`, runKindTeam, root.ID)
	_, _, err = f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	assertContextDiagnostic(t, err, mcpContextDelegationRoot)
}

func TestScheduledMCPReviewWithOldAttachmentAndLongSource(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	// An upload bound to the earlier source request, long before the occurrence.
	f.exec(`INSERT INTO attachments VALUES('synthetic-old-upload',?,'synthetic-old.pdf','application/pdf',9,'synthetic-old-upload','2000-01-01T00:00:00Z')`, f.c.ID)
	if err := f.s.store.BindAttachments(f.c.ID, f.source.TriggerMessageID, []string{"synthetic-old-upload"}); err != nil {
		t.Fatal(err)
	}
	f.toolEvidence(true, false)
	x, digest, err := f.s.readMCPReviewContext(f.s.store.db, f.c, f.r)
	if err != nil || digest == "" || x.ScheduleLineage == nil {
		t.Fatal("old attachment blocked the scheduled occurrence", err)
	}
	boundary := x.ScheduleLineage.Contexts[0].Context.AttachmentBoundary
	if boundary == nil || len(boundary.Omissions) != 1 || boundary.Omissions[0].ID != "synthetic-old-upload" || !boundary.Omissions[0].ContentNotProvided {
		t.Fatalf("old attachment was not an explicit manifest: %+v", boundary)
	}
	q := f.approvedQuestion()
	if err := f.claimEffect(q.ID); err != nil {
		t.Fatal(err)
	}
	if f.effects.Load() != 1 || f.p.calls.Load() != 1 {
		t.Fatal("scheduled review with history did not execute once")
	}
}

// A long source request is captured as a bounded prefix plus its digest and
// still verifies against the durable message at occurrence time.
func TestScheduleChatSourceLongHistoryKeepsNativeLineage(t *testing.T) {
	dir := t.TempDir()
	store, bot, c := scheduleTestStoreAt(t, dir)
	s := &Server{store: store, accountID: "synthetic-long-source", closing: true}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<300)
INSERT INTO messages(id,conversation_id,seq,role,content,created_at) SELECT printf('synthetic-history-%03d',i),?,1000+i,'assistant',?,? FROM n`, c.ID, strings.Repeat("h", 4000), now()); err != nil {
		t.Fatal(err)
	}
	intent := "Every hour read synthetic alpha; never send it elsewhere. " + strings.Repeat("约束", 4000)
	_, source, _, err := store.AddUserRun(c.ID, bot.ID, intent, "synthetic-long-source")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.SetRunStatus(source.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	source, _ = store.GetRun(source.ID)
	spec := ScheduleSpec{Content: "Read synthetic alpha.", Kind: scheduleInterval, IntervalSeconds: 3600, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano), Timezone: "UTC"}
	schedule, err := store.createScheduleWithSource(c.ID, bot.ID, spec, s.scheduleChatSource(c, source))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := readScheduleAuthorizationRevision(store.db, schedule.ID, 1)
	if err != nil || revision.SourceKind != scheduleSourceChat {
		t.Fatalf("long history degraded the source to unknown: %+v %v", revision.SourceKind, err)
	}
	var saved mcpReviewContext
	if err := json.Unmarshal(revision.SourceContext, &saved); err != nil || saved.Bounds == nil || saved.Bounds.Intent == nil || !saved.Bounds.OlderMessagesOmitted {
		t.Fatalf("source bounds missing: %+v %v", saved.Bounds, err)
	}
	if ok, err := store.SetRunStatus(source.ID, "done", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	makeDue(t, store, schedule.ID, time.Now().Add(-time.Minute))
	runs, err := store.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatal(runs, err)
	}
	if ok, err := store.SetRunStatus(runs[0].ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	r, _ := store.GetRun(runs[0].ID)
	x, digest, err := s.readMCPReviewContext(store.db, c, r)
	if err != nil || digest == "" || !strings.Contains(x.Intent, "never send it elsewhere") {
		t.Fatal("bounded source did not verify at occurrence", err)
	}
	// The digest still binds the full durable request beyond the shown prefix.
	if _, err := store.db.Exec(`UPDATE messages SET content=? WHERE id=?`, intent+"!", source.TriggerMessageID); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.readMCPReviewContext(store.db, c, r)
	assertContextDiagnostic(t, err, mcpContextSourceProvenance)
}

func TestMCPContextRequiredTellsModelToStopRetrying(t *testing.T) {
	f := newProvenanceReviewFixture(t)
	q := newMCPReviewProposal(t, f)
	diagnosticExec(t, f, `INSERT INTO attachments VALUES('synthetic-current',?,'synthetic','text/plain',1,'synthetic-current',?)`, f.c.ID, now())
	if err := f.s.store.BindAttachments(f.c.ID, f.r.TriggerMessageID, []string{"synthetic-current"}); err != nil {
		t.Fatal(err)
	}
	err := f.s.reviewNewMCPProposal(context.Background(), f.c, f.r, f.call, q)
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Code != "mcp_review_context_missing" || out.Status != tooloutcome.Permanent || out.NextAction != "replan" || !strings.Contains(out.Message, string(mcpContextNonText)) || !strings.Contains(out.Message, "attachments the reviewer cannot read") || !strings.Contains(out.Message, "Do not retry") {
		t.Fatalf("model-facing closure lacks its diagnostic: %+v", out)
	}
}
