package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// The model and remote computer are deterministic test doubles. The actual
// app tools, HTTP submission, worker lifecycle and history persistence run.
type userFormAcceptanceEngine struct{}

func (userFormAcceptanceEngine) Run(ctx context.Context, req Request) (Result, error) {
	tools := map[string]Tool{}
	for _, tool := range req.Tools {
		tools[tool.Name] = tool
	}
	execute := func(name string, args any) (string, error) {
		tool, ok := tools[name]
		if !ok {
			return "", fmt.Errorf("missing tool %s", name)
		}
		raw, _ := json.Marshal(args)
		callID := "fixture-" + name
		if req.OnToolEvent != nil {
			if err := req.OnToolEvent(runtime.ToolEvent{CallID: callID, Name: name, Arguments: string(raw), Status: "queued"}); err != nil {
				return "", err
			}
			if err := req.OnToolEvent(runtime.ToolEvent{CallID: callID, Name: name, Status: "running"}); err != nil {
				return "", err
			}
		}
		result, err := tool.Execute(ctx, raw)
		if err != nil {
			return "", err
		}
		if req.OnToolEvent != nil {
			if err := req.OnToolEvent(runtime.ToolEvent{CallID: callID, Name: name, Result: result, Status: "completed"}); err != nil {
				return "", err
			}
		}
		return result, nil
	}
	definition := formDefinition()
	definition.Question = "填写示例网站的登录信息（隔离验收，请勿输入真实密码）"
	definition.Fields[0].Label, definition.Fields[1].Label, definition.Fields[2].Label = "邮箱", "密码", "备注"
	answer, err := execute("ask_user_form", definition)
	if err != nil {
		return Result{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(answer), &fields); err != nil {
		return Result{}, err
	}
	if _, cancelled := fields["status"]; cancelled {
		return Result{Content: "表单已取消或过期，没有填写网页。"}, nil
	}
	var email string
	var private formSecretAnswer
	if json.Unmarshal(fields["email"], &email) != nil || json.Unmarshal(fields["password"], &private) != nil || !private.ValueHidden {
		return Result{}, errors.New("invalid protected form answer")
	}
	if _, err := execute("computer_browser", map[string]string{"action": "browser.snapshot"}); err != nil {
		return Result{}, err
	}
	if _, err := execute("computer_desktop", map[string]string{"action": "desktop.type", "text": email}); err != nil {
		return Result{}, err
	}
	if _, err := execute("use_secret_input", map[string]string{"secret_ref": private.SecretRef, "action": "browser_type"}); err != nil {
		return Result{}, err
	}
	return Result{Content: "表单已收到，任务已自动继续。测试输入已送达模拟网页字段；没有连接真实网站，密码原文没有返回给模型。"}, nil
}

func formAcceptanceComputer(t *testing.T) (*computer.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	typed := []string{}
	client, err := computer.New(computer.Config{Socket: "/tmp/tofi-form-synthetic.sock", Client: &http.Client{Transport: secretTransport(func(req *http.Request) (*http.Response, error) {
		var action computer.Action
		if req.Body != nil {
			_ = json.NewDecoder(req.Body).Decode(&action)
		}
		result := any(map[string]any{"ok": true})
		if action.Name == "browser.snapshot" {
			result = map[string]any{"current": map[string]string{"url": "https://example.test/login"}, "current_source": "focused"}
		}
		if action.Name == "desktop.type" || action.Name == "browser.type_private" {
			var args struct {
				Text   string `json:"text"`
				Origin string `json:"origin"`
			}
			_ = json.Unmarshal(action.Args, &args)
			if action.Name == "browser.type_private" && args.Origin != "https://example.test" {
				t.Error("private input lost approved origin")
			}
			mu.Lock()
			typed = append(typed, args.Text)
			mu.Unlock()
		}
		payload := map[string]any{"ok": true, "result": result}
		if action.Name == "" {
			payload = map[string]any{"kind": "synthetic", "state": "ready", "workspace_root": "/workspace"}
		}
		encoded, _ := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encoded))), Header: make(http.Header)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), typed...) }
}

func TestUserFormWorkerResumesFillsAndDoesNotRecordPassword(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: userFormAcceptanceEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client, typed := formAcceptanceComputer(t)
	s.microVM = client
	b, err := s.store.CreateBot("form acceptance", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Synthetic form acceptance", "form-http")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, run)
	var pending []Question
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		pending, _ = s.store.PendingQuestions(c.ID)
		if len(pending) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 1 || pending[0].Type != questionForm {
		t.Fatal("registered tool did not publish form")
	}
	values := formValues()
	data, _ := json.Marshal(map[string]any{"fields": values})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/questions/"+pending[0].ID+"/answer", strings.NewReader(string(data))))
	if w.Code != 200 {
		t.Fatalf("HTTP response %d: %s", w.Code, w.Body.String())
	}
	var finished Run
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		finished, _ = s.store.GetRun(run.ID)
		if finished.Status == "done" || finished.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finished.Status != "done" {
		t.Fatalf("worker did not finish: %+v", finished)
	}
	inputs := typed()
	if len(inputs) != 2 || inputs[0] != values["email"] || inputs[1] != values["password"] {
		t.Fatal("expected plain and private inputs did not reach simulated page")
	}
	for _, tableColumn := range [][2]string{{"messages", "content"}, {"events", "data"}, {"tool_activities", "arguments"}, {"tool_activities", "result"}, {"questions", "answer_json"}} {
		var count int
		if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM `+tableColumn[0]+` WHERE `+tableColumn[1]+` LIKE ?`, "%synthetic-private-value%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("secret leaked to %s", tableColumn[0])
		}
	}
	// Run status is persisted before the worker's deferred teardown completes.
	// Wait for that bounded cleanup rather than racing the status transition.
	for deadline := time.Now().Add(time.Second); ; {
		s.secretVault.mu.Lock()
		count := len(s.secretVault.records)
		s.secretVault.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed worker retained secret")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUserFormBrowserAcceptanceFixture(t *testing.T) {
	manifest := os.Getenv("TOFI_FORM_UI_ACCEPTANCE_MANIFEST")
	if manifest == "" {
		t.Skip("opt-in isolated UI acceptance")
	}
	ui, err := filepath.Abs("../../ui/dist")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{Environment: "acceptance", DataDir: t.TempDir(), UIDir: ui, Engine: userFormAcceptanceEngine{}, Provider: "acceptance", DefaultModel: "fixture-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.microVM, _ = formAcceptanceComputer(t)
	b, err := s.store.CreateBot("网页表单验收", "This is an isolated synthetic form fixture, not a real website.", "fixture-model")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	data, _ := json.Marshal(map[string]string{"base_url": httpServer.URL, "bot_id": b.ID, "conversation_id": b.DMConversationID})
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Synthetic user-form fixture: %s", httpServer.URL)
	<-time.After(10 * time.Minute)
}
