package app

import (
	"strings"
	"testing"
	"time"
)

// The page a picture shows is read when the picture is taken. The last page a
// browser result named is not it: the Bot may have clicked on since.
func TestPrivateOriginIsReadAtCaptureTimeNotFromTheLastBrowserResult(t *testing.T) {
	s, c, run := captureServer(t)
	setAuto(t, s, autoTracePrivateOrigins, "bank.example")
	c.snapshotURL = "https://public.example/"
	runTool(t, s, run, "computer_browser", `{"action":"browser.snapshot"}`)
	// A click on a link opens the bank; no browser result has named it yet.
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.click","x":10,"y":10}`)
	c.snapshotURL = "https://bank.example/accounts"
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	rows := captures(t, s, run)
	if len(rows) != 2 || !rows[1].Redacted || rows[1].AttachmentID != "" || rows[1].Reason != traceReasonPrivateOrigin {
		t.Fatalf("%+v", rows)
	}
	if n := tracePictures(t, s); n != 1 {
		t.Fatalf("%d pictures stored", n)
	}
}

func TestAPageThatCannotBeResolvedCountsAsPrivateWhileARuleExists(t *testing.T) {
	s, c, run := captureServer(t)
	c.failSnapshot = true
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	if rows := captures(t, s, run); len(rows) != 1 || rows[0].Redacted {
		t.Fatalf("with no rule the picture is kept: %+v", rows)
	}
	setAuto(t, s, autoTracePrivateOrigins, "bank.example")
	out := runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	if !strings.Contains(out, "image_url") {
		t.Fatal("the Bot must still get its screenshot")
	}
	rows := captures(t, s, run)
	if len(rows) != 2 || !rows[1].Redacted || rows[1].AttachmentID != "" || rows[1].Reason != traceReasonPrivateOrigin {
		t.Fatalf("%+v", rows)
	}
}

// The background capture acts as no model and leaves needs_observation as it was.
func TestBackgroundCaptureActsAsNoModelAndLeavesObservationAlone(t *testing.T) {
	s, c, run := engineTraceServer(t)
	if err := engineCall(t, s, run, "browser_press_key", `{"key":"Tab"}`); err != nil {
		t.Fatal(err)
	}
	s.traceWG.Wait()
	var snaps int
	c.rmu.Lock()
	for _, a := range c.actions {
		if a.Name == "browser.snapshot" {
			snaps++
			if a.Source != "viewer" {
				t.Errorf("the background capture has source %q", a.Source)
			}
		}
	}
	c.rmu.Unlock()
	if snaps != 1 {
		t.Fatalf("%d snapshots", snaps)
	}
	s.computerOwnerMu.Lock()
	observed := s.desktopObserved[run.ID]
	s.computerOwnerMu.Unlock()
	if observed {
		t.Fatal("a picture the model never saw marked the desktop as observed")
	}
	if len(captures(t, s, run)) != 1 {
		t.Fatal("no picture kept")
	}
}

// The tool call returns before the screenshot does, and at most
// traceInflightMax captures run at once; the rest are dropped.
func TestBackgroundCaptureDoesNotSlowTheToolAndIsBounded(t *testing.T) {
	s, c, run := engineTraceServer(t)
	c.gate = make(chan struct{})
	start := time.Now()
	for i := 0; i < 4; i++ {
		resetGap(s, run)
		if err := engineCall(t, s, run, "browser_press_key", `{"key":"Tab"}`); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("tool calls waited for the screenshot")
	}
	if rows, _ := s.store.runCaptures(run.ID); len(rows) != 0 {
		t.Fatalf("a picture was stored before the screenshot came back: %+v", rows)
	}
	close(c.gate)
	s.traceWG.Wait()
	if n := c.count("browser.snapshot"); n != traceInflightMax {
		t.Fatalf("%d screenshots asked, at most %d may run at once and the rest are dropped", n, traceInflightMax)
	}
	if rows := captures(t, s, run); len(rows) != traceInflightMax {
		t.Fatalf("%+v", rows)
	}
}

func TestBackgroundPicturesLeaveRoomForTheModelsOwn(t *testing.T) {
	s, _, run := engineTraceServer(t)
	for i := 0; i < traceBackgroundPerRunMax; i++ {
		if _, err := s.store.addRunCapture(RunCapture{RunID: run.ID, TakenAt: now(), Label: "x", Detail: "background", Redacted: true, Reason: traceReasonSecretInput}); err != nil {
			t.Fatal(err)
		}
	}
	if err := engineCall(t, s, run, "browser_press_key", `{"key":"Tab"}`); err != nil {
		t.Fatal(err)
	}
	rows := captures(t, s, run)
	if len(rows) != traceBackgroundPerRunMax+1 || rows[len(rows)-1].Reason != traceReasonLimit {
		t.Fatalf("the background share was not capped: %d %+v", len(rows), rows[len(rows)-1])
	}
}
