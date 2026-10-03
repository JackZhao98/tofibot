package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func portableFixture(t *testing.T) (*Store, portableBundle) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bot, err := s.CreateBot("Synthetic Bot", "Do not execute on import.", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateBot("Synthetic Colleague", "data only", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateGroup("Synthetic Team", []string{bot.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, conv := range []string{bot.DMConversationID, group.ID} {
		if _, _, err = s.AddMessage(conv, "user", "", "", "Original fact, 中文, source https://example.test/fixture.", "fixture"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.AddMemoryWithMetadata(conv, bot.ID, MemoryInput{Content: "Preserve this fact.", Title: "Fixture memory", Description: "Original provenance"}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateSchedule(conv, bot.ID, ScheduleSpec{Content: "Must stay paused.", Title: "Fixture schedule", Description: "Metadata survives", Kind: "once", Timezone: "UTC", RunAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`UPDATE messages SET kind='ui_card',notice_data='{"type":"fixture","action":"do-not-execute"}' WHERE conversation_id=?`, bot.DMConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.putUserTimezone("UTC", false); err != nil {
		t.Fatal(err)
	}
	b, err := s.exportPortable(context.Background(), "synthetic-source-instance", portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	return s, b
}

func TestPortableRoundTripMetadataIDsAndNoExecution(t *testing.T) {
	_, b := portableFixture(t)
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	selected, err := selectPortable(b, portableSelection{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.previewPortable(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.applyPortable(context.Background(), selected, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Counts, selected.Counts) {
		t.Fatal("counts changed")
	}
	for old, id := range result.IDMap {
		if old == id || !portableID(id) {
			t.Fatal("IDs not remapped")
		}
	}
	for _, x := range selected.Bots {
		got, err := s.GetBot(result.IDMap[x.ID])
		if err != nil || got.Instructions != x.Instructions || got.DMConversationID != result.IDMap[x.DMConversationID] {
			t.Fatalf("Bot config/references lost: %v", err)
		}
	}
	for _, x := range selected.Memories {
		got, err := s.GetMemory(result.IDMap[x.ID])
		if err != nil || got.Title != x.Title || got.Description != x.Description || got.Content != x.Content || got.BotID != result.IDMap[x.BotID] {
			t.Fatalf("memory lost: %v", err)
		}
	}
	var active int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM schedules WHERE status<>'paused'`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("schedule not paused: %d %v", active, err)
	}
	for _, table := range []string{"runs", "questions", "mcp_call_approvals", "computer_jobs", "schedule_occurrences", "owner_sessions", "account_sessions"} {
		var exists int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			continue
		}
		var count int
		err = s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count)
		if err != nil && table == "account_sessions" {
			continue
		}
		if err != nil || count != 0 {
			t.Fatalf("execution/auth rows in %s: %d %v", table, count, err)
		}
	}
	for _, x := range selected.Messages {
		var content, kind string
		var run any
		if err = s.db.QueryRow(`SELECT content,kind,run_id FROM messages WHERE id=?`, result.IDMap[x.ID]).Scan(&content, &kind, &run); err != nil || content != x.Content || run != nil || kind == "ui_card" {
			t.Fatalf("history not inert: %v", err)
		}
	}
	reexport, err := s.exportPortable(context.Background(), "synthetic-destination", portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	if reexport.Messages[0].Origin.InstanceID != "synthetic-source-instance" {
		t.Fatal("source provenance lost on re-export")
	}
	if reexport.Schedules[0].Origin.Status != "active" || reexport.Schedules[0].Status != "paused" {
		t.Fatal("original schedule state not retained as provenance")
	}
}

func TestPortableStandaloneExcludesGroupsAndReportsMissingAssets(t *testing.T) {
	s, b := portableFixture(t)
	id := b.Bots[0].ID
	if _, err := s.AddAttachment(b.Bots[0].DMConversationID, "synthetic.txt", "text/plain", strings.NewReader("synthetic asset")); err != nil {
		t.Fatal(err)
	}
	single, err := s.exportPortable(context.Background(), "source", portableSelection{Categories: []string{"bot_config", "chats", "memories", "schedules"}, BotIDs: []string{id}}, "bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Bots) != 1 || single.SkippedGroups != 1 || single.AttachmentCount != 1 {
		t.Fatalf("wrong standalone scope: %+v", single.Counts)
	}
	for _, c := range single.Conversations {
		if c.Kind != "dm" {
			t.Fatal("group silently exported")
		}
	}
	if _, err = s.exportPortable(context.Background(), "source", portableSelection{Categories: []string{"bot_config"}, BotIDs: []string{"00000000-0000-0000-0000-000000000099"}}, "bot"); err == nil {
		t.Fatal("foreign Bot export accepted")
	}
}

func TestPortableRollbackThenRestartAndIdempotentApply(t *testing.T) {
	_, b := portableFixture(t)
	b, _ = selectPortable(b, portableSelection{})
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fixture_failure BEFORE INSERT ON schedules BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyPortable(context.Background(), b, p.ID); err == nil {
		t.Fatal("failure did not propagate")
	}
	for _, table := range []string{"bots", "conversations", "members", "messages", "memories", "schedules", "portability_provenance", "workspace_events"} {
		var count int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial import %s count=%d err=%v", table, count, err)
		}
	}
	var status string
	if err = s.db.QueryRow(`SELECT status FROM portability_imports WHERE id=?`, p.ID).Scan(&status); err != nil || status != "preview" {
		t.Fatal("journal prematurely committed")
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fixture_failure`); err != nil {
		t.Fatal(err)
	}
	first, err := s.applyPortable(context.Background(), b, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	second, err := s.applyPortable(context.Background(), b, p.ID)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("restart duplicate changed result: %v", err)
	}
	bots, _ := s.ListBots(true)
	if len(bots) != len(b.Bots) {
		t.Fatal("duplicate imported twice")
	}
	// Options/package binding also applies to already committed idempotent requests.
	b.Bots[0].Instructions = "modified"
	if _, err = s.applyPortable(context.Background(), b, p.ID); err == nil {
		t.Fatal("changed payload reused journal")
	}
}

func TestPortableDestinationChangeRequiresNewPreview(t *testing.T) {
	b, _ := parsePortableBundle([]byte(portableLegacyFixture))
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateBot("Concurrent Bot", "fixture", "model"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyPortable(context.Background(), b, p.ID); err == nil {
		t.Fatal("stale target accepted")
	}
}

func TestPortableConcurrentApplyAndSettingsOptIn(t *testing.T) {
	_, original := portableFixture(t)
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.putUserTimezone("Asia/Tokyo", false); err != nil {
		t.Fatal(err)
	}
	b, err := selectPortable(original, portableSelection{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]portableResult, 2)
	errors := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errors[i] = s.applyPortable(context.Background(), b, p.ID) }(i)
	}
	wg.Wait()
	if errors[0] != nil || errors[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("concurrent retry: %v %v", errors[0], errors[1])
	}
	zone, err := s.userTimezone()
	if err != nil || zone != "Asia/Tokyo" {
		t.Fatalf("default overwrote settings: %q %v", zone, err)
	}
	withSettings, err := selectPortable(original, portableSelection{Categories: original.Included})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.previewPortable(context.Background(), withSettings)
	if err != nil || len(p.Conflicts) == 0 {
		t.Fatalf("settings replacement not disclosed: %v", err)
	}
	if _, err = s.applyPortable(context.Background(), withSettings, p.ID); err != nil {
		t.Fatal(err)
	}
	zone, err = s.userTimezone()
	if err != nil || zone != "UTC" {
		t.Fatalf("opt-in settings missing: %q %v", zone, err)
	}
}

func TestPortableAccountIsolationAndCSRF(t *testing.T) {
	g := accountFixture(t)
	a, err := g.create(context.Background(), "fixture-a", "", "SyntheticPassword123!", true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.create(context.Background(), "fixture-b", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, b.ID); err != nil {
		t.Fatal(err)
	}
	ac, bc := accountCookie(t, g, a), accountCookie(t, g, b)
	body, _ := json.Marshal(portableImportRequest{Bundle: json.RawMessage(portableLegacyFixture)})
	w := accountRequest(g, "POST", "/api/portability/preview", string(body), ac)
	if w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var preview portablePreview
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(portableImportRequest{Bundle: json.RawMessage(portableLegacyFixture), PreviewID: preview.ID})
	w = accountRequest(g, "POST", "/api/portability/apply", string(body), bc)
	if w.Code != 409 {
		t.Fatalf("foreign-account preview accepted: %d", w.Code)
	}
	w = accountRequest(g, "POST", "/api/portability/apply", string(body), nil)
	if w.Code != 401 {
		t.Fatal("anonymous import accepted")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/portability/apply", strings.NewReader(string(body)))
	r.AddCookie(ac)
	r.Header.Set("Origin", "https://evil.example.test")
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site apply accepted")
	}
	w = accountRequest(g, "POST", "/api/portability/apply", string(body), ac)
	if w.Code != 200 {
		t.Fatalf("own apply rejected: %d %s", w.Code, w.Body.String())
	}
	ws, err := g.workspace(b)
	if err != nil {
		t.Fatal(err)
	}
	bots, _ := ws.store.ListBots(true)
	if len(bots) != 0 {
		t.Fatal("sibling workspace modified")
	}
}
