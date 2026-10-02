package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestAccountComputerBrokerIdentityFence(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "tofi-account-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	var wrong atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["account_id"] != id || r.URL.Path != "/v1/accounts" {
			t.Error("wrong account/protocol")
		}
		path := filepath.Join(root, id, "control.sock")
		if wrong.Load() {
			path = filepath.Join(root, "personal", "control.sock")
		}
		json.NewEncoder(w).Encode(map[string]any{"account_id": id, "socket": path})
	})}
	go server.Serve(listener)
	defer server.Close()
	g := &AccountGateway{config: Config{AccountProvisionerSocket: socket, AccountComputerSocketRoot: root, AccountComputerDiskGiB: 8}}
	if got, err := g.computerRequest(context.Background(), "ensure", id); err != nil || got != filepath.Join(root, id, "control.sock") {
		t.Fatalf("socket=%s err=%v", got, err)
	}
	// Request/response is synchronous; handler response is complete before changing
	// fixture behavior. The broker must never route a new account to personal.
	wrong.Store(true)
	if _, err := g.computerRequest(context.Background(), "ensure", id); err == nil {
		t.Fatal("accepted personal fallback")
	}
}
