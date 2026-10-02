package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func TestAccountGuestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("ACCOUNT_RUNNER_FIXTURE") == "" {
		return
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "tenant-fixture", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{mcprunner.ProtocolVersion}})
	mcp.AddTool(s, &mcp.Tool{Name: "identity"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: os.Getenv("ACCOUNT_RUNNER_FIXTURE")}}}, nil, nil
	})
	if s.Run(context.Background(), &mcp.StdioTransport{}) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestAccountGuestRunnerUserFlowAndRestart(t *testing.T) {
	ctx := context.Background()
	root, err := os.MkdirTemp("/tmp", "tofi-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	var mu sync.Mutex
	guests := map[string]*guest.Service{}
	servers := []*http.Server{}
	startGuest := func(id string) {
		dir := filepath.Join(root, "guests", id)
		state := filepath.Join(dir, "shared", ".tofi", "runner")
		if err := os.MkdirAll(state, 0700); err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(state, "manifest.json")
		if _, err := os.Stat(manifest); os.IsNotExist(err) {
			records := []map[string]any{{"request": map[string]string{"id": "fixture"}, "spec": mcprunner.Spec{ID: "fixture", Command: os.Args[0], Args: []string{"-test.run=^TestAccountGuestRunnerHelperProcess$"}, WorkDir: state, Env: map[string]string{"ACCOUNT_RUNNER_FIXTURE": id}}}}
			data, _ := json.Marshal(records)
			if os.WriteFile(manifest, data, 0600) != nil {
				t.Fatal("manifest write")
			}
		}
		s, err := guest.New(dir, 1)
		if err != nil {
			t.Fatal(err)
		}
		guests[id] = s
	}
	brokerPath := filepath.Join(root, "broker.sock")
	listener, err := net.Listen("unix", brokerPath)
	if err != nil {
		t.Fatal(err)
	}
	broker := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Op, AccountID string
			QuotaGiB      int
		}
		var input map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&input)
		json.Unmarshal(input["op"], &body.Op)
		json.Unmarshal(input["account_id"], &body.AccountID)
		mu.Lock()
		defer mu.Unlock()
		if body.Op == "reserve" {
			id := body.AccountID
			startGuest(id)
			parent := filepath.Join(root, "sockets", id)
			os.MkdirAll(parent, 0700)
			unix, err := net.Listen("unix", filepath.Join(parent, "control.sock"))
			if err != nil {
				t.Error(err)
				http.Error(w, "fixture", 500)
				return
			}
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/info" {
					w.Write([]byte(`{"state":"ready"}`))
					return
				}
				mu.Lock()
				s := guests[id]
				mu.Unlock()
				s.Handler().ServeHTTP(w, r)
			})}
			servers = append(servers, srv)
			go srv.Serve(unix)
		}
		json.NewEncoder(w).Encode(map[string]string{"account_id": body.AccountID, "socket": filepath.Join(root, "sockets", body.AccountID, "control.sock")})
	})}
	go broker.Serve(listener)
	t.Cleanup(func() {
		broker.Close()
		for _, s := range servers {
			s.Close()
		}
		for _, s := range guests {
			s.Close(ctx)
		}
	})
	g, err := NewAccountGateway(Config{DataDir: filepath.Join(root, "app"), OwnerAuth: true, Environment: "acceptance", AccountProvisionerSocket: brokerPath, AccountComputerSocketRoot: filepath.Join(root, "sockets"), AccountComputerDiskGiB: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	setup := accountRequest(g, "POST", "/api/auth/setup", `{"username":"admin","password":"SyntheticPassword123!"}`, nil)
	if setup.Code != 200 {
		t.Fatal(setup.Code, setup.Body.String())
	}
	adminCookie := setup.Result().Cookies()[0]
	users := []Account{}
	cookies := []*http.Cookie{}
	for _, name := range []string{"alpha", "bravo"} {
		created := accountRequest(g, "POST", "/api/admin/accounts", `{"username":"`+name+`","password":"SyntheticPassword123!"}`, adminCookie)
		if created.Code != 201 {
			t.Fatal(created.Code, created.Body.String())
		}
		var a Account
		json.Unmarshal(created.Body.Bytes(), &a)
		uc := accountCookie(t, g, a)
		changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, uc)
		if changed.Code != 200 {
			t.Fatal(changed.Code, changed.Body.String())
		}
		uc = changed.Result().Cookies()[0]
		users = append(users, a)
		cookies = append(cookies, uc)
	}
	call := func(index int) {
		w := accountRequest(g, "GET", "/api/extensions/local-mcp", "", cookies[index])
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"fixture"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		w = accountRequest(g, "POST", "/api/extensions/local-mcp/fixture/attach", `{}`, cookies[index])
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		s, err := g.workspace(users[index])
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := s.extensions.Prepare(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		found := false
		for _, tool := range prepared.Tools {
			if strings.HasSuffix(tool.Name, "__identity") {
				found = true
				value, err := tool.Execute(ctx, json.RawMessage(`{}`))
				if err != nil || !strings.Contains(value, users[index].ID) || strings.Contains(value, users[1-index].ID) {
					t.Fatalf("tenant result=%s err=%v", value, err)
				}
			}
		}
		if !found {
			t.Fatalf("missing tool: diagnostics=%v", prepared.Diagnostics)
		}
	}
	call(0)
	call(1)
	mu.Lock()
	old := guests[users[0].ID]
	mu.Unlock()
	old.Close(ctx)
	mu.Lock()
	startGuest(users[0].ID)
	mu.Unlock()
	call(0)
	w := accountRequest(g, "DELETE", "/api/extensions/local-mcp/fixture", "", cookies[0])
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = accountRequest(g, "GET", "/api/extensions/local-mcp", "", cookies[0])
	if strings.Contains(w.Body.String(), `"fixture"`) {
		t.Fatal("deleted plugin survived")
	}
	call(1)
	g.Close()
	g, err = NewAccountGateway(Config{DataDir: filepath.Join(root, "app"), OwnerAuth: true, Environment: "acceptance", AccountProvisionerSocket: brokerPath, AccountComputerSocketRoot: filepath.Join(root, "sockets"), AccountComputerDiskGiB: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	call(1)
}
