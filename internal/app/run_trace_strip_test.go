package app

import (
	"context"
	"encoding/json"
	"image"
	"os"
	"strings"
	"testing"
	"time"
)

// endRun marks the run failed (any end state builds the strip) and runs the
// hook the worker runs when a run ends.
func endRun(t *testing.T, s *Server, run Run) Run {
	t.Helper()
	// A run that already ended stays as it is (the hook runs again on a retry).
	if _, err := s.store.SetRunStatus(run.ID, "failed", "synthetic end"); err != nil {
		t.Fatal(err)
	}
	conv, err := s.store.GetConversation(run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.store.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.finalizeRunTrace(conv, current)
	return current
}

func captureN(t *testing.T, s *Server, run Run, n int) {
	t.Helper()
	tool, _ := toolNamed(s.microVMTools(run), "computer_desktop")
	for i := 0; i < n; i++ {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.capture","detail":"overview"}`)); err != nil {
			t.Fatal(err)
		}
	}
}

func traceOf(s *Server, run Run) *RunTrace {
	list := []Run{run}
	s.store.decorateRunTraces(list)
	return list[0].Trace
}

func TestEndOfARunBuildsOneStripFromItsCaptures(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 5)
	ended := endRun(t, s, run)
	trace := traceOf(s, ended)
	if trace == nil || trace.Tiles != 5 || trace.Steps != 5 || trace.AttachmentID == "" {
		t.Fatalf("%+v", trace)
	}
	a, path, err := s.store.Attachment(trace.AttachmentID)
	if err != nil || a.MIME != "image/jpeg" || !strings.HasPrefix(a.Name, "work-trace-") || a.ConversationID != run.ConversationID {
		t.Fatalf("%+v %v", a, err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width != 3*montageTileW+4*montageGap || cfg.Height != 2*(montageTileH+montageLabelH)+3*montageGap {
		t.Fatalf("strip %v %v", cfg, err)
	}
	// The strip is the run's own: no message, no other run carries it.
	var bound int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM attachment_messages WHERE attachment_id=?`, trace.AttachmentID).Scan(&bound); err != nil || bound != 0 {
		t.Fatalf("strip bound to a message: %d %v", bound, err)
	}
	// Ending again replaces the strip, not duplicates it.
	endRun(t, s, run)
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE name LIKE 'work-trace-%'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d strips", n)
	}
}

func TestStripNeedsTwoCapturesAndTheTraceOn(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 1)
	if trace := traceOf(s, endRun(t, s, run)); trace != nil {
		t.Fatalf("one capture made a strip: %+v", trace)
	}
	s2, _, run2 := captureServer(t)
	setAuto(t, s2, autoWorkTrace, "false")
	captureN(t, s2, run2, 3)
	if trace := traceOf(s2, endRun(t, s2, run2)); trace != nil {
		t.Fatalf("trace off made a strip: %+v", trace)
	}
}

func TestStripOfOnlyRedactedStepsIsNotMade(t *testing.T) {
	s, _, run := captureServer(t)
	s.store.openSecretWindow(run.ID)
	captureN(t, s, run, 3)
	if trace := traceOf(s, endRun(t, s, run)); trace != nil {
		t.Fatalf("a strip of grey cells was made: %+v", trace)
	}
	if !s.store.secretWindowOpen() {
		t.Fatal("the secret window closed at the end of the run")
	}
}

func TestLongRunsFoldEarlierStepsIntoTheFirstCell(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 15)
	trace := traceOf(s, endRun(t, s, run))
	if trace == nil || trace.Tiles != montagePersonTilesMax-1 || trace.Steps != 15 {
		t.Fatalf("%+v", trace)
	}
	_, path, _ := s.store.Attachment(trace.AttachmentID)
	f, _ := os.Open(path)
	defer f.Close()
	cfg, _, _ := image.DecodeConfig(f)
	if cfg.Height != 4*(montageTileH+montageLabelH)+5*montageGap {
		t.Fatalf("height %d", cfg.Height)
	}
}

func TestEndOfRunClearsWhatTheServerKeptInMemory(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 2)
	if _, ok := s.traceRuns.Load(run.ID); !ok {
		t.Fatal("no state to clear")
	}
	endRun(t, s, run)
	if _, ok := s.traceRuns.Load(run.ID); ok {
		t.Fatal("state kept after the run ended")
	}
}

func TestRunsReadForTheWebCarryTheirStrip(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 3)
	endRun(t, s, run)
	runs, err := s.store.Runs(run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range runs {
		if r.ID == run.ID {
			found = true
			if r.Trace == nil || r.Trace.Steps != 3 {
				t.Fatalf("%+v", r.Trace)
			}
		}
	}
	if !found {
		t.Fatal("run missing")
	}
}

func TestRetentionDeletesOldPicturesRowsAndStripsButNotNewOnes(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 3)
	endRun(t, s, run)
	rows := captures(t, s, run)
	old := time.Now().Add(-10 * 24 * time.Hour)
	if _, err := s.store.db.Exec(`UPDATE run_captures SET taken_at=? WHERE seq<=2 AND run_id=?`, stampTime(old), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`UPDATE run_traces SET created_at=? WHERE run_id=?`, stampTime(old), run.ID); err != nil {
		t.Fatal(err)
	}
	stripID := traceOf(s, run).AttachmentID
	setAuto(t, s, autoTraceRetentionDays, "7")
	s.runTraceRetention(time.Now())
	left := captures(t, s, run)
	if len(left) != 1 || left[0].Seq != 3 || left[0].AttachmentID != rows[2].AttachmentID {
		t.Fatalf("%+v", left)
	}
	for _, gone := range []string{rows[0].AttachmentID, rows[1].AttachmentID, stripID} {
		if _, _, err := s.store.Attachment(gone); err == nil {
			t.Fatalf("picture %s kept past its period", gone)
		}
	}
	if traceOf(s, run) != nil {
		t.Fatal("old strip kept")
	}
	if _, _, err := s.store.Attachment(rows[2].AttachmentID); err != nil {
		t.Fatalf("a recent picture was deleted: %v", err)
	}
}

func TestRetentionOfZeroDaysKeepsEverything(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 2)
	if _, err := s.store.db.Exec(`UPDATE run_captures SET taken_at=?`, stampTime(time.Now().Add(-400*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	setAuto(t, s, autoTraceRetentionDays, "0")
	s.runTraceRetention(time.Now())
	if len(captures(t, s, run)) != 2 {
		t.Fatal("0 days must keep everything")
	}
}

func TestExportsLeaveTheTraceOut(t *testing.T) {
	s, b := portableFixture(t)
	conv := b.Conversations[0].ID
	keep, err := s.addAttachmentOpts(context.Background(), conv, "report.txt", "text/plain", strings.NewReader("a report"), attachmentOpts{})
	if err != nil {
		t.Fatal(err)
	}
	trace, err := s.addAttachmentOpts(context.Background(), conv, "capture-1.jpg", "image/jpeg", strings.NewReader("\xff\xd8\xff\xd9"), attachmentOpts{Origin: attachmentOriginTrace})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.exportPortable(context.Background(), "synthetic-source-instance", portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, a := range out.Attachments {
		ids[a.ID] = true
	}
	if !ids[keep.ID] || ids[trace.ID] {
		t.Fatalf("exported attachments %v (keep %s, trace %s)", ids, keep.ID, trace.ID)
	}
	if out.AttachmentCount != 0 {
		t.Fatalf("the trace counts as an excluded attachment: %d", out.AttachmentCount)
	}
}

func TestDeletingATraceAlsoDeletesItsStrip(t *testing.T) {
	s, _, run := captureServer(t)
	captureN(t, s, run, 2)
	ended := endRun(t, s, run)
	stripID := traceOf(s, ended).AttachmentID
	if _, err := s.store.deleteRunTrace(run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.Attachment(stripID); err == nil {
		t.Fatal("strip left behind")
	}
	if traceOf(s, ended) != nil || len(captures(t, s, run)) != 0 {
		t.Fatal("rows left behind")
	}
}
