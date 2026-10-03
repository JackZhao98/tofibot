package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Reviewer race schedules are unchanged. Count successful guest mutations separately
// from rejected HTTP write requests: the new guard rejects at the Guest boundary.
// Test-only overlay: deterministic concurrent path changes at request boundaries.
// Uses the candidate guest, app, runtime and existing synthetic provider fixture.
func TestReviewerUncertainWritePathChanges(t *testing.T) {
	for _, mode := range []string{"lookup-before-dispatch", "postfailure-replaces-evidence"} {
		t.Run(mode, func(t *testing.T) {
			s, err := NewServer(Config{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b, _ := s.store.CreateBot("synthetic", "", "synthetic")
			r, _ := s.store.AddRun(b.DMConversationID, b.ID, "")
			_, _ = s.store.SetRunStatus(r.ID, "running", "")
			root := t.TempDir()
			g, err := guest.NewWithIdleTimeout(root, 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close(context.Background())
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(root, "bots", b.ID)
			if err = os.MkdirAll(profile, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "b"} {
				if err = os.WriteFile(filepath.Join(profile, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(profile, "candidate")
			initial := "b"
			if mode == "postfailure-replaces-evidence" {
				initial = "a"
			}
			if err = os.Symlink(initial, link); err != nil {
				t.Fatal(err)
			}
			retarget := func(name string) {
				t.Helper()
				if e := os.Remove(link); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(name, link); e != nil {
					t.Fatal(e)
				}
			}
			writes, dispatches, rejected := 0, 0, 0
			client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-unused-review-race.sock", Client: &http.Client{Transport: reviewTransport(func(req *http.Request) (*http.Response, error) {
				var a computer.Action
				if e := json.NewDecoder(req.Body).Decode(&a); e != nil {
					return nil, e
				}
				var args struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(a.Args, &args)
				raw, _ := json.Marshal(a)
				w := httptest.NewRecorder()
				g.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewReader(raw)).WithContext(req.Context()))
				if a.Name == "files.identity" && args.Path == "candidate" && mode == "lookup-before-dispatch" {
					// Lookup verified b, then another actor repoints the path to a.
					retarget("a")
				}
				if a.Name == "files.write" {
					dispatches++
					if w.Code == 200 {
						writes++
					} else {
						rejected++
					}
					if dispatches == 1 {
						if w.Code != 200 {
							t.Fatalf("first write: %d %s", w.Code, w.Body.String())
						}
						if mode == "postfailure-replaces-evidence" {
							retarget("b")
						}
						return nil, errors.New("synthetic lost response after append")
					}
				}
				return w.Result(), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			s.microVM = client
			first, second := "a", "candidate"
			if mode == "postfailure-replaces-evidence" {
				first, second = "candidate", "a"
			}
			arg := func(name string) string {
				raw, _ := json.Marshal(map[string]any{"action": "files.write", "path": name, "content": "X", "append": true})
				return string(raw)
			}
			calls := []reviewCall{{"computer_files", arg(first)}, {"computer_files", arg(second)}}
			_, err = reviewEngine(t, calls, 0).Run(context.Background(), runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic uncertain replay race"}}, Tools: s.microVMTools(r)})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(profile, "a"))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "lookup-before-dispatch" && (dispatches != 2 || rejected != 1) {
				t.Fatalf("dispatches=%d rejected=%d; want one rejected mutation request", dispatches, rejected)
			}
			if string(got) != "X" || writes != 1 {
				t.Fatalf("unresolved effect replayed: guest writes=%d, original target=%q; want one write and X", writes, got)
			}
		})
	}
}
