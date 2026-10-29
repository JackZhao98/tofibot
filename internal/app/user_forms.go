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
	// SuggestedName is the Bot's proposed variable name for "Save for this
	// Bot". Only request_secret_input sets it; the person decides whether to
	// save and under which name.
	SuggestedName string `json:"suggested_name,omitempty"`
}

type formSecretAnswer struct {
	SecretRef   string `json:"secret_ref"`
	ValueHidden bool   `json:"value_hidden"`
	// SavedAs is the name (never the value) the person kept it under for this Bot.
	SavedAs string `json:"saved_as,omitempty"`
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
		if f.SuggestedName != "" {
			return in, errors.New("a suggested secret name belongs to a private input only")
		}
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

// A private input is a form card with one password field and no website. Its
// secret is not bound to an origin: it feeds use_secret_input's computer
// actions (shell_exec, env, ssh) and is never typed into a page. Only
// request_secret_input sets the stored private_input marker; a webpage form
// always carries a source_url and is bound to its HTTPS origin.
func normalizePrivateInput(in askQuestionInput) (askQuestionInput, error) {
	if len(in.Fields) != 1 || in.Fields[0].Type != "password" || !in.Fields[0].Required || in.SourceURL != "" {
		return in, errors.New("a private input is one required password field")
	}
	f := &in.Fields[0]
	f.Label = strings.TrimSpace(f.Label)
	if !userFormFieldID.MatchString(f.ID) || f.Label == "" || utf8.RuneCountInString(f.Label) > 120 || f.Placeholder != "" {
		return in, errors.New("a private input needs a short label")
	}
	if f.SuggestedName != "" && !allowedSecretEnv(f.SuggestedName) {
		return in, errors.New("suggested_name must be an environment variable name: capital letters, digits and underscores")
	}
	in.Options = nil
	return in, nil
}

func (q Question) isPrivateInput() bool {
	return q.Type == questionForm && q.PrivateInput
}

const privateInputFieldID = "secret"

// secretInputTool keeps the long-standing model-facing request_secret_input
// name as a thin front for the form rendezvous: the same durable question,
// suspension, restart and expiry rules, and the same vault write on answer.
func (s *Server) secretInputTool(c Conversation, r Run) Tool {
	return Tool{Name: "request_secret_input", Description: "Ask the human for one secret (a key, token or password) through a private input card, then continue. The value never enters chat, history or your tool result; the result is {secret:{secret_ref,value_hidden:true}}, an opaque reference usable only by use_secret_input in this run, for its computer actions (shell_exec, env, ssh); it is never typed into a page, so ask for a website password with ask_user_form instead. State the intended use accurately in purpose. Never ask for secrets in chat or in plain questions. Cancellation or expiry never permits inventing a value. The person may choose on the card to keep the secret for this Bot; you cannot choose that. A kept secret is listed by name in your context and used by naming it in computer_shell secrets. The response window, restart and cleared-reference rules are on expires_in_seconds.", Parameters: objectSchema(map[string]any{
		"label":              map[string]any{"type": "string", "description": "Short name of the secret, shown on the field"},
		"purpose":            map[string]any{"type": "string", "description": "Why it is needed and exactly how it will be used"},
		"suggested_name":     map[string]any{"type": "string", "description": "Environment variable name to offer if the person keeps the secret"},
		"expires_in_seconds": questionExpirySchema(),
	}, []string{"label", "purpose"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := requireHumanPresent(c); err != nil {
			return "", err
		}
		var arg struct {
			Label            string `json:"label"`
			Purpose          string `json:"purpose"`
			SuggestedName    string `json:"suggested_name"`
			ExpiresInSeconds int    `json:"expires_in_seconds"`
		}
		if json.Unmarshal(raw, &arg) != nil || len(arg.Label) > 120 || len(arg.Purpose) > 500 || strings.TrimSpace(arg.Label) == "" || strings.TrimSpace(arg.Purpose) == "" {
			return "", errors.New("label and purpose are required")
		}
		if s.secretVault == nil || s.computer() == nil {
			return "", errors.New("private input requires secret storage and a shared computer; do not fall back to plain text")
		}
		in, err := normalizeQuestionInput(askQuestionInput{
			Question: strings.TrimSpace(arg.Purpose), Type: questionForm, PrivateInput: true, ExpiresInSeconds: arg.ExpiresInSeconds,
			Fields: []UserFormField{{ID: privateInputFieldID, Label: arg.Label, Type: "password", Required: true, SuggestedName: strings.TrimSpace(arg.SuggestedName)}},
		})
		if err != nil {
			return "", err
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

func (s *Server) userFormTool(c Conversation, r Run) Tool {
	return Tool{Name: "ask_user_form", Description: "Collect missing webpage form values from the human in one card, then continue. Inspect the actual page first; include its source_url and real field labels; never invent fields or collect unnecessary data. Plain answers are conversation data. Password values are never returned: each becomes {secret_ref,value_hidden:true}, usable only via use_secret_input(action=browser_type) in this run on the same HTTPS origin. Never request passwords, API keys or verification codes in plain fields or chat. After submission, re-inspect the page, focus each real field, type plain answers with computer tools and password references with use_secret_input; inspect before and after actions. Submitted values are not approval for purchases, legal agreements, security changes or other consequential submissions. Cancellation/expiry never permits inventing answers. The response window, restart and cleared-reference rules are on expires_in_seconds.", Parameters: objectSchema(map[string]any{
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
		if err := requireHumanPresent(c); err != nil {
			return "", err
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
			if f.Type == "password" && (s.secretVault == nil || s.computer() == nil) {
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
	return s.AnswerUserFormSaving(id, actor, values, "")
}

// AnswerUserFormSaving also keeps the private input for the card's Bot under
// saveAs when the person chose "Save for this Bot". Only the HTTP answer route
// passes a name, from the human's submitted answer; no Bot tool reaches it.
func (s *Server) AnswerUserFormSaving(id, actor string, values map[string]string, saveAs string) (Question, bool, error) {
	v := s.secretVault
	// Database connection first, vault mutex second: portability transactions
	// hold the connection and then wait for the vault, so this order is the
	// only one that cannot form a lock cycle with them.
	tx, err := s.store.db.Begin()
	if err != nil {
		return Question{}, false, err
	}
	defer tx.Rollback()
	if v != nil {
		v.mu.Lock()
		defer v.mu.Unlock()
	}
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
	if q.isPrivateInput() && (len(q.Fields) != 1 || q.Fields[0].ID != privateInputFieldID || q.Fields[0].Type != "password") {
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
	if saveAs != "" && (!q.isPrivateInput() || !allowedSecretEnv(saveAs) || v == nil) {
		return q, false, errors.New("choose a valid variable name to save this secret")
	}
	answers := make(map[string]any, len(q.Fields))
	created := []string{}
	var replaced *secretRecord
	committed := false
	defer func() {
		if !committed && len(created) > 0 {
			for _, ref := range created {
				delete(v.records, ref)
			}
			if replaced != nil {
				v.records[replaced.ID] = *replaced
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
			// An expired reference is unusable and must not hold a slot.
			if created, e := time.Parse(time.RFC3339Nano, record.CreatedAt); record.RunID == q.RunID && (e != nil || time.Since(created) <= secretReferenceLifetime) {
				count++
			}
		}
		if count >= 8 || len(v.records) >= 256 {
			return q, false, errors.New("private input limit reached")
		}
		record := secretRecord{ID: uuid.NewString(), Label: f.Label, Purpose: q.Prompt, BotID: q.BotID, RunID: q.RunID, ConversationID: q.ConversationID, Status: "ready", CreatedAt: now()}
		if !q.isPrivateInput() {
			u, err := formSourceURL(q.SourceURL)
			if err != nil || u.Scheme != "https" {
				return q, false, errors.New("private input needs an HTTPS website")
			}
			record.Kind, record.Target = "browser_form", formOrigin(u)
		}
		record.Ciphertext, err = v.seal(record.ID, value)
		if err != nil {
			return q, false, errors.New("could not protect private input")
		}
		v.records[record.ID] = record
		created = append(created, record.ID)
		answer := formSecretAnswer{SecretRef: record.ID, ValueHidden: true}
		if saveAs != "" {
			savedID, old, err := v.storeBotSecretLocked(q.BotID, saveAs, f.Label, value)
			if err != nil {
				return q, false, err
			}
			created, replaced = append(created, savedID), old
			answer.SavedAs = saveAs
		}
		answers[f.ID] = answer
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
	if _, err := s.computer().Action(ctx, computer.Action{BotID: run.BotID, RunID: run.ID, Source: "model", Name: "browser.type_private", Args: args}); err != nil {
		// Do not forward guest/CDP diagnostics: they may contain private input.
		// Older guests fail closed; never fall back to unguarded desktop paste.
		return "", errors.New("private input rejected: inspect the page and focus the visible password field on the approved website before retrying; the shared computer must support protected form input")
	}
	return `{"ok":true}`, nil
}
