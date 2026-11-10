package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type attentionRowFixture struct {
	ID     int64
	Kind   string
	Conv   string
	Bot    string
	Seq    *int64
	Detail string
	Stamp  string
}

func attentionRows(t *testing.T, s *Store) []attentionRowFixture {
	t.Helper()
	rows, err := s.attentionAfter(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]attentionRowFixture, len(rows))
	for i, r := range rows {
		out[i] = attentionRowFixture{r.ID, r.Kind, r.ConversationID, r.BotID, r.MessageSeq, r.Detail, r.CreatedAt}
	}
	return out
}

func attentionKinds(t *testing.T, s *Store) []string {
	t.Helper()
	var kinds []string
	for _, r := range attentionRows(t, s) {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

func wantKinds(t *testing.T, s *Store, want ...string) {
	t.Helper()
	got := attentionKinds(t, s)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("attention kinds=%v want %v", got, want)
	}
}

// The classifier table is the web client's rule (ui/src/App.tsx refreshConversations):
// an assistant message that is not internal, in a conversation the list shows,
// is a reply, or a task result for the delegation kinds. The list never shows
// bot_result, scheduled_task and internal rows, so those never notified.
func TestClassifyAttentionMatchesTheClientRule(t *testing.T) {
	msg := func(role, kind string, internal, listed bool) attentionSignal {
		return attentionSignal{Source: attentionFromMessage, Role: role, Kind: kind, Internal: internal, Listed: listed}
	}
	for _, tc := range []struct {
		name string
		sig  attentionSignal
		want string
	}{
		{"plain assistant reply", msg("assistant", "", false, true), attentionReply},
		{"segment", msg("assistant", "segment", false, true), attentionReply},
		{"message to the person", msg("assistant", "user_message", false, true), attentionReply},
		{"card", msg("assistant", "ui_card", false, true), attentionReply},
		{"delegation result", msg("assistant", "forward_result", false, true), attentionTaskResult},
		{"private anchor is never listed", msg("assistant", "bot_result", false, true), ""},
		{"run-internal text", msg("assistant", "internal", false, true), ""},
		{"scheduled trigger", msg("assistant", "scheduled_task", false, true), ""},
		{"internal flag wins over reply", msg("assistant", "", true, true), ""},
		{"internal flag wins over task result", msg("assistant", "forward_result", true, true), ""},
		{"the person's own message", msg("user", "", false, true), ""},
		{"the person's scheduled message", msg("user", "scheduled_task", false, true), ""},
		{"hidden or archived conversation", msg("assistant", "", false, false), ""},
		{"run waits", attentionSignal{Source: attentionFromRun, RunStatus: "waiting"}, attentionNeedsYou},
		{"run running", attentionSignal{Source: attentionFromRun, RunStatus: "running"}, ""},
		{"run done", attentionSignal{Source: attentionFromRun, RunStatus: "done"}, ""},
		{"spend at warning", attentionSignal{Source: attentionFromSpend, SpendPercent: 80}, attentionSpendAlert},
		{"spend at cap", attentionSignal{Source: attentionFromSpend, SpendPercent: 100}, attentionSpendAlert},
		{"spend below warning", attentionSignal{Source: attentionFromSpend, SpendPercent: 79}, ""},
		{"unknown source", attentionSignal{Role: "assistant"}, ""},
	} {
		if got := classifyAttention(tc.sig); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestFinishedReplyRecordsOneRowWithPointers(t *testing.T) {
	s := spendStore(t)
	bot, err := s.CreateBot("Reply Bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	// What the person sends is not a notification.
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "hello", "client-1", []runSpec{{BotID: bot.ID}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	wantKinds(t, s)
	if _, err = s.SetRunStatus(runs[0].ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	m, finished, err := s.FinishRun(runs[0].ID, bot.DMConversationID, bot.ID, "the answer")
	if err != nil || !finished {
		t.Fatalf("finish=%v err=%v", finished, err)
	}
	rows := attentionRows(t, s)
	if len(rows) != 1 || rows[0].Kind != attentionReply || rows[0].Conv != bot.DMConversationID || rows[0].Bot != bot.ID || rows[0].Seq == nil || *rows[0].Seq != m.Seq {
		t.Fatalf("rows=%+v message=%+v", rows, m)
	}
	// A replayed finish posts nothing and so records nothing.
	if _, finished, err = s.FinishRun(runs[0].ID, bot.DMConversationID, bot.ID, "the answer"); err != nil || finished {
		t.Fatalf("second finish=%v err=%v", finished, err)
	}
	wantKinds(t, s, attentionReply)
}

func TestDelegationResultIsATaskResultAndTheDelegateStaysQuiet(t *testing.T) {
	s := spendStore(t)
	source, _ := s.CreateBot("source", "", "source-model")
	target, _ := s.CreateBot("target", "", "target-model")
	parent, err := s.AddRun(source.DMConversationID, source.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(parent.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	_, child, err := s.AddForwardHandoff(source.DMConversationID, source.ID, target.ID, parent.ID, "fixture task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetRunStatus(child.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, finished, err := s.FinishRun(child.ID, target.DMConversationID, target.ID, "target result"); err != nil || !finished {
		t.Fatalf("finish=%v err=%v", finished, err)
	}
	// The delegate's own message sits in its DM under a run that started elsewhere: internal, silent.
	// The result that returns to the delegating conversation is the one notification.
	rows := attentionRows(t, s)
	if len(rows) != 1 || rows[0].Kind != attentionTaskResult || rows[0].Conv != source.DMConversationID || rows[0].Bot != target.ID || rows[0].Seq == nil {
		t.Fatalf("rows=%+v", rows)
	}
	var kind string
	if err = s.db.QueryRow(`SELECT kind FROM messages WHERE conversation_id=? AND seq=?`, rows[0].Conv, *rows[0].Seq).Scan(&kind); err != nil || kind != "forward_result" {
		t.Fatalf("pointer names kind=%q err=%v", kind, err)
	}
}

// postStoredMessage inserts a message the way the posting sites do and runs the recorder on it.
func postStoredMessage(t *testing.T, s *Store, m Message) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var runID any
	if m.RunID != "" {
		runID = m.RunID
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, runID, m.Content, now()); err != nil {
		t.Fatal(err)
	}
	if err = recordMessageAttentionTx(tx, m); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestHiddenAndInternalMessagesRecordNothing(t *testing.T) {
	s := spendStore(t)
	bot, _ := s.CreateBot("Hidden Bot", "", "model")
	other, _ := s.CreateBot("Other Bot", "", "model")
	conv := bot.DMConversationID
	n := int64(100)
	next := func() int64 { n++; return n }
	post := func(role, kind, runID string) {
		postStoredMessage(t, s, Message{ID: newID(), ConversationID: conv, Seq: next(), Role: role, Kind: kind, SenderBotID: bot.ID, RunID: runID, Content: "x"})
	}
	post("assistant", "internal", "")
	post("assistant", "bot_result", "")
	post("user", "", "")
	post("user", "scheduled_task", "")
	// A run that started in another conversation: its message here is internal.
	delegated, err := s.AddRun(other.DMConversationID, other.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE runs SET conversation_id=?, origin_conversation_id=? WHERE id=?`, conv, other.DMConversationID, delegated.ID); err != nil {
		t.Fatal(err)
	}
	post("assistant", "", delegated.ID)
	wantKinds(t, s)
	// The same run's message addressed to the person (user_message) is not internal.
	post("assistant", "user_message", delegated.ID)
	wantKinds(t, s, attentionReply)

	// Archived conversations are not in the list the client polls.
	if _, err = s.db.Exec(`UPDATE conversations SET archived=1 WHERE id=?`, conv); err != nil {
		t.Fatal(err)
	}
	post("assistant", "", "")
	// Nor are conversations the person cannot see.
	if _, err = s.db.Exec(`UPDATE conversations SET archived=0, user_visible=0 WHERE id=?`, conv); err != nil {
		t.Fatal(err)
	}
	post("assistant", "", "")
	wantKinds(t, s, attentionReply)
}

func TestAskModeApprovalRecordsNeedsYou(t *testing.T) {
	s, c, r := questionFixtureDir(t, t.TempDir())
	t.Cleanup(func() { s.Close() })
	wantKinds(t, s)
	q, err := s.CreateQuestion(c.ID, r, askQuestionInput{Type: questionApproval, Question: "Synthetic approval", ExpiresInSeconds: 1800, Approval: &ApprovalDetails{Action: "write", Target: "synthetic", Impact: "test only"}})
	if err != nil {
		t.Fatal(err)
	}
	// Asking alone does not stop the run; the stop does.
	wantKinds(t, s)
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, json.RawMessage(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	rows := attentionRows(t, s)
	if len(rows) != 1 || rows[0].Kind != attentionNeedsYou || rows[0].Conv != c.ID || rows[0].Bot != r.BotID || rows[0].Seq != nil || rows[0].Detail != "" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestSpendAlertsAreRecordedOncePerThresholdPerMonth(t *testing.T) {
	s := spendStore(t)
	bot, err := s.CreateBot("Alert Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-sonnet-5-5"
	setSpendLimit(t, s, "10")
	june := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	bookSpend(t, s, june, model, 7)
	s.noteSpend(june)
	wantKinds(t, s)
	bookSpend(t, s, june, model, 1.5)
	s.noteSpend(june)
	s.noteSpend(june)
	wantKinds(t, s, attentionSpendAlert)
	rows := attentionRows(t, s)
	if rows[0].Conv != bot.DMConversationID || rows[0].Bot != bot.ID || rows[0].Seq == nil || rows[0].Detail != attentionSpendWarning {
		t.Fatalf("warning row=%+v", rows[0])
	}
	// Reaching the cap is its own threshold, again once.
	bookSpend(t, s, june, model, 2)
	s.noteSpend(june)
	s.noteSpend(june)
	wantKinds(t, s, attentionSpendAlert, attentionSpendAlert)
	if capRow := attentionRows(t, s)[1]; capRow.Seq != nil || capRow.Detail != attentionSpendCap || capRow.Conv != bot.DMConversationID {
		t.Fatalf("cap row=%+v", capRow)
	}
	// A new month alerts again.
	july := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	bookSpend(t, s, july, model, 9)
	s.noteSpend(july)
	s.noteSpend(july)
	wantKinds(t, s, attentionSpendAlert, attentionSpendAlert, attentionSpendAlert)
}

func TestSpendJumpingStraightToTheCapWritesOneNotification(t *testing.T) {
	s := spendStore(t)
	if _, err := s.CreateBot("Alert Bot", "", "claude-sonnet-5-5"); err != nil {
		t.Fatal(err)
	}
	setSpendLimit(t, s, "10")
	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	bookSpend(t, s, at, "claude-sonnet-5-5", 12)
	s.noteSpend(at)
	s.noteSpend(at)
	wantKinds(t, s, attentionSpendAlert)
	if row := attentionRows(t, s)[0]; row.Detail != attentionSpendCap {
		t.Fatalf("a jump to the cap names %q", row.Detail)
	}
}

func seedAttention(t *testing.T, s *Store, n int, conv string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.db.Exec(`INSERT INTO attention(kind,conversation_id,bot_id,message_seq,created_at) VALUES('reply',?,'bot',?,?)`, conv, i+1, now()); err != nil {
			t.Fatal(err)
		}
	}
}

type notificationsBody struct {
	Notifications []map[string]any `json:"notifications"`
	NextAfter     int64            `json:"next_after"`
}

func getNotifications(t *testing.T, s *Server, query string) (notificationsBody, int) {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/notifications"+query, nil))
	var body notificationsBody
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %s: %v", w.Body.String(), err)
		}
	}
	return body, w.Code
}

func TestNotificationsAPIPagesAndCapsTheLimit(t *testing.T) {
	srv, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	seedAttention(t, srv.store, 250, "conv-1")

	first, code := getNotifications(t, srv, "")
	if code != 200 || len(first.Notifications) != attentionDefaultLimit || first.NextAfter != int64(attentionDefaultLimit) {
		t.Fatalf("default page: code=%d n=%d next=%d", code, len(first.Notifications), first.NextAfter)
	}
	huge, _ := getNotifications(t, srv, "?limit=100000")
	if len(huge.Notifications) != attentionMaxLimit || huge.NextAfter != int64(attentionMaxLimit) {
		t.Fatalf("capped page: n=%d next=%d", len(huge.Notifications), huge.NextAfter)
	}
	// Walking the cursor sees every row once, in order.
	seen, after := 0, int64(0)
	for guard := 0; guard < 10; guard++ {
		page, _ := getNotifications(t, srv, fmt.Sprintf("?after=%d&limit=100", after))
		for i, n := range page.Notifications {
			if int64(n["id"].(float64)) != after+int64(i)+1 {
				t.Fatalf("out of order at %d: %v", after, n)
			}
		}
		seen += len(page.Notifications)
		if len(page.Notifications) == 0 {
			if page.NextAfter != after {
				t.Fatalf("empty page moved the cursor: %d -> %d", after, page.NextAfter)
			}
			break
		}
		after = page.NextAfter
	}
	if seen != 250 || after != 250 {
		t.Fatalf("walked %d rows, cursor %d", seen, after)
	}
	row := first.Notifications[0]
	for _, key := range []string{"id", "kind", "detail", "conversation_id", "bot_id", "message_seq", "created_at"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("row lacks %q: %v", key, row)
		}
	}
	if len(row) != 7 {
		t.Fatalf("row carries more than pointers: %v", row)
	}
	for _, bad := range []string{"?after=-1", "?after=x", "?limit=0", "?limit=x"} {
		if _, code = getNotifications(t, srv, bad); code != 400 {
			t.Fatalf("%s: code=%d", bad, code)
		}
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/notifications", nil))
	if w.Code != 405 {
		t.Fatalf("POST: %d", w.Code)
	}
}

func TestNotificationsAPIKeepsAccountsApart(t *testing.T) {
	g := accountFixture(t)
	maria, err := g.create(context.Background(), "maria", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	omar, err := g.create(context.Background(), "omar", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id IN (?,?)`, maria.ID, omar.ID); err != nil {
		t.Fatal(err)
	}
	mariaWS, err := g.workspace(maria)
	if err != nil {
		t.Fatal(err)
	}
	omarWS, err := g.workspace(omar)
	if err != nil {
		t.Fatal(err)
	}
	seedAttention(t, mariaWS.store, 3, "maria-conversation")
	seedAttention(t, omarWS.store, 1, "omar-conversation")

	read := func(a Account) notificationsBody {
		w := accountRequest(g, "GET", "/api/notifications", "", accountCookie(t, g, a))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", a.ID, w.Code, w.Body.String())
		}
		var body notificationsBody
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	m, o := read(maria), read(omar)
	if len(m.Notifications) != 3 || len(o.Notifications) != 1 {
		t.Fatalf("maria sees %d, omar sees %d", len(m.Notifications), len(o.Notifications))
	}
	for _, n := range m.Notifications {
		if n["conversation_id"] != "maria-conversation" {
			t.Fatalf("maria sees %v", n)
		}
	}
	for _, n := range o.Notifications {
		if n["conversation_id"] != "omar-conversation" {
			t.Fatalf("omar sees %v", n)
		}
	}
	if w := accountRequest(g, "GET", "/api/notifications", "", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

func TestAttentionRetentionDeletesRowsOlderThanThirtyDays(t *testing.T) {
	s := spendStore(t)
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for _, age := range []time.Duration{45 * 24 * time.Hour, 31 * 24 * time.Hour, 29 * 24 * time.Hour, time.Hour} {
		if _, err := s.db.Exec(`INSERT INTO attention(kind,created_at) VALUES('reply',?)`, stampTime(at.Add(-age))); err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{store: s}
	srv.runAttentionRetention(at)
	rows := attentionRows(t, s)
	if len(rows) != 2 {
		t.Fatalf("rows left=%+v", rows)
	}
	// Ids keep rising after the table empties, so a client cursor never points past a reused id.
	srv.runAttentionRetention(at.Add(90 * 24 * time.Hour))
	if _, err := s.db.Exec(`INSERT INTO attention(kind,created_at) VALUES('reply',?)`, stampTime(at)); err != nil {
		t.Fatal(err)
	}
	if after := attentionRows(t, s); len(after) != 1 || after[0].ID <= rows[1].ID {
		t.Fatalf("id reused: %+v after %+v", after, rows)
	}
}

func TestDeletingAConversationRemovesItsNotifications(t *testing.T) {
	s := spendStore(t)
	a, _ := s.CreateBot("A", "", "model")
	b, _ := s.CreateBot("B", "", "model")
	seedAttention(t, s, 2, a.DMConversationID)
	seedAttention(t, s, 1, b.DMConversationID)
	if _, err := s.DeleteBot(a.ID); err != nil {
		t.Fatal(err)
	}
	rows := attentionRows(t, s)
	if len(rows) != 1 || rows[0].Conv != b.DMConversationID {
		t.Fatalf("rows=%+v", rows)
	}
}
