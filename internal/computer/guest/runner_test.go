package guest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func TestGuestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("TOFI_GUEST_RUNNER_HELPER") != "1" {
		return
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "account-fixture", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{mcprunner.ProtocolVersion}})
	mcp.AddTool(s, &mcp.Tool{Name: "identity"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: os.Getenv("ACCOUNT_FIXTURE")}}}, nil, nil
	})
	if s.Run(context.Background(), &mcp.StdioTransport{}) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func runnerManifest(t *testing.T, root, label string) {
	t.Helper()
	dir := filepath.Join(root, "shared", ".tofi", "runner")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	record := []map[string]any{{"request": map[string]string{"id": "fixture"}, "spec": mcprunner.Spec{ID: "fixture", Command: os.Args[0], Args: []string{"-test.run=^TestGuestRunnerHelperProcess$"}, WorkDir: dir, Env: map[string]string{"TOFI_GUEST_RUNNER_HELPER": "1", "ACCOUNT_FIXTURE": label}}}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGuestRunnerStateIdentityAndRestart(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir()}
	for i, label := range []string{"account-A", "account-B"} {
		runnerManifest(t, roots[i], label)
		for restart := 0; restart < 2; restart++ {
			s, err := New(roots[i], 1)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "/v1/runner/v1/plugins/fixture/call", strings.NewReader(`{"name":"identity","arguments":{}}`))
			request.Header.Set("Authorization", "Bearer other-account-token")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, request)
			if w.Code != 200 || !strings.Contains(w.Body.String(), label) {
				t.Fatalf("call status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "other-account-token") {
				t.Fatal("private token leaked")
			}
			if err := s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			w = httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/runner/v1/plugins", nil))
			if w.Code != 503 {
				t.Fatal("closed Runner revived")
			}
		}
	}
	empty := newTestService(t)
	w := httptest.NewRecorder()
	empty.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/v1/runner/v1/plugins/fixture/call", strings.NewReader(`{"name":"identity"}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("empty account inherited plugin: %d", w.Code)
	}
}
