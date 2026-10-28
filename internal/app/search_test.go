package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf16"
)

func searchFixture(t *testing.T) (*Store, Bot) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bot, err := s.CreateBot("Harbor Bot", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	return s, bot
}

func putMsg(t *testing.T, s *Store, conv, kind, content, at string) Message {
	t.Helper()
	m, _, err := s.AddMessage(conv, "assistant", "", "", content, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE messages SET kind=?,created_at=? WHERE id=?`, kind, at, m.ID); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFTS5TrigramCompiledIn(t *testing.T) {
	s, _ := searchFixture(t)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='messages_fts'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("fts table n=%d err=%v", n, err)
	}
	if _, err := s.db.Exec(`CREATE VIRTUAL TABLE temp.probe USING fts5(x, tokenize='trigram')`); err != nil {
		t.Fatalf("trigram tokenizer unavailable: %v", err)
	}
}

func TestSearchTitleBodyCJKAndFilters(t *testing.T) {
	s, bot := searchFixture(t)
	conv := bot.DMConversationID
	putMsg(t, s, conv, "", "The quarterly Lighthouse report is ready", "2026-01-01T00:00:00.000000000Z")
	putMsg(t, s, conv, "", "我们明天讨论发布计划和预算", "2026-01-02T00:00:00.000000000Z")
	putMsg(t, s, conv, "progress", "lighthouse progress internal", "2026-01-03T00:00:00.000000000Z")
	putMsg(t, s, conv, "internal", "lighthouse internal", "2026-01-03T00:00:00.000000000Z")
	putMsg(t, s, conv, "notice", "lighthouse notice", "2026-01-03T00:00:00.000000000Z")
	putMsg(t, s, conv, "scheduled_task", "lighthouse scheduled", "2026-01-03T00:00:00.000000000Z")

	r, err := s.SearchAll("lighthouse", "all", 0)
	if err != nil || len(r.Messages) != 1 || !strings.Contains(r.Messages[0].Snippet, "Lighthouse") {
		t.Fatalf("body %+v err=%v", r, err)
	}
	m := r.Messages[0]
	u := utf16.Encode([]rune(m.Snippet))
	if got := string(utf16.Decode(u[m.MatchStart : m.MatchStart+m.MatchLen])); got != "Lighthouse" {
		t.Fatalf("offsets give %q", got)
	}
	if m.ConversationName != "Harbor Bot" || m.ConversationID != conv {
		t.Fatalf("conversation %+v", m)
	}
	// Three-character CJK goes through the trigram index, two-character through LIKE.
	for _, q := range []string{"发布计划", "发布计", "预算", "明天"} {
		r, err = s.SearchAll(q, "messages", 0)
		if err != nil || len(r.Messages) != 1 {
			t.Fatalf("cjk %q: %+v err=%v", q, r, err)
		}
		mm := r.Messages[0]
		u = utf16.Encode([]rune(mm.Snippet))
		if got := string(utf16.Decode(u[mm.MatchStart : mm.MatchStart+mm.MatchLen])); got != q {
			t.Fatalf("cjk offsets %q give %q", q, got)
		}
	}
	// Title.
	r, err = s.SearchAll("harbor", "all", 0)
	if err != nil || len(r.Chats) != 1 || r.Chats[0].ID != conv || r.Chats[0].MatchLen != 6 {
		t.Fatalf("title %+v err=%v", r, err)
	}
	if r, _ = s.SearchAll("harbor", "messages", 0); len(r.Chats) != 0 {
		t.Fatalf("kind=messages returned chats")
	}
	if r, _ = s.SearchAll("lighthouse", "chats", 0); len(r.Messages) != 0 {
		t.Fatalf("kind=chats returned messages")
	}
	// LIKE wildcards are literal.
	if r, _ = s.SearchAll("%", "all", 0); len(r.Messages) != 0 || len(r.Chats) != 0 {
		t.Fatalf("wildcard matched: %+v", r)
	}
	if r, _ = s.SearchAll("\"; DROP", "all", 0); len(r.Messages) != 0 {
		t.Fatalf("quote: %+v", r)
	}

	// Archived and hidden conversations drop out.
	if _, err = s.db.Exec(`UPDATE conversations SET archived=1 WHERE id=?`, conv); err != nil {
		t.Fatal(err)
	}
	if r, _ = s.SearchAll("lighthouse", "all", 0); len(r.Messages) != 0 {
		t.Fatalf("archived leaked: %+v", r)
	}
	if r, _ = s.SearchAll("harbor", "all", 0); len(r.Chats) != 0 {
		t.Fatalf("archived chat leaked: %+v", r)
	}
	if _, err = s.db.Exec(`UPDATE conversations SET archived=0,user_visible=0 WHERE id=?`, conv); err != nil {
		t.Fatal(err)
	}
	if r, _ = s.SearchAll("lighthouse", "all", 0); len(r.Messages) != 0 {
		t.Fatalf("hidden leaked: %+v", r)
	}
}

func TestSearchOrderLimitAndIndexMaintenance(t *testing.T) {
	s, bot := searchFixture(t)
	conv := bot.DMConversationID
	var ids []string
	for i := 0; i < 5; i++ {
		m := putMsg(t, s, conv, "", fmt.Sprintf("needle number %d", i), fmt.Sprintf("2026-02-0%dT00:00:00.000000000Z", i+1))
		ids = append(ids, m.ID)
	}
	r, err := s.SearchAll("needle", "messages", 3)
	if err != nil || len(r.Messages) != 3 {
		t.Fatalf("limit %+v err=%v", r, err)
	}
	for i, m := range r.Messages {
		if m.ID != ids[4-i] {
			t.Fatalf("not newest first at %d: %s", i, m.Snippet)
		}
	}
	// Short queries are newest first too.
	r, _ = s.SearchAll("er", "messages", 2)
	if len(r.Messages) != 2 || r.Messages[0].ID != ids[4] {
		t.Fatalf("short %+v", r)
	}
	// Update and delete keep the index in step.
	if _, err = s.db.Exec(`UPDATE messages SET content='renamed gadget' WHERE id=?`, ids[4]); err != nil {
		t.Fatal(err)
	}
	if r, _ = s.SearchAll("number 4", "messages", 0); len(r.Messages) != 0 {
		t.Fatalf("stale index: %+v", r)
	}
	if r, _ = s.SearchAll("gadget", "messages", 0); len(r.Messages) != 1 {
		t.Fatalf("update not indexed: %+v", r)
	}
	if _, err = s.db.Exec(`DELETE FROM messages WHERE id=?`, ids[4]); err != nil {
		t.Fatal(err)
	}
	if r, _ = s.SearchAll("gadget", "messages", 0); len(r.Messages) != 0 {
		t.Fatalf("deleted still found: %+v", r)
	}
}

func TestSearchBackfillIsIdempotent(t *testing.T) {
	s, bot := searchFixture(t)
	putMsg(t, s, bot.DMConversationID, "", "before the index existed", "2026-03-01T00:00:00.000000000Z")
	// Drop the index as an old database would have it, then migrate twice.
	if _, err := s.db.Exec(`DROP TRIGGER messages_fts_ai; DROP TRIGGER messages_fts_ad; DROP TRIGGER messages_fts_au; DROP TABLE messages_fts`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrateSearch(s.db); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.SearchAll("the index", "messages", 0)
	if err != nil || len(r.Messages) != 1 {
		t.Fatalf("backfill %+v err=%v", r, err)
	}
}

func TestRouteSearch(t *testing.T) {
	s, bot := searchFixture(t)
	putMsg(t, s, bot.DMConversationID, "", "route needle text", "2026-04-01T00:00:00.000000000Z")
	srv := &Server{store: s}
	get := func(method, qs string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		if !srv.routeSearch(w, httptest.NewRequest(method, "/api/search?"+qs, nil), "search") {
			t.Fatal("route did not match")
		}
		return w
	}
	w := get(http.MethodGet, "q="+url.QueryEscape("needle")+"&kind=messages&limit=5")
	var res SearchResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.Messages) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = get(http.MethodGet, "q="); w.Code != 200 || !strings.Contains(w.Body.String(), `"messages":[]`) {
		t.Fatalf("empty %d %s", w.Code, w.Body)
	}
	if w = get(http.MethodGet, "q=x&kind=bogus"); w.Code != 400 {
		t.Fatalf("kind %d", w.Code)
	}
	if w = get(http.MethodGet, "q=x&limit=0"); w.Code != 400 {
		t.Fatalf("limit %d", w.Code)
	}
	if w = get(http.MethodPost, "q=x"); w.Code != 405 {
		t.Fatalf("post %d", w.Code)
	}
	if srv.routeSearch(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/other", nil), "other") {
		t.Fatal("matched another route")
	}
}
