package app

import "testing"

func (f taskFix) foldedTaskOf(conv, messageID string) string {
	f.t.Helper()
	history, _, err := f.s.Messages(conv, 0, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, m := range history {
		if m.ID == messageID {
			return m.TaskID
		}
	}
	f.t.Fatalf("message %s not in history", messageID)
	return ""
}

// Only the part's work report folds. A note or a mid-run message of the holder is
// spoken to the person and stays an ordinary message.
func TestOnlyThePartResultFolds(t *testing.T) {
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"}})
	part := f.startPart(r.Tasks[0].TaskID, f.c)
	note, _, err := f.s.AddMessage(f.g.ID, "assistant", f.c.ID, part.ID, "Checking two sites.", "")
	if err != nil {
		t.Fatal(err)
	}
	result, ok, err := f.s.FinishRun(part.ID, f.g.ID, f.c.ID, "Three hotels.")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := f.foldedTaskOf(f.g.ID, note.ID); got != "" {
		t.Fatalf("a note of the holder is not a report to the lead: %q", got)
	}
	if got := f.foldedTaskOf(f.g.ID, result.ID); got != r.Tasks[0].TaskID {
		t.Fatalf("the result folds under its part: %q", got)
	}
}

// A member's reply to the person never folds, also when the person's message was
// queued for the run that holds the part (queued steering).
func TestAReplyToThePersonIsNotFolded(t *testing.T) {
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"}})
	part := f.startPart(r.Tasks[0].TaskID, f.c)
	// The person asks the holder a question while it works: the run reads it at a boundary.
	person, _, _, _, err := f.s.addUserRunsSteering(f.g.ID, "what price range?", "client-"+newID(), nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`INSERT INTO bot_inbox(id,bot_id,conversation_id,kind,body,wakes,needs_reply,priority,parent_run,anchor,created_at,consumed_by_run,consumed_at)
 VALUES(?,?,?,'user_message','what price range?',0,1,0,?,?,?,?,?)`, newID(), f.c.ID, f.g.ID, part.ID, person.ID, now(), part.ID, now()); err != nil {
		t.Fatal(err)
	}
	reply, ok, err := f.s.FinishRun(part.ID, f.g.ID, f.c.ID, "Between 100 and 150 a night.")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := f.foldedTaskOf(f.g.ID, reply.ID); got != "" {
		t.Fatalf("the reply to the person must stay visible: %q", got)
	}
}

// A run that a message of the person started answers the person.
func TestAResultOfARunTheUserStartedIsNotFolded(t *testing.T) {
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"}})
	part := f.startPart(r.Tasks[0].TaskID, f.c)
	person, _, _, _, err := f.s.addUserRunsSteering(f.g.ID, "@charlie any news?", "client-"+newID(), nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`UPDATE runs SET trigger_message_id=? WHERE id=?`, person.ID, part.ID); err != nil {
		t.Fatal(err)
	}
	reply, ok, err := f.s.FinishRun(part.ID, f.g.ID, f.c.ID, "Nothing new.")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := f.foldedTaskOf(f.g.ID, reply.ID); got != "" {
		t.Fatalf("a reply to the person stays visible: %q", got)
	}
}

// A result with a file, a card, a question or an error stays visible.
func TestResultsWithFilesCardsAndErrorsAreNotFolded(t *testing.T) {
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"}})
	part := f.startPart(r.Tasks[0].TaskID, f.c)
	result, ok, err := f.s.FinishRun(part.ID, f.g.ID, f.c.ID, "Three hotels, in the attached file.")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := f.foldedTaskOf(f.g.ID, result.ID); got != r.Tasks[0].TaskID {
		t.Fatalf("without a file the result folds: %q", got)
	}
	if _, err = f.s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES('att-1',?,'hotels.csv','text/csv',3,'att-1.bin',?)`, f.g.ID, now()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`INSERT INTO attachment_messages(attachment_id,message_id) VALUES('att-1',?)`, result.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.foldedTaskOf(f.g.ID, result.ID); got != "" {
		t.Fatalf("a result with a file stays visible: %q", got)
	}
	for _, kind := range []string{"question", "card", "error", "notice"} {
		if _, err = f.s.db.Exec(`UPDATE messages SET kind=? WHERE id=?`, kind, result.ID); err != nil {
			t.Fatal(err)
		}
		if got := f.foldedTaskOf(f.g.ID, result.ID); got != "" {
			t.Fatalf("a %s message stays visible: %q", kind, got)
		}
	}
}
