package app

// Structured questions are a small, durable rendezvous between a running
// model and a human. They deliberately do not share the secret-input vault:
// question prompts and answers are ordinary conversation data. Form password
// answers are the exception: only opaque references are stored here; values
// go directly to the private vault through AnswerUserForm.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

const (
	questionText         = "text"
	questionYesNo        = "yes_no"
	questionApproval     = "approval"
	questionSingleChoice = "single_choice"
	questionMultiChoice  = "multi_choice"
	questionForm         = "form"
	questionPending      = "pending"
	questionAnswered     = "answered"
	questionCancelled    = "cancelled"
	questionRunDone      = "run_done"
	questionExpired      = "expired"
	defaultQuestionWait  = 30 * time.Minute
	maxQuestionWait      = 24 * time.Hour
)

var (
	ErrQuestionNotFound      = errors.New("question not found")
	ErrQuestionNotPending    = errors.New("question is no longer pending")
	ErrQuestionInvalidAnswer = errors.New("invalid question answer")
	ErrQuestionBotActor      = errors.New("bot cannot answer a question")
)

// QuestionOption is public card metadata. Values are identifiers, so labels
// may be changed by a client without changing the answer recorded by the run.
type QuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// QuestionOtherAnswer records an optional human-written response without
// inventing an option identifier or treating the text as yes/no approval.
type QuestionOtherAnswer struct {
	Values    []string `json:"values"`
	OtherText string   `json:"other_text"`
}

// ApprovalDetails describe the exact proposal shown to the human. The card
// records a decision; the proposed action still requires its own tool call.
type ApprovalDetails struct {
	ReviewOnly bool              `json:"review_only,omitempty"` // Advisory card; never permission.
	Review     *MCPReviewDisplay `json:"review,omitempty"`      // Internal MCP gate only.
	Action     string            `json:"action"`
	Target     string            `json:"target"`
	Impact     string            `json:"impact"`
	// Payload is a bounded, plain-text snapshot for internal MCP call review.
	// The model-visible request_approval tool cannot supply it.
	Payload      string `json:"payload,omitempty"`
	ApproveLabel string `json:"approve_label,omitempty"`
	DenyLabel    string `json:"deny_label,omitempty"`
	DraftID      string `json:"draft_id,omitempty"`
}

type Question struct {
	Resumable      bool             `json:"-"`
	ID             string           `json:"question_id"`
	RunID          string           `json:"run_id"`
	ConversationID string           `json:"conversation_id"`
	BotID          string           `json:"bot_id"`
	Type           string           `json:"type"`
	Prompt         string           `json:"question"`
	Options        []QuestionOption `json:"options,omitempty"`
	AllowOther     bool             `json:"allow_other,omitempty"`
	Fields         []UserFormField  `json:"fields,omitempty"`
	SourceURL      string           `json:"source_url,omitempty"`
	Approval       *ApprovalDetails `json:"approval,omitempty"`
	MinSelections  int              `json:"min_selections,omitempty"`
	MaxSelections  int              `json:"max_selections,omitempty"`
	Status         string           `json:"status"`
	Answer         json.RawMessage  `json:"answer,omitempty"`
	AnsweredBy     string           `json:"answered_by,omitempty"`
	CreatedAt      string           `json:"created_at"`
	ExpiresAt      string           `json:"expires_at,omitempty"`
	UpdatedAt      string           `json:"updated_at"`
}

// QuestionCard is safe to send to the UI. It intentionally excludes answer
// and actor fields while a question is pending.
type QuestionCard struct {
	Outcome        *tooloutcome.Outcome `json:"outcome,omitempty"`
	Type           string               `json:"type"`
	QuestionID     string               `json:"question_id"`
	Question       string               `json:"question"`
	QuestionType   string               `json:"question_type"`
	Options        []QuestionOption     `json:"options,omitempty"`
	AllowOther     bool                 `json:"allow_other,omitempty"`
	Fields         []UserFormField      `json:"fields,omitempty"`
	SourceURL      string               `json:"source_url,omitempty"`
	Approval       *ApprovalDetails     `json:"approval,omitempty"`
	MinSelections  int                  `json:"min_selections,omitempty"`
	MaxSelections  int                  `json:"max_selections,omitempty"`
	RunID          string               `json:"run_id"`
	ConversationID string               `json:"conversation_id"`
	BotID          string               `json:"bot_id"`
	UpdatedAt      string               `json:"updated_at"`
	CreatedAt      string               `json:"created_at"`
	Answer         json.RawMessage      `json:"answer,omitempty"`
	AnsweredBy     string               `json:"answered_by,omitempty"`
	Status         string               `json:"status"`
	ExpiresAt      string               `json:"expires_at,omitempty"`
}

type askQuestionInput struct {
	Question         string           `json:"question"`
	Type             string           `json:"type"`
	Options          []QuestionOption `json:"options"`
	AllowOther       bool             `json:"allow_other"`
	Fields           []UserFormField  `json:"fields"`
	SourceURL        string           `json:"source_url"`
	Approval         *ApprovalDetails `json:"approval"`
	MinSelections    int              `json:"min_selections"`
	MaxSelections    int              `json:"max_selections"`
	ExpiresInSeconds int              `json:"expires_in_seconds"`
}

// migrateQuestions is called from Store.migrate. Keeping the DDL here keeps
// question lifecycle changes isolated from the general application schema.
func migrateQuestions(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS questions(
id TEXT PRIMARY KEY,
run_id TEXT NOT NULL,
conversation_id TEXT NOT NULL,
bot_id TEXT NOT NULL,
type TEXT NOT NULL,
prompt TEXT NOT NULL,
options_json TEXT NOT NULL DEFAULT '[]',
allow_other INTEGER NOT NULL DEFAULT 0,
min_selections INTEGER NOT NULL DEFAULT 0,
max_selections INTEGER NOT NULL DEFAULT 0,
status TEXT NOT NULL,
answer_json TEXT,
answered_by TEXT,
created_at TEXT NOT NULL,
expires_at TEXT,
updated_at TEXT NOT NULL,
FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS questions_conversation_status ON questions(conversation_id,status,created_at);
CREATE INDEX IF NOT EXISTS questions_run_status ON questions(run_id,status);
	CREATE UNIQUE INDEX IF NOT EXISTS questions_one_pending_run ON questions(run_id) WHERE status='pending';`)
	if err != nil {
		return err
	}
	if err = ensureColumn(db, "questions", "allow_other", `ALTER TABLE questions ADD COLUMN allow_other INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err = ensureColumn(db, "questions", "min_selections", `ALTER TABLE questions ADD COLUMN min_selections INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err = ensureColumn(db, "questions", "max_selections", `ALTER TABLE questions ADD COLUMN max_selections INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err = ensureColumn(db, "questions", "fields_json", `ALTER TABLE questions ADD COLUMN fields_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	if err = ensureColumn(db, "questions", "source_url", `ALTER TABLE questions ADD COLUMN source_url TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err = ensureColumn(db, "questions", "approval_json", `ALTER TABLE questions ADD COLUMN approval_json TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS mcp_call_approvals (
question_id TEXT PRIMARY KEY REFERENCES questions(id) ON DELETE CASCADE,
run_id TEXT NOT NULL,
action_hash TEXT NOT NULL,
claimed_at TEXT NOT NULL DEFAULT ''
);
DROP INDEX IF EXISTS mcp_call_approvals_run_action;
CREATE INDEX IF NOT EXISTS mcp_call_approvals_run_action ON mcp_call_approvals(run_id,action_hash);
CREATE TABLE IF NOT EXISTS question_renewals(original_id TEXT PRIMARY KEY REFERENCES questions(id) ON DELETE CASCADE,new_id TEXT NOT NULL UNIQUE REFERENCES questions(id) ON DELETE CASCADE);`)
	return err
}

func normalizeQuestionInput(x askQuestionInput) (askQuestionInput, error) {
	x.Question = strings.TrimSpace(x.Question)
	if len([]rune(x.Question)) > 4000 {
		return x, errors.New("question is too long")
	}
	x.Type = strings.ToLower(strings.TrimSpace(x.Type))
	if x.Type == "yesno" || x.Type == "boolean" {
		x.Type = questionYesNo
	}
	if x.Question == "" {
		return x, errors.New("question is required")
	}
	if x.AllowOther && x.Type != questionYesNo && x.Type != questionSingleChoice && x.Type != questionMultiChoice {
		return x, errors.New("allow_other only applies to yes/no and choice questions")
	}
	switch x.Type {
	case questionApproval:
		if x.Approval == nil {
			return x, errors.New("approval details are required")
		}
		if x.Approval.Review != nil || x.Approval.ReviewOnly {
			return x, errors.New("AutoReview metadata is reserved for the internal MCP gate")
		}
		for _, field := range []*string{&x.Approval.Action, &x.Approval.Target, &x.Approval.Impact} {
			*field = strings.TrimSpace(*field)
			if *field == "" || utf8.RuneCountInString(*field) > 1000 {
				return x, errors.New("approval action, target and impact must be 1–1000 characters")
			}
		}
		if x.Approval.Payload != "" && (len(x.Approval.Payload) > maxMCPApprovalPayloadBytes || !utf8.ValidString(x.Approval.Payload) || !json.Valid([]byte(x.Approval.Payload))) {
			return x, errors.New("approval payload is invalid or too large")
		}
		for _, field := range []*string{&x.Approval.ApproveLabel, &x.Approval.DenyLabel} {
			*field = strings.TrimSpace(*field)
			if utf8.RuneCountInString(*field) > 32 {
				return x, errors.New("approval button label is too long")
			}
		}
		if x.Approval.DraftID != "" {
			if _, err := uuid.Parse(x.Approval.DraftID); err != nil {
				return x, errors.New("invalid draft_id")
			}
		}
		if len(x.Options) > 0 || x.AllowOther || len(x.Fields) > 0 || x.SourceURL != "" {
			return x, errors.New("approval does not accept options, Other answers or form fields")
		}
	case questionForm:
		var err error
		x, err = normalizeUserForm(x)
		if err != nil {
			return x, err
		}
	case questionText:
		x.Options = nil
	case questionYesNo:
		x.Options = []QuestionOption{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}}
	case questionSingleChoice, questionMultiChoice:
		if len(x.Options) < 1 || len(x.Options) > 32 {
			return x, errors.New("options are required")
		}
		seen := map[string]bool{}
		for i := range x.Options {
			x.Options[i].ID = strings.TrimSpace(x.Options[i].ID)
			x.Options[i].Label = strings.TrimSpace(x.Options[i].Label)
			if len([]rune(x.Options[i].ID)) > 128 || len([]rune(x.Options[i].Label)) > 1000 || x.Options[i].ID == "" || x.Options[i].Label == "" || seen[x.Options[i].ID] {
				return x, errors.New("options require unique id and label")
			}
			seen[x.Options[i].ID] = true
		}
	default:
		return x, fmt.Errorf("unsupported question type %q", x.Type)
	}
	if x.Type != questionForm {
		x.Fields, x.SourceURL = nil, ""
	}
	if x.Type != questionApproval {
		x.Approval = nil
	}
	if x.Type == questionSingleChoice {
		x.MinSelections, x.MaxSelections = 1, 1
	} else if x.Type == questionMultiChoice {
		optionCount := len(x.Options)
		if x.AllowOther {
			optionCount++
		}
		if x.MinSelections < 0 || x.MaxSelections < 0 || (x.MaxSelections > 0 && x.MinSelections > x.MaxSelections) || x.MinSelections > optionCount || x.MaxSelections > optionCount {
			return x, errors.New("invalid selection bounds")
		}
	} else if x.MinSelections != 0 || x.MaxSelections != 0 {
		return x, errors.New("selection bounds only apply to choices")
	}
	if x.ExpiresInSeconds < 0 {
		return x, errors.New("expires_in_seconds cannot be negative")
	}
	if x.ExpiresInSeconds == 0 {
		x.ExpiresInSeconds = int(defaultQuestionWait / time.Second)
	}
	if x.ExpiresInSeconds > int(maxQuestionWait/time.Second) {
		return x, errors.New("expires_in_seconds exceeds maximum")
	}
	return x, nil
}

func (s *Server) questionTools(c Conversation, r Run) []Tool {
	return []Tool{{Name: "ask_user_question", Description: "Ask the human a text, yes/no, single-choice, or multiple-choice question and wait for the answer. Set allow_other=true for yes/no or choice questions to offer a human-written alternative. When used, it returns {values:[selected option IDs],other_text:string}; values is empty for yes/no and single-choice. An Other response is not affirmative approval; interpret its actual text. Ordinary answers retain their boolean, string, or array format. Answers are ordinary conversation data: never ask for secrets here. Use ask_user_form for several webpage fields in one card, including private password fields.", Parameters: objectSchema(map[string]any{
		"question":           map[string]any{"type": "string"},
		"type":               map[string]any{"type": "string", "enum": []string{questionText, questionYesNo, questionSingleChoice, questionMultiChoice}},
		"options":            map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"}}, "required": []string{"id", "label"}, "additionalProperties": false}},
		"allow_other":        map[string]any{"type": "boolean", "default": false, "description": "Offer an Other answer with nonblank text up to 4000 characters. Only valid for yes_no, single_choice and multi_choice. In multi_choice, Other counts as one selection toward min_selections and max_selections."},
		"min_selections":     map[string]any{"type": "integer", "minimum": 0},
		"max_selections":     map[string]any{"type": "integer", "minimum": 0},
		"expires_in_seconds": questionExpirySchema(),
	}, []string{"question", "type"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in askQuestionInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", ErrQuestionInvalidAnswer
		}
		in, err := normalizeQuestionInput(in)
		if err != nil {
			return "", err
		}
		if in.Type == questionForm {
			return "", errors.New("use ask_user_form to collect form fields")
		}
		q, err := s.CreateQuestion(c.ID, r, in)
		if err != nil {
			return "", err
		}
		card := q.Card()
		if _, err := s.store.Event(c.ID, "question", card); err != nil {
			return "", err
		}
		answer, err := s.WaitQuestion(ctx, q.ID)
		if err != nil {
			return "", err
		}
		return string(answer), nil
	}}, s.userFormTool(c, r), s.approvalTool(c, r)}
}

func (s *Server) approvalTool(c Conversation, r Run) Tool {
	return Tool{Name: "request_approval", Description: "Ask the human to approve one specific proposed action. Show the action, target and impact in a dedicated card and wait for a boolean decision. Acceptance records a decision for this proposal only: it does not execute the action, grant standing permission or approve changed details. After acceptance, recheck current state and use the separate action tool; after rejection, do not execute it. An optional draft_id links a draft in this conversation for preview only and does not authorize sending it. Never use this card to collect secrets.", Parameters: objectSchema(map[string]any{
		"question":           map[string]any{"type": "string"},
		"action":             map[string]any{"type": "string"},
		"target":             map[string]any{"type": "string"},
		"impact":             map[string]any{"type": "string"},
		"approve_label":      map[string]any{"type": "string"},
		"deny_label":         map[string]any{"type": "string"},
		"draft_id":           map[string]any{"type": "string", "description": "Optional saved draft ID in the current conversation; preview link only."},
		"expires_in_seconds": questionExpirySchema(),
	}, []string{"question", "action", "target", "impact"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var input struct {
			Question string `json:"question"`
			ApprovalDetails
			ExpiresInSeconds int `json:"expires_in_seconds"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return "", ErrQuestionInvalidAnswer
		}
		if input.Payload != "" {
			return "", errors.New("request_approval cannot supply a raw payload")
		}
		in, err := normalizeQuestionInput(askQuestionInput{Question: input.Question, Type: questionApproval, Approval: &input.ApprovalDetails, ExpiresInSeconds: input.ExpiresInSeconds})
		if err != nil {
			return "", err
		}
		if in.Approval.DraftID != "" {
			draft, err := s.store.GetMailDraft(in.Approval.DraftID)
			if err != nil || draft.ConversationID != c.ID {
				return "", errors.New("draft_id does not belong to this conversation")
			}
		}
		q, err := s.CreateQuestion(c.ID, r, in)
		if err != nil {
			return "", err
		}
		if _, err := s.store.Event(c.ID, "question", q.Card()); err != nil {
			return "", err
		}
		answer, err := s.WaitQuestion(ctx, q.ID)
		if err != nil {
			return "", err
		}
		return string(answer), nil
	}}
}

func (s *Server) CreateQuestion(conv string, run Run, in askQuestionInput) (Question, error) {
	return s.store.CreateQuestion(conv, run, in)
}

func (q Question) Card() QuestionCard {
	card := QuestionCard{Type: "question", QuestionID: q.ID, Question: q.Prompt, QuestionType: q.Type, Options: q.Options, AllowOther: q.AllowOther, Fields: q.Fields, SourceURL: q.SourceURL, Approval: q.Approval, MinSelections: q.MinSelections, MaxSelections: q.MaxSelections, RunID: q.RunID, ConversationID: q.ConversationID, BotID: q.BotID, CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt, Answer: q.Answer, AnsweredBy: q.AnsweredBy, Status: q.Status, ExpiresAt: q.ExpiresAt}
	if q.Status == questionPending || (q.Type == questionApproval && q.Status == questionExpired) {
		status, code, message, next := tooloutcome.NeedInformation, "human_input", "Task is waiting for the requested information.", "answer_question"
		if q.Type == questionApproval {
			status, code, message, next = tooloutcome.NeedApproval, "human_approval", "Task is waiting for approval of this exact proposal.", "answer_approval"
		}
		if q.Status == questionExpired {
			status, code, message, next = tooloutcome.Expired, "approval_window_expired", "The approval window expired. This workflow is concluding; the expired proposal is not permission to execute or retry.", "finish_summary"
		}
		o := tooloutcome.New(status, code, "not_executed", message, next)
		card.Outcome = &o
	}
	return card
}

func (s *Store) CreateQuestion(conv string, run Run, in askQuestionInput) (Question, error) {
	opts, err := json.Marshal(in.Options)
	if err != nil {
		return Question{}, err
	}
	fields, err := json.Marshal(in.Fields)
	if err != nil {
		return Question{}, err
	}
	approval := ""
	if in.Approval != nil {
		encoded, err := json.Marshal(in.Approval)
		if err != nil {
			return Question{}, err
		}
		approval = string(encoded)
	}
	nowAt := now()
	expires := ""
	if in.ExpiresInSeconds > 0 {
		expires = time.Now().UTC().Add(time.Duration(in.ExpiresInSeconds) * time.Second).Format(time.RFC3339Nano)
	}
	q := Question{ID: uuid.NewString(), RunID: run.ID, ConversationID: conv, BotID: run.BotID, Type: in.Type, Prompt: in.Question, Options: in.Options, AllowOther: in.AllowOther, Fields: in.Fields, SourceURL: in.SourceURL, Approval: in.Approval, MinSelections: in.MinSelections, MaxSelections: in.MaxSelections, Status: questionPending, CreatedAt: nowAt, ExpiresAt: expires, UpdatedAt: nowAt}
	if in.Approval != nil && in.Approval.ReviewOnly {
		q.Status = questionRunDone
	}
	_, err = s.db.Exec(`INSERT INTO questions(id,run_id,conversation_id,bot_id,type,prompt,options_json,min_selections,max_selections,status,created_at,expires_at,updated_at,fields_json,source_url,allow_other,approval_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, q.ID, q.RunID, q.ConversationID, q.BotID, q.Type, q.Prompt, string(opts), q.MinSelections, q.MaxSelections, q.Status, q.CreatedAt, nullString(q.ExpiresAt), q.UpdatedAt, string(fields), q.SourceURL, q.AllowOther, approval)
	return q, err
}

const questionColumns = `id,run_id,conversation_id,bot_id,type,prompt,options_json,min_selections,max_selections,status,answer_json,answered_by,created_at,expires_at,updated_at,fields_json,source_url,allow_other,approval_json`

func scanQuestion(row interface{ Scan(...any) error }) (Question, error) {
	var q Question
	var opts, ans, actor, expires, fields, approval sql.NullString
	err := row.Scan(&q.ID, &q.RunID, &q.ConversationID, &q.BotID, &q.Type, &q.Prompt, &opts, &q.MinSelections, &q.MaxSelections, &q.Status, &ans, &actor, &q.CreatedAt, &expires, &q.UpdatedAt, &fields, &q.SourceURL, &q.AllowOther, &approval)
	if err != nil {
		return q, err
	}
	q.ExpiresAt, q.AnsweredBy = expires.String, actor.String
	if err = json.Unmarshal([]byte(opts.String), &q.Options); err != nil {
		return q, err
	}
	if err = json.Unmarshal([]byte(fields.String), &q.Fields); err != nil {
		return q, err
	}
	if approval.String != "" {
		q.Approval = new(ApprovalDetails)
		if err = json.Unmarshal([]byte(approval.String), q.Approval); err != nil {
			return q, err
		}
	}
	if ans.Valid {
		q.Answer = json.RawMessage(ans.String)
	}
	return q, nil
}

func (s *Store) GetQuestion(id string) (Question, error) {
	q, err := scanQuestion(s.db.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err == nil && q.Type == questionApproval && q.Status == questionExpired {
		_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_input_waits w JOIN runs r ON r.id=w.run_id WHERE w.question_id=? AND w.state='waiting' AND r.status='waiting')`, id).Scan(&q.Resumable)
	}
	return q, err
}

func (s *Store) PendingQuestions(conv string) ([]Question, error) {
	rows, err := s.db.Query(`SELECT `+questionColumns+` FROM questions WHERE conversation_id=? AND status=? ORDER BY created_at,id`, conv, questionPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Question
	for rows.Next() {
		q, e := scanQuestion(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) ListQuestions(conv string) ([]Question, error) {
	rows, err := s.db.Query(`SELECT `+questionColumns+` FROM questions WHERE conversation_id=? ORDER BY created_at DESC,id DESC LIMIT 500`, conv)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Question
	for rows.Next() {
		q, e := scanQuestion(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Type == questionApproval && out[i].Status == questionExpired {
			_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_input_waits w JOIN runs r ON r.id=w.run_id WHERE w.question_id=? AND w.state='waiting' AND r.status='waiting')`, out[i].ID).Scan(&out[i].Resumable)
		}
	}
	return out, nil
}

func (s *Store) CancelQuestion(id string) (Question, error) {
	res, err := s.db.Exec(`UPDATE questions SET status=?,updated_at=? WHERE id=? AND status=?`, questionCancelled, now(), id, questionPending)
	if err != nil {
		return Question{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		q, e := s.GetQuestion(id)
		if errors.Is(e, sql.ErrNoRows) {
			return Question{}, ErrQuestionNotFound
		}
		if e != nil {
			return Question{}, e
		}
		return q, nil
	}
	return s.GetQuestion(id)
}

func (s *Server) routeQuestions(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "questions" && !strings.HasPrefix(p, "questions/") {
		return false
	}
	if p == "questions" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return true
		}
		conv := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
		if conv == "" {
			writeErr(w, 400, "invalid_request", "conversation_id is required")
			return true
		}
		qs, err := s.store.ListQuestions(conv)
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
			return true
		}
		cards := make([]QuestionCard, 0, len(qs))
		for _, q := range qs {
			cards = append(cards, q.Card())
		}
		writeJSON(w, 200, map[string]any{"questions": cards})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(p, "questions/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeErr(w, 404, "not_found", "question not found")
		return true
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "renew" && r.Method == http.MethodPost {
		q, err := s.store.RenewExpiredApproval(id)
		if err != nil {
			writeErr(w, http.StatusConflict, "approval_not_resumable", "This approval cannot be renewed; refresh its current task state.")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"question": q.Card()})
		return true
	}
	if len(parts) == 2 && parts[1] == "answer" && r.Method == http.MethodPost {
		q, err := s.store.GetQuestion(id)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, 404, "not_found", "question not found")
			return true
		}
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
			return true
		}
		var body struct {
			Text      string            `json:"text"`
			Value     *bool             `json:"value"`
			Values    []string          `json:"values"`
			Fields    map[string]string `json:"fields"`
			OtherText json.RawMessage   `json:"other_text"`
		}
		var raw json.RawMessage
		if err = decodeQuestionAnswer(r, &raw); err == nil {
			err = json.Unmarshal(raw, &body)
		}
		if err != nil {
			writeErr(w, 400, "invalid_request", "invalid answer")
			return true
		}
		var answer any
		if body.OtherText != nil {
			// Presence matters here: even an empty or null legacy field must not
			// be silently discarded beside an Other answer.
			var members map[string]json.RawMessage
			_ = json.Unmarshal(raw, &members)
			for key := range members {
				if strings.EqualFold(key, "text") || strings.EqualFold(key, "value") || strings.EqualFold(key, "fields") {
					writeErr(w, 400, "invalid_answer", "other_text cannot be combined with text, value or fields")
					return true
				}
			}
			other := QuestionOtherAnswer{Values: append([]string{}, body.Values...)}
			if err = json.Unmarshal(body.OtherText, &other.OtherText); err != nil || validateQuestionAnswer(q, other) != nil {
				writeErr(w, 400, "invalid_answer", "invalid Other answer")
				return true
			}
			answer = other
		} else {
			switch q.Type {
			case questionText:
				answer = body.Text
			case questionYesNo, questionApproval:
				if body.Value == nil {
					writeErr(w, 400, "invalid_answer", "value is required")
					return true
				}
				answer = *body.Value
			case questionSingleChoice:
				if len(body.Values) != 1 {
					writeErr(w, 400, "invalid_answer", "one value is required")
					return true
				}
				answer = body.Values[0]
			case questionMultiChoice:
				answer = body.Values
			}
		}
		// This deployment has one human workspace. Never accept a client supplied
		// sender identity: it could manufacture a Bot answerer.
		actor := "human"
		if q.Type == questionForm {
			q, _, err = s.AnswerUserForm(id, actor, body.Fields)
		} else {
			q, _, err = s.store.AnswerQuestion(id, actor, answer)
		}
		if err != nil {
			code := 400
			if errors.Is(err, ErrQuestionNotFound) {
				code = 404
			}
			if errors.Is(err, ErrQuestionNotPending) {
				code = 409
			}
			writeErr(w, code, "invalid_answer", err.Error())
			return true
		}
		_, _ = s.store.Event(q.ConversationID, "question", q.Card())
		s.startConversationWorker(q.ConversationID)
		writeJSON(w, 200, map[string]any{"question": q.Card()})
		return true
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		q, err := s.store.CancelQuestion(id)
		if errors.Is(err, ErrQuestionNotFound) {
			writeErr(w, 404, "not_found", "question not found")
			return true
		}
		if err != nil {
			writeErr(w, 500, "storage", err.Error())
			return true
		}
		_, _ = s.store.Event(q.ConversationID, "question", q.Card())
		s.startConversationWorker(q.ConversationID)
		writeJSON(w, 200, map[string]any{"question": q.Card()})
		return true
	}
	writeErr(w, 405, "method_not_allowed", "unsupported question operation")
	return true
}

func decodeQuestionAnswer(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing request data")
	}
	return nil
}

// AnswerQuestion is idempotent by question id. The caller must authenticate
// the human identity and conversation membership before calling it; a bot id
// is rejected here as a final guard against fabricated senders.
func (s *Store) AnswerQuestion(id, actor string, answer any) (Question, bool, error) {
	tx, err := s.db.Begin()
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
	if actor == "" || actor == q.BotID || actor == autoReviewActor {
		return Question{}, false, ErrQuestionBotActor
	}
	if q.Status != questionPending {
		if q.Status == questionAnswered {
			return q, true, nil
		}
		return q, false, ErrQuestionNotPending
	}
	var active bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE id=? AND status IN ('running','waiting'))`, q.RunID).Scan(&active); err != nil {
		return q, false, err
	}
	if !active {
		return q, false, ErrQuestionNotPending
	}
	if q.ExpiresAt != "" {
		if t, e := time.Parse(time.RFC3339Nano, q.ExpiresAt); e == nil && !time.Now().UTC().Before(t) {
			q.Status, q.UpdatedAt = questionExpired, now()
			if _, err = tx.Exec(`UPDATE questions SET status=?,updated_at=? WHERE id=? AND status=?`, questionExpired, q.UpdatedAt, id, questionPending); err != nil {
				return q, false, err
			}
			if err = insertRecoveryEvent(tx, q.ConversationID, "question", q.Card(), q.UpdatedAt); err != nil {
				return q, false, err
			}
			if err = enqueueApprovalExpiryTx(tx, q); err != nil {
				return q, false, err
			}
			if err = tx.Commit(); err != nil {
				return q, false, err
			}
			return q, false, ErrQuestionNotPending
		}
	}
	if err = validateQuestionAnswer(q, answer); err != nil {
		return q, false, err
	}
	if other, ok := answer.(QuestionOtherAnswer); ok && other.Values == nil {
		other.Values = []string{}
		answer = other
	}
	b, err := json.Marshal(answer)
	if err != nil {
		return q, false, ErrQuestionInvalidAnswer
	}
	t := now()
	res, err := tx.Exec(`UPDATE questions SET status=?,answer_json=?,answered_by=?,updated_at=? WHERE id=? AND status=?`, questionAnswered, string(b), actor, t, id, questionPending)
	if err != nil {
		return q, false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		_ = tx.Rollback()
		latest, latestErr := s.GetQuestion(id)
		return latest, latest.Status == questionAnswered, latestErr
	}
	q.Status, q.Answer, q.AnsweredBy, q.UpdatedAt = questionAnswered, b, actor, t
	if err = tx.Commit(); err != nil {
		return Question{}, false, err
	}
	return q, false, nil
}

func validateQuestionAnswer(q Question, answer any) error {
	if other, ok := answer.(QuestionOtherAnswer); ok {
		if !q.AllowOther || strings.TrimSpace(other.OtherText) == "" || !utf8.ValidString(other.OtherText) || utf8.RuneCountInString(other.OtherText) > 4000 {
			return ErrQuestionInvalidAnswer
		}
		switch q.Type {
		case questionSingleChoice, questionYesNo:
			if len(other.Values) != 0 {
				return ErrQuestionInvalidAnswer
			}
		case questionMultiChoice:
			return validateQuestionSelections(q, other.Values, 1)
		default:
			return ErrQuestionInvalidAnswer
		}
		return nil
	}
	switch q.Type {
	case questionText:
		v, ok := answer.(string)
		if !ok || len([]rune(v)) > 4000 {
			return ErrQuestionInvalidAnswer
		}
	case questionYesNo, questionApproval:
		v, ok := answer.(bool)
		if !ok {
			return ErrQuestionInvalidAnswer
		}
		_ = v
	case questionSingleChoice:
		v, ok := answer.(string)
		if !ok || !questionOptionExists(q.Options, v) {
			return ErrQuestionInvalidAnswer
		}
	case questionMultiChoice:
		var ids []string
		switch v := answer.(type) {
		case []any:
			for _, item := range v {
				id, ok := item.(string)
				if !ok {
					return ErrQuestionInvalidAnswer
				}
				ids = append(ids, id)
			}
		case []string:
			ids = v
		default:
			return ErrQuestionInvalidAnswer
		}
		return validateQuestionSelections(q, ids, 0)
	default:
		return ErrQuestionInvalidAnswer
	}
	return nil
}

func validateQuestionSelections(q Question, ids []string, otherCount int) error {
	count := len(ids) + otherCount
	if count == 0 || q.MinSelections > 0 && count < q.MinSelections || q.MaxSelections > 0 && count > q.MaxSelections {
		return ErrQuestionInvalidAnswer
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || !questionOptionExists(q.Options, id) {
			return ErrQuestionInvalidAnswer
		}
		seen[id] = true
	}
	return nil
}

func questionOptionExists(options []QuestionOption, id string) bool {
	for _, x := range options {
		if x.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) WaitQuestion(ctx context.Context, id string) (json.RawMessage, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		if q, e := s.store.GetQuestion(id); e == nil {
			_ = s.store.MarkQuestionsForRun(q.RunID, questionCancelled)
		}
		return nil, err
	}
	if runtime.CanSuspend(ctx) {
		return nil, runtime.SuspendForUserInput(ctx, id)
	}
	// Keep the context itself intact: Stop, steering and parent deadlines still
	// interrupt the wait. Only active execution-time accounting is suspended.
	resume := runtime.PauseForUserInput(ctx)
	defer resume()
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		q, err := s.store.GetQuestion(id)
		if err != nil {
			return nil, err
		}
		if q.Status == questionPending && q.ExpiresAt != "" {
			if deadline, parseErr := time.Parse(time.RFC3339Nano, q.ExpiresAt); parseErr == nil && !time.Now().UTC().Before(deadline) {
				if q.Type == questionApproval {
					if err = s.store.expireApproval(id); err != nil {
						return nil, err
					}
				} else {
					_, _ = s.store.db.Exec(`UPDATE questions SET status=?,updated_at=? WHERE id=? AND status=?`, questionExpired, now(), id, questionPending)
				}
				return json.RawMessage(`{"status":"expired"}`), nil
			}
		}
		switch q.Status {
		case questionAnswered:
			return q.Answer, nil
		case questionCancelled, questionExpired:
			return json.RawMessage(fmt.Sprintf(`{"status":%q}`, q.Status)), nil
		case questionRunDone:
			return nil, fmt.Errorf("question %s: %s", id, q.Status)
		}
		select {
		case <-ctx.Done():
			_ = s.store.MarkQuestionsForRun(q.RunID, questionCancelled)
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func questionExpirySchema() map[string]any {
	return map[string]any{
		"type": "integer", "minimum": 0, "maximum": int(maxQuestionWait / time.Second),
		"description": "Human response window in seconds; omitted or 0 means 30 minutes, at most 24 hours. Use a shorter window for time-sensitive information. Waiting does not consume active execution time. A saved waiting checkpoint can resume after service restart; cancellation and expiry still apply. Reinspect the webpage after waiting; cleared password references must be requested again.",
	}
}

func (s *Store) MarkQuestionsForRun(runID, status string) error {
	if status != questionCancelled && status != questionRunDone {
		return errors.New("invalid question terminal status")
	}
	_, err := s.db.Exec(`UPDATE questions SET status=?,updated_at=? WHERE run_id=? AND status=?`, status, now(), runID, questionPending)
	return err
}
