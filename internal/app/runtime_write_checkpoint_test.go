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

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestRetargetedWriteFenceSurvivesGuestRestartAndCheckpointReload(t *testing.T) {
	for _, mode := range []string{"bound-existing", "uncertain-create", "older-guest", "failed-lookup"} {
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
			defer func() { _ = g.Close(context.Background()) }()
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(root, "bots", b.ID)
			if err := os.MkdirAll(profile, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "b", "c"} {
				if name == "a" && mode == "uncertain-create" {
					continue
				}
				if err := os.WriteFile(filepath.Join(profile, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(profile, "candidate")
			retarget := func(name string) {
				t.Helper()
				_ = os.Remove(link)
				if err := os.Symlink(name, link); err != nil {
					t.Fatal(err)
				}
			}
			retarget("a")
			writes, reads, lookups := 0, 0, 0
			client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-write-checkpoint.sock", Client: &http.Client{Transport: reviewTransport(func(req *http.Request) (*http.Response, error) {
				var a computer.Action
				if err := json.NewDecoder(req.Body).Decode(&a); err != nil {
					return nil, err
				}
				if a.Name == "files.identity" {
					lookups++
					if mode == "failed-lookup" {
						return nil, errors.New("synthetic identity lookup failure")
					}
				}
				raw, _ := json.Marshal(a)
				w := httptest.NewRecorder()
				g.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewReader(raw)).WithContext(req.Context()))
				if a.Name == "files.identity" && mode == "older-guest" {
					var out map[string]any
					_ = json.Unmarshal(w.Body.Bytes(), &out)
					delete(out["result"].(map[string]any), "guard_version")
					raw, _ := json.Marshal(out)
					w = httptest.NewRecorder()
					_, _ = w.Write(raw)
				}
				if a.Name == "files.read" || a.Name == "files.list" {
					reads++
				}
				if a.Name == "files.write" {
					if w.Code != 200 {
						t.Fatalf("unexpected guest write rejection: %d %s", w.Code, w.Body.String())
					}
					writes++
					if writes == 1 {
						retarget("b")
						return nil, errors.New("synthetic response lost after append")
					}
				}
				return w.Result(), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			s.microVM = client
			checkpointFile := filepath.Join(t.TempDir(), "checkpoint.json")
			tools := append(s.microVMTools(r), s.computerTools(r)...)
			tools = append(tools, Tool{Name: "synthetic_ask", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return "", runtime.SuspendForUserInput(ctx, "synthetic-question")
			}})
			req := runtime.Request{BotID: b.ID, RunID: r.ID, Messages: []runtime.Message{{Role: "user", Content: "Synthetic retained uncertainty"}}, Tools: tools, OnSuspend: func(_ string, raw json.RawMessage) error { return os.WriteFile(checkpointFile, raw, 0600) }}
			first := "candidate"
			if mode == "uncertain-create" {
				first = "a"
			}
			args, _ := json.Marshal(map[string]any{"action": "files.write", "path": first, "content": "X", "append": true})
			paused, err := reviewEngine(t, []reviewCall{{"computer_files", string(args)}, {"synthetic_ask", `{}`}}, 0).Run(context.Background(), req)
			if err != nil || !paused.Suspended || writes != 1 || lookups != 1 {
				t.Fatalf("pause=%+v writes=%d lookups=%d err=%v", paused, writes, lookups, err)
			}
			checkpoint, err := os.ReadFile(checkpointFile)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Agent agent.Continuation `json:"agent"`
			}
			if err := json.Unmarshal(checkpoint, &saved); err != nil {
				t.Fatal(err)
			}
			if len(saved.Agent.ToolRecovery) != 1 || saved.Agent.ToolRecovery[0].Identity == nil {
				t.Fatal("dispatch evidence lost")
			}
			original := *saved.Agent.ToolRecovery[0].Identity
			if mode == "bound-existing" && (original.Target != filepath.Join(profile, "a") || original.Object == "" || original.GuardVersion != 1) {
				t.Fatalf("original identity replaced: %+v", original)
			}
			// Repeated outside retargets and a fresh Guest/process cannot shrink the
			// backend-owned fence loaded from disk.
			for _, name := range []string{"c", "b", "a", "c", "a"} {
				retarget(name)
			}
			if err := g.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			g, err = guest.NewWithIdleTimeout(root, 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			req.Continuation, req.ResumeResult = checkpoint, "synthetic answer"
			calls := []reviewCall{{"computer_files", `{"action":"files.write","path":"a","content":"X","append":true}`}, {"computer_files", `{"action":"files.write","path":"./a","content":"X","append":true,"offset":1}`}, {"computer_action", `{"computer_id":"firecracker","action":"files.write","args":{"path":"candidate","content":"X","append":true}}`}, {"computer_files", `{"action":"files.read","path":"a"}`}, {"computer_files", `{"action":"files.list","path":"."}`}, {"computer_files", `{"action":"files.write","path":"b","content":"Y","append":true}`}}
			completed, err := reviewEngine(t, calls, 0).Run(context.Background(), req)
			wantWrites, wantB := 1, ""
			if mode == "bound-existing" {
				wantWrites, wantB = 2, "Y"
			}
			if err != nil || completed.Suspended || writes != wantWrites || reads != 2 {
				t.Fatalf("resume=%+v writes=%d reads=%d err=%v", completed, writes, reads, err)
			}
			for name, want := range map[string]string{"a": "X", "b": wantB, "c": ""} {
				got, err := os.ReadFile(filepath.Join(profile, name))
				if err != nil || string(got) != want {
					t.Fatalf("%s=%q want=%q err=%v", name, got, want, err)
				}
			}
		})
	}
}
