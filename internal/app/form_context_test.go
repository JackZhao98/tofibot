package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnsweredFormValuesSurviveNextTurnWithoutPrivateReferences(t *testing.T) {
	s, c, run, q := formFixture(t)
	answered, _, err := s.AnswerUserForm(q.ID, "human", formValues())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.store.FinishRun(run.ID, c.ID, run.BotID, "Registration submitted."); err != nil {
		t.Fatal(err)
	}
	_, next, _, err := s.store.AddUserRun(c.ID, run.BotID, "What ordinary details did I supply?", "form-context-next")
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := s.store.GetBot(run.BotID)
	messages, _ := s.buildContextParts(c, next, bot)
	raw, _ := json.Marshal(messages)
	for _, value := range []string{formValues()["email"], formValues()["notes"]} {
		if !strings.Contains(string(raw), value) {
			t.Fatalf("next-turn context lost supplied ordinary value %q", value)
		}
	}
	var answers map[string]json.RawMessage
	_ = json.Unmarshal(answered.Answer, &answers)
	var secret formSecretAnswer
	_ = json.Unmarshal(answers["password"], &secret)
	for _, forbidden := range []string{formValues()["password"], secret.SecretRef, "secret_ref"} {
		if forbidden != "" && strings.Contains(string(raw), forbidden) {
			t.Fatal("private form value/reference crossed the next-turn context boundary")
		}
	}
}

func TestAnsweredFormContextBoundaries(t *testing.T) {
	s, c, run, q := formFixture(t)
	older, err := s.store.AddRun(c.ID, run.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AnswerUserForm(q.ID, "human", formValues()); err != nil {
		t.Fatal(err)
	}
	next, err := s.store.AddRun(c.ID, run.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.store.answeredFormContext(c.ID, next, maxFormContextRunes); got == "" {
		t.Fatal("expected prior ordinary answers")
	}
	otherBot := next
	otherBot.BotID = "not-the-original-bot"
	for name, current := range map[string]Run{"future answer": older, "current run": run, "other bot": otherBot} {
		if got := s.store.answeredFormContext(c.ID, current, maxFormContextRunes); got != "" {
			t.Fatalf("crossed %s context boundary", name)
		}
	}
	if got := s.store.answeredFormContext("different-conversation", next, maxFormContextRunes); got != "" {
		t.Fatal("cross-conversation answer exposed")
	}
	if got := s.store.answeredFormContext(c.ID, next, 200); got != "" {
		t.Fatal("form context displaced reserved current-request budget")
	}
	for _, status := range []string{questionPending, questionCancelled, questionExpired, questionRunDone} {
		if _, err = s.store.db.Exec(`UPDATE questions SET status=? WHERE id=?`, status, q.ID); err != nil {
			t.Fatal(err)
		}
		if got := s.store.answeredFormContext(c.ID, next, maxFormContextRunes); got != "" {
			t.Fatalf("included non-answered status %s", status)
		}
	}
}

func TestAnsweredFormContextBoundsAndAllowlist(t *testing.T) {
	s, c, run, q := formFixture(t)
	if _, _, err := s.AnswerUserForm(q.ID, "human", formValues()); err != nil {
		t.Fatal(err)
	}
	// Test old/corrupt records too: only declared ordinary string fields may
	// cross this boundary, never opaque secret objects or unrecognized keys.
	raw, _ := json.Marshal(map[string]any{
		"email":    map[string]string{"secret_ref": "not-an-ordinary-value"},
		"notes":    strings.Repeat("Very long ordinary note 字 ", 1000),
		"password": "legacy-private-value", "unknown": "undeclared-private-value",
	})
	if _, err := s.store.db.Exec(`UPDATE questions SET answer_json=? WHERE id=?`, string(raw), q.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.store.AddRun(c.ID, run.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := s.store.answeredFormContext(c.ID, next, 1800)
	if got == "" || len([]rune(got)) > 1800 || !strings.Contains(got, `"truncated":true`) {
		t.Fatal("large answer did not fit its explicit budget truthfully")
	}
	for _, forbidden := range []string{"not-an-ordinary-value", "legacy-private-value", "undeclared-private-value", "secret_ref"} {
		if strings.Contains(got, forbidden) {
			t.Fatal("undeclared or private value exposed")
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "{") && !json.Valid([]byte(line)) {
			t.Fatal("truncated context broke its JSON record")
		}
	}
}
