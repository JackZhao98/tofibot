package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// shotRecorder is the Playwright test computer that also answers the guest's
// browser.snapshot with a screenshot, or fails it.
type shotRecorder struct {
	*pwRecorder
	failShot bool
}

func (c *shotRecorder) Action(ctx context.Context, a computer.Action) (computer.ActionResult, error) {
	return c.ActionWithin(ctx, a, 0)
}

func (c *shotRecorder) ActionWithin(ctx context.Context, a computer.Action, d time.Duration) (computer.ActionResult, error) {
	if a.Name == "browser.snapshot" {
		c.rmu.Lock()
		c.actions = append(c.actions, a)
		c.rmu.Unlock()
		if c.failShot {
			return computer.ActionResult{}, errors.New("synthetic screenshot failure")
		}
		return computer.ActionResult{OK: true, Result: captureJSON(1280, 800, "", 90)}, nil
	}
	return c.pwRecorder.ActionWithin(ctx, a, d)
}

func engineTraceServer(t *testing.T) (*Server, *shotRecorder, Run) {
	t.Helper()
	s, rec, run := pwServer(t, "playwright")
	c := &shotRecorder{pwRecorder: rec}
	s.microVM = c
	return s, c, run
}

func engineCall(t *testing.T, s *Server, run Run, tool, args string) error {
	t.Helper()
	tl, ok := toolNamed(s.microVMTools(run), tool)
	if !ok {
		t.Fatalf("no tool %s", tool)
	}
	_, err := tl.Execute(context.Background(), json.RawMessage(args))
	return err
}

func resetGap(s *Server, run Run) { s.traceState(run.ID).lastStored = time.Time{} }

func TestPlaywrightCallsLeaveAPictureInTheTraceThatTheModelNeverSees(t *testing.T) {
	s, c, run := engineTraceServer(t)
	if err := engineCall(t, s, run, "browser_navigate", `{"url":"https://shop.example/cart"}`); err != nil {
		t.Fatal(err)
	}
	rows := captures(t, s, run)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	r := rows[0]
	if r.Redacted || r.AttachmentID == "" || r.Label != "navigate shop.example" || r.Action != "browser.snapshot" || r.Detail != "background" {
		t.Fatalf("%+v", r)
	}
	if c.count("browser.snapshot") != 1 {
		t.Fatal("one screenshot expected")
	}
	// The tool result is the engine's text: no image, no capture number.
	tl, _ := toolNamed(s.microVMTools(run), "browser_snapshot")
	out, err := tl.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || strings.Contains(out, "image_url") || strings.Contains(out, "capture_seq") {
		t.Fatalf("%v %.80s", err, out)
	}
	if len(captures(t, s, run)) != 1 {
		t.Fatal("a pure read added a capture")
	}
}

func TestPlaywrightCapturesFollowTheSamePrivacyRules(t *testing.T) {
	s, _, run := engineTraceServer(t)
	setAuto(t, s, autoTracePrivateOrigins, "shop.example")
	if err := engineCall(t, s, run, "browser_navigate", `{"url":"https://shop.example/cart"}`); err != nil {
		t.Fatal(err)
	}
	rows := captures(t, s, run)
	if len(rows) != 1 || !rows[0].Redacted || rows[0].AttachmentID != "" || rows[0].Reason != traceReasonPrivateOrigin {
		t.Fatalf("private origin: %+v", rows)
	}
	setAuto(t, s, autoTracePrivateOrigins, "")
	resetGap(s, run)
	s.store.openSecretWindow(run.ID)
	if err := engineCall(t, s, run, "browser_press_key", `{"key":"Tab"}`); err != nil {
		t.Fatal(err)
	}
	rows = captures(t, s, run)
	if len(rows) != 2 || !rows[1].Redacted || rows[1].Reason != traceReasonSecretInput {
		t.Fatalf("secret window: %+v", rows)
	}
	// A navigation ends the window, and its own picture is kept.
	resetGap(s, run)
	if err := engineCall(t, s, run, "browser_navigate", `{"url":"https://news.example/"}`); err != nil {
		t.Fatal(err)
	}
	rows = captures(t, s, run)
	if len(rows) != 3 || rows[2].Redacted || rows[2].AttachmentID == "" {
		t.Fatalf("after navigation: %+v", rows)
	}
}

func TestPlaywrightCaptureIsSkippedWhileThePersonHoldsTheScreen(t *testing.T) {
	s, _, run := engineTraceServer(t)
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Enter the code", Type: questionTakeover, SourceURL: "https://login.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateQuestion(run.ConversationID, run, in); err != nil {
		t.Fatal(err)
	}
	if err := engineCall(t, s, run, "browser_navigate", `{"url":"https://shop.example/cart"}`); err != nil {
		t.Fatal(err)
	}
	if rows := captures(t, s, run); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

func TestAFailedScreenshotNeverFailsTheToolCall(t *testing.T) {
	s, c, run := engineTraceServer(t)
	c.failShot = true
	if err := engineCall(t, s, run, "browser_navigate", `{"url":"https://shop.example/cart"}`); err != nil {
		t.Fatalf("the tool call failed: %v", err)
	}
	if rows := captures(t, s, run); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

func TestPlaywrightCapturesAreLimitedToOneEveryTwoSeconds(t *testing.T) {
	s, c, run := engineTraceServer(t)
	for _, key := range []string{"A", "B", "C"} {
		if err := engineCall(t, s, run, "browser_press_key", `{"key":"`+key+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(captures(t, s, run)); n != 1 || c.count("browser.snapshot") != 1 {
		t.Fatalf("%d rows, %d screenshots", n, c.count("browser.snapshot"))
	}
	s.traceState(run.ID).lastStored = time.Now().Add(-3 * time.Second)
	if err := engineCall(t, s, run, "browser_press_key", `{"key":"D"}`); err != nil {
		t.Fatal(err)
	}
	if n := len(captures(t, s, run)); n != 2 {
		t.Fatalf("%d rows after the gap", n)
	}
}

func TestPlaywrightReadsAndTabListsTakeNoPicture(t *testing.T) {
	s, _, run := engineTraceServer(t)
	for tool, args := range map[string]string{"browser_snapshot": `{}`, "browser_tabs": `{"action":"list"}`, "browser_wait_for": `{"seconds":1}`} {
		if err := engineCall(t, s, run, tool, args); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	if rows := captures(t, s, run); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
	if err := engineCall(t, s, run, "browser_tabs", `{"action":"select","index":0}`); err != nil {
		t.Fatal(err)
	}
	if rows := captures(t, s, run); len(rows) != 1 {
		t.Fatalf("select tab: %+v", rows)
	}
}

func TestDesktopTraceJoinsThePlaywrightCaptures(t *testing.T) {
	s, _, run := engineTraceServer(t)
	for _, u := range []string{"https://a.example/", "https://b.example/"} {
		resetGap(s, run)
		if err := engineCall(t, s, run, "browser_navigate", `{"url":"`+u+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	res, err := runTrace(t, s, run, `{"action":"desktop.trace"}`)
	if err != nil || len(res.Tiles) != 2 || res.Tiles[0].Label != "navigate a.example" || !res.Tiles[1].Kept {
		t.Fatalf("%v %+v", err, res.Tiles)
	}
}

func TestWorkTraceOffTakesNoPlaywrightScreenshot(t *testing.T) {
	s, c, run := engineTraceServer(t)
	setAuto(t, s, autoWorkTrace, "false")
	if err := engineCall(t, s, run, "browser_navigate", `{"url":"https://shop.example/cart"}`); err != nil {
		t.Fatal(err)
	}
	if c.count("browser.snapshot") != 0 || len(captures(t, s, run)) != 0 {
		t.Fatal("trace off must not screenshot")
	}
}
