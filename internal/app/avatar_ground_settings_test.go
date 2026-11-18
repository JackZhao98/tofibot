package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func setLookCall(look string) string {
	b, _ := json.Marshal(map[string]string{"action": "set", "key": "bot.avatar", "value": look})
	return string(b)
}

const (
	v1Rice = `{"schemaVersion":1,"shape":"rice","pattern":"solid","palette":"honey"}`
	v1Bean = `{"schemaVersion":1,"shape":"bean","pattern":"solid","palette":"ivory"}`
	v2Bean = `{"schemaVersion":2,"shape":"bean","pattern":"solid","palette":"ivory","ground":"teal"}`
)

// a: a Bot holds a ground and sets a v1 look. The audit holds what is stored, and both Undo paths restore the exact old text.
func TestSettingsAvatarV1WriteOverGroundAuditsStoredValueAndUndoes(t *testing.T) {
	for _, byPerson := range []bool{false, true} {
		e := newSettingsEnv(t)
		if err := e.s.store.SetBotAvatar(e.bot.ID, canonicalLook(t, groundLook)); err != nil {
			t.Fatal(err)
		}
		old, _ := e.s.store.botAvatar(e.bot.ID)
		out := e.mustCall(setLookCall(v1Rice))
		stored, _ := e.s.store.botAvatar(e.bot.ID)
		if out["applied"] != true || !strings.Contains(stored, `"ground":"teal"`) || !strings.Contains(stored, `"rice"`) {
			t.Fatalf("set: %+v stored %q", out, stored)
		}
		id := out["change_id"].(string)
		if byPerson {
			if _, err := e.s.revertSettingsChangeByPerson(context.Background(), id); err != nil {
				t.Fatalf("person's undo: %v", err)
			}
		} else if undone := e.mustCall(`{"action":"revert","change_id":"` + id + `"}`); undone["applied"] != true {
			t.Fatalf("tool undo: %+v", undone)
		}
		if got, _ := e.s.store.botAvatar(e.bot.ID); got != old {
			t.Fatalf("undo (person=%v) restored %q, want %q", byPerson, got, old)
		}
	}
}

// b: setting the same look again as v1 is a no-op, not a recorded change.
func TestSettingsAvatarSameV1LookOverGroundIsUnchanged(t *testing.T) {
	e := newSettingsEnv(t)
	if err := e.s.store.SetBotAvatar(e.bot.ID, canonicalLook(t, v2Bean)); err != nil {
		t.Fatal(err)
	}
	out := e.mustCall(setLookCall(v1Bean))
	if out["unchanged"] != true || out["applied"] != false {
		t.Fatalf("a same look was applied: %+v", out)
	}
	if rows, _ := e.s.store.recentSettingsChanges(10); len(rows) != 0 {
		t.Fatalf("a no-op was recorded: %+v", rows)
	}
}

// c: v1 look A, a v2 look B with a ground is set; Undo gives back exactly A, with no ground.
func TestSettingsAvatarUndoRemovesAGroundTheChangeAdded(t *testing.T) {
	for _, byPerson := range []bool{false, true} {
		e := newSettingsEnv(t)
		if err := e.s.store.SetBotAvatar(e.bot.ID, canonicalLook(t, v1Rice)); err != nil {
			t.Fatal(err)
		}
		old, _ := e.s.store.botAvatar(e.bot.ID)
		out := e.mustCall(setLookCall(v2Bean))
		id := out["change_id"].(string)
		if byPerson {
			if _, err := e.s.revertSettingsChangeByPerson(context.Background(), id); err != nil {
				t.Fatalf("person's undo: %v", err)
			}
		} else {
			e.mustCall(`{"action":"revert","change_id":"` + id + `"}`)
		}
		got, _ := e.s.store.botAvatar(e.bot.ID)
		if got != old || strings.Contains(got, "ground") {
			t.Fatalf("undo (person=%v) gave %q, want %q", byPerson, got, old)
		}
	}
}
