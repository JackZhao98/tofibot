package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestGuestInputPathsAreSafeDeterministicAndStable(t *testing.T) {
	mk := func(id, name string) guestInputCandidate {
		return guestInputCandidate{Attachment: Attachment{ID: id, Name: name}}
	}
	list := []guestInputCandidate{
		mk("aaaaaaaa-1111", "invoices.zip"),
		mk("bbbbbbbb-2222", "Invoices.ZIP"), // collides when case is ignored
		mk("cccccccc-3333", "../../etc/passwd"),
		mk("dddddddd-4444", ".."),
		mk("eeeeeeee-5555", "scan.part"),
		mk("ffffffff-6666", "报告 2026.pdf"),
		mk("gggggggg-7777", "a\x00b\nc.txt"),
	}
	got := guestInputPaths("conv-1", list)
	folder := guestUploadsRoot + "/conv-1/"
	want := map[string]string{
		"aaaaaaaa-1111": folder + "invoices.zip",
		"bbbbbbbb-2222": folder + "Invoices-bbbbbbbb.ZIP",
		"cccccccc-3333": folder + ".._.._etc_passwd",
		"dddddddd-4444": folder + "attachment",
		"eeeeeeee-5555": folder + "scan.part_",
		"ffffffff-6666": folder + "报告 2026.pdf",
		"gggggggg-7777": folder + "a_b_c.txt",
	}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("%s: got %q want %q", id, got[id], p)
		}
	}
	// Later attachments never move earlier ones; the mapping is repeatable.
	more := append(append([]guestInputCandidate{}, list...), mk("hhhhhhhh-8888", "invoices.zip"))
	again := guestInputPaths("conv-1", more)
	for id, p := range got {
		if again[id] != p {
			t.Errorf("%s moved: %q -> %q", id, p, again[id])
		}
	}
	if again["hhhhhhhh-8888"] != folder+"invoices-hhhhhhhh.zip" {
		t.Errorf("late duplicate=%q", again["hhhhhhhh-8888"])
	}
	seen := map[string]bool{}
	for _, p := range again {
		if seen[strings.ToLower(p)] {
			t.Errorf("duplicate path %q", p)
		}
		seen[strings.ToLower(p)] = true
		if strings.Contains(strings.TrimPrefix(p, folder), "/") {
			t.Errorf("nested path %q", p)
		}
	}
	long := guestInputName(strings.Repeat("长", 100) + ".xlsx")
	if len(long) > 200 || !strings.HasSuffix(long, ".xlsx") {
		t.Errorf("long name=%d %q", len(long), long)
	}
}

func newGuestInputServer(t *testing.T) (*Server, string, *[]string) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	root := t.TempDir()
	g, err := guest.NewWithIdleTimeout(root, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close(context.Background()) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var statuses []string
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-guest-inputs.sock", Client: &http.Client{Transport: reviewTransport(func(req *http.Request) (*http.Response, error) {
		var a computer.Action
		if err := json.NewDecoder(req.Body).Decode(&a); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(a)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/action", bytes.NewReader(raw)).WithContext(req.Context()))
		if a.Name == "files.import_chunk" {
			var out struct {
				Result struct {
					Status string `json:"status"`
				} `json:"result"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			mu.Lock()
			statuses = append(statuses, out.Result.Status)
			mu.Unlock()
		}
		return w.Result(), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = client
	old := guestUploadsRoot
	guestUploadsRoot = filepath.Join(root, "home", "uploads")
	t.Cleanup(func() { guestUploadsRoot = old })
	return s, root, &statuses
}

func sendWithFiles(t *testing.T, s *Server, conv, botID, text, key string, files map[string][]byte, order []string) (Message, Run, []Attachment) {
	t.Helper()
	var ids []string
	var made []Attachment
	for _, name := range order {
		a, err := s.store.AddAttachment(conv, name, "application/octet-stream", bytes.NewReader(files[name]))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
		made = append(made, a)
	}
	m, runs, _, err := s.store.AddUserRunsWithAttachments(conv, text, key, []runSpec{{BotID: botID}}, ids)
	if err != nil || len(runs) != 1 {
		t.Fatalf("send: %v", err)
	}
	return m, runs[0], made
}

func TestGuestInputsReachTheComputerOnFirstToolUseOnly(t *testing.T) {
	s, root, statuses := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	other, _ := s.store.CreateBot("neighbour", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	big := bytes.Repeat([]byte{0, 1, 2, 0xff}, 400<<10) // 1.6 MB, three chunks
	_, _, _ = sendWithFiles(t, s, conv.ID, bot.ID, "earlier", "k1", map[string][]byte{"old.xlsx": []byte("old")}, []string{"old.xlsx"})
	m, run, made := sendWithFiles(t, s, conv.ID, bot.ID, "here are the invoices", "k2",
		map[string][]byte{"invoices.zip": big, "old.xlsx": []byte("newer same name")}, []string{"invoices.zip", "old.xlsx"})
	// Another conversation's upload must not reach this Bot.
	otherConv, _ := s.store.GetConversation(other.DMConversationID)
	sendWithFiles(t, s, otherConv.ID, other.ID, "private", "k3", map[string][]byte{"secret.pdf": []byte("secret")}, []string{"secret.pdf"})
	if run.TriggerMessageID != m.ID {
		t.Fatalf("trigger=%q message=%q", run.TriggerMessageID, m.ID)
	}

	// Context building alone is lazy: it names paths and moves nothing.
	paths := s.guestInputPathsFor(conv, run.TriggerMessageID)
	if len(*statuses) != 0 || len(paths) != 3 {
		t.Fatalf("paths=%v transfers=%v", paths, *statuses)
	}
	pm := runtime.Message{Role: "user", Content: m.Content}
	left, budget := 3, int64(8<<20)
	s.addAttachmentContext(conv, m, &pm, &left, &budget, paths)
	for _, a := range made {
		if !strings.Contains(pm.Content, "attachment_id="+a.ID) || !strings.Contains(pm.Content, "On the Bot computer at "+paths[a.ID]) {
			t.Fatalf("prompt lacks the path of %s: %s", a.Name, pm.Content)
		}
	}
	if !strings.Contains(pm.Content, "old-") {
		t.Fatalf("same-name file kept the plain name: %s", pm.Content)
	}
	// A Bot with no computer gets no path line.
	bare := runtime.Message{Role: "user", Content: m.Content}
	(&Server{store: s.store}).addAttachmentContext(conv, m, &bare, &left, &budget, (&Server{store: s.store}).guestInputPathsFor(conv, ""))
	if strings.Contains(bare.Content, "On the computer") {
		t.Fatal("path line without a computer")
	}

	tools := s.microVMTools(run)
	files, ok := toolNamed(tools, "computer_files")
	if !ok {
		t.Fatal("no computer_files tool")
	}
	if _, err := files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "home", "uploads", conv.ID)
	got, err := os.ReadFile(filepath.Join(folder, "invoices.zip"))
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("invoices.zip: %d bytes, %v", len(got), err)
	}
	if got, err = os.ReadFile(strings.Replace(paths[made[1].ID], guestUploadsRoot, filepath.Join(root, "home", "uploads"), 1)); err != nil || string(got) != "newer same name" {
		t.Fatalf("collided file: %q %v", got, err)
	}
	if got, err = os.ReadFile(filepath.Join(folder, "old.xlsx")); err != nil || string(got) != "old" {
		t.Fatalf("earlier file: %q %v", got, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "home", "uploads")); len(entries) != 1 {
		t.Fatalf("uploads holds %d folders; another conversation leaked", len(entries))
	}
	written := 0
	for _, st := range *statuses {
		if st == "written" {
			written++
		}
	}
	if written != 3 {
		t.Fatalf("statuses=%v", *statuses)
	}

	// Idempotent: another tool call and a whole new run's tool set send nothing,
	// and a file the Bot edited is kept.
	before := len(*statuses)
	if err = os.WriteFile(filepath.Join(folder, "old.xlsx"), []byte("edited by bot"), 0660); err != nil {
		t.Fatal(err)
	}
	if _, err = files.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	again, _ := toolNamed(s.microVMTools(run), "computer_files")
	if _, err = again.Execute(context.Background(), json.RawMessage(`{"action":"files.list","path":"."}`)); err != nil {
		t.Fatal(err)
	}
	for _, st := range (*statuses)[before:] {
		if st != "exists" {
			t.Fatalf("repeat transferred bytes: %v", (*statuses)[before:])
		}
	}
	if got, _ = os.ReadFile(filepath.Join(folder, "old.xlsx")); string(got) != "edited by bot" {
		t.Fatalf("edit overwritten: %q", got)
	}
}

func TestGuestInputsFollowConversationMembership(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	a, _ := s.store.CreateBot("member", "", "model")
	b, _ := s.store.CreateBot("peer", "", "model")
	outsider, _ := s.store.CreateBot("outsider", "", "model")
	group, err := s.store.CreateGroup("synthetic", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	m, run, _ := sendWithFiles(t, s, group.ID, a.ID, "group file", "g1", map[string][]byte{"budget.xlsx": []byte("g")}, []string{"budget.xlsx"})
	// The peer's own run in the group sees the group's files at the same path.
	peerRun := Run{ID: "peer-run", BotID: b.ID, ConversationID: group.ID, TriggerMessageID: m.ID}
	if len(s.guestInputPathsFor(group, peerRun.TriggerMessageID)) != 1 {
		t.Fatal("group member does not get group attachments")
	}
	// A Bot's own DM never lists them; neither does a Bot outside the group.
	outConv, _ := s.store.GetConversation(outsider.DMConversationID)
	if p := s.guestInputPathsFor(outConv, ""); len(p) != 0 {
		t.Fatalf("outsider DM paths=%v", p)
	}
	aDM, _ := s.store.GetConversation(a.DMConversationID)
	if p := s.guestInputPathsFor(aDM, ""); len(p) != 0 {
		t.Fatalf("member DM paths=%v", p)
	}
	// An assistant message's files (a Bot's own publication) are not re-uploaded.
	if _, _, err = s.store.AddMessage(group.ID, "assistant", a.ID, "", "published", "bot-msg"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Run{run, peerRun} {
		if err := s.syncGuestInputs(context.Background(), r, group); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "home", "uploads", group.ID, "budget.xlsx")); err != nil {
		t.Fatal(err)
	}
	// A hidden conversation obeys the same rule: its own messages only.
	hidden := group
	hidden.UserVisible = false
	if len(s.guestInputPathsFor(hidden, "")) != 1 {
		t.Fatal("hidden conversation lost its own attachments")
	}
}

func TestComputerPromptStatesTheInputsPrinciple(t *testing.T) {
	prompt := syntheticComputerPrompt(t, "bot")
	if !strings.Contains(prompt, "Attached files are on this Bot computer at the listed paths") {
		t.Fatalf("prompt=%s", prompt)
	}
}
