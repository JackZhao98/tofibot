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
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// request_secret_input is a front for the form rendezvous. These tests run the
// production runtime, workers, SQLite, vault and HTTP answer route; only the
// model endpoint and the remote computer are deterministic local doubles.

func recordingComputer(t *testing.T) (*computer.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	client, err := computer.New(computer.Config{Socket: "/tmp/tofi-secret-fold.sock", Client: &http.Client{Transport: secretTransport(func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			raw, _ := io.ReadAll(req.Body)
			mu.Lock()
			bodies = append(bodies, string(raw))
			mu.Unlock()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"exit_code":0,"stdout":"","stderr":""}}`)), Header: make(http.Header)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), bodies...) }
}

var secretRefPattern = regexp.MustCompile(`"secret_ref":"([^"]+)"`)

func TestRequestSecretInputSuspendsAcrossRestartAndYieldsWorkingReference(t *testing.T) {
	const value = "synthetic-private-value-7731"
	var calls atomic.Int32
	var resumedWith atomic.Value
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
				CallID  string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			http.Error(w, "invalid", 400)
			return
		}
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		call := func(id, name, args string) map[string]any {
			return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}
		}
		var delta map[string]any
		switch n {
		case 1:
			delta = call("ask", "request_secret_input", `{"label":"Service key","purpose":"Install it as an environment variable for the synthetic job"}`)
		case 2:
			var ask string
			for _, m := range payload.Messages {
				if m.Role == "tool" && m.CallID == "ask" {
					ask = m.Content
				}
			}
			resumedWith.Store(ask)
			match := secretRefPattern.FindStringSubmatch(ask)
			if match == nil {
				t.Errorf("resumed result carries no reference: %s", ask)
				delta = map[string]any{"content": "no reference"}
				break
			}
			delta = call("use", "use_secret_input", fmt.Sprintf(`{"secret_ref":%q,"action":"env","target":"SYNTHETIC_SERVICE_KEY"}`, match[1]))
		default:
			delta = map[string]any{"content": "Installed the key; the value was never visible."}
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	defer endpoint.Close()
	engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: endpoint.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, Provider: "test", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	client, sent := recordingComputer(t)
	s.microVM = client
	b, err := s.store.CreateBot("secret fold", "", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Install the synthetic key", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, run)
	waitForRunStatus(t, s, run.ID, runWaiting)
	pending, err := s.store.PendingQuestions(c.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v %v", pending, err)
	}
	q := pending[0]
	if q.Type != questionForm || q.SourceURL != "" || len(q.Fields) != 1 || q.Fields[0].Type != "password" || !q.isPrivateInput() {
		t.Fatalf("not a single private password card: %+v", q)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("run did not suspend: %d model calls", calls.Load())
	}

	// Restart: the same question is still pending and the run is still parked.
	s, err = NewServer(Config{DataDir: dir, Provider: "test", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client, sent = recordingComputer(t)
	s.microVM = client
	waitForRunStatus(t, s, run.ID, runWaiting)
	if still, e := s.store.PendingQuestions(c.ID); e != nil || len(still) != 1 || still[0].ID != q.ID {
		t.Fatalf("question lost across restart: %+v %v", still, e)
	}
	// The HTTP route that used to take the value is gone.
	for _, probe := range []struct{ method, path string }{{"GET", "/api/secret-inputs?conversation_id=" + c.ID}, {"POST", "/api/secret-inputs/" + q.ID}} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(probe.method, probe.path, strings.NewReader(`{"value":"`+value+`"}`)))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s is still served: %d", probe.method, probe.path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"fields": map[string]string{privateInputFieldID: value}})
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/questions/"+q.ID+"/answer", strings.NewReader(string(body))))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), value) {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	waitForRunStatus(t, s, run.ID, "done")
	if got, _ := resumedWith.Load().(string); !strings.Contains(got, `"value_hidden":true`) || strings.Contains(got, value) {
		t.Fatalf("resumed result: %s", got)
	}
	var typed bool
	for _, request := range sent() {
		if strings.Contains(request, value) {
			typed = true
		}
	}
	if !typed {
		t.Fatalf("reference did not reach the computer action: %v", sent())
	}
	for _, tableColumn := range [][2]string{{"messages", "content"}, {"events", "data"}, {"tool_activities", "arguments"}, {"tool_activities", "result"}, {"questions", "answer_json"}, {"run_input_waits", "checkpoint_json"}} {
		var count int
		if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM `+tableColumn[0]+` WHERE `+tableColumn[1]+` LIKE ?`, "%"+value+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("secret leaked to %s.%s", tableColumn[0], tableColumn[1])
		}
	}
	vaultFile, err := os.ReadFile(filepath.Join(dir, "secrets", "vault.json"))
	if err != nil || strings.Contains(string(vaultFile), value) {
		t.Fatalf("vault holds plaintext or is unreadable: %v", err)
	}
}

// A secret reference stays bound to the run, Bot and conversation that asked,
// lives 15 minutes, and a private input is not tied to any website.
func TestPrivateInputAnswerCreatesRunBoundReference(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	vault, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: s, secretVault: vault}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Why", Type: questionForm, PrivateInput: true, Fields: []UserFormField{{ID: privateInputFieldID, Label: "Key", Type: "password", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := server.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	answered, _, err := server.AnswerUserForm(q.ID, "human", map[string]string{privateInputFieldID: " synthetic value "})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]formSecretAnswer
	if json.Unmarshal(answered.Answer, &fields) != nil || !fields[privateInputFieldID].ValueHidden {
		t.Fatalf("answer: %s", answered.Answer)
	}
	rec := vault.records[fields[privateInputFieldID].SecretRef]
	if rec.RunID != run.ID || rec.BotID != run.BotID || rec.ConversationID != c.ID || rec.Status != "ready" || rec.Kind != "" || rec.Target != "" {
		t.Fatalf("unexpected record: %+v", publicSecret(rec))
	}
	if value, err := vault.reveal(rec); err != nil || value != " synthetic value " {
		t.Fatalf("value not stored verbatim: %q %v", value, err)
	}
	// A webpage form still binds its reference to the approved HTTPS origin.
	web, err := normalizeQuestionInput(askQuestionInput{Question: "Log in", Type: questionForm, SourceURL: "https://example.test/login?x=1", Fields: []UserFormField{{ID: "pw", Label: "Password", Type: "password", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	wq, err := server.CreateQuestion(c.ID, run, web)
	if err != nil {
		t.Fatal(err)
	}
	webAnswered, _, err := server.AnswerUserForm(wq.ID, "human", map[string]string{"pw": "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	var webFields map[string]formSecretAnswer
	if json.Unmarshal(webAnswered.Answer, &webFields) != nil {
		t.Fatalf("answer: %s", webAnswered.Answer)
	}
	if web := vault.records[webFields["pw"].SecretRef]; web.Kind != "browser_form" || web.Target != "https://example.test" {
		t.Fatalf("webpage reference lost its origin binding: %+v", publicSecret(web))
	}
}

func TestPrivateInputIsNotReachableThroughTheWebpageFormTool(t *testing.T) {
	if _, err := normalizeQuestionInput(askQuestionInput{Question: "Why", Type: questionForm, Fields: []UserFormField{{ID: "secret", Label: "Key", Type: "password", Required: true}}}); err == nil {
		t.Fatal("a form without a destination was accepted outside the private-input path")
	}
	var fromModel askQuestionInput
	if err := json.Unmarshal([]byte(`{"question":"Why","type":"form","PrivateInput":true,"fields":[{"id":"secret","label":"Key","type":"password","required":true}]}`), &fromModel); err != nil {
		t.Fatal(err)
	}
	if fromModel.PrivateInput {
		t.Fatal("model arguments can set the private-input marker")
	}
}

// Records written by a build that still had its own pending-secret route must
// not crash startup, must not be served, and must not be usable.
func TestLegacyPendingSecretRecordIsHarmless(t *testing.T) {
	dir := t.TempDir()
	v, err := initializeSecretVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, _ := v.seal("legacy-ready", "synthetic-legacy-value")
	v.records["legacy-pending"] = secretRecord{ID: "legacy-pending", RunID: "run", BotID: "bot", ConversationID: "conv", Label: "Old key", Purpose: "Old", Status: "pending", CreatedAt: now()}
	v.records["legacy-ready"] = secretRecord{ID: "legacy-ready", RunID: "run", BotID: "bot", ConversationID: "conv", Status: "ready", CreatedAt: now(), Ciphertext: ciphertext}
	v.records["settings-credential"] = secretRecord{ID: "settings-credential", Kind: "env", Target: "SYNTHETIC_KEY", Status: "stored", CreatedAt: now(), Ciphertext: ciphertext}
	if err = v.saveLocked(); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{DataDir: dir, Provider: "test"})
	if err != nil {
		t.Fatalf("startup with a legacy pending record: %v", err)
	}
	defer s.Close()
	if _, ok := s.secretVault.records["legacy-pending"]; ok {
		t.Fatal("legacy pending request survived startup")
	}
	if _, ok := s.secretVault.records["legacy-ready"]; ok {
		t.Fatal("legacy run reference survived startup")
	}
	if _, ok := s.secretVault.records["settings-credential"]; !ok {
		t.Fatal("persistent Settings credential was dropped")
	}
	// Even if one were present in memory, the old route is gone and a pending
	// record cannot be used as a reference.
	s.secretVault.records["late-pending"] = secretRecord{ID: "late-pending", RunID: "run", BotID: "bot", ConversationID: "conv", Status: "pending", CreatedAt: now()}
	for _, probe := range []struct{ method, path string }{{"GET", "/api/secret-inputs"}, {"POST", "/api/secret-inputs/late-pending"}, {"DELETE", "/api/secret-inputs/late-pending"}} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(probe.method, probe.path, strings.NewReader(`{"value":"x"}`)))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s is still served: %d", probe.method, probe.path, rec.Code)
		}
	}
	if s.secretVault.records["late-pending"].Status != "pending" {
		t.Fatal("legacy route mutated a pending record")
	}
	client, sent := recordingComputer(t)
	s.microVM = client
	use := s.secretTools(Run{ID: "run", BotID: "bot", ConversationID: "conv"})[0]
	if _, err = use.Execute(context.Background(), json.RawMessage(`{"secret_ref":"late-pending","action":"env","target":"SYNTHETIC_KEY"}`)); err == nil || len(sent()) != 0 {
		t.Fatalf("a pending record was usable as a reference: %v %v", err, sent())
	}
}

func TestSecretToolsRegisterOnlyUseSecretInput(t *testing.T) {
	s, _ := secretTestServer(t)
	var names []string
	for _, tool := range s.secretTools(Run{ID: "run", BotID: "bot", ConversationID: "conv"}) {
		names = append(names, tool.Name)
	}
	if len(names) != 1 || names[0] != "use_secret_input" {
		t.Fatalf("secret tools: %v", names)
	}
}

// A cleared reference is re-requested through the tool that asked for it.
func TestPrivateInputExpiredReferenceNamesItsOwnTool(t *testing.T) {
	store, c, run := questionFixture(t)
	defer store.Close()
	vault, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, secretVault: vault}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Why", Type: questionForm, PrivateInput: true, Fields: []UserFormField{{ID: privateInputFieldID, Label: "Key", Type: "password", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	answered, _, err := s.AnswerUserForm(q.ID, "human", map[string]string{privateInputFieldID: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	s.clearRunSecrets(run.ID)
	if got := s.inputResumeResult(answered); !strings.Contains(got, "secret_expired") || !strings.Contains(got, "request_secret_input") || strings.Contains(got, "ask_user_form") {
		t.Fatalf("resume result: %s", got)
	}
}

func privateInputServer(t *testing.T) (*Server, Conversation, Run) {
	t.Helper()
	store, c, run := questionFixture(t)
	t.Cleanup(func() { store.Close() })
	vault, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: store, secretVault: vault}, c, run
}

// Only the marker written by request_secret_input makes a form a private
// input. A stored webpage-less form without it never yields an unbound
// reference, and a marked row must still have exactly the expected field.
func TestPrivateInputIsIdentifiedByMarkerAndShape(t *testing.T) {
	password := func(id string) []UserFormField {
		return []UserFormField{{ID: id, Label: "Key", Type: "password", Required: true}}
	}
	t.Run("legacy form without a website", func(t *testing.T) {
		s, c, run := privateInputServer(t)
		q, err := s.CreateQuestion(c.ID, run, askQuestionInput{Question: "Legacy", Type: questionForm, Fields: password(privateInputFieldID), ExpiresInSeconds: 600})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.AnswerUserForm(q.ID, "human", map[string]string{privateInputFieldID: "synthetic"}); err == nil {
			t.Fatal("a form without the marker produced a reference")
		}
		if len(s.secretVault.records) != 0 {
			t.Fatalf("legacy row created a vault record: %v", s.secretVault.records)
		}
	})
	t.Run("marked row with the wrong shape", func(t *testing.T) {
		for name, fields := range map[string][]UserFormField{
			"other id":  password("other"),
			"text type": {{ID: privateInputFieldID, Label: "Key", Type: "text", Required: true}},
			"two":       append(password(privateInputFieldID), UserFormField{ID: "extra", Label: "Extra", Type: "text"}),
		} {
			s, c, run := privateInputServer(t)
			q, err := s.CreateQuestion(c.ID, run, askQuestionInput{Question: "Marked", Type: questionForm, PrivateInput: true, Fields: fields, ExpiresInSeconds: 600})
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for _, f := range fields {
				values[f.ID] = "synthetic"
			}
			if _, _, err = s.AnswerUserForm(q.ID, "human", values); !errors.Is(err, ErrQuestionInvalidAnswer) {
				t.Fatalf("%s: want invalid answer, got %v", name, err)
			}
			if len(s.secretVault.records) != 0 {
				t.Fatalf("%s: vault record created", name)
			}
		}
	})
}

// A private-input value has no origin, so it can never be typed into a page.
func TestPrivateInputReferenceIsNeverTyped(t *testing.T) {
	s, count := secretTestServer(t)
	run := Run{ID: "run", BotID: "bot", ConversationID: "conv"}
	ciphertext, _ := s.secretVault.seal("ref", "synthetic")
	s.secretVault.records["ref"] = secretRecord{ID: "ref", RunID: run.ID, BotID: run.BotID, ConversationID: run.ConversationID, Status: "ready", CreatedAt: now(), Ciphertext: ciphertext}
	use := s.secretTools(run)[0]
	_, err := use.Execute(context.Background(), json.RawMessage(`{"secret_ref":"ref","action":"browser_type"}`))
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Status != tooloutcome.Denied || out.Code != "private_input_not_typeable" || out.Certainty != "not_executed" || !strings.Contains(out.Message, "ask_user_form") {
		t.Fatalf("want classified refusal, got %v", err)
	}
	if *count != 0 {
		t.Fatal("refused call reached the computer")
	}
	if _, err = use.Execute(context.Background(), json.RawMessage(`{"secret_ref":"ref","action":"env","target":"SYNTHETIC_KEY"}`)); err != nil || *count != 1 {
		t.Fatalf("env install must keep working: %v", err)
	}
}

// References past their lifetime are unusable and must not hold one of the
// eight per-run slots.
func TestExpiredReferencesDoNotCountTowardRunCap(t *testing.T) {
	s, c, run := privateInputServer(t)
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("old-%d", i)
		s.secretVault.records[id] = secretRecord{ID: id, RunID: run.ID, BotID: run.BotID, ConversationID: c.ID, Status: "ready", CreatedAt: old}
	}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Why", Type: questionForm, PrivateInput: true, Fields: []UserFormField{{ID: privateInputFieldID, Label: "Key", Type: "password", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AnswerUserForm(q.ID, "human", map[string]string{privateInputFieldID: "synthetic"}); err != nil {
		t.Fatalf("expired references blocked a new request: %v", err)
	}
}

// After expiry both the live use and the resume path say the same thing:
// asking again is permitted.
func TestExpiredReferenceGuidanceIsConsistent(t *testing.T) {
	s, count := secretTestServer(t)
	run := Run{ID: "run", BotID: "bot", ConversationID: "conv"}
	ciphertext, _ := s.secretVault.seal("ref", "synthetic")
	s.secretVault.records["ref"] = secretRecord{ID: "ref", RunID: run.ID, BotID: run.BotID, ConversationID: run.ConversationID, Status: "ready", CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), Ciphertext: ciphertext}
	_, err := s.secretTools(run)[0].Execute(context.Background(), json.RawMessage(`{"secret_ref":"ref","action":"env","target":"SYNTHETIC_KEY"}`))
	out, ok := tooloutcome.FromError(err)
	if !ok || out.Code != "secret_reference_unavailable" || out.NextAction != secretRerequestNext || out.NextAction == "explain_blocker" || !strings.Contains(out.Message, "permitted") || *count != 0 {
		t.Fatalf("live use: %v", err)
	}
	// The resume path carries the same action and wording.
	store, c, qrun := questionFixture(t)
	defer store.Close()
	server := &Server{store: store, secretVault: s.secretVault}
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Why", Type: questionForm, PrivateInput: true, Fields: []UserFormField{{ID: privateInputFieldID, Label: "Key", Type: "password", Required: true}}})
	q, _ := server.CreateQuestion(c.ID, qrun, in)
	answered, _, err := server.AnswerUserForm(q.ID, "human", map[string]string{privateInputFieldID: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	server.clearRunSecrets(qrun.ID)
	var fields map[string]map[string]any
	if json.Unmarshal([]byte(server.inputResumeResult(answered)), &fields) != nil {
		t.Fatal("resume result is not JSON")
	}
	got := fields[privateInputFieldID]
	if got["next_action"] != secretRerequestNext || got["action_required"] != secretRerequestText("request_secret_input") || !strings.Contains(out.Message, "permitted") || !strings.Contains(got["action_required"].(string), "permitted") {
		t.Fatalf("resume guidance differs: %v", got)
	}
}
