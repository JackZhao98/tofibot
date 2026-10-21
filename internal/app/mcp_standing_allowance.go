package app

// Standing allowances ("Always allow"). A person who approves a write-tier MCP
// call can also allow this Bot to use this tool from now on, until they revoke
// it in Settings. Unlike a remembered approval (mcp_remembered_approval.go) it
// is scoped to (account, Bot, MCP server, tool), not to a chat or an argument
// payload, has no expiry, and also decides calls in scheduled runs. It binds
// to the server's identity (config fingerprint: name, address, transport,
// headers): any change voids it. A high_risk ("Always ask me") tool is never
// grantable, and a grant is created only from a human answer arriving through
// the owner-authenticated question route.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/google/uuid"
)

// standingAllowanceActor answers a card from a standing allowance. Host actor:
// the question route never accepts it.
const standingAllowanceActor = "standing-allowance"

// mcpDecisionSourceStanding marks a card decided by a standing allowance.
const mcpDecisionSourceStanding = "standing_allowance"

const mcpStandingReason = "You allowed this Bot to use this tool at any time. The standing allowance decided this call; no person was asked."

// MCPStandingOffer is shown on a pending card when "Always allow" is available.
type MCPStandingOffer struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
}

// MCPStandingAllowance is the owner-visible record. No payload, URL or header.
type MCPStandingAllowance struct {
	ID         string `json:"id"`
	BotID      string `json:"bot_id"`
	BotName    string `json:"bot_name"`
	Server     string `json:"server"`
	Tool       string `json:"tool"`
	State      string `json:"state"` // active | server_changed
	CreatedAt  string `json:"created_at"`
	UseCount   int    `json:"use_count"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// ToolAllowance is attached to a tool activity a standing allowance let through.
type ToolAllowance struct {
	Kind string `json:"kind"` // standing_allowance
	ID   string `json:"id"`
}

var errStandingNotAvailable = errors.New("this approval cannot be turned into a standing allowance")

func migrateStandingAllowances(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS mcp_standing_allowances(
id TEXT PRIMARY KEY,
account_id TEXT NOT NULL,
bot_id TEXT NOT NULL,
server TEXT NOT NULL,
tool TEXT NOT NULL,
config_fingerprint TEXT NOT NULL,
source_question_id TEXT NOT NULL,
created_at TEXT NOT NULL,
created_by TEXT NOT NULL,
revoked_at TEXT NOT NULL DEFAULT '',
revoked_by TEXT NOT NULL DEFAULT '',
use_count INTEGER NOT NULL DEFAULT 0,
last_used_at TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS mcp_standing_allowances_active ON mcp_standing_allowances(account_id,bot_id,server,tool) WHERE revoked_at='';
CREATE TABLE IF NOT EXISTS mcp_standing_allowance_uses(
run_id TEXT NOT NULL,
call_id TEXT NOT NULL,
conversation_id TEXT NOT NULL,
allowance_id TEXT NOT NULL,
question_id TEXT NOT NULL,
used_at TEXT NOT NULL,
PRIMARY KEY(run_id,call_id));
CREATE INDEX IF NOT EXISTS mcp_standing_allowance_uses_conversation ON mcp_standing_allowance_uses(conversation_id);`); err != nil {
		return err
	}
	return ensureColumn(db, "mcp_call_approvals", "standing_eligible", `ALTER TABLE mcp_call_approvals ADD COLUMN standing_eligible INTEGER NOT NULL DEFAULT 0`)
}

// standingTierOK says whether the call's tool is write tier right now with a
// current binding. read_only needs no grant and high_risk can never have one.
func (s *Server) standingTierOK(call extensions.MCPCallApproval) bool {
	if s.extensions == nil || call.TierBinding == "" || call.ConfigVersion == "" || call.Server == "" || call.Tool == "" {
		return false
	}
	tier, current := s.mcpCallTier(call)
	return current && tier == extensions.MCPTierWrite
}

// standingOfferable: "Always allow" is offered on a card in a conversation a
// person can see, for a write-tier call under a current binding.
func (s *Server) standingOfferable(c Conversation, call extensions.MCPCallApproval) bool {
	return c.UserVisible && s.standingTierOK(call)
}

// matchStandingAllowance finds the live allowance for this exact Bot, server
// identity and tool. Revoked allowances, other Bots, other tools, a changed
// server and archived Bots never match.
func (s *Server) matchStandingAllowance(db reviewQuerier, r Run, call extensions.MCPCallApproval) (string, error) {
	if !s.standingTierOK(call) {
		return "", nil
	}
	var id string
	err := db.QueryRow(`SELECT a.id FROM mcp_standing_allowances a JOIN bots b ON b.id=a.bot_id AND b.archived=0 WHERE a.account_id=? AND a.bot_id=? AND a.server=? AND a.tool=? AND a.config_fingerprint=? AND a.revoked_at='' LIMIT 1`, s.reviewAccountID(), r.BotID, call.Server, call.Tool, call.ConfigVersion).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// applyStandingAllowance answers a pending card from an allowance inside the
// caller's transaction (a decision, not an execution; the card keeps its own
// claim). Audit: the card's actor, decision source and grant id, and the
// approval row's grant_id. The use is counted when the call is claimed.
func applyStandingAllowance(tx *sql.Tx, c Conversation, q *Question, allowanceID string) error {
	if q.Status != questionPending || q.Approval == nil {
		return errors.New("standing allowance applies only to a pending proposal")
	}
	q.Status, q.Answer, q.AnsweredBy, q.UpdatedAt = questionAnswered, json.RawMessage("true"), standingAllowanceActor, now()
	q.Approval.Review = &MCPReviewDisplay{Source: mcpDecisionSourceStanding, Status: "human_decided", Reason: mcpStandingReason, DecisionSource: mcpDecisionSourceStanding, GrantID: allowanceID}
	raw, _ := json.Marshal(q.Approval)
	res, err := tx.Exec(`UPDATE questions SET status=?,answer_json=?,answered_by=?,approval_json=?,updated_at=? WHERE id=? AND status=?`, q.Status, string(q.Answer), q.AnsweredBy, string(raw), q.UpdatedAt, q.ID, questionPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("the proposal is no longer pending")
	}
	if _, err = tx.Exec(`UPDATE mcp_call_approvals SET grant_id=? WHERE question_id=?`, allowanceID, q.ID); err != nil {
		return err
	}
	return insertRecoveryEvent(tx, c.ID, "question", q.Card(), q.UpdatedAt)
}

// standingClaimValid re-checks a standing answer at claim time: the allowance
// is still live for this Bot, server identity and tool, the tool is still
// write tier, and the Bot is active. Revocation therefore bites at the next
// claim even for a run already in progress.
func (s *Server) standingClaimValid(db reviewQuerier, r Run, call extensions.MCPCallApproval, q Question) bool {
	var grantID string
	if db.QueryRow(`SELECT grant_id FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&grantID) != nil || grantID == "" {
		return false
	}
	id, err := s.matchStandingAllowance(db, r, call)
	return err == nil && id == grantID
}

// recordStandingUse is the audit of a call the allowance let through: the use
// counter, and the tool activity link that "View activity" reads.
func (s *Store) recordStandingUse(c Conversation, runID, callID, questionID, allowanceID string) error {
	t := now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE mcp_standing_allowances SET use_count=use_count+1,last_used_at=? WHERE id=?`, t, allowanceID); err != nil {
		return err
	}
	if callID != "" {
		if _, err = tx.Exec(`INSERT OR REPLACE INTO mcp_standing_allowance_uses(run_id,call_id,conversation_id,allowance_id,question_id,used_at) VALUES(?,?,?,?,?,?)`, runID, callID, c.ID, allowanceID, questionID, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// hydrateAllowances marks activities a standing allowance let through.
func (s *Store) hydrateAllowances(conversationID string, items []ToolActivity) {
	if len(items) == 0 {
		return
	}
	rows, err := s.db.Query(`SELECT run_id,call_id,allowance_id FROM mcp_standing_allowance_uses WHERE conversation_id=?`, conversationID)
	if err != nil {
		return
	}
	defer rows.Close()
	used := map[[2]string]string{}
	for rows.Next() {
		var run, call, id string
		if rows.Scan(&run, &call, &id) == nil {
			used[[2]string{run, call}] = id
		}
	}
	for i := range items {
		if id, ok := used[[2]string{items[i].RunID, items[i].CallID}]; ok {
			items[i].AllowedBy = &ToolAllowance{Kind: mcpDecisionSourceStanding, ID: id}
		}
	}
}

// ------------------------------------------------------------ creation

// standingOfferedFor says whether the card may be answered with "always
// allow": a pending MCP proposal the gate marked eligible.
func (s *Server) standingOfferedFor(q Question) bool {
	if q.Type != questionApproval || q.Status != questionPending {
		return false
	}
	var eligible int
	if s.store.db.QueryRow(`SELECT standing_eligible FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&eligible) != nil {
		return false
	}
	return eligible == 1
}

// grantStandingAllowance creates the allowance from a card a human just
// approved. It reads the binding the gate recorded on the approval row, never
// the request, and re-checks tier and server identity now.
func (s *Server) grantStandingAllowance(q Question, actor string) (MCPStandingAllowance, error) {
	if q.Type != questionApproval || q.Status != questionAnswered || strings.TrimSpace(string(q.Answer)) != "true" || q.AnsweredBy != actor || actor == "" || actor == autoReviewActor || actor == rememberedApprovalActor || actor == standingAllowanceActor {
		return MCPStandingAllowance{}, errStandingNotAvailable
	}
	var server, tool, config, tierBinding string
	var eligible int
	err := s.store.db.QueryRow(`SELECT server,tool,config_fingerprint,tier_binding,standing_eligible FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&server, &tool, &config, &tierBinding, &eligible)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (eligible != 1 || server == "" || tool == "" || config == "") {
		return MCPStandingAllowance{}, errStandingNotAvailable
	}
	if err != nil {
		return MCPStandingAllowance{}, err
	}
	if !s.standingTierOK(extensions.MCPCallApproval{Server: server, Tool: tool, ConfigVersion: config, TierBinding: tierBinding}) {
		return MCPStandingAllowance{}, errRememberStale
	}
	var archived int
	if s.store.db.QueryRow(`SELECT archived FROM bots WHERE id=?`, q.BotID).Scan(&archived) != nil || archived != 0 {
		return MCPStandingAllowance{}, errStandingNotAvailable
	}
	t := now()
	tx, err := s.store.db.Begin()
	if err != nil {
		return MCPStandingAllowance{}, err
	}
	defer tx.Rollback()
	account := s.reviewAccountID()
	var existing string
	err = tx.QueryRow(`SELECT id FROM mcp_standing_allowances WHERE account_id=? AND bot_id=? AND server=? AND tool=? AND config_fingerprint=? AND revoked_at=''`, account, q.BotID, server, tool, config).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MCPStandingAllowance{}, err
	}
	id := existing
	if id == "" {
		// An older allowance for a server identity that has since changed is
		// replaced by this one.
		if _, err = tx.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by='superseded' WHERE account_id=? AND bot_id=? AND server=? AND tool=? AND revoked_at=''`, t, account, q.BotID, server, tool); err != nil {
			return MCPStandingAllowance{}, err
		}
		id = uuid.NewString()
		if _, err = tx.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,source_question_id,created_at,created_by) VALUES(?,?,?,?,?,?,?,?,?)`, id, account, q.BotID, server, tool, config, q.ID, t, actor); err != nil {
			return MCPStandingAllowance{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return MCPStandingAllowance{}, err
	}
	return MCPStandingAllowance{ID: id, BotID: q.BotID, Server: server, Tool: tool, State: "active", CreatedAt: t}, nil
}

// ------------------------------------------------------------ list / revoke / removal

type settingsRememberedApproval struct {
	MCPRememberedApproval
	BotName          string `json:"bot_name"`
	ConversationName string `json:"conversation_name"`
}

func (s *Server) listStandingAllowances() ([]MCPStandingAllowance, error) {
	rows, err := s.store.db.Query(`SELECT a.id,a.bot_id,b.name,a.server,a.tool,a.config_fingerprint,a.created_at,a.use_count,a.last_used_at FROM mcp_standing_allowances a JOIN bots b ON b.id=a.bot_id WHERE a.account_id=? AND a.revoked_at='' ORDER BY a.created_at DESC,a.id DESC LIMIT 500`, s.reviewAccountID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MCPStandingAllowance{}
	for rows.Next() {
		var a MCPStandingAllowance
		var fingerprint string
		if err := rows.Scan(&a.ID, &a.BotID, &a.BotName, &a.Server, &a.Tool, &fingerprint, &a.CreatedAt, &a.UseCount, &a.LastUsedAt); err != nil {
			return nil, err
		}
		a.State = "server_changed"
		if s.extensions != nil {
			if current, ok := s.extensions.MCPConfigVersion(a.Server); ok && current == fingerprint {
				a.State = "active"
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Server) listAllRememberedApprovals() ([]settingsRememberedApproval, error) {
	rows, err := s.store.db.Query(`SELECT g.id,g.conversation_id,g.bot_id,g.server,g.tool,g.target_kind,g.target_summary,g.source_question_id,g.created_at,g.expires_at,g.use_count,g.last_used_at,COALESCE(b.name,''),COALESCE(c.name,'') FROM mcp_remembered_approvals g LEFT JOIN bots b ON b.id=g.bot_id LEFT JOIN conversations c ON c.id=g.conversation_id WHERE g.account_id=? AND g.revoked_at='' AND julianday(g.expires_at)>julianday(?) ORDER BY g.created_at DESC,g.id DESC LIMIT 200`, s.reviewAccountID(), now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []settingsRememberedApproval{}
	for rows.Next() {
		var g settingsRememberedApproval
		if err := rows.Scan(&g.ID, &g.ConversationID, &g.BotID, &g.Server, &g.Tool, &g.TargetKind, &g.TargetSummary, &g.SourceQuestionID, &g.CreatedAt, &g.ExpiresAt, &g.UseCount, &g.LastUsedAt, &g.BotName, &g.ConversationName); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) revokeStandingAllowance(account, id, actor string) (bool, error) {
	res, err := s.db.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by=? WHERE id=? AND account_id=? AND revoked_at=''`, now(), actor, id, account)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// deleteStandingAllowancesForServer removes every allowance for a server that
// no longer exists.
func (s *Store) deleteStandingAllowancesForServer(server string) error {
	_, err := s.db.Exec(`DELETE FROM mcp_standing_allowances WHERE server=?`, server)
	return err
}

func deleteStandingAllowancesForBotTx(tx *sql.Tx, botID string) error {
	_, err := tx.Exec(`DELETE FROM mcp_standing_allowances WHERE bot_id=?`, botID)
	return err
}

// deleteMCPServer removes a configured server and its standing allowances.
func (s *Server) deleteMCPServer(name string) error {
	err := s.extensions.DeleteMCP(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e := s.store.deleteStandingAllowancesForServer(name); e != nil {
		return e
	}
	return err
}

// routeStandingAllowances serves Settings: GET /api/standing-allowances lists
// the standing allowances and the chat-scoped remembered approvals (every
// place the system acts without asking); DELETE /api/standing-allowances/{id}
// revokes one. Owner authentication and CSRF apply like every /api route.
func (s *Server) routeStandingAllowances(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] != "standing-allowances" {
		return false
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		allowances, err := s.listStandingAllowances()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return true
		}
		remembered, err := s.listAllRememberedApprovals()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"allowances": allowances, "remembered": remembered})
	case len(parts) == 2 && r.Method == http.MethodDelete:
		id, _ := url.PathUnescape(parts[1])
		ok, err := s.store.revokeStandingAllowance(s.reviewAccountID(), id, "human")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return true
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", "standing allowance not found")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported standing-allowance operation")
	}
	return true
}
