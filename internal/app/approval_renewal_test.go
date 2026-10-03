package app

import (
	"context"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExpiredApprovalRestartRenewAndDuplicateResume(t *testing.T) {
	var effects atomic.Int32
	remote := newAppMCPFixture(t, "fixture", newAppTextTool("write", "Synthetic write", "target"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		effects.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "synthetic write executed"}}}, nil
	})
	model := newScheduledApprovalModel(t, "approve", &effects)
	defer model.Close()
	engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: model.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	config := Config{DataDir: dir, Provider: "test", Engine: engine, MCPConfigPath: filepath.Join(dir, "mcp.json")}
	s, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	if err = s.extensions.SaveMCP("fixture", extensions.MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	b, _ := s.store.CreateBot("renew fixture", "", "test-model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	schedule, err := s.store.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "Synthetic write after exact human approval", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s.store, schedule.ID, time.Now().Add(-time.Minute))
	s.scheduler.tick()
	deadline := time.Now().Add(5 * time.Second)
	var r Run
	for {
		schedule, _ = s.store.GetSchedule(schedule.ID)
		if schedule.LastRunID != "" {
			r, _ = s.store.GetRun(schedule.LastRunID)
			if r.Status == runWaiting {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("never parked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	questions, _ := s.store.ListQuestions(c.ID)
	old := questions[0]
	if effects.Load() != 0 || old.Card().Outcome.Status != "need_approval" {
		t.Fatal("approval wait not safe/visible")
	}
	_, err = s.store.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.refreshInputWaits(c.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, s, r.ID, runWaiting)
	expired, _ := s.store.GetQuestion(old.ID)
	if expired.Card().Outcome.Status != "approval_expired" || expired.Card().Outcome.NextAction != "renew_approval" {
		t.Fatalf("expired=%+v", expired.Card())
	}
	if response := answerOtherQuestionHTTP(s, old.ID, `{"value":true}`); response.Code != http.StatusConflict {
		t.Fatal("late old approval accepted")
	}
	fresh, err := s.store.RenewExpiredApproval(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.store.RenewExpiredApproval(old.ID)
	if err != nil || duplicate.ID != fresh.ID || fresh.ID == old.ID || effects.Load() != 0 {
		t.Fatalf("renew replay: %+v %v effects=%d", duplicate, err, effects.Load())
	}
	if fresh.Approval.Payload != old.Approval.Payload {
		t.Fatal("renewal changed scope")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, s, r.ID, runWaiting)
	for range 2 {
		if response := answerOtherQuestionHTTP(s, fresh.ID, `{"value":true}`); response.Code != http.StatusOK {
			t.Fatalf("answer HTTP=%d %s", response.Code, response.Body.String())
		}
	}
	waitForRunStatus(t, s, r.ID, "done")
	if effects.Load() != 1 {
		t.Fatalf("duplicate effect count=%d", effects.Load())
	}
	if response := answerOtherQuestionHTTP(s, old.ID, `{"value":true}`); response.Code != http.StatusConflict {
		t.Fatal("old ID resurrected")
	}
	var checkpointCount int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM run_input_waits WHERE run_id=?`, r.ID).Scan(&checkpointCount)
	if checkpointCount != 0 {
		t.Fatal("terminal checkpoint retained")
	}
}

func TestApprovalAndInformationOutcomeClassification(t *testing.T) {
	for _, tc := range []struct{ kind, status, want string }{{questionApproval, questionPending, "need_approval"}, {questionText, questionPending, "need_information"}, {questionApproval, questionExpired, "approval_expired"}} {
		q := Question{Type: tc.kind, Status: tc.status, Resumable: true}
		b, _ := json.Marshal(q.Card())
		if !strings.Contains(string(b), `"status":"`+tc.want+`"`) || !strings.Contains(string(b), `"execution_certainty":"not_executed"`) {
			t.Fatal(string(b))
		}
	}
}

func TestApprovalExpiresAfterAnswerBeforeDispatchAndReparksConsumedWait(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Allow synthetic write?", Type: questionApproval, Approval: &ApprovalDetails{Action: "write", Target: "fixture", Impact: "synthetic only"}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, r, in)
	if err != nil {
		t.Fatal(err)
	}
	call := extensions.MCPCallApproval{Server: "fixture", Tool: "write", ConfigVersion: "v1", Arguments: json.RawMessage(`{"target":"synthetic"}`)}
	if _, err = s.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) VALUES(?,?,?)`, q.ID, r.ID, mcpApprovalHash(call)); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{"first":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AnswerQuestion(q.ID, "human", true); err != nil {
		t.Fatal(err)
	}
	if err = s.refreshInputWaits(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.claimInputContinuation(context.Background(), r.ID); err != nil || !ok {
		t.Fatalf("claim=%v %v", ok, err)
	}
	if _, err = s.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), q.ID); err != nil {
		t.Fatal(err)
	}
	err = (&Server{store: s}).approveMCPCall(context.Background(), c, r, call)
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Status != tooloutcome.Expired || o.Certainty != "not_executed" {
		t.Fatalf("late dispatch allowed: %v %+v", err, o)
	}
	var claimed string
	if err = s.db.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&claimed); err != nil || claimed != "" {
		t.Fatalf("claimed=%q %v", claimed, err)
	}
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{"expired":true}`)); err != nil {
		t.Fatalf("cannot repark: %v", err)
	}
	run, _ := s.GetRun(r.ID)
	expired, _ := s.GetQuestion(q.ID)
	if run.Status != runWaiting || expired.Card().Outcome.NextAction != "renew_approval" {
		t.Fatalf("lost expired wait: %+v %+v", run, expired)
	}
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{}`)); err == nil {
		t.Fatal("unconsumed expired checkpoint overwritten")
	}
}
