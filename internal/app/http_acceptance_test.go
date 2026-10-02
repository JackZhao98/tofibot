package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type httpAcceptanceClient struct {
	base string
	http *http.Client
}

func newHTTPAcceptanceClient(t *testing.T, base string) *httpAcceptanceClient {
	t.Helper()
	return &httpAcceptanceClient{base: base, http: &http.Client{Timeout: 2 * time.Second}}
}

func (c *httpAcceptanceClient) do(method, path string, body any, headers map[string]string) (int, http.Header, []byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b, err
}

func acceptanceJSON[T any](t *testing.T, body []byte, out *T) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("invalid JSON %q: %v", string(body), err)
	}
}

func acceptanceCreateBot(t *testing.T, c *httpAcceptanceClient, name, origin string) Bot {
	t.Helper()
	headers := map[string]string{}
	if origin != "" {
		headers["Origin"] = origin
	}
	status, _, body, err := c.do(http.MethodPost, "/api/bots", map[string]string{
		"name":         name,
		"instructions": "Answer briefly",
		"model":        "test-model",
	}, headers)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Fatalf("create bot status=%d body=%s", status, body)
	}
	var b Bot
	acceptanceJSON(t, body, &b)
	return b
}

func acceptanceWait(t *testing.T, description string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if f() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
}

func acceptanceWaitChannel(t *testing.T, description string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(4 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

type httpAcceptanceRunEnvelope struct {
	Run Run `json:"run"`
}

type httpAcceptanceRunsEnvelope struct {
	Runs []Run `json:"runs"`
}

type httpAcceptanceMessagesEnvelope struct {
	Messages []Message `json:"messages"`
}

type httpAcceptanceBotsEnvelope struct {
	Bots []Bot `json:"bots"`
}

type httpAcceptanceRecordingEngine struct {
	mu     sync.Mutex
	calls  int
	called chan struct{}
}

func (e *httpAcceptanceRecordingEngine) Run(context.Context, runtime.Request) (runtime.Result, error) {
	e.mu.Lock()
	e.calls++
	if e.calls == 1 {
		close(e.called)
	}
	e.mu.Unlock()
	return runtime.Result{Content: "recorded"}, nil
}

func (e *httpAcceptanceRecordingEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type httpAcceptanceBlockingEngine struct {
	started chan struct{}
	done    chan struct{}
	once    sync.Once
	release chan struct{}
}

func (e *httpAcceptanceBlockingEngine) Run(ctx context.Context, _ runtime.Request) (runtime.Result, error) {
	e.once.Do(func() { close(e.started) })
	defer close(e.done)
	select {
	case <-ctx.Done():
		return runtime.Result{}, ctx.Err()
	case <-e.release:
		return runtime.Result{Content: "late"}, nil
	}
}

type httpAcceptanceRetryEngine struct {
	mu        sync.Mutex
	calls     int
	firstDone chan struct{}
}

func (e *httpAcceptanceRetryEngine) Run(context.Context, runtime.Request) (runtime.Result, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	e.mu.Unlock()
	if call == 1 {
		close(e.firstDone)
		return runtime.Result{}, errors.New("intentional first failure")
	}
	return runtime.Result{Content: "retried"}, nil
}

func (e *httpAcceptanceRetryEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func TestHTTPAcceptanceDirectAccessOriginAndStaticAssets(t *testing.T) {
	uiDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<!doctype html><script src=\"/app.js\"></script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "app.js"), []byte("console.log('ok')"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{
		DataDir:      t.TempDir(),
		Engine:       &httpAcceptanceRecordingEngine{called: make(chan struct{})},
		UIDir:        uiDir,
		PublicOrigin: "https://tofi.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	c := newHTTPAcceptanceClient(t, httpServer.URL)

	status, _, body, err := c.do(http.MethodGet, "/api/bots", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("empty bots status=%d body=%s", status, body)
	}
	var bots httpAcceptanceBotsEnvelope
	acceptanceJSON(t, body, &bots)
	if bots.Bots == nil || len(bots.Bots) != 0 {
		t.Fatalf("expected an empty bot list, got %#v", bots.Bots)
	}
	status, _, body, err = c.do(http.MethodGet, "/api/config", nil, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config status=%d err=%v body=%s", status, err, body)
	}
	status, _, body, err = c.do(http.MethodGet, "/api/auth/codex", nil, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("Codex status=%d err=%v body=%s", status, err, body)
	}

	status, headers, body, err := c.do(http.MethodGet, "/", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || !strings.HasPrefix(headers.Get("Content-Type"), "text/html") {
		t.Fatalf("index status=%d content-type=%q body=%s", status, headers.Get("Content-Type"), body)
	}
	status, headers, body, err = c.do(http.MethodGet, "/app.js", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || !strings.HasPrefix(headers.Get("Content-Type"), "text/javascript") && !strings.HasPrefix(headers.Get("Content-Type"), "application/javascript") {
		t.Fatalf("javascript status=%d content-type=%q body=%s", status, headers.Get("Content-Type"), body)
	}

	status, _, body, err = c.do(http.MethodPost, "/api/bots", map[string]string{"name": "evil"}, map[string]string{"Origin": "https://evil.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusForbidden {
		t.Fatalf("malicious origin status=%d body=%s", status, body)
	}
	acceptanceCreateBot(t, c, "allowed", "https://tofi.example.test")
}

func TestHTTPAcceptanceMessageClientIDIsIdempotent(t *testing.T) {
	e := &httpAcceptanceRecordingEngine{called: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	c := newHTTPAcceptanceClient(t, httpServer.URL)
	b := acceptanceCreateBot(t, c, "idempotent", "")
	payload := map[string]string{"content": "hello", "client_message_id": "client-1"}
	status, _, body, err := c.do(http.MethodPost, "/api/conversations/"+b.DMConversationID+"/messages", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted {
		t.Fatalf("first message status=%d body=%s", status, body)
	}
	var first httpAcceptanceRunEnvelope
	acceptanceJSON(t, body, &first)
	status, _, body, err = c.do(http.MethodPost, "/api/conversations/"+b.DMConversationID+"/messages", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("duplicate message status=%d body=%s", status, body)
	}
	var duplicate httpAcceptanceRunEnvelope
	acceptanceJSON(t, body, &duplicate)
	if first.Run.ID == "" || duplicate.Run.ID != first.Run.ID {
		t.Fatalf("idempotency run IDs first=%q duplicate=%q", first.Run.ID, duplicate.Run.ID)
	}
	acceptanceWaitChannel(t, "one engine invocation", e.called)
	acceptanceWait(t, "one stored run", func() bool {
		status, _, body, err := c.do(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/runs", nil, nil)
		if err != nil || status != http.StatusOK {
			return false
		}
		var runs httpAcceptanceRunsEnvelope
		if json.Unmarshal(body, &runs) != nil {
			return false
		}
		return len(runs.Runs) == 1 && e.callCount() == 1
	})
}

func TestHTTPAcceptanceCancelDoesNotAppendLateAssistant(t *testing.T) {
	e := &httpAcceptanceBlockingEngine{started: make(chan struct{}), done: make(chan struct{}), release: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	c := newHTTPAcceptanceClient(t, httpServer.URL)
	b := acceptanceCreateBot(t, c, "cancel", "")
	status, _, body, err := c.do(http.MethodPost, "/api/conversations/"+b.DMConversationID+"/messages", map[string]string{"content": "stop", "client_message_id": "cancel-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted {
		t.Fatalf("message status=%d body=%s", status, body)
	}
	var submitted httpAcceptanceRunEnvelope
	acceptanceJSON(t, body, &submitted)
	acceptanceWaitChannel(t, "engine start", e.started)
	status, _, body, err = c.do(http.MethodPost, "/api/runs/"+submitted.Run.ID+"/cancel", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", status, body)
	}
	var cancelled Run
	acceptanceJSON(t, body, &cancelled)
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancel response status=%q body=%s", cancelled.Status, body)
	}
	acceptanceWaitChannel(t, "cancelled engine", e.done)
	acceptanceWait(t, "cancelled run without assistant", func() bool {
		status, _, runBody, err := c.do(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/runs", nil, nil)
		if err != nil || status != http.StatusOK {
			return false
		}
		var runs httpAcceptanceRunsEnvelope
		if json.Unmarshal(runBody, &runs) != nil || len(runs.Runs) != 1 || runs.Runs[0].Status != "cancelled" {
			return false
		}
		status, _, msgBody, err := c.do(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/messages", nil, nil)
		if err != nil || status != http.StatusOK {
			return false
		}
		var messages httpAcceptanceMessagesEnvelope
		if json.Unmarshal(msgBody, &messages) != nil {
			return false
		}
		if len(messages.Messages) != 1 {
			return false
		}
		return messages.Messages[0].Role == "user"
	})
}

func TestHTTPAcceptanceRetryCreatesNewRun(t *testing.T) {
	e := &httpAcceptanceRetryEngine{firstDone: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	c := newHTTPAcceptanceClient(t, httpServer.URL)
	b := acceptanceCreateBot(t, c, "retry", "")
	status, _, body, err := c.do(http.MethodPost, "/api/conversations/"+b.DMConversationID+"/messages", map[string]string{"content": "retry me", "client_message_id": "retry-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted {
		t.Fatalf("message status=%d body=%s", status, body)
	}
	var first httpAcceptanceRunEnvelope
	acceptanceJSON(t, body, &first)
	acceptanceWaitChannel(t, "first failed engine", e.firstDone)
	acceptanceWait(t, "failed first run", func() bool {
		status, _, body, err := c.do(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/runs", nil, nil)
		if err != nil || status != http.StatusOK {
			return false
		}
		var runs httpAcceptanceRunsEnvelope
		if json.Unmarshal(body, &runs) != nil || len(runs.Runs) != 1 {
			return false
		}
		return runs.Runs[0].ID == first.Run.ID && runs.Runs[0].Status == "failed"
	})

	status, _, body, err = c.do(http.MethodPost, "/api/runs/"+first.Run.ID+"/retry", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Fatalf("retry status=%d body=%s", status, body)
	}
	var retried Run
	acceptanceJSON(t, body, &retried)
	if retried.ID == "" || retried.ID == first.Run.ID {
		t.Fatalf("retry did not create a new run: first=%q retry=%q", first.Run.ID, retried.ID)
	}
	acceptanceWait(t, "new run completed", func() bool {
		if e.callCount() != 2 {
			return false
		}
		status, _, body, err := c.do(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/runs", nil, nil)
		if err != nil || status != http.StatusOK {
			return false
		}
		var runs httpAcceptanceRunsEnvelope
		if json.Unmarshal(body, &runs) != nil || len(runs.Runs) != 2 {
			return false
		}
		for _, run := range runs.Runs {
			if run.ID == retried.ID {
				return run.Status == "done"
			}
		}
		return false
	})
}
