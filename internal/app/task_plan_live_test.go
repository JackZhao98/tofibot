package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// The live shape (v0.6.2-rc.7): the person @-mentions one Bot, the run holds no
// task, and one delegate call gives two parts. The run gets a root task, the
// parts are its checklist, the history carries task_id on the two result
// messages (and only on those), and the root task closes with the lead's answer.
func TestALeadWithNoTaskDelegatingTwoPartsFoldsLikeAPlan(t *testing.T) {
	f := newTaskFix(t)
	lead := f.running(f.g.ID, f.b)
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Flights", "title": "Flights"},
		{"bot_id": f.a.ID, "task": "Lodging", "title": "Lodging"},
	})
	var rootID string
	if err := f.s.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, lead.ID).Scan(&rootID); err != nil {
		t.Fatalf("no root task: %v", err)
	}
	if items := f.planItems(rootID); len(items) != 2 {
		t.Fatalf("plan items %+v", items)
	}
	flights := f.startPart(r.Tasks[0].TaskID, f.c)
	lodging := f.startPart(r.Tasks[1].TaskID, f.a)
	// The lead ends its turn pending, the parts report, the wait group wakes the lead.
	if _, ok, err := f.s.FinishRun(lead.ID, f.g.ID, f.b.ID, ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	m1, _, err := f.s.FinishRun(flights.ID, f.g.ID, f.c.ID, "Flights found.")
	if err != nil {
		t.Fatal(err)
	}
	m2, _, err := f.s.FinishRun(lodging.ID, f.g.ID, f.a.ID, "Lodging found.")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(rootID); got.Status != "in_progress" {
		t.Fatalf("the plan stays open until the lead answers: %s", got.Status)
	}
	f.srv.flushInbox()
	woken := f.wokenRuns(f.b)
	if len(woken) != 1 {
		t.Fatalf("the lead wakes once: %d", len(woken))
	}
	f.start(woken[0].ID)
	final, _, err := f.s.FinishRun(woken[0].ID, f.g.ID, f.b.ID, "Here is the plan.")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(rootID); got.Status != "done" {
		t.Fatalf("the root task closes with the answer: %s", got.Status)
	}
	// History as the API returns it.
	history, _, err := f.s.Messages(f.g.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(history)
	var rows []map[string]any
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	order := map[string]int{}
	taskOf := map[string]any{}
	assignment := ""
	for i, row := range rows {
		id := row["id"].(string)
		order[id] = i
		taskOf[id] = row["task_id"]
		if content, _ := row["content"].(string); strings.HasPrefix(content, "@") {
			assignment = id
			if row["task_id"] != nil {
				t.Fatalf("an assignment never folds: %v", row)
			}
		}
		if row["role"] == "user" && row["task_id"] != nil {
			t.Fatalf("a request never folds: %v", row)
		}
	}
	if taskOf[m1.ID] != r.Tasks[0].TaskID || taskOf[m2.ID] != r.Tasks[1].TaskID {
		t.Fatalf("the result messages carry their part: %v %v", taskOf[m1.ID], taskOf[m2.ID])
	}
	if taskOf[final.ID] != nil {
		t.Fatalf("the lead's answer is not folded: %v", taskOf[final.ID])
	}
	// The assignment comes before the results in the timeline.
	if assignment == "" || order[assignment] > order[m1.ID] || order[assignment] > order[m2.ID] {
		t.Fatalf("assignment %q must come before the results", assignment)
	}
	// The stream carries it too.
	events, err := f.s.Events(f.g.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ev := range events {
		data, _ := ev["data"].(map[string]any)
		if ev["type"] == "message" && (data["id"] == m1.ID || data["id"] == m2.ID) && data["task_id"] != nil {
			seen[data["id"].(string)] = true
		}
	}
	if !seen[m1.ID] || !seen[m2.ID] {
		t.Fatalf("stream events without task_id: %v", seen)
	}
}
