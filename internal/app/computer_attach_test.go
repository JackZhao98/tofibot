package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// In an account gateway only an administrator assigns or removes a VM, through
// /api/admin/accounts/{id}/computer/*. A person cannot, and what the
// administrator gets back is the VM's state, never a personal computer.
func TestGatewayAdminAssignsTheVMAndSeesNothingPersonal(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "vm-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "vm-user", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	vm := newFakeComputer("firecracker")
	g.runtimeFactory = func(c Config) (*Server, error) {
		s, err := NewServer(c)
		if err != nil {
			return nil, err
		}
		s.computerFactory = func() (computer.Provider, error) { return vm, nil }
		return s, nil
	}
	ac, uc := accountCookie(t, g, admin), accountCookie(t, g, user)

	// The user's own workspace holds a personal computer the administrator must not learn of.
	workspace, err := g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := workspace.store.createComputerPairing()
	if _, _, err = workspace.store.pairComputer(code, "Maria's private laptop", "linux", []string{"files.list"}); err != nil {
		t.Fatal(err)
	}
	if err = workspace.RegisterComputer(newFakeComputer("laptop-9"), "Maria's private laptop", "eu"); err != nil {
		t.Fatal(err)
	}

	path := "/api/admin/accounts/" + user.ID + "/computer/"
	if w := accountRequest(g, "POST", path+"attach", "", nil); w.Code != 401 {
		t.Fatalf("anonymous attach: %d", w.Code)
	}
	if w := accountRequest(g, "POST", path+"attach", "", uc); w.Code != 403 {
		t.Fatalf("a person attached their own VM: %d %s", w.Code, w.Body.String())
	}
	// A workspace never serves the admin surface, whoever asks.
	if w := accountRequest(g, "POST", "/api/admin/computer/attach", "", uc); w.Code != 403 {
		t.Fatalf("workspace admin route: %d", w.Code)
	}
	if w := accountRequest(g, "POST", "/api/admin/computer/attach", "", ac); w.Code != 403 {
		t.Fatalf("workspace admin route for an admin: %d", w.Code)
	}

	// The user's workspace started with no computer: its factory is set, no VM yet.
	if workspace.computer() != nil {
		t.Fatal("test workspace started with a VM")
	}
	w := accountRequest(g, "POST", path+"attach", "", ac)
	if w.Code != 200 || workspace.computer() == nil {
		t.Fatalf("admin attach: %d %s", w.Code, w.Body.String())
	}
	w = accountRequest(g, "GET", path+"state", "", ac)
	if w.Code != 200 {
		t.Fatalf("state: %d", w.Code)
	}
	for _, leak := range []string{"Maria", "laptop-9", "personal", "eu"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("admin view mentions %q: %s", leak, w.Body.String())
		}
	}
	var view adminVMSummary
	if json.Unmarshal(w.Body.Bytes(), &view) != nil || !view.Attached || view.Kind != "vm" {
		t.Fatalf("state body: %s", w.Body.String())
	}
	w = accountRequest(g, "POST", path+"detach", `{"wait_seconds":1}`, ac)
	if w.Code != 200 || workspace.computer() != nil {
		t.Fatalf("admin detach: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Maria") || strings.Contains(w.Body.String(), "laptop-9") {
		t.Fatalf("detach answer mentions a personal computer: %s", w.Body.String())
	}
	// The person's computer is untouched by the VM's detach.
	if _, err = workspace.computerByID("laptop-9"); err != nil {
		t.Fatalf("personal computer lost with the VM: %v", err)
	}
	if w = accountRequest(g, "POST", path+"detach", `{"wait_seconds":601}`, ac); w.Code != 400 {
		t.Fatalf("out of range wait: %d", w.Code)
	}
}
