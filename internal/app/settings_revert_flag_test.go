package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// After a successful revert the change is no longer revertable, in the revert answer and in the list.
func TestRevertedChangeIsNotRevertableAnyMore(t *testing.T) {
	e := newSettingsEnv(t)
	out := e.mustCall(`{"action":"set","key":"account.language","value":"de"}`)
	h := e.s.Handler()
	do := func(method, path string) (int, map[string]any) {
		r := httptest.NewRequest(method, "http://settings.local"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	id := out["change_id"].(string)
	code, body := do(http.MethodPost, "/api/settings-changes/"+id+"/revert")
	if code != 200 {
		t.Fatalf("revert: %d %+v", code, body)
	}
	if change, _ := body["change"].(map[string]any); change["revertable"] != false {
		t.Fatalf("the revert answer still says revertable: %+v", change)
	}
	_, list := do(http.MethodGet, "/api/settings-changes")
	for _, row := range list["changes"].([]any) {
		if m := row.(map[string]any); m["id"] == id && m["revertable"] != false {
			t.Fatalf("the list still says revertable: %+v", m)
		}
	}
	if code, _ := do(http.MethodPost, "/api/settings-changes/"+id+"/revert"); code != 409 {
		t.Fatalf("second revert: %d", code)
	}
}
