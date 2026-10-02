package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type mcpApprovalEngine struct{}

func (mcpApprovalEngine) Run(ctx context.Context, request runtime.Request) (runtime.Result, error) {
	var search, call runtime.Tool
	for _, tool := range request.Tools {
		switch tool.Name {
		case "search_mcp_tools":
			search = tool
		case "call_mcp_tool":
			call = tool
		}
	}
	if _, err := search.Execute(ctx, json.RawMessage(`{"server":"fixture","query":"write"}`)); err != nil {
		return runtime.Result{}, err
	}
	result, err := call.Execute(ctx, json.RawMessage(`{"name":"mcp_fixture__write","arguments":{"target":"synthetic"}}`))
	return runtime.Result{Content: result}, err
}

func TestMCPApprovalAppRunWaitsBeforeRemoteWrite(t *testing.T) {
	var remoteCalls atomic.Int32
	remote := newAppMCPFixture(t, "fixture", newAppTextTool("write", "Synthetic external write", "target"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
	})
	dir := t.TempDir()
	server, err := NewServer(Config{DataDir: dir, Engine: mcpApprovalEngine{}, MCPConfigPath: filepath.Join(dir, "mcp.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.extensions.SaveMCP("fixture", extensions.MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	bot, err := server.store.CreateBot("synthetic approval", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := server.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := server.store.AddRun(conversation.ID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { server.execute(conversation, run); close(done) }()
	deadline := time.After(5 * time.Second)
	var question Question
	for {
		pending, err := server.store.PendingQuestions(conversation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 {
			question = pending[0]
			break
		}
		select {
		case <-deadline:
			current, _ := server.store.GetRun(run.ID)
			t.Fatalf("app run did not request MCP approval: %+v", current)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if remoteCalls.Load() != 0 || question.Type != questionApproval {
		t.Fatalf("remote call before approval: calls=%d question=%+v", remoteCalls.Load(), question)
	}
	if _, _, err := server.store.AnswerQuestion(question.ID, "human", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("app run did not resume after approval")
	}
	if remoteCalls.Load() != 1 {
		t.Fatalf("approved remote calls=%d", remoteCalls.Load())
	}
}

func TestMCPApprovalExactOneUseAndDenial(t *testing.T) {
	store, conversation, run := questionFixture(t)
	defer store.Close()
	server := &Server{store: store}
	call := extensions.MCPCallApproval{Server: "fixture", Tool: "publish", ConfigVersion: "v1", Arguments: json.RawMessage(`{"target":"draft","text":"hello"}`)}
	done := make(chan error, 1)
	go func() { done <- server.approveMCPCall(context.Background(), conversation, run, call) }()
	var question Question
	deadline := time.After(3 * time.Second)
	for {
		pending, err := store.PendingQuestions(conversation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 {
			question = pending[0]
			break
		}
		select {
		case <-deadline:
			t.Fatal("approval question did not appear")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if question.Type != questionApproval || question.Approval == nil || !strings.Contains(question.Approval.Payload, `"target":"draft"`) {
		t.Fatalf("approval card = %+v", question)
	}
	if _, _, err := store.AnswerQuestion(question.ID, "human", true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := server.approveMCPCall(context.Background(), conversation, run, call); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("duplicate call = %v", err)
	}
	changed := call
	changed.Arguments = json.RawMessage(`{"target":"different","text":"hello"}`)
	if hash := mcpApprovalHash(changed); hash == mcpApprovalHash(call) {
		t.Fatal("changed arguments retained approval hash")
	}
	changedConfig := call
	changedConfig.ConfigVersion = "v2"
	if mcpApprovalHash(changedConfig) == mcpApprovalHash(call) {
		t.Fatal("changed server configuration retained approval hash")
	}
	changedDone := make(chan error, 1)
	go func() { changedDone <- server.approveMCPCall(context.Background(), conversation, run, changed) }()
	var changedQuestion Question
	deadline = time.After(3 * time.Second)
	for {
		pending, err := store.PendingQuestions(conversation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 {
			changedQuestion = pending[0]
			break
		}
		select {
		case <-deadline:
			t.Fatal("changed arguments did not request fresh approval")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if changedQuestion.ID == question.ID || changedQuestion.Approval == nil || !strings.Contains(changedQuestion.Approval.Payload, `"target":"different"`) {
		t.Fatalf("changed approval card = %+v", changedQuestion)
	}
	if _, _, err := store.AnswerQuestion(changedQuestion.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	if err := <-changedDone; err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("denied call = %v", err)
	}
	var claimed string
	if err := store.db.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, changedQuestion.ID).Scan(&claimed); err != nil || claimed != "" {
		t.Fatalf("denied call claimed=%q err=%v", claimed, err)
	}
}

func TestMCPApprovalRestoresAnsweredDecisionWithoutReasking(t *testing.T) {
	dir := t.TempDir()
	store, conversation, run := questionFixtureDir(t, dir)
	call := extensions.MCPCallApproval{Server: "fixture", Tool: "write", ConfigVersion: "v1", Arguments: json.RawMessage(`{"value":"ok"}`)}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Allow?", Type: questionApproval, Approval: &ApprovalDetails{Action: "write", Target: "fixture", Impact: "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := store.CreateQuestion(conversation.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) VALUES(?,?,?)`, q.ID, run.ID, mcpApprovalHash(call)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AnswerQuestion(q.ID, "human", true); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.SetRunStatus(run.ID, "waiting", ""); err != nil || !changed {
		t.Fatalf("park run for restart: changed=%v err=%v", changed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.db.Exec(`UPDATE runs SET status='queued' WHERE id=? AND status='waiting'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if changed, err := reopened.SetRunStatus(run.ID, "running", ""); err != nil || !changed {
		t.Fatalf("resume run after restart: changed=%v err=%v", changed, err)
	}
	if err := (&Server{store: reopened}).approveMCPCall(context.Background(), conversation, run, call); err != nil {
		t.Fatalf("restored approval: %v", err)
	}
	var count int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_approvals WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("approval count=%d err=%v", count, err)
	}
	if err := (&Server{store: reopened}).approveMCPCall(context.Background(), conversation, run, call); err == nil {
		t.Fatal("claimed approval was replayed")
	}
}

func TestMCPApprovalRejectsPrivateAndOversizedArguments(t *testing.T) {
	for _, raw := range []string{`{"api_key":"private"}`, `{"nested":{"password":"private"}}`, `{"nested":[{"access-token":"private"}]}`, `{"text":"` + strings.Repeat("a", maxMCPApprovalPayloadBytes) + `"}`, `[]`, `null`, `{}` + `{}`} {
		if _, err := mcpApprovalPayload(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted unreviewable payload: %q", raw[:min(len(raw), 40)])
		}
	}
}

func TestMCPApprovalPreservesLongUnicodeMultilinePayload(t *testing.T) {
	raw := json.RawMessage(`{"nested":{"note":"第一行\\n第二行 👀 <img src=x onerror=alert(1)>"},"text":"` + strings.Repeat("长", 2500) + `"}`)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	raw = pretty.Bytes()
	payload, err := mcpApprovalPayload(raw)
	if err != nil || payload != string(raw) {
		t.Fatalf("long payload was changed: bytes=%d err=%v", len(raw), err)
	}
	dir := t.TempDir()
	store, conversation, run := questionFixtureDir(t, dir)
	call := extensions.MCPCallApproval{Server: "fixture", Tool: "write", ConfigVersion: "reviewed-config", Arguments: raw}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (&Server{store: store}).approveMCPCall(ctx, conversation, run, call) }()
	deadline := time.After(3 * time.Second)
	var q Question
	for {
		pending, err := store.PendingQuestions(conversation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 {
			q = pending[0]
			break
		}
		select {
		case <-deadline:
			t.Fatal("long argument approval did not appear")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if q.Approval == nil || q.Approval.Payload != payload || strings.Contains(q.Approval.Impact, "<img") {
		t.Fatal("long payload was truncated or placed in summary")
	}
	if _, _, err := store.AnswerQuestion(q.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("denied long payload: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reloaded, err := reopened.GetQuestion(q.ID)
	if err != nil || reloaded.Approval == nil || reloaded.Approval.Payload != payload || reloaded.Status != questionAnswered {
		t.Fatalf("long payload was lost after restart: status=%s err=%v", reloaded.Status, err)
	}
}

func TestMCPApprovalPayloadHardLimitIsBounded(t *testing.T) {
	const prefix = `{"text":"`
	const suffix = `"}`
	raw := json.RawMessage(prefix + strings.Repeat("a", maxMCPApprovalPayloadBytes-len(prefix)-len(suffix)) + suffix)
	if len(raw) != maxMCPApprovalPayloadBytes {
		t.Fatalf("fixture bytes=%d", len(raw))
	}
	if payload, err := mcpApprovalPayload(raw); err != nil || len(payload) != maxMCPApprovalPayloadBytes {
		t.Fatalf("hard-limit payload bytes=%d err=%v", len(payload), err)
	}
	raw = append(raw, ' ')
	if _, err := mcpApprovalPayload(raw); err == nil {
		t.Fatal("payload beyond hard limit was accepted")
	}
}
