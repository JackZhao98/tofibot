package guest

import (
	"context"
	"os"
	"path/filepath"
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
