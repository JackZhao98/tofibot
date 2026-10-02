package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestToolActivityLifecycleAndEventTransaction(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = migrateToolActivity(s.db); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("bot", "instructions", "model")
	if err != nil {
		t.Fatal(err)
	}
	events := []runtime.ToolEvent{
		{CallID: "call/a", Name: "bash", Arguments: `{"command":"pwd"}`, Status: "queued"},
		{CallID: "call/a", Name: "bash", Status: "running"},
		{CallID: "call/a", Name: "bash", Result: "output", Status: "completed"},
	}
	for _, event := range events {
		if err = s.RecordToolEvent(b.DMConversationID, b.ID, "run/a", event); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.RecordToolEvent(b.DMConversationID, b.ID, "run/a", events[0]); err == nil {
		t.Fatal("duplicate queued event was accepted")
	}
	activities, err := s.ToolActivities(b.DMConversationID, 500)
	if err != nil || len(activities) != 1 {
		t.Fatalf("activities=%+v err=%v", activities, err)
	}
	if activities[0].Status != "completed" || activities[0].Result != "output" || activities[0].CallID != "call/a" {
		t.Fatalf("activity=%+v", activities[0])
	}
	eventsInStore, err := s.Events(b.DMConversationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsInStore) != 3 || eventsInStore[0]["type"] != "tool" {
		t.Fatalf("stored events=%+v", eventsInStore)
	}
}

func TestToolActivityBoundsAndRestartInterrupt(t *testing.T) {
	d := t.TempDir()
	s, err := OpenStore(d)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.CreateBot("bot", "", "model")
	if err = migrateToolActivity(s.db); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", maxToolActivityResult+100)
	if err = s.RecordToolEvent(b.DMConversationID, b.ID, "run/b", runtime.ToolEvent{CallID: "call/b", Name: "bash", Arguments: `{}`, Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordToolEvent(b.DMConversationID, b.ID, "run/b", runtime.ToolEvent{CallID: "call/b", Name: "bash", Result: long, Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	activities, _ := s.ToolActivities(b.DMConversationID, 1)
	if !activities[0].Truncated || len([]rune(activities[0].Result)) > maxToolActivityResult+1 {
		t.Fatalf("bounded activity=%+v", activities[0])
	}
	if err = s.RecordToolEvent(b.DMConversationID, b.ID, "run/c", runtime.ToolEvent{CallID: "call/c", Name: "bash", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(d)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = migrateToolActivity(s.db); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ToolActivities(b.DMConversationID, 10)
	for _, activity := range got {
		if activity.CallID == "call/c" && activity.Status != "interrupted" {
			t.Fatalf("stale activity=%+v", activity)
		}
	}
}

func TestRouteToolActivities(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "", "model")
	if err = migrateToolActivity(s.store.db); err != nil {
		t.Fatal(err)
	}
	if err = s.store.RecordToolEvent(b.DMConversationID, b.ID, "run", runtime.ToolEvent{CallID: "call", Name: "info", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/tools?limit=2", nil)
	res := httptest.NewRecorder()
	if !s.routeToolActivities(res, req, "conversations/"+b.DMConversationID+"/tools") {
		t.Fatal("route did not claim tools path")
	}
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"activities"`) {
		t.Fatalf("response=%d %s", res.Code, res.Body.String())
	}
}

func TestRouteToolActivityRunSummaryAndDetailPages(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("bot", "", "model")
	if err = migrateToolActivity(s.store.db); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		status := "completed"
		if i == 1 {
			status = "failed"
		} else if i == 2 {
			status = "interrupted"
		}
		if _, err = s.store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, b.DMConversationID, b.ID, "historic", fmt.Sprintf("call/%02d", i), "fixture", fmt.Sprintf("arg-%d", i), "result", status, fmt.Sprintf("2026-09-27T00:00:%02dZ", i), fmt.Sprintf("2026-09-27T00:01:%02dZ", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < defaultToolActivityLimit; i++ {
		if _, err = s.store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, b.DMConversationID, b.ID, "newer", fmt.Sprintf("call/%03d", i), "fixture", "completed", fmt.Sprintf("2026-09-28T00:%02d:%02dZ", i/60, i%60), "2026-09-28T01:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := s.store.ToolActivities(b.DMConversationID, 0)
	if err != nil || len(recent) != defaultToolActivityLimit {
		t.Fatalf("recent=%d err=%v", len(recent), err)
	}
	for _, activity := range recent {
		if activity.RunID == "historic" {
			t.Fatalf("old run leaked into bounded recent window: %+v", activity)
		}
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/tools?run_ids=historic", nil)
	summaryRes := httptest.NewRecorder()
	s.routeToolActivities(summaryRes, summaryReq, "conversations/"+b.DMConversationID+"/tools")
	var summaryBody struct {
		Summaries []ToolActivityRunSummary `json:"summaries"`
	}
	if summaryRes.Code != http.StatusOK || strings.Contains(summaryRes.Body.String(), "arg-0") || json.Unmarshal(summaryRes.Body.Bytes(), &summaryBody) != nil || len(summaryBody.Summaries) != 1 {
		t.Fatalf("summary response=%d %s", summaryRes.Code, summaryRes.Body.String())
	}
	summary := summaryBody.Summaries[0]
	if summary.RunID != "historic" || summary.BotID != b.ID || summary.ToolCount != 15 || summary.CompletedCount != 13 || summary.FailedCount != 1 || summary.InterruptedCount != 1 || summary.PendingCount != 0 || summary.StartedAt == "" || summary.UpdatedAt == "" {
		t.Fatalf("summary=%+v", summary)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/tools?run_id=historic&limit=2", nil)
	detailRes := httptest.NewRecorder()
	s.routeToolActivities(detailRes, detailReq, "conversations/"+b.DMConversationID+"/tools")
	var detailBody struct {
		Activities []ToolActivity `json:"activities"`
		ToolCount  int            `json:"tool_count"`
		HasMore    bool           `json:"has_more"`
	}
	if detailRes.Code != http.StatusOK || json.Unmarshal(detailRes.Body.Bytes(), &detailBody) != nil || len(detailBody.Activities) != 2 || detailBody.ToolCount != 15 || !detailBody.HasMore || detailBody.Activities[0].Arguments != "arg-0" {
		t.Fatalf("detail response=%d %+v", detailRes.Code, detailBody)
	}
	emptyDetailReq := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/tools?run_id=historic&offset=999", nil)
	emptyDetailRes := httptest.NewRecorder()
	s.routeToolActivities(emptyDetailRes, emptyDetailReq, "conversations/"+b.DMConversationID+"/tools")
	if emptyDetailRes.Code != http.StatusOK || json.Unmarshal(emptyDetailRes.Body.Bytes(), &detailBody) != nil || len(detailBody.Activities) != 0 || detailBody.HasMore || detailBody.ToolCount != 15 {
		t.Fatalf("empty detail response=%d %+v", emptyDetailRes.Code, detailBody)
	}

	invalidReq := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/tools?run_ids=historic,historic", nil)
	invalidRes := httptest.NewRecorder()
	s.routeToolActivities(invalidRes, invalidReq, "conversations/"+b.DMConversationID+"/tools")
	if invalidRes.Code != http.StatusBadRequest {
		t.Fatalf("duplicate run ids accepted: %d %s", invalidRes.Code, invalidRes.Body.String())
	}
	tooDeepReq := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/tools?run_id=historic&offset=1001", nil)
	tooDeepRes := httptest.NewRecorder()
	s.routeToolActivities(tooDeepRes, tooDeepReq, "conversations/"+b.DMConversationID+"/tools")
	if tooDeepRes.Code != http.StatusBadRequest {
		t.Fatalf("unbounded detail offset accepted: %d %s", tooDeepRes.Code, tooDeepRes.Body.String())
	}
}
