package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageRunsSummarizesStatusFailuresAndToolTimeouts(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Health Bot", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	conv := bot.DMConversationID
	finish := func(i int, status, msg string) string {
		t.Helper()
		_, runs, _, err := s.AddUserRuns(conv, "go", fmt.Sprintf("health-%d", i), []runSpec{{BotID: bot.ID, Model: bot.Model}}, i > 0)
		if err != nil || len(runs) != 1 {
			t.Fatalf("runs=%v err=%v", runs, err)
		}
		if _, err = s.db.Exec(`UPDATE runs SET status=?,error=? WHERE id=?`, status, nullString(msg), runs[0].ID); err != nil {
			t.Fatal(err)
		}
		return runs[0].ID
	}
	const upstream = "api error (http 401) invalid_api_key sk-upstream-secret-body"
	finish(0, "done", "")
	finish(1, "done", "")
	finish(2, "failed", upstream)
	finish(3, "failed", upstream)
	finish(4, "failed", "stream read error")
	finish(5, "cancelled", "stopped by user")
	old := finish(6, "failed", "maximum agent steps")
	if _, err = s.db.Exec(`UPDATE runs SET updated_at='2020-01-01T00:00:00.000000000Z' WHERE id=?`, old); err != nil {
		t.Fatal(err)
	}
	run := finish(7, "done", "")
	tool := func(call, status, result, started string) {
		t.Helper()
		if _, err = s.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			conv, bot.ID, run, call, "shell_exec", "{}", result, status, started, started); err != nil {
			t.Fatal(err)
		}
	}
	recent := now()
	tool("ok", "completed", "fine", recent)
	tool("slow", "failed", "[Timed out after 30000ms]\npartial", recent)
	tool("killed", "failed", "out\n[Exit 137: Killed (SIGKILL, possibly OOM)]", recent)
	tool("plain", "failed", "[Exit code: 2]", recent)
	tool("ancient", "failed", "[Timed out after 30000ms]", "2020-01-01T00:00:00.000000000Z")

	w := httptest.NewRecorder()
	if !(&Server{store: s}).routeUsageRuns(w, httptest.NewRequest(http.MethodGet, "/api/usage/runs?days=7", nil), "usage/runs") || w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var got UsageRunHealth
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Days != 7 || got.Runs != 7 || got.ByStatus["done"] != 3 || got.ByStatus["failed"] != 3 || got.ByStatus["cancelled"] != 1 {
		t.Fatalf("status counts wrong: %+v", got)
	}
	if len(got.Failures) != 2 || got.Failures[0] != (UsageRunFailure{"model_auth_invalid", 2}) || got.Failures[1] != (UsageRunFailure{"connection_interrupted", 1}) {
		t.Fatalf("failures=%+v", got.Failures)
	}
	if got.ToolTimeouts != 2 || got.ToolCalls != 4 {
		t.Fatalf("tools: timeouts=%d calls=%d", got.ToolTimeouts, got.ToolCalls)
	}
	if strings.Contains(w.Body.String(), "sk-upstream") || strings.Contains(w.Body.String(), "api error") {
		t.Fatalf("upstream text leaked: %s", w.Body.String())
	}

	for _, q := range []string{"days=0", "days=31", "days=x"} {
		w = httptest.NewRecorder()
		(&Server{store: s}).routeUsageRuns(w, httptest.NewRequest(http.MethodGet, "/api/usage/runs?"+q, nil), "usage/runs")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s code=%d", q, w.Code)
		}
	}
	w = httptest.NewRecorder()
	(&Server{store: s}).routeUsageRuns(w, httptest.NewRequest(http.MethodPost, "/api/usage/runs", nil), "usage/runs")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post code=%d", w.Code)
	}
}

func TestHealthReportsVersionAndUsageRunsNeedsOwner(t *testing.T) {
	s, _ := ownerTestServer(t)
	old := Version
	Version = "v9.9.9-test"
	defer func() { Version = old }()
	w := ownerCall(s, "GET", "/health", nil, nil, false)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || body["version"] != "v9.9.9-test" {
		t.Fatalf("health code=%d body=%s", w.Code, w.Body.String())
	}
	if w = ownerCall(s, "GET", "/api/usage/runs", nil, nil, true); w.Code != 401 {
		t.Fatalf("unauthenticated usage/runs: %d", w.Code)
	}
}
