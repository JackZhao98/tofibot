package app

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Tool tiers per Bot. The tier of a tool is global (mcp_tool_tiers.go: the
// provider's mark, or the owner's choice on the connection's page) until the
// owner sets one for a Bot. A Bot with no row follows the global tier, so the
// existing global values are every Bot's default with nothing copied: a Bot
// created later gets them too, and a global edit moves every Bot that has not
// chosen its own. A row binds to the tool definition's digest, like the global
// override: a changed tool makes the Bot's choice stale and a stale choice
// only tightens. Only the owner writes it, through these routes, or a Bot
// through `bot.tool_tier` after the person approved a card for that change.

const toolTierSetByOwner = "owner"

// botTierCache holds every row in memory. The tier of a tool is resolved from inside review transactions, and the database has one
// connection, so the lookup must not query it; the table is small (one row per Bot, connection and tool the owner changed) and only
// the methods below write it, each updating the cache after the database.
type botTierCache struct {
	mu   sync.RWMutex
	rows map[botTierKey]botTierRow
}

type botTierKey struct{ bot, server, tool string }
type botTierRow struct{ tier, digest string }

func migrateBotToolTiers(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS bot_tool_tiers(
bot_id TEXT NOT NULL,
server TEXT NOT NULL,
tool TEXT NOT NULL,
tier TEXT NOT NULL,
digest TEXT NOT NULL,
set_by TEXT NOT NULL,
set_at TEXT NOT NULL,
PRIMARY KEY(bot_id, server, tool)
)`)
	return err
}

// loadBotToolTiers reads the table into the cache. It runs once at start and again after a Bot's rows were deleted with the Bot.
func (s *Store) loadBotToolTiers() error {
	rows, err := s.db.Query(`SELECT bot_id,server,tool,tier,digest FROM bot_tool_tiers`)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := map[botTierKey]botTierRow{}
	for rows.Next() {
		var k botTierKey
		var r botTierRow
		if err := rows.Scan(&k.bot, &k.server, &k.tool, &r.tier, &r.digest); err != nil {
			return err
		}
		loaded[k] = r
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.botTiers.mu.Lock()
	s.botTiers.rows = loaded
	s.botTiers.mu.Unlock()
	return nil
}

// botToolTier is the extensions.Config.BotToolTier hook.
func (s *Store) botToolTier(botID, server, tool string) (tier, digest string, ok bool) {
	s.botTiers.mu.RLock()
	defer s.botTiers.mu.RUnlock()
	r, ok := s.botTiers.rows[botTierKey{botID, server, tool}]
	return r.tier, r.digest, ok
}

// botToolTierRaised is the extensions.Config.BotToolTierRaised hook.
func (s *Store) botToolTierRaised(server, tool string) bool {
	s.botTiers.mu.RLock()
	defer s.botTiers.mu.RUnlock()
	for k, r := range s.botTiers.rows {
		if k.server == server && k.tool == tool && r.tier != "read_only" {
			return true
		}
	}
	return false
}

// setBotToolTier records one Bot's tier for one tool, bound to the tool's
// current digest. An empty tier clears it: the Bot follows the global tier again.
func (s *Store) setBotToolTier(botID, server, tool, tier, digest string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if tier == "" {
		_, err = tx.Exec(`DELETE FROM bot_tool_tiers WHERE bot_id=? AND server=? AND tool=?`, botID, server, tool)
	} else {
		_, err = tx.Exec(`INSERT INTO bot_tool_tiers(bot_id,server,tool,tier,digest,set_by,set_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(bot_id,server,tool) DO UPDATE SET tier=excluded.tier,digest=excluded.digest,set_by=excluded.set_by,set_at=excluded.set_at`, botID, server, tool, tier, digest, toolTierSetByOwner, now())
	}
	if err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.botTiers.mu.Lock()
	defer s.botTiers.mu.Unlock()
	if s.botTiers.rows == nil {
		s.botTiers.rows = map[botTierKey]botTierRow{}
	}
	key := botTierKey{botID, server, tool}
	if tier == "" {
		delete(s.botTiers.rows, key)
	} else {
		s.botTiers.rows[key] = botTierRow{tier, digest}
	}
	return nil
}

// clearBotToolTiersForServer drops every row of a removed connection.
func (s *Store) clearBotToolTiersForServer(server string) error {
	if _, err := s.db.Exec(`DELETE FROM bot_tool_tiers WHERE server=?`, server); err != nil {
		return err
	}
	return s.loadBotToolTiers()
}

// revokeStandingAllowancesForBotTool ends one Bot's "Always allow" on a tool
// it was just told to always ask about.
func (s *Store) revokeStandingAllowancesForBotTool(botID, server, tool, by string) error {
	_, err := s.db.Exec(`UPDATE mcp_standing_allowances SET revoked_at=?,revoked_by=? WHERE bot_id=? AND server=? AND tool=? AND revoked_at=''`, now(), by, botID, server, tool)
	return err
}

// applyBotToolTier validates and stores one Bot's tier. The tool must have
// been observed: its digest is what the choice binds to.
func (s *Server) applyBotToolTier(botID, server, tool, tier string) (extensions.MCPToolTierView, error) {
	if tier != "" && !extensions.ValidMCPToolTier(tier) {
		return extensions.MCPToolTierView{}, errors.New("tier must be read_only, write, high_risk or empty")
	}
	if !s.mcpServerExists(server) {
		return extensions.MCPToolTierView{}, os.ErrNotExist
	}
	digest := s.extensions.MCPToolTier(server, tool).Digest
	if digest == "" {
		return extensions.MCPToolTierView{}, os.ErrNotExist
	}
	if err := s.store.setBotToolTier(botID, server, tool, tier, digest); err != nil {
		return extensions.MCPToolTierView{}, err
	}
	if tier == string(extensions.MCPTierHighRisk) {
		if err := s.store.revokeStandingAllowancesForBotTool(botID, server, tool, "tier_raised"); err != nil {
			return extensions.MCPToolTierView{}, err
		}
	}
	return s.extensions.MCPToolTierForBot(botID, server, tool), nil
}

// handleBotToolTiers serves one Bot's tool tiers to the owner:
//
//	GET /api/bots/{id}/tool-tiers/{server}        the connection's tools with the global tier and this Bot's own
//	PUT /api/bots/{id}/tool-tiers/{server}/{tool} {tier} set this Bot's tier; empty follows the global tier
func (s *Server) handleBotToolTiers(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/bots/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || len(parts) > 4 || parts[0] == "" || parts[1] != "tool-tiers" || parts[2] == "" {
		return false
	}
	isList := len(parts) == 3 && r.Method == http.MethodGet
	isSet := len(parts) == 4 && r.Method == http.MethodPut
	if !isList && !isSet {
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return true
	}
	if s.extensions == nil {
		writeErr(w, 503, "extensions_unavailable", "extensions unavailable")
		return true
	}
	botID := parts[0]
	if _, err := s.store.GetBot(botID); err != nil {
		writeErr(w, 404, "not_found", "Bot not found")
		return true
	}
	server, err := url.PathUnescape(parts[2])
	if err != nil || !s.mcpServerExists(server) {
		writeErr(w, 404, "not_found", "connection not found")
		return true
	}
	if isList {
		writeJSON(w, 200, s.extensions.ListToolTiersForBot(botID, server))
		return true
	}
	tool, err := url.PathUnescape(parts[3])
	if err != nil || tool == "" {
		writeErr(w, 400, "invalid_request", "invalid tool name")
		return true
	}
	var in struct {
		Tier *string `json:"tier"`
	}
	if e := decodeExtensionJSON(r, &in); e != nil || in.Tier == nil {
		writeErr(w, 400, "invalid_request", "tier must be read_only, write, high_risk or empty")
		return true
	}
	view, err := s.applyBotToolTier(botID, server, tool, *in.Tier)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeErr(w, 404, "not_found", "tool not found")
	case err != nil:
		writeErr(w, 400, "invalid_request", err.Error())
	default:
		writeJSON(w, 200, map[string]any{"ok": true, "tool": view, "next_run": true})
	}
	return true
}
