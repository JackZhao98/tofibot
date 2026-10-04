package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type accountComputerEngine struct{}

func (accountComputerEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if strings.HasSuffix(req.RunID, ":memory") {
		return runtime.Result{Content: "synthetic summary"}, nil
	}
	for _, tool := range req.Tools {
		if tool.Name == "computer_shell" {
			output, err := tool.Execute(ctx, json.RawMessage(`{"command":"printf synthetic-account-marker"}`))
			return runtime.Result{Content: output}, err
		}
	}
	return runtime.Result{}, fmt.Errorf("computer tool missing")
}

func TestAccountModelComputerRestartAndLegacySuspension(t *testing.T) {
	sockets, err := os.MkdirTemp("/tmp", "tofi-isolation-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockets)
	var mu sync.Mutex
	actions := map[string][]computer.Action{}
	managers := map[string]*http.Server{}
	listen := func(path string, handler http.Handler) *http.Server {
		ln, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: handler}
		go server.Serve(ln)
		t.Cleanup(func() { server.Close() })
		return server
	}
	listen(filepath.Join(sockets, "broker.sock"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Op    string `json:"op"`
			ID    string `json:"account_id"`
			Quota int    `json:"quota_gib"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if request.Op == "reserve" {
			path := filepath.Join(sockets, request.ID)
			if err := os.Mkdir(path, 0700); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			identity := request.ID
			server := listen(filepath.Join(path, "control.sock"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/info" {
					json.NewEncoder(w).Encode(computer.Info{Kind: "firecracker", State: "ready", ID: identity, WorkspaceRoot: "/workspace"})
					return
				}
				var action computer.Action
				if r.URL.Path != "/v1/action" || json.NewDecoder(r.Body).Decode(&action) != nil {
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				actions[identity] = append(actions[identity], action)
				mu.Unlock()
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]string{"account": identity}})
			}))
			mu.Lock()
			managers[identity] = server
			mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]string{"account_id": request.ID, "socket": filepath.Join(sockets, request.ID, "control.sock")})
	}))
	cfg := Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true, AccountProvisionerSocket: filepath.Join(sockets, "broker.sock"), AccountComputerSocketRoot: sockets, AccountComputerDiskGiB: 8}
	g, err := NewAccountGateway(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { g.Close() }()
	g.runtimeFactory = func(c Config) (*Server, error) { c.Engine = accountComputerEngine{}; return NewServer(c) }
	admin, err := g.create(context.Background(), "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := g.workspace(admin)
	if err != nil {
		t.Fatal(err)
	}
	if legacy == g.root || legacy.scheduler == nil || g.root.scheduler != nil {
		t.Fatal("control plane executes legacy workspace jobs")
	}
	var users []Account
	var bots []Bot
	var cookies []*http.Cookie
	for i := 0; i < 2; i++ {
		a, err := g.create(context.Background(), fmt.Sprintf("user-%d", i), "", "SyntheticPassword123!", false, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID); err != nil {
			t.Fatal(err)
		}
		s, err := g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		bot, err := s.store.CreateBot("isolated", "", "synthetic-model")
		if err != nil {
			t.Fatal(err)
		}
		cookie := accountCookie(t, g, a)
		w := accountRequest(g, http.MethodPost, "/api/conversations/"+bot.DMConversationID+"/messages", `{"content":"Use the computer tool","client_message_id":"synthetic-model-task"}`, cookie)
		if w.Code != 202 {
			t.Fatalf("message %d %s", w.Code, w.Body.String())
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			messages, _, err := s.store.Messages(bot.DMConversationID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, m := range messages {
				if m.Role == "assistant" && strings.Contains(m.Content, a.ID) {
					found = true
				}
			}
			if found {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("account %s did not persist own computer result: %+v", a.ID, messages)
			}
			time.Sleep(10 * time.Millisecond)
		}
		users = append(users, a)
		bots = append(bots, bot)
		cookies = append(cookies, cookie)
	}
	mu.Lock()
	for i, a := range users {
		seen := false
		for _, action := range actions[a.ID] {
			if action.Name == "shell.exec" {
				seen = true
				if action.BotID != bots[i].ID {
					t.Error("model action crossed account")
				}
			}
		}
		if !seen {
			t.Error("no computer action")
		}
	}
	mu.Unlock()
	if w := accountRequest(g, "GET", "/api/conversations/"+bots[0].DMConversationID+"/messages", "", cookies[1]); w.Code != 404 {
		t.Fatalf("cross account read %d", w.Code)
	}
	if w := accountRequest(g, "PUT", "/api/computer/resources", `{"disk_gib":1024}`, cookies[0]); w.Code != 403 {
		t.Fatalf("quota bypass %d", w.Code)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET role='admin' WHERE id=?`, users[0].ID); err != nil {
		t.Fatal(err)
	}
	if w := accountRequest(g, "PATCH", "/api/admin/accounts/"+admin.ID, `{"disabled":true}`, cookies[0]); w.Code != 200 {
		t.Fatalf("legacy disable %d %s", w.Code, w.Body.String())
	}
	if !legacy.closing {
		t.Fatal("disabled legacy background runtime still running")
	}
	if _, err := g.root.store.db.Exec(`SELECT 1`); err != nil {
		t.Fatal("legacy disable closed account registry")
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	g, err = NewAccountGateway(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g.workspaces[admin.ID] != nil {
		t.Fatal("disabled legacy restored")
	}
	for i, a := range users {
		s := g.workspaces[a.ID]
		if s == nil || s.scheduler == nil {
			t.Fatal("enabled background runtime requires login")
		}
		if _, err = s.store.GetBot(bots[i].ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.GetBot(bots[1-i].ID); err == nil {
			t.Fatal("restart mixed accounts")
		}
	}
	if w := accountRequest(g, "GET", "/api/conversations/"+bots[0].DMConversationID+"/messages", "", cookies[0]); w.Code != 200 {
		t.Fatalf("durable session lost %d", w.Code)
	}
}
