package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func occurrenceFixture(t *testing.T, s *Store, bot Bot, c Conversation, kind string) (Schedule, Run) {
	t.Helper()
	spec := ScheduleSpec{Content: "synthetic private task", Kind: kind, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"}
	if kind == scheduleInterval {
		spec.IntervalSeconds = 60
	}
	x, err := s.CreateSchedule(c.ID, bot.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim=%+v err=%v", runs, err)
	}
	return x, runs[0]
}

func occurrenceCall(s *Server, method, conversationID, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/conversations/"+conversationID+"/schedule-occurrences", nil)
	r.URL.RawQuery = query
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func readOccurrences(t *testing.T, server *Server, conversationID string, ids ...string) []ScheduleOccurrence {
	t.Helper()
	w := occurrenceCall(server, http.MethodGet, conversationID, "root_run_ids="+strings.Join(ids, ","))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%d %s cache=%s", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
	var envelope struct {
		Occurrences []ScheduleOccurrence `json:"occurrences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Occurrences == nil {
		t.Fatal("occurrences must be an array, never null")
	}
	var raw struct {
		Occurrences []map[string]json.RawMessage `json:"occurrences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, fields := range raw.Occurrences {
		allowed := map[string]bool{"root_run_id": true, "schedule_id": true, "scheduled_for_utc": true, "title": true, "created_by": true, "kind": true, "timezone": true, "interval_seconds": true, "daily_time": true, "occurrence_number": true, "execution_status": true, "status_run_id": true, "status_error": true, "result_in_conversation": true}
		for key := range fields {
			if !allowed[key] {
				t.Fatalf("unexpected DTO field %s: %s", key, w.Body.String())
			}
		}
		for _, key := range []string{"root_run_id", "schedule_id", "scheduled_for_utc", "execution_status", "status_run_id", "result_in_conversation"} {
			if _, ok := fields[key]; !ok {
				t.Fatalf("missing DTO field %s", key)
			}
		}
	}
	if strings.Contains(w.Body.String(), "synthetic private task") || strings.Contains(w.Body.String(), "synthetic private delegation") {
		t.Fatalf("private payload leaked: %s", w.Body.String())
	}
	return envelope.Occurrences
}

func TestScheduleOccurrenceResultRequiresReceiptAndFinalReply(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginStream(root); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PublishAssistantTurn(context.Background(), root.ID, 1, "I will check the sources."); err != nil || !ok {
		t.Fatalf("intermediate turn: ok=%v err=%v", ok, err)
	}
	for _, status := range []string{"queued", "running", "completed"} {
		if err := s.RecordToolEvent(c.ID, bot.ID, root.ID, runtime.ToolEvent{CallID: "receipt", Name: "complete_scheduled_task", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readOccurrences(t, &Server{store: s}, c.ID, root.ID); len(got) != 1 || got[0].ResultInConversation {
		t.Fatalf("receipt and intermediate turn cannot certify final reply: %+v", got)
	}
	if _, published, err := s.FinishRun(root.ID, c.ID, bot.ID, "Verified final result."); err != nil || !published {
		t.Fatalf("finish: published=%v err=%v", published, err)
	}
	if got := readOccurrences(t, &Server{store: s}, c.ID, root.ID); len(got) != 1 || got[0].ExecutionStatus != "done" || !got[0].ResultInConversation {
		t.Fatalf("final result not reported: %+v", got)
	}
	_, withoutReceipt := occurrenceFixture(t, s, bot, c, scheduleOnce)
	if _, err := s.SetRunStatus(withoutReceipt.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, published, err := s.FinishRun(withoutReceipt.ID, c.ID, bot.ID, "Unconfirmed final text."); err != nil || !published {
		t.Fatalf("finish without receipt: published=%v err=%v", published, err)
	}
	if got := readOccurrences(t, &Server{store: s}, c.ID, withoutReceipt.ID); len(got) != 1 || got[0].ResultInConversation {
		t.Fatalf("unconfirmed text cannot certify scheduled result: %+v", got)
	}
}

func TestScheduleOccurrenceDoesNotCountLegacySenderPrefixAsDeliveredResult(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	for _, content := range []string{"[sender " + bot.Name + " id=" + bot.ID + "] \n", " \n\t "} {
		_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
		if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{"queued", "running", "completed"} {
			if err := s.RecordToolEvent(c.ID, bot.ID, root.ID, runtime.ToolEvent{CallID: "legacy-receipt", Name: "complete_scheduled_task", Status: status}); err != nil {
				t.Fatal(err)
			}
		}
		// Old versions could persist internal markers or whitespace as final
		// messages. Neither contains a user-facing result despite the receipt.
		if _, published, err := s.FinishRun(root.ID, c.ID, bot.ID, content); err != nil || !published {
			t.Fatalf("legacy finish: published=%v err=%v", published, err)
		}
		if got := readOccurrences(t, &Server{store: s}, c.ID, root.ID); len(got) != 1 || got[0].ExecutionStatus != "done" || got[0].ResultInConversation {
			t.Fatalf("internal marker or whitespace is not a delivered result: %+v", got)
		}
	}
}

func TestScheduleOccurrenceHiddenDelegateResultIsNotUserReply(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	delegate, err := s.CreateBot("hidden delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddForwardHandoff(c.ID, bot.ID, delegate.ID, root.ID, "private request")
	if err != nil {
		t.Fatal(err)
	}
	if child.ConversationID == c.ID {
		t.Fatal("delegate must run in a hidden conversation")
	}
	if _, err := s.SetRunStatus(root.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(child.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running", "completed"} {
		if err := s.RecordToolEvent(child.ConversationID, delegate.ID, child.ID, runtime.ToolEvent{CallID: "private-receipt", Name: "complete_scheduled_task", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	if _, published, err := s.FinishRun(child.ID, child.ConversationID, delegate.ID, "Private result."); err != nil || !published {
		t.Fatalf("private finish: published=%v err=%v", published, err)
	}
	if got := readOccurrences(t, &Server{store: s}, c.ID, root.ID); len(got) != 1 || got[0].ExecutionStatus != "queued" || got[0].ResultInConversation {
		t.Fatalf("hidden result incorrectly reported as user-visible: %+v", got)
	}
}

func TestScheduleOccurrencesHistoricalRoots(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	x, first := occurrenceFixture(t, s, bot, c, scheduleInterval)
	if _, err := s.SetRunStatus(first.ID, "failed", "synthetic private error"); err != nil {
		t.Fatal(err)
	}
	secondTime := time.Now().Add(-time.Second)
	makeDue(t, s, x.ID, secondTime)
	runs, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(runs) != 1 {
		t.Fatalf("second claim=%+v err=%v", runs, err)
	}
	second := runs[0]
	if _, err := s.SetRunStatus(second.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	got := readOccurrences(t, &Server{store: s}, c.ID, second.ID, first.ID, second.ID)
	if len(got) != 2 || got[0].RootRunID != second.ID || got[0].ExecutionStatus != "running" || got[0].StatusRunID != second.ID || got[0].ScheduledForUTC != scheduleTime(secondTime) || got[1].RootRunID != first.ID || got[1].ExecutionStatus != "failed" || got[1].StatusRunID != first.ID || got[0].ScheduleID != x.ID || got[1].ScheduleID != x.ID || got[0].StatusError != "" || got[1].StatusError != "synthetic private error" {
		t.Fatalf("historical outcomes=%+v", got)
	}
	latest, err := s.GetSchedule(x.ID)
	if err != nil || latest.ExecutionStatus != got[0].ExecutionStatus || latest.LastRunID != got[0].StatusRunID {
		t.Fatalf("latest and exact disagree: %+v err=%v", latest, err)
	}
}

func TestScheduleOccurrenceKeepsTitleCreatorFrequencyAndSequence(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	spec := ScheduleSpec{Title: "Daily summary", CreatedBy: "user", Content: "synthetic private task", Kind: scheduleInterval, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), IntervalSeconds: 60, Timezone: "UTC"}
	x, err := s.CreateSchedule(c.ID, bot.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Minute))
	first, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%v err=%v", first, err)
	}
	if _, err := s.SetRunStatus(first[0].ID, "failed", "fixture"); err != nil {
		t.Fatal(err)
	}
	makeDue(t, s, x.ID, time.Now().Add(-time.Second))
	second, err := s.ClaimDueSchedules(time.Now())
	if err != nil || len(second) != 1 {
		t.Fatalf("second claim=%v err=%v", second, err)
	}
	if _, err := s.db.Exec(`UPDATE schedules SET title='Changed later',kind='daily',daily_time='11:30',interval_seconds=0 WHERE id=?`, x.ID); err != nil {
		t.Fatal(err)
	}
	got := readOccurrences(t, &Server{store: s}, c.ID, first[0].ID, second[0].ID)
	if len(got) != 2 {
		t.Fatalf("occurrences=%+v", got)
	}
	for i, occurrence := range got {
		if occurrence.Title != "Daily summary" || occurrence.CreatedBy != "user" || occurrence.Kind != scheduleInterval || occurrence.IntervalSeconds != 60 || occurrence.Timezone != "UTC" || occurrence.OccurrenceNumber != int64(i+1) {
			t.Fatalf("snapshot changed: %+v", occurrence)
		}
	}
}

func TestScheduleOccurrencesCrossConversationDelegate(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	delegate, err := s.CreateBot("fixture delegate", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(root.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddForwardHandoff(c.ID, bot.ID, delegate.ID, root.ID, "synthetic private delegation")
	if err != nil {
		t.Fatal(err)
	}
	if child.ConversationID == c.ID {
		t.Fatal("fixture must delegate across conversations")
	}
	if _, err := s.SetRunStatus(root.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running"} {
		if status == "running" {
			if _, err := s.SetRunStatus(child.ID, status, ""); err != nil {
				t.Fatal(err)
			}
		}
		got := readOccurrences(t, &Server{store: s}, c.ID, root.ID)
		if len(got) != 1 || got[0].ExecutionStatus != status || got[0].StatusRunID != child.ID {
			t.Fatalf("delegate outcome=%+v", got)
		}
		latest, err := s.GetSchedule(x.ID)
		if err != nil || latest.ExecutionStatus != status || latest.LastRunID != child.ID {
			t.Fatalf("latest=%+v err=%v", latest, err)
		}
	}
	const detail = "Delegate could not verify the result.\nUNCONFIRMED: <not markup>"
	if _, err := s.SetRunStatus(child.ID, "failed", detail); err != nil {
		t.Fatal(err)
	}
	got := readOccurrences(t, &Server{store: s}, c.ID, root.ID)
	if len(got) != 1 || got[0].ExecutionStatus != "failed" || got[0].StatusRunID != child.ID || got[0].StatusError != detail {
		t.Fatalf("delegated failure detail=%+v", got)
	}
}

func TestScheduleOccurrencesRetriesAndIndependentFailure(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	if _, err := s.SetRunStatus(root.ID, "failed", "synthetic private failure"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryRun(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(retry.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	got := readOccurrences(t, &Server{store: s}, c.ID, root.ID)
	if len(got) != 1 || got[0].ExecutionStatus != "done" || got[0].StatusRunID != retry.ID || got[0].StatusError != "" {
		t.Fatalf("retry outcome=%+v", got)
	}
	branch, err := s.AddRun(c.ID, bot.ID, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRunStatus(branch.ID, "failed", "synthetic private independent failure"); err != nil {
		t.Fatal(err)
	}
	got = readOccurrences(t, &Server{store: s}, c.ID, root.ID)
	if len(got) != 1 || got[0].ExecutionStatus != "failed" || got[0].StatusRunID != branch.ID || got[0].StatusError != "synthetic private independent failure" {
		t.Fatalf("independent failure hidden: %+v", got)
	}
	latest, err := s.GetSchedule(x.ID)
	if err != nil || latest.ExecutionStatus != "failed" || latest.LastRunID != branch.ID {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}

func TestScheduleOccurrencesFailureDetailBounds(t *testing.T) {
	for _, tc := range []struct{ name, status, detail string }{
		{"empty", "failed", ""},
		{"interrupted", "interrupted", "The worker stopped before publishing a result."},
		{"unicode-boundary", "failed", strings.Repeat("猫", maxOccurrenceError)},
		{"unicode-truncated", "failed", strings.Repeat("猫🙂", 3000)},
		{"running-stale-error", "running", "Must not appear as a current failure"},
		{"done-stale-error", "done", "Must not appear after success"},
		{"cancelled", "cancelled", "Not a failure diagnostic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bot, c := scheduleTestStore(t)
			defer s.Close()
			_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
			if changed, err := s.SetRunStatus(root.ID, tc.status, tc.detail); err != nil || !changed {
				t.Fatalf("status update=%v err=%v", changed, err)
			}
			got := readOccurrences(t, &Server{store: s}, c.ID, root.ID)
			if len(got) != 1 {
				t.Fatalf("occurrence=%+v", got)
			}
			want := tc.detail
			if tc.status != "failed" && tc.status != "interrupted" {
				want = ""
			}
			if utf8.RuneCountInString(want) > maxOccurrenceError {
				if !utf8.ValidString(got[0].StatusError) || utf8.RuneCountInString(got[0].StatusError) != maxOccurrenceError || !strings.HasSuffix(got[0].StatusError, "\n[… truncated]") {
					t.Fatalf("invalid bound: %q", got[0].StatusError)
				}
			} else if got[0].StatusError != want {
				t.Fatalf("detail=%q want=%q", got[0].StatusError, want)
			}
		})
	}
}

func TestScheduleOccurrencesScopeDeletedAndReadOnly(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	other, err := s.CreateBot("other", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	otherConv, err := s.GetConversation(other.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	foreignSchedule, foreign := occurrenceFixture(t, s, other, otherConv, scheduleOnce)
	ordinary, err := s.AddRun(c.ID, bot.ID, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: s}
	got := readOccurrences(t, server, c.ID, foreign.ID, ordinary.ID, uuid.NewString(), root.ID)
	if len(got) != 1 || got[0].RootRunID != root.ID || got[0].ScheduleID != x.ID {
		t.Fatalf("scope/deleted outcome=%+v", got)
	}
	if got := readOccurrences(t, server, otherConv.ID, root.ID, ordinary.ID); len(got) != 0 {
		t.Fatalf("cross-conversation roots leaked: %+v", got)
	}
	// A corrupt occurrence association must not bypass the root's own scope.
	if _, err := s.db.Exec(`UPDATE schedules SET conversation_id=? WHERE id=?`, c.ID, foreignSchedule.ID); err != nil {
		t.Fatal(err)
	}
	if got := readOccurrences(t, server, c.ID, foreign.ID); len(got) != 0 {
		t.Fatalf("mismatched root scope leaked: %+v", got)
	}
	// Existing owner read surfaces allow archived/hidden conversations by ID.
	if _, err := s.db.Exec(`UPDATE conversations SET archived=1,user_visible=0 WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	got = readOccurrences(t, server, c.ID, root.ID)
	if len(got) != 1 {
		t.Fatalf("archived hidden read=%+v", got)
	}
}

func TestScheduleOccurrencesOwnerAuth(t *testing.T) {
	server, dir := ownerTestServer(t)
	bot, err := server.store.CreateBot("fixture owner bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := server.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, root := occurrenceFixture(t, server.store, bot, c, scheduleOnce)
	const diagnostic = "owner-only scheduled failure detail"
	if _, err := server.store.SetRunStatus(root.ID, "failed", diagnostic); err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + c.ID + "/schedule-occurrences?root_run_ids=" + root.ID
	cookie := ownerSetup(t, server, dir)
	for _, auth := range []*http.Cookie{nil, {Name: cookie.Name, Value: "invalid-fixture-session"}} {
		w := ownerCall(server, http.MethodGet, path, nil, auth, true)
		if w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), root.ID) || strings.Contains(w.Body.String(), diagnostic) {
			t.Fatalf("unauthorized response=%d %s", w.Code, w.Body.String())
		}
	}
	w := ownerCall(server, http.MethodGet, path, nil, cookie, true)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), root.ID) || !strings.Contains(w.Body.String(), diagnostic) {
		t.Fatalf("owner response=%d %s", w.Code, w.Body.String())
	}
	w = ownerCall(server, http.MethodPost, path, nil, cookie, true)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
		t.Fatalf("write method=%d %s", w.Code, w.Body.String())
	}
}

func TestScheduleOccurrencesQueryBounds(t *testing.T) {
	s, bot, c := scheduleTestStore(t)
	defer s.Close()
	_, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
	server := &Server{store: s}
	for _, query := range []string{
		"", "root_run_ids=", "root_run_ids=,", "root_run_ids=" + root.ID + ",", "root_run_ids=" + root.ID + ",," + root.ID,
		"root_run_ids=not-a-uuid", "root_run_ids=" + strings.ReplaceAll(root.ID, "-", ""), "root_run_ids=" + url.QueryEscape(" "+root.ID),
		"root_run_ids=%zz", "root_run_ids=" + root.ID + ";junk", "root_run_ids=" + root.ID + "&root_run_ids=" + root.ID,
		"root_run_ids=" + root.ID + "&unexpected=1", "root_run_ids=" + strings.Repeat(root.ID+",", 50) + root.ID,
		"root_run_ids=" + strings.Repeat("x", maxOccurrenceQuery),
	} {
		w := occurrenceCall(server, http.MethodGet, c.ID, query)
		if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("query prefix %.100q: code=%d body=%s", query, w.Code, w.Body.String())
		}
	}
	ids := []string{root.ID}
	for len(ids) < 50 {
		ids = append(ids, uuid.NewString())
	}
	if got := readOccurrences(t, server, c.ID, ids...); len(got) != 1 {
		t.Fatalf("50-root boundary=%+v", got)
	}
	w := occurrenceCall(server, http.MethodGet, uuid.NewString(), "root_run_ids="+root.ID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing conversation=%d", w.Code)
	}
	if _, err := s.ScheduleOccurrences(context.Background(), c.ID, nil); err == nil {
		t.Fatal("store accepted unbounded/empty input")
	}
}

func TestScheduleOccurrencesCorruptCycle(t *testing.T) {
	for _, self := range []bool{true, false} {
		s, bot, c := scheduleTestStore(t)
		t.Cleanup(func() { s.Close() })
		x, root := occurrenceFixture(t, s, bot, c, scheduleOnce)
		if _, err := s.SetRunStatus(root.ID, "failed", "fixture cycle failure"); err != nil {
			t.Fatal(err)
		}
		parentID := root.ID
		if !self {
			retry, err := s.RetryRun(root.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetRunStatus(retry.ID, "done", ""); err != nil {
				t.Fatal(err)
			}
			parentID = retry.ID
		}
		if _, err := s.db.Exec(`UPDATE runs SET parent_run_id=? WHERE id=?`, parentID, root.ID); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		got, err := s.ScheduleOccurrences(ctx, c.ID, []string{root.ID})
		cancel()
		if err != nil || len(got) != 1 || got[0].ExecutionStatus != "failed" || got[0].StatusRunID != root.ID {
			t.Fatalf("self=%v cycle outcome=%+v err=%v", self, got, err)
		}
		latest, err := s.GetSchedule(x.ID)
		if err != nil || latest.ExecutionStatus != "failed" || latest.LastRunID != root.ID {
			t.Fatalf("self=%v latest cycle=%+v err=%v", self, latest, err)
		}
	}
}
