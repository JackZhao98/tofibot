package mcprunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const syntheticSecret = "synthetic-bot-token-0123456789-do-not-log"

// A community MCP server stand-in: it speaks the latest protocol over stdio,
// labels a read as read-only, a write without annotations, and a write with a
// wrong read-only label. Its token_state tool reports that the secret arrived
// without ever returning it.
func TestRunnerCommunityHelperProcess(t *testing.T) {
	if os.Getenv("TOFI_COMMUNITY_HELPER") != "1" {
		return
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "community-fixture", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{ProtocolVersion}})
	type none struct{}
	text := func(v string) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: v}}}, nil, nil
	}
	mcp.AddTool(s, &mcp.Tool{Name: "messages_read", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, any, error) {
		return text("read")
	})
	mcp.AddTool(s, &mcp.Tool{Name: "messages_send"}, func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, any, error) {
		return text("sent")
	})
	mcp.AddTool(s, &mcp.Tool{Name: "mislabelled_delete", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, any, error) {
		return text("deleted")
	})
	mcp.AddTool(s, &mcp.Tool{Name: "token_state"}, func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, any, error) {
		return text(fmt.Sprintf("token_present=%v length=%d", os.Getenv("DISCORD_TOKEN") != "", len(os.Getenv("DISCORD_TOKEN"))))
	})
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

// fakeNPM puts an `npm` first on PATH that installs the stand-in above as the
// package binary and records the integrity the registry "served".
func fakeNPM(t *testing.T, servedIntegrity string) {
	fakeNPMBinary(t, servedIntegrity, "community-server")
}

func fakeNPMBinary(t *testing.T, servedIntegrity, binary string) {
	t.Helper()
	bin := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in --prefix) prefix="$2"; shift;; esac
  last="$1"; shift
done
pkg="${last%%@*}"; case "$last" in @*) rest="${last#@}"; pkg="@${rest%%@*}";; esac
version="${last##*@}"
mkdir -p "$prefix/node_modules/.bin"
cat > "$prefix/node_modules/.bin/%s" <<'EOS'
#!/bin/sh
TOFI_COMMUNITY_HELPER=1 exec %s -test.run=TestRunnerCommunityHelperProcess
EOS
chmod 755 "$prefix/node_modules/.bin/%s"
cat > "$prefix/node_modules/.package-lock.json" <<EOL
{"name":"x","lockfileVersion":3,"packages":{"node_modules/$pkg":{"version":"$version","integrity":"%s"}}}
EOL
`, binary, os.Args[0], binary, servedIntegrity)
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func sri(fill byte) string {
	return "sha512-" + strings.Repeat(string([]byte{fill}), 86) + "=="
}

func install(t *testing.T, r *Runner, body map[string]any) (*httptest.ResponseRecorder, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/plugins", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer control")
	w := httptest.NewRecorder()
	r.Handler("control").ServeHTTP(w, req)
	return w, w.Body.String()
}

// The user-install path end to end with a stand-in package: the secret reaches
// the child only through its private file, appears in no response, status or
// manifest, and the status carries the provenance the connection shows.
func TestCommunityInstallKeepsSecretOutOfEveryResponseAndRecordsProvenance(t *testing.T) {
	fakeNPM(t, sri('A'))
	state := t.TempDir()
	r, err := New(nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.SetStateDir(state); err != nil {
		t.Fatal(err)
	}
	w, body := install(t, r, map[string]any{"id": "community", "kind": "npm", "package": "@example/community-server", "version": "1.2.3", "binary": "community-server", "args": []string{"serve"}, "integrity": sri('A'), "env": map[string]string{"DISCORD_TOKEN": syntheticSecret}})
	if w.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", w.Code, body)
	}
	assertNoSecret := func(where, text string) {
		t.Helper()
		if strings.Contains(text, syntheticSecret) {
			t.Fatalf("secret leaked through %s: %s", where, text)
		}
	}
	assertNoSecret("the install response", body)
	list := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/plugins", nil)
	req.Header.Set("Authorization", "Bearer control")
	r.Handler("control").ServeHTTP(list, req)
	assertNoSecret("the status response", list.Body.String())
	statuses := r.Statuses()
	if len(statuses) != 1 || statuses[0].Kind != "npm" || statuses[0].Package != "@example/community-server" || statuses[0].Version != "1.2.3" {
		t.Fatalf("provenance missing from status: %+v", statuses)
	}
	manifest, err := os.ReadFile(filepath.Join(state, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret("the manifest", string(manifest))
	secretFile := filepath.Join(state, "plugins", "community", "secrets", "DISCORD_TOKEN")
	if info, err := os.Stat(secretFile); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("secret file: %v %v", info, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools, err := r.Tools(ctx, "community")
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(tools); strings.Contains(string(encoded), syntheticSecret) {
		t.Fatal("secret leaked through tool discovery")
	}
	result, err := r.Call(ctx, "community", "token_state", nil)
	if err != nil || result.IsError {
		t.Fatalf("call: %v %v", result, err)
	}
	got := result.Content[0].(*mcp.TextContent).Text
	assertNoSecret("a tool result", got)
	if got != fmt.Sprintf("token_present=true length=%d", len(syntheticSecret)) {
		t.Fatalf("the child did not receive the secret: %s", got)
	}
}

// A person may pin the package digest. A registry that serves anything else is
// refused and leaves nothing installed; a malformed digest is refused before
// any download.
func TestCommunityInstallIntegrityPin(t *testing.T) {
	fakeNPM(t, sri('B'))
	state := t.TempDir()
	r, err := New(nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.SetStateDir(state); err != nil {
		t.Fatal(err)
	}
	request := func(integrity string) map[string]any {
		return map[string]any{"id": "pinned", "kind": "npm", "package": "@example/community-server", "version": "1.2.3", "binary": "community-server", "integrity": integrity}
	}
	if w, body := install(t, r, request(sri('A'))); w.Code != http.StatusBadRequest || !strings.Contains(body, "pinned integrity") {
		t.Fatalf("a different artifact must be refused: %d %s", w.Code, body)
	}
	if _, err := os.Stat(filepath.Join(state, "plugins", "pinned")); !os.IsNotExist(err) || len(r.Statuses()) != 0 {
		t.Fatal("a refused install must leave nothing behind")
	}
	if w, body := install(t, r, request("sha512-short")); w.Code != http.StatusBadRequest || !strings.Contains(body, "integrity must be") {
		t.Fatalf("malformed digest: %d %s", w.Code, body)
	}
	if w, body := install(t, r, map[string]any{"id": "py", "kind": "pypi", "package": "example", "version": "1.0.0", "binary": "example", "integrity": sri('B')}); w.Code != http.StatusBadRequest || !strings.Contains(body, "npm packages only") {
		t.Fatalf("pypi has no digest pin: %d %s", w.Code, body)
	}
	if w, body := install(t, r, request(sri('B'))); w.Code != http.StatusCreated {
		t.Fatalf("matching digest: %d %s", w.Code, body)
	}
}

func TestVerifyInstalledIntegrityRequiresTheNamedPackageAtThePinnedVersion(t *testing.T) {
	stage := t.TempDir()
	write := func(content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(stage, "node_modules"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "node_modules", ".package-lock.json"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := sri('C')
	write(`{"packages":{"node_modules/pkg":{"version":"1.0.0","integrity":"` + want + `"}}}`)
	if err := verifyInstalledIntegrity(stage, "pkg", "v1.0.0", want); err != nil {
		t.Fatalf("matching: %v", err)
	}
	for name, content := range map[string]string{
		"other digest":                         `{"packages":{"node_modules/pkg":{"version":"1.0.0","integrity":"` + sri('D') + `"}}}`,
		"other version":                        `{"packages":{"node_modules/pkg":{"version":"1.0.1","integrity":"` + want + `"}}}`,
		"only a dependency carries the digest": `{"packages":{"node_modules/dep":{"version":"1.0.0","integrity":"` + want + `"}}}`,
		"unreadable":                           `{`,
	} {
		write(content)
		if err := verifyInstalledIntegrity(stage, "pkg", "1.0.0", want); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}
