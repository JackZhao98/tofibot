package extensions

import (
	"errors"
	"strings"
	"testing"
)

func TestMCPInspectionDiagnosticGoogleReasonsAreBounded(t *testing.T) {
	for _, tc := range []struct{ detail, code string }{
		{`403 {"reason":"SERVICE_DISABLED","activationUrl":"https://evil.invalid/?client_secret=PRIVATE"}`, "api_disabled"},
		{`accessNotConfigured access_token=PRIVATE`, "api_disabled"},
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
