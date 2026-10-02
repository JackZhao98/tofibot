package mcprunner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("TOFI_MCP_RUNNER_HELPER") != "1" {
		return
	}
	f, err := os.OpenFile(os.Getenv("TOFI_MCP_RUNNER_STARTS"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, _ = f.WriteString("start\n")
	_ = f.Close()
	s := mcp.NewServer(&mcp.Implementation{Name: "runner-fixture", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{ProtocolVersion}})
	mcp.AddTool(s, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
		Message string `json:"message"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Message}}}, nil, nil
	})
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestIdleReapAndConcurrentColdStart(t *testing.T) {
	startFile := filepath.Join(t.TempDir(), "starts")
	r, err := New([]Spec{{ID: "fixture", Command: os.Args[0], Args: []string{"-test.run=TestRunnerHelperProcess"}, WorkDir: t.TempDir(), Env: map[string]string{"TOFI_MCP_RUNNER_HELPER": "1", "TOFI_MCP_RUNNER_STARTS": startFile}}}, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	tools, err := r.Tools(context.Background(), "fixture")
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for r.Statuses()[0].State != "sleeping" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r.Statuses()[0].State != "sleeping" {
		t.Fatal("plugin did not sleep")
	}
	if _, err := r.Tools(context.Background(), "fixture"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := r.Call(context.Background(), "fixture", "echo", map[string]any{"message": "ok"})
			if err != nil || result == nil || result.IsError {
				t.Errorf("call=%v err=%v", result, err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(startFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte("start\n")); got != 2 {
		t.Fatalf("wanted one restart after sleep, got %d starts", got)
	}
}

func TestPrivateHTTPRequiresTokenAndNeverReplaysUnknownTool(t *testing.T) {
	r, err := New(nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	h := r.Handler("secret")
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/plugins", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal(unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/plugins", nil)
	request.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
}

func TestGogPluginToolRouteDoesNotMatchGogOAuthRoute(t *testing.T) {
	startFile := filepath.Join(t.TempDir(), "starts")
	r, err := New([]Spec{{ID: "gog", Command: os.Args[0], Args: []string{"-test.run=TestRunnerHelperProcess"}, WorkDir: t.TempDir(), Env: map[string]string{"TOFI_MCP_RUNNER_HELPER": "1", "TOFI_MCP_RUNNER_STARTS": startFile}}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	request := httptest.NewRequest(http.MethodGet, "/v1/plugins/gog/tools", nil)
	request.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	r.Handler("secret").ServeHTTP(w, request)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"echo"`)) {
		t.Fatalf("tools route: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRunnerBridgesStdioToPrivateHTTPMCP(t *testing.T) {
	startFile := filepath.Join(t.TempDir(), "starts")
	r, err := New([]Spec{{ID: "fixture", Command: os.Args[0], Args: []string{"-test.run=TestRunnerHelperProcess"}, WorkDir: t.TempDir(), Env: map[string]string{"TOFI_MCP_RUNNER_HELPER": "1", "TOFI_MCP_RUNNER_STARTS": startFile}}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	srv := httptest.NewServer(r.Handler("secret"))
	defer srv.Close()
	ctx := context.Background()
	cli, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp/fixture", HTTPClient: &http.Client{Transport: bearerTransport{token: "secret"}}}, &mcp.ClientSessionOptions{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if cli.InitializeResult().ProtocolVersion != ProtocolVersion {
		t.Fatalf("unexpected protocol: %s", cli.InitializeResult().ProtocolVersion)
	}
	tools, err := cli.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	result, err := cli.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"message": "bridged"}})
	if err != nil || result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("call=%v err=%v", result, err)
	}
	if content, ok := result.Content[0].(*mcp.TextContent); !ok || content.Text != "bridged" {
		t.Fatalf("bridge lost tool content: %v", result.Content)
	}
}

func TestPluginSecretsUsePrivateFiles(t *testing.T) {
	dir := t.TempDir()
	paths, err := installSecretEnv(dir, map[string]string{"API_TOKEN": "private-value"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths["API_TOKEN"])
	if err != nil || string(data) != "private-value" {
		t.Fatalf("secret=%q err=%v", data, err)
	}
	info, err := os.Stat(paths["API_TOKEN"])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("secret permissions=%v", info.Mode())
	}
}

// bearerTransport supplies the existing private control-plane authentication.
type bearerTransport struct{ token string }

func (tr bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+tr.token)
	return http.DefaultTransport.RoundTrip(req)
}

func TestRunnerLegacyHelperProcess(t *testing.T) {
	if os.Getenv("TOFI_MCP_RUNNER_HELPER") != "1" {
		return
	}
	for scanner := bufio.NewScanner(os.Stdin); scanner.Scan(); {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		f, err := os.OpenFile(os.Getenv("TOFI_MCP_RUNNER_STARTS"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
		if err != nil {
			os.Exit(3)
		}
		_, _ = fmt.Fprintln(f, request.Method)
		_ = f.Close()
		if os.Getenv("TOFI_MCP_RUNNER_LEGACY_DISCOVER") == "1" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"supportedVersions": []string{"2025-11-25"}, "capabilities": map[string]any{}}})
		} else {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "legacy server"}})
		}
	}
	os.Exit(0)
}

func TestRunnerRejectsLegacyPluginWithoutSendingInitialize(t *testing.T) {
	for _, discover := range []string{"0", "1"} {
		t.Run("legacy-discover-"+discover, func(t *testing.T) {
			requests := filepath.Join(t.TempDir(), "requests")
			r, err := New([]Spec{{ID: "legacy", Command: os.Args[0], Args: []string{"-test.run=TestRunnerLegacyHelperProcess"}, WorkDir: t.TempDir(), Env: map[string]string{"TOFI_MCP_RUNNER_HELPER": "1", "TOFI_MCP_RUNNER_STARTS": requests, "TOFI_MCP_RUNNER_LEGACY_DISCOVER": discover}}}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := r.Tools(context.Background(), "legacy"); !errors.Is(err, ErrIncompatibleProtocol) {
				t.Fatalf("expected explicit incompatibility: %v", err)
			}
			data, err := os.ReadFile(requests)
			if err != nil || string(data) != "server/discover\n" {
				t.Fatalf("unexpected handshake requests: %q err=%v", data, err)
			}
			request := httptest.NewRequest(http.MethodPost, "/mcp/legacy", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`))
			request.Header.Set("Authorization", "Bearer secret")
			request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
			w := httptest.NewRecorder()
			r.Handler("secret").ServeHTTP(w, request)
			if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "requires protocol 2026-07-28") {
				t.Fatalf("incompatibility lost at HTTP boundary: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRunnerHTTPOnlySupportsLatestStatelessProtocol(t *testing.T) {
	startFile := filepath.Join(t.TempDir(), "starts")
	r, err := New([]Spec{{ID: "fixture", Command: os.Args[0], Args: []string{"-test.run=TestRunnerHelperProcess"}, WorkDir: t.TempDir(), Env: map[string]string{"TOFI_MCP_RUNNER_HELPER": "1", "TOFI_MCP_RUNNER_STARTS": startFile}}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	h := r.Handler("secret")
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/mcp/fixture", nil)
		req.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Mcp-Session-Id") != "" {
			t.Fatalf("stateless %s: status=%d session=%q", method, w.Code, w.Header().Get("Mcp-Session-Id"))
		}
	}
	legacyInitialize := httptest.NewRequest(http.MethodPost, "/mcp/fixture", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"test","version":"1"},"capabilities":{}}}`))
	legacyInitialize.Header.Set("Authorization", "Bearer secret")
	legacyInitialize.Header.Set("Content-Type", "application/json")
	legacyInitialize.Header.Set("Accept", "application/json, text/event-stream")
	legacyResponse := httptest.NewRecorder()
	h.ServeHTTP(legacyResponse, legacyInitialize)
	if legacyResponse.Code != http.StatusBadRequest {
		t.Fatalf("legacy initialize without a version header was accepted: %d %s", legacyResponse.Code, legacyResponse.Body.String())
	}
	legacyInitialize = httptest.NewRequest(http.MethodPost, "/mcp/fixture", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"test","version":"1"},"capabilities":{}}}`))
	legacyInitialize.Header.Set("Authorization", "Bearer secret")
	legacyInitialize.Header.Set("Content-Type", "application/json")
	legacyInitialize.Header.Set("Accept", "application/json, text/event-stream")
	legacyInitialize.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	legacyInitialize.Header.Set("Mcp-Method", "initialize")
	legacyResponse = httptest.NewRecorder()
	h.ServeHTTP(legacyResponse, legacyInitialize)
	if legacyResponse.Code != http.StatusBadRequest {
		t.Fatalf("legacy initialize with a modern HTTP header was accepted: %d %s", legacyResponse.Code, legacyResponse.Body.String())
	}
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", ProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":%q,"io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`, version)
			req := httptest.NewRequest(http.MethodPost, "/mcp/fixture", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("MCP-Protocol-Version", version)
			req.Header.Set("Mcp-Method", "server/discover")
			req.Header.Set("Mcp-Session-Id", "must-be-ignored")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Header().Get("Mcp-Session-Id") != "" {
				t.Fatalf("stateless handler assigned a session: %v", w.Header())
			}
			if version == ProtocolVersion {
				if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"supportedVersions":["2026-07-28"]`)) {
					t.Fatalf("latest discovery: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte("Unsupported protocol version")) {
				t.Fatalf("legacy request was accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
