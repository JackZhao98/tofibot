package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type conversationRouteEngine struct{ calls *atomic.Int64 }

func (e conversationRouteEngine) Run(context.Context, Request) (Result, error) {
	e.calls.Add(1)
	return Result{}, errors.New("synthetic navigation fixture cannot execute work")
}

// Opt-in real HTTP/browser acceptance: two disposable accounts, no credentials,
// provider, Worker or external calls. Only test builds expose fixture controls.
func TestConversationRouteBrowserFixture(t *testing.T) {
	manifest := os.Getenv("TOFI_ROUTE_BROWSER_MANIFEST")
	if manifest == "" {
		t.Skip("opt-in rendered conversation route acceptance")
	}
	dir, err := os.MkdirTemp("/tmp", "tofi-route-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var calls atomic.Int64
	g, err := NewAccountGateway(Config{DataDir: dir, Listen: "127.0.0.1:0", UIDir: "../../ui/dist", Environment: "acceptance", OwnerAuth: true, OwnerAllowLoopbackHTTP: true, Engine: conversationRouteEngine{&calls}, Provider: "synthetic", ComputerSocket: filepath.Join(dir, "no-worker.sock")})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.runtimeFactory = func(c Config) (*Server, error) {
		c.Provider, c.Engine, c.AccountControlPlane = "synthetic", conversationRouteEngine{&calls}, true
		return NewServer(c)
	}
	accounts := make([]Account, 0, 2)
	workspaces := make([]*Server, 0, 2)
	fixtures := make([]map[string]any, 0, 2)
	for i, username := range []string{"route-alpha", "route-bravo"} {
		a, err := g.create(context.Background(), username, "", "SyntheticRoutePassword123!", i == 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID); err != nil {
			t.Fatal(err)
		}
		a.MustChangePassword = false
		s, err := g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		bots := make([]Bot, 0, 4)
		for _, suffix := range []string{"One", "Two", "Archived", "Deleted"} {
			bot, err := s.store.CreateBot(username+" "+suffix, "Use synthetic data only.", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.store.AddMessage(bot.DMConversationID, "assistant", bot.ID, "", username+" HISTORY "+suffix, ""); err != nil {
				t.Fatal(err)
			}
			bots = append(bots, bot)
		}
		group, err := s.store.CreateGroup(username+" Group", []string{bots[0].ID, bots[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.store.AddMessage(group.ID, "assistant", bots[0].ID, "", username+" GROUP HISTORY", ""); err != nil {
			t.Fatal(err)
		}
		internal, err := s.store.CreateGroup(username+" INTERNAL MUST NOT RENDER", []string{bots[0].ID, bots[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.db.Exec(`UPDATE conversations SET user_visible=0 WHERE id=?`, internal.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.SetBotArchived(bots[2].ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.DeleteBot(bots[3].ID); err != nil {
			t.Fatal(err)
		}
		accounts, workspaces = append(accounts, a), append(workspaces, s)
		fixtures = append(fixtures, map[string]any{"username": username, "bots": bots, "group": group, "internal": internal.ID})
	}
	stop := make(chan struct{})
	var once sync.Once
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acceptance/state":
			counts := make([]map[string]int, len(workspaces))
			for i, s := range workspaces {
				counts[i] = map[string]int{}
				for _, table := range []string{"runs", "schedules", "work_items", "messages", "bots", "conversations"} {
					var n int
					if err := s.store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					counts[i][table] = n
				}
			}
			writeJSON(w, 200, map[string]any{"counts": counts, "engine_calls": calls.Load()})
		case "/acceptance/notification":
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			bot := fixtures[0]["bots"].([]Bot)[1]
			message, _, err := workspaces[0].store.AddMessage(bot.DMConversationID, "assistant", bot.ID, "", "SYNTHETIC NOTIFICATION", "")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, 200, message)
		case "/acceptance/stop":
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			writeJSON(w, 200, map[string]bool{"stopped": true})
			once.Do(func() { close(stop) })
		default:
			g.Handler().ServeHTTP(w, r)
		}
	}))
	defer httpServer.Close()
	data, err := json.Marshal(map[string]any{"origin": httpServer.URL, "accounts": fixtures, "password": "SyntheticRoutePassword123!"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic conversation route browser fixture: %s (accounts %d)", httpServer.URL, len(accounts))
	select {
	case <-stop:
	case <-time.After(15 * time.Minute):
		t.Fatal("browser fixture timeout")
	}
}
