package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStageRunAttachmentsBindsOnFinishAndDeduplicatesRetry(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("publisher", "", "model")
	c, _ := s.GetConversation(b.DMConversationID)
	_, r, _, err := s.AddUserRun(c.ID, b.ID, "publish", "publish-client")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running %v %v", ok, err)
	}
	a1, _ := s.AddAttachment(c.ID, "report.txt", "text/plain", bytes.NewBufferString("one"))
	first, err := s.stageRunAttachment(r, c, a1, "report")
	if err != nil {
		t.Fatal(err)
	}
	retry, _ := s.AddAttachment(c.ID, "report.txt", "text/plain", bytes.NewBufferString("one"))
	dedup, err := s.stageRunAttachment(r, c, retry, "report")
	if err != nil || dedup.ID != first.ID {
		t.Fatalf("dedup=%#v %v", dedup, err)
	}
	if _, _, err = s.Attachment(retry.ID); err == nil {
		t.Fatal("retry attachment was retained")
	}
	a2, _ := s.AddAttachment(c.ID, "report.txt", "text/plain", bytes.NewBufferString("two"))
	if _, err = s.stageRunAttachment(r, c, a2, "report"); err != nil {
		t.Fatal(err)
	}
	m, ok, err := s.FinishRun(r.ID, c.ID, b.ID, "files attached")
	if err != nil || !ok {
		t.Fatalf("finish=%v %v", ok, err)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].ID != a2.ID {
		t.Fatalf("attachments=%#v", m.Attachments)
	}
	if _, _, err = s.Attachment(a1.ID); err != nil {
		t.Fatalf("superseded attachment must stay stored: %v", err)
	}
}

func TestStageRunAttachmentSupersedesSameSourceOrName(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("publisher", "", "model")
	c, _ := s.GetConversation(b.DMConversationID)
	_, r, _, _ := s.AddUserRun(c.ID, b.ID, "publish", "supersede-client")
	_, _ = s.SetRunStatus(r.ID, "running", "")
	add := func(name, body, key string) Attachment {
		a, _ := s.AddAttachment(c.ID, name, "text/markdown", bytes.NewBufferString(body))
		staged, err := s.stageRunAttachment(r, c, a, key)
		if err != nil {
			t.Fatal(err)
		}
		return staged
	}
	add("seo.md", "v1", "/work/seo.md")
	b2 := add("seo.md", "v2 fixed", "/work/seo.md")         // same path
	b3 := add("seo.md", "v3 elsewhere", "/work/out/seo.md") // same name, different path
	other := add("notes.md", "n", "/work/notes.md")
	m, ok, err := s.FinishRun(r.ID, c.ID, b.ID, "done")
	if err != nil || !ok {
		t.Fatalf("finish=%v %v", ok, err)
	}
	if len(m.Attachments) != 2 || m.Attachments[0].ID != b3.ID || m.Attachments[1].ID != other.ID {
		t.Fatalf("attachments=%#v (b2=%s)", m.Attachments, b2.ID)
	}
}

// A run that publishes a file and then fails (for example the provider stays
// overloaded) must not take the file with it: the failure carries it, and the
// link the model already handed out keeps opening.
func TestFailedRunKeepsPublishedFileReachable(t *testing.T) {
	s, dir := ownerTestServer(t)
	cookie := ownerSetup(t, s, dir)
	b, _ := s.store.CreateBot("publisher", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, _ := s.store.AddUserRun(c.ID, b.ID, "publish", "failure-client")
	_, _ = s.store.SetRunStatus(r.ID, "running", "")
	a, _ := s.store.AddAttachment(c.ID, "x.xlsx", "application/octet-stream", bytes.NewBufferString("sheet"))
	if _, err := s.store.stageRunAttachment(r, c, a, "x"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "failed", "response failed: [server_is_overloaded] try later"); err != nil || !ok {
		t.Fatalf("fail=%v %v", ok, err)
	}
	w := ownerCall(s, "GET", "/api/attachments/"+a.ID, nil, cookie, true)
	if w.Code != 200 || w.Body.String() != "sheet" {
		t.Fatalf("download after failure=%d %q", w.Code, w.Body.String())
	}
	msgs, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var carried bool
	for _, m := range msgs {
		if m.RunID != r.ID || m.Role != "assistant" {
			continue
		}
		if items, e := s.store.AttachmentsForMessage(m.ID); e == nil && len(items) == 1 && items[0].ID == a.ID {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("no failure message carries the file: %#v", msgs)
	}
	var pending int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, r.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("staged rows left=%d %v", pending, err)
	}
}

func TestCancelAndRestartRecoveryCleanPendingAttachments(t *testing.T) {
	for _, mode := range []string{"cancel", "restart"} {
		t.Run(mode, func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b, _ := s.CreateBot("publisher", "", "model")
			c, _ := s.GetConversation(b.DMConversationID)
			_, r, _, _ := s.AddUserRun(c.ID, b.ID, "x", "cleanup-"+mode)
			_, _ = s.SetRunStatus(r.ID, "running", "")
			a, _ := s.AddAttachment(c.ID, "x.txt", "text/plain", bytes.NewBufferString("x"))
			if _, err = s.stageRunAttachment(r, c, a, "x"); err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				err = s.CancelRunTree(r.ID)
			} else {
				giveRunObservableWork(t, s, r)
				err = recoverInterruptedRuns(s.db)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.Attachment(a.ID); err == nil {
				t.Fatal("terminal run retained pending attachment")
			}
		})
	}
}

func TestForwardResultCarriesAttachmentsAndScopesReaders(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	parent, _ := s.CreateBot("parent", "", "model")
	child, _ := s.CreateBot("child", "", "model")
	origin, _ := s.CreateGroup("origin", []string{parent.ID, child.ID})
	target, _ := s.GetConversation(child.DMConversationID)
	pr, err := s.AddRun(origin.ID, parent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(pr.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, cr, err := s.AddForwardHandoff(origin.ID, parent.ID, child.ID, pr.ID, "make file")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(cr.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	a, _ := s.AddAttachment(target.ID, "result.txt", "text/plain", bytes.NewBufferString("forwarded"))
	if _, err = s.stageRunAttachment(cr, target, a, "result"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.FinishRun(cr.ID, target.ID, child.ID, "done"); err != nil || !ok {
		t.Fatalf("finish=%v %v", ok, err)
	}
	var forwardID string
	if err = s.db.QueryRow(`SELECT id FROM messages WHERE conversation_id=? AND kind='forward_result' AND run_id=?`, origin.ID, cr.ID).Scan(&forwardID); err != nil {
		t.Fatal(err)
	}
	items, err := s.AttachmentsForMessage(forwardID)
	if err != nil || len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("forward attachments=%#v %v", items, err)
	}
	if text, _, err := s.ReadAttachmentText(a.ID, origin.ID); err != nil || text != "forwarded" {
		t.Fatalf("origin read=%q %v", text, err)
	}
	stranger, _ := s.CreateBot("stranger", "", "model")
	if _, _, err = s.ReadAttachmentText(a.ID, stranger.DMConversationID); err == nil {
		t.Fatal("unforwarded conversation read attachment")
	}
}

func TestAttachmentPDFInlineOnlyOnRequest(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := &Server{store: s}
	b, _ := s.CreateBot("viewer", "", "model")
	c, _ := s.GetConversation(b.DMConversationID)
	a, err := s.AddAttachment(c.ID, "doc.pdf", "application/pdf", bytes.NewBufferString("%PDF-1.4\n%%EOF"))
	if err != nil {
		t.Fatal(err)
	}
	for url, want := range map[string]string{"/api/attachments/" + a.ID: "attachment", "/api/attachments/" + a.ID + "?inline=1": "inline"} {
		w := httptest.NewRecorder()
		if !srv.routeAttachments(w, httptest.NewRequest(http.MethodGet, url, nil), "attachments/"+a.ID) || w.Code != 200 {
			t.Fatalf("%s: %d", url, w.Code)
		}
		if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, want) {
			t.Fatalf("%s disposition=%q", url, got)
		}
	}
}

func TestPublishedFileResultCarriesOpenableLink(t *testing.T) {
	s, dir := ownerTestServer(t)
	cookie := ownerSetup(t, s, dir)
	b, _ := s.store.CreateBot("linker", "", "model")
	a, err := s.store.AddAttachment(b.DMConversationID, "news.md", "text/markdown", bytes.NewBufferString("# synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(s.publishedFileResult(a)), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := read()["url"]; got != "/api/attachments/"+a.ID {
		t.Fatalf("relative url=%v", got)
	}
	s.publicOrigin = "https://tofi.example.invalid"
	link, _ := read()["url"].(string)
	if link != "https://tofi.example.invalid/api/attachments/"+a.ID {
		t.Fatalf("absolute url=%q", link)
	}
	path := strings.TrimPrefix(link, s.publicOrigin)
	if w := ownerCall(s, "GET", path, nil, nil, true); w.Code != 401 {
		t.Fatalf("unauthenticated link=%d", w.Code)
	}
	w := ownerCall(s, "GET", path, nil, cookie, true)
	if w.Code != 200 || w.Body.String() != "# synthetic" {
		t.Fatalf("authenticated link=%d %q", w.Code, w.Body.String())
	}
}
