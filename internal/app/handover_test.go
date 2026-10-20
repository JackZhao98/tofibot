package app

import (
	"bytes"
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
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Handover cards run on the production runtime, workers, SQLite and HTTP
// routes. Only the model endpoint and the remote computer are local doubles.

const (
	typedSecret     = "synthetic-typed-secret-4417"
	clipboardSecret = "synthetic-clipboard-secret-9902"
)

type handoverRig struct {
	t          *testing.T
	dir        string
	endpoint   *httptest.Server
	engine     runtime.Engine
	calls      atomic.Int32
	mu         sync.Mutex
	result     string
	requests   []string
	guest      *handoverGuest
	s          *Server
	bot        Bot
	conv       Conversation
	run        Run
	q          Question
	setup      func(*Server, string)
	configured bool
}

// newHandoverRig starts a run whose model calls one handover tool and then
// answers in words. The run is returned suspended on the card.
func newHandoverRig(t *testing.T, tool, args string, setup func(*Server, string)) *handoverRig {
	t.Helper()
	g := &handoverRig{t: t, dir: t.TempDir(), setup: setup}
	g.endpoint = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.requests = append(g.requests, string(raw))
		g.mu.Unlock()
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
				CallID  string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		n := g.calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		var delta map[string]any
		if n == 1 {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "hand", "type": "function", "function": map[string]string{"name": tool, "arguments": args}}}}
		} else {
			for _, m := range payload.Messages {
				if m.Role == "tool" && m.CallID == "hand" {
					g.mu.Lock()
					g.result = m.Content
					g.mu.Unlock()
				}
			}
			delta = map[string]any{"content": "Continuing from the actual state."}
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	t.Cleanup(g.endpoint.Close)
	engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: g.endpoint.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	g.engine = engine
	g.open()
	if g.bot, err = g.s.store.CreateBot("handover", "", "test-model"); err != nil {
		t.Fatal(err)
	}
	g.conv, _ = g.s.store.GetConversation(g.bot.DMConversationID)
	_, g.run, _, err = g.s.store.AddUserRun(g.conv.ID, g.bot.ID, "Do the synthetic task", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	g.s.enqueue(g.conv, g.run)
	waitForRunStatus(t, g.s, g.run.ID, runWaiting)
	pending, err := g.s.store.PendingQuestions(g.conv.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v %v", pending, err)
	}
	g.q = pending[0]
	return g
}

func (g *handoverRig) open() {
	g.t.Helper()
	s, err := NewServer(Config{DataDir: g.dir, Provider: "test", Engine: g.engine})
	if err != nil {
		g.t.Fatal(err)
	}
	g.guest = &handoverGuest{}
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: g.guest}})
	if g.setup != nil && !g.configured {
		g.setup(s, g.dir)
		g.configured = true
	}
	g.s = s
}

// restart closes the server mid-card and opens a new one on the same data.
func (g *handoverRig) restart() {
	g.t.Helper()
	if err := g.s.Close(); err != nil {
		g.t.Fatal(err)
	}
	if g.calls.Load() != 1 {
		g.t.Fatalf("run did not stay suspended: %d model calls", g.calls.Load())
	}
	g.open()
	waitForRunStatus(g.t, g.s, g.run.ID, runWaiting)
	if still, err := g.s.store.PendingQuestions(g.conv.ID); err != nil || len(still) != 1 || still[0].ID != g.q.ID {
		g.t.Fatalf("card lost across restart: %+v %v", still, err)
	}
}

func (g *handoverRig) post(path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	g.s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func (g *handoverRig) answer(body string) *httptest.ResponseRecorder {
	return g.post("/api/questions/"+g.q.ID+"/answer", body)
}

func (g *handoverRig) control(action string, args any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(map[string]any{"bot_id": g.bot.ID, "action": action, "args": args})
	return g.post("/api/computers/firecracker/actions", string(raw))
}

// finish waits for the resumed run and returns what the model was told.
func (g *handoverRig) finish() map[string]any {
	g.t.Helper()
	waitForRunStatus(g.t, g.s, g.run.ID, "done")
	g.mu.Lock()
	text := g.result
	g.mu.Unlock()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		g.t.Fatalf("model result is not a handover result: %q", text)
	}
	return out
}

func (g *handoverRig) expireOffline() {
	g.t.Helper()
	if err := g.s.Close(); err != nil {
		g.t.Fatal(err)
	}
	st, err := OpenStore(g.dir)
	if err != nil {
		g.t.Fatal(err)
	}
	if _, err = st.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), g.q.ID); err != nil {
		g.t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		g.t.Fatal(err)
	}
	g.open()
}

func (g *handoverRig) noLeak(secrets ...string) {
	g.t.Helper()
	dump := dumpDatabase(g.t, g.s)
	g.mu.Lock()
	haystacks := map[string]string{"database": dump, "model requests": strings.Join(g.requests, "\n"), "model result": g.result}
	g.mu.Unlock()
	for name, hay := range haystacks {
		for _, secret := range secrets {
			if strings.Contains(hay, secret) {
				g.t.Fatalf("%q reached the %s", secret, name)
			}
		}
	}
}

// dumpDatabase renders every column of every table, so a leak anywhere fails.
func dumpDatabase(t *testing.T, s *Server) string {
	t.Helper()
	tables, err := s.store.db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		_ = tables.Scan(&n)
		names = append(names, n)
	}
	tables.Close()
	var out strings.Builder
	for _, name := range names {
		rows, err := s.store.db.Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			continue
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if rows.Scan(ptrs...) != nil {
				break
			}
			for _, v := range vals {
				switch x := v.(type) {
				case []byte:
					out.Write(x)
				default:
					fmt.Fprint(&out, x)
				}
				out.WriteByte('|')
			}
			out.WriteByte('\n')
		}
		rows.Close()
	}
	return out.String()
}

func controlID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Result struct {
			ID string `json:"control_id"`
		} `json:"result"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &env) != nil || env.Result.ID == "" {
		t.Fatalf("acquire: %d %s", rec.Code, rec.Body.String())
	}
	return env.Result.ID
}

// handoverGuest answers every guest call and keeps what it was sent.
type handoverGuest struct {
	mu      sync.Mutex
	actions []computer.Action
}

func (f *handoverGuest) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		var action computer.Action
		if json.NewDecoder(r.Body).Decode(&action) == nil && action.Name != "" {
			f.mu.Lock()
			f.actions = append(f.actions, action)
			f.mu.Unlock()
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"lease_ms":15000,"seq":1}}`))}, nil
}

const takeoverArgs = `{"question":"Enter the verification code for the synthetic account.","site":"login.example.test/signin?step=2#frag"}`

func TestTakeoverSuspendsSurvivesRestartAndIsNotStolenByIdleRelease(t *testing.T) {
	g := newHandoverRig(t, "request_takeover", takeoverArgs, nil)
	if g.q.Type != questionTakeover || g.q.SourceURL != "https://login.example.test" || g.q.Status != questionPending {
		t.Fatalf("card: %+v", g.q)
	}
	if g.q.ExpiresAt == "" {
		t.Fatal("takeover card never expires")
	}
	g.restart()
	defer g.s.Close()

	// Contrast: an ordinary human hold is yielded to a waiting Bot when idle.
	plain := controlID(t, g.control("desktop.control.acquire", map[string]any{}))
	g.s.computerOwnerMu.Lock()
	held := g.s.computerControls[g.bot.ID]
	g.s.desktopWaiters = append(g.s.desktopWaiters, &desktopWaiter{BotID: g.bot.ID, RunID: "waiting-run", QueuedAt: now()})
	g.s.computerOwnerMu.Unlock()
	held.mu.Lock()
	held.lastActivity = time.Now().Add(-time.Hour)
	held.mu.Unlock()
	if rec := g.control("desktop.control.renew", map[string]any{"control_id": plain}); !strings.Contains(rec.Body.String(), `"released":true`) {
		t.Fatalf("contrast hold was not idle-released: %s", rec.Body.String())
	}
	g.s.computerOwnerMu.Lock()
	g.s.desktopWaiters = nil
	g.s.computerOwnerMu.Unlock()

	// The handover hold is bound to the open card and is not yielded.
	id := controlID(t, g.control("desktop.control.acquire", map[string]any{"handover_question": g.q.ID}))
	g.s.computerOwnerMu.Lock()
	held = g.s.computerControls[g.bot.ID]
	g.s.computerOwnerMu.Unlock()
	g.s.computerOwnerMu.Lock()
	g.s.desktopWaiters = []*desktopWaiter{{BotID: g.bot.ID, RunID: "waiting-run", QueuedAt: now()}}
	g.s.computerOwnerMu.Unlock()
	held.mu.Lock()
	held.lastActivity = time.Now().Add(-time.Hour)
	held.mu.Unlock()
	for i := 0; i < 3; i++ {
		if rec := g.control("desktop.control.renew", map[string]any{"control_id": id}); rec.Code != 200 || strings.Contains(rec.Body.String(), `"released":true`) {
			t.Fatalf("handover hold was stolen: %d %s", rec.Code, rec.Body.String())
		}
	}
	// Keystrokes and clipboard go to the guest only.
	g.control("desktop.control.input", map[string]any{"control_id": id, "seq": 1, "events": []map[string]any{{"type": "text", "text": typedSecret}, {"type": "keydown", "key": "a"}, {"type": "keyup", "key": "a"}}})
	g.control("desktop.control.clipboard.write", map[string]any{"control_id": id, "text": clipboardSecret})
	g.control("desktop.control.clipboard.read", map[string]any{"control_id": id})
	if run, _ := g.s.store.GetRun(g.run.ID); run.Status != runWaiting {
		t.Fatalf("run resumed while the person held control: %s", run.Status)
	}

	// Only the enumerated word is accepted; typed content is refused.
	if rec := g.answer(`{"result":"` + typedSecret + `"}`); rec.Code != 400 {
		t.Fatalf("free text accepted: %d %s", rec.Code, rec.Body.String())
	}
	if rec := g.answer(`{"result":"done","text":"` + typedSecret + `","fields":{"x":"` + clipboardSecret + `"}}`); rec.Code != 200 || strings.Contains(rec.Body.String(), typedSecret) {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	result := g.finish()
	if result["status"] != "handed_back" || result["completed"] != true {
		t.Fatalf("done result: %v", result)
	}
	g.s.computerOwnerMu.Lock()
	remaining := g.s.computerControls[g.bot.ID]
	g.s.computerOwnerMu.Unlock()
	g.s.computerOwnerMu.Lock()
	g.s.desktopWaiters = nil
	g.s.computerOwnerMu.Unlock()
	if remaining != nil || !g.s.claimComputerOwner(g.bot.ID, "next-run") {
		t.Fatal("control was not handed back when the card closed")
	}
	g.noLeak(typedSecret, clipboardSecret)
	g.guest.mu.Lock()
	reachedGuest := false
	for _, action := range g.guest.actions {
		raw, _ := json.Marshal(action)
		if bytes.Contains(raw, []byte(typedSecret)) {
			reachedGuest = true
		}
	}
	g.guest.mu.Unlock()
	if !reachedGuest {
		t.Fatal("test did not exercise the input path to the guest")
	}
}

func TestTakeoverOutcomesResumeTheRunWithTheMatchingStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		act    func(*handoverRig)
		status string
		done   any
	}{
		{"cannot", func(g *handoverRig) {
			if rec := g.answer(`{"result":"cannot"}`); rec.Code != 200 {
				t.Fatal(rec.Body.String())
			}
		}, "declined", false},
		{"cancel", func(g *handoverRig) {
			rec := httptest.NewRecorder()
			g.s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/questions/"+g.q.ID, nil))
			if rec.Code != 200 {
				t.Fatal(rec.Body.String())
			}
		}, questionCancelled, nil},
		{"expiry", func(g *handoverRig) { g.expireOffline() }, "expired", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newHandoverRig(t, "request_takeover", takeoverArgs, nil)
			defer func() { g.s.Close() }()
			g.restart()
			// The person is mid-takeover when the card ends.
			controlID(t, g.control("desktop.control.acquire", map[string]any{"handover_question": g.q.ID}))
			tc.act(g)
			result := g.finish()
			if result["status"] != tc.status || result["completed"] != tc.done {
				t.Fatalf("result: %v", result)
			}
			g.s.computerOwnerMu.Lock()
			held := g.s.computerControls[g.bot.ID]
			g.s.computerOwnerMu.Unlock()
			if held != nil {
				t.Fatal("the resumed run would queue behind the person's old hold")
			}
		})
	}
}

func TestTakeoverAcquireRefusesAClosedCard(t *testing.T) {
	g := newHandoverRig(t, "request_takeover", takeoverArgs, nil)
	defer g.s.Close()
	if rec := g.answer(`{"result":"done"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	g.finish()
	if rec := g.control("desktop.control.acquire", map[string]any{"handover_question": g.q.ID}); rec.Code != 409 || !strings.Contains(rec.Body.String(), "handover_closed") {
		t.Fatalf("closed card granted control: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTakeoverNeedsAWebsiteAndAComputer(t *testing.T) {
	if _, err := normalizeQuestionInput(askQuestionInput{Question: "Sign in", Type: questionTakeover}); err == nil {
		t.Fatal("takeover without a site accepted")
	}
	if _, err := normalizeQuestionInput(askQuestionInput{Question: "Sign in", Type: questionTakeover, SourceURL: "https://login.example.test", Options: []QuestionOption{{ID: "a", Label: "a"}}}); err == nil {
		t.Fatal("takeover with options accepted")
	}
	s, c, run := questionFixture(t)
	defer s.Close()
	server := &Server{store: s}
	for _, tool := range server.questionTools(c, run) {
		if tool.Name == "request_takeover" {
			t.Fatal("takeover offered with no shared computer")
		}
	}
}

func connectionSetup(connected bool) func(*Server, string) {
	return func(s *Server, dir string) {
		if err := s.extensions.SaveMCP("notes", extensionsFixture("https://mcp.example.test"), false); err != nil {
			panic(err)
		}
		if connected {
			connectService(dir, "notes")
		}
	}
}

func connectService(dir, name string) {
	token := filepath.Join(dir, ".oauth-"+name+".json")
	if err := os.WriteFile(token, []byte(`{"access_token":"synthetic-access-token","token_type":"bearer"}`), 0o600); err != nil {
		panic(err)
	}
}

const connectionArgs = `{"service":"notes","question":"Connect Notes so I can file the summary."}`

func TestConnectionCardSuccessFailureAndExpiry(t *testing.T) {
	t.Run("success needs a real connection", func(t *testing.T) {
		g := newHandoverRig(t, "request_connection", connectionArgs, connectionSetup(false))
		if g.q.Type != questionConnection || g.q.Target != "notes" {
			t.Fatalf("card: %+v", g.q)
		}
		g.restart()
		defer g.s.Close()
		// The browser's word is not enough: nothing is connected yet.
		if rec := g.answer(`{"result":"connected"}`); rec.Code != 409 {
			t.Fatalf("unverified success accepted: %d %s", rec.Code, rec.Body.String())
		}
		connectService(g.dir, "notes")
		if rec := g.answer(`{"result":"connected"}`); rec.Code != 200 {
			t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
		}
		if result := g.finish(); result["status"] != "connected" || result["connected"] != true {
			t.Fatalf("result: %v", result)
		}
		g.noLeak("synthetic-access-token")
	})
	t.Run("failure", func(t *testing.T) {
		g := newHandoverRig(t, "request_connection", connectionArgs, connectionSetup(false))
		defer g.s.Close()
		if rec := g.answer(`{"result":"failed"}`); rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		if result := g.finish(); result["status"] != "failed" || result["connected"] != false {
			t.Fatalf("result: %v", result)
		}
	})
	t.Run("declined", func(t *testing.T) {
		g := newHandoverRig(t, "request_connection", connectionArgs, connectionSetup(false))
		defer g.s.Close()
		if rec := g.answer(`{"result":"declined"}`); rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		if result := g.finish(); result["status"] != "declined" {
			t.Fatalf("result: %v", result)
		}
	})
	t.Run("expiry", func(t *testing.T) {
		g := newHandoverRig(t, "request_connection", connectionArgs, connectionSetup(false))
		defer func() { g.s.Close() }()
		g.expireOffline()
		if result := g.finish(); result["status"] != "expired" {
			t.Fatalf("result: %v", result)
		}
	})
}

func TestConnectionToolValidatesTheService(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	bot, err := srv.store.CreateBot("validate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := srv.store.GetConversation(bot.DMConversationID)
	run, err := srv.store.AddRun(c.ID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.extensions.SaveMCP("notes", extensionsFixture("https://mcp.example.test"), false); err != nil {
		t.Fatal(err)
	}
	if err := srv.extensions.SaveMCP("plain", extensions.MCPServerConfig{URL: "https://plain.example.test/mcp"}, false); err != nil {
		t.Fatal(err)
	}
	var tool Tool
	for _, candidate := range srv.questionTools(c, run) {
		if candidate.Name == "request_connection" {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("request_connection is not registered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, args := range []string{`{"service":"missing","question":"x"}`, `{"service":"plain","question":"x"}`} {
		if _, err := tool.Execute(ctx, json.RawMessage(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	connectService(dir, "notes")
	out, err := tool.Execute(ctx, json.RawMessage(connectionArgs))
	if err != nil || !strings.Contains(out, "already_connected") {
		t.Fatalf("connected service raised a card: %q %v", out, err)
	}
	if pending, _ := srv.store.PendingQuestions(c.ID); len(pending) != 0 {
		t.Fatalf("card for a connected service: %+v", pending)
	}
}

func TestOAuthRequiredPointsAtTheCardOnlyWhereAPersonCanAnswer(t *testing.T) {
	visible := extensionOAuthRequired(Conversation{UserVisible: true})
	hidden := extensionOAuthRequired(Conversation{UserVisible: false})
	if !strings.Contains(fmt.Sprint(visible["message"]), "request_connection") || strings.Contains(fmt.Sprint(visible["message"]), "Settings") {
		t.Fatalf("visible: %v", visible)
	}
	if strings.Contains(fmt.Sprint(hidden["message"]), "request_connection") || !strings.Contains(fmt.Sprint(hidden["message"]), "Settings") {
		t.Fatalf("hidden conversation was sent to a card it cannot show: %v", hidden)
	}
}

func TestHandoverResultCarriesOnlyAStatusWord(t *testing.T) {
	for _, q := range []Question{
		{Type: questionTakeover, Status: questionAnswered, Answer: json.RawMessage(`"done"`)},
		{Type: questionTakeover, Status: questionAnswered, Answer: json.RawMessage(`"cannot"`)},
		{Type: questionConnection, Status: questionAnswered, Answer: json.RawMessage(`"connected"`)},
		{Type: questionTakeover, Status: questionExpired},
		{Type: questionConnection, Status: questionCancelled},
	} {
		var out map[string]any
		if err := json.Unmarshal([]byte(handoverResult(q)), &out); err != nil || out["status"] == "" || len(out) > 3 {
			t.Fatalf("%+v -> %v %v", q, out, err)
		}
	}
	if handoverAnswerAllowed(questionTakeover, "connected") || handoverAnswerAllowed(questionConnection, "done") || handoverAnswerAllowed(questionTakeover, "anything typed") {
		t.Fatal("answer vocabulary is open")
	}
}

// The person is still in control when the card expires: the next heartbeat
// hands the desktop back instead of holding it for a run that has moved on.
func TestTakeoverHoldEndsWhenTheCardExpiresUnderIt(t *testing.T) {
	g := newHandoverRig(t, "request_takeover", takeoverArgs, nil)
	defer g.s.Close()
	id := controlID(t, g.control("desktop.control.acquire", map[string]any{"handover_question": g.q.ID}))
	if _, err := g.s.store.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), g.q.ID); err != nil {
		t.Fatal(err)
	}
	if rec := g.control("desktop.control.renew", map[string]any{"control_id": id}); !strings.Contains(rec.Body.String(), "handover_ended") {
		t.Fatalf("hold outlived its card: %s", rec.Body.String())
	}
	if rec := g.control("desktop.control.acquire", map[string]any{"handover_question": g.q.ID}); rec.Code != 409 {
		t.Fatalf("expired card granted control: %d", rec.Code)
	}
}

// A run parked on a takeover card keeps the page the person is about to act
// on; an ordinary run end still closes the tabs the run opened.
func TestRunParkedOnTakeoverKeepsItsBrowserTabs(t *testing.T) {
	for _, parked := range []bool{true, false} {
		guest := &handoverGuest{}
		client, _ := computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: guest}})
		store, _, run := questionFixture(t)
		if parked {
			in, err := normalizeQuestionInput(askQuestionInput{Question: "Sign in", Type: questionTakeover, SourceURL: "https://login.example.test"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.CreateQuestion(run.ConversationID, run, in); err != nil {
				t.Fatal(err)
			}
		}
		s := &Server{store: store, microVM: client, computerOwners: map[string]string{run.BotID: run.ID}}
		s.maintainComputerHold(context.Background(), run)()
		guest.mu.Lock()
		var release computer.Action
		for _, a := range guest.actions {
			if a.Name == "desktop.release" {
				release = a
			}
		}
		guest.mu.Unlock()
		closes := strings.Contains(string(release.Args), "close_run_tabs")
		if release.Name == "" || closes == parked {
			t.Fatalf("parked=%v release=%s", parked, release.Args)
		}
		store.Close()
	}
}
