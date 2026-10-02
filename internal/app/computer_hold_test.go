package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

func TestRunHoldCleanupDoesNotReleaseAnotherRun(t *testing.T) {
	var mu sync.Mutex
	var actions []computer.Action
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a computer.Action
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			t.Error(err)
		}
		mu.Lock()
		actions = append(actions, a)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	client, err := computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: redirectComputerTransport{base: server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client, computerOwners: map[string]string{"bot": "run-a"}}
	a := Run{BotID: "bot", ID: "run-a"}
	if err := s.renewComputerHold(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	// An unrelated run ending must not release the owner or its guest hold.
	s.maintainComputerHold(context.Background(), Run{BotID: "bot", ID: "run-b"})()
	s.maintainComputerHold(context.Background(), a)()
	mu.Lock()
	defer mu.Unlock()
	if len(actions) != 2 || actions[0].Name != "desktop.hold" || actions[1].Name != "desktop.release" || actions[1].RunID != "run-a" {
		t.Fatalf("unexpected lifecycle actions: %#v", actions)
	}
	if s.computerOwners["bot"] != "run-a" {
		t.Fatal("hold cleanup altered app ownership before caller cleanup")
	}
}

func TestRunHoldCleanupReleasesAfterCancellation(t *testing.T) {
	var mu sync.Mutex
	var actions []computer.Action
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a computer.Action
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			t.Error(err)
		}
		mu.Lock()
		actions = append(actions, a)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	client, err := computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: redirectComputerTransport{base: server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client, computerOwners: map[string]string{"bot": "run-a"}}
	run := Run{BotID: "bot", ID: "run-a"}
	if err := s.renewComputerHold(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	stop := s.maintainComputerHold(runCtx, run)
	cancel()
	stop()

	mu.Lock()
	defer mu.Unlock()
	if len(actions) != 2 || actions[0].Name != "desktop.hold" || actions[1].Name != "desktop.release" || actions[1].RunID != run.ID {
		t.Fatalf("cancellation did not release matching hold: %#v", actions)
	}
}

type redirectComputerTransport struct{ base string }

func (r redirectComputerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	target, err := http.NewRequestWithContext(req.Context(), req.Method, r.base+req.URL.Path, req.Body)
	if err != nil {
		return nil, err
	}
	copyReq.URL = target.URL
	return http.DefaultTransport.RoundTrip(copyReq)
}
