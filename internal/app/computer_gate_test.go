package app

// The release gate of computer-decoupling (S7). Production runtime, workers,
// SQLite, file store, outbox, attach and detach endpoints; only the model
// endpoint (scripted) and the computer's guest (the real guest handler on a
// temporary directory) are local doubles. What it proves:
//
//  1. with the VM detached a Bot chats, uses server tools (memory, schedule,
//     reading an uploaded file), and its files are delivered from the server;
//  2. reattaching brings the computer tools back, and nothing on the server moved;
//  3. detaching during a run ends the run's computer call with an ordinary tool
//     error (computer_detached) instead of hanging, and the run finishes.
//
// Manual script for the same gate on a real host (admin session, one account with a VM):
//  1. Upload a file in a chat, ask the Bot to write a file to /workspace/home/outbox, note the Bot's memories and schedules.
//  2. POST /api/admin/accounts/<id>/computer/detach {"wait_seconds":30}. Expect 200, status "detached", and the outbox file posted in the chat as a message with the file attached.
//  3. In the same chat: say hello, ask for a memory and a reminder, ask it to read the uploaded file, download the file. All work; the Bot does not offer shell or browser.
//  4. POST .../computer/attach. Expect status "attached"; ask the Bot to run `ls /workspace`; memories, schedule and files are as before.
//  5. Start a long shell command (sleep 60) in a chat, then detach with wait_seconds 5. Expect the step to end with a detached-computer error within seconds and the Bot to answer anyway.
//  6. Restart the App: the VM stays detached until step 4's call.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/google/uuid"
)

// gateVM is the VM as the server sees it: the real guest behind a Provider, and
// a legacy blob store for files that exist only on the computer.
type gateVM struct {
	computer.Provider
	blobs *legacyBlobs
}

func (g gateVM) PutBlob(ctx context.Context, id string, b []byte) error {
	return g.blobs.PutBlob(ctx, id, b)
}
func (g gateVM) GetBlob(ctx context.Context, id string) ([]byte, error) {
	return g.blobs.GetBlob(ctx, id)
}
func (g gateVM) DeleteBlob(ctx context.Context, id string) error { return g.blobs.DeleteBlob(ctx, id) }

// gateModel is a scripted chat-completions endpoint. A scenario is a list of
// steps; each model call takes the next step.
type gateModel struct {
	mu        sync.Mutex
	scenario  string
	step      int
	toolsSeen map[string][]string // scenario -> tool names offered at its first call
	results   map[string]string   // tool call id -> content the model received
	notesID   string
	outbox    string
}

func (m *gateModel) use(name string) {
	m.mu.Lock()
	m.scenario, m.step = name, 0
	m.mu.Unlock()
}

func (m *gateModel) result(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.results[id]
}

func (m *gateModel) offered(scenario string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, n := range m.toolsSeen[scenario] {
		out[n] = true
	}
	return out
}

func (m *gateModel) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
				CallID  string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		m.mu.Lock()
		m.step++
		n, scenario := m.step, m.scenario
		if n == 1 {
			var names []string
			for _, tool := range payload.Tools {
				names = append(names, tool.Function.Name)
			}
			m.toolsSeen[scenario] = names
		}
		for _, msg := range payload.Messages {
			if msg.Role == "tool" {
				m.results[msg.CallID] = msg.Content
			}
		}
		notes, outbox := m.notesID, m.outbox
		m.mu.Unlock()
		call := func(id, name, args string) map[string]any {
			return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}
		}
		text := func(s string) map[string]any { return map[string]any{"content": s} }
		listOutbox, _ := json.Marshal(map[string]string{"action": "files.list", "path": outbox})
		var delta map[string]any
		switch scenario + fmt.Sprint(n) {
		case "chat1":
			delta = text("Hello. I am here and working.")
		case "server1":
			delta = call("mem", "save_memory", `{"title":"Gate fact","description":"A fact saved while the computer was detached","content":"The report is due on Friday."}`)
		case "server2":
			at := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
			delta = call("sched", "create_schedule", fmt.Sprintf(`{"title":"Friday reminder","description":"Remind about the report","content":"Remind the person that the report is due.","kind":"once","run_at":%q,"timezone":"UTC"}`, at))
		case "server3":
			delta = call("read", "read_attachment", fmt.Sprintf(`{"attachment_id":%q}`, notes))
		case "server4":
			delta = text("Saved the fact, scheduled the reminder and read your notes.")
		case "computer1", "slow1":
			delta = call("files", "computer_files", string(listOutbox))
		case "computer2", "slow2":
			delta = text("Finished the computer step.")
		default:
			delta = text("unexpected step " + scenario + fmt.Sprint(n))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	})
}

func fileSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestReleaseGateBotWorksWithoutTheComputer(t *testing.T) {
	model := &gateModel{toolsSeen: map[string][]string{}, results: map[string]string{}}
	endpoint := httptest.NewServer(model.handler(t))
	defer endpoint.Close()
	engine, err := runtime.New(runtime.Config{Provider: "openai_completions", BaseURL: endpoint.URL + "/v1", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}

	// The computer: the real guest on a temporary directory, with a switch that
	// makes file listing hang until the call is canceled.
	root := t.TempDir()
	g, err := guest.NewWithIdleTimeout(root, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close(context.Background()) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	oldOutbox := guestOutboxDir
	guestOutboxDir = filepath.Join(root, "home", "outbox")
	t.Cleanup(func() { guestOutboxDir = oldOutbox })
	if err = os.MkdirAll(guestOutboxDir, 0o770); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(guestOutboxDir, "quarterly.txt"), []byte("quarterly numbers, made on the computer"), 0o660); err != nil {
		t.Fatal(err)
	}
	var hang atomic.Bool
	var hanging atomic.Int32
	client, err := computer.New(computer.Config{Socket: "/tmp/tofi-gate.sock", Client: &http.Client{Transport: reviewTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/info" {
			rec := httptest.NewRecorder()
			rec.WriteString(`{"kind":"firecracker","state":"ready","workspace_root":"/workspace"}`)
			return rec.Result(), nil
		}
		var a computer.Action
		if err := json.NewDecoder(req.Body).Decode(&a); err != nil {
			return nil, err
		}
		if hang.Load() && a.Name == "files.list" && !strings.HasPrefix(a.RunID, "detach-sync:") {
			hanging.Add(1)
			defer hanging.Add(-1)
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		raw, _ := json.Marshal(a)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewReader(raw)).WithContext(req.Context()))
		return w.Result(), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	blobs := newLegacyBlobs()
	vm := gateVM{Provider: client, blobs: blobs}

	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, Provider: "test", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.computerFactory = func() (computer.Provider, error) { return vm, nil }
	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://tofi.local"+path, strings.NewReader(body)))
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://tofi.local"+path, nil))
		return rec
	}
	if rec := post("/api/admin/computer/attach", ""); rec.Code != 200 || s.computer() == nil {
		t.Fatalf("attach: %d %s", rec.Code, rec.Body.String())
	}

	bot, err := s.store.CreateBot("gate", "", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	notesBody := []byte("Notes from the person: the report is due on Friday.")
	notes, err := s.store.AddAttachment(conv.ID, "notes.txt", "text/plain", bytes.NewReader(notesBody))
	if err != nil {
		t.Fatal(err)
	}
	model.mu.Lock()
	model.notesID, model.outbox = notes.ID, guestOutboxDir
	model.mu.Unlock()
	onlyOnVM := legacyRow(t, s.store, conv.ID, blobs, "only-on-vm.txt", []byte("lives on the computer only"))
	// The notes ride on a person's message so the Bot may read them.
	_, firstRuns, _, err := s.store.AddUserRunsWithAttachments(conv.ID, "Here are my notes", "gate-notes", []runSpec{{BotID: bot.ID}}, []string{notes.ID})
	if err != nil {
		t.Fatal(err)
	}
	model.use("chat")
	s.enqueue(conv, firstRuns[0])
	waitFor(t, s, firstRuns[0].ID, "done")

	runScenario := func(name, text string) Run {
		t.Helper()
		model.use(name)
		_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, text, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		s.enqueue(conv, run)
		return run
	}

	// ---- attached: computer tools are offered and work ----
	run := runScenario("computer", "list the outbox")
	waitFor(t, s, run.ID, "done")
	if offered := model.offered("computer"); !offered["computer_files"] || !offered["publish_file"] {
		t.Fatalf("attached: computer tools missing from %v", offered)
	}
	if res := model.result("files"); !strings.Contains(res, "quarterly.txt") {
		t.Fatalf("computer call result: %s", res)
	}

	// ---- detach: the outbox is copied to the server first ----
	// The file of the run above was copied when that run ended. This one was
	// written afterwards, so only the detach can save it.
	lateBody := []byte("written after the last run, before the detach")
	if err = os.WriteFile(filepath.Join(guestOutboxDir, "late.txt"), lateBody, 0o660); err != nil {
		t.Fatal(err)
	}
	rec := post("/api/admin/computer/detach", `{"wait_seconds":2}`)
	if rec.Code != 200 || s.computer() != nil {
		t.Fatalf("detach: %d %s", rec.Code, rec.Body.String())
	}
	var detach struct {
		Detach DetachReport `json:"detach"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detach)
	if !detach.Detach.Drained || detach.Detach.Outbox.Synced != 1 {
		t.Fatalf("detach report: %s", rec.Body.String())
	}
	var shown int
	s.store.db.QueryRow(`SELECT COUNT(*) FROM attachment_messages am JOIN attachments a ON a.id=am.attachment_id WHERE a.name='late.txt'`).Scan(&shown)
	if shown != 1 {
		t.Fatalf("the saved file is not shown in the chat: %d", shown)
	}
	var outboxID string
	if err = s.store.db.QueryRow(`SELECT id FROM attachments WHERE origin='outbox' AND name='late.txt'`).Scan(&outboxID); err != nil {
		t.Fatalf("the outbox file was not copied before the detach: %v", err)
	}

	// ---- detached: chat, server tools, schedule, files ----
	run = runScenario("chat", "are you there?")
	waitFor(t, s, run.ID, "done")
	if offered := model.offered("chat"); offered["computer_files"] || offered["computer_shell"] || offered["publish_file"] {
		t.Fatalf("detached: computer tools still offered: %v", offered)
	}
	if !lastAssistantContains(s, conv.ID, "working") {
		t.Fatal("the Bot did not answer while detached")
	}
	run = runScenario("server", "save the due date, remind me and read my notes")
	waitFor(t, s, run.ID, "done")
	var memories, schedules int
	s.store.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE content LIKE '%due on Friday%'`).Scan(&memories)
	s.store.db.QueryRow(`SELECT COUNT(*) FROM schedules`).Scan(&schedules)
	if memories != 1 || schedules != 1 {
		t.Fatalf("server tools while detached: memories=%d schedules=%d (mem=%q sched=%q)", memories, schedules, model.result("mem"), model.result("sched"))
	}
	if !strings.Contains(model.result("read"), "report is due on Friday") {
		t.Fatalf("reading an uploaded file while detached: %q", model.result("read"))
	}
	// Files are delivered from the server; a file only on the computer says so.
	for id, want := range map[string][]byte{notes.ID: notesBody, outboxID: lateBody} {
		rec = get("/api/attachments/" + id)
		if rec.Code != 200 || fileSHA(rec.Body.Bytes()) != fileSHA(want) {
			t.Fatalf("download of %s while detached: %d", id, rec.Code)
		}
	}
	if rec = get("/api/attachments/" + onlyOnVM.ID); rec.Code != 409 || !strings.Contains(rec.Body.String(), "file_on_detached_computer") {
		t.Fatalf("a file only on the computer: %d %s", rec.Code, rec.Body.String())
	}

	// ---- reattach: the tools come back, the server data did not move ----
	if rec = post("/api/admin/computer/attach", ""); rec.Code != 200 || s.computer() == nil {
		t.Fatalf("reattach: %d %s", rec.Code, rec.Body.String())
	}
	run = runScenario("computer", "list the outbox again")
	waitFor(t, s, run.ID, "done")
	if offered := model.offered("computer"); !offered["computer_files"] || !offered["publish_file"] {
		t.Fatalf("reattached: computer tools missing from %v", offered)
	}
	if rec = get("/api/attachments/" + notes.ID); rec.Code != 200 || fileSHA(rec.Body.Bytes()) != fileSHA(notesBody) {
		t.Fatalf("notes after reattach: %d", rec.Code)
	}
	s.store.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE content LIKE '%due on Friday%'`).Scan(&memories)
	s.store.db.QueryRow(`SELECT COUNT(*) FROM schedules`).Scan(&schedules)
	if memories != 1 || schedules != 1 {
		t.Fatalf("server data after reattach: memories=%d schedules=%d", memories, schedules)
	}

	// ---- detach during a run: the call ends with a tool error, the run finishes ----
	hang.Store(true)
	run = runScenario("slow", "list the outbox, slowly")
	deadline := time.Now().Add(5 * time.Second)
	for hanging.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hanging.Load() == 0 {
		t.Fatal("the run never reached the computer")
	}
	start := time.Now()
	rec = post("/api/admin/computer/detach", `{"wait_seconds":1}`)
	if rec.Code != 200 {
		t.Fatalf("detach during a run: %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detach)
	if detach.Detach.Drained || detach.Detach.Interrupted < 1 {
		t.Fatalf("detach during a run reported %+v", detach.Detach)
	}
	waitFor(t, s, run.ID, "done")
	if time.Since(start) > 8*time.Second {
		t.Fatal("the run hung on the detached computer")
	}
	if res := model.result("files"); !strings.Contains(res, "computer_detached") {
		t.Fatalf("the Bot was not told the computer went away: %q", res)
	}
	hang.Store(false)
	if rec = get("/api/attachments/" + notes.ID); rec.Code != 200 || fileSHA(rec.Body.Bytes()) != fileSHA(notesBody) {
		t.Fatalf("notes after the interrupted run: %d", rec.Code)
	}
}

// waitFor waits for a run to reach a status, with room for a slow machine.
func waitFor(t *testing.T, s *Server, id, want string) Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.store.GetRun(id)
		if err == nil && r.Status == want {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := s.store.GetRun(id)
	t.Fatalf("run %s status=%s error=%q, want %s", id, r.Status, r.Error, want)
	return Run{}
}

func lastAssistantContains(s *Server, conv, text string) bool {
	var content string
	_ = s.store.db.QueryRow(`SELECT content FROM messages WHERE conversation_id=? AND role='assistant' ORDER BY seq DESC LIMIT 1`, conv).Scan(&content)
	return strings.Contains(content, text)
}
