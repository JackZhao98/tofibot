package mcprunner

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The Discord example of the user-install path: a pinned community package,
// the minimal tool surface and the invite permissions all live in
// docs/integrations/community/discord.json. These checks keep that file, the
// qualification record, the owner's guide and the tool snapshot of the pinned
// version telling the same story, and keep moderation and destructive tools out
// of what the guide tells a person to expose.

type discordPin struct {
	Package struct {
		Kind      string   `json:"kind"`
		Name      string   `json:"name"`
		Version   string   `json:"version"`
		Integrity string   `json:"integrity"`
		Binary    string   `json:"binary"`
		Args      []string `json:"args"`
	} `json:"package"`
	Source struct {
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
		Commit     string `json:"commit"`
	} `json:"source"`
	Env struct {
		Secret      []string          `json:"secret"`
		Recommended map[string]string `json:"recommended"`
	} `json:"env"`
	ToolAllowlist struct {
		Read  []string `json:"read"`
		Write []string `json:"write"`
	} `json:"tool_allowlist"`
	Invite struct {
		Scope          string         `json:"scope"`
		PermissionBits map[string]int `json:"permission_bits"`
		ReadOnlyBits   []string       `json:"read_only_bits"`
		ReadOnly       int            `json:"permissions_read_only"`
		ReadWrite      int            `json:"permissions_read_write"`
	} `json:"invite"`
}

const communityDocs = "../../docs/integrations/community/"

func loadDiscordPin(t *testing.T) discordPin {
	t.Helper()
	raw, err := os.ReadFile(communityDocs + "discord.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin discordPin
	if err := json.Unmarshal(raw, &pin); err != nil {
		t.Fatal(err)
	}
	return pin
}

type snapshotTool struct{ readOnly, destructive bool }

func loadToolSnapshot(t *testing.T, version string) map[string]snapshotTool {
	t.Helper()
	raw, err := os.ReadFile(communityDocs + "discord-mcp-" + version + ".tools.tsv")
	if err != nil {
		t.Fatalf("the pinned version needs a tool snapshot: %v", err)
	}
	tools := map[string]snapshotTool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			t.Fatalf("bad snapshot line %q", line)
		}
		tools[fields[0]] = snapshotTool{readOnly: fields[1] == "read_only", destructive: len(fields) > 2 && fields[2] == "destructive"}
	}
	return tools
}

func TestDiscordPinIsExactAndRecordedWhereAPersonReadsIt(t *testing.T) {
	pin := loadDiscordPin(t)
	if !packageName.MatchString(pin.Package.Name) || !versionName.MatchString(pin.Package.Version) || !binName.MatchString(pin.Package.Binary) || pin.Package.Kind != "npm" {
		t.Fatalf("the pin must be installable through the generic path: %+v", pin.Package)
	}
	if strings.ContainsAny(pin.Package.Version, "^~*xX<>=| ") || strings.Count(pin.Package.Version, ".") != 2 {
		t.Fatalf("the version must be exact: %q", pin.Package.Version)
	}
	if !sriSHA512.MatchString(pin.Package.Integrity) || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(pin.Source.Commit) || pin.Source.Tag != "v"+pin.Package.Version {
		t.Fatalf("integrity, source commit and tag must be exact: %+v %+v", pin.Package, pin.Source)
	}
	for _, name := range []string{"discord-package-qualification.md", "discord-add-it-yourself.md"} {
		raw, err := os.ReadFile("../../docs/integrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, want := range []string{pin.Package.Name, pin.Package.Version, pin.Package.Integrity, pin.Source.Commit} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not record %q", name, want)
			}
		}
	}
}

func TestDiscordToolSurfaceIsMinimalAndNeverModeration(t *testing.T) {
	pin := loadDiscordPin(t)
	snapshot := loadToolSnapshot(t, pin.Package.Version)
	forbidden := regexp.MustCompile(`ban|kick|delete|prune|role|permission|moderat|timeout|member|webhook|_dm|leave|bulk|pipeline|workflow|blueprint|guild_(modify|change)|channels_(create|modify)|automod|invite|command|onboarding|template`)
	for kind, names := range map[string][]string{"read": pin.ToolAllowlist.Read, "write": pin.ToolAllowlist.Write} {
		for _, name := range names {
			tool, ok := snapshot[name]
			if !ok {
				t.Errorf("%s tool %q is not in the pinned version's tool list", kind, name)
				continue
			}
			if tool.destructive || forbidden.MatchString(name) {
				t.Errorf("%s tool %q is moderation or destructive and must not be exposed", kind, name)
			}
			if kind == "read" && !tool.readOnly {
				t.Errorf("read tool %q is not marked read-only by the provider", name)
			}
			if kind == "write" && tool.readOnly {
				t.Errorf("write tool %q is marked read-only by the provider; classify it deliberately before exposing it", name)
			}
		}
	}
	if len(pin.ToolAllowlist.Write) == 0 || len(pin.ToolAllowlist.Write) > 3 || len(pin.ToolAllowlist.Read) > 10 {
		t.Fatalf("the surface must stay small: %d read, %d write", len(pin.ToolAllowlist.Read), len(pin.ToolAllowlist.Write))
	}
	// The package-side category list is a second fence; none of its categories
	// may be a moderation or administration surface.
	for _, category := range strings.Split(pin.Env.Recommended["MCP_CATEGORIES"], ",") {
		switch category {
		case "messages", "channels", "users", "reactions":
		default:
			t.Errorf("category %q widens the surface beyond what the tools need", category)
		}
	}
	if pin.Env.Recommended["MCP_WRITE_MODE"] != "allow" {
		t.Error("the person's approval is the write gate, so the package must not also hold every write in preview")
	}
}

// Discord's documented permission bit positions.
var discordPermissionShift = map[string]int{"ADD_REACTIONS": 6, "VIEW_CHANNEL": 10, "SEND_MESSAGES": 11, "READ_MESSAGE_HISTORY": 16}

func TestDiscordInvitePermissionsAreLeastPrivilegeAndAddUp(t *testing.T) {
	pin := loadDiscordPin(t)
	if pin.Invite.Scope != "bot" {
		t.Fatalf("scope = %q", pin.Invite.Scope)
	}
	total := 0
	for name, value := range pin.Invite.PermissionBits {
		shift, known := discordPermissionShift[name]
		if !known || value != 1<<shift {
			t.Errorf("permission %s=%d is not a least-privilege bit with the documented value", name, value)
		}
		total |= value
	}
	readOnly := 0
	for _, name := range pin.Invite.ReadOnlyBits {
		readOnly |= pin.Invite.PermissionBits[name]
	}
	if total != pin.Invite.ReadWrite || readOnly != pin.Invite.ReadOnly || readOnly == total {
		t.Fatalf("integers do not add up: read-only %d (computed %d), read-write %d (computed %d)", pin.Invite.ReadOnly, readOnly, pin.Invite.ReadWrite, total)
	}
	raw, err := os.ReadFile("../../docs/integrations/discord-add-it-yourself.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"permissions=66560", "permissions=68672", "scope=bot", "Message Content"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the owner's guide does not give %q", want)
		}
	}
}

// What the guide tells a person to type is accepted by the generic install
// path and ends with the token in a private file, not in the manifest.
func TestDiscordGuideRequestInstallsThroughTheGenericPath(t *testing.T) {
	pin := loadDiscordPin(t)
	fakeNPMBinary(t, pin.Package.Integrity, pin.Package.Binary)
	state := t.TempDir()
	r, err := New(nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.SetStateDir(state); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, name := range pin.Env.Secret {
		env[name] = syntheticSecret
	}
	for name, value := range pin.Env.Recommended {
		env[name] = value
	}
	w, body := install(t, r, map[string]any{"id": "discord", "kind": pin.Package.Kind, "package": pin.Package.Name, "version": pin.Package.Version, "binary": pin.Package.Binary, "args": pin.Package.Args, "integrity": pin.Package.Integrity, "env": env})
	if w.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", w.Code, body)
	}
	manifest, _ := os.ReadFile(filepath.Join(state, "manifest.json"))
	if strings.Contains(string(manifest), syntheticSecret) || strings.Contains(body, syntheticSecret) {
		t.Fatal("the token leaked")
	}
}
