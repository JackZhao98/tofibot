package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func outboxFixture(t *testing.T) (*Server, string, Conversation, Run) {
	t.Helper()
	s, root, _ := newGuestInputServer(t)
	old := guestOutboxDir
	guestOutboxDir = filepath.Join(root, "home", "outbox")
	t.Cleanup(func() { guestOutboxDir = old })
	if err := os.MkdirAll(guestOutboxDir, 0o770); err != nil {
		t.Fatal(err)
	}
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "make the report", "outbox-run")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	return s, root, conv, run
}

func markComputerUse(t *testing.T, s *Server, conv Conversation, run Run) {
	t.Helper()
	if _, err := s.store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		conv.ID, run.BotID, run.ID, "call-1", "computer_shell", "completed", now(), now()); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxSyncCopiesNewFilesOnceAndRefusesUnsafeOnes(t *testing.T) {
	s, root, conv, run := outboxFixture(t)
	out := guestOutboxDir
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(out, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o770); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o660); err != nil {
			t.Fatal(err)
		}
	}
	write("report.txt", "first")
	write("sub/report.txt", "second, same name")
	// Things that must not be copied: a link to a file outside the outbox, a
	// link to a folder, and a file over the per-file cap.
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("outside the outbox"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "secret.txt"), filepath.Join(out, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "home"), filepath.Join(out, "homelink")); err != nil {
		t.Fatal(err)
	}
	big, err := os.Create(filepath.Join(out, "huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	big.Truncate(outboxFileBytes + 1)
	big.Close()

	// A run that never used the computer does not touch it.
	if got := s.stageOutbox(context.Background(), run, conv); got != (outboxResult{}) {
		t.Fatalf("run without computer use synced: %+v", got)
	}
	markComputerUse(t, s, conv, run)
	got := s.stageOutbox(context.Background(), run, conv)
	if got.Staged != 2 || got.Refused != 1 {
		t.Fatalf("first sync: %+v", got)
	}
	names := s.store.runAttachmentNames(run.ID)
	if len(names) != 2 || names[0] == names[1] {
		t.Fatalf("names=%v", names)
	}
	var n int
	s.store.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE origin='outbox' AND bot_id=? AND sha256 IS NOT NULL`, run.BotID).Scan(&n)
	if n != 2 {
		t.Fatalf("outbox rows=%d", n)
	}
	// Nothing on the computer was removed or changed.
	for _, rel := range []string{"report.txt", "sub/report.txt", "leak.txt", "huge.bin"} {
		if _, err := os.Lstat(filepath.Join(out, rel)); err != nil {
			t.Fatalf("%s was touched: %v", rel, err)
		}
	}
	// No attachment holds the outside file's bytes.
	var all []string
	rows, _ := s.store.db.Query(`SELECT id FROM attachments`)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		all = append(all, id)
	}
	rows.Close()
	for _, id := range all {
		if b, _ := os.ReadFile(filepath.Join(s.store.mustAttachmentRoot(t), id+".upload")); strings.Contains(string(b), "outside the outbox") {
			t.Fatal("a link target was exported")
		}
	}

	// Second sync: same files, nothing new.
	again := s.stageOutbox(context.Background(), run, conv)
	if again.Staged != 0 || again.Skipped != 2 {
		t.Fatalf("repeat sync: %+v", again)
	}
	// A changed file is copied again; a merely touched one is not.
	write("report.txt", "first, edited")
	if got = s.stageOutbox(context.Background(), run, conv); got.Staged != 1 {
		t.Fatalf("edited file: %+v", got)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(out, "sub", "report.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	if got = s.stageOutbox(context.Background(), run, conv); got.Staged != 0 {
		t.Fatalf("touched file copied again: %+v", got)
	}

	// The run is cancelled and its staged copies are discarded: the ledger must
	// not claim they exist, so the files come back with the next reply.
	s.store.cleanupRunAttachments(run.ID)
	s.store.SetRunStatus(run.ID, "done", "")
	_, run2, _, err := s.store.AddUserRun(conv.ID, run.BotID, "again", "outbox-run-2")
	if err != nil {
		t.Fatal(err)
	}
	s.store.SetRunStatus(run2.ID, "running", "")
	markComputerUse(t, s, conv, Run{ID: run2.ID, BotID: run.BotID})
	if got = s.stageOutbox(context.Background(), run2, conv); got.Staged != 2 {
		t.Fatalf("after discard: %+v", got)
	}
}

func TestOutboxSyncKeepsWithinTheReplyCap(t *testing.T) {
	s, _, conv, run := outboxFixture(t)
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(guestOutboxDir, "f"+string(rune('a'+i))+".txt"), []byte{byte('a' + i)}, 0o660); err != nil {
			t.Fatal(err)
		}
	}
	markComputerUse(t, s, conv, run)
	got := s.stageOutbox(context.Background(), run, conv)
	if got.Staged != outboxRunFileCap || got.Deferred != 2 {
		t.Fatalf("%+v", got)
	}
	// The two that did not fit are still in the outbox and not in the ledger.
	var n int
	s.store.db.QueryRow(`SELECT COUNT(*) FROM outbox_synced`).Scan(&n)
	if n != outboxRunFileCap {
		t.Fatalf("ledger=%d", n)
	}
}

func TestGuestExportRefusesSymlinksWhenAsked(t *testing.T) {
	s, root, _, run := outboxFixture(t)
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside.txt", filepath.Join(guestOutboxDir, "l.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guestOutboxDir, "ok.txt"), []byte("ok"), 0o660); err != nil {
		t.Fatal(err)
	}
	args := func(p string, noLinks bool) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"path": p, "offset": 0, "limit": 1024, "no_symlinks": noLinks})
		return b
	}
	if _, err := s.microVMAction(context.Background(), run, "files.export_chunk", args(filepath.Join(guestOutboxDir, "l.txt"), true)); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("link was exported: %v", err)
	}
	if _, err := s.microVMAction(context.Background(), run, "files.export_chunk", args(filepath.Join(guestOutboxDir, "ok.txt"), true)); err != nil {
		t.Fatalf("regular file refused: %v", err)
	}
	// publish_file keeps its old behaviour (links inside /workspace still work).
	if _, err := s.microVMAction(context.Background(), run, "files.export_chunk", args(filepath.Join(guestOutboxDir, "l.txt"), false)); err != nil {
		t.Fatalf("publish path changed: %v", err)
	}
}
