package mcprunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/mailread"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMailGogHelperProcess(t *testing.T) {
	if os.Getenv("TOFI_MAIL_GOG_HELPER") != "1" {
		return
	}
	if marker := os.Getenv("TOFI_MAIL_GOG_STARTED"); marker != "" {
		if os.WriteFile(marker, []byte("synthetic child started"), 0600) != nil {
			os.Exit(5)
		}
	}
	mailbox := ""
	for i, arg := range os.Args {
		if arg == "--account" && i+1 < len(os.Args) {
			mailbox = os.Args[i+1]
		}
	}
	raw, err := os.ReadFile(os.Getenv("TOFI_MAIL_GOG_FIXTURE"))
	if err != nil {
		os.Exit(2)
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		os.Exit(3)
	}
	root["_fixture_bound_mailbox"] = mailbox
	server := mcp.NewServer(&mcp.Implementation{Name: "synthetic-gog", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{ProtocolVersion}})
	server.AddTool(&mcp.Tool{Name: "gmail_search", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: root, Meta: mcp.Meta{mailread.MetaKey: mailread.Identity{Version: 1, Provider: "gmail", Connection: "forged", Mailbox: "forged@example.test"}}}, nil
	})
	if server.Run(context.Background(), &mcp.StdioTransport{}) != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

type gogArgumentCase struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

func gogArgumentCases(t *testing.T) (allowed, overrides []gogArgumentCase) {
	t.Helper()
	raw, err := os.ReadFile("testdata/gog-argument-parser/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Allowed   []gogArgumentCase `json:"allowed"`
		Overrides []gogArgumentCase `json:"overrides"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases.Allowed, cases.Overrides
}

func TestGogMCPArgumentsKeepRootOptionsRunnerOwned(t *testing.T) {
	allowed, overrides := gogArgumentCases(t)
	for _, tc := range allowed {
		t.Run(tc.Name, func(t *testing.T) {
			original := append([]string(nil), tc.Args...)
			got, err := boundGogMCPArgs("reader-a@example.test", tc.Args)
			want := append([]string{"--account", "reader-a@example.test"}, original...)
			if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(tc.Args, original) {
				t.Fatalf("args=%v err=%v original=%v", got, err, tc.Args)
			}
		})
	}
	for _, tc := range append(overrides, []gogArgumentCase{
		{Name: "root-home", Args: []string{"mcp", "--home", "synthetic-other-home"}},
		{Name: "root-token", Args: []string{"mcp", "--access-token=synthetic-token"}},
		{Name: "root-client", Args: []string{"mcp", "--client", "synthetic-other-client"}},
		{Name: "terminator", Args: []string{"mcp", "--", "--acct", "reader-b@example.test"}},
		{Name: "flag-as-value", Args: []string{"mcp", "--allow-tool", "--acct=reader-b@example.test"}},
		{Name: "missing-value", Args: []string{"mcp", "--allow-tool"}},
		{Name: "invalid-limit", Args: []string{"mcp", "--max-output-bytes=0"}},
		{Name: "invalid-bool", Args: []string{"mcp", "--allow-write=maybe"}},
		{Name: "unknown-mcp-option", Args: []string{"mcp", "--future-account", "reader-b@example.test"}},
		{Name: "missing-command"},
	}...) {
		t.Run(tc.Name, func(t *testing.T) {
			if args, err := boundGogMCPArgs("reader-a@example.test", tc.Args); err == nil || args != nil {
				t.Fatalf("unmanaged argument accepted: %v", args)
			}
		})
	}
}

func TestMailReadRunnerRejectsAccountOverridesBeforeChildLaunch(t *testing.T) {
	_, overrides := gogArgumentCases(t)
	for _, tc := range overrides {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "synthetic-gog")
			marker := filepath.Join(dir, "child-started")
			script := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=TestMailGogHelperProcess -- \"$@\"\n", strings.ReplaceAll(os.Args[0], "'", "'\\''"))
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			fixture, err := filepath.Abs("../mailread/testdata/gog-v0400-search.json")
			if err != nil {
				t.Fatal(err)
			}
			account, _ := json.Marshal(gogAccount{Email: "reader-a@example.test", Scope: "readonly"})
			if err := os.WriteFile(filepath.Join(dir, "gog-account.json"), account, 0600); err != nil {
				t.Fatal(err)
			}
			runner, err := New([]Spec{{ID: "synthetic-gog", Kind: "builtin_gog", Command: bin, Args: tc.Args, WorkDir: dir, Env: map[string]string{"TOFI_MAIL_GOG_HELPER": "1", "TOFI_MAIL_GOG_FIXTURE": fixture, "TOFI_MAIL_GOG_STARTED": marker}}}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := runner.Call(ctx, "synthetic-gog", "gmail_search", map[string]any{})
			if err == nil || result != nil {
				t.Fatalf("override produced a read/receipt: result=%v err=%v", result, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("override reached synthetic child: %v", err)
			}
		})
	}
}
func TestMailReadRunnerBindsActualMailboxAndReplacesPluginMetadata(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "synthetic-gog")
	script := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=TestMailGogHelperProcess -- \"$@\"\n", strings.ReplaceAll(os.Args[0], "'", "'\\''"))
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../mailread/testdata/gog-v0400-search.json")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := New([]Spec{{ID: "synthetic-gog", Kind: "builtin_gog", Command: bin, Args: []string{"mcp", "--allow-tool", "gmail"}, WorkDir: dir, Env: map[string]string{"TOFI_MAIL_GOG_HELPER": "1", "TOFI_MAIL_GOG_FIXTURE": fixture}}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	for _, mailbox := range []string{"reader-a@example.test", "reader-b@example.test"} {
		account, _ := json.Marshal(gogAccount{Email: mailbox, Scope: "readonly"})
		if err = os.WriteFile(filepath.Join(dir, "gog-account.json"), account, 0600); err != nil {
			t.Fatal(err)
		}
		result, err := runner.Call(context.Background(), "synthetic-gog", "gmail_search", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result.Meta[mailread.MetaKey])
		var identity mailread.Identity
		json.Unmarshal(encoded, &identity)
		raw, _ := json.Marshal(result.StructuredContent)
		var root map[string]any
		json.Unmarshal(raw, &root)
		if identity.Mailbox != mailbox || identity.Connection != "synthetic-gog" || root["_fixture_bound_mailbox"] != mailbox {
			t.Fatalf("read boundary not bound: identity=%+v child=%v", identity, root["_fixture_bound_mailbox"])
		}
		if _, err := mailread.NormalizeGog(identity, "gmail_search", result.StructuredContent, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Remove(filepath.Join(dir, "gog-account.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Call(context.Background(), "synthetic-gog", "gmail_search", map[string]any{}); err == nil {
		t.Fatal("disconnected mailbox read accepted")
	}
}
