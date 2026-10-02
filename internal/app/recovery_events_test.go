package app

import (
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestOpenStoreRecoveryReplaysInterruptedRunAndTool(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("recovery", "", "model")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	running, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(running.ID, "running", ""); err != nil || !ok {
		s.Close()
		t.Fatalf("set running: %v %v", ok, err)
	}
	if _, err = s.BeginStream(running); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err = s.RecordToolEvent(c.ID, b.ID, running.ID, runtime.ToolEvent{CallID: "call/recovery", Name: "search", Status: "queued"}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err = s.RecordToolEvent(c.ID, b.ID, running.ID, runtime.ToolEvent{CallID: "call/recovery", Name: "search", Status: "running"}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	queued, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	cursor := s.EventCursor(c.ID)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gotRun, err := s.GetRun(running.ID)
	if err != nil || gotRun.Status != "interrupted" {
		t.Fatalf("recovered run=%+v err=%v", gotRun, err)
	}
	gotDraft, err := s.StreamDraft(running.ID)
	if err != nil || gotDraft.Status == streamDraftActive {
		t.Fatalf("recovered draft=%+v err=%v", gotDraft, err)
	}
	activities, err := s.ToolActivities(c.ID, 20)
	if err != nil || len(activities) != 1 || activities[0].Status != "interrupted" {
		t.Fatalf("recovered activities=%+v err=%v", activities, err)
	}
	gotQueued, err := s.GetRun(queued.ID)
	if err != nil || gotQueued.Status != "queued" {
		t.Fatalf("queued run=%+v err=%v", gotQueued, err)
	}

	replayed, err := s.Events(c.ID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 {
		t.Fatalf("recovery events=%+v, want run and tool", replayed)
	}
	if replayed[0]["type"] != "run" || replayed[1]["type"] != "tool" {
		t.Fatalf("recovery event types=%q,%q", replayed[0]["type"], replayed[1]["type"])
	}
	if replayed[0]["data"].(map[string]any)["status"] != "interrupted" || replayed[1]["data"].(map[string]any)["status"] != "interrupted" {
		t.Fatalf("recovery event data=%+v", replayed)
	}

	secondCursor := s.EventCursor(c.ID)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	again, err := s.Events(c.ID, secondCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("recovery repeated events=%+v", again)
	}
}

func TestRecoverInterruptedRunsRollsBackStateWhenToolEventFails(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	b, err := s.CreateBot("recovery rollback", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(running.ID, "running", ""); err != nil || !ok {
		t.Fatalf("set running: %v %v", ok, err)
	}
	if _, err = s.BeginStream(running); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordToolEvent(c.ID, b.ID, running.ID, runtime.ToolEvent{CallID: "call/rollback", Name: "search", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordToolEvent(c.ID, b.ID, running.ID, runtime.ToolEvent{CallID: "call/rollback", Name: "search", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	queued, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordToolEvent(c.ID, b.ID, queued.ID, runtime.ToolEvent{CallID: "call/queued", Name: "search", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	cursor := s.EventCursor(c.ID)

	_, err = s.db.Exec(`CREATE TRIGGER fail_recovery_tool_event
BEFORE INSERT ON events
WHEN NEW.type='tool'
BEGIN
  SELECT RAISE(ABORT, 'recovery tool event rejected');
END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = recoverInterruptedRuns(s.db); err == nil {
		t.Fatal("recovery unexpectedly succeeded")
	}

	gotRunning, err := s.GetRun(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRunning.Status != "running" {
		t.Fatalf("run status after rollback=%q, want running", gotRunning.Status)
	}
	draft, err := s.StreamDraft(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != streamDraftActive {
		t.Fatalf("draft status after rollback=%q, want active", draft.Status)
	}
	var runningToolStatus string
	if err = s.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND call_id=?`, running.ID, "call/rollback").Scan(&runningToolStatus); err != nil {
		t.Fatal(err)
	}
	if runningToolStatus != "running" {
		t.Fatalf("tool status after rollback=%q, want running", runningToolStatus)
	}
	gotQueued, err := s.GetRun(queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotQueued.Status != "queued" {
		t.Fatalf("queued run status after rollback=%q, want queued", gotQueued.Status)
	}
	var queuedToolStatus string
	if err = s.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND call_id=?`, queued.ID, "call/queued").Scan(&queuedToolStatus); err != nil {
		t.Fatal(err)
	}
	if queuedToolStatus != "queued" {
		t.Fatalf("queued tool status after rollback=%q, want queued", queuedToolStatus)
	}
	if got := s.EventCursor(c.ID); got != cursor {
		t.Fatalf("event cursor after rollback=%d, want %d", got, cursor)
	}

	if _, err = s.db.Exec(`DROP TRIGGER fail_recovery_tool_event`); err != nil {
		t.Fatal(err)
	}
	if err = recoverInterruptedRuns(s.db); err != nil {
		t.Fatal(err)
	}
	gotRunning, err = s.GetRun(running.ID)
	if err != nil || gotRunning.Status != "interrupted" {
		t.Fatalf("recovered run=%+v err=%v", gotRunning, err)
	}
	activities, err := s.ToolActivities(c.ID, 20)
	if err != nil || len(activities) != 2 {
		t.Fatalf("recovered activities=%+v err=%v", activities, err)
	}
	for _, activity := range activities {
		if activity.RunID == running.ID && activity.Status != "interrupted" {
			t.Fatalf("recovered running tool=%+v", activity)
		}
		if activity.RunID == queued.ID && activity.Status != "queued" {
			t.Fatalf("queued tool changed=%+v", activity)
		}
	}
	replayed, err := s.Events(c.ID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 || replayed[0]["type"] != "run" || replayed[1]["type"] != "tool" {
		t.Fatalf("recovery events=%+v, want run and tool", replayed)
	}
}
