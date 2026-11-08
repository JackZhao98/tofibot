package app

// User groups and organization policy (docs/design/user-groups.md, slice 1:
// root-DB schema, the policy model and its pure resolver). The tables live in
// the control-plane database next to the account tables. Nothing here changes
// what a workspace does yet: an account with no member row resolves to the
// built-in self_serve group, which is today's behaviour exactly.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// The four independent capabilities. Each is one row per group (and per
// account override), never a field of a larger object.
const (
	capAIModels  = "ai_models"
	capComputer  = "computer"
	capWebSearch = "web_search"
	capWebFetch  = "web_fetch"
)

var orgCapabilities = []string{capAIModels, capComputer, capWebSearch, capWebFetch}

const (
	groupSelfServe = "self_serve"
	groupProvided  = "provided"

	// maxCustomGroups bounds the groups an admin creates; built-in groups do not count.
	maxCustomGroups = 32
	maxGroupNameLen = 64

	policyModeProvided = "provided"
	policyModeOwn      = "own"
	computerSourceVM   = "vm"
	computerSourceE2B  = "e2b"
)

// orgGroupsDDL is idempotent. The capability CHECK, validPolicy and the UI
// options change together (design 3.2); the same CHECK guards the override table.
const orgGroupsDDL = `CREATE TABLE IF NOT EXISTS org_groups(
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL UNIQUE COLLATE NOCASE,
 builtin INTEGER NOT NULL DEFAULT 0,
 position INTEGER NOT NULL,
 created_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS org_group_policies(
 group_id TEXT NOT NULL REFERENCES org_groups(id) ON DELETE CASCADE,
 capability TEXT NOT NULL CHECK(capability IN ('ai_models','computer','web_search','web_fetch')),
 mode TEXT NOT NULL, source TEXT NOT NULL DEFAULT '', params TEXT NOT NULL DEFAULT '{}',
 PRIMARY KEY(group_id,capability));
 CREATE TABLE IF NOT EXISTS org_group_members(
 account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 group_id TEXT NOT NULL REFERENCES org_groups(id));
 CREATE TABLE IF NOT EXISTS org_account_policies(
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 capability TEXT NOT NULL CHECK(capability IN ('ai_models','computer','web_search','web_fetch')),
 mode TEXT NOT NULL, source TEXT NOT NULL DEFAULT '', params TEXT NOT NULL DEFAULT '{}',
 PRIMARY KEY(account_id,capability));
 CREATE TABLE IF NOT EXISTS org_resources(
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 config TEXT NOT NULL DEFAULT '{}',
 key_hint TEXT NOT NULL DEFAULT '',
 key_prefix TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL,
 verified_at TEXT, error_code TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS org_usage(
 account_id TEXT NOT NULL, month TEXT NOT NULL, capability TEXT NOT NULL,
 requests INTEGER NOT NULL DEFAULT 0, usd REAL NOT NULL DEFAULT 0, updated_at TEXT NOT NULL,
 PRIMARY KEY(account_id,month,capability));`

// validPolicy is the one place that says which (mode, source) pairs a
// capability accepts (design 3.1).
func validPolicy(capability, mode, source string) bool {
	switch capability {
	case capAIModels:
		return (mode == policyModeOwn || mode == policyModeProvided) && source == ""
	case capComputer:
		switch mode {
		case "host", "none", policyModeOwn:
			return source == ""
		case policyModeProvided:
			return source == computerSourceVM || source == computerSourceE2B
		}
	case capWebSearch:
		return (mode == "off" || mode == policyModeOwn || mode == policyModeProvided) && source == ""
	case capWebFetch:
		return (mode == "builtin" || mode == policyModeOwn || mode == policyModeProvided) && source == ""
	}
	return false
}

// policyRow is one stored (group or override) capability row.
type policyRow struct {
	Capability string
	Mode       string
	Source     string
	Params     string
}

// PolicyValue is one resolved capability.
type PolicyValue struct {
	Mode   string          `json:"mode"`
	Source string          `json:"source,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ResolvedPolicy always holds all four capabilities.
type ResolvedPolicy map[string]PolicyValue

// selfServeDefaults is "today" (design 3.4): the policy of every account that
// has no member row, and the fallback for any capability a row does not set.
func selfServeDefaults() ResolvedPolicy {
	return ResolvedPolicy{
		capAIModels:  {Mode: policyModeOwn},
		capComputer:  {Mode: "host"},
		capWebSearch: {Mode: policyModeOwn},
		capWebFetch:  {Mode: policyModeOwn},
	}
}

// resolvePolicy takes, per capability, the first hit of override row, group
// row, self_serve default. Rows that fail validPolicy are ignored, so a bad
// stored row can neither widen nor break resolution. Pure: no I/O.
func resolvePolicy(group, override []policyRow) ResolvedPolicy {
	out := selfServeDefaults()
	apply := func(rows []policyRow) {
		for _, row := range rows {
			if !slices.Contains(orgCapabilities, row.Capability) || !validPolicy(row.Capability, row.Mode, row.Source) {
				continue
			}
			v := PolicyValue{Mode: row.Mode, Source: row.Source}
			if p := strings.TrimSpace(row.Params); p != "" && p != "{}" && json.Valid([]byte(p)) {
				v.Params = json.RawMessage(p)
			}
			out[row.Capability] = v
		}
	}
	apply(group)
	// Overrides are applied second, so they are the first hit.
	apply(override)
	return out
}

// hostIsKVM says whether this server can reserve VMs. It reads configuration,
// not the live computerOff switch, because it decides a stored default.
func (g *AccountGateway) hostIsKVM() bool {
	return g.config.ComputerBackend == ComputerBackendKVM || g.config.AccountProvisionerSocket != ""
}

func (g *AccountGateway) providedComputerSource() string {
	if g.hostIsKVM() {
		return computerSourceVM
	}
	return computerSourceE2B
}

// builtinGroupPolicies are the stored rows of the two built-in groups.
func builtinGroupPolicies(group, providedComputerSource string) []policyRow {
	switch group {
	case groupSelfServe:
		d := selfServeDefaults()
		rows := make([]policyRow, 0, len(orgCapabilities))
		for _, c := range orgCapabilities {
			rows = append(rows, policyRow{Capability: c, Mode: d[c].Mode, Source: d[c].Source, Params: "{}"})
		}
		return rows
	case groupProvided:
		return []policyRow{
			{Capability: capAIModels, Mode: policyModeProvided, Params: "{}"},
			{Capability: capComputer, Mode: policyModeProvided, Source: providedComputerSource, Params: "{}"},
			{Capability: capWebSearch, Mode: policyModeProvided, Params: "{}"},
			{Capability: capWebFetch, Mode: policyModeProvided, Params: "{}"},
		}
	}
	return nil
}

type orgExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// migrateOrgGroups creates the tables and inserts the two built-in groups with
// their policy rows (INSERT OR IGNORE, so an admin's later edit of "provided"
// survives every restart). It writes no member rows: every existing account
// stays in self_serve. providedComputerSource is "vm" on a KVM host, else "e2b".
func migrateOrgGroups(db orgExecer, providedComputerSource string) error {
	if _, err := db.Exec(orgGroupsDDL); err != nil {
		return err
	}
	at := time.Now().Unix()
	for i, g := range []struct{ id, name string }{{groupSelfServe, "Self-serve"}, {groupProvided, "Provided"}} {
		if _, err := db.Exec(`INSERT OR IGNORE INTO org_groups(id,name,builtin,position,created_at) VALUES(?,?,1,?,?)`, g.id, g.name, i, at); err != nil {
			return err
		}
		for _, row := range builtinGroupPolicies(g.id, providedComputerSource) {
			if _, err := db.Exec(`INSERT OR IGNORE INTO org_group_policies(group_id,capability,mode,source,params) VALUES(?,?,?,?,?)`, g.id, row.Capability, row.Mode, row.Source, row.Params); err != nil {
				return err
			}
		}
	}
	return nil
}

type orgQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func readPolicyRows(ctx context.Context, q orgQuerier, query string, args ...any) ([]policyRow, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []policyRow
	for rows.Next() {
		var r policyRow
		if err := rows.Scan(&r.Capability, &r.Mode, &r.Source, &r.Params); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// policyFor resolves the account's policy from the control database on every
// call (no cache), so a change applies at the next check. The two reads run one
// after the other because the control database has a single connection.
func (g *AccountGateway) policyFor(ctx context.Context, accountID string) (ResolvedPolicy, error) {
	db := g.root.store.db
	group, err := readPolicyRows(ctx, db, `SELECT capability,mode,source,params FROM org_group_policies WHERE group_id=COALESCE((SELECT group_id FROM org_group_members WHERE account_id=?),?)`, accountID, groupSelfServe)
	if err != nil {
		return nil, err
	}
	override, err := readPolicyRows(ctx, db, `SELECT capability,mode,source,params FROM org_account_policies WHERE account_id=?`, accountID)
	if err != nil {
		return nil, err
	}
	return resolvePolicy(group, override), nil
}

// ---- Config closures (design 4.1) ----

// OrgUsageEvent is one use of an organization resource by an account.
type OrgUsageEvent struct {
	Capability string
	Requests   int
	USD        float64
}

// orgSecret is an organization credential, valid only inside the server
// process. It prints as a placeholder under every fmt verb so it cannot reach a
// log or an error string by accident.
type orgSecret struct {
	ID, Kind string
	Value    string
	Hint     string
}

func (o orgSecret) Format(f fmt.State, _ rune) { _, _ = fmt.Fprintf(f, "orgSecret(%s)", o.ID) }

// errOrgResourceMissing is returned when policy says "provided" but the
// organization holds no such resource. There is no fallback to the member's own key.
var errOrgResourceMissing = errors.New("organization resource missing")

// wirePolicy gives a workspace its policy, credential and usage hooks. The
// workspace stores no organization key: each use asks the gateway again.
func (g *AccountGateway) wirePolicy(c *Config, a Account) {
	id := a.ID
	c.OrgPolicy = func(ctx context.Context) (ResolvedPolicy, error) { return g.policyFor(ctx, id) }
	c.OrgCredential = func(ctx context.Context, resource string) (orgSecret, error) { return g.orgCredential(ctx, resource) }
	c.OrgUsage = func(ev OrgUsageEvent) { g.recordOrgUsage(id, ev) }
}

// recordOrgUsage adds one use to the account's month row (UTC month).
func (g *AccountGateway) recordOrgUsage(accountID string, ev OrgUsageEvent) {
	if (ev.Requests == 0 && ev.USD == 0) || !slices.Contains(orgCapabilities, ev.Capability) {
		return
	}
	at := time.Now().UTC()
	_, err := g.root.store.db.Exec(`INSERT INTO org_usage(account_id,month,capability,requests,usd,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(account_id,month,capability) DO UPDATE SET requests=requests+excluded.requests,usd=usd+excluded.usd,updated_at=excluded.updated_at`,
		accountID, at.Format("2006-01"), ev.Capability, ev.Requests, ev.USD, at.Format(time.RFC3339))
	if err != nil {
		log.Printf("[org] usage for account %s: %v", accountID, err)
	}
}
