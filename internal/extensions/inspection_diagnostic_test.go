package extensions

import (
	"errors"
	"strings"
	"testing"
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
