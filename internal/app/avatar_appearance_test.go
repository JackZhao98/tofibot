package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func apiJSON(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, "http://avatar.local"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

const sampleLook = `{"schemaVersion":1,"shape":"loaf","pattern":"tabby","palette":"tabby"}`

func TestBotAvatarIsStoredOnTheServerAndFollowsThePerson(t *testing.T) {
	e := newSettingsEnv(t)
	h := e.s.Handler()
	code, bot := apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":`+sampleLook+`}`)
	if code != 200 || bot["avatar"] == nil {
		t.Fatalf("patch: %d %+v", code, bot)
	}
	look, _ := bot["avatar"].(map[string]any)
	if look["shape"] != "loaf" || look["pattern"] != "tabby" || look["palette"] != "tabby" || look["schemaVersion"] != float64(1) {
		t.Fatalf("avatar: %+v", look)
	}
	// Another device reads the same list.
	code, list := apiJSON(t, h, http.MethodGet, "/api/bots", "")
	bots, _ := list["bots"].([]any)
	if code != 200 || len(bots) != 1 || bots[0].(map[string]any)["avatar"] == nil {
		t.Fatalf("list: %d %+v", code, list)
	}
	// A look the picker cannot draw is refused and changes nothing.
	for _, bad := range []string{`{"shape":"loaf","pattern":"tabby","palette":"ocean"}`, `{"shape":"nope","pattern":"solid","palette":"ivory"}`, `{"schemaVersion":2,"shape":"loaf","pattern":"solid","palette":"ivory"}`, `{"shape":"loaf","pattern":"solid","palette":"ivory","extra":1}`, `"loaf"`, `[]`} {
		if code, body := apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":`+bad+`}`); code != 400 {
			t.Errorf("%s: %d %+v", bad, code, body)
		}
	}
	if got, _ := e.s.store.botAvatar(e.bot.ID); !strings.Contains(got, `"loaf"`) {
		t.Fatalf("a refused look changed the stored one: %q", got)
	}
	// null clears it; an omitted field leaves it.
	apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"name":"Renamed"}`)
	if got, _ := e.s.store.botAvatar(e.bot.ID); got == "" {
		t.Fatal("an update without avatar cleared the look")
	}
	if code, cleared := apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":null}`); code != 200 || cleared["avatar"] != nil {
		t.Fatalf("clear: %d %+v", code, cleared)
	}
}

func TestBotAvatarMigrationIsIdempotent(t *testing.T) {
	e := newSettingsEnv(t)
	for i := 0; i < 2; i++ {
		if err := migrateBotAvatar(e.s.store.db); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBotAvatarSurvivesExportAndImport(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Synthetic Bot", "data only", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBotAvatar(bot.ID, canonicalLook(t, sampleLook)); err != nil {
		t.Fatal(err)
	}
	bundle, err := s.exportPortable(context.Background(), "source-instance", portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Bots) != 1 || !strings.Contains(string(bundle.Bots[0].Avatar), `"tabby"`) {
		t.Fatalf("export: %+v", bundle.Bots)
	}
	target, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	selected, err := selectPortable(bundle, portableSelection{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := target.previewPortable(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.applyPortable(context.Background(), selected, p.ID); err != nil {
		t.Fatal(err)
	}
	looks, err := target.botAvatars()
	if err != nil || len(looks) != 1 {
		t.Fatalf("imported looks: %v %v", looks, err)
	}
	for _, look := range looks {
		if !strings.Contains(look, `"loaf"`) {
			t.Fatalf("imported look %q", look)
		}
	}
	// A look the picker cannot draw is dropped on import, not an error.
	bundle.Bots[0].Avatar = json.RawMessage(`{"UserChosenMetadata":"Preserved"}`)
	other, _ := OpenStore(t.TempDir())
	defer other.Close()
	selected, _ = selectPortable(bundle, portableSelection{})
	if p, err = other.previewPortable(context.Background(), selected); err != nil {
		t.Fatal(err)
	}
	if _, err := other.applyPortable(context.Background(), selected, p.ID); err != nil {
		t.Fatal(err)
	}
	if looks, _ := other.botAvatars(); len(looks) != 0 {
		t.Fatalf("unknown look imported: %v", looks)
	}
}

func canonicalLook(t *testing.T, raw string) string {
	t.Helper()
	look, err := normalizeAvatar(raw)
	if err != nil {
		t.Fatal(err)
	}
	return look
}

func TestSettingsToolChangesAvatarAndAppearance(t *testing.T) {
	e := newSettingsEnv(t)
	// Own look: direct, recorded, undoable.
	out := e.mustCall(`{"action":"set","key":"bot.avatar","value":"{\"shape\":\"rice\",\"pattern\":\"solid\",\"palette\":\"honey\"}"}`)
	if out["applied"] != true || out["revertable"] != true {
		t.Fatalf("avatar: %+v", out)
	}
	if got, _ := e.s.store.botAvatar(e.bot.ID); !strings.Contains(got, `"rice"`) || !strings.Contains(got, `"honey"`) {
		t.Fatalf("stored %q", got)
	}
	got := e.mustCall(`{"action":"get","key":"bot.avatar"}`)
	if got["risk"] != "direct" || !strings.Contains(got["note"].(string), "Shapes:") {
		t.Fatalf("get: %+v", got)
	}
	e.refused(`{"action":"set","key":"bot.avatar","value":"{\"shape\":\"rice\",\"pattern\":\"solid\",\"palette\":\"nope\"}"}`, "settings_invalid_value")
	if undone := e.mustCall(`{"action":"revert","change_id":"` + out["change_id"].(string) + `"}`); undone["applied"] != true {
		t.Fatalf("revert: %+v", undone)
	}
	if got, _ := e.s.store.botAvatar(e.bot.ID); got != "" {
		t.Fatalf("avatar after revert %q", got)
	}
	// Another Bot's look needs a card, and the card says whose avatar changes.
	other, _ := e.s.store.CreateBot("beta", "", "model")
	in := `{"action":"set","key":"bot.avatar","bot_id":"` + other.ID + `","value":"{\"shape\":\"bean\",\"pattern\":\"solid\",\"palette\":\"ivory\"}"}`
	card := e.decide(e.needApproval(in), true)
	if card.Approval.Facts.Setting.EffectCode != "bot_avatar" || card.Approval.Facts.Setting.TargetName != "beta" {
		t.Fatalf("facts: %+v", card.Approval.Facts.Setting)
	}
	e.mustCall(in)

	// The theme follows the person: stored on the server, read by every device.
	e.mustCall(`{"action":"set","key":"account.appearance","value":"dark"}`)
	prefs, _ := e.s.store.userPreferences()
	if prefs.Appearance != "dark" {
		t.Fatalf("appearance %q", prefs.Appearance)
	}
	code, body := apiJSON(t, e.s.Handler(), http.MethodGet, "/api/preferences", "")
	if code != 200 || body["appearance"] != "dark" {
		t.Fatalf("GET /api/preferences: %d %+v", code, body)
	}
	e.refused(`{"action":"set","key":"account.appearance","value":"sepia"}`, "settings_invalid_value")
}

func TestPreferencesAppearanceRoundTrip(t *testing.T) {
	e := newSettingsEnv(t)
	h := e.s.Handler()
	if code, body := apiJSON(t, h, http.MethodGet, "/api/preferences", ""); code != 200 || body["appearance"] != "" {
		t.Fatalf("a fresh workspace has no choice: %d %+v", code, body)
	}
	if code, body := apiJSON(t, h, http.MethodPut, "/api/preferences", `{"appearance":"light"}`); code != 200 || body["appearance"] != "light" {
		t.Fatalf("put: %d %+v", code, body)
	}
	if code, _ := apiJSON(t, h, http.MethodPut, "/api/preferences", `{"appearance":"neon"}`); code != 400 {
		t.Fatalf("invalid theme: %d", code)
	}
	if code, body := apiJSON(t, h, http.MethodPut, "/api/preferences", `{"appearance":""}`); code != 200 || body["appearance"] != "" {
		t.Fatalf("clear: %d %+v", code, body)
	}
	for i := 0; i < 2; i++ {
		if err := migrateUserPreferences(e.s.store.db); err != nil {
			t.Fatal(err)
		}
	}
}
