package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

func TestAccountComputerSizeIsAdminOnly(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "size-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "size-user", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	ac, uc := accountCookie(t, g, admin), accountCookie(t, g, user)
	if _, err := g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id IN (?,?)`, user.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "tofi-size-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "accounts", user.ID), 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	desired := computer.ResourceAllocation{VCPUs: 2, MemoryMiB: 2048, DiskGiB: 16}
	var configured []computer.ResourceAllocation
	var quotas []int
	manager := http.NewServeMux()
	manager.HandleFunc("/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			var in computer.ResourceAllocation
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				w.WriteHeader(400)
				return
			}
			configured = append(configured, in)
			desired = in
		}
		json.NewEncoder(w).Encode(computer.Resources{Scope: "account", State: "ready", Current: computer.ResourceAllocation{VCPUs: 2, MemoryMiB: 2048, DiskGiB: 16}, Desired: desired, Pending: desired.VCPUs != 2, Limits: map[string]int{"max_vcpus": 32, "max_memory_mib": 32768, "max_disk_gib": 1024}})
	})
	provisioner := http.NewServeMux()
	provisioner.HandleFunc("/v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Op    string `json:"op"`
			ID    string `json:"account_id"`
			Quota int    `json:"quota_gib"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Op != "quota" || in.ID != user.ID {
			w.WriteHeader(400) // capacity is not modelled here; the handler then skips the free-space bound
			return
		}
		mu.Lock()
		quotas = append(quotas, in.Quota)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"account_id": in.ID, "quota_bytes": int64(in.Quota) << 30, "applied": true, "provisioned": true})
	})
	serve := func(path string, h http.Handler) {
		ln, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		s := &http.Server{Handler: h}
		go s.Serve(ln)
		t.Cleanup(func() { s.Close() })
	}
	serve(filepath.Join(dir, "broker.sock"), provisioner)
	serve(filepath.Join(dir, "accounts", user.ID, "control.sock"), manager)
	g.config.AccountProvisionerSocket = filepath.Join(dir, "broker.sock")
	g.config.AccountComputerSocketRoot = filepath.Join(dir, "accounts")

	body := `{"vcpus":4,"memory_mib":4096,"disk_gib":32}`
	path := "/api/admin/accounts/" + user.ID + "/resources"
	// A member cannot write the size, through the workspace route or the admin route.
	for _, test := range []struct{ method, path string }{
		{"PUT", "/api/computer/resources"}, {"POST", "/api/computer/resources"}, {"PUT", path}, {"GET", path},
	} {
		if w := accountRequest(g, test.method, test.path, body, uc); w.Code != 403 {
			t.Fatalf("member %s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
	}
	if w := accountRequest(g, "PUT", path, body, nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if len(configured) != 0 || len(quotas) != 0 {
		t.Fatal("refused request reached the computer")
	}
	// The admin reads, rejects a smaller disk, and saves a larger size.
	if w := accountRequest(g, "GET", path, "", ac); w.Code != 200 {
		t.Fatalf("admin read: %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "PUT", path, `{"vcpus":4,"memory_mib":4096,"disk_gib":8}`, ac); w.Code != 400 {
		t.Fatalf("shrink: %d", w.Code)
	}
	if w := accountRequest(g, "PUT", path, `{"vcpus":64,"memory_mib":4096,"disk_gib":32}`, ac); w.Code != 400 {
		t.Fatalf("range: %d", w.Code)
	}
	w := accountRequest(g, "PUT", path, body, ac)
	if w.Code != 200 {
		t.Fatalf("admin save: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(quotas) != 1 || quotas[0] != 32 || len(configured) != 1 || configured[0].VCPUs != 4 || configured[0].DiskGiB != 32 {
		t.Fatalf("quotas %v configured %v", quotas, configured)
	}
}
