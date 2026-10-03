package guest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func boundGuest(t *testing.T) *Service {
	t.Helper()
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if err := os.MkdirAll(filepath.Join(s.root, "bots", testBot), 0700); err != nil {
		t.Fatal(err)
	}
	return s
}

func boundIdentity(t *testing.T, s *Service, name string) tooloutcome.Identity {
	t.Helper()
	result, err := s.fileIdentity(testBot, name)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var i tooloutcome.Identity
	if err := json.Unmarshal(raw, &i); err != nil {
		t.Fatal(err)
	}
	return i
}

func guardedHTTP(t *testing.T, s *Service, args fileArgs, i tooloutcome.Identity) (int, ActionResponse) {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	raw, _ := json.Marshal(ActionRequest{BotID: testBot, RunID: "synthetic-guard", Action: "files.write", Source: "model", Args: rawArgs, WriteIdentity: &i})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewReader(raw)))
	var out ActionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return w.Code, out
}

func TestGuardedGuestStableExistingAndNewFileWriteOnce(t *testing.T) {
	s := boundGuest(t)
	profile := filepath.Join(s.root, "bots", testBot)
	if err := os.WriteFile(filepath.Join(profile, "existing"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("before"))
	for _, args := range []fileArgs{{Path: "existing", Content: "after", ExpectedSHA256: hex.EncodeToString(h[:])}, {Path: "nested/new", Content: "X", Append: true}} {
		i := boundIdentity(t, s, args.Path)
		status, out := guardedHTTP(t, s, args, i)
		if status != 200 || !out.OK {
			t.Fatalf("write %s: %d %+v", args.Path, status, out)
		}
		got, err := os.ReadFile(filepath.Join(profile, args.Path))
		if err != nil || string(got) != args.Content {
			t.Fatalf("%s=%q err=%v", args.Path, got, err)
		}
	}
}

func TestGuardedGuestRetargetAfterOpenWritesOnlyBoundDescriptor(t *testing.T) {
	s := boundGuest(t)
	profile := filepath.Join(s.root, "bots", testBot)
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(profile, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(profile, "candidate")
	if err := os.Symlink("a", link); err != nil {
		t.Fatal(err)
	}
	args := fileArgs{Path: "candidate", Content: "X", Append: true}
	i := boundIdentity(t, s, args.Path)
	f, err := s.openBoundWriteFile(context.Background(), testBot, args, i)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Replace both the original alias and physical path after the file is open.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b", link); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(profile, "a"), filepath.Join(profile, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b", filepath.Join(profile, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeBoundDescriptor(context.Background(), f, args); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"original": "X", "a": "", "b": "", "candidate": ""} {
		got, err := os.ReadFile(filepath.Join(profile, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s=%q want=%q err=%v", name, got, want, err)
		}
	}
	info, err := f.Stat()
	if err != nil || fileObject(info) != i.Object {
		t.Fatal("descriptor identity changed", err)
	}
}

func TestGuardedGuestMismatchHasNoMutationOrLegacyFallback(t *testing.T) {
	for _, mode := range []string{"file-replaced", "parent-replaced", "unexpected-entry", "symlink-retarget"} {
		t.Run(mode, func(t *testing.T) {
			s := boundGuest(t)
			profile := filepath.Join(s.root, "bots", testBot)
			name := "a"
			if err := os.WriteFile(filepath.Join(profile, "a"), []byte("A"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profile, "b"), []byte("B"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "parent-replaced":
				if err := os.Mkdir(filepath.Join(profile, "folder"), 0700); err != nil {
					t.Fatal(err)
				}
				name = "folder/new"
			case "unexpected-entry":
				name = "new"
			case "symlink-retarget":
				name = "candidate"
				if err := os.Symlink("a", filepath.Join(profile, name)); err != nil {
					t.Fatal(err)
				}
			}
			i := boundIdentity(t, s, name)
			switch mode {
			case "file-replaced":
				if err := os.Rename(filepath.Join(profile, "a"), filepath.Join(profile, "original")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(profile, "a"), []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			case "parent-replaced":
				if err := os.Rename(filepath.Join(profile, "folder"), filepath.Join(profile, "original-folder")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(profile, "folder"), 0700); err != nil {
					t.Fatal(err)
				}
			case "unexpected-entry":
				if err := os.WriteFile(filepath.Join(profile, "new"), []byte("winner"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-retarget":
				if err := os.Remove(filepath.Join(profile, name)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("b", filepath.Join(profile, name)); err != nil {
					t.Fatal(err)
				}
			}
			status, out := guardedHTTP(t, s, fileArgs{Path: name, Content: "MUST_NOT_WRITE"}, i)
			if status != 409 || out.OK || out.Outcome == nil || out.Outcome.Code != "write_identity_changed" || out.Outcome.Certainty != "not_executed" {
				t.Fatalf("mismatch: %d %+v", status, out)
			}
			for file, want := range map[string]string{"a": "A", "b": "B"} {
				if mode == "file-replaced" && file == "a" {
					want = "replacement"
				}
				got, err := os.ReadFile(filepath.Join(profile, file))
				if err != nil || string(got) != want {
					t.Fatalf("%s=%q err=%v", file, got, err)
				}
			}
			if mode == "unexpected-entry" {
				got, _ := os.ReadFile(filepath.Join(profile, "new"))
				if string(got) != "winner" {
					t.Fatalf("exclusive create overwrote winner: %q", got)
				}
			}
			if mode == "parent-replaced" {
				if _, err := os.Stat(filepath.Join(profile, name)); !os.IsNotExist(err) {
					t.Fatal("changed parent mutated", err)
				}
			}
		})
	}
}
