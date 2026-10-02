package app

import (
	"context"
	"net/http"
	"testing"
)

func TestAccountMaintenanceFencesWritesAndBackgroundRuntimeStartup(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "owner", "owner@example.test", "SyntheticPassword123!", true)
	if err != nil {
		t.Fatal(err)
	}
	c := g.config
	g.Close()
	c.AccountMaintenance = true
	g, err = NewAccountGateway(c)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if len(g.workspaces) != 0 {
		t.Fatal("maintenance eagerly opened background runtimes")
	}
	cookie := accountCookie(t, g, admin)
	for _, route := range []string{"/api/auth/setup", "/api/auth/password", "/api/admin/accounts", "/api/conversations/fixture/messages", "/api/computers/firecracker/actions"} {
		if got := accountRequest(g, http.MethodPost, route, `{}`, cookie); got.Code != 503 {
			t.Fatalf("maintenance allowed %s: %d", route, got.Code)
		}
	}
	if len(g.workspaces) != 0 {
		t.Fatal("rejected writes created a runtime")
	}
	created := false
	g.runtimeFactory = func(cfg Config) (*Server, error) {
		created = true
		if !cfg.AccountControlPlane {
			t.Error("metadata reader can start background jobs")
		}
		return NewServer(cfg)
	}
	if got := accountRequest(g, http.MethodGet, "/api/bots", "", cookie); got.Code != 200 {
		t.Fatalf("metadata read: %d %s", got.Code, got.Body.String())
	}
	if !created {
		t.Fatal("metadata read did not exercise the fenced runtime")
	}
}
