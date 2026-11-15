package app

// Opt-in LIVE eval (real Anthropic API): does a Bot find a route with the means
// it has before it says it cannot? Tasks are in eval/resourceful_tasks.json;
// each runs in a fresh Bot's DM through the normal message route, so the
// prompt, tool catalog, say protocol, reviews and approvals are the product's.
//
// The Bot computer is the real guest service (internal/computer/guest) served
// in process on this host, behind the real computer.Client. It is an eval
// stand-in, not the VM: the host is macOS, the image's tools are linked in
// from TOFI_LIVE_TOOLCHAIN, there is no desktop (the browser and desktop tools
// fail as on a computer without a display), "/workspace" is mapped to
// TOFI_LIVE_GUEST_ROOT both ways, and every shell and terminal command runs
// under /usr/bin/sandbox-exec with the profile TOFI_LIVE_SANDBOX_PROFILE (the
// eval refuses to start a computer without one). The profile is the caller's
// job: it must deny writes outside the guest root and reads of secrets.
//
// Run (from internal/app):
//
//	TOFI_LIVE_RESOURCEFUL=1 TOFI_LIVE_KEY_FILE=~/.tofi-test-key TOFI_LIVE_WORKSPACE_ID=wrkspc_... \
//	TOFI_LIVE_GUEST_ROOT=<empty dir> TOFI_LIVE_TOOLCHAIN=<dir with bin/> TOFI_LIVE_SANDBOX_PROFILE=<file.sb> \
//	TOFI_LIVE_OUT=<results dir> [TOFI_LIVE_MODEL=claude-sonnet-5-5] [TOFI_LIVE_TASKS=video,qrcode] \
//	go test -run TestLiveResourcefulEval -count=1 -timeout 3h .
//
// The key is read from the file and sent only through the provider-key route;
// it is never logged or written.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/google/uuid"
)

type resourcefulTask struct {
	ID         string `json:"id"`
	Message    string `json:"message"`
	NoComputer bool   `json:"no_computer"`
	// Onboarding gives the message to a new Bot whose setup is still pending,
	// as the first message a person sends to their first Bot.
	Onboarding bool `json:"onboarding"`
	// Instructions replaces the file's Bot instructions for this task.
	Instructions string `json:"instructions"`
}

type resourcefulTaskFile struct {
	BotInstructions string            `json:"bot_instructions"`
	Tasks           []resourcefulTask `json:"tasks"`
}

// evalGuest serves the computer.Client's requests from an in-process guest.
type evalGuest struct {
	h       http.Handler
	roots   []string // the guest root as the guest reports it and as given
	profile string
}

var evalBareShell = regexp.MustCompile(`^(exec\s+)?(/bin/)?(bash|sh)(\s+(-i|-l|--login|--norc|--noprofile))*$`)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// sandboxed runs a command, or an interactive shell, under sandbox-exec.
func (g *evalGuest) sandboxed(command string, interactive bool) string {
	prefix := "exec /usr/bin/sandbox-exec -f " + shellQuote(g.profile) + " /bin/bash --noprofile --norc"
	if interactive {
		return prefix + " -i"
	}
	return prefix + ` -c "$(printf %s ` + base64.StdEncoding.EncodeToString([]byte(command)) + ` | /usr/bin/base64 -D)"`
}

func (g *evalGuest) toGuest(body []byte) []byte {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	var action string
	_ = json.Unmarshal(req["action"], &action)
	args := strings.ReplaceAll(string(req["args"]), "/workspace", g.roots[0])
	var fields map[string]any
	if (action == "shell.exec" || action == "terminal.open") && json.Unmarshal([]byte(args), &fields) == nil {
		command, _ := fields["command"].(string)
		trimmed := strings.TrimSpace(command)
		interactive := action == "terminal.open" && (trimmed == "" || strings.HasPrefix(trimmed, "# tofi-default-terminal") || evalBareShell.MatchString(trimmed))
		fields["command"] = g.sandboxed(command, interactive)
		b, _ := json.Marshal(fields)
		args = string(b)
	}
	req["args"] = json.RawMessage(args)
	out, _ := json.Marshal(req)
	return out
}

func (g *evalGuest) toModel(body []byte) []byte {
	s := string(body)
	for _, root := range g.roots {
		s = strings.ReplaceAll(s, root, "/workspace")
	}
	return []byte(s)
}

func (g *evalGuest) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}
	if req.URL.Path == "/v1/action" && len(body) > 0 {
		body = g.toGuest(body)
	}
	inner := httptest.NewRequest(req.Method, req.URL.String(), bytes.NewReader(body)).WithContext(req.Context())
	inner.Header = req.Header.Clone()
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, inner)
	res := rec.Result()
	out, _ := io.ReadAll(res.Body)
	out = g.toModel(out)
	res.Body = io.NopCloser(bytes.NewReader(out))
	res.ContentLength = int64(len(out))
	res.Header.Del("Content-Length")
	return res, nil
}

func TestLiveResourcefulEval(t *testing.T) {
	if os.Getenv("TOFI_LIVE_RESOURCEFUL") != "1" {
		t.Skip("opt-in live eval: set TOFI_LIVE_RESOURCEFUL=1 (see the file comment)")
	}
	keyFile, workspace, outDir := os.Getenv("TOFI_LIVE_KEY_FILE"), os.Getenv("TOFI_LIVE_WORKSPACE_ID"), os.Getenv("TOFI_LIVE_OUT")
	if keyFile == "" || outDir == "" {
		t.Fatal("TOFI_LIVE_KEY_FILE and TOFI_LIVE_OUT are required")
	}
	model := os.Getenv("TOFI_LIVE_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	raw, err := os.ReadFile("../../eval/resourceful_tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var file resourcefulTaskFile
	if err = json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("TOFI_LIVE_TASKS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	if err = os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keyBytes, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal("read key file")
	}
	key := strings.TrimSpace(string(keyBytes))
	keyBytes = nil

	// One server with the computer and one without; tasks pick theirs.
	servers := map[bool]*Server{}
	serverFor := func(withComputer bool) *Server {
		if s := servers[withComputer]; s != nil {
			return s
		}
		dir := t.TempDir()
		s, err := NewServer(Config{DataDir: dir, Environment: "acceptance", Provider: "anthropic", DefaultModel: model, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if err = s.store.putAutoReviewMode("auto"); err != nil {
			t.Fatal(err)
		}
		if code, out := evalDo(s, "PUT", "/api/providers/anthropic/key", map[string]string{"key": key, "workspace_id": workspace}); code != 200 {
			t.Fatalf("install key: %d %s", code, liveRedact(out, key))
		}
		if code, out := evalDo(s, "PUT", "/api/model-settings", map[string]string{"model": model}); code != 200 {
			t.Fatalf("model settings: %d %s", code, out)
		}
		if withComputer {
			root, toolchain, profile := os.Getenv("TOFI_LIVE_GUEST_ROOT"), os.Getenv("TOFI_LIVE_TOOLCHAIN"), os.Getenv("TOFI_LIVE_SANDBOX_PROFILE")
			if root == "" || toolchain == "" || profile == "" {
				t.Fatal("a computer task needs TOFI_LIVE_GUEST_ROOT, TOFI_LIVE_TOOLCHAIN and TOFI_LIVE_SANDBOX_PROFILE")
			}
			if _, err := os.Stat(profile); err != nil {
				t.Fatal("sandbox profile missing")
			}
			svc, err := guest.NewWithIdleTimeout(root, 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Close(context.Background()) })
			canonical, _ := filepath.EvalSymlinks(root)
			bin := filepath.Join(canonical, "shared", "bin")
			entries, _ := os.ReadDir(filepath.Join(toolchain, "bin"))
			for _, e := range entries {
				_ = os.Symlink(filepath.Join(toolchain, "bin", e.Name()), filepath.Join(bin, e.Name()))
			}
			roots := []string{canonical}
			if root != canonical {
				roots = append(roots, root)
			}
			if strings.HasPrefix(canonical, "/private/") {
				roots = append(roots, strings.TrimPrefix(canonical, "/private"))
			}
			client, err := computer.New(computer.Config{Socket: "/tmp/tofi-live-eval.sock", Client: &http.Client{Transport: &evalGuest{h: svc.Handler(), roots: roots, profile: profile}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.attachProvider(client); err != nil {
				t.Fatal(err)
			}
		}
		servers[withComputer] = s
		return s
	}

	var summary strings.Builder
	fmt.Fprintf(&summary, "# Resourceful eval\n\nmodel=%s at %s\n\n", model, time.Now().UTC().Format(time.RFC3339))
	for _, task := range file.Tasks {
		if len(want) > 0 && !want[task.ID] {
			continue
		}
		s := serverFor(!task.NoComputer)
		var bot Bot
		if task.Onboarding {
			bot, _, err = s.store.createOnboardingBot(uuid.NewString(), model, "", "zh-CN", !task.NoComputer, false)
		} else {
			instructions := file.BotInstructions
			if task.Instructions != "" {
				instructions = task.Instructions
			}
			bot, err = s.store.CreateBotWithReasoning("助理-"+task.ID, instructions, "", "")
		}
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		code, out := evalDo(s, "POST", "/api/conversations/"+bot.DMConversationID+"/messages", map[string]string{"content": task.Message, "client_message_id": uuid.NewString()})
		if code != 200 && code != 201 && code != 202 {
			t.Fatalf("%s: post: %d %s", task.ID, code, out)
		}
		timedOut := !evalWaitIdle(s, bot.DMConversationID, 15*time.Minute)
		record := evalCollect(t, s, bot.DMConversationID, filepath.Join(outDir, task.ID))
		record["task"] = task
		record["elapsed_s"] = int(time.Since(started).Seconds())
		record["timed_out"] = timedOut
		b, _ := json.MarshalIndent(record, "", "  ")
		_ = os.WriteFile(filepath.Join(outDir, task.ID+".json"), b, 0o644)
		md := evalTranscript(task, record)
		_ = os.WriteFile(filepath.Join(outDir, task.ID+".md"), []byte(md), 0o644)
		var firstRun string
		_ = s.store.db.QueryRow(`SELECT id FROM runs WHERE conversation_id=? ORDER BY created_at LIMIT 1`, bot.DMConversationID).Scan(&firstRun)
		if code, snap := evalDo(s, "GET", "/api/runs/"+firstRun+"/context", nil); code == 200 {
			_ = os.WriteFile(filepath.Join(outDir, task.ID+".context.json"), []byte(snap), 0o644)
		}
		fmt.Fprintf(&summary, "## %s (%ds%s)\n\n%s\n", task.ID, record["elapsed_s"], map[bool]string{true: ", TIMED OUT"}[timedOut], record["final"])
		t.Logf("%s done in %ds", task.ID, record["elapsed_s"])
	}
	_ = os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(summary.String()), 0o644)
}

func evalDo(s *Server, method, path string, body any) (int, string) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "http://127.0.0.1"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// evalWaitIdle waits until the conversation has no active run and no open wait
// for several polls in a row.
func evalWaitIdle(s *Server, conversationID string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	quiet := 0
	time.Sleep(2 * time.Second)
	for time.Now().Before(deadline) {
		var active int
		_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=? AND status IN ('queued','running','waiting')`, conversationID).Scan(&active)
		if active == 0 {
			quiet++
		} else {
			quiet = 0
		}
		if quiet >= 4 {
			return true
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return false
}

func evalClip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func evalCollect(t *testing.T, s *Server, conversationID, fileDir string) map[string]any {
	db := s.store.db
	type runRow struct{ ID, Kind, Status, Error string }
	var runs []runRow
	rows, err := db.Query(`SELECT id,kind,status,COALESCE(error,'') FROM runs WHERE conversation_id=? ORDER BY created_at`, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r runRow
		_ = rows.Scan(&r.ID, &r.Kind, &r.Status, &r.Error)
		runs = append(runs, r)
	}
	rows.Close()
	type toolRow struct{ Run, Name, Status, Args, Result, Outcome string }
	var tools []toolRow
	rows, err = db.Query(`SELECT t.run_id,t.name,t.status,t.arguments,t.result,COALESCE(t.outcome_json,'') FROM tool_activities t JOIN runs r ON r.id=t.run_id WHERE r.conversation_id=? ORDER BY t.started_at, t.rowid`, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r toolRow
		_ = rows.Scan(&r.Run, &r.Name, &r.Status, &r.Args, &r.Result, &r.Outcome)
		r.Args, r.Result = evalClip(r.Args, 1500), evalClip(r.Result, 1500)
		tools = append(tools, r)
	}
	rows.Close()
	type msgRow struct{ ID, Kind, Content string }
	var msgs []msgRow
	rows, err = db.Query(`SELECT id,kind,content FROM messages WHERE conversation_id=? AND role='assistant' ORDER BY seq`, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var m msgRow
		_ = rows.Scan(&m.ID, &m.Kind, &m.Content)
		msgs = append(msgs, m)
	}
	rows.Close()
	type fileRow struct {
		ID, Name, MIME string
		Size           int64
		Saved          string
	}
	var files []fileRow
	rows, err = db.Query(`SELECT DISTINCT a.id,a.name,a.mime,a.size FROM attachments a JOIN attachment_messages am ON am.attachment_id=a.id JOIN messages m ON m.id=am.message_id WHERE m.conversation_id=? AND m.role='assistant'`, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var f fileRow
		_ = rows.Scan(&f.ID, &f.Name, &f.MIME, &f.Size)
		files = append(files, f)
	}
	rows.Close()
	for i, f := range files {
		_, path, err := s.store.Attachment(f.ID)
		if err != nil {
			continue
		}
		rc, err := s.store.openAttachment(context.Background(), path)
		if err != nil {
			continue
		}
		_ = os.MkdirAll(fileDir, 0o755)
		dest := filepath.Join(fileDir, filepath.Base(f.Name))
		if w, err := os.Create(dest); err == nil {
			_, _ = io.Copy(w, rc)
			w.Close()
			files[i].Saved = dest
		}
		rc.Close()
	}
	var in, outTokens int64
	_ = db.QueryRow(`SELECT COALESCE(SUM(u.input_tokens),0),COALESCE(SUM(u.output_tokens),0) FROM usage_calls u JOIN runs r ON r.id=u.run_id WHERE r.conversation_id=?`, conversationID).Scan(&in, &outTokens)
	final := ""
	for _, m := range msgs {
		if m.Kind == "" && strings.TrimSpace(m.Content) != "" {
			final = m.Content
		}
	}
	return map[string]any{"runs": runs, "tools": tools, "messages": msgs, "files": files, "final": final, "input_tokens": in, "output_tokens": outTokens}
}

func evalTranscript(task resourcefulTask, record map[string]any) string {
	b, _ := json.Marshal(record)
	var r struct {
		Runs     []struct{ ID, Kind, Status, Error string }
		Tools    []struct{ Run, Name, Status, Args, Result, Outcome string }
		Messages []struct{ ID, Kind, Content string }
		Files    []struct {
			Name, MIME, Saved string
			Size              int64
		}
		ElapsedS     int   `json:"elapsed_s"`
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	_ = json.Unmarshal(b, &r)
	var w strings.Builder
	fmt.Fprintf(&w, "# %s\n\n> %s\n\nelapsed %ds, tokens in %d out %d\n\n## Runs\n", task.ID, task.Message, r.ElapsedS, r.InputTokens, r.OutputTokens)
	for _, run := range r.Runs {
		fmt.Fprintf(&w, "- %s kind=%q status=%s %s\n", run.ID[:8], run.Kind, run.Status, run.Error)
	}
	w.WriteString("\n## Tool calls\n")
	for i, tool := range r.Tools {
		fmt.Fprintf(&w, "\n### %d. %s [%s] (run %s)\n\nargs:\n```\n%s\n```\nresult:\n```\n%s\n```\n", i+1, tool.Name, tool.Status, tool.Run[:8], tool.Args, tool.Result)
		if tool.Outcome != "" {
			fmt.Fprintf(&w, "outcome: `%s`\n", tool.Outcome)
		}
	}
	w.WriteString("\n## Messages\n")
	for _, m := range r.Messages {
		fmt.Fprintf(&w, "\n**[%s]** %s\n", m.Kind, m.Content)
	}
	w.WriteString("\n## Files\n")
	for _, f := range r.Files {
		fmt.Fprintf(&w, "- %s (%s, %d bytes) -> %s\n", f.Name, f.MIME, f.Size, f.Saved)
	}
	return w.String()
}
