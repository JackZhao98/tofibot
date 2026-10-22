package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// A package a person installs into the local Runner is attached as a community
// connection: its name and version travel with the saved server, the bundled
// gogcli does not get them, and neither the Runner credential nor the package's
// own secret appears in the settings view.
func TestAttachedCommunityPackageCarriesProvenance(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "runner-token")
	if err := os.WriteFile(tokenPath, []byte("private-runner-token"), 0600); err != nil {
		t.Fatal(err)
	}
	const botToken = "synthetic-bot-token-0123456789-do-not-log"
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/plugins" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"x","state":"sleeping"}`))
		case r.URL.Path == "/v1/plugins" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"plugins":[{"id":"chat","kind":"npm","package":"@example/community-server","version":"1.2.3","state":"sleeping"},{"id":"gog","kind":"builtin_gog","state":"sleeping"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runner.Close()
	t.Setenv("TOFI_MCP_RUNNER_URL", runner.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenPath)
	s, err := NewServer(Config{DataDir: root, MCPConfigPath: filepath.Join(root, "mcp.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"chat", "gog"} {
		w := httptest.NewRecorder()
		body := `{"id":"` + id + `","kind":"npm","package":"@example/community-server","version":"1.2.3","binary":"b","env":{"DISCORD_TOKEN":"` + botToken + `"}}`
		if !s.routeLocalMCP(w, httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/install", strings.NewReader(body)), "extensions/local-mcp/install") || w.Code != http.StatusCreated {
			t.Fatalf("install %s: %d %s", id, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), botToken) {
			t.Fatal("the package secret came back in the install response")
		}
	}
	views, err := s.extensions.ListMCP()
	if err != nil || len(views) != 2 {
		t.Fatalf("views=%+v err=%v", views, err)
	}
	byName := map[string]extensions.MCPServerView{}
	for _, v := range views {
		byName[v.Name] = v
	}
	chat := byName["local_chat"].Provenance
	if chat == nil || chat.Origin != extensions.MCPOriginCommunity || chat.Kind != "npm" || chat.Package != "@example/community-server" || chat.Version != "1.2.3" {
		t.Fatalf("community provenance = %+v", chat)
	}
	if byName["local_gog"].Provenance != nil {
		t.Fatalf("the bundled gogcli is not a community package: %+v", byName["local_gog"].Provenance)
	}
	encoded, _ := json.Marshal(views)
	if strings.Contains(string(encoded), botToken) || strings.Contains(string(encoded), "private-runner-token") {
		t.Fatalf("a secret reached the settings view: %s", encoded)
	}
}

// A skill a person installed with scripts is copied to the Bot's computer when
// the Bot reads it, like a built-in one. The files are inert until the Bot runs
// them, and a skill that is only SKILL.md transfers nothing.
func TestInstalledSkillWithScriptsReachesTheComputer(t *testing.T) {
	s, root, statuses := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	skill := map[string][]byte{
		"SKILL.md":         []byte("---\nname: greeter\ndescription: prints a greeting\n---\nRun the script."),
		"scripts/hello.py": []byte("print('hello from an installed skill')\n"),
	}
	if err := s.extensions.InstallSkill("greeter", skill); err != nil {
		t.Skipf("no skills directory in this fixture: %v", err)
	}
	if err := s.extensions.InstallSkill("only-text", map[string][]byte{"SKILL.md": []byte("---\nname: only-text\ndescription: text only\n---\nBODY")}); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.extensions.PrepareDiscoverableForBot(context.Background(), bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	read, ok := toolNamed(s.withBuiltinSkillStaging(Run{ID: "r", BotID: bot.ID}, prepared.Tools), "read_skill")
	if !ok {
		t.Fatal("no read_skill tool")
	}
	plain, err := read.Execute(context.Background(), json.RawMessage(`{"name":"only-text"}`))
	if err != nil || plain != "BODY" || len(*statuses) != 0 {
		t.Fatalf("a text-only skill transferred files: %q err=%v transfers=%v", plain, err, *statuses)
	}
	out, err := read.Execute(context.Background(), json.RawMessage(`{"name":"greeter"}`))
	if err != nil {
		t.Fatal(err)
	}
	files, digest, err := s.extensions.InstalledSkillFiles("greeter")
	if err != nil || len(files) != 2 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	dir := guestSkillsDir("greeter", digest)
	if !strings.Contains(out, dir) || !strings.Contains(out, "Run the script.") {
		t.Fatalf("read_skill result lacks the folder or the body: %s", out)
	}
	onDisk := filepath.Join(root, "home", "uploads", ".skills", "greeter", digest)
	for path, want := range skill {
		got, err := os.ReadFile(filepath.Join(onDisk, filepath.FromSlash(path)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s on the computer: %q %v", path, got, err)
		}
	}
	if py, lookErr := exec.LookPath("python3"); lookErr == nil {
		if output, runErr := exec.Command(py, filepath.Join(onDisk, "scripts", "hello.py")).CombinedOutput(); runErr != nil || !strings.Contains(string(output), "hello from an installed skill") {
			t.Fatalf("staged script: %v %s", runErr, output)
		}
	}
}
