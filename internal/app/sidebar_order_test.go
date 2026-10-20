package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func layoutFixture(t *testing.T, n int) (*Server, []string) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ids := []string{}
	for i := 0; i < n; i++ {
		b := mustBot(t, s.store, "layout-bot-"+string(rune('a'+i)))
		ids = append(ids, b.DMConversationID)
	}
	return s, ids
}

func listed(t *testing.T, s *Server) map[string]Conversation {
	t.Helper()
	all, err := s.store.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Conversation{}
	for _, c := range all {
		m[c.ID] = c
	}
	return m
}

func TestSidebarLayoutPersistsOrderAndPinsInListing(t *testing.T) {
	s, ids := layoutFixture(t, 4)
	h := s.Handler()
	code, out := primaryCall(t, h, "PUT", "/api/conversations/order", map[string]any{"pinned": []string{ids[2], ids[0]}, "order": []string{ids[3], ids[1]}})
	if code != 200 {
		t.Fatalf("code=%d out=%v", code, out)
	}
	got := listed(t, s)
	if !got[ids[2]].Pinned || got[ids[2]].PinOrder != 1 || !got[ids[0]].Pinned || got[ids[0]].PinOrder != 2 {
		t.Fatalf("pins=%+v %+v", got[ids[2]], got[ids[0]])
	}
	if got[ids[3]].SortOrder != 1 || got[ids[1]].SortOrder != 2 || got[ids[3]].Pinned {
		t.Fatalf("order=%+v %+v", got[ids[3]], got[ids[1]])
	}
	// The HTTP listing carries the same fields.
	code, out = primaryCall(t, h, "GET", "/api/conversations", nil)
	if code != 200 {
		t.Fatal(code)
	}
	for _, raw := range out["conversations"].([]any) {
		c := raw.(map[string]any)
		if c["id"] == ids[2] && (c["pinned"] != true || c["pin_order"] != float64(1)) {
			t.Fatalf("listing=%v", c)
		}
		if c["id"] == ids[3] && c["sort_order"] != float64(1) {
			t.Fatalf("listing=%v", c)
		}
	}
	// A later write replaces the whole pin set: ids[2] drops out.
	if code, _ = primaryCall(t, h, "PUT", "/api/conversations/order", map[string]any{"pinned": []string{ids[0]}, "order": []string{ids[2]}}); code != 200 {
		t.Fatal(code)
	}
	got = listed(t, s)
	if got[ids[2]].Pinned || got[ids[0]].PinOrder != 1 || got[ids[2]].SortOrder != 1 {
		t.Fatalf("after replace %+v %+v", got[ids[2]], got[ids[0]])
	}
}

func TestSidebarLayoutSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	a := mustBot(t, s.store, "reopen-a")
	b := mustBot(t, s.store, "reopen-b")
	if _, err = s.store.SetSidebarLayout([]string{b.DMConversationID}, []string{a.DMConversationID}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := listed(t, s)
	if got[b.DMConversationID].PinOrder != 1 || got[a.DMConversationID].SortOrder != 1 {
		t.Fatalf("reopen %+v %+v", got[b.DMConversationID], got[a.DMConversationID])
	}
}

func TestSidebarLayoutIsAtomicOnInvalidInput(t *testing.T) {
	s, ids := layoutFixture(t, 3)
	if _, err := s.store.SetSidebarLayout([]string{ids[0]}, []string{ids[1], ids[2]}); err != nil {
		t.Fatal(err)
	}
	before := listed(t, s)
	h := s.Handler()
	bad := []struct {
		name          string
		pinned, order []string
		code          int
	}{
		{"unknown id last", []string{ids[2]}, []string{ids[0], "missing"}, 404},
		{"duplicate in order", nil, []string{ids[0], ids[0]}, 400},
		{"in both lists", []string{ids[0]}, []string{ids[0]}, 400},
		{"empty id", []string{""}, nil, 400},
	}
	for _, c := range bad {
		code, out := primaryCall(t, h, "PUT", "/api/conversations/order", map[string]any{"pinned": c.pinned, "order": c.order})
		if code != c.code {
			t.Fatalf("%s: code=%d out=%v", c.name, code, out)
		}
		if !reflect.DeepEqual(listed(t, s), before) {
			t.Fatalf("%s: a rejected write changed the layout", c.name)
		}
	}
	too := make([]string, maxPinnedConversations+1)
	if code, _ := primaryCall(t, h, "PUT", "/api/conversations/order", map[string]any{"pinned": too, "order": []string{}}); code != 400 {
		t.Fatalf("oversize pinned code=%d", code)
	}
	if code, _ := primaryCall(t, h, "GET", "/api/conversations/order", nil); code == 200 {
		t.Fatal("GET must not be served")
	}
}

func TestArchivingUnpinsAndRestoreStaysUnpinned(t *testing.T) {
	s, ids := layoutFixture(t, 2)
	g, err := s.store.CreateGroup("pinned group", s.mustBotIDs(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetSidebarLayout([]string{g.ID, ids[0], ids[1]}, nil); err != nil {
		t.Fatal(err)
	}
	// Archiving a group unpins it.
	if _, err = s.store.SetGroupArchived(g.ID, true); err != nil {
		t.Fatal(err)
	}
	if listed(t, s)[g.ID].Pinned {
		t.Fatal("archived group still pinned")
	}
	if _, err = s.store.SetGroupArchived(g.ID, false); err != nil {
		t.Fatal(err)
	}
	if listed(t, s)[g.ID].Pinned {
		t.Fatal("restore must not re-pin")
	}
	// Archiving a Bot unpins its DM; other pins keep their slots.
	bot, err := s.store.GetConversation(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetBotArchived(bot.BotID, true); err != nil {
		t.Fatal(err)
	}
	got := listed(t, s)
	if got[ids[0]].Pinned || got[ids[0]].PinOrder != 0 {
		t.Fatalf("archived DM pinned: %+v", got[ids[0]])
	}
	if !got[ids[1]].Pinned {
		t.Fatalf("unrelated pin lost: %+v", got[ids[1]])
	}
	// An archived conversation cannot be pinned.
	if _, err = s.store.SetSidebarLayout([]string{ids[0]}, nil); err != ErrLayoutInvalid {
		t.Fatalf("pin archived err=%v", err)
	}
}

func (s *Server) mustBotIDs(t *testing.T) []string {
	t.Helper()
	bots, err := s.store.ListBots()
	if err != nil || len(bots) == 0 {
		t.Fatalf("bots=%v err=%v", bots, err)
	}
	ids := []string{}
	for _, b := range bots {
		ids = append(ids, b.ID)
	}
	return ids
}

func TestSidebarColumnsMigrateIdempotently(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Simulate a database from before the feature: drop the columns, then reopen twice.
	db, err := sql.Open("sqlite", filepath.Join(dir, "tofi.db"))
	if err == nil {
		_, err = db.Exec(`ALTER TABLE conversations DROP COLUMN pin_order`)
		if err == nil {
			_, err = db.Exec(`ALTER TABLE conversations DROP COLUMN sort_order`)
		}
		db.Close()
	}
	if err != nil {
		t.Skipf("cannot rewind schema here: %v", err)
	}
	for i := 0; i < 2; i++ {
		s, err = NewServer(Config{DataDir: dir})
		if err != nil {
			t.Fatalf("reopen %d: %v", i, err)
		}
		if _, err = s.store.ListConversations(true); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

// Pins and the manual order describe one account's own sidebar. An export is merged into another account (ids are
// remapped, the target has its own list), so it carries neither; this pins that decision down.
func TestPortableExportLeavesSidebarLayoutBehind(t *testing.T) {
	s, ids := layoutFixture(t, 2)
	if _, err := s.store.SetSidebarLayout([]string{ids[0]}, []string{ids[1]}); err != nil {
		t.Fatal(err)
	}
	b, err := s.store.exportPortable(context.Background(), s.instance.ID, portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Conversations) == 0 {
		t.Fatal("export holds no conversations; the check below would prove nothing")
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"pinned", "pin_order", "sort_order"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatalf("export carries sidebar field %q", field)
		}
	}
}
