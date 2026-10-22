package mcprunner

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFakeGogProcess(t *testing.T) {
	if os.Getenv("TOFI_FAKE_GOG") != "1" {
		return
	}
	args := strings.Join(os.Args, " ")
	switch {
	case strings.Contains(args, "credentials set -"):
		_, _ = io.Copy(io.Discard, os.Stdin)
	case strings.Contains(args, "--step 1"):
		_, _ = fmt.Fprint(os.Stdout, `{"auth_url":"https://accounts.google.com/o/oauth2/v2/auth?state=fixture-state","state_reused":false}`)
	case strings.Contains(args, "--step 2"):
		if !strings.Contains(args, "state=fixture-state") {
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stdout, `{"ok":true}`)
	case strings.Contains(args, "gmail search"):
		_, _ = fmt.Fprint(os.Stdout, `{"messages":[]}`)
	case strings.Contains(args, "gmail send"):
		if !strings.Contains(args, "--body-file -") || strings.Contains(args, "private draft body") {
			os.Exit(4)
		}
		body, _ := io.ReadAll(os.Stdin)
		if string(body) != "private draft body" {
			os.Exit(5)
		}
		_, _ = fmt.Fprint(os.Stdout, `{"id":"sent-1"}`)
	case strings.Contains(args, "auth remove"):
		_, _ = fmt.Fprint(os.Stdout, `{"ok":true}`)
	default:
		os.Exit(3)
	}
	os.Exit(0)
}

func TestGogWebAuthorizationAndReadVerification(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-gog")
	script := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=TestFakeGogProcess -- \"$@\"\n", strings.ReplaceAll(os.Args[0], "'", "'\\''"))
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := New([]Spec{{ID: "gog", Kind: "builtin_gog", Command: bin, WorkDir: dir, Env: map[string]string{"TOFI_FAKE_GOG": "1"}}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	redirect := "https://tofi.example/api/extensions/local-mcp/gog/oauth/callback"
	credentials, _ := json.Marshal(map[string]any{"web": map[string]any{"client_id": "id", "client_secret": "secret", "redirect_uris": []string{redirect}}})
	result, err := r.GogStart(context.Background(), "gog", GogStartRequest{Email: "person@gmail.com", CredentialsJSON: credentials, RedirectURI: redirect})
	if err != nil || !strings.Contains(result.AuthorizationURL, "fixture-state") {
		t.Fatalf("start=%v err=%v", result, err)
	}
	if err := r.GogFinish(context.Background(), "gog", GogFinishRequest{RedirectedURL: redirect + "?code=code&state=wrong"}); err == nil {
		t.Fatal("wrong state accepted")
	}
	if err := r.GogFinish(context.Background(), "gog", GogFinishRequest{RedirectedURL: redirect + "?code=code&state=fixture-state"}); err != nil {
		t.Fatal(err)
	}
	status, err := r.GogStatus("gog")
	if err != nil || status.Email != "person@gmail.com" {
		t.Fatalf("status=%v err=%v", status, err)
	}
	if err := r.GogCheck(context.Background(), "gog"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GogSend(context.Background(), "gog", GogSendRequest{To: "person@example.com", Subject: "Hello", Body: "private draft body"}); err == nil {
		t.Fatal("read-only account sent mail")
	}
	if _, err := r.GogStart(context.Background(), "gog", GogStartRequest{Email: "person@gmail.com", CredentialsJSON: credentials, RedirectURI: redirect, Scope: "read-send"}); err != nil {
		t.Fatal(err)
	}
	if err := r.GogFinish(context.Background(), "gog", GogFinishRequest{RedirectedURL: redirect + "?code=code&state=fixture-state"}); err != nil {
		t.Fatal(err)
	}
	status, err = r.GogStatus("gog")
	if err != nil || status.Scope != "read-send" {
		t.Fatalf("send scope status=%+v err=%v", status, err)
	}
	sentResult, err := r.GogSend(context.Background(), "gog", GogSendRequest{To: "person@example.com", Subject: "Hello", Body: "private draft body"})
	if err != nil || !strings.Contains(string(sentResult), "sent-1") {
		t.Fatalf("send result=%s err=%v", sentResult, err)
	}
	if _, err := r.GogSend(context.Background(), "gog", GogSendRequest{To: "person@example.com\nBcc:other@example.com", Subject: "Hello", Body: "private draft body"}); err == nil {
		t.Fatal("header injection accepted")
	}
	if err := r.GogDisconnect(context.Background(), "gog"); err != nil {
		t.Fatal(err)
	}
	status, err = r.GogStatus("gog")
	if err != nil || status.Email != "" {
		t.Fatalf("disconnect status=%v err=%v", status, err)
	}
}

func TestPinnedGogCLIProtocolCompatibility(t *testing.T) {
	binary := os.Getenv("TOFI_GOG_BIN")
	if binary == "" {
		t.Skip("set TOFI_GOG_BIN to the pinned gogcli binary")
	}
	build, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if build.Main.Path != "github.com/openclaw/gogcli" || build.Main.Version != "v0.40.0" {
		t.Fatalf("compatibility fixture does not match the Docker pin: %s %s", build.Main.Path, build.Main.Version)
	}
	t.Logf("pinned gogcli module %s %s (%s), compiler %s; Runner requires %s", build.Main.Path, build.Main.Version, build.Main.Sum, build.GoVersion, ProtocolVersion)
	dir := t.TempDir()
	secret := filepath.Join(dir, "keyring-password")
	if err := os.WriteFile(secret, []byte("fixture-password"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := New([]Spec{{ID: "gog", Kind: "builtin_gog", Command: binary, Args: []string{"mcp", "--allow-tool", "gmail"}, WorkDir: dir, Env: map[string]string{"GOG_HOME": dir, "GOG_KEYRING_BACKEND": "file", "HOME": dir}, SecretEnv: map[string]string{"GOG_KEYRING_PASSWORD": secret}}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tools, err := r.Tools(ctx, "gog")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name == "gmail_search" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gogcli exposed no Gmail search tool: %d tools", len(tools))
	}
	redirect := "https://tofi.example/api/extensions/local-mcp/gog/oauth/callback"
	credentials, _ := json.Marshal(map[string]any{"web": map[string]any{"client_id": "fixture.apps.googleusercontent.com", "client_secret": "fixture-secret", "redirect_uris": []string{redirect}}})
	started, err := r.GogStart(ctx, "gog", GogStartRequest{Email: "person@gmail.com", CredentialsJSON: credentials, RedirectURI: redirect})
	if err != nil || !strings.HasPrefix(started.AuthorizationURL, "https://accounts.google.com/") {
		t.Fatalf("real gogcli OAuth URL=%q err=%v", started.AuthorizationURL, err)
	}
	authorization, err := url.Parse(started.AuthorizationURL)
	if err != nil || authorization.Query().Get("redirect_uri") != redirect || !strings.Contains(authorization.Query().Get("scope"), "gmail.readonly") {
		t.Fatalf("unexpected real gogcli authorization URL: %v err=%v", authorization, err)
	}
}

func TestGogServicesAreIncrementalAndBounded(t *testing.T) {
	got, err := gogServiceList(nil, nil)
	if err != nil || strings.Join(got, ",") != "gmail" {
		t.Fatalf("default: %v %v", got, err)
	}
	// A second service is added to what the account already has; nothing is dropped.
	got, err = gogServiceList([]string{"calendar"}, []string{"gmail"})
	if err != nil || strings.Join(got, ",") != "gmail,calendar" {
		t.Fatalf("incremental: %v %v", got, err)
	}
	got, _ = gogServiceList([]string{"drive", "calendar"}, nil)
	if strings.Join(got, ",") != "calendar,drive" {
		t.Fatalf("order is stable: %v", got)
	}
	if _, err := gogServiceList([]string{"gmail", "admin-sdk"}, nil); err == nil {
		t.Fatal("an unknown service must be refused")
	}
}

func TestGogAuthorizationURLCarriesIncludeGrantedScopes(t *testing.T) {
	got, err := withIncrementalAuth("https://accounts.google.com/o/oauth2/auth?client_id=c&state=s&scope=a")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("include_granted_scopes") != "true" || u.Query().Get("state") != "s" || u.Query().Get("scope") != "a" {
		t.Fatalf("url: %s", got)
	}
	for _, bad := range []string{"https://evil.example/auth", "http://accounts.google.com/x", "://"} {
		if _, err := withIncrementalAuth(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestGogFailureIsClassifiedFromTheStructuredError(t *testing.T) {
	body := `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"PRIVATE https://evil.example","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","metadata":{"service":"gmail.googleapis.com","consumer":"projects/622599621014"}}]}}`
	err := classifyGogFailure(&gogCommandError{err: io.EOF, output: "gog: " + body}, "gmail.googleapis.com")
	var disabled *APIDisabled
	if !errors.As(err, &disabled) || disabled.API != "gmail.googleapis.com" || disabled.Project != "622599621014" {
		t.Fatalf("not classified: %v", err)
	}
	if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "evil") {
		t.Fatal("provider text in the error")
	}
	if classifyGogFailure(&gogCommandError{err: io.EOF, output: "SERVICE_DISABLED accessNotConfigured"}, "gmail.googleapis.com") != nil {
		t.Fatal("wording alone must not classify")
	}
	if classifyGogFailure(io.EOF, "gmail.googleapis.com") != nil {
		t.Fatal("a plain error is not a Google failure")
	}
}
