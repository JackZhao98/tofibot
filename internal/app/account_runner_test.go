package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestAccountRunnerNeverInheritsLegacyCredentials(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy-token")
	own := filepath.Join(root, "own-token")
	if err := os.WriteFile(legacy, []byte("synthetic-legacy-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("synthetic-own-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOFI_MCP_RUNNER_URL", "http://legacy-runner:8090")
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", legacy)
	tenant := &Server{isolatedWorkspace: true}
	if _, _, err := tenant.localRunnerConfig(); err == nil {
		t.Fatal("tenant inherited legacy Runner")
	}
	tenant.localRunnerURL = "http://own-runner:8090"
	tenant.localRunnerTokenFile = own
	base, token, err := tenant.localRunnerConfig()
	if err != nil || base != "http://own-runner:8090" || token != "synthetic-own-token" {
		t.Fatalf("tenant configuration failed: %v", err)
	}
	legacyServer := &Server{}
	base, token, err = legacyServer.localRunnerConfig()
	if err != nil || base != "http://legacy-runner:8090" || token != "synthetic-legacy-token" {
		t.Fatal("legacy Runner changed")
	}
}

func TestAccountRunnerDoesNotRedirectRequestsOrCredentials(t *testing.T) {
	var siblingCalls atomic.Int32
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siblingCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sibling.Close()
	own := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-own-token" {
			t.Error("own Runner missing own token")
		}
		http.Redirect(w, r, sibling.URL+"/v1/plugins", http.StatusTemporaryRedirect)
	}))
	defer own.Close()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("synthetic-own-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{isolatedWorkspace: true, localRunnerURL: own.URL, localRunnerTokenFile: path}
	_, status, err := s.runnerRequest(httptest.NewRequest("POST", "/", nil), "POST", "/v1/plugins", map[string]string{"id": "synthetic-plugin"})
	if err != nil || status != http.StatusTemporaryRedirect {
		t.Fatalf("redirect result status=%d err=%v", status, err)
	}
	if siblingCalls.Load() != 0 {
		t.Fatal("Runner redirected an authenticated operation into sibling origin")
	}
}
