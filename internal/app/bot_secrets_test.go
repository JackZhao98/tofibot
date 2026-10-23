package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// All values below are synthetic.

// echoingComputer answers every action like a shell that prints the injected
// environment, and records each request body.
func echoingComputer(t *testing.T) (*computer.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	client, err := computer.New(computer.Config{Socket: "/tmp/tofi-bot-secrets.sock", Client: &http.Client{Transport: secretTransport(func(req *http.Request) (*http.Response, error) {
		var raw []byte
		if req.Body != nil {
			raw, _ = io.ReadAll(req.Body)
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		var in struct {
			Args struct {
				Env map[string]string `json:"env"`
			} `json:"args"`
		}
		_ = json.Unmarshal(raw, &in)
		stdout := ""
		for _, v := range in.Args.Env {
			stdout += "echo:" + v + "\n"
		}
		result, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"exit_code": 0, "stdout": stdout, "stderr": ""}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(result))), Header: make(http.Header)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), bodies...) }
}

func shellTool(t *testing.T, s *Server, run Run) Tool {
	t.Helper()
	for _, tool := range s.microVMTools(run) {
		if tool.Name == "computer_shell" {
			return tool
		}
	}
	t.Fatal("computer_shell missing")
	return Tool{}
}

func putBotSecret(t *testing.T, v *secretVault, botID, name, value string) {
	t.Helper()
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, _, err := v.storeBotSecretLocked(botID, name, "label", value); err != nil {
		t.Fatal(err)
	}
	if err := v.saveLocked(); err != nil {
		t.Fatal(err)
	}
}

func putGlobalEnv(t *testing.T, v *secretVault, name, value string) {
	t.Helper()
	rec := secretRecord{ID: "global-" + name, Kind: "env", Target: name, Status: "stored", CreatedAt: now()}
	rec.Ciphertext, _ = v.seal(rec.ID, value)
	v.mu.Lock()
	v.records[rec.ID] = rec
	v.mu.Unlock()
}

// Per-Bot isolation, enforced by the server: Bot B naming Bot A's secret gets a
// tool error that lists only B's own names, and nothing reaches the computer.
func TestBotSecretIsolationIsServerEnforced(t *testing.T) {
	s, count := secretTestServer(t)
	putBotSecret(t, s.secretVault, "bot-a", "SYNTHETIC_A_KEY", "synthetic-value-of-a")
	putBotSecret(t, s.secretVault, "bot-b", "SYNTHETIC_B_KEY", "synthetic-value-of-b")
	tool := shellTool(t, s, Run{ID: "run-b", BotID: "bot-b"})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"true","secrets":["SYNTHETIC_A_KEY"]}`))
	if err == nil || !strings.Contains(err.Error(), "SYNTHETIC_B_KEY") || strings.Contains(err.Error(), "SYNTHETIC_A_KEY"+",") || strings.Contains(err.Error(), "synthetic-value") {
		t.Fatalf("expected an error listing only Bot B's names, got %v", err)
	}
	if strings.Contains(err.Error(), "available names: SYNTHETIC_A_KEY") {
		t.Fatalf("Bot A's name leaked: %v", err)
	}
	if *count != 0 {
		t.Fatalf("the computer received %d actions for a refused secret", *count)
	}
	if _, rerr := s.resolveShellSecrets("bot-b", []string{"SYNTHETIC_A_KEY"}); rerr == nil {
		t.Fatal("Bot B resolved Bot A's secret")
	}
}

// The Bot's own secret beats the Settings credential of the same name; the
// Settings credential serves a Bot that has none.
func TestBotScopedSecretWinsOverGlobal(t *testing.T) {
	s, _ := secretTestServer(t)
	putGlobalEnv(t, s.secretVault, "SYNTHETIC_TOKEN", "synthetic-global")
	putGlobalEnv(t, s.secretVault, "SYNTHETIC_ONLY_GLOBAL", "synthetic-only-global")
	putBotSecret(t, s.secretVault, "bot-a", "SYNTHETIC_TOKEN", "synthetic-bot-a")
	env, err := s.resolveShellSecrets("bot-a", []string{"SYNTHETIC_TOKEN", "SYNTHETIC_ONLY_GLOBAL"})
	if err != nil || env["SYNTHETIC_TOKEN"] != "synthetic-bot-a" || env["SYNTHETIC_ONLY_GLOBAL"] != "synthetic-only-global" {
		t.Fatalf("env=%v err=%v", env, err)
	}
	env, err = s.resolveShellSecrets("bot-other", []string{"SYNTHETIC_TOKEN"})
	if err != nil || env["SYNTHETIC_TOKEN"] != "synthetic-global" {
		t.Fatalf("a Bot with no secret of that name must get the global one: %v %v", env, err)
	}
	// Only the Bot's own record is stamped.
	s.secretVault.mu.Lock()
	own, _ := s.secretVault.findBotSecretLocked("bot-a", "SYNTHETIC_TOKEN")
	global, _ := s.secretVault.findGlobalEnvLocked("SYNTHETIC_TOKEN")
	s.secretVault.mu.Unlock()
	if own.LastUsedAt == "" || global.LastUsedAt != "" {
		t.Fatalf("last_used_at: own=%q global=%q", own.LastUsedAt, global.LastUsedAt)
	}
}

// Injection is an environment field on the one command. The command text that
// reaches the computer is the Bot's own, and the value is not written anywhere
// by the host.
func TestInjectionSendsEnvironmentNotAFileWriteAndMasksOutput(t *testing.T) {
	s, _ := secretTestServer(t)
	client, sent := echoingComputer(t)
	s.microVM = client
	const value = "synthetic-masked-value-5521"
	putBotSecret(t, s.secretVault, "bot-a", "SYNTHETIC_KEY", value)
	tool := shellTool(t, s, Run{ID: "run-a", BotID: "bot-a"})
	const command = `printf %s "$SYNTHETIC_KEY"`
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":`+fmt.Sprintf("%q", command)+`,"secrets":["SYNTHETIC_KEY"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, value) || !strings.Contains(out, secretMask) {
		t.Fatalf("output not masked: %s", out)
	}
	var shellBody string
	for _, b := range sent() {
		if strings.Contains(b, "shell.exec") {
			shellBody = b
		}
	}
	var action struct {
		Args struct {
			Command string            `json:"command"`
			Env     map[string]string `json:"env"`
		} `json:"args"`
	}
	if json.Unmarshal([]byte(shellBody), &action) != nil || action.Args.Command != command || action.Args.Env["SYNTHETIC_KEY"] != value {
		t.Fatalf("command was rewritten or the value did not travel as environment: %s", shellBody)
	}
	if strings.Contains(action.Args.Command, value) || strings.Contains(action.Args.Command, ">") || strings.Contains(action.Args.Command, ".tofi-env") {
		t.Fatalf("injection added a file write: %q", action.Args.Command)
	}
	// An unknown name is a clear tool error, not a silent skip.
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"command":"true","secrets":["SYNTHETIC_MISSING"]}`)); err == nil || !strings.Contains(err.Error(), "SYNTHETIC_KEY") {
		t.Fatalf("unknown name error: %v", err)
	}
}

func TestMaskCoversJSONEscapedAndOverlappingValues(t *testing.T) {
	values := []string{"line1\nline2\"q", "line1"}
	text := `{"stdout":"a line1\nline2\"q b line1"}`
	got := maskSecretValues(text, values)
	if strings.Contains(got, "line1") || strings.Count(got, secretMask) != 2 {
		t.Fatalf("masked: %s", got)
	}
}

// scriptedBotRun drives one real run with a scripted model. steps[i] is the
// i-th model call: a tool call, or a final answer when tool is empty.
type scriptStep struct{ id, tool, args string }

func scriptedModel(t *testing.T, steps []scriptStep, tools *sync.Map) *httptest.Server {
	var calls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
				CallID  string `json:"tool_call_id"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		for _, m := range payload.Messages {
			if m.Role == "tool" {
				tools.Store(m.CallID, m.Content)
			}
			if m.Role == "user" && strings.Contains(m.Content, "[saved secrets]") {
				tools.Store("context", m.Content)
			}
		}
		n := int(calls.Add(1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")
		delta := map[string]any{"content": "done"}
		if n < len(steps) && steps[n].tool != "" {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": steps[n].id, "type": "function", "function": map[string]string{"name": steps[n].tool, "arguments": steps[n].args}}}}
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
}

func answerCard(t *testing.T, s *Server, qid string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/questions/"+qid+"/answer", strings.NewReader(string(raw))))
	return rec
}

func botSecretNamesOf(s *Server, botID string) []string {
	s.secretVault.mu.Lock()
	defer s.secretVault.mu.Unlock()
	return s.secretVault.botSecretNamesLocked(botID)
}

// The whole path on the production runtime: the Bot asks (and tries to save by
// itself), the person ticks Save and answers, a second run lists the name,
// uses it, and the echoed value is masked in the result, the transcript, the
// events, the tool records and the checkpoints. A reloaded vault keeps it.
func TestBotSecretCollectSaveUseMaskAndReload(t *testing.T) {
	const value = "synthetic-service-key-9917"
	tools := &sync.Map{}
	steps := []scriptStep{
		// A save flag is not part of the tool: the schema refuses it, no card opens.
		{"try", "request_secret_input", `{"label":"Service key","purpose":"Call the synthetic service","suggested_name":"SYNTHETIC_SERVICE_KEY","save":true,"save_as":"HIJACKED"}`},
		{"ask", "request_secret_input", `{"label":"Service key","purpose":"Call the synthetic service","suggested_name":"SYNTHETIC_SERVICE_KEY"}`},
		{"sh", "computer_shell", `{"command":"printf %s \"$SYNTHETIC_SERVICE_KEY\"","secrets":["SYNTHETIC_SERVICE_KEY"]}`},
	}
	endpoint := scriptedModel(t, steps, tools)
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
	client, _ := echoingComputer(t)
	s.microVM = client
	b, _ := s.store.CreateBot("keeper", "", "test-model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Use the synthetic service", "fixture")
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
	if q.Fields[0].SuggestedName != "SYNTHETIC_SERVICE_KEY" {
		t.Fatalf("the card did not carry the Bot's suggested name: %+v", q.Fields)
	}
	// Nothing was kept by anything but the person.
	if names := botSecretNamesOf(s, b.ID); len(names) != 0 {
		t.Fatalf("the Bot caused a save: %v", names)
	}
	// The person ticks Save and may edit the name.
	if rec := answerCard(t, s, q.ID, map[string]any{"fields": map[string]string{privateInputFieldID: value}, "save_as": "lowercase-bad"}); rec.Code == 200 {
		t.Fatalf("an invalid variable name was accepted: %s", rec.Body.String())
	}
	if rec := answerCard(t, s, q.ID, map[string]any{"fields": map[string]string{privateInputFieldID: value}, "save_as": "SYNTHETIC_SERVICE_KEY"}); rec.Code != 200 || strings.Contains(rec.Body.String(), value) {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	waitForRunStatus(t, s, run.ID, "done")
	if refused, _ := tools.Load("try"); !strings.Contains(fmt.Sprint(refused), "invalid_arguments") {
		t.Fatalf("a Bot-set save flag was not refused: %v", refused)
	}
	if names := botSecretNamesOf(s, b.ID); len(names) != 1 || names[0] != "SYNTHETIC_SERVICE_KEY" {
		t.Fatalf("saved names: %v", names)
	}
	if seen, _ := tools.Load("sh"); strings.Contains(fmt.Sprint(seen), value) || !strings.Contains(fmt.Sprint(seen), secretMask) {
		t.Fatalf("the model saw %v", seen)
	}
	// The saved record is a Bot record: not in the Settings credential list.
	list := httptest.NewRecorder()
	s.Handler().ServeHTTP(list, httptest.NewRequest("GET", "/api/computer/credentials", nil))
	if strings.Contains(list.Body.String(), "SYNTHETIC_SERVICE_KEY") || strings.Contains(list.Body.String(), value) {
		t.Fatalf("a Bot secret appears in Settings credentials: %s", list.Body.String())
	}
	// A later run: the Bot already has it.
	tool := shellTool(t, s, Run{ID: "run-2", BotID: b.ID})
	out, err := tool.Execute(context.Background(), json.RawMessage(steps[2].args))
	if err != nil || strings.Contains(out, value) || !strings.Contains(out, secretMask) {
		t.Fatalf("masked use: %q %v", out, err)
	}
	if ctx := s.botSecretContext(b.ID); !strings.Contains(ctx, "SYNTHETIC_SERVICE_KEY") || strings.Contains(ctx, value) {
		t.Fatalf("context: %q", ctx)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	// Reload: the vault keeps the Bot secret and still seals it.
	v, err := initializeSecretVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := v.findBotSecretLocked(b.ID, "SYNTHETIC_SERVICE_KEY")
	if !ok || rec.Kind != botSecretKind {
		t.Fatal("vault reload lost the Bot secret")
	}
	if got, err := v.reveal(rec); err != nil || got != value {
		t.Fatalf("reloaded value: %v", err)
	}
	file, _ := os.ReadFile(filepath.Join(dir, "secrets", "vault.json"))
	if strings.Contains(string(file), value) {
		t.Fatal("vault holds plaintext")
	}
}

// A masked result travels through the production runtime: the model, the
// transcript, events, tool records and checkpoints see the mask only.
func TestMaskedOutputReachesNoPersistedSurface(t *testing.T) {
	const value = "synthetic-persisted-value-4408"
	tools := &sync.Map{}
	steps := []scriptStep{{"sh", "computer_shell", `{"command":"printf %s \"$SYNTHETIC_KEY\"","secrets":["SYNTHETIC_KEY"]}`}}
	endpoint := scriptedModel(t, steps, tools)
	defer endpoint.Close()
	engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: endpoint.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{DataDir: t.TempDir(), Provider: "test", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client, _ := echoingComputer(t)
	s.microVM = client
	b, _ := s.store.CreateBot("masker", "", "test-model")
	putBotSecret(t, s.secretVault, b.ID, "SYNTHETIC_KEY", value)
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "Run the synthetic command", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, run)
	waitForRunStatus(t, s, run.ID, "done")
	got, _ := tools.Load("sh")
	if text, _ := got.(string); text == "" || strings.Contains(text, value) || !strings.Contains(text, secretMask) {
		t.Fatalf("the model saw %q", got)
	}
	// Awareness: the run's context lists the name and never the value.
	ctx, _ := tools.Load("context")
	if text, _ := ctx.(string); !strings.Contains(text, "SYNTHETIC_KEY") || strings.Contains(text, value) {
		t.Fatalf("saved-secret context: %q", ctx)
	}
	for _, tc := range [][2]string{{"messages", "content"}, {"events", "data"}, {"tool_activities", "arguments"}, {"tool_activities", "result"}, {"run_checkpoints", "*"}, {"run_context_snapshots", "*"}} {
		var n int
		var q string
		if tc[1] == "*" {
			rows, err := s.store.db.Query(`SELECT * FROM ` + tc[0])
			if err != nil {
				t.Fatal(err)
			}
			cols, _ := rows.Columns()
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				_ = rows.Scan(ptrs...)
				for _, v := range vals {
					if strings.Contains(fmt.Sprint(v), value) {
						n++
					}
				}
			}
			rows.Close()
		} else {
			q = `SELECT COUNT(*) FROM ` + tc[0] + ` WHERE ` + tc[1] + ` LIKE ?`
			if err := s.store.db.QueryRow(q, "%"+value+"%").Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
		if n != 0 {
			t.Fatalf("value leaked to %s.%s", tc[0], tc[1])
		}
	}
}

// The person decides. No path other than the answer route's save_as saves, and
// that field is refused for a Bot actor and for a non-private card.
func TestOnlyThePersonCanSaveASecret(t *testing.T) {
	s, c, run := questionFixture(t)
	defer s.Close()
	vault, err := initializeSecretVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: s, secretVault: vault}
	in, _ := normalizeQuestionInput(askQuestionInput{Question: "Why", Type: questionForm, PrivateInput: true, Fields: []UserFormField{{ID: privateInputFieldID, Label: "Key", Type: "password", Required: true, SuggestedName: "SYNTHETIC_KEY"}}})
	q, err := server.CreateQuestion(c.ID, run, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = server.AnswerUserFormSaving(q.ID, run.BotID, map[string]string{privateInputFieldID: "synthetic"}, "SYNTHETIC_KEY"); err == nil {
		t.Fatal("a Bot actor saved a secret")
	}
	if len(vault.botSecretNamesLocked(run.BotID)) != 0 {
		t.Fatal("record left behind after a refused save")
	}
	// Not ticked: only the one-shot reference exists.
	if _, _, err = server.AnswerUserForm(q.ID, "human", map[string]string{privateInputFieldID: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if len(vault.botSecretNamesLocked(run.BotID)) != 0 {
		t.Fatal("a secret was kept without the person's choice")
	}
	// A webpage form cannot save, and a model cannot set a suggested name there.
	web, _ := normalizeQuestionInput(askQuestionInput{Question: "Log in", Type: questionForm, SourceURL: "https://example.test/login", Fields: []UserFormField{{ID: "pw", Label: "Password", Type: "password", Required: true}}})
	wq, err := server.CreateQuestion(c.ID, run, web)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = server.AnswerUserFormSaving(wq.ID, "human", map[string]string{"pw": "synthetic"}, "SYNTHETIC_KEY"); err == nil {
		t.Fatal("a webpage form saved a Bot secret")
	}
	if _, err = normalizeQuestionInput(askQuestionInput{Question: "Log in", Type: questionForm, SourceURL: "https://example.test/login", Fields: []UserFormField{{ID: "pw", Label: "Password", Type: "password", Required: true, SuggestedName: "SYNTHETIC_KEY"}}}); err == nil {
		t.Fatal("a webpage form accepted a suggested name")
	}
	if _, err = server.store.CancelQuestion(wq.ID); err != nil {
		t.Fatal(err)
	}
	// Saving the same name again replaces the value.
	for _, v := range []string{"synthetic-first", "synthetic-second"} {
		q2, _ := server.CreateQuestion(c.ID, run, in)
		if _, _, err = server.AnswerUserFormSaving(q2.ID, "human", map[string]string{privateInputFieldID: v}, "SYNTHETIC_KEY"); err != nil {
			t.Fatal(err)
		}
	}
	if names := vault.botSecretNamesLocked(run.BotID); len(names) != 1 {
		t.Fatalf("names after replace: %v", names)
	}
	rec, _ := vault.findBotSecretLocked(run.BotID, "SYNTHETIC_KEY")
	if got, _ := vault.reveal(rec); got != "synthetic-second" {
		t.Fatalf("replace kept %q", got)
	}
}

// The management routes need the owner's session and never carry a value.
func TestBotSecretRoutesAreOwnerOnlyAndNeverReturnValues(t *testing.T) {
	s, dir := ownerTestServer(t)
	var err error
	if s.secretVault, err = initializeSecretVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	bot, err := s.store.CreateBot("owned", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	const value = "synthetic-route-value-7102"
	putBotSecret(t, s.secretVault, bot.ID, "SYNTHETIC_KEY", value)
	path := "/api/bots/" + bot.ID + "/secrets"
	for _, call := range []struct{ method, path string }{{"GET", path}, {"PUT", path + "/SYNTHETIC_KEY"}, {"DELETE", path + "/SYNTHETIC_KEY"}} {
		if w := ownerCall(s, call.method, call.path, map[string]string{"value": "synthetic-other"}, nil, true); w.Code != 401 {
			t.Fatalf("%s %s without a session: %d", call.method, call.path, w.Code)
		}
	}
	if got, _ := s.secretVault.reveal(s.secretVault.records[firstBotSecretID(s, bot.ID)]); got != value {
		t.Fatal("an unauthenticated call changed the secret")
	}
	cookie := ownerSetup(t, s, dir)
	w := ownerCall(s, "GET", path, nil, cookie, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), value) || strings.Contains(strings.ToLower(w.Body.String()), "ciphertext") || !strings.Contains(w.Body.String(), "SYNTHETIC_KEY") || !strings.Contains(w.Body.String(), "created_at") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	w = ownerCall(s, "PUT", path+"/SYNTHETIC_KEY", map[string]string{"value": "synthetic-replaced"}, cookie, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-replaced") {
		t.Fatalf("replace: %d %s", w.Code, w.Body.String())
	}
	if got, _ := s.secretVault.reveal(s.secretVault.records[firstBotSecretID(s, bot.ID)]); got != "synthetic-replaced" {
		t.Fatalf("replaced value: %q", got)
	}
	if w = ownerCall(s, "PUT", path+"/SYNTHETIC_MISSING", map[string]string{"value": "x"}, cookie, true); w.Code != 404 {
		t.Fatalf("replace of an unknown name created a secret: %d", w.Code)
	}
	if w = ownerCall(s, "DELETE", path+"/SYNTHETIC_KEY", nil, cookie, true); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if names := botSecretNamesOf(s, bot.ID); len(names) != 0 {
		t.Fatalf("delete left %v", names)
	}
	// Deleting the Bot removes its secrets too.
	putBotSecret(t, s.secretVault, bot.ID, "SYNTHETIC_KEY", value)
	s.secretVault.purgeBotSecrets(bot.ID)
	if names := botSecretNamesOf(s, bot.ID); len(names) != 0 {
		t.Fatalf("purge left %v", names)
	}
}

func firstBotSecretID(s *Server, botID string) string {
	for id, r := range s.secretVault.records {
		if r.Kind == botSecretKind && r.BotID == botID {
			return id
		}
	}
	return ""
}

// Names only, inside a fixed budget, and nothing when the Bot has none.
func TestBotSecretContextListsNamesWithinBudget(t *testing.T) {
	s, _ := secretTestServer(t)
	if got := s.botSecretContext("bot-a"); got != "" {
		t.Fatalf("empty Bot context: %q", got)
	}
	for i := 0; i < maxBotSecrets; i++ {
		putBotSecret(t, s.secretVault, "bot-a", fmt.Sprintf("SYNTHETIC_LONG_SECRET_NAME_NUMBER_%02d", i), "synthetic-value-"+fmt.Sprint(i))
	}
	got := s.botSecretContext("bot-a")
	if len([]rune(got)) > botSecretContextRunes || !strings.Contains(got, "more") || strings.Contains(got, "synthetic-value") {
		t.Fatalf("context (%d runes): %q", len([]rune(got)), got)
	}
}
