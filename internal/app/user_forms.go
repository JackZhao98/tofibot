package app

// A form shares the durable question lifecycle, but password values never
// enter its SQL row, event stream, model result or ordinary message history.
// Only run-scoped encrypted vault references cross those public boundaries.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

type UserFormField struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
}

type formSecretAnswer struct {
	SecretRef   string `json:"secret_ref"`
	ValueHidden bool   `json:"value_hidden"`
}

var userFormFieldID = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

func formSourceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(raw) > 2048 {
		return nil, errors.New("source_url must be an HTTP(S) page URL without credentials")
	}
	// Query/fragment can contain login codes or other page state. They are not
	// needed for consent or origin checking, and should not be persisted here.
	u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func formOrigin(u *url.URL) string {
	host := u.Hostname()
	port := u.Port()
	if port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + strings.ToLower(host)
}

func normalizeUserForm(in askQuestionInput) (askQuestionInput, error) {
	if len(in.Fields) == 0 || len(in.Fields) > 12 {
		return in, errors.New("a form needs 1–12 fields")
	}
	u, err := formSourceURL(in.SourceURL)
	if err != nil {
		return in, err
	}
	in.SourceURL, in.Options = u.String(), nil
	seen := map[string]bool{}
	passwords := 0
	for i := range in.Fields {
		f := &in.Fields[i]
		f.Label = strings.TrimSpace(f.Label)
		if !userFormFieldID.MatchString(f.ID) || seen[f.ID] || f.Label == "" || utf8.RuneCountInString(f.Label) > 120 || utf8.RuneCountInString(f.Placeholder) > 200 {
			return in, errors.New("fields need unique identifiers, short labels and placeholders")
		}
		seen[f.ID] = true
		switch f.Type {
		case "text", "email", "textarea":
		case "password":
			passwords++
		default:
			return in, errors.New("field type must be text, email, password or textarea")
		}
	}
	if passwords > 8 {
		return in, errors.New("a form supports at most eight password fields")
	}
	if passwords > 0 && u.Scheme != "https" {
		return in, errors.New("password forms require an HTTPS destination")
	}
	return in, nil
}

func (s *Server) userFormTool(c Conversation, r Run) Tool {
	return Tool{Name: "ask_user_form", Description: "Collect missing webpage form values from the human in one structured card, then continue the task. Inspect the actual page first and include its source_url and field labels; do not invent fields or collect unnecessary data. Supports text, email, textarea and password. Plain fields are conversation data visible to you. Password values are never returned: each becomes {secret_ref,value_hidden:true}, usable only with use_secret_input(action=browser_type) for this run and the same HTTPS website origin. Never request passwords, API keys or verification codes in plain fields or chat; use password fields for confidential values. After submission, inspect the page again, focus each actual webpage field, type ordinary answers with computer tools and password references with use_secret_input. Inspect the page before and after actions. The human submitting this card supplies values, not blanket approval for purchases, legal agreements, security changes or other consequential external submissions. Cancellation/expiry is not permission to invent answers. The default response window is 30 minutes; expires_in_seconds can set a shorter window for time-sensitive data, up to 24 hours. Human waiting does not consume active execution time. A saved waiting checkpoint can resume after service restart; cancellation and expiry still apply. Password references cleared by restart or expiry must be requested again with private fields.", Parameters: objectSchema(map[string]any{
		"question":           map[string]any{"type": "string", "description": "Explain why these values are needed"},
		"source_url":         map[string]any{"type": "string", "description": "Actual current webpage URL. Password destinations must be HTTPS."},
		"expires_in_seconds": questionExpirySchema(),
		"fields": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": map[string]any{"type": "object", "properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Unique stable field identifier, letters/digits/underscore/hyphen; starts with a letter"},
			"label":       map[string]any{"type": "string"},
			"type":        map[string]any{"type": "string", "enum": []string{"text", "email", "password", "textarea"}},
			"required":    map[string]any{"type": "boolean"},
			"placeholder": map[string]any{"type": "string"},
		}, "required": []string{"id", "label", "type", "required"}, "additionalProperties": false}},
	}, []string{"question", "source_url", "fields"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !c.UserVisible {
			return "", errors.New("user forms require a user-visible conversation")
		}
		var in askQuestionInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", errors.New("invalid form definition")
		}
		in.Type = questionForm
		in, err := normalizeQuestionInput(in)
		if err != nil {
			return "", err
		}
		for _, f := range in.Fields {
			if f.Type == "password" && (s.secretVault == nil || s.microVM == nil) {
				return "", errors.New("private browser form input requires secret storage and a shared computer; do not fall back to plain text")
			}
		}
		q, err := s.CreateQuestion(c.ID, r, in)
		if err != nil {
			return "", err
		}
		if _, err = s.store.Event(c.ID, "question", q.Card()); err != nil {
			_, _ = s.store.CancelQuestion(q.ID)
			return "", err
		}
		answer, err := s.WaitQuestion(ctx, q.ID)
		return string(answer), err
	}}
}

func validateFormValues(fields []UserFormField, values map[string]string) error {
	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.ID] = true
		value := values[f.ID]
		empty := strings.TrimSpace(value) == ""
		if f.Type == "password" {
			empty = value == "" // Never trim or rewrite private input.
		}
		if f.Required && empty {
			return errors.New("complete all required form fields")
		}
		if strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
			return ErrQuestionInvalidAnswer
		}
		if f.Type == "password" {
			if len(value) > 65536 {
				return errors.New("password field exceeds the input limit")
			}
		} else if utf8.RuneCountInString(value) > 4000 {
			return errors.New("text field exceeds 4000 characters")
		}
		if f.Type == "email" && !empty {
			address, err := mail.ParseAddress(strings.TrimSpace(value))
			if err != nil || address.Address != strings.TrimSpace(value) {
				return errors.New("enter a valid email address")
			}
		}
	}
	for id := range values {
		if !known[id] {
			return errors.New("answer contains an unknown form field")
		}
	}
	return nil
}

// AnswerUserForm serializes vault mutation with run teardown. SQL stores only
// plaintext fields and opaque password references. On failed SQL persistence,
// newly created vault entries are removed; replay returns the first answer.
func (s *Server) AnswerUserForm(id, actor string, values map[string]string) (Question, bool, error) {
	v := s.secretVault
	if v != nil {
		v.mu.Lock()
		defer v.mu.Unlock()
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return Question{}, false, err
	}
	defer tx.Rollback()
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Question{}, false, ErrQuestionNotFound
	}
	if err != nil {
		return Question{}, false, err
	}
	if actor == "" || actor == q.BotID {
		return q, false, ErrQuestionBotActor
	}
	if q.Type != questionForm {
		return q, false, ErrQuestionInvalidAnswer
	}
	if q.Status == questionAnswered {
		return q, true, nil
	}
	if q.Status != questionPending {
		return q, false, ErrQuestionNotPending
	}
	if expires, parseErr := time.Parse(time.RFC3339Nano, q.ExpiresAt); parseErr == nil && !time.Now().Before(expires) {
		return q, false, ErrQuestionNotPending
	}
	var active int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=? AND bot_id=? AND conversation_id=? AND status IN ('running','waiting')`, q.RunID, q.BotID, q.ConversationID).Scan(&active); err != nil || active != 1 {
		return q, false, ErrQuestionNotPending
	}
	if err := validateFormValues(q.Fields, values); err != nil {
		return q, false, err
	}
	answers := make(map[string]any, len(q.Fields))
	created := []string{}
	committed := false
	defer func() {
		if !committed && len(created) > 0 {
			for _, ref := range created {
				delete(v.records, ref)
			}
			_ = v.saveLocked()
		}
	}()
	for _, f := range q.Fields {
		value := values[f.ID]
		if f.Type != "password" || value == "" {
			answers[f.ID] = value
			continue
		}
		if v == nil {
			return q, false, errors.New("private input storage unavailable")
		}
		count := 0
		for _, record := range v.records {
			if record.RunID == q.RunID {
				count++
			}
		}
		if count >= 8 || len(v.records) >= 256 {
			return q, false, errors.New("private input limit reached")
		}
		u, err := formSourceURL(q.SourceURL)
		if err != nil || u.Scheme != "https" {
			return q, false, errors.New("private input needs an HTTPS website")
		}
		record := secretRecord{ID: uuid.NewString(), Kind: "browser_form", Target: formOrigin(u), Label: f.Label, Purpose: q.Prompt, BotID: q.BotID, RunID: q.RunID, ConversationID: q.ConversationID, Status: "ready", CreatedAt: now()}
		record.Ciphertext, err = v.seal(record.ID, value)
		if err != nil {
			return q, false, errors.New("could not protect private input")
		}
		v.records[record.ID] = record
		created = append(created, record.ID)
		answers[f.ID] = formSecretAnswer{SecretRef: record.ID, ValueHidden: true}
	}
	if len(created) > 0 {
		if err := v.saveLocked(); err != nil {
			return q, false, errors.New("could not save private input")
		}
	}
	encoded, err := json.Marshal(answers)
	if err != nil {
		return q, false, ErrQuestionInvalidAnswer
	}
	q.Answer, q.AnsweredBy, q.Status, q.UpdatedAt = encoded, actor, questionAnswered, now()
	if _, err := tx.Exec(`UPDATE questions SET answer_json=?,answered_by=?,status=?,updated_at=? WHERE id=? AND status=?`, string(encoded), actor, q.Status, q.UpdatedAt, id, questionPending); err != nil {
		return q, false, errors.New("could not save form response")
	}
	if err := tx.Commit(); err != nil {
		return q, false, errors.New("could not save form response")
	}
	committed = true
	return q, false, nil
}

// Check and type under the same desktop lease: other application actions may
// not switch the page between the origin check and private typing. This does
// not isolate a credential from the receiving website or arbitrary VM code.
func (s *Server) applyFormSecret(ctx context.Context, run Run, rec secretRecord, action string) (string, error) {
	if action != "browser_type" {
		return "", tooloutcome.InvalidArguments("form passwords may only be typed into their original website")
	}
	for {
		if err := s.waitComputerOwner(ctx, run); err != nil {
			return "", err
		}
		lease := s.computerLease(run.BotID)
		if err := lockComputerLease(ctx, lease); err != nil {
			s.releaseComputerOwner(run.BotID, run.ID)
			return "", err
		}
		if !s.ownsComputer(run) {
			lease.Unlock()
			continue
		}
		defer lease.Unlock()
		break
	}
	if err := s.renewComputerHold(ctx, run); err != nil {
		s.releaseComputerOwner(run.BotID, run.ID)
		return "", tooloutcome.InvalidArguments("computer control is unavailable; inspect the page again before filling")
	}
	if !s.desktopObservedFor(run) {
		return "", tooloutcome.InvalidArguments("needs_observation: inspect the current page and focus the intended private field before using the reference")
	}
	snapshot, err := s.microVMActionOnLease(ctx, run, "browser.snapshot", json.RawMessage(`{}`))
	if err != nil {
		return "", tooloutcome.InvalidArguments("could not verify the current website; inspect and focus the intended field again")
	}
	var page struct {
		Current *struct {
			URL string `json:"url"`
		} `json:"current"`
		Source string `json:"current_source"`
	}
	if json.Unmarshal([]byte(snapshot), &page) != nil || page.Current == nil || page.Source != "focused" {
		return "", tooloutcome.InvalidArguments("the current website is not focused; inspect and focus the intended field again")
	}
	u, err := formSourceURL(page.Current.URL)
	if err != nil || formOrigin(u) != rec.Target {
		return "", tooloutcome.New(tooloutcome.Denied, "private_input_destination_mismatch", "not_executed", "the current website differs from the form's approved destination; request a new form for a different website", "explain_blocker").Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value, err := s.secretVault.reveal(rec)
	if err != nil {
		return "", tooloutcome.New(tooloutcome.Permanent, "secret_unavailable", "not_executed", "secret unavailable", "explain_blocker").Err()
	}
	args, _ := json.Marshal(map[string]string{"origin": rec.Target, "text": value})
	// Internal-only action: do not add it to the model/public computer-action
	// allowlist. This path already owns the shared lease and approved vault ref.
	if _, err := s.microVM.Action(ctx, computer.Action{BotID: run.BotID, RunID: run.ID, Source: "model", Name: "browser.type_private", Args: args}); err != nil {
		// Do not forward guest/CDP diagnostics: they may contain private input.
		// Older guests fail closed; never fall back to unguarded desktop paste.
		return "", errors.New("private input rejected: inspect the page and focus the visible password field on the approved website before retrying; the shared computer must support protected form input")
	}
	return `{"ok":true}`, nil
}
