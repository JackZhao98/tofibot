package extensions

import (
	"bytes"
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPIDisabledDiagnosticCarriesOnlyTOFIBuiltLinks(t *testing.T) {
	body := `status 403: {"error":{"code":403,"status":"PERMISSION_DENIED","message":"PRIVATE","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","metadata":{"service":"gmail.googleapis.com","consumer":"projects/622599621014","activationUrl":"https://evil.invalid/x"}}]}}`
	got := mcpInspectionDiagnostic("gmail", "https://gmailmcp.googleapis.com/mcp/v1", errors.New(body))
	if got.Code != "api_disabled" || got.API != "gmail.googleapis.com" || got.EnableURL != "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=622599621014" {
		t.Fatalf("diagnostic: %+v", got)
	}
	// An error that names no known API falls back to the endpoint's own product API.
	body = `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","metadata":{"service":"evil.example.com"}}]}}`
	got = mcpInspectionDiagnostic("calendar", "https://calendarmcp.googleapis.com/mcp/v1", errors.New(body))
	if got.API != "calendar-json.googleapis.com" || strings.Contains(got.EnableURL, "evil") || !strings.HasPrefix(got.EnableURL, "https://console.cloud.google.com/apis/") {
		t.Fatalf("fallback: %+v", got)
	}
}

func TestMCPInspectionDiagnosticGoogleReasonsAreBounded(t *testing.T) {
	for _, tc := range []struct{ detail, code string }{
		{`status 403: {"error":{"code":403,"status":"PERMISSION_DENIED","message":"PRIVATE","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","metadata":{"service":"gmail.googleapis.com","consumer":"projects/622599621014","activationUrl":"https://evil.invalid/?client_secret=PRIVATE"}}]}}`, "api_disabled"},
		// The same words with no structured error are not "API not enabled" (wording is never evidence).
		{`403 {"reason":"SERVICE_DISABLED","activationUrl":"https://evil.invalid/?client_secret=PRIVATE"}`, ""},
		{`accessNotConfigured access_token=PRIVATE`, ""},
		{`access_denied refresh_token=PRIVATE`, "access_denied"},
		{`request failed status 403 PRIVATE`, "permission_denied"},
		{`PERMISSION_DENIED PRIVATE`, "permission_denied"},
		{`status 500 PRIVATE`, ""},
	} {
		got := mcpInspectionDiagnostic("gmail", "https://gmailmcp.googleapis.com/mcp/v1", errors.New(tc.detail))
		if got.Code != tc.code || strings.Contains(got.Message, "PRIVATE") || strings.Contains(got.Message, "evil.invalid") {
			t.Fatalf("diagnostic: %+v", got)
		}
	}
	for _, endpoint := range []string{"https://gmailmcp.googleapis.com.evil.invalid/mcp", "https://example.com/mcp", "http://gmailmcp.googleapis.com/mcp", "https://gmailmcp.googleapis.com:8443/mcp"} {
		if got := mcpInspectionDiagnostic("google-gmail", endpoint, errors.New("SERVICE_DISABLED")); got.Code != "" {
			t.Fatalf("untrusted provider classified: %+v", got)
		}
	}
}

func TestOpenMCPClientRecordsRejectingHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
		_, err := m.openMCPClient(ctx, ctx, "g", MCPServerConfig{URL: srv.URL})
		cancel()
		srv.Close()
		var httpErr *mcpHTTPStatusError
		if err == nil || !errors.As(err, &httpErr) || httpErr.Status != status {
			t.Fatalf("status %d not recorded: %v", status, err)
		}
	}
}

func TestToolCallRejectedWithAuthStatusNamesPreviewThenAgentic(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "g", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{requiredMCPProtocolVersion}})
	server.AddTool(fixtureTool("list"), fixtureNoop)
	inner := fixtureHTTPHandler(server)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"tools/call"`)) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`PRIVATE provider text`))
			return
		}
		inner.ServeHTTP(w, r)
	}))
	defer remote.Close()
	for _, readOnly := range []bool{true, false} {
		for _, google := range []bool{true, false} {
			m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), ToolTimeout: 5 * time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cli, err := m.openMCPClient(ctx, ctx, "g", MCPServerConfig{URL: remote.URL})
			if err != nil {
				t.Fatal(err)
			}
			if google {
				v, _ := m.mcpStatus.Load(cli)
				v.(*mcpSessionHTTP).endpoint = "https://gmailmcp.googleapis.com/mcp/v1"
			}
			_, err = m.callMCPTool(ctx, cli, "list", nil, readOnly)
			cancel()
			cli.Close()
			if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("err=%v", err)
			}
			msg := err.Error()
			named := strings.Contains(msg, "Developer Preview Program") && strings.Contains(msg, "AI-powered agent")
			if named != google || (google && strings.Index(msg, "Developer Preview Program") > strings.Index(msg, "AI-powered agent")) {
				t.Fatalf("readOnly=%v google=%v: %v", readOnly, google, err)
			}
		}
	}
}

func TestMCPInspectionDiagnosticNamesAgenticClientForGoogleHosts(t *testing.T) {
	for _, status := range []int{401, 403} {
		// The wrapped error carries no status text: classification is by host
		// plus the structured HTTP status only.
		err := &mcpHTTPStatusError{Status: status, Err: errors.New("connect MCP: rejected PRIVATE")}
		for _, host := range []string{"gmailmcp", "drivemcp", "docsmcp", "sheetsmcp", "slidesmcp", "calendarmcp"} {
			got := mcpInspectionDiagnostic("google", "https://"+host+".googleapis.com/mcp/v1", err)
			if got.Code != "permission_denied" || !strings.Contains(got.Message, "AI-powered agent") || !strings.Contains(got.Message, "Developer Preview Program") || strings.Contains(got.Message, "PRIVATE") {
				t.Fatalf("%s %d: %+v", host, status, got)
			}
		}
	}
	// Other statuses do not point at the agentic setting.
	got := mcpInspectionDiagnostic("google", "https://gmailmcp.googleapis.com/mcp/v1", &mcpHTTPStatusError{Status: 500, Err: errors.New("x")})
	if strings.Contains(got.Message, "AI-powered agent") {
		t.Fatalf("500 named agentic: %+v", got)
	}
}

func TestMCPInspectionDiagnosticNonGoogleHostNeverNamesAgentic(t *testing.T) {
	for _, endpoint := range []string{"https://mcp.notion.com/mcp", "https://example.com/mcp/v1", "https://gmailmcp.googleapis.com.evil.invalid/mcp/v1"} {
		got := mcpInspectionDiagnostic("x", endpoint, &mcpHTTPStatusError{Status: 403, Err: errors.New("x")})
		if got.Code != "" || strings.Contains(got.Message, "agent") {
			t.Fatalf("%s: %+v", endpoint, got)
		}
	}
}
