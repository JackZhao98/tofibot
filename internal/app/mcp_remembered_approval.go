package app

// Remembered approvals ("Approve and remember in this chat"). Contract:
// docs/agent-plan/mcp-tool-tiers.md §8. A grant replaces only the human
// decision a card would otherwise wait for; it never replaces the reviewer,
// the owner's "Always ask me" tier, a claim or a config/tier binding. A grant
// is created only from a human answer that arrives through the owner-
// authenticated question route; nothing a model or a tool result produces can
// create, widen or extend one.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/google/uuid"
)

// rememberedApprovalActor answers a card from a remembered grant. It is a
// host actor like autoReviewActor: the question route never accepts it.
const rememberedApprovalActor = "remembered-approval"

// mcpDecisionSourceRemembered marks a card decided by a remembered grant.
const mcpDecisionSourceRemembered = "remembered_approval"

// rememberedApprovalValidity bounds a grant in time, independently of the
// bindings that also end it (config fingerprint, tier binding, revocation).
const rememberedApprovalValidity = 24 * time.Hour

const (
	mcpRememberTargetIdentifier = "identifier"
	mcpRememberTargetExact      = "exact"
)

const mcpRememberSummaryRunes = 160

// MCPRememberOffer is shown on a pending card when a grant may be created
// from its approval: what "the same target" would mean for this call.
type MCPRememberOffer struct {
	Kind   string `json:"kind"`             // identifier | exact
	Target string `json:"target,omitempty"` // "page_id = page-q4"; empty for exact
}

// MCPRememberedApproval is the owner-visible grant record. It carries no
// argument payload, URL, header or credential.
type MCPRememberedApproval struct {
	ID               string `json:"id"`
	ConversationID   string `json:"conversation_id"`
	BotID            string `json:"bot_id"`
	Server           string `json:"server"`
	Tool             string `json:"tool"`
	TargetKind       string `json:"target_kind"`
	TargetSummary    string `json:"target_summary,omitempty"`
	SourceQuestionID string `json:"source_question_id"`
	CreatedAt        string `json:"created_at"`
	ExpiresAt        string `json:"expires_at"`
	UseCount         int    `json:"use_count"`
	LastUsedAt       string `json:"last_used_at,omitempty"`
}

var errRememberNotAvailable = errors.New("this approval cannot be remembered")
var errRememberStale = errors.New("the tool or its configuration changed; the approval was not remembered")

func migrateRememberedApprovals(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS mcp_remembered_approvals(
id TEXT PRIMARY KEY,
account_id TEXT NOT NULL,
conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
bot_id TEXT NOT NULL,
source_question_id TEXT NOT NULL,
source_run_id TEXT NOT NULL,
server TEXT NOT NULL,
tool TEXT NOT NULL,
config_fingerprint TEXT NOT NULL,
tier_binding TEXT NOT NULL,
target_key TEXT NOT NULL,
target_kind TEXT NOT NULL,
target_summary TEXT NOT NULL DEFAULT '',
created_at TEXT NOT NULL,
created_by TEXT NOT NULL,
expires_at TEXT NOT NULL,
revoked_at TEXT NOT NULL DEFAULT '',
revoked_by TEXT NOT NULL DEFAULT '',
use_count INTEGER NOT NULL DEFAULT 0,
last_used_at TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS mcp_remembered_approvals_lookup ON mcp_remembered_approvals(conversation_id,server,tool,target_key);`); err != nil {
		return err
	}
	// The card row records what a grant made from it binds to. The values are
	// written by the gate from the real proposal, never from a request body.
	for _, col := range []struct{ name, ddl string }{
		{"server", `ALTER TABLE mcp_call_approvals ADD COLUMN server TEXT NOT NULL DEFAULT ''`},
		{"tool", `ALTER TABLE mcp_call_approvals ADD COLUMN tool TEXT NOT NULL DEFAULT ''`},
		{"config_fingerprint", `ALTER TABLE mcp_call_approvals ADD COLUMN config_fingerprint TEXT NOT NULL DEFAULT ''`},
		{"tier_binding", `ALTER TABLE mcp_call_approvals ADD COLUMN tier_binding TEXT NOT NULL DEFAULT ''`},
		{"target_key", `ALTER TABLE mcp_call_approvals ADD COLUMN target_key TEXT NOT NULL DEFAULT ''`},
		{"target_kind", `ALTER TABLE mcp_call_approvals ADD COLUMN target_kind TEXT NOT NULL DEFAULT ''`},
		{"target_summary", `ALTER TABLE mcp_call_approvals ADD COLUMN target_summary TEXT NOT NULL DEFAULT ''`},
		{"remember_eligible", `ALTER TABLE mcp_call_approvals ADD COLUMN remember_eligible INTEGER NOT NULL DEFAULT 0`},
		{"grant_id", `ALTER TABLE mcp_call_approvals ADD COLUMN grant_id TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(db, "mcp_call_approvals", col.name, col.ddl); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------ target key

type mcpRememberTarget struct {
	Key, Kind, Summary string
}

// mcpIdentifierTokens are the last name token of an argument that names the
// call's target: ids, keys, addresses, channels, recipients and paths.
var mcpIdentifierTokens = map[string]bool{"id": true, "ids": true, "uid": true, "uuid": true, "guid": true, "key": true, "url": true, "uri": true, "path": true, "channel": true, "recipient": true, "recipients": true, "to": true, "cc": true, "bcc": true, "identifier": true, "slug": true, "handle": true}

// mcpNameTokens splits snake_case, kebab-case and camelCase names.
func mcpNameTokens(name string) []string {
	var tokens []string
	var b strings.Builder
	var prev rune
	flush := func() {
		if b.Len() > 0 {
			tokens = append(tokens, strings.ToLower(b.String()))
			b.Reset()
		}
	}
	for _, r := range name {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '/':
			flush()
		case unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
			flush()
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
		prev = r
	}
	flush()
	return tokens
}

func mcpIdentifierName(name string) bool {
	tokens := mcpNameTokens(name)
	return len(tokens) > 0 && mcpIdentifierTokens[tokens[len(tokens)-1]]
}

// mcpScalarIdentifier accepts a string, number or bool, or a non-empty list
// of those. An object or a nested list is not an identifier.
func mcpScalarIdentifier(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case float64, bool:
		return true
	case []any:
		if len(x) == 0 {
			return false
		}
		for _, item := range x {
			switch item.(type) {
			case string, float64, bool:
			default:
				return false
			}
		}
		return true
	}
	return false
}

// mcpRememberTargetFor derives what "the same target" means for one call.
// Identifier arguments named by the tool's own input schema bind the grant to
// their exact values; without one, only the identical full arguments match.
// The key never widens to "any arguments".
func mcpRememberTargetFor(schema, arguments json.RawMessage) (mcpRememberTarget, bool) {
	var args map[string]any
	if json.Unmarshal(arguments, &args) != nil || args == nil {
		return mcpRememberTarget{}, false
	}
	var parsed map[string]any
	if json.Unmarshal(schema, &parsed) != nil || parsed == nil {
		return mcpRememberTarget{}, false
	}
	props, _ := parsed["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	identifiers := map[string]any{}
	var summary []string
	for _, name := range names {
		value, ok := args[name]
		if !ok || !mcpIdentifierName(name) || !mcpScalarIdentifier(value) {
			continue
		}
		identifiers[name] = value
		shown, _ := json.Marshal(value)
		if s, ok := value.(string); ok {
			shown = []byte(s)
		}
		summary = append(summary, name+" = "+string(shown))
	}
	keyOf := func(v any) string {
		raw, _ := json.Marshal(v)
		sum := sha256.Sum256(raw)
		return "v1:" + hex.EncodeToString(sum[:])
	}
	if len(identifiers) > 0 {
		text := strings.Join(summary, ", ")
		if utf8.RuneCountInString(text) > mcpRememberSummaryRunes {
			text = string([]rune(text)[:mcpRememberSummaryRunes-1]) + "…"
		}
		return mcpRememberTarget{Key: keyOf(map[string]any{"identifiers": identifiers}), Kind: mcpRememberTargetIdentifier, Summary: text}, true
	}
	return mcpRememberTarget{Key: keyOf(map[string]any{"arguments": args}), Kind: mcpRememberTargetExact}, true
}

func (t mcpRememberTarget) offer() *MCPRememberOffer {
	return &MCPRememberOffer{Kind: t.Kind, Target: t.Summary}
}

// ------------------------------------------------------------ eligibility

// rememberedApprovalUsable says whether a grant may decide this call at all:
// the tool's tier is current and not the owner's "Always ask me", and a
// person attends the run. It says nothing about whether a grant exists.
func (s *Server) rememberedApprovalUsable(r Run, call extensions.MCPCallApproval) bool {
	tier, current := s.mcpCallTier(call)
	if !current || tier == extensions.MCPTierHighRisk || call.TierBinding == "" || call.ConfigVersion == "" {
		return false
	}
	unattended, err := s.mcpRunUnattended(r)
	return err == nil && !unattended
}

// matchRememberedMCPApproval finds a live grant for this exact binding in
// this conversation and Bot. Account, server, tool, config fingerprint,
// tier binding and target key must all match; revoked or expired grants
// never match.
func (s *Server) matchRememberedMCPApproval(db reviewQuerier, c Conversation, r Run, call extensions.MCPCallApproval, target mcpRememberTarget) (string, error) {
	var id string
	err := db.QueryRow(`SELECT id FROM mcp_remembered_approvals WHERE account_id=? AND conversation_id=? AND bot_id=? AND server=? AND tool=? AND config_fingerprint=? AND tier_binding=? AND target_key=? AND revoked_at='' AND julianday(expires_at)>julianday(?) ORDER BY created_at DESC,id DESC LIMIT 1`, s.reviewAccountID(), c.ID, r.BotID, call.Server, call.Tool, call.ConfigVersion, call.TierBinding, target.Key, now()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// applyRememberedMCPApproval answers a pending card from a grant inside the
// caller's transaction. The card keeps its own claim: this is a decision,
// not an execution. The audit lands in three places: the card's actor, the
// card's display (decision source and grant id) and the approval row's
// grant_id; the grant counts the use.
func applyRememberedMCPApproval(tx *sql.Tx, c Conversation, q *Question, grantID string) error {
	if q.Status != questionPending || q.Approval == nil {
		return errors.New("remembered approval applies only to a pending proposal")
	}
	q.Status, q.Answer, q.AnsweredBy, q.UpdatedAt = questionAnswered, json.RawMessage("true"), rememberedApprovalActor, now()
	if q.Approval.Review == nil {
		q.Approval.Review = &MCPReviewDisplay{Source: mcpDecisionSourceRemembered, Status: "human_decided", Reason: mcpRememberedReason, Model: ""}
	}
	q.Approval.Review.DecisionSource = mcpDecisionSourceRemembered
	q.Approval.Review.GrantID = grantID
	raw, _ := json.Marshal(q.Approval)
	res, err := tx.Exec(`UPDATE questions SET status=?,answer_json=?,answered_by=?,approval_json=?,updated_at=? WHERE id=? AND status=?`, q.Status, string(q.Answer), q.AnsweredBy, string(raw), q.UpdatedAt, q.ID, questionPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("the proposal is no longer pending")
	}
	if _, err = tx.Exec(`UPDATE mcp_call_approvals SET grant_id=? WHERE question_id=?`, grantID, q.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE mcp_remembered_approvals SET use_count=use_count+1,last_used_at=? WHERE id=?`, q.UpdatedAt, grantID); err != nil {
		return err
	}
	return insertRecoveryEvent(tx, c.ID, "question", q.Card(), q.UpdatedAt)
}

const mcpRememberedReason = "You approved this tool on this target earlier in this chat and asked to remember it. The remembered approval decided this call; no person was asked."

// rememberedClaimValid re-checks a remembered answer at claim time against
// the grant it came from: live, same conversation and Bot, same server, tool,
// config fingerprint, tier binding and target key, and the call still
// eligible (tier not "Always ask me", run attended).
func (s *Server) rememberedClaimValid(db reviewQuerier, c Conversation, r Run, call extensions.MCPCallApproval, q Question) bool {
	var grantID string
	if db.QueryRow(`SELECT grant_id FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&grantID) != nil || grantID == "" {
		return false
	}
	target, ok := mcpRememberTargetFor(call.Schema, call.Arguments)
	if !ok || !s.rememberedApprovalUsable(r, call) {
		return false
	}
	var live bool
	if db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_remembered_approvals WHERE id=? AND account_id=? AND conversation_id=? AND bot_id=? AND server=? AND tool=? AND config_fingerprint=? AND tier_binding=? AND target_key=? AND revoked_at='' AND julianday(expires_at)>julianday(?))`, grantID, s.reviewAccountID(), c.ID, r.BotID, call.Server, call.Tool, call.ConfigVersion, call.TierBinding, target.Key, now()).Scan(&live) != nil {
		return false
	}
	return live
}

// withdrawRememberedAnswer returns a card whose remembered answer no longer
// binds to pending, so a person decides. The card keeps its grant id as the
// record of what was withdrawn.
func withdrawRememberedAnswer(tx *sql.Tx, c Conversation, q Question) error {
	q.Status, q.Answer, q.AnsweredBy, q.UpdatedAt = questionPending, nil, "", now()
	if deadline, e := time.Parse(time.RFC3339Nano, q.ExpiresAt); e != nil || !time.Now().Before(deadline) {
		q.Status = questionExpired
	}
	if q.Approval != nil && q.Approval.Review != nil {
		q.Approval.Review.DecisionSource = ""
		q.Approval.Review.Status = "human_required"
		q.Approval.Review.Reason = "The remembered approval no longer applies (revoked, expired, or the tool changed). A person decides this call."
	}
	raw := ""
	if q.Approval != nil {
		b, _ := json.Marshal(q.Approval)
		raw = string(b)
	}
	if _, err := tx.Exec(`UPDATE questions SET status=?,answer_json=NULL,answered_by=NULL,approval_json=?,updated_at=? WHERE id=?`, q.Status, raw, q.UpdatedAt, q.ID); err != nil {
		return err
	}
	return insertRecoveryEvent(tx, c.ID, "question", q.Card(), q.UpdatedAt)
}

// ------------------------------------------------------------ creation

// rememberMCPApproval creates a grant from a card a human just approved. It
// reads the binding the gate recorded on the approval row, never the request,
// and refuses when the card is not an eligible MCP proposal, the tool moved
// since, the tool is "Always ask me", or the run is unattended.
func (s *Server) rememberMCPApproval(q Question, actor string) (MCPRememberedApproval, error) {
	if q.Type != questionApproval || q.Status != questionAnswered || strings.TrimSpace(string(q.Answer)) != "true" || q.AnsweredBy != actor || actor == "" || actor == autoReviewActor || actor == rememberedApprovalActor {
		return MCPRememberedApproval{}, errRememberNotAvailable
	}
	var server, tool, config, tierBinding, key, kind, summary, runID string
	var eligible int
	err := s.store.db.QueryRow(`SELECT server,tool,config_fingerprint,tier_binding,target_key,target_kind,target_summary,remember_eligible,run_id FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&server, &tool, &config, &tierBinding, &key, &kind, &summary, &eligible, &runID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (eligible != 1 || server == "" || tool == "" || config == "" || tierBinding == "" || key == "") {
		return MCPRememberedApproval{}, errRememberNotAvailable
	}
	if err != nil {
		return MCPRememberedApproval{}, err
	}
	if s.extensions == nil {
		return MCPRememberedApproval{}, errRememberStale
	}
	probe := extensions.MCPCallApproval{Server: server, Tool: tool, ConfigVersion: config, TierBinding: tierBinding}
	run, err := s.store.GetRun(runID)
	if err != nil {
		return MCPRememberedApproval{}, errRememberNotAvailable
	}
	if !s.rememberedApprovalUsable(run, probe) {
		return MCPRememberedApproval{}, errRememberStale
	}
	t := now()
	g := MCPRememberedApproval{ID: uuid.NewString(), ConversationID: q.ConversationID, BotID: q.BotID, Server: server, Tool: tool, TargetKind: kind, TargetSummary: summary, SourceQuestionID: q.ID, CreatedAt: t, ExpiresAt: time.Now().UTC().Add(rememberedApprovalValidity).Format(time.RFC3339Nano)}
	_, err = s.store.db.Exec(`INSERT INTO mcp_remembered_approvals(id,account_id,conversation_id,bot_id,source_question_id,source_run_id,server,tool,config_fingerprint,tier_binding,target_key,target_kind,target_summary,created_at,created_by,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, g.ID, s.reviewAccountID(), g.ConversationID, g.BotID, q.ID, runID, server, tool, config, tierBinding, key, kind, summary, t, actor, g.ExpiresAt)
	if err != nil {
		return MCPRememberedApproval{}, err
	}
	return g, nil
}

// rememberOfferedFor says whether the card may be answered with "remember":
// an eligible MCP proposal still pending. Checked before the answer lands so
// a refused remember does not answer the card at all.
func (s *Server) rememberOfferedFor(q Question) bool {
	if q.Type != questionApproval || q.Status != questionPending {
		return false
	}
	var eligible int
	if s.store.db.QueryRow(`SELECT remember_eligible FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&eligible) != nil {
		return false
	}
	return eligible == 1
}

// ------------------------------------------------------------ list / revoke

func (s *Store) listRememberedApprovals(conversationID string) ([]MCPRememberedApproval, error) {
	rows, err := s.db.Query(`SELECT id,conversation_id,bot_id,server,tool,target_kind,target_summary,source_question_id,created_at,expires_at,use_count,last_used_at FROM mcp_remembered_approvals WHERE conversation_id=? AND revoked_at='' AND julianday(expires_at)>julianday(?) ORDER BY created_at DESC,id DESC LIMIT 200`, conversationID, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MCPRememberedApproval{}
	for rows.Next() {
		var g MCPRememberedApproval
		if err := rows.Scan(&g.ID, &g.ConversationID, &g.BotID, &g.Server, &g.Tool, &g.TargetKind, &g.TargetSummary, &g.SourceQuestionID, &g.CreatedAt, &g.ExpiresAt, &g.UseCount, &g.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) revokeRememberedApproval(conversationID, id, actor string) (bool, error) {
	res, err := s.db.Exec(`UPDATE mcp_remembered_approvals SET revoked_at=?,revoked_by=? WHERE id=? AND conversation_id=? AND revoked_at=''`, now(), actor, id, conversationID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// routeRememberedApprovals serves the conversation's "Remembered approvals"
// list and Revoke. Owner authentication and the CSRF checks apply like every
// other /api route.
func (s *Server) routeRememberedApprovals(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "conversations" || parts[2] != "remembered-approvals" {
		return false
	}
	conversationID, _ := url.PathUnescape(parts[1])
	c, err := s.store.GetConversation(conversationID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return true
	}
	switch {
	case len(parts) == 3 && r.Method == http.MethodGet:
		grants, err := s.store.listRememberedApprovals(c.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"grants": grants})
	case len(parts) == 4 && r.Method == http.MethodDelete:
		id, _ := url.PathUnescape(parts[3])
		ok, err := s.store.revokeRememberedApproval(c.ID, id, "human")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return true
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", "remembered approval not found")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported remembered-approval operation")
	}
	return true
}
