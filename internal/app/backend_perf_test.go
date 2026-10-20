package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// retainDeltaEvents keeps delta events after their draft is published, for
// tests that inspect how a finished run streamed.
func retainDeltaEvents(t *testing.T) {
	t.Helper()
	pruneDeltaEvents = false
	t.Cleanup(func() { pruneDeltaEvents = true })
}

// explainPlan returns the lines of EXPLAIN QUERY PLAN for query.
func explainPlan(tb testing.TB, s *Store, query string, args ...any) string {
	tb.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		tb.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			tb.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, "\n")
}

func requirePlan(t *testing.T, plan string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(plan, w) {
			t.Fatalf("plan lacks %q:\n%s", w, plan)
		}
	}
}

func rejectScan(t *testing.T, plan string, tables ...string) {
	t.Helper()
	for _, line := range strings.Split(plan, "\n") {
		for _, table := range tables {
			if strings.HasPrefix(strings.TrimSpace(line), "SCAN "+table) {
				t.Fatalf("full scan of %s in plan:\n%s", table, plan)
			}
		}
	}
}

// seedLiveAndHistoricalWork adds tool activities, drafts and attachments to a
// seeded account: a long finished history plus a handful of live rows.
func seedLiveAndHistoricalWork(tb testing.TB, s *Store) {
	tb.Helper()
	if err := migrateToolActivity(s.db); err != nil {
		tb.Fatal(err)
	}
	if err := s.EnsureStreamSchema(); err != nil {
		tb.Fatal(err)
	}
	if err := migrateAttachments(s.db); err != nil {
		tb.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	ts := stampTime(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	for i := 0; i < 90; i++ {
		conv, bot := fmt.Sprintf("conv_%03d", i), fmt.Sprintf("bot_%03d", i)
		for m := 2; m <= 400; m += 2 {
			run := fmt.Sprintf("run_%03d_%05d", i, m)
			if _, err = tx.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				conv, bot, run, "c1", "tofi_shell", "{}", "ok", "completed", ts, ts); err != nil {
				tb.Fatal(err)
			}
			if _, err = tx.Exec(`INSERT INTO stream_drafts(run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				run, conv, bot, "draft_"+run, 0, "", "done", 1, ts, ts); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if _, err = tx.Exec(`UPDATE runs SET status='running' WHERE id='run_000_00400'`); err != nil {
		tb.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE tool_activities SET status='running' WHERE run_id='run_000_00400'`); err != nil {
		tb.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}

func TestBackendPerfPlansAvoidFullScans(t *testing.T) {
	s := seedLargeAccount(t, 800)
	seedLiveAndHistoricalWork(t, s)

	// BP1: the conversation list picks one run per conversation by index probes.
	ids := []any{"conv_000", "conv_001", "conv_002"}
	plan := explainPlan(t, s, taskStateRunsSQL(len(ids)), ids...)
	requirePlan(t, plan, "runs_active", "runs_conversation")
	rejectScan(t, plan, "r", "runs")
	working := explainPlan(t, s, `SELECT conversation_id,bot_id FROM runs WHERE status IN ('queued','running','waiting') AND status IN ('queued','running') AND bot_id<>'' ORDER BY conversation_id,bot_id`)
	requirePlan(t, working, "runs_active")

	// BP2: the sweeper and startup recovery read only unfinished tool calls.
	requirePlan(t, explainPlan(t, s, stuckToolCallsSQL, questionPending), "tool_activities_active")
	requirePlan(t, explainPlan(t, s, orphanToolActivitiesSQL), "tool_activities_active")

	// BP5: opening a conversation and inserting a message stay index-backed.
	requirePlan(t, explainPlan(t, s, nextMessageSeqSQL, "conv_001", "conv_001", streamDraftActive), "stream_drafts_conversation")
	requirePlan(t, explainPlan(t, s, `SELECT run_id FROM stream_drafts WHERE conversation_id=? AND status=? AND EXISTS(SELECT 1 FROM runs WHERE runs.id=stream_drafts.run_id AND runs.status='running') ORDER BY updated_at,run_id`, "conv_001", streamDraftActive), "stream_drafts_conversation")
	requirePlan(t, explainPlan(t, s, `SELECT am.message_id FROM attachment_messages am WHERE am.message_id IN (?,?)`, "a", "b"), "attachment_messages_message")

	// BP7: the unused content index is gone.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='messages_search'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("messages_search present: %d %v", n, err)
	}
}

// The list must cost the same however much finished history sits behind it.
func TestConversationListIgnoresRunHistoryDepth(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds a large account")
	}
	// Both accounts have more than 100 unread messages per conversation, which
	// the count caps; everything else must not follow the history length.
	small := seedLargeAccount(t, 250)
	large := seedLargeAccount(t, 1000)
	measure := func(s *Store) time.Duration {
		best := time.Hour
		for i := 0; i < 5; i++ {
			start := time.Now()
			if _, err := s.ListConversations(true); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}
	a, b := measure(small), measure(large)
	t.Logf("list: %v with 250 messages per conversation, %v with 1000", a, b)
	if b > a*3+15*time.Millisecond {
		t.Fatalf("conversation list grows with history: %v -> %v", a, b)
	}
}

func TestConversationListKeepsActiveRunAheadOfNewerTerminalRun(t *testing.T) {
	s := seedLargeAccount(t, 40)
	// conv_001 has an older running run and a newer finished one; the live one wins.
	if _, err := s.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,created_at,updated_at) VALUES('zz_live','conv_001','bot_001','running','2026-01-01T00:00:01Z','2026-01-01T00:00:01Z'),('aa_newer','conv_001','bot_001','done','2026-12-01T00:00:00Z','2026-12-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == "conv_001" {
			if c.TaskState == nil || c.TaskState.RunID != "zz_live" || c.TaskState.Status != "executing" {
				t.Fatalf("task state %+v", c.TaskState)
			}
			if len(c.WorkingBotIDs) != 1 || c.WorkingBotIDs[0] != "bot_001" {
				t.Fatalf("working %v", c.WorkingBotIDs)
			}
			return
		}
	}
	t.Fatal("conv_001 missing")
}

func TestConversationListCapsUnreadCount(t *testing.T) {
	s := seedLargeAccount(t, 600)
	list, err := s.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.UnreadCount > 100 {
			t.Fatalf("%s unread %d", c.ID, c.UnreadCount)
		}
	}
}

func TestRunsPagePagesAndKeepsLiveRuns(t *testing.T) {
	s := seedLargeAccount(t, 1)
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	tx, _ := s.db.Begin()
	for i := 0; i < 250; i++ {
		status := "done"
		if i == 3 {
			status = "waiting" // old, but live
		}
		ts := stampTime(base.Add(time.Duration(i) * time.Minute))
		if _, err := tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, fmt.Sprintf("pg_%03d", i), "conv_001", "bot_001", status, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	first, more, err := s.RunsPage("conv_001", 0, "", "")
	if err != nil || !more {
		t.Fatalf("first page more=%v err=%v", more, err)
	}
	ids := map[string]bool{}
	for _, r := range first {
		ids[r.ID] = true
	}
	if len(first) != defaultRunsPage+1 || !ids["pg_249"] || !ids["pg_003"] || ids["pg_100"] {
		t.Fatalf("first page %d runs, live=%v newest=%v", len(first), ids["pg_003"], ids["pg_249"])
	}
	oldest := first[len(first)-1]
	for _, r := range first {
		if r.ID != "pg_003" && r.CreatedAt < oldest.CreatedAt {
			oldest = r
		}
	}
	// Walk the rest by cursor: every run appears exactly once overall.
	seen := map[string]int{}
	for _, r := range first {
		seen[r.ID]++
	}
	cursor := oldest
	for guard := 0; guard < 10; guard++ {
		page, more, err := s.RunsPage("conv_001", 100, cursor.CreatedAt, cursor.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page {
			seen[r.ID]++
		}
		if len(page) > 0 {
			cursor = page[0]
			for _, r := range page {
				if r.CreatedAt < cursor.CreatedAt {
					cursor = r
				}
			}
		}
		if !more {
			break
		}
	}
	for i := 0; i < 250; i++ {
		id := fmt.Sprintf("pg_%03d", i)
		if seen[id] < 1 {
			t.Fatalf("%s never returned", id)
		}
	}
	// The endpoint carries the same page.
	srv, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	b, _ := srv.store.CreateBot("bot", "", "model")
	ts := stampTime(base)
	for i := 0; i < 150; i++ {
		if _, err = srv.store.db.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, fmt.Sprintf("h_%03d", i), b.DMConversationID, b.ID, "done", ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+b.DMConversationID+"/runs", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	var body struct {
		Runs    []Run `json:"runs"`
		HasMore bool  `json:"has_more"`
	}
	if err = json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	if len(body.Runs) != defaultRunsPage || !body.HasMore {
		t.Fatalf("endpoint returned %d runs, more=%v (status %d)", len(body.Runs), body.HasMore, res.Code)
	}
}

func deltaEventStats(t *testing.T, s *Store, conv string) (count, bytes int, withContent int) {
	t.Helper()
	rows, err := s.db.Query(`SELECT data FROM events WHERE conversation_id=? AND type='delta'`, conv)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		count++
		bytes += len(data)
		var v map[string]any
		if json.Unmarshal([]byte(data), &v) == nil {
			if _, ok := v["content"]; ok {
				withContent++
			}
		}
	}
	return
}

func TestDeltaEventsGrowLinearlyAndArePrunedOnPublish(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	const chunks, chunk = 400, 40
	reply := strings.Repeat("x", chunks*chunk)
	for i := 0; i < chunks; i++ {
		if _, ok, err := s.AppendStreamDelta(t.Context(), r.ID, reply[i*chunk:(i+1)*chunk]); err != nil || !ok {
			t.Fatalf("delta %d: %v %v", i, ok, err)
		}
	}
	count, bytes, withContent := deltaEventStats(t, s, c.ID)
	t.Logf("%d delta events, %d bytes for a %d byte reply, %d carry the whole draft", count, bytes, len(reply), withContent)
	if count != chunks {
		t.Fatalf("events %d", count)
	}
	// Fixed per-event fields cost about 500 bytes; the repeated draft must add
	// only a small multiple of the reply, not a copy of it per event.
	if bytes > count*700+4*len(reply) {
		t.Fatalf("delta events hold %d bytes for a %d byte reply", bytes, len(reply))
	}
	if withContent < 2 || withContent > 10 {
		t.Fatalf("events carrying content: %d", withContent)
	}
	// The first revision and the resync points carry the draft; the rest do not.
	for rev, want := range map[int64]bool{1: true, 2: false, 15: false, 16: true, 17: false, 64: true, 80: false, 128: true, 129: false} {
		if deltaCarriesContent(rev) != want {
			t.Fatalf("revision %d carries content: %v", rev, !want)
		}
	}
	if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, reply); err != nil || !done {
		t.Fatalf("finish: %v %v", done, err)
	}
	if count, bytes, _ = deltaEventStats(t, s, c.ID); count != 0 || bytes != 0 {
		t.Fatalf("%d delta events (%d bytes) left after the reply was published", count, bytes)
	}
}

func TestDeltaEventsPrunedOnResetAndCancel(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"a", "b", "c"} {
		if _, _, err := s.AppendStreamDelta(t.Context(), r.ID, text); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ResetStreamDraft(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := deltaEventStats(t, s, c.ID); n != 0 {
		t.Fatalf("%d delta events after reset", n)
	}
	if _, _, err := s.AppendStreamDelta(t.Context(), r.ID, "d"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelStream(r.ID); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := deltaEventStats(t, s, c.ID); n != 0 {
		t.Fatalf("%d delta events after cancel", n)
	}
}

func TestStartupSweepsOrphanDeltaEvents(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.CreateBot("bot", "", "model")
	for _, id := range []string{"gone", "live"} {
		if _, err = s.db.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, b.DMConversationID, "delta", `{"message_id":"`+id+`","text":"t"}`, now()); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := s.AddRun(b.DMConversationID, b.ID, "")
	if _, err = s.db.Exec(`INSERT INTO stream_drafts(run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, r.ID, b.DMConversationID, b.ID, "live", 0, "", "active", 1, now(), now()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnsureStreamSchema(); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := deltaEventStats(t, s, b.DMConversationID); n != 1 {
		t.Fatalf("delta events after sweep: %d", n)
	}
}

func TestAttachmentsForMessagesLoadsAPageInOneQuery(t *testing.T) {
	s := seedLargeAccount(t, 4)
	if err := migrateAttachments(s.db); err != nil {
		t.Fatal(err)
	}
	ts := stampTime(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	for i, msg := range []string{"msg_001_00001", "msg_001_00002", "msg_001_00002"} {
		id := fmt.Sprintf("att_%d", i)
		if _, err := s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, id, "conv_001", id+".txt", "text/plain", 3, id, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, id, msg); err != nil {
			t.Fatal(err)
		}
	}
	got, err := attachmentsForMessages(s.db, []string{"msg_001_00001", "msg_001_00002", "msg_001_00003"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got["msg_001_00001"]) != 1 || len(got["msg_001_00002"]) != 2 || len(got["msg_001_00003"]) != 0 {
		t.Fatalf("attachments by message: %+v", got)
	}
	if a := got["msg_001_00002"][0]; a.MessageID != "msg_001_00002" || a.ID != "att_1" || a.Name != "att_1.txt" {
		t.Fatalf("attachment %+v", a)
	}
	if empty, err := attachmentsForMessages(s.db, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty page: %v %v", empty, err)
	}
}
