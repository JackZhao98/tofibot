package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestLibraryListsActiveBotsThatTurnedAnOfficialSkillOff(t *testing.T) {
	env := newLibEnv(t)
	official := officialNames()[0]
	listing := func() librarySkillView {
		status, body := extensionHTTP(t, env.handler, http.MethodGet, "/api/extensions/skills", nil)
		if status != http.StatusOK {
			t.Fatal(status, string(body))
		}
		var out struct {
			Skills []librarySkillView `json:"skills"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		for _, v := range out.Skills {
			if v.Name == official {
				return v
			}
		}
		t.Fatalf("%s missing from the library", official)
		return librarySkillView{}
	}
	if v := listing(); len(v.OffBots) != 0 || v.EnabledBots != v.BotsTotal {
		t.Fatalf("nothing is off yet: %+v", v)
	}
	for _, b := range []Bot{env.b, env.a} {
		if status, body := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+b.ID+"/skills/"+official, map[string]any{"enabled": false}); status != http.StatusOK {
			t.Fatal(status, string(body))
		}
	}
	v := listing()
	if len(v.OffBots) != 2 || v.OffBots[0].Name != "Alpha Bot" || v.OffBots[1].Name != "Bravo Bot" || v.OffBots[0].ID != env.a.ID {
		t.Fatalf("off bots not listed by name: %+v", v.OffBots)
	}
	if v.EnabledBots != v.BotsTotal-2 {
		t.Fatalf("enabled %d of %d", v.EnabledBots, v.BotsTotal)
	}
	if _, err := env.s.store.db.Exec(`UPDATE bots SET archived=1 WHERE id=?`, env.b.ID); err != nil {
		t.Fatal(err)
	}
	if v := listing(); len(v.OffBots) != 1 || v.OffBots[0].ID != env.a.ID {
		t.Fatalf("archived Bot listed: %+v", v.OffBots)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/skills/"+official, map[string]any{"enabled": true}); status != http.StatusOK {
		t.Fatal(status)
	}
	if v := listing(); len(v.OffBots) != 0 {
		t.Fatalf("still listed after turning on: %+v", v.OffBots)
	}
}
