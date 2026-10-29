package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A Bot's own approval mode wins over the account's for that Bot's runs only;
// a change advances the review epoch; a Bot with none shows the account's mode.
func TestBotApprovalModeOverridesAccountMode(t *testing.T) {
	s := providerServer(t, newFakeOpenAI(t, "ok"), newFakeAnthropic(t, "ok"))
	a, err := s.store.CreateBot("synthetic a", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("synthetic b", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	modeOf := func(id string) string {
		t.Helper()
		x, err := readAutoReviewSettingsForBot(s.store.db, id)
		if err != nil {
			t.Fatal(err)
		}
		return x.Mode
	}
	shown := func(id string) string {
		t.Helper()
		bot, err := s.store.GetBot(id)
		if err != nil {
			t.Fatal(err)
		}
		return s.withEffectiveModel(t.Context(), bot).ApprovalMode
	}
	if modeOf(a.ID) != "off" || shown(a.ID) != "ask" {
		t.Fatalf("a Bot with no mode of its own follows the account: %s %s", modeOf(a.ID), shown(a.ID))
	}
	before, _ := s.store.getAutoReviewSettings()
	rec := providerRequest(t, s, http.MethodPatch, "/api/bots/"+a.ID, `{"approval_mode":"auto"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got Bot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ApprovalMode != "auto" {
		t.Fatalf("PATCH body=%s err=%v", rec.Body.String(), err)
	}
	after, _ := s.store.getAutoReviewSettings()
	if after.Revision != before.Revision+1 || after.Mode != "off" {
		t.Fatalf("a per-Bot change advances the epoch and leaves the account mode: before=%+v after=%+v", before, after)
	}
	if modeOf(a.ID) != "auto" || modeOf(b.ID) != "off" || shown(b.ID) != "ask" {
		t.Fatalf("override must reach only its Bot: a=%s b=%s", modeOf(a.ID), modeOf(b.ID))
	}
	// Ask keeps an account-wide Observe setting; it never turns auto on.
	if err := s.store.putAutoReviewMode("shadow"); err != nil {
		t.Fatal(err)
	}
	if rec := providerRequest(t, s, http.MethodPatch, "/api/bots/"+a.ID, `{"approval_mode":"ask"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if modeOf(a.ID) != "shadow" {
		t.Fatalf("ask under an Observe account stays observe-only, got %s", modeOf(a.ID))
	}
	// A Bot with none of its own follows an account in auto mode.
	if err := s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	if modeOf(b.ID) != "auto" || shown(b.ID) != "auto" || modeOf(a.ID) != "off" {
		t.Fatalf("follow/own under auto account: a=%s b=%s", modeOf(a.ID), modeOf(b.ID))
	}
	for _, body := range []string{`{"approval_mode":"always"}`, `{"approval_mode":"auto","name":"x"}`} {
		if rec := providerRequest(t, s, http.MethodPatch, "/api/bots/"+a.ID, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", body, rec.Code)
		}
	}
	if rec := providerRequest(t, s, http.MethodPatch, "/api/bots/missing", `{"approval_mode":"auto"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing Bot status=%d", rec.Code)
	}
}
