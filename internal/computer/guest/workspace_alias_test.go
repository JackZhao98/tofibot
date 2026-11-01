package guest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceAliasUsesReadableBotNameAndCanonicalTarget(t *testing.T) {
	root := t.TempDir()
	s, err := NewWithIdleTimeout(root, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	botID := "11111111-1111-4111-8111-111111111111"
	alias, err := s.ensureWorkspaceAlias(botID, "DevOps 助手")
	if err != nil {
		t.Fatal(err)
	}
	wantAlias := filepath.Join(s.root, "home", "bots", "DevOps-助手--11111111")
	if alias != wantAlias {
		t.Fatalf("alias = %q, want %q", alias, wantAlias)
	}
	target, err := os.Readlink(alias)
	if err != nil {
		t.Fatal(err)
	}
	wantTarget := filepath.Join(s.root, "bots", botID)
	if target != wantTarget {
		t.Fatalf("target = %q, want %q", target, wantTarget)
	}

	renamed, err := s.ensureWorkspaceAlias(botID, "Linux/Helper")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(alias); !os.IsNotExist(err) {
		t.Fatalf("old alias still exists: %v", err)
	}
	wantRenamed := filepath.Join(s.root, "home", "bots", "Linux-Helper--11111111")
	if renamed != wantRenamed {
		t.Fatalf("renamed alias = %q, want %q", renamed, wantRenamed)
	}
}

func aliasCollisionRun(t *testing.T, prepare func(s *Service, aliasPath string)) (*Service, string) {
	t.Helper()
	s := newTestService(t)
	aliasRoot := filepath.Join(s.root, "home", "bots")
	if err := os.MkdirAll(aliasRoot, 0770); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(aliasRoot, "Probe--"+testBot[:8])
	prepare(s, aliasPath)
	res, err := s.action(context.Background(), ActionRequest{
		BotID: testBot, BotName: "Probe", RunID: "alias-collision", Action: "shell.exec",
		Args: []byte(`{"command":"printf still-works"}`),
	})
	if err != nil {
		t.Fatalf("action failed on alias collision: %v", err)
	}
	if !strings.Contains(fmt.Sprint(res), "still-works") {
		t.Fatalf("command did not run: %v", res)
	}
	return s, aliasPath
}

func TestAliasCollidingDirectoryDoesNotBlockComputer(t *testing.T) {
	var marker string
	_, aliasPath := aliasCollisionRun(t, func(s *Service, aliasPath string) {
		if err := os.Mkdir(aliasPath, 0770); err != nil {
			t.Fatal(err)
		}
		marker = filepath.Join(aliasPath, "bot-file.txt")
		if err := os.WriteFile(marker, []byte("keep"), 0660); err != nil {
			t.Fatal(err)
		}
	})
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Fatalf("colliding directory was touched: %v %q", err, b)
	}
	if info, err := os.Lstat(aliasPath); err != nil || !info.IsDir() {
		t.Fatalf("colliding directory missing: %v", err)
	}
}

func TestAliasPointingElsewhereDoesNotBlockComputer(t *testing.T) {
	var other string
	_, aliasPath := aliasCollisionRun(t, func(s *Service, aliasPath string) {
		other = filepath.Join(s.root, "bots", "22222222-2222-4222-8222-222222222222")
		if err := os.MkdirAll(other, 0770); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, aliasPath); err != nil {
			t.Fatal(err)
		}
	})
	if got, err := os.Readlink(aliasPath); err != nil || got != other {
		t.Fatalf("foreign symlink changed: %v %q", err, got)
	}
}

func TestAliasCollisionUsesFallbackLink(t *testing.T) {
	s, _ := aliasCollisionRun(t, func(s *Service, aliasPath string) {
		if err := os.Mkdir(aliasPath, 0770); err != nil {
			t.Fatal(err)
		}
	})
	alias, ok := s.workspaceAlias(testBot)
	want := filepath.Join(s.root, "home", "bots", "Probe--"+testBot[:8]+"-link")
	if !ok || alias != want {
		t.Fatalf("workspaceAlias = %q, %v; want %q", alias, ok, want)
	}
}

func TestActionProceedsWhenWorkspaceAliasCannotBePlaced(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	// A plain file where the alias directory belongs makes every alias step fail.
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(home, 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "bots")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bots"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	botID := "11111111-1111-4111-8111-111111111111"
	if _, err := s.ensureWorkspaceAlias(botID, "Helper"); err == nil {
		t.Fatal("expected the alias step itself to fail in this setup")
	}
	_, err = s.action(context.Background(), ActionRequest{BotID: botID, RunID: "r", Source: "model", Action: "files.list", Args: []byte(`{}`)})
	if err != nil && strings.Contains(err.Error(), "workspace alias") {
		t.Fatalf("alias failure aborted the action: %v", err)
	}
}
