package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func (f taskFix) textAttachment(conv, name, body string) Attachment {
	f.t.Helper()
	a, err := f.s.AddAttachment(conv, name, "text/plain", strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

func (f taskFix) readAs(c Conversation, r Run, id string) (string, error) {
	f.t.Helper()
	raw, _ := json.Marshal(map[string]string{"attachment_id": id})
	return f.srv.attachmentTools(c, r)[0].Execute(context.Background(), raw)
}

// A delegate across DMs and a message to several Bots both put the files on the
// brief the recipient reads, so the recipient opens and stages them.
func TestDelegateAttachmentsReachTheRecipientsRun(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	att := f.textAttachment(dm.ID, "notes.txt", "quarterly numbers")
	root := f.running(dm.ID, f.a)

	// Before the call the recipient cannot see the file.
	bravoDM, _ := f.s.GetConversation(f.b.DMConversationID)
	if ok, _ := f.s.attachmentAvailableInConversation(att.ID, bravoDM.ID); ok {
		t.Fatal("recipient saw the file before delegation")
	}
	if _, err := call(t, f.tool(dm, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "Summarise the notes", "attachment_ids": []string{att.ID}}); err != nil {
		t.Fatal(err)
	}
	var childID, trigger string
	if err := f.s.db.QueryRow(`SELECT id,trigger_message_id FROM runs WHERE bot_id=? AND parent_run_id=?`, f.b.ID, root.ID).Scan(&childID, &trigger); err != nil {
		t.Fatal(err)
	}
	child, _ := f.s.GetRun(childID)
	if n := f.count(`SELECT COUNT(*) FROM attachment_messages WHERE attachment_id=? AND message_id=?`, att.ID, trigger); n != 1 {
		t.Fatalf("brief carries %d bindings", n)
	}
	if out, err := f.readAs(bravoDM, child, att.ID); err != nil || !strings.Contains(out, "quarterly numbers") {
		t.Fatalf("recipient read: %q %v", out, err)
	}
	staged, err := f.s.guestInputCandidates(bravoDM.ID, trigger)
	if err != nil || len(staged) != 1 || staged[0].ID != att.ID || !staged[0].Triggering {
		t.Fatalf("staging %+v %v", staged, err)
	}

	// Several recipients share one hidden trace; the brief there carries the file.
	root2 := f.running(dm.ID, f.a)
	if _, err := call(t, f.tool(dm, root2, "delegate"), map[string]any{"bot_ids": []string{f.b.ID, f.c.ID}, "task": "Both read the notes", "attachment_ids": []string{att.ID}}); err != nil {
		t.Fatal(err)
	}
	var traceRun, traceConv string
	if err := f.s.db.QueryRow(`SELECT id,conversation_id FROM runs WHERE bot_id=? AND parent_run_id=?`, f.c.ID, root2.ID).Scan(&traceRun, &traceConv); err != nil {
		t.Fatal(err)
	}
	trace, _ := f.s.GetConversation(traceConv)
	tr, _ := f.s.GetRun(traceRun)
	if out, err := f.readAs(trace, tr, att.ID); err != nil || !strings.Contains(out, "quarterly numbers") {
		t.Fatalf("trace read: %q %v", out, err)
	}
}

func TestDelegateRefusesForeignOrUnknownAttachmentAndChangesNothing(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	foreign := f.textAttachment(f.c.DMConversationID, "secret.txt", "not yours")
	root := f.running(dm.ID, f.a)
	for _, ids := range [][]string{{foreign.ID}, {"no-such-attachment"}} {
		_, err := call(t, f.tool(dm, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "Look", "attachment_ids": ids})
		if err == nil || !strings.Contains(err.Error(), "not an attachment of this conversation") {
			t.Fatalf("ids %v: err %v", ids, err)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM runs WHERE parent_run_id=?`, root.ID); n != 0 {
		t.Fatalf("%d runs started", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM attachment_messages WHERE attachment_id=?`, foreign.ID); n != 0 {
		t.Fatalf("%d bindings of a foreign file", n)
	}
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = f.textAttachment(dm.ID, "f.txt", "x").ID
	}
	if _, err := call(t, f.tool(dm, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "Look", "attachment_ids": nine}); err == nil {
		t.Fatal("nine attachments accepted")
	}
}

// Decision 0065: the "@name brief" message of a group delegation shows the files.
func TestGroupDelegationMessageCarriesAttachments(t *testing.T) {
	f := newTaskFix(t)
	att := f.textAttachment(f.g.ID, "plan.txt", "the plan")
	root := f.running(f.g.ID, f.a)
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "Review the plan", "attachment_ids": []string{att.ID}}); err != nil {
		t.Fatal(err)
	}
	msgs := f.groupMessages(f.g.ID)
	if len(msgs) != 1 || msgs[0].Content != "@bravo Review the plan" {
		t.Fatalf("messages %+v", msgs)
	}
	got, err := f.s.AttachmentsForMessage(msgs[0].ID)
	if err != nil || len(got) != 1 || got[0].ID != att.ID {
		t.Fatalf("message attachments %+v %v", got, err)
	}
	var ev string
	if err := f.s.db.QueryRow(`SELECT data FROM events WHERE conversation_id=? AND type='message' AND data LIKE ? ORDER BY id DESC LIMIT 1`, f.g.ID, `%Review the plan%`).Scan(&ev); err != nil || !strings.Contains(ev, att.ID) {
		t.Fatalf("event lacks the attachment: %v %s", err, ev)
	}
}

// A task that waits carries its files: when the dependency is done and the task
// starts, the brief that reaches its run holds them.
func TestWaitingDelegationDeliversAttachmentsWhenItStarts(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	att := f.textAttachment(dm.ID, "data.txt", "waiting payload")
	root := f.running(dm.ID, f.a)
	iface := f.delegateTo(dm, root, f.b, "Prepare the ground")
	out, err := call(t, f.tool(dm, root, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "Use the data", "depends_on": []string{iface.TaskID}, "attachment_ids": []string{att.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Tasks []receiptTask `json:"tasks"`
	}
	if err = json.Unmarshal([]byte(out), &receipt); err != nil || len(receipt.Tasks) != 1 {
		t.Fatalf("receipt %q %v", out, err)
	}
	waiting := receipt.Tasks[0]
	charlieDM, _ := f.s.GetConversation(f.c.DMConversationID)
	if ok, _ := f.s.attachmentAvailableInConversation(att.ID, charlieDM.ID); ok {
		t.Fatal("file visible before the task starts")
	}
	ifaceRun := f.taskRow(iface.TaskID).AssigneeRunID
	f.start(ifaceRun)
	if _, ok, err := f.s.FinishRun(ifaceRun, f.b.DMConversationID, f.b.ID, "Ground prepared."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.srv.flushInbox()
	row := f.taskRow(waiting.TaskID)
	if row.AssigneeRunID == "" {
		t.Fatalf("task did not start: %+v", row)
	}
	run, _ := f.s.GetRun(row.AssigneeRunID)
	if out, err := f.readAs(charlieDM, run, att.ID); err != nil || !strings.Contains(out, "waiting payload") {
		t.Fatalf("read: %q %v", out, err)
	}
	staged, err := f.s.guestInputCandidates(charlieDM.ID, "")
	if err != nil || len(staged) != 1 || staged[0].ID != att.ID {
		t.Fatalf("staging %+v %v", staged, err)
	}
}
