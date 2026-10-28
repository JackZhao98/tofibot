package app

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Which Bots may use a connection (docs/design/tools-and-connections.md section 3.7).
//
// Every connection is open to every Bot until the owner limits it: a Bot and a
// server with no row in bot_connection_access means open, so the table changes
// nothing on its own and a Bot created later can use what exists. A row says
// who set it and when. Only the owner writes it, through these routes; a Bot
// has no tool that reads or changes it. The filter runs where the run's
// servers are chosen (extensions.Config.ConnectionAccess), so a closed
// connection is not listed, searched, described or dialled for that Bot.

const connectionSetByOwner = "owner"

func migrateConnectionAccess(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS bot_connection_access(
bot_id TEXT NOT NULL,
server TEXT NOT NULL,
enabled INTEGER NOT NULL,
set_by TEXT NOT NULL,
set_at TEXT NOT NULL,
PRIMARY KEY(bot_id, server)
)`)
	return err
}

// connectionAccessFilter is the extensions.Config.ConnectionAccess hook. An
// error is returned to the caller, which withholds every server.
func (s *Store) connectionAccessFilter(ctx context.Context, botID string, names []string) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	closed := map[string]bool{}
	if botID != "" {
		rows, err := s.db.QueryContext(ctx, `SELECT server FROM bot_connection_access WHERE bot_id=? AND enabled=0`, botID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var server string
			if err := rows.Scan(&server); err != nil {
				rows.Close()
				return nil, err
			}
			closed[server] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = !closed[name]
	}
	return allowed, nil
}

type connectionAccessRow struct {
	Server  string `json:"server"`
	Enabled bool   `json:"enabled"`
	SetBy   string `json:"set_by,omitempty"`
	SetAt   string `json:"set_at,omitempty"`
}

// botConnectionAccess lists one row per configured server with this Bot's
// choice on it (open when it never had one).
func (s *Server) botConnectionAccess(botID string) ([]connectionAccessRow, error) {
	servers, err := s.extensions.ListMCP()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.db.Query(`SELECT server,enabled,set_by,set_at FROM bot_connection_access WHERE bot_id=?`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]connectionAccessRow{}
	for rows.Next() {
		var row connectionAccessRow
		var on int
		if err := rows.Scan(&row.Server, &on, &row.SetBy, &row.SetAt); err != nil {
			return nil, err
		}
		row.Enabled = on != 0
		set[row.Server] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]connectionAccessRow, 0, len(servers))
	for _, server := range servers {
		row, ok := set[server.Name]
		if !ok {
			row = connectionAccessRow{Server: server.Name, Enabled: true}
		}
		out = append(out, row)
	}
	return out, nil
}

// setConnectionAccess records one Bot's choice for one connection and tells
// open pages the configuration changed.
func (s *Store) setConnectionAccess(botID, server string, enabled bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = setConnectionAccessTx(tx, botID, server, enabled); err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func setConnectionAccessTx(tx *sql.Tx, botID, server string, enabled bool) error {
	on := 0
	if enabled {
		on = 1
	}
	_, err := tx.Exec(`INSERT INTO bot_connection_access(bot_id,server,enabled,set_by,set_at) VALUES(?,?,?,?,?)
ON CONFLICT(bot_id,server) DO UPDATE SET enabled=excluded.enabled,set_by=excluded.set_by,set_at=excluded.set_at`, botID, server, on, connectionSetByOwner, now())
	return err
}

// setServerBots makes a connection open to every Bot (allBots) or to exactly the
// listed Bots among the current ones. A Bot created later has no row and so can
// use it; the owner limits that Bot on its own page.
func (s *Store) setServerBots(server string, allBots bool, botIDs []string) error {
	bots, err := s.ListBots()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM bot_connection_access WHERE server=?`, server); err != nil {
		return err
	}
	if !allBots {
		chosen := map[string]bool{}
		for _, id := range botIDs {
			chosen[id] = true
		}
		for _, bot := range bots {
			if err = setConnectionAccessTx(tx, bot.ID, server, chosen[bot.ID]); err != nil {
				return err
			}
		}
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// clearConnectionAccessForServer drops every row of a removed connection.
func (s *Store) clearConnectionAccessForServer(server string) error {
	_, err := s.db.Exec(`DELETE FROM bot_connection_access WHERE server=?`, server)
	return err
}

// handleBotConnections serves one Bot's connections to the owner:
//
//	GET /api/bots/{id}/connections          every configured connection with this Bot's choice on it
//	PUT /api/bots/{id}/connections/{name}   {enabled} open or close one connection for this Bot
func (s *Server) handleBotConnections(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/bots/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] != "connections" {
		return false
	}
	isList := len(parts) == 2 && r.Method == http.MethodGet
	isSet := len(parts) == 3 && r.Method == http.MethodPut
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
	if isList {
		list, err := s.botConnectionAccess(botID)
		if err != nil {
			writeErr(w, 500, "internal", "connections unavailable")
			return true
		}
		writeJSON(w, 200, map[string]any{"connections": list})
		return true
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if e := decodeExtensionJSON(r, &in); e != nil || in.Enabled == nil {
		writeErr(w, 400, "invalid_request", "enabled must be true or false")
		return true
	}
	if !s.mcpServerExists(parts[2]) {
		writeErr(w, 404, "not_found", "connection not found")
		return true
	}
	if err := s.store.setConnectionAccess(botID, parts[2], *in.Enabled); err != nil {
		writeErr(w, 500, "internal", "connection access not saved")
		return true
	}
	writeJSON(w, 200, map[string]any{"ok": true, "next_run": true, "enabled": *in.Enabled})
	return true
}

func (s *Server) mcpServerExists(name string) bool {
	servers, err := s.extensions.ListMCP()
	if err != nil {
		return false
	}
	for _, server := range servers {
		if server.Name == name {
			return true
		}
	}
	return false
}

type connectionBotRow struct {
	BotID   string `json:"bot_id"`
	BotName string `json:"bot_name"`
	Enabled bool   `json:"enabled"`
}

// serverBots lists the current Bots with whether each may use the connection.
func (s *Server) serverBots(server string) ([]connectionBotRow, error) {
	bots, err := s.store.ListBots()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.db.Query(`SELECT bot_id,enabled FROM bot_connection_access WHERE server=?`, server)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var id string
		var on int
		if err := rows.Scan(&id, &on); err != nil {
			return nil, err
		}
		set[id] = on != 0
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]connectionBotRow, 0, len(bots))
	for _, bot := range bots {
		if bot.Archived {
			continue
		}
		on, has := set[bot.ID]
		out = append(out, connectionBotRow{BotID: bot.ID, BotName: bot.Name, Enabled: !has || on})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].BotName) < strings.ToLower(out[j].BotName) })
	return out, nil
}

// routeConnectionBots serves a connection's page:
//
//	GET /api/extensions/mcp/{name}/bots   the current Bots and who may use the connection
//	PUT /api/extensions/mcp/{name}/bots   {all:true} open it to every Bot, or {bot_ids:[...]} to exactly those
func (s *Server) routeConnectionBots(w http.ResponseWriter, r *http.Request, name string) {
	if !s.mcpServerExists(name) {
		writeErr(w, 404, "not_found", "connection not found")
		return
	}
	if r.Method == http.MethodGet {
		list, err := s.serverBots(name)
		if err != nil {
			writeErr(w, 500, "internal", "connection Bots unavailable")
			return
		}
		all := true
		for _, row := range list {
			all = all && row.Enabled
		}
		writeJSON(w, 200, map[string]any{"bots": list, "all": all})
		return
	}
	var in struct {
		All    bool     `json:"all"`
		BotIDs []string `json:"bot_ids"`
	}
	if e := decodeExtensionJSON(r, &in); e != nil || (!in.All && in.BotIDs == nil) {
		writeErr(w, 400, "invalid_request", "all or bot_ids is required")
		return
	}
	known := map[string]bool{}
	list, err := s.serverBots(name)
	if err != nil {
		writeErr(w, 500, "internal", "connection Bots unavailable")
		return
	}
	for _, row := range list {
		known[row.BotID] = true
	}
	for _, id := range in.BotIDs {
		if !known[id] {
			writeErr(w, 400, "invalid_request", "unknown Bot")
			return
		}
	}
	if err := s.store.setServerBots(name, in.All, in.BotIDs); err != nil {
		writeErr(w, 500, "internal", "connection access not saved")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "next_run": true})
}

// accessibleServerViews keeps the listed servers this Bot may use; a failed
// lookup is an error, so the caller lists none.
func (s *Server) accessibleServerViews(ctx context.Context, botID string, servers []extensions.MCPServerView) ([]extensions.MCPServerView, error) {
	names := make([]string, len(servers))
	for i, server := range servers {
		names[i] = server.Name
	}
	allowed, err := s.store.connectionAccessFilter(ctx, botID, names)
	if err != nil {
		return nil, err
	}
	out := make([]extensions.MCPServerView, 0, len(servers))
	for _, server := range servers {
		if allowed[server.Name] {
			out = append(out, server)
		}
	}
	return out, nil
}
