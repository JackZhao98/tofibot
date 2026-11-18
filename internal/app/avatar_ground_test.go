package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const groundLook = `{"schemaVersion":2,"shape":"loaf","pattern":"tabby","palette":"tabby","ground":"teal"}`

func TestBotGroundIsValidatedAndKeptByVersionOneWrites(t *testing.T) {
	e := newSettingsEnv(t)
	h := e.s.Handler()
	code, bot := apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":`+groundLook+`}`)
	if look, _ := bot["avatar"].(map[string]any); code != 200 || look["ground"] != "teal" || look["schemaVersion"] != float64(2) {
		t.Fatalf("v2 write: %d %+v", code, bot)
	}
	// The list read carries it.
	_, list := apiJSON(t, h, http.MethodGet, "/api/bots", "")
	if look, _ := list["bots"].([]any)[0].(map[string]any)["avatar"].(map[string]any); look["ground"] != "teal" {
		t.Fatalf("list: %+v", list)
	}
	// An older client writes version 1: the look changes, the ground stays.
	code, bot = apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":{"schemaVersion":1,"shape":"curl","pattern":"solid","palette":"ivory"}}`)
	look, _ := bot["avatar"].(map[string]any)
	if code != 200 || look["ground"] != "teal" || look["shape"] != "curl" || look["schemaVersion"] != float64(2) {
		t.Fatalf("v1 write: %d %+v", code, bot)
	}
	// The settings tool writes through the same store call.
	if err := e.s.store.SetBotAvatar(e.bot.ID, canonicalLook(t, `{"shape":"tall","pattern":"solid","palette":"honey"}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.store.botAvatar(e.bot.ID); !strings.Contains(got, `"ground":"teal"`) || !strings.Contains(got, `"tall"`) {
		t.Fatalf("store write dropped the ground: %q", got)
	}
	// A v2 write replaces it; null clears everything, ground included.
	apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":{"schemaVersion":2,"shape":"loaf","pattern":"solid","palette":"ivory","ground":"lime"}}`)
	if got, _ := e.s.store.botAvatar(e.bot.ID); !strings.Contains(got, `"lime"`) {
		t.Fatalf("v2 replace: %q", got)
	}
	apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":null}`)
	if got, _ := e.s.store.botAvatar(e.bot.ID); got != "" {
		t.Fatalf("clear: %q", got)
	}
	apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":{"shape":"loaf","pattern":"solid","palette":"ivory"}}`)
	if got, _ := e.s.store.botAvatar(e.bot.ID); strings.Contains(got, "ground") {
		t.Fatalf("a ground came from nowhere: %q", got)
	}
	// Invalid grounds and unknown keys stay refused; a v1 look cannot carry a ground.
	for _, bad := range []string{
		`{"schemaVersion":2,"shape":"loaf","pattern":"solid","palette":"ivory"}`,
		`{"schemaVersion":2,"shape":"loaf","pattern":"solid","palette":"ivory","ground":"pink"}`,
		`{"schemaVersion":1,"shape":"loaf","pattern":"solid","palette":"ivory","ground":"teal"}`,
		`{"schemaVersion":3,"shape":"loaf","pattern":"solid","palette":"ivory","ground":"teal"}`,
		`{"schemaVersion":2,"shape":"loaf","pattern":"solid","palette":"ivory","ground":"teal","hue":1}`,
	} {
		if code, body := apiJSON(t, h, http.MethodPatch, "/api/bots/"+e.bot.ID, `{"avatar":`+bad+`}`); code != 400 {
			t.Errorf("%s: %d %+v", bad, code, body)
		}
	}
}

func TestBotGroundSurvivesExportAndImportAndOldExportsStillImport(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Synthetic Bot", "data only", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBotAvatar(bot.ID, canonicalLook(t, groundLook)); err != nil {
		t.Fatal(err)
	}
	bundle, err := s.exportPortable(context.Background(), "source-instance", portableSelection{}, "account")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Bots) != 1 || !strings.Contains(string(bundle.Bots[0].Avatar), `"ground":"teal"`) {
		t.Fatalf("export lost the ground: %+v", bundle.Bots)
	}
	importLook := func(raw string) []string {
		bundle.Bots[0].Avatar = json.RawMessage(raw)
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
		looks, _ := target.botAvatars()
		out := []string{}
		for _, look := range looks {
			out = append(out, look)
		}
		return out
	}
	if looks := importLook(groundLook); len(looks) != 1 || !strings.Contains(looks[0], `"ground":"teal"`) {
		t.Fatalf("v2 import lost the ground: %v", looks)
	}
	// An export from before the ground (version 1) imports with no ground.
	if looks := importLook(sampleLook); len(looks) != 1 || strings.Contains(looks[0], "ground") {
		t.Fatalf("v1 import: %v", looks)
	}
	// An invalid ground drops the whole look, as any other invalid avatar field does.
	if looks := importLook(`{"schemaVersion":2,"shape":"loaf","pattern":"tabby","palette":"tabby","ground":"pink"}`); len(looks) != 0 {
		t.Fatalf("invalid ground imported: %v", looks)
	}
}
