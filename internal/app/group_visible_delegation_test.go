package app

import (
	"strings"
	"testing"
)

// Decision 0065: in a group, a delegation to a member of the same group is a
// normal message of the assigning Bot, "@<name> <brief>".

type visibleMsg struct {
	ID, Kind, Sender, Content, RunID string
}

func (f taskFix) groupMessages(conv string) []visibleMsg {
	f.t.Helper()
	rows, err := f.s.db.Query(`SELECT id,kind,COALESCE(sender_bot_id,''),content,COALESCE(run_id,'') FROM messages WHERE conversation_id=? AND role='assistant' ORDER BY seq`, conv)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []visibleMsg
	for rows.Next() {
		var m visibleMsg
		if err := rows.Scan(&m.ID, &m.Kind, &m.Sender, &m.Content, &m.RunID); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestSingleGroupDelegationIsOneVisibleMessageMentioningTheRecipient(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	out, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "Write the page"})
	if err != nil {
		t.Fatal(err)
	}
	msgs := f.groupMessages(f.g.ID)
	if len(msgs) != 1 {
		t.Fatalf("want one message, got %+v", msgs)
	}
	m := msgs[0]
	if m.Kind != "" || m.Sender != f.a.ID || m.Content != "@bravo Write the page" {
		t.Fatalf("message %+v", m)
	}
	if n := f.count(`SELECT COUNT(*) FROM messages WHERE kind IN ('notice','message_ref')`); n != 0 {
		t.Fatalf("%d notice lines", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM conversations WHERE user_visible=0`); n != 0 {
		t.Fatalf("%d hidden conversations", n)
	}
	var childID, conv, kind, trigger string
	if err := f.s.db.QueryRow(`SELECT id,conversation_id,kind,trigger_message_id FROM runs WHERE bot_id=? AND parent_run_id=?`, f.b.ID, root.ID).Scan(&childID, &conv, &kind, &trigger); err != nil {
		t.Fatalf("%v (%s)", err, out)
	}
	if conv != f.g.ID || kind != runKindGroupTask || trigger != m.ID {
		t.Fatalf("run %s %s %s trigger %s want %s", childID, conv, kind, trigger, m.ID)
	}
	task := f.taskOf(childID)
	if task.AssignerBotID != f.a.ID || task.Status != "todo" || task.BotID != f.b.ID {
		t.Fatalf("task %+v", task)
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND run_id=? AND kind='task_result' AND status='pending'`, f.a.ID, root.ID); n != 1 {
		t.Fatalf("giver holds %d waits", n)
	}
	// The brief on the task record is the brief, without the mention.
	if task.Description != "Write the page" {
		t.Fatalf("spec %q", task.Description)
	}
	// The message is not a contribution of the giver: it may still answer.
	if f.s.hasPostedMessage(root.ID) {
		t.Fatal("a delegation message counts as the giver's own reply")
	}
}

func TestSeveralRecipientsInAGroupGetOneVisibleMessage(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_ids": []string{f.b.ID, f.c.ID}, "task": "Each of you checks one source"}); err != nil {
		t.Fatal(err)
	}
	msgs := f.groupMessages(f.g.ID)
	if len(msgs) != 1 || msgs[0].Kind != "" || msgs[0].Sender != f.a.ID || msgs[0].Content != "@bravo @charlie Each of you checks one source" {
		t.Fatalf("messages %+v", msgs)
	}
	if n := f.count(`SELECT COUNT(*) FROM messages WHERE kind IN ('notice','message_ref')`); n != 0 {
		t.Fatalf("%d notice lines", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM conversations WHERE user_visible=0`); n != 0 {
		t.Fatalf("%d hidden conversations", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM runs WHERE parent_run_id=? AND conversation_id=? AND kind=? AND trigger_message_id=?`, root.ID, f.g.ID, runKindGroupTask, msgs[0].ID); n != 2 {
		t.Fatalf("%d runs triggered by the message", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM work_items WHERE assigner_bot_id=? AND work_conversation_id=?`, f.a.ID, f.g.ID); n != 2 {
		t.Fatalf("%d tasks in the group", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND kind='task_result' AND status='pending'`, f.a.ID); n != 2 {
		t.Fatalf("giver holds %d waits", n)
	}
}

func TestGiverHearsOnceAfterAllVisibleParts(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_ids": []string{f.b.ID, f.c.ID}, "task": "Check your sources"}); err != nil {
		t.Fatal(err)
	}
	rows, err := f.s.db.Query(`SELECT id,bot_id FROM runs WHERE parent_run_id=? ORDER BY bot_id`, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kids []Run
	for rows.Next() {
		var id, bot string
		rows.Scan(&id, &bot)
		kids = append(kids, Run{ID: id, BotID: bot})
	}
	rows.Close()
	if len(kids) != 2 {
		t.Fatalf("%d children", len(kids))
	}
	if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, ""); err != nil {
		t.Fatalf("finish giver %v %v", ok, err)
	}
	for i, k := range kids {
		f.start(k.ID)
		if _, ok, err := f.s.FinishRun(k.ID, f.g.ID, k.BotID, "RESULT-"+k.BotID); err != nil || !ok {
			t.Fatalf("finish %d: %v %v", i, ok, err)
		}
		n := f.count(`SELECT COUNT(*) FROM runs WHERE kind=? AND bot_id=? AND conversation_id=?`, runKindFollowup, f.a.ID, f.g.ID)
		if i == 0 && n != 0 {
			t.Fatalf("giver returned after the first part (%d)", n)
		}
		if i == 1 && n != 1 {
			t.Fatalf("giver return runs after all parts: %d", n)
		}
	}
}

func TestDelegationOutsideTheGroupIsUnchanged(t *testing.T) {
	f := newTaskFix(t)
	d, _ := f.s.CreateBot("delta", "", "m")
	root := f.running(f.g.ID, f.a)
	// A recipient that is not in the group: the hidden trace stays.
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_ids": []string{f.b.ID, d.ID}, "task": "Look at this"}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM conversations WHERE user_visible=0`); n != 1 {
		t.Fatalf("%d hidden traces", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind='message_ref'`, f.g.ID); n != 1 {
		t.Fatalf("%d capsules", n)
	}
	// From a DM, the forward handoff keeps its notices.
	rd := f.running(f.a.DMConversationID, f.a)
	dm, err := f.s.GetConversation(f.a.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, f.tool(dm, rd, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "Do it"}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind='notice'`, f.a.DMConversationID); n != 1 {
		t.Fatalf("%d notices in the DM", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind='notice'`, f.c.DMConversationID); n != 1 {
		t.Fatalf("%d task anchors in the recipient DM", n)
	}
}

func TestVisibleDelegationReadsAsOwnForTheGiverAndSenderForOthers(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "Write the page"}); err != nil {
		t.Fatal(err)
	}
	var childID string
	if err := f.s.db.QueryRow(`SELECT id FROM runs WHERE bot_id=? AND parent_run_id=?`, f.b.ID, root.ID).Scan(&childID); err != nil {
		t.Fatal(err)
	}
	child, _ := f.s.GetRun(childID)
	srv := &Server{store: f.s}
	// The recipient: the brief once, in the task block, not again in the history.
	msgs, _ := srv.buildContext(f.g, child, f.b)
	brief, history := 0, 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "[current handoff task]") && strings.Contains(m.Content, "Write the page") {
			brief++
		} else if strings.Contains(m.Content, "Write the page") && strings.Contains(m.Content, "[sender") {
			history++
		}
	}
	if brief != 1 || history != 0 {
		t.Fatalf("recipient sees the brief %d times in the task block and %d in history: %#v", brief, history, msgs)
	}
	// A third member reads it as a message of alpha.
	other := f.running(f.g.ID, f.c)
	msgs, _ = srv.buildContext(f.g, other, f.c)
	foundSender := false
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "[sender alpha id="+f.a.ID+"] @bravo Write the page") {
			foundSender = true
		}
	}
	if !foundSender {
		t.Fatalf("third member context %#v", msgs)
	}
	// The giver reads it as its own assistant message.
	f2 := f.running(f.g.ID, f.a)
	msgs, _ = srv.buildContext(f.g, f2, f.a)
	own := false
	for _, m := range msgs {
		if m.Role == "assistant" && strings.Contains(m.Content, "@bravo Write the page") && !strings.Contains(m.Content, "[sender") {
			own = true
		}
	}
	if !own {
		t.Fatalf("giver context %#v", msgs)
	}
}

func TestASecondDelegationOfARunIsVisibleToo(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "First part"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "Second part"}); err != nil {
		t.Fatal(err)
	}
	msgs := f.groupMessages(f.g.ID)
	if len(msgs) != 2 || msgs[0].Content != "@bravo First part" || msgs[1].Content != "@charlie Second part" || msgs[1].Kind != "" || msgs[1].Sender != f.a.ID {
		t.Fatalf("messages %+v", msgs)
	}
	if n := f.count(`SELECT COUNT(*) FROM messages WHERE kind IN ('notice','message_ref')`); n != 0 {
		t.Fatalf("%d notice lines", n)
	}
}
