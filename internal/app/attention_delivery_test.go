package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- deliverable: the one rule function ----

const dStamp = "2026-11-09T10:00:00.000000000Z"

func dTime(sec int) string {
	base, _ := time.Parse(storedTimeLayout, dStamp)
	return stampTime(base.Add(time.Duration(sec) * time.Second))
}

func dRow(id int64, kind, conv string, seq int64, sec int) AttentionRow {
	r := AttentionRow{ID: id, Kind: kind, ConversationID: conv, BotID: "bot-1", CreatedAt: dTime(sec)}
	if seq > 0 {
		r.MessageSeq = &seq
	}
	return r
}

func dFacts() deliveryFacts {
	return deliveryFacts{
		Prefs:   userPreferences{NotifyChats: true, NotifyTasks: true, NotifyNeedsYou: true, NotifySpendAlerts: true},
		Content: true,
		Titles:  map[string]string{"c1": "Chat one", "c2": "Chat two"},
		Reads:   map[string]int64{},
		Pending: map[string]bool{"c1": true},
		Bots:    map[string]string{"bot-1": "Ada"},
		Previews: map[string]string{
			deliveryPreviewKey("c1", 5): "hello there",
			deliveryPreviewKey("c1", 6): "second",
			deliveryPreviewKey("c2", 1): "other chat",
		},
	}
}

func TestDeliverableEachSwitchHidesOnlyItsKind(t *testing.T) {
	rows := []AttentionRow{
		dRow(1, attentionReply, "c1", 5, 0),
		dRow(2, attentionTaskResult, "c2", 1, 0),
		dRow(3, attentionNeedsYou, "c1", 0, 0),
		dRow(4, attentionSpendAlert, "", 0, 0),
	}
	kinds := func(items []DeliveryItem) string {
		var k []string
		for _, it := range items {
			k = append(k, it.Kind)
		}
		return strings.Join(k, ",")
	}
	if got := kinds(deliverable(rows, nil, dFacts())); got != "reply,task_result,needs_you,spend_alert" {
		t.Fatalf("all on: %s", got)
	}
	for _, tc := range []struct {
		name string
		off  func(*userPreferences)
		want string
	}{
		{"chats", func(p *userPreferences) { p.NotifyChats = false }, "task_result,needs_you,spend_alert"},
		{"tasks", func(p *userPreferences) { p.NotifyTasks = false }, "reply,needs_you,spend_alert"},
		{"needs_you", func(p *userPreferences) { p.NotifyNeedsYou = false }, "reply,task_result,spend_alert"},
		{"spend", func(p *userPreferences) { p.NotifySpendAlerts = false }, "reply,task_result,needs_you"},
	} {
		f := dFacts()
		tc.off(&f.Prefs)
		if got := kinds(deliverable(rows, nil, f)); got != tc.want {
			t.Errorf("%s off: got %s want %s", tc.name, got, tc.want)
		}
	}
}

func TestDeliverableDropsWhatIsAlreadyReadOrAnsweredOrGone(t *testing.T) {
	f := dFacts()
	f.Reads["c1"] = 5
	rows := []AttentionRow{dRow(1, attentionReply, "c1", 5, 0), dRow(2, attentionReply, "c1", 6, 1)}
	items := deliverable(rows, nil, f)
	if len(items) != 1 || items[0].ID != 2 {
		t.Fatalf("seq 5 is read (<= read_seq), seq 6 is not: %+v", items)
	}
	// A needs_you whose card was answered elsewhere is not delivered; one still pending is.
	f = dFacts()
	needs := []AttentionRow{dRow(1, attentionNeedsYou, "c1", 0, 0), dRow(2, attentionNeedsYou, "c2", 0, 0)}
	items = deliverable(needs, nil, f)
	if len(items) != 1 || items[0].ConversationID != "c1" {
		t.Fatalf("only c1 has a pending card: %+v", items)
	}
	// A deleted conversation has no title: its rows are dropped.
	if items = deliverable([]AttentionRow{dRow(1, attentionReply, "gone", 1, 0)}, nil, dFacts()); len(items) != 0 {
		t.Fatalf("unknown conversation: %+v", items)
	}
}

func TestDeliverableMergesOneConversationAndKindWithACount(t *testing.T) {
	rows := []AttentionRow{
		dRow(1, attentionReply, "c1", 5, 0),
		dRow(2, attentionReply, "c2", 1, 1),
		dRow(3, attentionReply, "c1", 6, 2),
		dRow(4, attentionTaskResult, "c1", 7, 3),
	}
	f := dFacts()
	f.Previews[deliveryPreviewKey("c1", 7)] = "task text"
	items := deliverable(rows, nil, f)
	if len(items) != 3 {
		t.Fatalf("want 3 items (c1 replies merged, c2 reply, c1 task result): %+v", items)
	}
	var c1 DeliveryItem
	for _, it := range items {
		if it.ConversationID == "c1" && it.Kind == attentionReply {
			c1 = it
		}
	}
	if c1.Count != 2 || c1.ID != 3 || c1.MessageSeq == nil || *c1.MessageSeq != 6 || c1.Preview != "second" || c1.Title != "Chat one" || c1.BotName != "Ada" {
		t.Fatalf("merged item takes the last row: %+v", c1)
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].ID > items[i].ID {
			t.Fatalf("items must stay in id order: %+v", items)
		}
	}
}

func TestDeliverableMarksABurstQuiet(t *testing.T) {
	f := dFacts()
	// An earlier delivered row of the same conversation inside 30 s: quiet.
	items := deliverable([]AttentionRow{dRow(11, attentionReply, "c1", 6, 20)}, []AttentionRow{dRow(10, attentionReply, "c1", 5, 0)}, f)
	if len(items) != 1 || !items[0].Quiet {
		t.Fatalf("row 20 s after a delivered one is quiet: %+v", items)
	}
	// 31 s later is loud again.
	items = deliverable([]AttentionRow{dRow(11, attentionReply, "c1", 6, 31)}, []AttentionRow{dRow(10, attentionReply, "c1", 5, 0)}, f)
	if len(items) != 1 || items[0].Quiet {
		t.Fatalf("31 s gap is loud: %+v", items)
	}
	// Another conversation does not make it quiet.
	items = deliverable([]AttentionRow{dRow(11, attentionReply, "c2", 1, 5)}, []AttentionRow{dRow(10, attentionReply, "c1", 5, 0)}, f)
	if len(items) != 1 || items[0].Quiet {
		t.Fatalf("other conversation: %+v", items)
	}
	// An earlier row that is itself filtered (already read) does not count.
	f.Reads["c1"] = 5
	items = deliverable([]AttentionRow{dRow(11, attentionReply, "c1", 6, 5)}, []AttentionRow{dRow(10, attentionReply, "c1", 5, 0)}, f)
	if len(items) != 1 || items[0].Quiet {
		t.Fatalf("a filtered earlier row is not a delivered one: %+v", items)
	}
	// Inside one response the first item is loud, a later kind of the same conversation is quiet.
	f = dFacts()
	items = deliverable([]AttentionRow{dRow(1, attentionReply, "c1", 5, 0), dRow(2, attentionNeedsYou, "c1", 0, 3)}, nil, f)
	if len(items) != 2 || items[0].Quiet || !items[1].Quiet {
		t.Fatalf("burst in one response: %+v", items)
	}
	// Rows merged into one item do not make that item quiet by themselves.
	items = deliverable([]AttentionRow{dRow(1, attentionReply, "c1", 5, 0), dRow(2, attentionReply, "c1", 6, 3)}, nil, f)
	if len(items) != 1 || items[0].Quiet || items[0].Count != 2 {
		t.Fatalf("merged rows: %+v", items)
	}
}

func TestDeliverableContentOffDropsThePreviewAndKeepsTheTitle(t *testing.T) {
	rows := []AttentionRow{dRow(1, attentionReply, "c1", 5, 0)}
	f := dFacts()
	if items := deliverable(rows, nil, f); items[0].Preview != "hello there" {
		t.Fatalf("content on: %+v", items)
	}
	f.Content = false
	items := deliverable(rows, nil, f)
	if items[0].Preview != "" || items[0].Title != "Chat one" {
		t.Fatalf("content off keeps the title, drops the preview: %+v", items)
	}
	// Needs-you and spend alerts carry no preview in any case.
	f = dFacts()
	if items = deliverable([]AttentionRow{dRow(1, attentionNeedsYou, "c1", 0, 0)}, nil, f); items[0].Preview != "" {
		t.Fatalf("needs_you preview: %+v", items)
	}
}

func TestAttentionPreviewMatchesTheWebRule(t *testing.T) {
	for in, want := range map[string]string{
		"hello  **bold**\n world":                              "hello bold world",
		"see [the docs](https://x.test/a) now":                 "see the docs now",
		"[message_id=0f8fad5b-d9cb-469f-a165-70867728950e] hi": "hi",
		"[message_id=0f8fad5b":                                 "",
		"":                                                     "",
	} {
		if got := attentionPreview(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	long := attentionPreview(strings.Repeat("a", 200))
	if len([]rune(long)) != 160 || !strings.HasSuffix(long, "…") {
		t.Fatalf("long preview has %d runes", len([]rune(long)))
	}
}

// ---- HTTP: the view, the cursor, tail, P1 compatibility ----

type deliveryResponse struct {
	Items     []DeliveryItem `json:"items"`
	NextAfter int64          `json:"next_after"`
	Language  string         `json:"language"`
}

func notifServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func getDelivery(t *testing.T, s *Server, query string) (deliveryResponse, int) {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/notifications"+query, nil))
	var body deliveryResponse
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %s: %v", w.Body.String(), err)
		}
	}
	return body, w.Code
}

// seedReply writes a bot message and its attention row the way the posting sites do.
func seedReply(t *testing.T, s *Store, convID, botID string, seq int64, text string) {
	t.Helper()
	postStoredMessage(t, s, Message{ID: fmt.Sprintf("m-%s-%d", convID, seq), ConversationID: convID, Seq: seq, Role: "assistant", SenderBotID: botID, Content: text})
}

func TestDeliveryViewServesTitleAndPreviewAndAdvancesPastFilteredRows(t *testing.T) {
	srv := notifServer(t)
	bot, err := srv.store.CreateBot("Ada", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	seedReply(t, srv.store, bot.DMConversationID, bot.ID, 1, "first **reply**")
	seedReply(t, srv.store, bot.DMConversationID, bot.ID, 2, "second reply")
	got, code := getDelivery(t, srv, "?view=delivery")
	if code != 200 || len(got.Items) != 1 || got.NextAfter != 2 {
		t.Fatalf("code=%d %+v", code, got)
	}
	it := got.Items[0]
	if it.Count != 2 || it.Preview != "second reply" || it.BotName != "Ada" || it.Title == "" || it.ConversationID != bot.DMConversationID || it.ID != 2 {
		t.Fatalf("item: %+v", it)
	}
	// content=0 drops the preview and nothing else.
	got, _ = getDelivery(t, srv, "?view=delivery&content=0")
	if got.Items[0].Preview != "" || got.Items[0].Title != it.Title || got.Items[0].Count != 2 {
		t.Fatalf("content=0: %+v", got.Items[0])
	}
	// Read in another place: the rows are filtered but the cursor still moves.
	if _, err = srv.store.MarkRead(bot.DMConversationID, 2); err != nil {
		t.Fatal(err)
	}
	got, _ = getDelivery(t, srv, "?view=delivery")
	if len(got.Items) != 0 || got.NextAfter != 2 || got.Items == nil {
		t.Fatalf("read rows: %+v", got)
	}
	// A switch turned off is honoured on the next read.
	seedReply(t, srv.store, bot.DMConversationID, bot.ID, 3, "third")
	off := false
	if err = srv.store.putUserNotify(notifySwitches{Chats: &off}); err != nil {
		t.Fatal(err)
	}
	got, _ = getDelivery(t, srv, "?view=delivery&after=2")
	if len(got.Items) != 0 || got.NextAfter != 3 {
		t.Fatalf("chats off: %+v", got)
	}
	if err = srv.store.putUserLanguage("ja"); err != nil {
		t.Fatal(err)
	}
	if got, _ = getDelivery(t, srv, "?view=delivery&after=3"); got.Language != "ja" {
		t.Fatalf("language: %+v", got)
	}
}

func TestDeliveryViewNeedsYouFollowsThePendingCard(t *testing.T) {
	srv := notifServer(t)
	bot, err := srv.store.CreateBot("Ada", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.store.AddRun(bot.DMConversationID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	q, err := srv.store.CreateQuestion(bot.DMConversationID, run, askQuestionInput{Type: questionApproval, Question: "Synthetic approval", ExpiresInSeconds: 1800, Approval: &ApprovalDetails{Action: "write", Target: "synthetic", Impact: "test only"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.store.SaveInputContinuation(context.Background(), run.ID, q.ID, json.RawMessage(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := getDelivery(t, srv, "?view=delivery")
	if len(got.Items) != 1 || got.Items[0].Kind != attentionNeedsYou || got.Items[0].Preview != "" {
		t.Fatalf("pending card: %+v", got)
	}
	if _, _, err = srv.store.AnswerQuestion(q.ID, "human", true); err != nil {
		t.Fatal(err)
	}
	got, _ = getDelivery(t, srv, "?view=delivery")
	if len(got.Items) != 0 || got.NextAfter != 1 {
		t.Fatalf("answered card must not be delivered, cursor must move: %+v", got)
	}
}

func TestDeliveryViewQuietUsesRowsBeforeTheCursor(t *testing.T) {
	srv := notifServer(t)
	bot, _ := srv.store.CreateBot("Ada", "", "model")
	seedReply(t, srv.store, bot.DMConversationID, bot.ID, 1, "one")
	first, _ := getDelivery(t, srv, "?view=delivery")
	if first.Items[0].Quiet {
		t.Fatalf("first of a burst is loud: %+v", first.Items[0])
	}
	seedReply(t, srv.store, bot.DMConversationID, bot.ID, 2, "two")
	second, _ := getDelivery(t, srv, fmt.Sprintf("?view=delivery&after=%d", first.NextAfter))
	if len(second.Items) != 1 || !second.Items[0].Quiet {
		t.Fatalf("the next one, seconds later, is quiet: %+v", second)
	}
}

func TestNotificationsTailReturnsTheCursorAndNoItems(t *testing.T) {
	srv := notifServer(t)
	seedAttention(t, srv.store, 3, "conv-1")
	got, code := getDelivery(t, srv, "?view=delivery&tail=1")
	if code != 200 || got.NextAfter != 3 || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("tail: code=%d %+v", code, got)
	}
	raw, _ := getNotifications(t, srv, "?tail=1")
	if raw.NextAfter != 3 || raw.Notifications == nil || len(raw.Notifications) != 0 {
		t.Fatalf("raw tail: %+v", raw)
	}
	// After retention empties the table the cursor does not fall back to 0.
	if _, err := srv.store.db.Exec(`DELETE FROM attention`); err != nil {
		t.Fatal(err)
	}
	if got, _ = getDelivery(t, srv, "?view=delivery&tail=1"); got.NextAfter != 3 {
		t.Fatalf("tail after retention: %+v", got)
	}
	for _, bad := range []string{"?view=x", "?view=delivery&wait=-1", "?view=delivery&wait=x", "?view=delivery&content=2", "?tail=2"} {
		if _, code = getDelivery(t, srv, bad); code != 400 {
			t.Errorf("%s: code=%d", bad, code)
		}
	}
}

// Without view the answer is the phase 1 body, byte for byte.
func TestNotificationsWithoutViewKeepTheP1Bytes(t *testing.T) {
	srv := notifServer(t)
	seq := int64(3)
	for _, r := range []struct {
		kind, conv, bot, detail string
		seq                     any
	}{{"reply", "c1", "b1", "", seq}, {"spend_alert", "", "", "spend_cap", nil}} {
		if _, err := srv.store.db.Exec(`INSERT INTO attention(kind,conversation_id,bot_id,message_seq,detail,created_at) VALUES(?,?,?,?,?,?)`,
			r.kind, nullString(r.conv), nullString(r.bot), r.seq, nullString(r.detail), "2026-11-09T10:00:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
	}
	want := `{"notifications":[{"id":1,"kind":"reply","detail":"","conversation_id":"c1","bot_id":"b1","message_seq":3,"created_at":"2026-11-09T10:00:00.000000000Z"},` +
		`{"id":2,"kind":"spend_alert","detail":"spend_cap","message_seq":null,"created_at":"2026-11-09T10:00:00.000000000Z"}],"next_after":2}` + "\n"
	for _, query := range []string{"", "?after=0&limit=50"} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/notifications"+query, nil))
		if w.Code != 200 || w.Body.String() != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%q: code=%d cache=%q\n got %s\nwant %s", query, w.Code, w.Header().Get("Cache-Control"), w.Body.String(), want)
		}
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/notifications?after=2", nil))
	if w.Body.String() != `{"notifications":[],"next_after":2}`+"\n" {
		t.Fatalf("empty page: %s", w.Body.String())
	}
}

// ---- long poll ----

func fastPoll(t *testing.T, settle, every time.Duration) {
	t.Helper()
	oldSettle, oldEvery := attentionSettle, attentionPollEvery
	attentionSettle, attentionPollEvery = settle, every
	t.Cleanup(func() { attentionSettle, attentionPollEvery = oldSettle, oldEvery })
}

func serveWait(srv *Server, ctx context.Context, query string) (*httptest.ResponseRecorder, time.Duration) {
	w := httptest.NewRecorder()
	start := time.Now()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/notifications"+query, nil).WithContext(ctx))
	return w, time.Since(start)
}

func TestLongPollReturnsAfterTheSettleAndCarriesTheBurst(t *testing.T) {
	fastPoll(t, 300*time.Millisecond, 20*time.Millisecond)
	srv := notifServer(t)
	bot, _ := srv.store.CreateBot("Ada", "", "model")
	go func() {
		time.Sleep(150 * time.Millisecond)
		seedReply(t, srv.store, bot.DMConversationID, bot.ID, 1, "one")
		time.Sleep(100 * time.Millisecond) // lands inside the settle window
		seedReply(t, srv.store, bot.DMConversationID, bot.ID, 2, "two")
	}()
	w, took := serveWait(srv, context.Background(), "?view=delivery&wait=10")
	var body deliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
		t.Fatalf("code=%d %s", w.Code, w.Body.String())
	}
	if len(body.Items) != 1 || body.Items[0].Count != 2 {
		t.Fatalf("the second row of the burst must ride the same response: %+v", body)
	}
	if took < 400*time.Millisecond || took > 3*time.Second {
		t.Fatalf("returned after %v: want first row (~150ms) plus the settle (300ms)", took)
	}
}

func TestLongPollTimesOutEmptyAndHonoursCancellation(t *testing.T) {
	fastPoll(t, 50*time.Millisecond, 20*time.Millisecond)
	srv := notifServer(t)
	w, took := serveWait(srv, context.Background(), "?view=delivery&wait=1&after=0")
	if w.Code != 200 || took < 900*time.Millisecond || took > 2500*time.Millisecond {
		t.Fatalf("code=%d after %v", w.Code, took)
	}
	var body deliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Items == nil || len(body.Items) != 0 || body.NextAfter != 0 {
		t.Fatalf("empty timeout: %s", w.Body.String())
	}
	// The client goes away: the handler returns long before wait and writes nothing.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	w, took = serveWait(srv, ctx, "?view=delivery&wait=25")
	if took > 2*time.Second || w.Body.Len() != 0 {
		t.Fatalf("cancelled wait took %v and wrote %q", took, w.Body.String())
	}
	if n := srv.attentionWaiters.n.Load(); n != 0 {
		t.Fatalf("a finished wait must give its slot back, holds %d", n)
	}
}

func TestLongPollCapsWaitersPerWorkspace(t *testing.T) {
	fastPoll(t, 50*time.Millisecond, 20*time.Millisecond)
	srv := notifServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < attentionMaxWaiters; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); serveWait(srv, ctx, "?view=delivery&wait=25") }()
	}
	deadline := time.Now().Add(3 * time.Second)
	for srv.attentionWaiters.n.Load() != attentionMaxWaiters {
		if time.Now().After(deadline) {
			t.Fatalf("waiters never reached %d: %d", attentionMaxWaiters, srv.attentionWaiters.n.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	w, _ := serveWait(srv, context.Background(), "?view=delivery&wait=25")
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), `"too_many_waiters"`) {
		t.Fatalf("5th waiter: %d %s", w.Code, w.Body.String())
	}
	// A plain read (no wait) is never counted.
	if _, code := getDelivery(t, srv, "?view=delivery"); code != 200 {
		t.Fatalf("plain read while full: %d", code)
	}
	cancel()
	wg.Wait()
	if n := srv.attentionWaiters.n.Load(); n != 0 {
		t.Fatalf("slots after cancel: %d", n)
	}
	if w, _ = serveWait(srv, context.Background(), "?view=delivery&wait=1"); w.Code != 200 {
		t.Fatalf("a slot must be free again: %d", w.Code)
	}
}

// ---- server-info, accounts ----

func TestServerInfoAdvertisesDeliverySupportInBothModes(t *testing.T) {
	check := func(name string, body []byte) {
		var info struct {
			Notifications map[string]any `json:"notifications"`
		}
		if err := json.Unmarshal(body, &info); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.Notifications["delivery"] != float64(1) || info.Notifications["max_wait"] != float64(25) {
			t.Fatalf("%s: notifications=%v", name, info.Notifications)
		}
	}
	srv := notifServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/server-info", nil))
	check("single", w.Body.Bytes())
	g := accountFixture(t)
	check("accounts", accountRequest(g, "GET", "/api/server-info", "", nil).Body.Bytes())
}

func TestDeliveryViewKeepsAccountsApart(t *testing.T) {
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
	mariaWS, _ := g.workspace(maria)
	omarWS, _ := g.workspace(omar)
	mb, _ := mariaWS.store.CreateBot("Mia", "", "model")
	ob, _ := omarWS.store.CreateBot("Otto", "", "model")
	seedReply(t, mariaWS.store, mb.DMConversationID, mb.ID, 1, "for maria")
	seedReply(t, omarWS.store, ob.DMConversationID, ob.ID, 1, "for omar")
	read := func(a Account) deliveryResponse {
		w := accountRequest(g, "GET", "/api/notifications?view=delivery", "", accountCookie(t, g, a))
		var body deliveryResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("%s: %d %s", a.ID, w.Code, w.Body.String())
		}
		return body
	}
	m, o := read(maria), read(omar)
	if len(m.Items) != 1 || m.Items[0].Preview != "for maria" || len(o.Items) != 1 || o.Items[0].Preview != "for omar" {
		t.Fatalf("maria %+v omar %+v", m, o)
	}
}
