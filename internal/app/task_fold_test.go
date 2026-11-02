package app

import (
	"strings"
	"testing"
)

// A message a part holder sent is derived to its task, so the timeline can fold it under the lead's card.
func TestPartMessagesCarryTheirTaskID(t *testing.T) {
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"},
		{"bot_id": f.a.ID, "task": "Plan days", "title": "Itinerary"},
	})
	hotels := f.startPart(r.Tasks[0].TaskID, f.c)
	msg, ok, err := f.s.FinishRun(hotels.ID, f.g.ID, f.c.ID, "Three hotels.")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	leadMsg, ok, err := f.s.FinishRun(lead.ID, f.g.ID, f.b.ID, "Here is the plan.")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	history, _, err := f.s.Messages(f.g.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range history {
		got[m.ID] = m.TaskID
	}
	if got[msg.ID] != r.Tasks[0].TaskID {
		t.Fatalf("the part's result is of task %q, want %q", got[msg.ID], r.Tasks[0].TaskID)
	}
	if got[leadMsg.ID] != "" {
		t.Fatalf("the lead's own answer is not folded: %q", got[leadMsg.ID])
	}
	for _, m := range history {
		if strings.HasPrefix(m.Content, "@") && m.TaskID != "" {
			t.Fatalf("the delegation message stays visible: %+v", m)
		}
	}
	// The live event carries it too.
	events, err := f.s.Events(f.g.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The message is posted once, whole; the link comes later as a narrow event
	// the client merges, so a reaction or delivery state on the message is kept.
	posted, linked := 0, 0
	for _, ev := range events {
		data, _ := ev["data"].(map[string]any)
		if data["id"] != msg.ID {
			continue
		}
		switch ev["type"] {
		case "message":
			posted++
			if data["content"] != "Three hotels." {
				t.Fatalf("the posted message lost its words: %v", data)
			}
		case "message_task":
			linked++
			if len(data) != 2 || data["task_id"] != r.Tasks[0].TaskID {
				t.Fatalf("the link event is only {id, task_id}: %v", data)
			}
		}
	}
	if posted != 1 || linked != 1 {
		t.Fatalf("the message is sent once (%d) and linked once (%d)", posted, linked)
	}
}
