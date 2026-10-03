package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Only the model and external MCP endpoint are synthetic. The runtime,
// scheduler, conversation worker, checkpoint, approval gate and HTTP API are
// production components. No model credentials or real account actions exist.
func TestScheduledMCPApprovalRestartHTTPDelivery(t *testing.T) {
	for _, outcome := range []string{"approve", "deny", "expire"} {
		t.Run(outcome, func(t *testing.T) {
			var remoteCalls atomic.Int32
			remote := newAppMCPFixture(t, "fixture", newAppTextTool("write", "Synthetic external write", "target"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				remoteCalls.Add(1)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "synthetic write executed"}}}, nil
			})
			model := newScheduledApprovalModel(t, outcome, &remoteCalls)
			defer model.Close()
			engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: model.URL + "/v1", Model: "test-model"})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			config := Config{DataDir: dir, Provider: "test", Engine: engine, MCPConfigPath: filepath.Join(dir, "mcp.json")}
			server, err := NewServer(config)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if server != nil {
					server.Close()
				}
			}()
			if err := server.extensions.SaveMCP("fixture", extensions.MCPServerConfig{URL: remote.URL}, false); err != nil {
				t.Fatal(err)
			}
			bot, err := server.store.CreateBot("synthetic scheduled approval", "", "test-model")
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := server.store.GetConversation(bot.DMConversationID)
			if err != nil {
				t.Fatal(err)
			}
			x, err := server.store.CreateSchedule(conversation.ID, bot.ID, ScheduleSpec{Content: "Perform the synthetic write only after human approval, then deliver the verified result.", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
			if err != nil {
				t.Fatal(err)
			}
			makeDue(t, server.store, x.ID, time.Now().Add(-time.Minute))
			// Use the scheduler's real claim and dispatch, then its worker.
			server.scheduler.tick()
			var run Run
			deadline := time.Now().Add(5 * time.Second)
			for {
				x, err = server.store.GetSchedule(x.ID)
				if err != nil {
					t.Fatal(err)
				}
				if x.LastRunID != "" {
					run, err = server.store.GetRun(x.LastRunID)
					if err != nil {
						t.Fatal(err)
					}
					if run.Status == runWaiting {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("never waited: schedule=%+v run=%+v", x, run)
				}
				time.Sleep(10 * time.Millisecond)
			}
			questions, err := server.store.ListQuestions(conversation.ID)
			if err != nil || len(questions) != 1 {
				t.Fatalf("questions=%+v err=%v", questions, err)
			}
			question := questions[0]
			if question.Type != questionApproval || remoteCalls.Load() != 0 {
				t.Fatalf("before approval: question=%+v calls=%d", question, remoteCalls.Load())
			}
			assertScheduledApprovalSnapshots(t, server, conversation.ID, run.ID, question.ID)
			cursor := server.store.EventCursor(conversation.ID)
			if err := server.Close(); err != nil {
				t.Fatal(err)
			}
			server = nil
			if outcome == "expire" {
				store, err := OpenStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), question.ID)
				store.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			server, err = NewServer(config)
			if err != nil {
				t.Fatal(err)
			}
			if outcome != "expire" {
				waitForRunStatus(t, server, run.ID, runWaiting)
				assertScheduledApprovalSnapshots(t, server, conversation.ID, run.ID, question.ID)
				value := outcome == "approve"
				for range 2 {
					response := answerOtherQuestionHTTP(server, question.ID, fmt.Sprintf(`{"value":%t}`, value))
					if response.Code != http.StatusOK {
						t.Fatalf("answer=%d %s", response.Code, response.Body.String())
					}
				}
			}
			if outcome == "expire" {
				waitForRunStatus(t, server, run.ID, "failed")
				deadline := time.Now().Add(3 * time.Second)
				for {
					q, _ := server.store.GetQuestion(question.ID)
					if q.Status == questionExpired {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("approval did not expire")
					}
					time.Sleep(10 * time.Millisecond)
				}
				response := answerOtherQuestionHTTP(server, question.ID, `{"value":true}`)
				if response.Code != http.StatusConflict || remoteCalls.Load() != 0 {
					t.Fatalf("expired approval executed: HTTP=%d calls=%d", response.Code, remoteCalls.Load())
				}
				return
			}
			wantStatus, wantCalls, wantDelivery := "failed", int32(0), false
			if outcome == "approve" {
				wantStatus, wantCalls, wantDelivery = "done", 1, true
			}
			waitForRunStatus(t, server, run.ID, wantStatus)
			assertScheduledApprovalSnapshots(t, server, conversation.ID, run.ID, question.ID)
			if remoteCalls.Load() != wantCalls {
				t.Fatalf("remote calls=%d want=%d", remoteCalls.Load(), wantCalls)
			}
			items := readOccurrences(t, server, conversation.ID, run.ID)
			if len(items) != 1 || items[0].ExecutionStatus != wantStatus || items[0].ResultInConversation != wantDelivery {
				t.Fatalf("occurrence=%+v", items)
			}
			var receipts, replies, approvals int
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE run_id=? AND name='complete_scheduled_task' AND status='completed'`, run.ID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND run_id=? AND role='assistant' AND kind<>'progress'`, conversation.ID, run.ID).Scan(&replies); err != nil {
				t.Fatal(err)
			}
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_approvals WHERE run_id=?`, run.ID).Scan(&approvals); err != nil {
				t.Fatal(err)
			}
			if receipts != int(wantCalls) || replies != int(wantCalls) || approvals != 1 {
				t.Fatalf("receipts=%d replies=%d approvals=%d", receipts, replies, approvals)
			}
			// A late duplicate or attempted approval of an expired card must
			// neither launch a worker nor create a second result.
			late := answerOtherQuestionHTTP(server, question.ID, `{"value":true}`)
			if outcome == "expire" && late.Code != http.StatusConflict {
				t.Fatalf("expired answer=%d %s", late.Code, late.Body.String())
			}
			if outcome != "expire" && late.Code != http.StatusOK {
				t.Fatalf("duplicate answer=%d %s", late.Code, late.Body.String())
			}
			for range 2 {
				server.scheduler.tick()
			}
			if remoteCalls.Load() != wantCalls {
				t.Fatal("duplicate approval replayed external action")
			}
			finalQuestion, err := server.store.GetQuestion(question.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantAnswer := "true"
			if outcome == "deny" {
				wantAnswer = "false"
			}
			if outcome == "expire" {
				wantAnswer = ""
			}
			if string(finalQuestion.Answer) != wantAnswer {
				t.Fatalf("late approval changed decision: %+v", finalQuestion)
			}
			var finalReplies, claimedApprovals, occurrenceCount int
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND run_id=? AND role='assistant' AND kind<>'progress'`, conversation.ID, run.ID).Scan(&finalReplies); err != nil {
				t.Fatal(err)
			}
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_approvals WHERE run_id=? AND claimed_at<>''`, run.ID).Scan(&claimedApprovals); err != nil {
				t.Fatal(err)
			}
			if err := server.store.db.QueryRow(`SELECT COUNT(*) FROM schedule_occurrences WHERE schedule_id=?`, x.ID).Scan(&occurrenceCount); err != nil {
				t.Fatal(err)
			}
			if finalReplies != int(wantCalls) || claimedApprovals != int(wantCalls) || occurrenceCount != 1 {
				t.Fatalf("after duplicate: replies=%d claimed=%d occurrences=%d", finalReplies, claimedApprovals, occurrenceCount)
			}
			assertScheduledApprovalReplay(t, server, conversation.ID, cursor)
		})
	}
}

// Check the exact refresh endpoints consumed by Web against durable state.
func assertScheduledApprovalSnapshots(t *testing.T, server *Server, conversationID, runID, questionID string) {
	t.Helper()
	get := func(path string, target any) {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("refresh %s: %d %s", path, response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
			t.Fatal(err)
		}
	}
	var runs struct {
		Runs []Run `json:"runs"`
	}
	get("/api/conversations/"+conversationID+"/runs", &runs)
	want, err := server.store.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, run := range runs.Runs {
		if run.ID == runID {
			matched = true
			if run.Status != want.Status || run.Error != want.Error {
				t.Fatalf("refresh run=%+v durable=%+v", run, want)
			}
		}
	}
	if !matched {
		t.Fatal("refresh omitted scheduled run")
	}
	var questions struct {
		Questions []QuestionCard `json:"questions"`
	}
	get("/api/questions?conversation_id="+conversationID, &questions)
	question, err := server.store.GetQuestion(questionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions.Questions) != 1 || !reflect.DeepEqual(questions.Questions[0], question.Card()) {
		t.Fatalf("refresh questions=%+v durable=%+v", questions, question.Card())
	}
	wantOccurrences, err := server.store.ScheduleOccurrences(context.Background(), conversationID, []string{runID})
	if err != nil {
		t.Fatal(err)
	}
	if got := readOccurrences(t, server, conversationID, runID); !reflect.DeepEqual(got, wantOccurrences) {
		t.Fatalf("refresh occurrences=%+v durable=%+v", got, wantOccurrences)
	}
}

// Reconnect from the cursor saved before shutdown and compare every durable
// replayed frame, including approval answer, resumed run and final delivery.
func assertScheduledApprovalReplay(t *testing.T, server *Server, conversationID string, cursor int64) {
	t.Helper()
	expected, err := server.store.Events(conversationID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) == 0 {
		t.Fatal("no durable events after restart")
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/api/conversations/"+conversationID+"/events?after=0", nil)
	request.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for _, event := range expected {
		var id, kind, data string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" && id != "" {
				break
			}
			if strings.HasPrefix(line, "id: ") {
				id = strings.TrimPrefix(line, "id: ")
			}
			if strings.HasPrefix(line, "event: ") {
				kind = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		var decoded any
		if err := json.Unmarshal([]byte(data), &decoded); err != nil {
			t.Fatalf("SSE data=%q error=%v scanner=%v", data, err, scanner.Err())
		}
		if id != fmt.Sprint(event["id"]) || kind != event["type"] || !reflect.DeepEqual(decoded, event["data"]) {
			t.Fatalf("replay id=%s kind=%s data=%s durable=%+v", id, kind, data, event)
		}
	}
}

// Shared by automated HTTP acceptance and the opt-in rendered-browser fixture.
func newScheduledApprovalModel(t *testing.T, outcome string, remoteCalls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Stream bool `json:"stream"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role, Content string
				CallID        string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture input", 400)
			return
		}
		seen := map[string]string{}
		for _, message := range input.Messages {
			if message.Role == "tool" {
				seen[message.CallID] = message.Content
			}
		}
		hasMCP := false
		for _, tool := range input.Tools {
			hasMCP = hasMCP || tool.Function.Name == "call_mcp_tool"
		}
		delta := map[string]any{"content": "Synthetic summary."}
		call := func(id, name, arguments string) {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]string{"name": name, "arguments": arguments}}}}
		}
		if hasMCP {
			switch {
			case seen["discover-before"] == "":
				call("discover-before", "search_mcp_tools", `{"server":"fixture","query":"write"}`)
			case seen["approval-wait"] == "":
				call("approval-wait", "call_mcp_tool", `{"name":"mcp_fixture__write","arguments":{"target":"synthetic"}}`)
			case seen["discover-after"] == "":
				// An approval answer is not the remote result. Reload the
				// schema in this new execution, then explicitly call once.
				if outcome == "approve" && !strings.Contains(seen["approval-wait"], "approval_recorded") {
					t.Errorf("resume lost approval answer: %q", seen["approval-wait"])
				}
				if remoteCalls.Load() != 0 {
					t.Error("remote write occurred before explicit resumed call")
				}
				call("discover-after", "search_mcp_tools", `{"server":"fixture","query":"write"}`)
			case seen["execute-after"] == "":
				call("execute-after", "call_mcp_tool", `{"name":"mcp_fixture__write","arguments":{"target":"synthetic"}}`)
			case outcome == "approve" && seen["receipt"] == "":
				if !strings.Contains(seen["execute-after"], "synthetic write executed") {
					t.Errorf("receipt without remote evidence: %q", seen["execute-after"])
				}
				call("receipt", "complete_scheduled_task", `{"content":"Synthetic scheduled write delivered."}`)
			default:
				content := "Synthetic scheduled write delivered."
				if outcome != "approve" {
					content = "The synthetic write was not approved; nothing was executed."
				}
				delta = map[string]any{"content": content}
			}
		}
		if !input.Stream {
			w.Header().Set("Content-Type","application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices":[]any{map[string]any{"index":0,"message":delta}},"usage":map[string]int{"prompt_tokens":10,"completion_tokens":5}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
	}))
}
