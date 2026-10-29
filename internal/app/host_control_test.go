package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

const hostToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

func hostCall(g *AccountGateway, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, req)
	return w
}

func bearer() map[string]string { return map[string]string{"Authorization": "Bearer " + hostToken} }

// The host's attach and detach reach every account without a restart, only with
// the host token, and only on a server whose host made the token file.
func TestHostControlAttachesAndDetachesEveryAccount(t *testing.T) {
	g := accountFixture(t)
	ctx := context.Background()
	admin, err := g.create(ctx, "host-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(ctx, "host-user", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	third, err := g.create(ctx, "host-third", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	var configs []Config
	g.runtimeFactory = func(c Config) (*Server, error) {
		configs = append(configs, c)
		c.ComputerSocket = "" // the fake computer stands in for the Worker's socket
		s, err := NewServer(c)
		if err != nil {
			return nil, err
		}
		vm := newFakeComputer("firecracker-" + c.AccountID)
		s.computerFactory = func() (computer.Provider, error) { return vm, nil }
		return s, nil
	}
	tokenFile := filepath.Join(t.TempDir(), "host-control.token")
	g.config.HostControlTokenFile = tokenFile

	// No token file: the route does not exist.
	if w := hostCall(g, "GET", "/api/host/computer/state", "", bearer()); w.Code != 404 && w.Code != 401 {
		t.Fatalf("no token file: %d", w.Code)
	}
	if err := os.WriteFile(tokenFile, []byte(hostToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Wrong, missing, short and proxied credentials are all refused.
	for name, h := range map[string]map[string]string{
		"none":    nil,
		"wrong":   {"Authorization": "Bearer " + strings.Repeat("a", 48)},
		"basic":   {"Authorization": "Basic " + hostToken},
		"proxied": {"Authorization": "Bearer " + hostToken, "X-Forwarded-For": "203.0.113.9"},
		"via":     {"Authorization": "Bearer " + hostToken, "Forwarded": "for=203.0.113.9"},
	} {
		if w := hostCall(g, "GET", "/api/host/computer/state", "", h); w.Code != 401 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if err := os.WriteFile(tokenFile, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := hostCall(g, "GET", "/api/host/computer/state", "", map[string]string{"Authorization": "Bearer short"}); w.Code != 401 {
		t.Fatalf("a short token authorized: %d", w.Code)
	}
	if err := os.WriteFile(tokenFile, []byte(hostToken), 0o600); err != nil {
		t.Fatal(err)
	}
	// No session and no admin cookie is needed, and a POST needs no Origin.
	if w := hostCall(g, "GET", "/api/host/computer/state", "", bearer()); w.Code != 200 || !strings.Contains(w.Body.String(), `"backend_available":false`) {
		t.Fatalf("state: %d %s", w.Code, w.Body.String())
	}
	// A server that started without the computer backend cannot hot attach.
	if w := hostCall(g, "POST", "/api/host/computer/attach", "", bearer()); w.Code != 409 || !strings.Contains(w.Body.String(), "computer_backend_unavailable") {
		t.Fatalf("attach without a backend: %d %s", w.Code, w.Body.String())
	}

	g.config.AccountProvisionerSocket = filepath.Join(t.TempDir(), "broker.sock")
	g.config.AccountComputerSocketRoot = t.TempDir()
	for _, a := range []Account{admin, user} {
		if _, err := g.workspace(a); err != nil {
			t.Fatal(err)
		}
	}
	w := hostCall(g, "POST", "/api/host/computer/attach", "", bearer())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"attached":3`) {
		t.Fatalf("attach: %d %s", w.Code, w.Body.String())
	}
	for _, a := range []Account{admin, user, third} {
		ws, err := g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		if ws.computer() == nil {
			t.Fatalf("account %s has no computer after the host attached them", a.Username)
		}
	}
	// A second attach changes nothing.
	if w := hostCall(g, "POST", "/api/host/computer/attach", "", bearer()); w.Code != 200 || !strings.Contains(w.Body.String(), `"already_attached":3`) {
		t.Fatalf("second attach: %d %s", w.Code, w.Body.String())
	}

	if w := hostCall(g, "POST", "/api/host/computer/detach", `{"wait_seconds":601}`, bearer()); w.Code != 400 {
		t.Fatalf("out of range wait: %d", w.Code)
	}
	w = hostCall(g, "POST", "/api/host/computer/detach", `{"wait_seconds":1}`, bearer())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"detached":3`) {
		t.Fatalf("detach: %d %s", w.Code, w.Body.String())
	}
	for _, a := range []Account{admin, user, third} {
		ws, _ := g.workspace(a)
		if ws.computer() != nil {
			t.Fatalf("account %s still has a computer", a.Username)
		}
	}
	// With the computers off nothing talks to the Worker, and a workspace opened now starts detached.
	if g.provisioner() != "" {
		t.Fatal("the gateway still addresses the Worker after the host detached the computers")
	}
	fourth, err := g.create(ctx, "host-fourth", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatalf("creating an account with the computers off must not reserve a computer: %v", err)
	}
	if _, err := g.workspace(fourth); err != nil {
		t.Fatal(err)
	}
	if last := configs[len(configs)-1]; !last.ComputerStartDetached || last.AccountID != fourth.ID {
		t.Fatalf("new workspace config: detached=%v account=%s", last.ComputerStartDetached, last.AccountID)
	}
	if w := hostCall(g, "GET", "/api/host/computer/state", "", bearer()); !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("state after detach: %s", w.Body.String())
	}
	// And the host can attach them all again.
	if w := hostCall(g, "POST", "/api/host/computer/attach", "", bearer()); w.Code != 200 || !strings.Contains(w.Body.String(), `"attached":4`) {
		t.Fatalf("attach again: %d %s", w.Code, w.Body.String())
	}
}

func TestHostControlIsNotServedThroughTheCookieSurface(t *testing.T) {
	g := accountFixture(t)
	g.config.HostControlTokenFile = filepath.Join(t.TempDir(), "absent")
	for _, method := range []string{"GET", "POST"} {
		if w := hostCall(g, method, "/api/host/computer/attach", "", bearer()); w.Code == 200 || w.Code == 409 {
			t.Fatalf("%s without a token file: %d", method, w.Code)
		}
	}
	_ = http.StatusOK
}
