package app

import (
	"bytes"
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
	if len(m.Attachments) != 2 || m.Attachments[0].ID != a1.ID || m.Attachments[1].ID != a2.ID {
		t.Fatalf("attachments=%#v", m.Attachments)
	}
}

func TestFailedRunCleansOnlyUnboundPublishedAttachment(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.CreateBot("publisher", "", "model")
	c, _ := s.GetConversation(b.DMConversationID)
	_, r, _, _ := s.AddUserRun(c.ID, b.ID, "publish", "failure-client")
	_, _ = s.SetRunStatus(r.ID, "running", "")
	a, _ := s.AddAttachment(c.ID, "x.bin", "application/octet-stream", bytes.NewBufferString("x"))
	if _, err = s.stageRunAttachment(r, c, a, "x"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(r.ID, "failed", "cancelled"); err != nil || !ok {
		t.Fatalf("fail=%v %v", ok, err)
	}
	if _, _, err = s.Attachment(a.ID); err == nil {
		t.Fatal("orphan attachment retained")
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
