package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func sayTestPNG() []byte {
	b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(tinyPNGURL, "data:image/png;base64,"))
	return b
}

func sayCall(s *Server, c Conversation, r Run, callID, args string) (string, error) {
	return s.sayTool(c, r).Execute(runtime.WithToolCallID(context.Background(), callID), json.RawMessage(args))
}

func attachmentCount(t *testing.T, s *Server, conv string) int {
	t.Helper()
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE conversation_id=? AND origin<>'trace'`, conv).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func runningCaptureServer(t *testing.T) (*Server, Conversation, Run) {
	t.Helper()
	s, _, run := captureServer(t)
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running %v %v", ok, err)
	}
	c, err := s.store.GetConversation(run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, run
}

func pictureServer(t *testing.T, hits *int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(sayTestPNG())
	}))
	t.Cleanup(srv.Close)
	return strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
}

func TestSendMessageAttachesUploadAndGeneratedImageAndRefusesAnotherConversation(t *testing.T) {
	s, c, run := runningCaptureServer(t)
	up, err := s.store.AddAttachment(c.ID, "notes.txt", "text/plain", strings.NewReader("synthetic notes"))
	if err != nil {
		t.Fatal(err)
	}
	gen, err := s.store.addAttachmentOpts(context.Background(), c.ID, "picture.png", "image/png", bytes.NewReader(sayTestPNG()), attachmentOpts{Origin: attachmentOriginGenerated, BotID: run.BotID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.stageRunAttachment(run, c, gen, "image:synthetic"); err != nil {
		t.Fatal(err)
	}
	out, err := sayCall(s, c, run, "a1", `{"purpose":"answer","outcome":"done","text":"Here they are.","attachments":[{"attachment_id":"`+up.ID+`"},{"attachment_id":"`+gen.ID+`"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Delivered") || !strings.Contains(out, "notes.txt") || !strings.Contains(out, "picture.png") {
		t.Fatalf("result %q", out)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 2 {
		t.Fatalf("staged for the reply: %v", names)
	}

	// A file of another conversation is refused by name.
	other, _ := s.store.CreateBot("neighbour", "", "model")
	oc, _ := s.store.GetConversation(other.DMConversationID)
	foreign, err := s.store.AddAttachment(oc.ID, "secret.txt", "text/plain", strings.NewReader("private"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = sayCall(s, c, run, "a2", `{"purpose":"message","outcome":"pending","text":"One more.","attachments":[{"attachment_id":"`+foreign.ID+`"}]}`)
	if code := outcomeErrCode(err); code != "attachment_other_conversation" {
		t.Fatalf("code %q err %v", code, err)
	}
}

func TestSendMessageReplacesTheGeneratedImage(t *testing.T) {
	s, c, run := runningCaptureServer(t)
	gen, err := s.store.addAttachmentOpts(context.Background(), c.ID, "raw.png", "image/png", bytes.NewReader(sayTestPNG()), attachmentOpts{Origin: attachmentOriginGenerated, BotID: run.BotID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.stageRunAttachment(run, c, gen, "image:synthetic"); err != nil {
		t.Fatal(err)
	}
	s.downloadTransport = http.DefaultTransport
	site := pictureServer(t, nil)
	if _, err := sayCall(s, c, run, "r1", `{"purpose":"answer","outcome":"done","text":"Finished.","attachments":[{"url":"`+site+`/final.png","replaces_attachment_id":"`+gen.ID+`"}]}`); err != nil {
		t.Fatal(err)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 1 || names[0] != "final.png" {
		t.Fatalf("reply files %v", names)
	}
}

func TestSendMessageAttachesABotComputerFile(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	old := sayAttachmentRoot
	sayAttachmentRoot = root
	t.Cleanup(func() { sayAttachmentRoot = old })
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "build the export", "sm-1", nil, nil)
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	dir := filepath.Join(root, "bots", bot.ID)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "export.zip"), []byte("zip bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sayCall(s, conv, run, "p0", `{"purpose":"answer","outcome":"done","text":"x","attachments":[{"path":"export.zip"}]}`); outcomeErrCode(err) != "attachment_path_invalid" {
		t.Fatalf("a relative path: %v", err)
	}
	if _, err := sayCall(s, conv, run, "p1", `{"purpose":"answer","outcome":"done","text":"x","attachments":[{"path":`+jsonQuote(filepath.Join(dir, "export.zip"))+`,"computer_id":"a-persons-mac"}]}`); outcomeErrCode(err) != "attachment_source_unavailable" {
		t.Fatalf("a person's computer: %v", err)
	}
	out, err := sayCall(s, conv, run, "p2", `{"purpose":"answer","outcome":"done","text":"Done.","attachments":[{"path":`+jsonQuote(filepath.Join(dir, "export.zip"))+`,"name":"report.zip"}]}`)
	if err != nil || !strings.Contains(out, "report.zip") {
		t.Fatalf("%q %v", out, err)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 1 || names[0] != "report.zip" {
		t.Fatalf("reply files %v", names)
	}
}

func TestSendMessageDownloadsAPublicURLAndRefusesAPrivateOne(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "get the picture")
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.downloadTransport = http.DefaultTransport
	site := pictureServer(t, nil)
	out, err := sayCall(s, conv, run, "u1", `{"purpose":"answer","outcome":"done","text":"Here.","attachments":[{"url":"`+site+`/pics/cat.png"}]}`)
	if err != nil || !strings.Contains(out, "cat.png") {
		t.Fatalf("%q %v", out, err)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 1 || names[0] != "cat.png" {
		t.Fatalf("reply files %v", names)
	}

	// The guarded default refuses loopback, by literal and by name.
	s.downloadTransport = nil
	before := attachmentCount(t, s, conv.ID)
	for _, u := range []string{strings.Replace(site, "localhost", "127.0.0.1", 1), site, "http://169.254.169.254/latest/"} {
		_, err = sayCall(s, conv, run, "u2", `{"purpose":"message","outcome":"pending","text":"Again.","attachments":[{"url":"`+u+`/x.png"}]}`)
		if code := outcomeErrCode(err); code != "attachment_download_refused" {
			t.Fatalf("%s: code %q err %v", u, code, err)
		}
	}
	if after := attachmentCount(t, s, conv.ID); after != before {
		t.Fatalf("attachments %d -> %d", before, after)
	}
}

func TestSendMessageSendsACaptureByItsIDAtFullSize(t *testing.T) {
	s, c, run := runningCaptureServer(t)
	desktop, ok := toolNamed(s.microVMTools(run), "computer_desktop")
	if !ok {
		t.Fatal("no computer_desktop")
	}
	out, err := desktop.Execute(t.Context(), json.RawMessage(`{"action":"desktop.capture","detail":"overview"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		CaptureID string `json:"capture_id"`
		Width     int    `json:"width"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || !strings.HasPrefix(res.CaptureID, "cap-") {
		t.Fatalf("result %.300s", out)
	}
	if res.Width >= 1280 {
		t.Errorf("the model read the full picture, width %d", res.Width)
	}
	if n := attachmentCount(t, s, c.ID); n != 0 {
		t.Fatalf("a capture was stored before it was sent: %d", n)
	}
	// browser.snapshot results carry one too.
	browser, _ := toolNamed(s.microVMTools(run), "computer_browser")
	if bo, err := browser.Execute(t.Context(), json.RawMessage(`{"action":"browser.snapshot"}`)); err != nil || !strings.Contains(bo, `"capture_id"`) {
		t.Fatalf("snapshot %v %.200s", err, bo)
	}

	if _, err := sayCall(s, c, run, "c1", `{"purpose":"answer","outcome":"done","text":"Here is the screen.","attachments":[{"attachment_id":"`+res.CaptureID+`"}]}`); err != nil {
		t.Fatal(err)
	}
	names := s.store.runAttachmentNames(run.ID)
	if len(names) != 1 || !strings.HasPrefix(names[0], "screenshot-") {
		t.Fatalf("reply files %v", names)
	}
	var id string
	if err := s.store.db.QueryRow(`SELECT attachment_id FROM run_attachments WHERE run_id=?`, run.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, path, err := s.store.Attachment(id)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if cfg, _, err := image.DecodeConfig(f); err != nil || cfg.Width != 1280 || cfg.Height != 800 {
		t.Errorf("attached picture %dx%d %v, want 1280x800", cfg.Width, cfg.Height, err)
	}
}

func TestSendMessageRefusesAWithheldCaptureAndAnotherRunsCapture(t *testing.T) {
	s, c, run := runningCaptureServer(t)
	desktop, _ := toolNamed(s.microVMTools(run), "computer_desktop")
	s.store.openSecretWindow(run.ID)
	out, err := desktop.Execute(t.Context(), json.RawMessage(`{"action":"desktop.capture"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		CaptureID string `json:"capture_id"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || res.CaptureID == "" {
		t.Fatalf("result %.200s", out)
	}
	_, err = sayCall(s, c, run, "w1", `{"purpose":"answer","outcome":"done","text":"Here.","attachments":[{"attachment_id":"`+res.CaptureID+`"}]}`)
	if code := outcomeErrCode(err); code != "attachment_withheld" {
		t.Fatalf("code %q err %v", code, err)
	}
	// A capture of another run is not found here.
	s.keepCapture("another-run", keptCapture{ComputerID: microVMComputerID}, `{"image_url":"`+tinyPNGURL+`"}`, `{"image_url":"`+tinyPNGURL+`"}`)
	var foreign string
	s.keptCaptures.Range(func(k, v any) bool {
		if k == "another-run" {
			foreign = v.(*runCaptureKeep).items[0].ID
		}
		return true
	})
	_, err = sayCall(s, c, run, "w2", `{"purpose":"answer","outcome":"done","text":"Here.","attachments":[{"attachment_id":"`+foreign+`"}]}`)
	if code := outcomeErrCode(err); code != "attachment_not_found" {
		t.Fatalf("code %q err %v", code, err)
	}
	if n := attachmentCount(t, s, c.ID); n != 0 {
		t.Fatalf("attachments left: %d", n)
	}
}

func TestSendMessageIsAtomicWhenAnItemFails(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "get the pictures")
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.downloadTransport = http.DefaultTransport
	site := pictureServer(t, nil)
	_, err := sayCall(s, conv, run, "x1", `{"purpose":"answer","outcome":"done","text":"Here.","attachments":[{"url":"`+site+`/a.png"},{"attachment_id":"missing"}]}`)
	o := outcomeErrCode(err)
	if o != "attachment_not_found" || !strings.Contains(err.Error(), "attachments[1]") {
		t.Fatalf("code %q err %v", o, err)
	}
	if n := attachmentCount(t, s, conv.ID); n != 0 {
		t.Fatalf("the first item's attachment was kept: %d", n)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 0 {
		t.Fatalf("staged %v", names)
	}
	// Two sources in one item, none in another.
	for _, bad := range []string{`{"url":"` + site + `/a.png","path":"/x"}`, `{}`} {
		_, err = sayCall(s, conv, run, "x2", `{"purpose":"answer","outcome":"done","text":"Here.","attachments":[`+bad+`]}`)
		if code := outcomeErrCode(err); code != "attachment_source_invalid" {
			t.Fatalf("%s: code %q", bad, code)
		}
	}
}

func TestSendMessageGateRefusalDeletesTheCreatedAttachments(t *testing.T) {
	s, c, run, state := outcomeFixture(t, "")
	state.review = func() bool { return true }
	s.downloadTransport = http.DefaultTransport
	site := pictureServer(t, nil)
	_, err := sayCall(s, c, run, "g1", `{"purpose":"answer","outcome":"done","text":"Here.","attachments":[{"url":"`+site+`/a.png"}]}`)
	if code := outcomeErrCode(err); code != "answer_review" {
		t.Fatalf("code %q err %v", code, err)
	}
	if n := attachmentCount(t, s, c.ID); n != 0 {
		t.Fatalf("attachments left after a refused answer: %d", n)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 0 {
		t.Fatalf("staged %v", names)
	}
	// The pending check refuses too, after the items resolved.
	state.review = nil
	_, err = sayCall(s, c, run, "g2", `{"purpose":"answer","outcome":"pending","text":"Soon.","attachments":[{"url":"`+site+`/a.png"}]}`)
	if code := outcomeErrCode(err); code != "answer_pending_work" {
		t.Fatalf("code %q err %v", code, err)
	}
	if n := attachmentCount(t, s, c.ID); n != 0 {
		t.Fatalf("attachments left after the pending check: %d", n)
	}
}

func TestSendMessageMessageBindsFilesToTheSegmentNotTheAnswer(t *testing.T) {
	s, c, run := runningCaptureServer(t)
	gen, err := s.store.addAttachmentOpts(context.Background(), c.ID, "kept.png", "image/png", bytes.NewReader(sayTestPNG()), attachmentOpts{Origin: attachmentOriginGenerated, BotID: run.BotID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.stageRunAttachment(run, c, gen, "image:synthetic"); err != nil {
		t.Fatal(err)
	}
	up, err := s.store.AddAttachment(c.ID, "notes.txt", "text/plain", strings.NewReader("synthetic notes"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := sayCall(s, c, run, "m1", `{"purpose":"message","outcome":"pending","text":"A first look.","attachments":[{"attachment_id":"`+up.ID+`"}]}`)
	if err != nil || !strings.Contains(out, "notes.txt") {
		t.Fatalf("%q %v", out, err)
	}
	var msg, kind string
	if err := s.store.db.QueryRow(`SELECT m.id,m.kind FROM attachment_messages am JOIN messages m ON m.id=am.message_id WHERE am.attachment_id=? AND m.run_id=?`, up.ID, run.ID).Scan(&msg, &kind); err != nil || kind != "segment" {
		t.Fatalf("the file is not bound to the segment message: %v kind=%q", err, kind)
	}
	if names := s.store.runAttachmentNames(run.ID); len(names) != 1 || names[0] != "kept.png" {
		t.Fatalf("files left for the answer: %v", names)
	}
	var events int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='message' AND data LIKE '%notes.txt%'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("the message event does not carry the file: %d %v", events, err)
	}
}

func TestSendMessageNoteRefusesFilesAndTooManyIsRefusedFirst(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	run := contextSurfaceRun(t, s, conv, bot, "get the pictures")
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.downloadTransport = http.DefaultTransport
	hits := 0
	site := pictureServer(t, &hits)
	_, err := sayCall(s, conv, run, "n1", `{"purpose":"note","outcome":"pending","text":"Working.","attachments":[{"url":"`+site+`/a.png"}]}`)
	if code := outcomeErrCode(err); code != "attachments_not_for_note" {
		t.Fatalf("code %q err %v", code, err)
	}
	var items []string
	for i := 0; i < 9; i++ {
		items = append(items, `{"url":"`+site+`/a.png"}`)
	}
	_, err = sayCall(s, conv, run, "n2", `{"purpose":"answer","outcome":"done","text":"Here.","attachments":[`+strings.Join(items, ",")+`]}`)
	if code := outcomeErrCode(err); code != "attachments_too_many" {
		t.Fatalf("code %q err %v", code, err)
	}
	if hits != 0 || attachmentCount(t, s, conv.ID) != 0 {
		t.Fatalf("something was created before the count check: hits=%d", hits)
	}
}

func TestPublishFileIsDeferredAndTheDesktopToolHasNoShare(t *testing.T) {
	s, _, run := runningCaptureServer(t)
	c, _ := s.store.GetConversation(run.ConversationID)
	tools := s.publishAttachmentTools(c, run)
	if len(tools) != 1 || !tools[0].Deferred {
		t.Fatalf("publish_file must be a deferred tool: %+v", tools)
	}
	if !strings.Contains(tools[0].Description, "send_message attachments") {
		t.Errorf("description %q", tools[0].Description)
	}
}
