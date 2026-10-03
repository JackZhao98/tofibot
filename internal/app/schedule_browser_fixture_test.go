package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type scheduleBrowserEngine struct{ scheduled runtime.Engine }

func (e scheduleBrowserEngine) Run(ctx context.Context, request runtime.Request) (runtime.Result, error) {
	for _, tool := range request.Tools {
		if tool.Name == "complete_scheduled_task" {
			return e.scheduled.Run(ctx, request)
		}
	}
	if len(request.Tools) == 0 {
		return runtime.Result{Content: "Synthetic summary."}, nil
	}
	trigger := ""
	for _, message := range request.Messages {
		if message.Role == "user" {
			trigger = message.Content
		}
	}
	if !strings.Contains(trigger, "验收定时审批") {
		return runtime.Result{Content: "隔离验收环境。发送「验收定时审批」会创建一次本地假工具任务，不涉及真实账号。"}, nil
	}
	for _, tool := range request.Tools {
		if tool.Name != "create_schedule" {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"title": "隔离定时审批验收", "description": "隔离环境中的单次审批验收。", "content": "Perform the synthetic write only after human approval, then deliver the verified result.", "kind": "once", "run_at": time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339Nano), "timezone": "UTC"})
		for _, status := range []string{"queued", "running"} {
			if err := request.OnToolEvent(runtime.ToolEvent{CallID: "browser-create", Name: tool.Name, Arguments: string(raw), Status: status}); err != nil {
				return runtime.Result{}, err
			}
		}
		result, err := tool.Execute(ctx, raw)
		if err != nil {
			return runtime.Result{}, err
		}
		if err := request.OnToolEvent(runtime.ToolEvent{CallID: "browser-create", Name: tool.Name, Arguments: string(raw), Status: "completed", Result: result}); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{Content: "已创建一次隔离定时任务；稍后会出现本地假工具的审批卡。"}, nil
	}
	return runtime.Result{}, fmt.Errorf("fixture has no create_schedule tool")
}

// Explicit opt-in keeps normal CI finite. Only the test wrapper offers restart,
// evidence and stop endpoints; no production route or credential is added.
func TestScheduledApprovalBrowserFixture(t *testing.T) {
	outcome := os.Getenv("TOFI_SCHEDULE_BROWSER")
	if outcome == "" {
		t.Skip("opt-in rendered browser acceptance")
	}
	if outcome != "approve" && outcome != "deny" {
		t.Fatal("fixture outcome must be approve or deny")
	}
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
	config := Config{DataDir: dir, Environment: "acceptance", Provider: "test", Engine: scheduleBrowserEngine{scheduled: engine}, MCPConfigPath: filepath.Join(dir, "mcp.json"), UIDir: "../../ui/dist"}
	server, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { server.Close() }()
	if err := server.extensions.SaveMCP("fixture", extensions.MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	bot, err := server.store.CreateBot("隔离审批验收", "Use only synthetic local acceptance data.", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	stop := make(chan struct{})
	var stopOnce sync.Once
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active := server
		mu.Unlock()
		switch r.URL.Path {
		case "/acceptance/restart":
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", 405)
				return
			}
			if err := active.Close(); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			replacement, err := NewServer(config)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			mu.Lock()
			server = replacement
			mu.Unlock()
			writeJSON(w, 200, map[string]bool{"restarted": true})
		case "/acceptance/evidence":
			schedules, err := active.store.ListSchedules(bot.DMConversationID)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			runs, _ := active.store.Runs(bot.DMConversationID)
			questions, _ := active.store.ListQuestions(bot.DMConversationID)
			var receipts, replies int
			_ = active.store.db.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE name='complete_scheduled_task' AND status='completed'`).Scan(&receipts)
			_ = active.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE content='Synthetic scheduled write delivered.' AND kind<>'progress'`).Scan(&replies)
			writeJSON(w, 200, map[string]any{"remote_calls": remoteCalls.Load(), "receipts": receipts, "final_replies": replies, "schedules": schedules, "runs": runs, "questions": questions})
		case "/acceptance/stop":
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", 405)
				return
			}
			writeJSON(w, 200, map[string]bool{"stopped": true})
			stopOnce.Do(func() { close(stop) })
		default:
			active.Handler().ServeHTTP(w, r)
		}
	}))
	defer func() {
		httpServer.CloseClientConnections()
		httpServer.Close()
	}()
	if err := os.WriteFile(os.Getenv("TOFI_SCHEDULE_BROWSER_URL"), []byte(httpServer.URL), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("browser fixture %s outcome=%s\n", httpServer.URL, outcome)
	select {
	case <-stop:
	case <-time.After(15 * time.Minute):
		t.Fatal("browser acceptance timed out")
	}
	schedules, err := server.store.ListSchedules(bot.DMConversationID)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("schedules=%+v err=%v", schedules, err)
	}
	want, wantCalls := "failed", int32(0)
	if outcome == "approve" {
		want, wantCalls = "done", 1
	}
	if schedules[0].ExecutionStatus != want || remoteCalls.Load() != wantCalls {
		t.Fatalf("outcome schedule=%+v remote=%d", schedules[0], remoteCalls.Load())
	}
}
