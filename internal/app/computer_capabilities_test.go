package app

import (
	"bytes"
	"encoding/base64"
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
	if v := get("/api/computer/capabilities"); v["host_backend"] != "none" || v["attached"] != false {
		t.Fatalf("no host backend: %v", v)
	}
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
	if got["host_backend"] != "kvm" || got["attached"] != true {
		t.Fatalf("attached: %v", got)
	}
	// Detached on a host that has a backend: no computer for this account, backend still there.
	s.microVM = nil
	s.computerFactory = func() (computer.Provider, error) { return client, nil }
	det := get("/api/computer/capabilities")
	if det["backend"] != "none" || det["available"] != false || det["attached"] != false || det["host_backend"] != "kvm" {
		t.Fatalf("detached on a kvm host: %v", det)
	}
	if caps, _ := det["capabilities"].([]any); len(caps) != 0 || s.hasComputerCapability(CapShell) {
		t.Fatalf("detached account keeps no computer capabilities: %v", det)
	}
	s.computerFactory = nil
	s.microVM = client
	if !s.hasComputerCapability(CapDesktop) || s.hasComputerCapability("nope") {
		t.Fatal("hasComputerCapability")
	}
	s.microVM = nil
}

func attachMissingComputer(t *testing.T, s *Server) {
	t.Helper()
	dir, _ := os.MkdirTemp("", "tfc-")
	t.Cleanup(func() { os.RemoveAll(dir) })
	client, err := computer.New(computer.Config{Socket: dir + "/missing.sock"})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
	t.Cleanup(func() { s.microVM = nil })
}

// A computer nothing was saved for says so in [run context]; once the person
// adds an environment variable or an SSH key the line goes away, and a
// workspace with no computer never gets it (it has the no-computer line).
func TestFreshComputerRunContextSaysNoKeys(t *testing.T) {
	s := noComputerServer(t, &captureEngine{})
	if note := s.runContextNote(t.Context()); strings.Contains(note, freshComputerStatus) || !strings.Contains(note, noComputerStatus) {
		t.Fatalf("no-computer context: %s", note)
	}
	attachMissingComputer(t, s)
	note := s.runContextNote(t.Context())
	if !strings.Contains(note, freshComputerStatus) || strings.Contains(note, noComputerStatus) {
		t.Fatalf("fresh computer context does not say no keys are set: %s", note)
	}
	if !strings.Contains(strings.ToLower(freshComputerStatus), "ask the person") {
		t.Fatal("the line must tell the Bot to ask")
	}
	for _, in := range []string{`{"name":"Token","kind":"env","target":"SYNTHETIC_TOKEN","value":"synthetic-value"}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/computer/credentials", strings.NewReader(in))
		r.Header.Set("Content-Type", "application/json")
		s.Handler().ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("add credential: %d %s", w.Code, w.Body)
		}
	}
	if note = s.runContextNote(t.Context()); strings.Contains(note, freshComputerStatus) {
		t.Fatalf("the line stayed after a variable was added: %s", note)
	}
}

// /api/computer/contents names what was saved for the computer and counts the
// files that still wait for their copy; it never returns a value. Without a
// computer there is nothing to list.
func TestComputerContentsListsNamesAndPendingCopies(t *testing.T) {
	s := noComputerServer(t, &captureEngine{})
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/computer/contents", nil))
		return w
	}
	if w := get(); w.Code != 404 || !strings.Contains(w.Body.String(), "not_configured") {
		t.Fatalf("no computer: %d %s", w.Code, w.Body)
	}
	attachMissingComputer(t, s)
	for _, in := range []string{
		`{"name":"Token","kind":"env","target":"SYNTHETIC_TOKEN","value":"synthetic-secret-value"}`,
		`{"name":"Deploy","kind":"ssh","target":"id_synthetic","value":"-----BEGIN OPENSSH PRIVATE KEY-----\nsynthetic\n-----END OPENSSH PRIVATE KEY-----"}`,
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/computer/credentials", strings.NewReader(in)))
		if w.Code != 201 {
			t.Fatalf("add credential: %d %s", w.Code, w.Body)
		}
	}
	bot, err := s.store.CreateBot("Synthetic helper", "Answer briefly.", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv := bot.DMConversationID
	for i := 0; i < 2; i++ {
		if _, err := s.store.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, "att"+string(rune('a'+i)), conv, "f.txt", "text/plain", 1, "vm:att"+string(rune('a'+i)), now()); err != nil {
			t.Fatal(err)
		}
	}
	w := get()
	if w.Code != 200 {
		t.Fatalf("contents: %d %s", w.Code, w.Body)
	}
	var out struct {
		Environment []string `json:"environment"`
		SSHKeys     []string `json:"ssh_keys"`
		Pending     int      `json:"pending_file_copies"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Environment) != 1 || out.Environment[0] != "SYNTHETIC_TOKEN" || len(out.SSHKeys) != 1 || out.SSHKeys[0] != "id_synthetic" || out.Pending != 2 {
		t.Fatalf("contents: %+v", out)
	}
	if strings.Contains(w.Body.String(), "synthetic-secret-value") || strings.Contains(w.Body.String(), "PRIVATE KEY") {
		t.Fatal("a value leaked into the contents list")
	}
}

// The portability import stores attachment bytes in the server file store, so a
// workspace with no computer (backend none) imports them.
func TestPortableImportWithBackendNone(t *testing.T) {
	_, bundle, _ := portableAssetFixture(t)
	s := noComputerServer(t, &captureEngine{})
	if s.microVM != nil || s.store.guestBlobs != nil {
		t.Fatal("expected a workspace without a computer")
	}
	p, err := s.store.previewPortable(t.Context(), bundle)
	if err != nil || !p.CanApply {
		t.Fatalf("preview: %+v %v", p, err)
	}
	result, err := s.store.applyPortable(t.Context(), bundle, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range bundle.Attachments {
		got, path, err := s.store.Attachment(result.IDMap[a.ID])
		if err != nil || got.Size != a.Size {
			t.Fatalf("attachment %s: %+v %v", a.ID, got, err)
		}
		raw, err := os.ReadFile(path)
		want, _ := base64.StdEncoding.DecodeString(a.Data)
		if err != nil || !bytes.Equal(raw, want) {
			t.Fatalf("imported bytes differ: %v", err)
		}
	}
}
