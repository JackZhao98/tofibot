package app

// Admin API for user groups, per-account policy and organization resources
// (design section 6). Every route here is served by the gateway, never by a
// workspace, and only to an admin whose password is updated (the guard of
// adminResources). A member gets 403 on every path under /api/admin/groups,
// /api/admin/org and /api/admin/accounts/{id}/policy, whether or not it exists.
// Errors are stable codes: invalid_policy, group_exists, group_limit,
// group_builtin_readonly, group_in_use, not_found, invalid_key,
// provider_unreachable. No response carries organization key material.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	adminGroupsPath  = "/api/admin/groups"
	adminOrgPath     = "/api/admin/org"
	adminAccountsDir = "/api/admin/accounts/"

	maxGroupMembersBatch = 1000
	orgBodyLimit         = 16 << 10
)

// adminOrg dispatches the organization admin surface; false means the path is not ours.
func (g *AccountGateway) adminOrg(w http.ResponseWriter, r *http.Request, a Account) bool {
	p := r.URL.Path
	groups := p == adminGroupsPath || strings.HasPrefix(p, adminGroupsPath+"/")
	org := p == adminOrgPath || strings.HasPrefix(p, adminOrgPath+"/")
	policy := strings.HasPrefix(p, adminAccountsDir) && strings.HasSuffix(p, "/policy")
	if !groups && !org && !policy {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	switch {
	case groups:
		g.serveGroups(w, r, strings.Trim(strings.TrimPrefix(p, adminGroupsPath), "/"))
	case org:
		g.serveOrg(w, r, strings.Trim(strings.TrimPrefix(p, adminOrgPath), "/"))
	default:
		g.serveAccountPolicy(w, r, strings.TrimSuffix(strings.TrimPrefix(p, adminAccountsDir), "/policy"))
	}
	return true
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeErr(w, 405, "method_not_allowed", "use "+allow)
}

func storageFailure(w http.ResponseWriter, what string, err error) {
	log.Printf("[org] %s: %v", what, err)
	writeErr(w, 500, "storage", "organization settings could not be saved")
}

// ---- policies on the wire ----

type policyIn struct {
	Mode   string          `json:"mode"`
	Source string          `json:"source"`
	Params json.RawMessage `json:"params"`
}

type policyOut struct {
	Mode   string          `json:"mode"`
	Source string          `json:"source,omitempty"`
	Params json.RawMessage `json:"params"`
}

func rowOut(r policyRow) policyOut {
	p := json.RawMessage(r.Params)
	if !json.Valid(p) {
		p = json.RawMessage("{}")
	}
	return policyOut{Mode: r.Mode, Source: r.Source, Params: p}
}

func policyMapOut(rows []policyRow) map[string]policyOut {
	out := map[string]policyOut{}
	for _, r := range rows {
		out[r.Capability] = rowOut(r)
	}
	return out
}

// parsePolicyRows validates an incoming capability map. fill adds a self_serve
// row for each capability the request leaves out (a group always holds all four);
// an override holds only the capabilities the admin sets. v1 stores no params.
func (g *AccountGateway) parsePolicyRows(in map[string]policyIn, fill bool) ([]policyRow, bool) {
	for c := range in {
		if !slices.Contains(orgCapabilities, c) {
			return nil, false
		}
	}
	d := selfServeDefaults()
	var rows []policyRow
	for _, c := range orgCapabilities {
		v, ok := in[c]
		if !ok {
			if fill {
				rows = append(rows, policyRow{Capability: c, Mode: d[c].Mode, Source: d[c].Source, Params: "{}"})
			}
			continue
		}
		if !validPolicy(c, v.Mode, v.Source) {
			return nil, false
		}
		// A VM can be provided only where this server can reserve one.
		if c == capComputer && v.Mode == policyModeProvided && v.Source == computerSourceVM && !g.hostIsKVM() {
			return nil, false
		}
		if p := strings.TrimSpace(string(v.Params)); p != "" && p != "{}" {
			return nil, false
		}
		rows = append(rows, policyRow{Capability: c, Mode: v.Mode, Source: v.Source, Params: "{}"})
	}
	return rows, true
}

func validGroupName(name string) bool {
	n := utf8.RuneCountInString(name)
	if n == 0 || n > maxGroupNameLen {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ---- groups ----

type groupView struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Builtin     bool                 `json:"builtin"`
	Position    int                  `json:"position"`
	MemberCount int                  `json:"member_count"`
	Policies    map[string]policyOut `json:"policies"`
}

type groupHeader struct {
	id, name string
	builtin  bool
	position int
}

func readGroupHeaders(ctx context.Context, q orgQuerier) ([]groupHeader, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,name,builtin,position FROM org_groups ORDER BY position,created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []groupHeader
	for rows.Next() {
		var h groupHeader
		if err := rows.Scan(&h.id, &h.name, &h.builtin, &h.position); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// groupViews builds the full list; the single connection means the reads run in sequence.
func (g *AccountGateway) groupViews(ctx context.Context, only string) ([]groupView, error) {
	db := g.root.store.db
	headers, err := readGroupHeaders(ctx, db)
	if err != nil {
		return nil, err
	}
	policies := map[string][]policyRow{}
	rows, err := db.QueryContext(ctx, `SELECT group_id,capability,mode,source,params FROM org_group_policies`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var r policyRow
		if err = rows.Scan(&id, &r.Capability, &r.Mode, &r.Source, &r.Params); err != nil {
			break
		}
		policies[id] = append(policies[id], r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	rows, err = db.QueryContext(ctx, `SELECT group_id,COUNT(*) FROM org_group_members GROUP BY group_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int
		if err = rows.Scan(&id, &n); err != nil {
			break
		}
		counts[id] = n
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	var accounts int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&accounts); err != nil {
		return nil, err
	}
	// An account with no member row is in self_serve.
	others := 0
	for id, n := range counts {
		if id != groupSelfServe {
			others += n
		}
	}
	counts[groupSelfServe] = max(accounts-others, 0)
	out := []groupView{}
	for _, h := range headers {
		if only != "" && h.id != only {
			continue
		}
		out = append(out, groupView{ID: h.id, Name: h.name, Builtin: h.builtin, Position: h.position, MemberCount: counts[h.id], Policies: policyMapOut(policies[h.id])})
	}
	return out, nil
}

func (g *AccountGateway) groupHeaderOf(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, id string) (groupHeader, bool, error) {
	h := groupHeader{id: id}
	err := q.QueryRowContext(ctx, `SELECT name,builtin,position FROM org_groups WHERE id=?`, id).Scan(&h.name, &h.builtin, &h.position)
	if errors.Is(err, sql.ErrNoRows) {
		return h, false, nil
	}
	return h, err == nil, err
}

func (g *AccountGateway) serveGroups(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	switch {
	case rest == "":
		switch r.Method {
		case http.MethodGet:
			views, err := g.groupViews(r.Context(), "")
			if err != nil {
				storageFailure(w, "list groups", err)
				return
			}
			writeJSON(w, 200, views)
		case http.MethodPost:
			g.createGroup(w, r)
		default:
			methodNotAllowed(w, "GET, POST")
		}
	case len(parts) == 1:
		switch r.Method {
		case http.MethodPatch:
			g.patchGroup(w, r, parts[0])
		case http.MethodDelete:
			g.deleteGroup(w, r, parts[0])
		default:
			methodNotAllowed(w, "PATCH, DELETE")
		}
	case len(parts) == 2 && parts[1] == "members":
		if r.Method != http.MethodPut {
			methodNotAllowed(w, "PUT")
			return
		}
		g.moveMembers(w, r, parts[0])
	default:
		writeErr(w, 404, "not_found", "not found")
	}
}

func (g *AccountGateway) writeGroup(w http.ResponseWriter, ctx context.Context, status int, id string) {
	views, err := g.groupViews(ctx, id)
	if err != nil || len(views) != 1 {
		storageFailure(w, "read group", err)
		return
	}
	writeJSON(w, status, views[0])
}

func insertPolicyRows(ctx context.Context, tx *sql.Tx, table, key, id string, rows []policyRow) error {
	for _, row := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+`(`+key+`,capability,mode,source,params) VALUES(?,?,?,?,?)`, id, row.Capability, row.Mode, row.Source, row.Params); err != nil {
			return err
		}
	}
	return nil
}

func (g *AccountGateway) createGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string              `json:"name"`
		Policies map[string]policyIn `json:"policies"`
	}
	if decodeStrict(r, orgBodyLimit, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid group")
		return
	}
	name := strings.TrimSpace(in.Name)
	if !validGroupName(name) {
		writeErr(w, 400, "invalid_request", "group name must be 1 to 64 characters")
		return
	}
	rows, ok := g.parsePolicyRows(in.Policies, true)
	if !ok {
		writeErr(w, 400, "invalid_policy", "policy is not valid")
		return
	}
	ctx := r.Context()
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err != nil {
		storageFailure(w, "begin create group", err)
		return
	}
	defer tx.Rollback()
	var custom int
	var exists bool
	var position int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM org_groups WHERE builtin=0),EXISTS(SELECT 1 FROM org_groups WHERE name=?),(SELECT COALESCE(MAX(position),0)+1 FROM org_groups)`, name).Scan(&custom, &exists, &position); err != nil {
		storageFailure(w, "check group", err)
		return
	}
	if exists {
		writeErr(w, 409, "group_exists", "a group with this name exists")
		return
	}
	if custom >= maxCustomGroups {
		writeErr(w, 409, "group_limit", "the group limit is reached")
		return
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO org_groups(id,name,builtin,position,created_at) VALUES(?,?,0,?,?)`, id, name, position, time.Now().Unix()); err == nil {
		err = insertPolicyRows(ctx, tx, "org_group_policies", "group_id", id, rows)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		storageFailure(w, "create group", err)
		return
	}
	g.writeGroup(w, ctx, 201, id)
}

func (g *AccountGateway) patchGroup(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Name     *string              `json:"name"`
		Policies *map[string]policyIn `json:"policies"`
	}
	if decodeStrict(r, orgBodyLimit, &in) != nil || (in.Name == nil && in.Policies == nil) {
		writeErr(w, 400, "invalid_request", "invalid group")
		return
	}
	ctx := r.Context()
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err != nil {
		storageFailure(w, "begin patch group", err)
		return
	}
	defer tx.Rollback()
	h, found, err := g.groupHeaderOf(ctx, tx, id)
	if err != nil {
		storageFailure(w, "read group", err)
		return
	}
	if !found {
		writeErr(w, 404, "not_found", "group not found")
		return
	}
	// The built-in groups keep their names; self_serve is also "today", so its values stay.
	var name string
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if h.builtin && name != h.name {
			writeErr(w, 409, "group_builtin_readonly", "a built-in group cannot be renamed")
			return
		}
	}
	if in.Policies != nil && id == groupSelfServe {
		writeErr(w, 409, "group_builtin_readonly", "the Self-serve group cannot be changed")
		return
	}
	var rows []policyRow
	if in.Policies != nil {
		var ok bool
		if rows, ok = g.parsePolicyRows(*in.Policies, true); !ok {
			writeErr(w, 400, "invalid_policy", "policy is not valid")
			return
		}
	}
	if in.Name != nil && !h.builtin {
		if !validGroupName(name) {
			writeErr(w, 400, "invalid_request", "group name must be 1 to 64 characters")
			return
		}
		var clash bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org_groups WHERE name=? AND id<>?)`, name, id).Scan(&clash); err != nil {
			storageFailure(w, "check group name", err)
			return
		}
		if clash {
			writeErr(w, 409, "group_exists", "a group with this name exists")
			return
		}
		if _, err = tx.ExecContext(ctx, `UPDATE org_groups SET name=? WHERE id=?`, name, id); err != nil {
			storageFailure(w, "rename group", err)
			return
		}
	}
	if in.Policies != nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM org_group_policies WHERE group_id=?`, id); err == nil {
			err = insertPolicyRows(ctx, tx, "org_group_policies", "group_id", id, rows)
		}
		if err != nil {
			storageFailure(w, "replace group policies", err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		storageFailure(w, "patch group", err)
		return
	}
	g.writeGroup(w, ctx, 200, id)
}

func (g *AccountGateway) deleteGroup(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err != nil {
		storageFailure(w, "begin delete group", err)
		return
	}
	defer tx.Rollback()
	h, found, err := g.groupHeaderOf(ctx, tx, id)
	if err != nil {
		storageFailure(w, "read group", err)
		return
	}
	if !found {
		writeErr(w, 404, "not_found", "group not found")
		return
	}
	if h.builtin {
		writeErr(w, 409, "group_builtin_readonly", "a built-in group cannot be deleted")
		return
	}
	var members int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM org_group_members WHERE group_id=?`, id).Scan(&members); err != nil {
		storageFailure(w, "count members", err)
		return
	}
	if members > 0 {
		writeErr(w, 409, "group_in_use", "move the members to another group first")
		return
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM org_groups WHERE id=?`, id); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		storageFailure(w, "delete group", err)
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true, "id": id})
}

func validAccountID(id string) bool {
	if id == "legacy-owner" {
		return true
	}
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

// setMembership moves one account to a group inside tx. self_serve is "no row".
func setMembership(ctx context.Context, tx *sql.Tx, accountID, groupID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM org_group_members WHERE account_id=?`, accountID); err != nil {
		return err
	}
	if groupID == groupSelfServe {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO org_group_members(account_id,group_id) VALUES(?,?)`, accountID, groupID)
	return err
}

// moveMembers is the batch move: every id must name an account, or nothing moves.
func (g *AccountGateway) moveMembers(w http.ResponseWriter, r *http.Request, groupID string) {
	var in struct {
		AccountIDs []string `json:"account_ids"`
	}
	if decodeStrict(r, 256<<10, &in) != nil || len(in.AccountIDs) == 0 || len(in.AccountIDs) > maxGroupMembersBatch {
		writeErr(w, 400, "invalid_request", "send 1 to 1000 account_ids")
		return
	}
	ids := []string{}
	for _, id := range in.AccountIDs {
		if !validAccountID(id) {
			writeErr(w, 404, "not_found", "account not found")
			return
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	ctx := r.Context()
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err != nil {
		storageFailure(w, "begin move members", err)
		return
	}
	defer tx.Rollback()
	if _, found, err := g.groupHeaderOf(ctx, tx, groupID); err != nil {
		storageFailure(w, "read group", err)
		return
	} else if !found {
		writeErr(w, 404, "not_found", "group not found")
		return
	}
	for _, id := range ids {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, id).Scan(&exists); err != nil {
			storageFailure(w, "check account", err)
			return
		}
		if !exists {
			writeErr(w, 404, "not_found", "account not found")
			return
		}
	}
	for _, id := range ids {
		if err = setMembership(ctx, tx, id, groupID); err != nil {
			storageFailure(w, "move member", err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		storageFailure(w, "move members", err)
		return
	}
	writeJSON(w, 200, map[string]any{"group_id": groupID, "moved": len(ids)})
}

// ---- per-account policy ----

func (g *AccountGateway) accountPolicyView(ctx context.Context, id string) (map[string]any, bool, error) {
	db := g.root.store.db
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, id).Scan(&exists); err != nil || !exists {
		return nil, false, err
	}
	var gid, gname string
	if err := db.QueryRowContext(ctx, `SELECT g.id,g.name FROM org_groups g WHERE g.id=COALESCE((SELECT group_id FROM org_group_members WHERE account_id=?),?)`, id, groupSelfServe).Scan(&gid, &gname); err != nil {
		return nil, false, err
	}
	overrides, err := readPolicyRows(ctx, db, `SELECT capability,mode,source,params FROM org_account_policies WHERE account_id=?`, id)
	if err != nil {
		return nil, false, err
	}
	effective, err := g.policyFor(ctx, id)
	if err != nil {
		return nil, false, err
	}
	eff := map[string]PolicyValue{}
	for c, v := range effective {
		v.Params = nil
		eff[c] = v
	}
	return map[string]any{"group": map[string]string{"id": gid, "name": gname}, "overrides": policyMapOut(overrides), "effective": eff}, true, nil
}

func (g *AccountGateway) serveAccountPolicy(w http.ResponseWriter, r *http.Request, id string) {
	if !validAccountID(id) {
		writeErr(w, 404, "not_found", "account not found")
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		if !g.putAccountPolicy(w, r, id) {
			return
		}
	default:
		methodNotAllowed(w, "GET, PUT")
		return
	}
	view, found, err := g.accountPolicyView(ctx, id)
	if err != nil {
		storageFailure(w, "read account policy", err)
		return
	}
	if !found {
		writeErr(w, 404, "not_found", "account not found")
		return
	}
	writeJSON(w, 200, view)
}

// putAccountPolicy changes the account's group and/or replaces its overrides in
// one transaction. It reports false after writing an error.
func (g *AccountGateway) putAccountPolicy(w http.ResponseWriter, r *http.Request, id string) bool {
	var in struct {
		GroupID   *string              `json:"group_id"`
		Overrides *map[string]policyIn `json:"overrides"`
	}
	if decodeStrict(r, orgBodyLimit, &in) != nil || (in.GroupID == nil && in.Overrides == nil) {
		writeErr(w, 400, "invalid_request", "send group_id and/or overrides")
		return false
	}
	var rows []policyRow
	if in.Overrides != nil {
		var ok bool
		if rows, ok = g.parsePolicyRows(*in.Overrides, false); !ok {
			writeErr(w, 400, "invalid_policy", "policy is not valid")
			return false
		}
	}
	ctx := r.Context()
	tx, err := g.root.store.db.BeginTx(ctx, nil)
	if err != nil {
		storageFailure(w, "begin account policy", err)
		return false
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, id).Scan(&exists); err != nil {
		storageFailure(w, "check account", err)
		return false
	}
	if !exists {
		writeErr(w, 404, "not_found", "account not found")
		return false
	}
	if in.GroupID != nil {
		if _, found, err := g.groupHeaderOf(ctx, tx, *in.GroupID); err != nil {
			storageFailure(w, "read group", err)
			return false
		} else if !found {
			writeErr(w, 404, "not_found", "group not found")
			return false
		}
		if err = setMembership(ctx, tx, id, *in.GroupID); err != nil {
			storageFailure(w, "set group", err)
			return false
		}
	}
	if in.Overrides != nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM org_account_policies WHERE account_id=?`, id); err == nil {
			err = insertPolicyRows(ctx, tx, "org_account_policies", "account_id", id, rows)
		}
		if err != nil {
			storageFailure(w, "set overrides", err)
			return false
		}
	}
	if err = tx.Commit(); err != nil {
		storageFailure(w, "save account policy", err)
		return false
	}
	return true
}
