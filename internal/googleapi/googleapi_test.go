package googleapi

import "testing"

const clientJSON = `{"web":{"client_id":"622599621014-abcdef0123456789.apps.googleusercontent.com","project_id":"tofi-sample-123456","client_secret":"GOCSPX-sample","redirect_uris":["https://tofi.example.test/api/extensions/local-mcp/gog/oauth/callback"]}}`

func TestParseClientReadsTheProject(t *testing.T) {
	c, err := ParseClient([]byte(clientJSON))
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectNumber != "622599621014" || c.ProjectID != "tofi-sample-123456" || c.Project() != "tofi-sample-123456" {
		t.Fatalf("project not read: %+v", c)
	}
	// Without project_id the number from the client id is used; a malformed project_id is ignored.
	for _, raw := range []string{
		`{"web":{"client_id":"622599621014-x.apps.googleusercontent.com","client_secret":"s"}}`,
		`{"web":{"client_id":"622599621014-x.apps.googleusercontent.com","client_secret":"s","project_id":"Bad Project!"}}`,
	} {
		c, err := ParseClient([]byte(raw))
		if err != nil || c.Project() != "622599621014" {
			t.Fatalf("number fallback failed: %+v %v", c, err)
		}
	}
	for _, bad := range []string{``, `{}`, `{"installed":{"client_id":"1-x.apps.googleusercontent.com","client_secret":"s"}}`, `{"web":{"client_id":"622599621014-x.apps.googleusercontent.com"}}`, `not json`} {
		if _, err := ParseClient([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// A client id that does not start with a project number yields no project at all (never guessed).
	c, _ = ParseClient([]byte(`{"web":{"client_id":"custom.apps.googleusercontent.com","client_secret":"s"}}`))
	if c.Project() != "" {
		t.Errorf("guessed a project: %q", c.Project())
	}
}

func TestEnableURLForEveryService(t *testing.T) {
	for service, api := range ServiceAPI {
		want := "https://console.cloud.google.com/apis/library/" + api + "?project=tofi-sample-123456"
		if got := EnableURL(api, "tofi-sample-123456"); got != want {
			t.Errorf("%s: %q", service, got)
		}
		if got := EnableURL(ServiceMCPAPI[service], "622599621014"); got != "https://console.cloud.google.com/apis/library/"+ServiceMCPAPI[service]+"?project=622599621014" {
			t.Errorf("%s mcp: %q", service, got)
		}
		if got := EnableURL(api, ""); got != "https://console.cloud.google.com/apis/enableflow;apiid="+api {
			t.Errorf("%s without a project: %q", service, got)
		}
	}
	for _, api := range []string{"", "evil.googleapis.com", "gmail.googleapis.com/../x", "gmail.googleapis.com?x=1"} {
		if EnableURL(api, "tofi-sample-123456") != "" {
			t.Errorf("linked an unknown API %q", api)
		}
	}
	// A project that is not a valid id or number is dropped, never put in a URL.
	if got := EnableURL("gmail.googleapis.com", "x&redirect=https://evil.example"); got != "https://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com" {
		t.Errorf("unsafe project reached the URL: %q", got)
	}
}

const disabledBody = `{"error":{"code":403,"message":"Gmail API has not been used in project 622599621014 before or it is disabled. Enable it by visiting https://evil.example/?token=1","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","domain":"googleapis.com","metadata":{"service":"gmail.googleapis.com","consumer":"projects/622599621014","activationUrl":"https://evil.example/enable"}}]}}`

func TestParseFailureUsesStructuredFieldsOnly(t *testing.T) {
	f, ok := ParseFailure(`Streamable HTTP error: status 403: ` + disabledBody)
	if !ok || f.API != "gmail.googleapis.com" || f.Project != "622599621014" || f.Reason != "SERVICE_DISABLED" {
		t.Fatalf("not classified: %+v %v", f, ok)
	}
	// The same words without the structured fields are not a classification.
	for _, text := range []string{
		`403: Gmail API has not been used in project 1 before or it is disabled. SERVICE_DISABLED accessNotConfigured`,
		`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"SERVICE_DISABLED"}}`,
		`{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"}]}}`,
		`{"error":{"code":401,"status":"UNAUTHENTICATED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED"}]}}`,
		`{"error":{"code":500,"status":"INTERNAL","details":[{"@type":"type.googleapis.com/google.rpc.Help","reason":"SERVICE_DISABLED"}]}}`,
		``, `no json here`, `{broken`,
	} {
		if f, ok := ParseFailure(text); ok {
			t.Errorf("classified without structure: %q -> %+v", text, f)
		}
	}
	// Legacy REST form.
	if _, ok := ParseFailure(`{"error":{"code":403,"errors":[{"domain":"usageLimits","reason":"accessNotConfigured"}]}}`); !ok {
		t.Error("legacy accessNotConfigured not recognised")
	}
	// An API or project TOFI cannot verify is left out, so no link is built from it.
	f, ok = ParseFailure(`{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","metadata":{"service":"evil.example.com","consumer":"projects/../x"}}]}}`)
	if !ok || f.API != "" || f.Project != "" {
		t.Errorf("unverified values reflected: %+v %v", f, ok)
	}
}
