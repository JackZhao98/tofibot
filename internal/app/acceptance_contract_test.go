package app

import (
	"testing"
	"time"
)

func TestFinishForwardResultIsAtomicAcrossDMs(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source, _ := s.CreateBot("source", "", "source-model")
	target, _ := s.CreateBot("target", "", "target-model")
	sourceConv, _ := s.GetConversation(source.DMConversationID)
	targetConv, _ := s.GetConversation(target.DMConversationID)
	parent, err := s.AddRun(sourceConv.ID, source.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, run, err := s.AddForwardHandoff(sourceConv.ID, source.ID, target.ID, parent.ID, "fixture task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	trigger := `CREATE TRIGGER fail_forward_result BEFORE INSERT ON messages WHEN NEW.conversation_id='` + sourceConv.ID + `' AND NEW.kind='forward_result' BEGIN SELECT RAISE(ABORT,'forward result rejected'); END`
	if _, err = s.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if _, finished, err := s.FinishRun(run.ID, targetConv.ID, target.ID, "finished target"); err == nil || finished {
		t.Fatalf("FinishRun trigger result finished=%v err=%v", finished, err)
	}
	var status string
	if err = s.db.QueryRow(`SELECT status FROM runs WHERE id=?`, run.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("target run was not rolled back: %s", status)
	}
	var targetMessages, sourceMessages int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind<>'notice'`, targetConv.ID).Scan(&targetMessages); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind<>'notice'`, sourceConv.ID).Scan(&sourceMessages); err != nil {
		t.Fatal(err)
	}
	if targetMessages != 0 || sourceMessages != 0 {
		t.Fatalf("partial FinishRun write target=%d source=%d", targetMessages, sourceMessages)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_forward_result`); err != nil {
		t.Fatal(err)
	}
	if _, finished, err := s.FinishRun(run.ID, targetConv.ID, target.ID, "finished target"); err != nil || !finished {
		t.Fatalf("successful FinishRun finished=%v err=%v", finished, err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind<>'notice'`, targetConv.ID).Scan(&targetMessages); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind='forward_result'`, sourceConv.ID).Scan(&sourceMessages); err != nil {
		t.Fatal(err)
	}
	if targetMessages != 1 || sourceMessages != 1 {
		t.Fatalf("successful FinishRun writes target=%d source=%d", targetMessages, sourceMessages)
	}
}

func TestParseRunAtAcceptsDatetimeLocalInConfiguredTimezone(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseRunAt("2026-09-17T09:30", loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 17, 16, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("datetime-local parsed as %s, want %s", got.UTC(), want)
	}
}

func TestClaimOnceEmitsQueuedScheduleSnapshot(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	defer s.Close()
	x, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "once", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE schedules SET next_at_utc=? WHERE id=?`, scheduleTime(time.Now().Add(-time.Minute)), x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimDueSchedules(time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetSchedule(x.ID)
	if err != nil || stored.Status != scheduleActive || stored.ExecutionStatus != "queued" {
		t.Fatalf("schedule=%+v err=%v", stored, err)
	}
	all, err := s.Events(c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var queued bool
	for _, event := range all {
		if event["type"] != "schedule" {
			continue
		}
		data, ok := event["data"].(map[string]any)
		if ok && data["id"] == x.ID && data["status"] == scheduleActive {
			queued = true
		}
	}
	if !queued {
		t.Fatalf("missing queued schedule event: %+v", all)
	}
}

func TestToolSchemasUseJSONSchemaArraysAndObjects(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.CreateBot("bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	tools := (&Server{store: s}).tools(c, run)
	wanted := map[string]bool{"list_bots": false}
	for _, tool := range tools {
		if tool.Parameters == nil {
			t.Fatalf("tool %q has nil parameters", tool.Name)
		}
		properties, ok := tool.Parameters["properties"].(map[string]any)
		if !ok || properties == nil {
			t.Fatalf("tool %q properties=%#v, want object", tool.Name, tool.Parameters["properties"])
		}
		required, ok := tool.Parameters["required"]
		if !ok || required == nil {
			t.Fatalf("tool %q required=%#v, want JSON array", tool.Name, required)
		}
		switch required.(type) {
		case []string, []any:
		default:
			t.Fatalf("tool %q required has type %T, want array", tool.Name, required)
		}
		if _, ok := wanted[tool.Name]; ok {
			wanted[tool.Name] = true
			if len(properties) != 0 {
				t.Fatalf("zero-argument tool %q has properties=%#v", tool.Name, properties)
			}
		}
	}
	for name, found := range wanted {
		if !found {
			t.Fatalf("final server tool surface omitted %q", name)
		}
	}
}
