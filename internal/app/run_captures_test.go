package app

import (
	"context"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func setAuto(t *testing.T, s *Server, key, value string) {
	t.Helper()
	if err := s.saveAutomationValue(key, &value); err != nil {
		t.Fatal(err)
	}
}

func captures(t *testing.T, s *Server, run Run) []RunCapture {
	t.Helper()
	rows, err := s.store.runCaptures(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func execTool(t *testing.T, s *Server, run Run, tool, args string) (string, error) {
	t.Helper()
	tl, ok := toolNamed(s.microVMTools(run), tool)
	if !ok {
		t.Fatalf("no tool %s", tool)
	}
	return tl.Execute(context.Background(), json.RawMessage(args))
}

func runTool(t *testing.T, s *Server, run Run, tool, args string) string {
	t.Helper()
	out, err := execTool(t, s, run, tool, args)
	if err != nil {
		t.Fatalf("%s %s: %v", tool, args, err)
	}
	return out
}

func TestEveryCaptureIsKeptAsASmallPictureWithItsStep(t *testing.T) {
	s, _, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.click","x":412,"y":300}`)
	out := runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	var res captureOut
	_ = json.Unmarshal([]byte(out), &res)
	rows := captures(t, s, run)
	if len(rows) != 2 || res.CaptureSeq != 2 || rows[1].Seq != 2 {
		t.Fatalf("rows %+v seq %d", rows, res.CaptureSeq)
	}
	row := rows[1]
	if row.Redacted || row.AttachmentID == "" || row.Label != "click 412,300" || row.Tool != "computer_desktop" || row.Action != "desktop.capture" || row.Detail != "full" {
		t.Fatalf("%+v", row)
	}
	a, path, err := s.store.Attachment(row.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	if a.MIME != "image/jpeg" || a.Size > traceTileBytesMax || a.ConversationID != run.ConversationID {
		t.Fatalf("attachment %+v", a)
	}
	var origin string
	if err := s.store.db.QueryRow(`SELECT origin FROM attachments WHERE id=?`, a.ID).Scan(&origin); err != nil || origin != attachmentOriginTrace {
		t.Fatalf("origin %q %v", origin, err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width != 640 || cfg.Height != 400 || row.Width != 640 || row.Height != 400 {
		t.Fatalf("picture %v %v", cfg, err)
	}
	// The next capture starts a new step: the action note is used once.
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture","detail":"overview"}`)
	rows = captures(t, s, run)
	if len(rows) != 3 || rows[2].Label != "capture" || rows[2].Detail != "overview" {
		t.Fatalf("%+v", rows)
	}
}

func TestLabelsNeverCarryTypedTextOrAPath(t *testing.T) {
	s, _, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.type","text":"hunter2-synthetic"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.key","key":"a"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	runTool(t, s, run, "computer_browser", `{"action":"browser.navigate","url":"https://user:pw@Shop.Example/cart?token=abc#x"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	var labels []string
	for _, c := range captures(t, s, run) {
		labels = append(labels, c.Label)
	}
	want := []string{"type 17 characters", "key", "navigate shop.example"}
	for i, w := range want {
		if labels[i+1] != w {
			t.Fatalf("labels %q", labels)
		}
	}
	for _, l := range labels {
		if strings.Contains(l, "hunter2") || strings.Contains(l, "token") || strings.Contains(l, "pw") {
			t.Fatalf("label leaks: %q", l)
		}
	}
}

func TestNoPictureIsKeptWhileTheRunHoldsAPrivateInput(t *testing.T) {
	s, c, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	s.store.openSecretWindow(run.ID)
	out := runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	var res captureOut
	_ = json.Unmarshal([]byte(out), &res)
	if res.ImageURL == "" || res.CaptureSeq != 2 {
		t.Fatalf("the Bot must still receive its capture: %+v", res)
	}
	rows := captures(t, s, run)
	if len(rows) != 2 || !rows[1].Redacted || rows[1].AttachmentID != "" || rows[1].Reason != traceReasonSecretInput {
		t.Fatalf("%+v", rows)
	}
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE origin=?`, attachmentOriginTrace).Scan(&n); err != nil || n != 1 {
		t.Fatalf("pictures stored: %d %v", n, err)
	}
	// A navigation ends the window; a failed one does not (the action returns an error first).
	runTool(t, s, run, "computer_browser", `{"action":"browser.navigate","url":"https://shop.example/done"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	rows = captures(t, s, run)
	if len(rows) != 3 || rows[2].Redacted || rows[2].AttachmentID == "" {
		t.Fatalf("%+v", rows)
	}
	_ = c
}

func TestAnsweringAFormWithAPasswordOpensTheSecretWindow(t *testing.T) {
	s, c, run, q := formFixture(t)
	if s.store.secretWindowOpen(run.ID) {
		t.Fatal("window open before the answer")
	}
	if _, _, err := s.AnswerUserForm(q.ID, "human", formValues()); err != nil {
		t.Fatal(err)
	}
	if !s.store.secretWindowOpen(run.ID) {
		t.Fatal("a password reference exists but the window is closed")
	}
	_ = c
	s.store.closeSecretWindow(run.ID)
	if s.store.secretWindowOpen(run.ID) {
		t.Fatal("window not closed")
	}
}

func TestPrivateOriginsAndVaultBoundOriginsAreNeverKept(t *testing.T) {
	s, c, run := captureServer(t)
	setAuto(t, s, autoTracePrivateOrigins, "Bank.Example\nhttps://pay.example:8443")
	for _, tc := range []struct {
		url     string
		private bool
	}{
		{"https://www.bank.example/login", true},
		{"https://bank.example", true},
		{"https://notbank.example/", false},
		{"https://pay.example:8443/x", true},
		{"https://pay.example/x", false},
		{"https://news.example/a", false},
	} {
		c.snapshotURL = ""
		before := len(captures(t, s, run))
		s.noteTracePage(run.ID, tc.url)
		runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
		rows := captures(t, s, run)
		got := rows[before]
		if got.Redacted != tc.private || (got.AttachmentID == "") != tc.private {
			t.Errorf("%s: %+v", tc.url, got)
		}
		if tc.private && got.Reason != traceReasonPrivateOrigin {
			t.Errorf("%s reason %q", tc.url, got.Reason)
		}
	}
	// An origin a vault secret is bound to is private without any setting.
	s.secretVault, _ = initializeSecretVault(t.TempDir())
	s.secretVault.records["ref1"] = secretRecord{ID: "ref1", Kind: "browser_form", Target: "https://login.shop.example", Status: "ready"}
	before := len(captures(t, s, run))
	s.noteTracePage(run.ID, "https://login.shop.example/signin?next=/")
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	if got := captures(t, s, run)[before]; !got.Redacted || got.AttachmentID != "" {
		t.Fatalf("vault origin kept: %+v", got)
	}
	_ = c
}

func TestAPageNamedByASnapshotDecidesThePrivacyOfThatCapture(t *testing.T) {
	s, c, run := captureServer(t)
	setAuto(t, s, autoTracePrivateOrigins, "private.example")
	c.snapshotURL = "https://private.example/inbox"
	runTool(t, s, run, "computer_browser", `{"action":"browser.snapshot"}`)
	c.snapshotURL = "https://public.example/"
	runTool(t, s, run, "computer_browser", `{"action":"browser.snapshot"}`)
	rows := captures(t, s, run)
	if !rows[0].Redacted || rows[1].Redacted {
		t.Fatalf("%+v", rows)
	}
}

func TestCaptureWhilePersonControlsTheScreenIsRefusedAndNothingIsKept(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	s.microVM = &captureComputer{fakeComputer: newFakeComputer("firecracker"), shade: 90}
	run := contextSurfaceRun(t, s, conv, bot, "sign in")
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Enter the code", Type: questionTakeover, SourceURL: "https://login.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateQuestion(conv.ID, run, in); err != nil {
		t.Fatal(err)
	}
	if _, err := execTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`); err == nil || !strings.Contains(err.Error(), "takeover_active") && !strings.Contains(err.Error(), "controls the screen") {
		t.Fatalf("capture during takeover: %v", err)
	}
	if rows := captures(t, s, run); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestWorkTraceOffStoresNothingAndTheBotStillSeesTheScreen(t *testing.T) {
	s, _, run := captureServer(t)
	setAuto(t, s, autoWorkTrace, "false")
	out := runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture","detail":"overview"}`)
	var res captureOut
	_ = json.Unmarshal([]byte(out), &res)
	if res.ImageURL == "" || res.Width != 640 || res.CaptureSeq != 0 {
		t.Fatalf("%+v", res)
	}
	if rows := captures(t, s, run); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE origin=?`, attachmentOriginTrace).Scan(&n); err != nil || n != 0 {
		t.Fatalf("pictures %d %v", n, err)
	}
}

func TestARunKeepsAtMostSixtyPictures(t *testing.T) {
	s, _, run := captureServer(t)
	tool, _ := toolNamed(s.microVMTools(run), "computer_desktop")
	for i := 0; i < traceCapturesPerRunMax+3; i++ {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.capture","detail":"overview"}`)); err != nil {
			t.Fatal(err)
		}
	}
	rows := captures(t, s, run)
	kept, limited := 0, 0
	for _, r := range rows {
		if r.AttachmentID != "" {
			kept++
		}
		if r.Reason == traceReasonLimit {
			limited++
		}
	}
	if len(rows) != traceCapturesPerRunMax+3 || kept != traceCapturesPerRunMax || limited != 3 {
		t.Fatalf("rows %d kept %d limited %d", len(rows), kept, limited)
	}
}

func TestPrivateOriginsSettingIsNormalizedAndValidated(t *testing.T) {
	s := providerServer(t, nil, nil)
	if code := autoPutCode(t, s, `{"trace_private_origins":" Bank.Example , *.corp.example\nhttps://PAY.example:443/x "}`); code != 200 {
		t.Fatalf("put: %d", code)
	}
	if got := s.autoValue(autoTracePrivateOrigins); got != "bank.example\ncorp.example\nhttps://pay.example" {
		t.Fatalf("stored %q", got)
	}
	for _, bad := range []string{`{"trace_private_origins":"ftp://x.example"}`, `{"trace_private_origins":"a b"}`, `{"trace_private_origins":123}`} {
		if code := autoPutCode(t, s, bad); code != 400 {
			t.Errorf("%s accepted: %d", bad, code)
		}
	}
	if code := autoPutCode(t, s, `{"trace_retention_days":91}`); code != 400 {
		t.Fatalf("retention 91: %d", code)
	}
	if got := s.autoInt(autoTraceRetentionDays); got != 7 {
		t.Fatalf("default retention %d", got)
	}
	if !s.autoBool(autoWorkTrace) {
		t.Fatal("work trace must default on")
	}
}

func TestCaptureRoutesListAndDeleteATrace(t *testing.T) {
	s, _, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	id := captures(t, s, run)[0].AttachmentID
	get := httptest.NewRecorder()
	s.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "http://tofi.local/api/runs/"+run.ID+"/captures", nil))
	var body struct {
		Captures []RunCapture `json:"captures"`
	}
	if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &body) != nil || len(body.Captures) != 2 || body.Captures[0].AttachmentID != id {
		t.Fatalf("get: %d %s", get.Code, get.Body.String())
	}
	picture := httptest.NewRecorder()
	s.Handler().ServeHTTP(picture, httptest.NewRequest(http.MethodGet, "http://tofi.local/api/attachments/"+id, nil))
	if picture.Code != 200 || picture.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("picture: %d %s", picture.Code, picture.Header().Get("Content-Type"))
	}
	del := httptest.NewRecorder()
	s.Handler().ServeHTTP(del, httptest.NewRequest(http.MethodDelete, "http://tofi.local/api/runs/"+run.ID+"/captures", nil))
	if del.Code != 200 {
		t.Fatalf("delete: %d %s", del.Code, del.Body.String())
	}
	if rows := captures(t, s, run); len(rows) != 0 {
		t.Fatalf("rows left: %+v", rows)
	}
	if _, _, err := s.store.Attachment(id); err == nil {
		t.Fatal("picture left behind")
	}
	missing := httptest.NewRecorder()
	s.Handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "http://tofi.local/api/runs/nope/captures", nil))
	if missing.Code != 404 {
		t.Fatalf("missing run: %d", missing.Code)
	}
}
