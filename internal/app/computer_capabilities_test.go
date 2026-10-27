package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

func noComputerServer(t *testing.T, e *captureEngine) *Server {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e, ComputerBackend: ComputerBackendNone})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A workspace with no computer backend registers no computer-bound tool, tells
// the model it has no computer, and a run still completes.
func TestNoComputerServerRegistersNoComputerToolsAndRunCompletes(t *testing.T) {
	t.Setenv("TOFI_COMPUTER_SOCKET", "/nonexistent/computer.sock")
	e := &captureEngine{}
	s := noComputerServer(t, e)
	if s.microVM != nil {
		t.Fatal("backend none still built a computer client")
	}
	bot, _ := s.store.CreateBot("Synthetic helper", "Answer briefly.", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "hello", "no-computer-1")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(conv, run)
	if got, _ := s.store.GetRun(run.ID); got.Status != "done" {
		t.Fatalf("run status=%q", got.Status)
	}
	denied := map[string]bool{"publish_file": true, "request_takeover": true, "use_secret_input": true, "request_secret_input": true, "list_computers": true, "computer_action": true, "notify_on_exit": true}
	for _, tool := range e.req.Tools {
		if strings.HasPrefix(tool.Name, "computer_") || denied[tool.Name] {
			t.Errorf("tool %s is registered without a computer", tool.Name)
		}
	}
	var seen []string
	for _, tool := range e.req.Tools {
		seen = append(seen, tool.Name)
	}
	if len(seen) == 0 {
		t.Fatal("no tools registered at all")
	}
	if strings.Contains(e.req.System, "signed-in browser") || strings.Contains(e.req.System, "shared desktop") {
		t.Fatal("system prompt names a computer route")
	}
	var context strings.Builder
	for _, m := range e.req.Messages {
		context.WriteString(m.Content)
	}
	if !strings.Contains(context.String(), noComputerStatus) {
		t.Fatal("run context does not say there is no computer")
	}
}

func TestNoComputerPromptsNeverClaimAComputer(t *testing.T) {
	for _, text := range []string{taskCompletionGuidanceFor(false), scheduledNoComputerFallbackGuidance, onboardingSystemPromptFor(false), noComputerSkillNote} {
		if strings.Contains(strings.ToLower(text), "your own computer") || strings.Contains(text, "signed-in browser") || strings.Contains(text, "computer_browser") {
			t.Fatalf("no-computer text claims a computer: %.120s", text)
		}
	}
	if taskCompletionGuidanceFor(true) != taskCompletionGuidance || !strings.Contains(taskCompletionGuidance, "signed-in browser") {
		t.Fatal("computer guidance changed")
	}
	if !strings.Contains(onboardingSystemPromptFor(true), "your own computer") {
		t.Fatal("computer onboarding prompt lost its computer line")
	}
	for lang, with := range onboardingWelcomes {
		without := onboardingWelcomeFor(lang, false)
		if len(without) != len(with) || without[1] == with[1] || without[0] != with[0] || without[2] != with[2] {
			t.Fatalf("%s no-computer welcome is wrong: %q", lang, without)
		}
	}
	for _, topic := range []string{"mail", "web_tasks", "software"} {
		raw, _ := json.Marshal(map[string]string{"topic": topic})
		if _, err := workflowGuideToolFor(false).Execute(t.Context(), raw); err == nil {
			t.Fatalf("workflow guide %s is offered without a computer", topic)
		}
	}
	raw, _ := json.Marshal(map[string]string{"topic": "research"})
	out, err := workflowGuideToolFor(false).Execute(t.Context(), raw)
	if err != nil || strings.Contains(out, "real browser") {
		t.Fatalf("research guide without computer: %v %q", err, out)
	}
}

func TestComputerCapabilitiesEndpointAndConfig(t *testing.T) {
	s := noComputerServer(t, &captureEngine{})
	h := s.Handler()
	get := func(path string) map[string]any {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	want := func(v map[string]any) {
		t.Helper()
		if v["backend"] != "none" || v["available"] != false {
			t.Fatalf("capabilities=%v", v)
		}
		if caps, ok := v["capabilities"].([]any); !ok || len(caps) != 0 {
			t.Fatalf("capabilities list=%v", v["capabilities"])
		}
	}
	want(get("/api/computer/capabilities"))
	cfg := get("/api/config")
	computerField, _ := cfg["computer"].(map[string]any)
	want(computerField)

	dir, _ := os.MkdirTemp("", "tfc-")
	t.Cleanup(func() { os.RemoveAll(dir) })
	client, err := computer.New(computer.Config{Socket: dir + "/missing.sock"})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
	got := get("/api/computer/capabilities")
	if got["backend"] != "kvm" || got["available"] != true {
		t.Fatalf("kvm capabilities=%v", got)
	}
	if !s.hasComputerCapability(CapDesktop) || s.hasComputerCapability("nope") {
		t.Fatal("hasComputerCapability")
	}
	s.microVM = nil
}
