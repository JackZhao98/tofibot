package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func onboardingCall(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func decodeOnboarding(t *testing.T, rec *httptest.ResponseRecorder) onboardingState {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var st onboardingState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestOnboardingStateRoundTripAndRules(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if st := decodeOnboarding(t, onboardingCall(t, s, http.MethodGet, "/api/onboarding", "")); st.Step != 0 || st.Completed || st.Skipped {
		t.Fatalf("fresh state=%+v", st)
	}
	if st := decodeOnboarding(t, onboardingCall(t, s, http.MethodPut, "/api/onboarding", `{"step":2}`)); st.Step != 2 {
		t.Fatalf("step=%+v", st)
	}
	// The step never moves backwards.
	if st := decodeOnboarding(t, onboardingCall(t, s, http.MethodPut, "/api/onboarding", `{"step":1,"skipped":true}`)); st.Step != 2 || !st.Skipped || st.SkippedAt == "" {
		t.Fatalf("skip=%+v", st)
	}
	// Resuming clears the skip; completing records the time and clears it too.
	if st := decodeOnboarding(t, onboardingCall(t, s, http.MethodPut, "/api/onboarding", `{"skipped":false}`)); st.Skipped {
		t.Fatalf("resume=%+v", st)
	}
	st := decodeOnboarding(t, onboardingCall(t, s, http.MethodPut, "/api/onboarding", `{"step":3,"completed":true}`))
	if !st.Completed || st.CompletedAt == "" || st.Step != 3 || st.Skipped {
		t.Fatalf("complete=%+v", st)
	}
	// A later skip cannot undo completion.
	if st = decodeOnboarding(t, onboardingCall(t, s, http.MethodPut, "/api/onboarding", `{"skipped":true}`)); !st.Completed || st.Skipped {
		t.Fatalf("skip after complete=%+v", st)
	}
	for _, body := range []string{`{}`, `{"step":0}`, `{"step":4}`, `{"bogus":1}`, `not json`} {
		if rec := onboardingCall(t, s, http.MethodPut, "/api/onboarding", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s status=%d", body, rec.Code)
		}
	}
	if rec := onboardingCall(t, s, http.MethodDelete, "/api/onboarding", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete status=%d", rec.Code)
	}
	// Preferences written before or after keep their own values.
	if rec := onboardingCall(t, s, http.MethodPut, "/api/preferences", `{"language":"de"}`); rec.Code != http.StatusOK {
		t.Fatalf("language %d %s", rec.Code, rec.Body.String())
	}
	if st = decodeOnboarding(t, onboardingCall(t, s, http.MethodGet, "/api/onboarding", "")); !st.Completed || st.Step != 3 {
		t.Fatalf("state lost after preference write: %+v", st)
	}
}

func TestOnboardingStateIsPerAccount(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "alice", "alice@example.test", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	adminCookie, userCookie := accountCookie(t, g, admin), accountCookie(t, g, user)
	changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, userCookie)
	if changed.Code != http.StatusOK {
		t.Fatalf("password %d %s", changed.Code, changed.Body.String())
	}
	userCookie = changed.Result().Cookies()[0]
	if w := accountRequest(g, "GET", "/api/onboarding", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read status=%d", w.Code)
	}
	if w := accountRequest(g, "PUT", "/api/onboarding", `{"step":3,"completed":true}`, adminCookie); w.Code != http.StatusOK {
		t.Fatalf("admin write %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "PUT", "/api/onboarding", `{"step":2,"skipped":true}`, userCookie); w.Code != http.StatusOK {
		t.Fatalf("user write %d %s", w.Code, w.Body.String())
	}
	read := func(c *http.Cookie) onboardingState {
		w := accountRequest(g, "GET", "/api/onboarding", "", c)
		return decodeOnboarding(t, w)
	}
	if a := read(adminCookie); !a.Completed || a.Skipped || a.Step != 3 {
		t.Fatalf("admin=%+v", a)
	}
	if u := read(userCookie); u.Completed || !u.Skipped || u.Step != 2 {
		t.Fatalf("user=%+v", u)
	}
}

func firstBotServer(t *testing.T, withModel bool) *Server {
	t.Helper()
	cfg := Config{DataDir: t.TempDir(), DefaultModel: "global-model", Provider: "local"}
	if withModel {
		cfg.Engine = &captureEngine{}
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFirstBotIsPreCreatedOnlyWhenNoneExist(t *testing.T) {
	// No model yet: nothing is created.
	noModel := firstBotServer(t, false)
	if rec := onboardingCall(t, noModel, http.MethodPost, "/api/onboarding/first-bot", `{}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "model_required") {
		t.Fatalf("without a model: %d %s", rec.Code, rec.Body.String())
	}
	if bots, _ := noModel.store.ListBots(true); len(bots) != 0 {
		t.Fatalf("bots created without a model: %d", len(bots))
	}

	s := firstBotServer(t, true)
	rec := onboardingCall(t, s, http.MethodPost, "/api/onboarding/first-bot", `{"locale":"de"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var made struct {
		Bot     Bot  `json:"bot"`
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil || !made.Created || made.Bot.Name != onboardingBotName || made.Bot.Model != followGlobalModel {
		t.Fatalf("created=%+v err=%v", made, err)
	}
	messages, _, err := s.store.Messages(made.Bot.DMConversationID, 0, 10)
	if err != nil || len(messages) != 1 || messages[0].Content != onboardingWelcome("de") {
		t.Fatalf("opening message=%+v err=%v", messages, err)
	}
	if pending, err := s.store.onboardingPending(made.Bot.ID); err != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}

	// Calling again, even with a fresh request id, returns the same Bot.
	again := onboardingCall(t, s, http.MethodPost, "/api/onboarding/first-bot", `{"client_creation_id":"2f2a7e0e-5b0e-4f55-9d0c-0a4a3c5b8d11"}`)
	var second struct {
		Bot     Bot  `json:"bot"`
		Created bool `json:"created"`
	}
	if again.Code != http.StatusOK || json.Unmarshal(again.Body.Bytes(), &second) != nil || second.Created || second.Bot.ID != made.Bot.ID {
		t.Fatalf("second call %d %s", again.Code, again.Body.String())
	}

	// An account whose Bot was made some other way never gets another one.
	other := firstBotServer(t, true)
	existing, err := other.store.CreateBot("existing", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	rec = onboardingCall(t, other, http.MethodPost, "/api/onboarding/first-bot", `{}`)
	var kept struct {
		Bot     Bot  `json:"bot"`
		Created bool `json:"created"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &kept) != nil || kept.Created || kept.Bot.ID != existing.ID {
		t.Fatalf("existing Bot: %d %s", rec.Code, rec.Body.String())
	}
	if bots, _ := other.store.ListBots(true); len(bots) != 1 {
		t.Fatalf("bot count=%d", len(bots))
	}
}

func TestFirstBotRefusesWhenABotAppearsInTheTransaction(t *testing.T) {
	s := firstBotServer(t, true)
	if _, err := s.store.CreateBot("racer", "", "model"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.createOnboardingBot("2f2a7e0e-5b0e-4f55-9d0c-0a4a3c5b8d12", followGlobalModel, followGlobalModel, "en", true); err != errBotsExist {
		t.Fatalf("err=%v", err)
	}
}

// The opening message asks for a name and for the work, in every UI language,
// and the model-facing onboarding prompt carries the name rules.
func TestOnboardingOpeningMessageAndPrompt(t *testing.T) {
	for language := range uiLanguages {
		text := onboardingWelcome(language)
		if text == "" || (language != "en" && text == onboardingWelcome("en")) {
			t.Fatalf("language %s has no distinct opening message: %q", language, text)
		}
		if strings.Contains(text, "\n") || len([]rune(text)) > 200 {
			t.Fatalf("opening message for %s is not short: %q", language, text)
		}
	}
	if len(onboardingWelcomes) != len(uiLanguages) {
		t.Fatalf("welcomes=%d languages=%d", len(onboardingWelcomes), len(uiLanguages))
	}
	if got := onboardingWelcome("xx"); got != onboardingWelcome("en") {
		t.Fatalf("unknown language did not fall back: %q", got)
	}
	en := onboardingWelcome("en")
	if !strings.Contains(en, "called") || !strings.Contains(en, "you pick") || !strings.Contains(en, "help with") {
		t.Fatalf("english opening misses a question: %q", en)
	}

	prompt := onboardingSystemPrompt
	for _, want := range []string{"use it exactly", "pick", "set_bot_profile", "at most one clarification question at a time", "placeholder"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("onboarding prompt lacks %q", want)
		}
	}
}
