package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/JackZhao98/tofibot/internal/computer"
)

type inputControlTransport struct {
	mu      sync.Mutex
	actions []computer.Action
}

func (f *inputControlTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var action computer.Action
	_ = json.NewDecoder(r.Body).Decode(&action)
	f.mu.Lock()
	f.actions = append(f.actions, action)
	f.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"lease_ms":15000,"seq":1}}`))}, nil
}

func TestComputerControlUsesExistingOwnerAndPreservesViewer(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fake := &inputControlTransport{}
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: fake}})
	bot, err := s.store.CreateBot("controlled", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	call := func(action string, args any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"bot_id": bot.ID, "action": action, "args": args})
		req := httptest.NewRequest(http.MethodPost, "http://tofi.local/api/computers/firecracker/actions", bytes.NewReader(raw))
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		return response
	}
	acquire := call("desktop.control.acquire", map[string]any{})
	if acquire.Code != 200 {
		t.Fatalf("acquire %d %s", acquire.Code, acquire.Body.String())
	}
	var envelope struct {
		Result struct {
			ID string `json:"control_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(acquire.Body.Bytes(), &envelope); err != nil || envelope.Result.ID == "" {
		t.Fatal("missing control id")
	}
	id := envelope.Result.ID
	if s.claimComputerOwner(bot.ID, "model-run") {
		t.Fatal("model stole human control")
	}
	if response := call("desktop.capture", map[string]any{}); response.Code != 200 {
		t.Fatalf("viewer blocked: %s", response.Body.String())
	}
	if response := call("desktop.control.acquire", map[string]any{}); response.Code != 409 {
		t.Fatalf("second tab acquired: %d", response.Code)
	}
	for _, action := range []string{"desktop.control.renew", "desktop.control.clipboard.read", "desktop.control.clipboard.write"} {
		if response := call(action, map[string]any{"control_id": id, "text": "中文"}); response.Code != 200 {
			t.Fatalf("%s: %d %s", action, response.Code, response.Body.String())
		}
	}
	if response := call("desktop.control.input", map[string]any{"control_id": id, "seq": 1, "events": []map[string]any{{"type": "reset"}}}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if response := call("desktop.control.release", map[string]any{"control_id": uuid.NewString()}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if s.claimComputerOwner(bot.ID, "model-run") {
		t.Fatal("foreign release removed ownership")
	}
	if response := call("desktop.control.release", map[string]any{"control_id": id}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if !s.claimComputerOwner(bot.ID, "model-run") {
		t.Fatal("release did not free ownership")
	}
	s.releaseComputerOwner(bot.ID, "model-run")
	if response := call("desktop.control.input", map[string]any{"control_id": id, "seq": 2}); response.Code != 409 {
		t.Fatal("expired input accepted")
	}
	call("desktop.control.acquire", map[string]any{})
	s.computerOwnerMu.Lock()
	current := s.computerControls[bot.ID]
	s.computerOwnerMu.Unlock()
	current.mu.Lock()
	current.expires = time.Now().Add(-time.Second)
	current.timer.Reset(time.Millisecond)
	current.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.computerOwnerMu.Lock()
		exists := s.computerControls[bot.ID] != nil
		s.computerOwnerMu.Unlock()
		if !exists {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !s.claimComputerOwner(bot.ID, "model-after-timeout") {
		t.Fatal("expired lease retained owner")
	}
	s.releaseComputerOwner(bot.ID, "model-after-timeout")
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, action := range fake.actions {
		if action.Name != "desktop.capture" && (!strings.HasPrefix(action.RunID, "human-control:") || action.Source != "human") {
			t.Fatalf("wrong identity: %#v", action)
		}
	}
}

func TestComputerControlRejectsGraphicalOwnerAndCrossSite(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: &inputControlTransport{}}})
	bot, _ := s.store.CreateBot("busy", "", "model")
	_, _, _, err = s.store.AddUserRun(bot.DMConversationID, bot.ID, "work", "one")
	if err != nil {
		t.Fatal(err)
	}
	s.claimComputerOwner(bot.ID, "graphical-run")
	raw, _ := json.Marshal(map[string]any{"bot_id": bot.ID, "action": "desktop.control.acquire", "args": map[string]any{}})
	for _, crossSite := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://tofi.local/api/computers/firecracker/actions", bytes.NewReader(raw))
		if crossSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 409
		if crossSite {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
}
