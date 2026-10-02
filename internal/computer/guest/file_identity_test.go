package guest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileIdentityResolvesAliasesAndMissingTargetsWithoutMutation(t *testing.T) {
	root := t.TempDir()
	s := &Service{root: root}
	const bot = "00000000-0000-4000-8000-000000000001"
	profile := filepath.Join(root, "bots", bot)
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(profile, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	a, err := s.fileIdentity(bot, "new/note")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.fileIdentity(bot, filepath.Join(root, "alias/new/note"))
	if err != nil {
		t.Fatal(err)
	}
	if a["target"] != b["target"] {
		t.Fatalf("alias differs: %v %v", a, b)
	}
	if _, err := os.Stat(filepath.Join(profile, "new")); !os.IsNotExist(err) {
		t.Fatal("lookup created a directory", err)
	}
	if err := os.WriteFile(filepath.Join(profile, "note"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("note", filepath.Join(profile, "link")); err != nil {
		t.Fatal(err)
	}
	a, err = s.fileIdentity(bot, "note")
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.fileIdentity(bot, "link")
	if err != nil {
		t.Fatal(err)
	}
	if a["target"] != b["target"] || a["object"] != b["object"] {
		t.Fatalf("file aliases differ: %v %v", a, b)
	}
	if err := os.Link(filepath.Join(profile, "note"), filepath.Join(profile, "hardlink")); err != nil {
		t.Fatal(err)
	}
	b, err = s.fileIdentity(bot, "hardlink")
	if err != nil {
		t.Fatal(err)
	}
	if a["object"] != b["object"] {
		t.Fatal("hardlink object differs", a, b)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(profile, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.fileIdentity(bot, "escape/note"); err == nil {
		t.Fatal("escaped target accepted")
	}
	if _, err := s.fileIdentity("00000000-0000-4000-8000-000000000002", "note"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "bots", "00000000-0000-4000-8000-000000000002")); !os.IsNotExist(err) {
		t.Fatal("lookup created a Bot profile", err)
	}
}

func TestFileIdentityHTTPDoesNotCreateWorkspaceOrAlias(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	const bot = "00000000-0000-4000-8000-000000000003"
	r := httptest.NewRequest(http.MethodPost, "/v1/action", strings.NewReader(`{"bot_id":"`+bot+`","bot_name":"synthetic-alias","run_id":"synthetic-lookup","action":"files.identity","args":{"path":"new/note"},"source":"model"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var result struct {
		Result struct {
			Target string `json:"target"`
		} `json:"result"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Result.Target != filepath.Join(s.root, "bots", bot, "new/note") {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.root, "bots", bot)); !os.IsNotExist(err) {
		t.Fatal("HTTP lookup created Bot workspace", err)
	}
	if _, err := os.Lstat(filepath.Join(s.root, "synthetic-alias")); !os.IsNotExist(err) {
		t.Fatal("HTTP lookup created alias", err)
	}
}
