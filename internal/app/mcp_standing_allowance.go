package app

// Standing allowances ("Always allow"). A person who approves a write-tier MCP
// call can also allow this Bot to use this tool from now on, until they revoke
// it in Settings. Unlike a remembered approval (mcp_remembered_approval.go) it
// is scoped to (account, Bot, MCP server, tool), not to a chat or an argument
// payload, has no expiry, and also decides calls in scheduled runs. It binds
// to the server's identity (config fingerprint: name, address, transport,
// headers, plus a digest of the credential target) and to the tool's
// definition digest (name, description, input schema, annotations): any
// change voids it. A high_risk ("Always ask me") tool is never grantable and
// raising a granted tool to it revokes the grant. A grant is created only from
// a human answer arriving through the owner-authenticated question route.
// It also applies when the Bot works with no person present (a schedule, a
// Bot-to-Bot turn): that is its purpose. It never applies in a conversation no
// person can see, and in AutoReview "auto" mode it replaces only the
// reviewer's "a person decides" outcome, never a deny.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/google/uuid"
)

// standingAllowanceActor answers a card from a standing allowance. Host actor:
// the question route never accepts it.
const standingAllowanceActor = "standing-allowance"

// mcpDecisionSourceStanding marks a card decided by a standing allowance.
const mcpDecisionSourceStanding = "standing_allowance"

const mcpStandingWithdrawnReason = "The standing allowance no longer applies (revoked, or the server, the tool or the Bot changed)."

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
	State      string `json:"state"` // active | server_changed | tool_changed | tier_changed
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
	// The first draft of the uses table keyed on (run_id, call_id) with a
	// mandatory call id. It never shipped; rebuild it with a surrogate key.
	var hasID bool
	if rows, err := db.Query(`SELECT name FROM pragma_table_info('mcp_standing_allowance_uses')`); err == nil {
		exists := false
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				exists = true
				hasID = hasID || name == "id"
			}
		}
		rows.Close()
		if exists && !hasID {
			if _, err := db.Exec(`DROP TABLE mcp_standing_allowance_uses`); err != nil {
				return err
			}
		}
	}
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
id INTEGER PRIMARY KEY AUTOINCREMENT,
run_id TEXT NOT NULL,
call_id TEXT,
conversation_id TEXT NOT NULL,
allowance_id TEXT NOT NULL,
question_id TEXT NOT NULL,
used_at TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS mcp_standing_allowance_uses_call ON mcp_standing_allowance_uses(run_id,call_id) WHERE call_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS mcp_standing_allowance_uses_conversation ON mcp_standing_allowance_uses(conversation_id);`); err != nil {
		return err
	}
	// tool_digest: the tool definition the person allowed. credential_target:
	// digest of the credential target of the server at that time.
	// server_changed_at: set the first time the server's identity is seen to
	// differ; it is never cleared, so changing an address and changing it back
	// does not revive the allowance.
	for _, column := range []struct{ name, ddl string }{
		{"tool_digest", `ALTER TABLE mcp_standing_allowances ADD COLUMN tool_digest TEXT NOT NULL DEFAULT ''`},
		{"credential_target", `ALTER TABLE mcp_standing_allowances ADD COLUMN credential_target TEXT NOT NULL DEFAULT ''`},
		{"server_changed_at", `ALTER TABLE mcp_standing_allowances ADD COLUMN server_changed_at TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(db, "mcp_standing_allowances", column.name, column.ddl); err != nil {
			return err
		}
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

// standingIdentity is the current identity a grant binds to: the server's
// credential-target digest and the tool's definition digest. ok is false when
// the server is gone, its fingerprint is not the call's, or the tool was
// never observed.
func (s *Server) standingIdentity(call extensions.MCPCallApproval) (credential, toolDigest string, ok bool) {
	if s.extensions == nil {
		return "", "", false
	}
	fingerprint, credential, found := s.extensions.MCPServerIdentity(call.Server)
	if !found || fingerprint != call.ConfigVersion {
		return "", "", false
	}
	toolDigest = s.extensions.MCPToolTier(call.Server, call.Tool).Digest
	return credential, toolDigest, credential != "" && toolDigest != ""
}

// standingAllowanceFor finds the live allowance for this exact Bot, server
// identity and tool definition, without looking at who can see the
// conversation. Callers that decide a call use matchStandingAllowance.
func (s *Server) standingAllowanceFor(db reviewQuerier, r Run, call extensions.MCPCallApproval) (string, error) {
	if !s.standingTierOK(call) {
		return "", nil
	}
	credential, toolDigest, ok := s.standingIdentity(call)
	if !ok {
		return "", nil
	}
	var id string
	err := db.QueryRow(`SELECT a.id FROM mcp_standing_allowances a JOIN bots b ON b.id=a.bot_id AND b.archived=0 WHERE a.account_id=? AND a.bot_id=? AND a.server=? AND a.tool=? AND a.config_fingerprint=? AND a.credential_target=? AND a.tool_digest=? AND a.revoked_at='' AND a.server_changed_at='' LIMIT 1`, s.reviewAccountID(), r.BotID, call.Server, call.Tool, call.ConfigVersion, credential, toolDigest).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// matchStandingAllowance is standingAllowanceFor for a conversation a person
// can see; a hidden conversation never matches.
func (s *Server) matchStandingAllowance(db reviewQuerier, c Conversation, r Run, call extensions.MCPCallApproval) (string, error) {
	if !c.UserVisible {
		return "", nil
	}
	return s.standingAllowanceFor(db, r, call)
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

// withdrawStandingAnswer takes back a standing-allowance answer that no longer
// binds. Where a person attends, the card returns to pending for them; where
// nobody does, it is closed (cancelled, named reason) instead of parking on a
// card nobody can answer. The card keeps its grant id as the record.
func withdrawStandingAnswer(tx *sql.Tx, c Conversation, q Question, unattended bool) error {
	q.Status, q.Answer, q.AnsweredBy, q.UpdatedAt = questionPending, nil, "", now()
	if deadline, e := time.Parse(time.RFC3339Nano, q.ExpiresAt); e != nil || !time.Now().Before(deadline) {
		q.Status = questionExpired
	}
	grantID := ""
	if q.Approval != nil && q.Approval.Review != nil {
		grantID = q.Approval.Review.GrantID
	}
	if q.Approval != nil {
		display := &MCPReviewDisplay{Source: autoReviewActor, Status: "human_required", Reason: mcpStandingWithdrawnReason + " A person decides this call.", PolicyVersion: autoReviewPolicyVersion, GrantID: grantID}
		if unattended {
			display.Status, display.Reason = "policy_denied", mcpNeedsHumanUnattendedPrefix+mcpStandingWithdrawnReason
			if q.Status == questionPending {
				q.Status = questionCancelled
			}
		}
		q.Approval.Review = display
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

// standingClaimValid re-checks a standing answer at claim time: the allowance
// is still live for this Bot, server identity and tool, the tool is still
// write tier, and the Bot is active. Revocation therefore bites at the next
// claim even for a run already in progress.
func (s *Server) standingClaimValid(db reviewQuerier, c Conversation, r Run, call extensions.MCPCallApproval, q Question) bool {
	var grantID string
	if db.QueryRow(`SELECT grant_id FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&grantID) != nil || grantID == "" {
		return false
	}
	id, err := s.matchStandingAllowance(db, c, r, call)
	return err == nil && id == grantID
}

// recordStandingUseTx is the audit of a call the allowance let through, written
// inside the claim transaction: either the call is claimed and counted, or
// neither. It bumps the use counter and writes the use row that "View
// activity" reads; a call without a provider call id gets a row with a null
// call id.
func recordStandingUseTx(tx *sql.Tx, conversationID, runID, callID, questionID string) error {
	var allowanceID string
	if err := tx.QueryRow(`SELECT grant_id FROM mcp_call_approvals WHERE question_id=?`, questionID).Scan(&allowanceID); err != nil || allowanceID == "" {
		return errors.New("standing allowance use has no grant")
	}
	t := now()
	if _, err := tx.Exec(`UPDATE mcp_standing_allowances SET use_count=use_count+1,last_used_at=? WHERE id=?`, t, allowanceID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT OR REPLACE INTO mcp_standing_allowance_uses(run_id,call_id,conversation_id,allowance_id,question_id,used_at) VALUES(?,?,?,?,?,?)`, runID, nullString(callID), conversationID, allowanceID, questionID, t)
	return err
}

// hydrateAllowances marks activities a standing allowance let through.
func (s *Store) hydrateAllowances(conversationID string, items []ToolActivity) {
	if len(items) == 0 {
		return
	}
	rows, err := s.db.Query(`SELECT run_id,call_id,allowance_id FROM mcp_standing_allowance_uses WHERE conversation_id=? AND call_id IS NOT NULL`, conversationID)
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
	probe := extensions.MCPCallApproval{Server: server, Tool: tool, ConfigVersion: config, TierBinding: tierBinding}
	if !s.standingTierOK(probe) {
		return MCPStandingAllowance{}, errRememberStale
	}
	credential, toolDigest, ok := s.standingIdentity(probe)
	if !ok {
		return MCPStandingAllowance{}, errRememberStale
	}
	t := now()
	tx, err := s.store.db.Begin()
	if err != nil {
		return MCPStandingAllowance{}, err
	}
	defer tx.Rollback()
	account := s.reviewAccountID()
	var existing string
	err = tx.QueryRow(`SELECT a.id FROM mcp_standing_allowances a JOIN bots b ON b.id=a.bot_id AND b.archived=0 WHERE a.account_id=? AND a.bot_id=? AND a.server=? AND a.tool=? AND a.config_fingerprint=? AND a.credential_target=? AND a.tool_digest=? AND a.revoked_at='' AND a.server_changed_at=''`, account, q.BotID, server, tool, config, credential, toolDigest).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MCPStandingAllowance{}, err
	}
	id := existing
	if id == "" {
		// An older allowance for a server or tool definition that has since
		// changed is replaced by this one.
		if _, err = tx.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by='superseded' WHERE account_id=? AND bot_id=? AND server=? AND tool=? AND revoked_at=''`, t, account, q.BotID, server, tool); err != nil {
			return MCPStandingAllowance{}, err
		}
		id = uuid.NewString()
		// The archived check is part of the insert: an archive that commits
		// first leaves nothing to insert.
		res, insertErr := tx.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,credential_target,tool_digest,source_question_id,created_at,created_by) SELECT ?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM bots WHERE id=? AND archived=0)`, id, account, q.BotID, server, tool, config, credential, toolDigest, q.ID, t, actor, q.BotID)
		if insertErr != nil {
			return MCPStandingAllowance{}, insertErr
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return MCPStandingAllowance{}, errStandingNotAvailable
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
	rows, err := s.store.db.Query(`SELECT a.id,a.bot_id,b.name,a.server,a.tool,a.config_fingerprint,a.credential_target,a.tool_digest,a.server_changed_at,a.created_at,a.use_count,a.last_used_at FROM mcp_standing_allowances a JOIN bots b ON b.id=a.bot_id WHERE a.account_id=? AND a.revoked_at='' ORDER BY a.created_at DESC,a.id DESC LIMIT 500`, s.reviewAccountID())
	if err != nil {
		return nil, err
	}
	out := []MCPStandingAllowance{}
	var changed []string
	for rows.Next() {
		var a MCPStandingAllowance
		var fingerprint, credential, digest, changedAt string
		if err := rows.Scan(&a.ID, &a.BotID, &a.BotName, &a.Server, &a.Tool, &fingerprint, &credential, &digest, &changedAt, &a.CreatedAt, &a.UseCount, &a.LastUsedAt); err != nil {
			rows.Close()
			return nil, err
		}
		a.State = "server_changed"
		if s.extensions != nil && changedAt == "" {
			if current, currentCredential, ok := s.extensions.MCPServerIdentity(a.Server); ok && current == fingerprint && currentCredential == credential {
				view := s.extensions.MCPToolTier(a.Server, a.Tool)
				switch {
				case view.Digest == "" || view.Digest != digest:
					a.State = "tool_changed"
				case view.Tier != extensions.MCPTierWrite:
					a.State = "tier_changed"
				default:
					a.State = "active"
				}
			}
		}
		if a.State == "server_changed" && changedAt == "" {
			changed = append(changed, a.ID)
		}
		out = append(out, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Seen different once: void for good, even if the address is later set back.
	for _, id := range changed {
		if _, err := s.store.db.Exec(`UPDATE mcp_standing_allowances SET server_changed_at=? WHERE id=? AND server_changed_at=''`, now(), id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// markStandingServerChanged is the configuration write's hook: the server's
// identity just changed (or it was removed), so every live allowance made for
// the earlier identity is void for good. It must not call into the extension
// Manager, which holds its configuration fence while calling it.
func (s *Server) markStandingServerChanged(server string) {
	if s.store == nil || s.store.db == nil {
		return
	}
	_, _ = s.store.db.Exec(`UPDATE mcp_standing_allowances SET server_changed_at=? WHERE server=? AND revoked_at='' AND server_changed_at=''`, now(), server)
}

// voidChangedStandingAllowances marks as server_changed every live allowance
// of a server whose identity no longer matches what it was made for. It runs
// before a call is decided and when Settings lists them: it catches a change
// made outside this process's configuration writes (an edited file).
func (s *Server) voidChangedStandingAllowances(server string) {
	if s.store == nil || s.store.db == nil {
		return
	}
	var fingerprint, credential string
	found := false
	if s.extensions != nil {
		var ok bool
		fingerprint, credential, ok = s.extensions.MCPServerIdentity(server)
		found = ok
	}
	if found {
		_, _ = s.store.db.Exec(`UPDATE mcp_standing_allowances SET server_changed_at=? WHERE server=? AND revoked_at='' AND server_changed_at='' AND (config_fingerprint<>? OR credential_target<>?)`, now(), server, fingerprint, credential)
		return
	}
	_, _ = s.store.db.Exec(`UPDATE mcp_standing_allowances SET server_changed_at=? WHERE server=? AND revoked_at='' AND server_changed_at=''`, now(), server)
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

// revokeStandingAllowancesForServer revokes (it keeps the rows, so the history
// and the uses stay readable) every allowance of a server that no longer exists.
func (s *Store) revokeStandingAllowancesForServer(server string) error {
	_, err := s.db.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by='server_removed' WHERE server=? AND revoked_at=''`, now(), server)
	return err
}

// revokeStandingAllowancesForTool: the owner set a tool to "Always ask me".
func (s *Store) revokeStandingAllowancesForTool(server, tool, by string) error {
	_, err := s.db.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by=? WHERE server=? AND tool=? AND revoked_at=''`, now(), by, server, tool)
	return err
}

// revokeStandingAllowancesForBotTx: by is bot_archived or bot_deleted.
func revokeStandingAllowancesForBotTx(tx *sql.Tx, botID, by string) error {
	_, err := tx.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by=? WHERE bot_id=? AND revoked_at=''`, now(), by, botID)
	return err
}

// deleteMCPServer removes a configured server and revokes its standing allowances.
func (s *Server) deleteMCPServer(name string) error {
	err := s.extensions.DeleteMCP(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e := s.store.revokeStandingAllowancesForServer(name); e != nil {
		return e
	}
	// A connection added later under the same name starts open, not with a stale choice.
	if e := s.store.clearConnectionAccessForServer(name); e != nil {
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
		// ?bot={id} narrows both lists to one Bot (the Bot's Tools page).
		if botID := r.URL.Query().Get("bot"); botID != "" {
			allowances = slices.DeleteFunc(allowances, func(a MCPStandingAllowance) bool { return a.BotID != botID })
			remembered = slices.DeleteFunc(remembered, func(m settingsRememberedApproval) bool { return m.BotID != botID })
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
