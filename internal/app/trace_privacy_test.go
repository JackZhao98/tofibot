package app

import (
	"strings"
	"testing"
	"time"
)

func tracePictures(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE origin=?`, attachmentOriginTrace).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The screen is the computer's: a private input typed in one run is still on it
// when that run has ended, for the next run of the same Bot and for another Bot.
func TestSecretWindowOutlivesTheRunAndCoversEveryRunOnTheComputer(t *testing.T) {
	s, _, run := captureServer(t)
	other, err := s.store.CreateBot("Second Bot", "Answer briefly.", "model")
	if err != nil {
		t.Fatal(err)
	}
	otherConv, err := s.store.GetConversation(other.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	s.store.openSecretWindow(run.ID)
	endRun(t, s, run)

	followUp := contextSurfaceRun(t, s, mustConversation(t, s, run.ConversationID), Bot{ID: run.BotID}, "continue")
	otherRun := contextSurfaceRun(t, s, otherConv, other, "look at the screen")
	for _, r := range []Run{followUp, otherRun} {
		out := runTool(t, s, r, "computer_desktop", `{"action":"desktop.capture"}`)
		if !strings.Contains(out, "image_url") {
			t.Fatalf("the Bot must still get its screenshot: %.80s", out)
		}
		rows := captures(t, s, r)
		if len(rows) != 1 || !rows[0].Redacted || rows[0].AttachmentID != "" || rows[0].Reason != traceReasonSecretInput {
			t.Fatalf("run %s: %+v", r.ID, rows)
		}
		s.releaseComputerOwner(r.BotID, r.ID)
	}
	if n := tracePictures(t, s); n != 0 {
		t.Fatalf("%d pictures stored while the window was open", n)
	}

	// A confirmed navigation closes the window for everyone.
	s.claimComputerOwner(otherRun.BotID, otherRun.ID)
	runTool(t, s, otherRun, "computer_browser", `{"action":"browser.navigate","url":"https://news.example/"}`)
	s.releaseComputerOwner(otherRun.BotID, otherRun.ID)
	runTool(t, s, followUp, "computer_desktop", `{"action":"desktop.capture"}`)
	rows := captures(t, s, followUp)
	if len(rows) != 2 || rows[1].Redacted || rows[1].AttachmentID == "" {
		t.Fatalf("after navigation: %+v", rows)
	}
}

func TestSecretWindowClosesOnDesktopStopAndAfterFifteenMinutes(t *testing.T) {
	s, _, run := captureServer(t)
	s.store.openSecretWindow(run.ID)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.stop"}`)
	if s.store.secretWindowOpen() {
		t.Fatal("desktop.stop must close the window")
	}

	s.store.openSecretWindow(run.ID)
	stamp := func(age time.Duration) {
		if _, err := s.store.db.Exec(`UPDATE computer_secret_windows SET opened_at=?`, stampTime(time.Now().Add(-age))); err != nil {
			t.Fatal(err)
		}
	}
	stamp(secretWindowMax - time.Minute)
	if !s.store.secretWindowOpen() {
		t.Fatal("the window closed before its bound")
	}
	stamp(secretWindowMax + time.Minute)
	if s.store.secretWindowOpen() {
		t.Fatal("the window outlived its 15-minute bound")
	}
	var n int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM computer_secret_windows`).Scan(&n)
	if n != 0 {
		t.Fatal("the expired window row was kept")
	}
}

func TestFormPasswordOriginStaysPrivateForLaterRuns(t *testing.T) {
	s, _, run, q := formFixture(t)
	if _, _, err := s.AnswerUserForm(q.ID, "human", formValues()); err != nil {
		t.Fatal(err)
	}
	// The next run has no vault record of its own: the origin is still private.
	s.secretVault.records = map[string]secretRecord{}
	if !s.privateTraceOrigin("https://example.test/login") {
		t.Fatal("the origin of a form password must stay private for later runs")
	}
	if s.privateTraceOrigin("https://other.test/") {
		t.Fatal("an unrelated origin was marked private")
	}
	_ = run
}

func mustConversation(t *testing.T, s *Server, id string) Conversation {
	t.Helper()
	c, err := s.store.GetConversation(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
