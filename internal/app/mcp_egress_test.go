package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type egressAppTransport func(*http.Request) (*http.Response, error)

func (f egressAppTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func egressAppResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestMCPEgressServerModeMatrix(t *testing.T) {
	old := http.DefaultClient
	calls := 0
	http.DefaultClient = &http.Client{Transport: egressAppTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return egressAppResponse(r, `{"authorization_endpoint":"https://10.0.0.1/auth","token_endpoint":"https://10.0.0.1/token"}`), nil
	})}
	defer func() { http.DefaultClient = old }()
	t.Setenv("TOFI_MCP_RUNNER_URL", "")
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", "")
	for _, tc := range []struct {
		name   string
		cfg    Config
		hosted bool
	}{
		{"new-isolated", Config{IsolatedWorkspace: true}, true},
		{"migrated", Config{IsolatedWorkspace: true, AccountRuntime: true}, true},
		{"legacy-runtime", Config{AccountRuntime: true}, true},
		{"control", Config{AccountControlPlane: true}, true},
		{"maintenance", Config{AccountMaintenance: true}, true},
		{"single-owner", Config{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.DataDir = t.TempDir()
			cfg.Provider = "synthetic"
			cfg.Engine = testEngine{}
			cfg.MCPConfigPath = filepath.Join(cfg.DataDir, "mcp.json")
			if hostedMCPEgress(cfg) != tc.hosted {
				t.Fatal("mode predicate mismatch")
			}
			s, err := NewServer(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.extensions.SaveMCP("local_spoof", extensions.MCPServerConfig{URL: "https://10.0.0.1/mcp", Headers: map[string]string{"X-Trusted": "true"}, OAuth: &extensions.OAuthConfig{ClientID: "synthetic-client", AuthServerMetadataURL: "https://10.0.0.1/metadata"}}, false); err != nil {
				t.Fatal(err)
			}
			before := calls
			sid, _, err := s.extensions.OAuthStart(context.Background(), "local_spoof", "http://localhost/callback")
			if tc.hosted {
				if !errors.Is(err, extensions.ErrOutboundDestinationDenied) || calls != before {
					t.Fatalf("hosted mode failed closed: %v calls=%d", err, calls-before)
				}
			} else {
				if err != nil || sid == "" || calls != before+1 {
					t.Fatalf("single-owner compatibility lost: %v", err)
				}
			}
		})
	}
}

func TestMCPEgressAccountCapabilityBeforePrepareAndEachRequest(t *testing.T) {
	ensures, requests, mcpCalls := 0, 0, 0
	privateErr := errors.New("synthetic account failure")
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-account/control.sock", Ensure: func(context.Context) error { ensures++; return nil }, Client: &http.Client{Transport: egressAppTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Path == "/v1/info" {
			return egressAppResponse(r, `{"state":"ready"}`), nil
		}
		if r.URL.Path == "/v1/runner/mcp/gog" {
			mcpCalls++
			if r.Header.Get("Authorization") != "" {
				t.Error("account bearer not stripped")
			}
			return nil, privateErr
		}
		if r.URL.Path == "/v1/runner/v1/plugins" {
			return egressAppResponse(r, `{"plugins":[]}`), nil
		}
		t.Errorf("unexpected private route %s", r.URL.Path)
		return nil, privateErr
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{isolatedWorkspace: true, hostedMCP: true, microVM: client}
	for _, endpoint := range []string{"https://account-computer/v1/runner/mcp/gog", "http://account-computer:80/v1/runner/mcp/gog", "http://ACCOUNT-COMPUTER/v1/runner/mcp/gog", "http://user@account-computer/v1/runner/mcp/gog", "http://account-computer/v1/runner/mcp/gog?", "http://account-computer/v1/runner/mcp/gog?a=b", "http://account-computer/v1/runner/mcp/gog#x", "http://account-computer/v1/runner/mcp/%67og", "http://account-computer/v1/runner/mcp/../gog", "http://account-computer/v1/runner/mcp/gog/other", "http://account-computer/v1/runner/v1/plugins", "http://account-computer/v1/runner", "http://account-computer/v1/runner/mcp/GOG"} {
		tr, err := s.localMCPTransport(endpoint)
		if tr != nil || !errors.Is(err, extensions.ErrOutboundDestinationDenied) {
			t.Errorf("invalid private capability accepted %s", endpoint)
		}
	}
	if ensures != 0 || requests != 0 {
		t.Fatal("invalid selection triggered Prepare/ensure")
	}
	endpoint := computer.RunnerOrigin + "/mcp/gog"
	tr, err := s.localMCPTransport(endpoint)
	if err != nil || tr == nil {
		t.Fatal(err)
	}
	if err := tr.(interface{ Prepare(context.Context) error }).Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ensures != 1 || requests != 1 {
		t.Fatal("Prepare was lost")
	}
	before := requests
	req, _ := http.NewRequest("POST", computer.RunnerOrigin+"/v1/plugins", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, extensions.ErrOutboundDestinationDenied) || requests != before {
		t.Fatal("request escaped exact private endpoint")
	}
	req, _ = http.NewRequest("POST", endpoint, nil)
	req.Header.Set("Authorization", "Bearer synthetic-tenant-token")
	if _, err := tr.RoundTrip(req); !errors.Is(err, privateErr) || mcpCalls != 1 {
		t.Fatal("fixed account route lost")
	}
	// Manager private failure remains terminal; its remote transport is a trap.
	old := http.DefaultTransport
	publicCalls := 0
	http.DefaultTransport = egressAppTransport(func(*http.Request) (*http.Response, error) { publicCalls++; return nil, errors.New("public trap") })
	defer func() { http.DefaultTransport = old }()
	m := extensions.NewManager(extensions.Config{HostedEgress: true, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: s.localMCPTransport})
	defer m.CloseIdleConnections()
	if err := m.SaveMCP("local_gog", extensions.MCPServerConfig{URL: endpoint}, false); err != nil {
		t.Fatal(err)
	}
	inspection := m.InspectMCP(context.Background(), "local_gog")
	if len(inspection.Diagnostics) == 0 || publicCalls != 0 {
		t.Fatal("private failure fell through")
	}
	data, status, err := s.runnerRequest(httptest.NewRequest("GET", "http://synthetic/", nil), "GET", "/v1/plugins", nil)
	if err != nil || status != 200 || !strings.Contains(string(data), "plugins") {
		t.Fatalf("account administration lost: %v", err)
	}
}

type readinessFixture struct{ prepares, checks, calls int }

func (f *readinessFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls++
	return egressAppResponse(r, "ok"), nil
}
func (f *readinessFixture) Prepare(context.Context) error { f.prepares++; return nil }
func (f *readinessFixture) Readiness(context.Context) (string, error) {
	f.checks++
	return "auth_required", nil
}
func TestMCPEgressPrivateWrapperPreservesReadiness(t *testing.T) {
	f := &readinessFixture{}
	tr := scopedMCPTransport{endpoint: computer.RunnerOrigin + "/mcp/gog", base: f}
	if err := tr.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := tr.Readiness(context.Background())
	if err != nil || state != "auth_required" || f.prepares != 1 || f.checks != 1 {
		t.Fatal("private readiness interfaces lost")
	}
}

func TestMCPEgressOperatorSnapshotAndManagementCompatibility(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "synthetic-token")
	if err := os.WriteFile(tokenPath, []byte("synthetic-runner-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{localRunnerURL: "http://runner.fixture.test:8080/base", localRunnerTokenFile: tokenPath, isolatedWorkspace: true, hostedMCP: true}
	s.snapshotLocalRunnerMCP()
	if s.localRunnerMCPBase == nil {
		t.Fatal("server snapshot absent")
	}
	defer s.localRunnerMCPHTTP.CloseIdleConnections()
	for _, suffix := range []string{"/mcp/gog?", "/mcp/gog?a=b", "/mcp/%67og", "/mcp/../gog", "/v1/plugins", "/mcp/gog/extra"} {
		tr, err := s.localMCPTransport(s.localRunnerURL + suffix)
		if tr != nil || !errors.Is(err, extensions.ErrOutboundDestinationDenied) {
			t.Fatal("operator capability widened")
		}
	}
	for _, raw := range []string{"https://runner.fixture.test:8080/base/mcp/gog", "http://runner.fixture.test:8081/base/mcp/gog", "http://user@runner.fixture.test:8080/base/mcp/gog"} {
		tr, err := s.localMCPTransport(raw)
		if tr != nil || !errors.Is(err, extensions.ErrOutboundDestinationDenied) {
			t.Fatal("operator authority mismatch accepted")
		}
	}
	if tr, err := s.localMCPTransport("http://other.fixture.test:8080/base/mcp/gog"); tr != nil || err != nil {
		t.Fatal("unrelated remote acquired private capability")
	}
	// Missing token is not read during arbitrary endpoint classification.
	s.localRunnerTokenFile = filepath.Join(root, "absent")
	tr, err := s.localMCPTransport(s.localRunnerURL + "/mcp/gog")
	if err != nil || tr == nil {
		t.Fatal("classification read credential")
	}
	s.localRunnerTokenFile = tokenPath
	calls := 0
	s.localRunnerMCPHTTP.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		if a != "runner.fixture.test:8080" {
			t.Error("operator dial escaped snapshot")
		}
		left, right := net.Pipe()
		go func() {
			defer right.Close()
			_ = right.SetDeadline(time.Now().Add(time.Second))
			r, err := http.ReadRequest(bufio.NewReader(right))
			if err != nil {
				return
			}
			calls++
			if r.URL.Path != "/base/mcp/gog" || r.Header.Get("Authorization") != "Bearer synthetic-runner-token" {
				t.Error("server credential/capability lost")
			}
			_ = (&http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Connection": []string{"close"}}, Body: io.NopCloser(strings.NewReader("ok")), ContentLength: 2, Close: true}).Write(right)
		}()
		return left, nil
	}
	req, _ := http.NewRequest("GET", s.localRunnerURL+"/mcp/gog", nil)
	req.Header.Set("Authorization", "Bearer synthetic-forged-token")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if calls != 1 {
		t.Fatal("configured Runner MCP lost")
	}
	old := http.DefaultTransport
	http.DefaultTransport = egressAppTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != s.localRunnerURL+"/v1/plugins" || r.Header.Get("Authorization") != "Bearer synthetic-runner-token" {
			t.Error("administration origin/auth changed")
		}
		return egressAppResponse(r, `{"plugins":[]}`), nil
	})
	defer func() { http.DefaultTransport = old }()
	_, status, err := s.runnerRequest(httptest.NewRequest("GET", "http://synthetic/", nil), "GET", "/v1/plugins", nil)
	if err != nil || status != 200 {
		t.Fatal("operator administration lost")
	}
	// Changing process configuration cannot rebind an already selected capability.
	s.localRunnerURL = "http://other.fixture.test:8080/base"
	req, _ = http.NewRequest("GET", "http://runner.fixture.test:8080/base/mcp/gog", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, extensions.ErrOutboundDestinationDenied) {
		t.Fatal("snapshot rebound")
	}
	for _, raw := range []string{"http://runner.fixture.test/base/../admin", "http://runner.fixture.test/base?", "http://runner.fixture.test/%62ase", "http://user@runner.fixture.test/base"} {
		u, _ := url.Parse(raw)
		if validRunnerBase(u) {
			t.Fatal("invalid server base accepted")
		}
	}
}

func TestMCPEgressSingleOwnerRemotePrivateCompatibility(t *testing.T) {
	backend := mcp.NewServer(&mcp.Implementation{Name: "synthetic", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{"2026-07-28"}, Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	backend.AddTool(&mcp.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "synthetic"}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return backend }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	old := http.DefaultTransport
	calls := 0
	http.DefaultTransport = egressAppTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		resp := w.Result()
		resp.Request = r
		return resp, nil
	})
	defer func() { http.DefaultTransport = old }()
	for _, hosted := range []bool{false, true} {
		s := &Server{hostedMCP: hosted, localRunnerURL: "http://127.0.0.1:8765", localRunnerTokenFile: "/tmp/synthetic-absent-token"}
		s.snapshotLocalRunnerMCP()
		defer s.localRunnerMCPHTTP.CloseIdleConnections()
		manager := extensions.NewManager(extensions.Config{HostedEgress: hosted, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: s.localMCPTransport})
		defer manager.CloseIdleConnections()
		if err := manager.SaveMCP("remote", extensions.MCPServerConfig{URL: "http://127.0.0.1:9090/mcp"}, false); err != nil {
			t.Fatal(err)
		}
		before := calls
		result := manager.InspectMCP(context.Background(), "remote")
		if hosted {
			if len(result.Diagnostics) == 0 || calls != before {
				t.Fatal("hosted private remote bypass")
			}
		} else {
			if len(result.Diagnostics) != 0 || result.ToolCount != 1 || calls <= before {
				t.Fatalf("single-owner private remote compatibility lost: %+v", result)
			}
		}
	}
}
