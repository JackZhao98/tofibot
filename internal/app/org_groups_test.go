package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidPolicyMatrix(t *testing.T) {
	type c struct{ capability, mode, source string }
	valid := []c{
		{capAIModels, "own", ""}, {capAIModels, "provided", ""},
		{capComputer, "host", ""}, {capComputer, "none", ""}, {capComputer, "own", ""},
		{capComputer, "provided", "vm"}, {capComputer, "provided", "e2b"},
		{capWebSearch, "off", ""}, {capWebSearch, "own", ""}, {capWebSearch, "provided", ""},
		{capWebFetch, "builtin", ""}, {capWebFetch, "own", ""}, {capWebFetch, "provided", ""},
	}
	allowed := map[c]bool{}
	for _, v := range valid {
		allowed[v] = true
		if !validPolicy(v.capability, v.mode, v.source) {
			t.Errorf("%v rejected", v)
		}
	}
	modes := []string{"", "own", "provided", "host", "none", "off", "builtin", "bogus"}
	sources := []string{"", "vm", "e2b", "kvm"}
	for _, capability := range append(append([]string{}, orgCapabilities...), "mcp", "") {
		for _, m := range modes {
			for _, s := range sources {
				if got := validPolicy(capability, m, s); got != allowed[c{capability, m, s}] {
					t.Errorf("validPolicy(%q,%q,%q)=%v", capability, m, s, got)
				}
			}
		}
	}
	// The table CHECK and the validator agree on the capability list.
	g := accountFixture(t)
	db := g.root.store.db
	for _, capability := range orgCapabilities {
		if _, err := db.Exec(`INSERT INTO org_group_policies(group_id,capability,mode) VALUES(?,?,?)`, groupProvided, capability+"_x", "own"); err == nil {
			t.Errorf("table accepted capability %q_x", capability)
		}
	}
	if _, err := db.Exec(`INSERT INTO org_group_policies(group_id,capability,mode) VALUES(?,?,?)`, groupProvided, "mcp", "own"); err == nil {
		t.Error("table accepted an unknown capability")
	}
	if _, err := db.Exec(`INSERT INTO org_account_policies(account_id,capability,mode) VALUES(?,?,?)`, "x", "mcp", "own"); err == nil {
		t.Error("override table accepted an unknown capability")
	}
}

func TestResolvePolicyPrecedence(t *testing.T) {
	row := func(c, m, s string) policyRow { return policyRow{Capability: c, Mode: m, Source: s, Params: "{}"} }
	// No rows at all: today's behaviour.
	if got := resolvePolicy(nil, nil); !reflect.DeepEqual(got, selfServeDefaults()) {
		t.Fatalf("defaults: %v", got)
	}
	group := []policyRow{row(capAIModels, "provided", ""), row(capComputer, "provided", "e2b"), row(capWebSearch, "off", "")}
	got := resolvePolicy(group, nil)
	want := ResolvedPolicy{capAIModels: {Mode: "provided"}, capComputer: {Mode: "provided", Source: "e2b"}, capWebSearch: {Mode: "off"}, capWebFetch: {Mode: "own"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group over default: %v", got)
	}
	// An override beats the group for that capability only.
	got = resolvePolicy(group, []policyRow{row(capWebSearch, "own", ""), row(capComputer, "none", "")})
	want = ResolvedPolicy{capAIModels: {Mode: "provided"}, capComputer: {Mode: "none"}, capWebSearch: {Mode: "own"}, capWebFetch: {Mode: "own"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("override over group: %v", got)
	}
	// Override rows are order independent and an override can fall back to default only by absence.
	if !reflect.DeepEqual(resolvePolicy(group, []policyRow{row(capAIModels, "own", "")})[capAIModels], PolicyValue{Mode: "own"}) {
		t.Fatal("override of ai_models lost")
	}
	// Invalid or unknown rows are ignored, never widened into something else.
	bad := []policyRow{row(capAIModels, "bogus", ""), row("mcp", "own", ""), row(capComputer, "provided", "")}
	if got := resolvePolicy(bad, bad); !reflect.DeepEqual(got, selfServeDefaults()) {
		t.Fatalf("invalid rows changed the policy: %v", got)
	}
	// An invalid override does not cancel a valid group row.
	if got := resolvePolicy(group, []policyRow{row(capAIModels, "bogus", "")}); got[capAIModels].Mode != "provided" {
		t.Fatalf("invalid override replaced the group value: %v", got)
	}
}

func TestBuiltinGroupsAreTodaysBehaviour(t *testing.T) {
	g := accountFixture(t)
	ctx := context.Background()
	admin, err := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	// Built-in groups exist, no member rows, every account resolves to self_serve.
	var members int
	if err = g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_group_members`).Scan(&members); err != nil || members != 0 {
		t.Fatalf("member rows after migration: %d %v", members, err)
	}
	got, err := g.policyFor(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	for c, v := range selfServeDefaults() {
		if got[c].Mode != v.Mode || got[c].Source != v.Source {
			t.Errorf("%s resolves to %+v", c, got[c])
		}
	}
	want := map[string]string{capAIModels: "own", capComputer: "host", capWebSearch: "own", capWebFetch: "own"}
	for c, m := range want {
		if got[c].Mode != m {
			t.Errorf("self_serve %s mode %q, want %q", c, got[c].Mode, m)
		}
	}
	stored := func(group string) map[string]string {
		rows, err := readPolicyRows(ctx, g.root.store.db, `SELECT capability,mode,source,params FROM org_group_policies WHERE group_id=?`, group)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, r := range rows {
			out[r.Capability] = r.Mode + "/" + r.Source
		}
		return out
	}
	if s := stored(groupSelfServe); !reflect.DeepEqual(s, map[string]string{capAIModels: "own/", capComputer: "host/", capWebSearch: "own/", capWebFetch: "own/"}) {
		t.Fatalf("self_serve rows: %v", s)
	}
	// No KVM host: the provided group hands out E2B computers.
	if s := stored(groupProvided); !reflect.DeepEqual(s, map[string]string{capAIModels: "provided/", capComputer: "provided/e2b", capWebSearch: "provided/", capWebFetch: "provided/"}) {
		t.Fatalf("provided rows: %v", s)
	}
	var builtin int
	g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_groups WHERE builtin=1`).Scan(&builtin)
	if builtin != 2 {
		t.Fatalf("built-in groups: %d", builtin)
	}

	// On a KVM host the provided group's computer source is vm.
	kvm := newMigrationDB(t)
	if err := migrateOrgGroups(kvm, "vm"); err != nil {
		t.Fatal(err)
	}
	var source string
	kvm.QueryRow(`SELECT source FROM org_group_policies WHERE group_id='provided' AND capability='computer'`).Scan(&source)
	if source != "vm" {
		t.Fatalf("kvm provided computer source: %q", source)
	}
	// The migration is idempotent and never overwrites an admin's edit.
	if _, err := kvm.Exec(`UPDATE org_group_policies SET source='e2b' WHERE group_id='provided' AND capability='computer'`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateOrgGroups(kvm, "vm"); err != nil {
			t.Fatal(err)
		}
	}
	kvm.QueryRow(`SELECT source FROM org_group_policies WHERE group_id='provided' AND capability='computer'`).Scan(&source)
	if source != "e2b" {
		t.Fatalf("migration overwrote an edit: %q", source)
	}
	if g.hostIsKVM() {
		t.Fatal("test gateway claims a KVM host")
	}
	if !(&AccountGateway{config: Config{AccountProvisionerSocket: "/run/x.sock"}}).hostIsKVM() {
		t.Fatal("a provisioner socket means a KVM host")
	}
}

func newMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	g := accountFixture(t)
	for _, table := range []string{"org_usage", "org_resources", "org_account_policies", "org_group_members", "org_group_policies", "org_groups"} {
		if _, err := g.root.store.db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	return g.root.store.db
}

// A database that predates this feature (no org tables) gains them and every
// account stays in self_serve; a second start changes nothing.
func TestOrgMigrationOnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir, Environment: "acceptance", OwnerAuth: true}
	g, err := NewAccountGateway(cfg)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := g.create(context.Background(), "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"org_usage", "org_resources", "org_account_policies", "org_group_members", "org_group_policies", "org_groups"} {
		if _, err = g.root.store.db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		g, err = NewAccountGateway(cfg)
		if err != nil {
			t.Fatal(err)
		}
		pol, err := g.policyFor(context.Background(), admin.ID)
		if err != nil || !reflect.DeepEqual(pol, selfServeDefaults()) {
			t.Fatalf("policy after migration: %v %v", pol, err)
		}
		var n int
		g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_groups`).Scan(&n)
		if n != 2 {
			t.Fatalf("groups: %d", n)
		}
		g.Close()
	}
}

// Single-account mode has no gateway and creates none of this.
func TestSingleAccountModeCreatesNoOrgTables(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'org\_%' ESCAPE '\'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("org tables in single-account mode: %d %v", n, err)
	}
	if s.orgPolicy != nil || s.orgCredential != nil || s.orgUsage != nil {
		t.Fatal("single-account server holds org hooks")
	}
}

// ---- admin API fixtures ----

type orgFixture struct {
	g      *AccountGateway
	admin  Account
	member Account
	ac, mc *http.Cookie
}

func newOrgFixture(t *testing.T) *orgFixture {
	t.Helper()
	g := accountFixture(t)
	ctx := context.Background()
	admin, err := g.create(ctx, "org-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	member, err := g.create(ctx, "org-member", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`); err != nil {
		t.Fatal(err)
	}
	return &orgFixture{g: g, admin: admin, member: member, ac: accountCookie(t, g, admin), mc: accountCookie(t, g, member)}
}

func (f *orgFixture) call(t *testing.T, method, path, body string, c *http.Cookie) (int, string) {
	t.Helper()
	w := accountRequest(f.g, method, path, body, c)
	return w.Code, w.Body.String()
}

func (f *orgFixture) admin_(t *testing.T, method, path, body string, want int) string {
	t.Helper()
	code, out := f.call(t, method, path, body, f.ac)
	if code != want {
		t.Fatalf("%s %s: %d %s (want %d)", method, path, code, out, want)
	}
	return out
}

func orgErrCode(t *testing.T, body string) string {
	t.Helper()
	var e struct {
		Error struct{ Code string }
	}
	if json.Unmarshal([]byte(body), &e) != nil {
		t.Fatalf("not an error body: %s", body)
	}
	return e.Error.Code
}

func (f *orgFixture) groupID(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := f.g.root.store.db.QueryRow(`SELECT id FROM org_groups WHERE name=?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdminGroupsCRUDAndBuiltinsReadonly(t *testing.T) {
	f := newOrgFixture(t)
	var list []groupView
	if err := json.Unmarshal([]byte(f.admin_(t, "GET", "/api/admin/groups", "", 200)), &list); err != nil || len(list) != 2 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if list[0].ID != groupSelfServe || list[1].ID != groupProvided || !list[0].Builtin || list[0].MemberCount != 2 || list[1].MemberCount != 0 {
		t.Fatalf("built-in list: %+v", list)
	}
	if len(list[0].Policies) != 4 {
		t.Fatalf("policies: %v", list[0].Policies)
	}

	// Create: missing capabilities are filled from self_serve.
	out := f.admin_(t, "POST", "/api/admin/groups", `{"name":"Interns","policies":{"web_search":{"mode":"off"},"computer":{"mode":"provided","source":"e2b"}}}`, 201)
	var made groupView
	json.Unmarshal([]byte(out), &made)
	if made.Builtin || made.Policies[capAIModels].Mode != "own" || made.Policies[capWebSearch].Mode != "off" || made.Policies[capComputer].Source != "e2b" {
		t.Fatalf("created group: %s", out)
	}
	if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/groups", `{"name":"interns"}`, 409)); got != "group_exists" {
		t.Fatalf("duplicate name (case-insensitive): %s", got)
	}
	if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/groups", `{"name":"Self-serve"}`, 409)); got != "group_exists" {
		t.Fatalf("built-in name reuse: %s", got)
	}
	for _, body := range []string{
		`{"name":"X","policies":{"computer":{"mode":"provided"}}}`,
		`{"name":"X","policies":{"ai_models":{"mode":"off"}}}`,
		`{"name":"X","policies":{"mcp":{"mode":"own"}}}`,
		`{"name":"X","policies":{"web_fetch":{"mode":"own","params":{"a":1}}}}`,
	} {
		if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/groups", body, 400)); got != "invalid_policy" {
			t.Fatalf("%s: %s", body, got)
		}
	}
	for _, body := range []string{`{"name":""}`, `{"name":"  "}`, `{"name":"` + strings.Repeat("x", 65) + `"}`, `{"name":"a","extra":1}`, `not json`} {
		if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/groups", body, 400)); got != "invalid_request" {
			t.Fatalf("%q: %s", body, got)
		}
	}

	// Patch: rename and whole-replace policies.
	out = f.admin_(t, "PATCH", "/api/admin/groups/"+made.ID, `{"name":"Contractors","policies":{"ai_models":{"mode":"provided"}}}`, 200)
	json.Unmarshal([]byte(out), &made)
	if made.Name != "Contractors" || made.Policies[capAIModels].Mode != "provided" || made.Policies[capWebSearch].Mode != "own" || made.Policies[capComputer].Mode != "host" {
		t.Fatalf("patched group: %s", out)
	}
	if got := orgErrCode(t, f.admin_(t, "PATCH", "/api/admin/groups/"+made.ID, `{"name":"Provided"}`, 409)); got != "group_exists" {
		t.Fatalf("rename onto a built-in name: %s", got)
	}
	if got := orgErrCode(t, f.admin_(t, "PATCH", "/api/admin/groups/"+made.ID, `{"policies":{"web_fetch":{"mode":"nope"}}}`, 400)); got != "invalid_policy" {
		t.Fatalf("patch invalid: %s", got)
	}
	f.admin_(t, "PATCH", "/api/admin/groups/"+made.ID, `{}`, 400)
	f.admin_(t, "PATCH", "/api/admin/groups/"+uuidZero, `{"name":"x"}`, 404)

	// Built-ins: self_serve is frozen, provided keeps its name but its values may change.
	for _, body := range []string{`{"name":"Other"}`, `{"policies":{"ai_models":{"mode":"provided"}}}`} {
		if got := orgErrCode(t, f.admin_(t, "PATCH", "/api/admin/groups/self_serve", body, 409)); got != "group_builtin_readonly" {
			t.Fatalf("self_serve %s: %s", body, got)
		}
	}
	if got := orgErrCode(t, f.admin_(t, "PATCH", "/api/admin/groups/provided", `{"name":"Everything"}`, 409)); got != "group_builtin_readonly" {
		t.Fatalf("provided rename: %s", got)
	}
	f.admin_(t, "PATCH", "/api/admin/groups/provided", `{"name":"Provided","policies":{"computer":{"mode":"provided","source":"vm"}}}`, 200)
	var source string
	f.g.root.store.db.QueryRow(`SELECT source FROM org_group_policies WHERE group_id='provided' AND capability='computer'`).Scan(&source)
	if source != "vm" {
		t.Fatalf("provided edit not stored: %q", source)
	}
	for _, id := range []string{"self_serve", "provided"} {
		if got := orgErrCode(t, f.admin_(t, "DELETE", "/api/admin/groups/"+id, "", 409)); got != "group_builtin_readonly" {
			t.Fatalf("delete %s: %s", id, got)
		}
	}

	// A group with members cannot be deleted; an empty one can.
	f.admin_(t, "PUT", "/api/admin/groups/"+made.ID+"/members", fmt.Sprintf(`{"account_ids":[%q]}`, f.member.ID), 200)
	if got := orgErrCode(t, f.admin_(t, "DELETE", "/api/admin/groups/"+made.ID, "", 409)); got != "group_in_use" {
		t.Fatalf("delete in use: %s", got)
	}
	f.admin_(t, "PUT", "/api/admin/groups/self_serve/members", fmt.Sprintf(`{"account_ids":[%q]}`, f.member.ID), 200)
	f.admin_(t, "DELETE", "/api/admin/groups/"+made.ID, "", 200)
	f.admin_(t, "DELETE", "/api/admin/groups/"+made.ID, "", 404)
	var n int
	f.g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_group_policies WHERE group_id=?`, made.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("policies of a deleted group remain: %d", n)
	}
	f.admin_(t, "POST", "/api/admin/groups", `{"name":"Contractors"}`, 201) // the name is free again
}

func TestAdminGroupLimit(t *testing.T) {
	f := newOrgFixture(t)
	for i := range maxCustomGroups {
		f.admin_(t, "POST", "/api/admin/groups", fmt.Sprintf(`{"name":"g%d"}`, i), 201)
	}
	if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/groups", `{"name":"one-too-many"}`, 409)); got != "group_limit" {
		t.Fatalf("limit: %s", got)
	}
}

func TestAdminBatchMoveIsAllOrNothing(t *testing.T) {
	f := newOrgFixture(t)
	gid := f.groupID(t, "Provided")
	third, err := f.g.create(context.Background(), "third", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	members := func(group string) int {
		var n int
		f.g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_group_members WHERE group_id=?`, group).Scan(&n)
		return n
	}
	// One unknown id rejects the whole batch.
	body := fmt.Sprintf(`{"account_ids":[%q,%q,%q]}`, f.member.ID, third.ID, uuidZero)
	if got := orgErrCode(t, f.admin_(t, "PUT", "/api/admin/groups/"+gid+"/members", body, 404)); got != "not_found" {
		t.Fatalf("unknown account: %s", got)
	}
	if members(gid) != 0 {
		t.Fatal("a rejected batch moved someone")
	}
	body = fmt.Sprintf(`{"account_ids":[%q,"not-an-id"]}`, f.member.ID)
	f.admin_(t, "PUT", "/api/admin/groups/"+gid+"/members", body, 404)
	f.admin_(t, "PUT", "/api/admin/groups/"+uuidZero+"/members", fmt.Sprintf(`{"account_ids":[%q]}`, f.member.ID), 404)
	f.admin_(t, "PUT", "/api/admin/groups/"+gid+"/members", `{"account_ids":[]}`, 400)
	if members(gid) != 0 {
		t.Fatal("a rejected batch moved someone")
	}
	// A good batch moves everyone, duplicates are harmless, and the policy follows.
	body = fmt.Sprintf(`{"account_ids":[%q,%q,%q]}`, f.member.ID, third.ID, f.member.ID)
	out := f.admin_(t, "PUT", "/api/admin/groups/"+gid+"/members", body, 200)
	if !strings.Contains(out, `"moved":2`) || members(gid) != 2 {
		t.Fatalf("batch move: %s members=%d", out, members(gid))
	}
	pol, err := f.g.policyFor(context.Background(), f.member.ID)
	if err != nil || pol[capAIModels].Mode != "provided" || pol[capComputer].Source != "e2b" {
		t.Fatalf("member policy after move: %v %v", pol, err)
	}
	if pol, _ = f.g.policyFor(context.Background(), f.admin.ID); pol[capAIModels].Mode != "own" {
		t.Fatalf("the admin moved with them: %v", pol)
	}
	var list []groupView
	json.Unmarshal([]byte(f.admin_(t, "GET", "/api/admin/groups", "", 200)), &list)
	if list[0].MemberCount != 1 || list[1].MemberCount != 2 {
		t.Fatalf("member counts: %+v", list)
	}
	// Moving into self_serve removes the rows: the account is in the default group again.
	f.admin_(t, "PUT", "/api/admin/groups/self_serve/members", fmt.Sprintf(`{"account_ids":[%q]}`, f.member.ID), 200)
	if members(gid) != 1 {
		t.Fatal("move to self_serve left a row")
	}
}

func TestAdminAccountPolicyOverrides(t *testing.T) {
	f := newOrgFixture(t)
	path := "/api/admin/accounts/" + f.member.ID + "/policy"
	var view struct {
		Group     struct{ ID, Name string }
		Overrides map[string]policyOut
		Effective map[string]PolicyValue
	}
	read := func(out string) {
		view.Overrides, view.Effective = nil, nil
		if err := json.Unmarshal([]byte(out), &view); err != nil {
			t.Fatal(err)
		}
	}
	read(f.admin_(t, "GET", path, "", 200))
	if view.Group.ID != groupSelfServe || len(view.Overrides) != 0 || view.Effective[capAIModels].Mode != "own" {
		t.Fatalf("default view: %+v", view)
	}
	read(f.admin_(t, "PUT", path, fmt.Sprintf(`{"group_id":"provided","overrides":{"web_search":{"mode":"off"}}}`), 200))
	if view.Group.ID != groupProvided || view.Overrides[capWebSearch].Mode != "off" || view.Effective[capWebSearch].Mode != "off" || view.Effective[capAIModels].Mode != "provided" {
		t.Fatalf("after put: %+v", view)
	}
	// group_id alone leaves overrides; overrides alone leaves the group; {} clears.
	read(f.admin_(t, "PUT", path, `{"group_id":"self_serve"}`, 200))
	if view.Group.ID != groupSelfServe || view.Overrides[capWebSearch].Mode != "off" {
		t.Fatalf("group only: %+v", view)
	}
	read(f.admin_(t, "PUT", path, `{"overrides":{"computer":{"mode":"none"},"web_fetch":{"mode":"builtin"}}}`, 200))
	if len(view.Overrides) != 2 || view.Effective[capComputer].Mode != "none" || view.Effective[capWebSearch].Mode != "own" {
		t.Fatalf("whole replace: %+v", view)
	}
	read(f.admin_(t, "PUT", path, `{"overrides":{}}`, 200))
	if len(view.Overrides) != 0 {
		t.Fatalf("clear: %+v", view)
	}
	if got := orgErrCode(t, f.admin_(t, "PUT", path, `{"overrides":{"computer":{"mode":"provided"}}}`, 400)); got != "invalid_policy" {
		t.Fatalf("invalid override: %s", got)
	}
	f.admin_(t, "PUT", path, `{"group_id":"`+uuidZero+`"}`, 404)
	f.admin_(t, "PUT", path, `{}`, 400)
	f.admin_(t, "GET", "/api/admin/accounts/"+uuidZero+"/policy", "", 404)
	f.admin_(t, "GET", "/api/admin/accounts/nope/policy", "", 404)
	f.admin_(t, "DELETE", path, "", 405)
}

// A member reaches none of the organization surface, existing route or not.
func TestMemberForbiddenOnEveryOrgRoute(t *testing.T) {
	f := newOrgFixture(t)
	routes := [][2]string{
		{"GET", "/api/admin/groups"}, {"POST", "/api/admin/groups"}, {"PATCH", "/api/admin/groups/provided"},
		{"DELETE", "/api/admin/groups/provided"}, {"PUT", "/api/admin/groups/provided/members"}, {"GET", "/api/admin/groups/nope/x"},
		{"GET", "/api/admin/accounts/" + f.member.ID + "/policy"}, {"PUT", "/api/admin/accounts/" + f.member.ID + "/policy"},
		{"GET", "/api/admin/accounts/" + f.admin.ID + "/policy"},
		{"GET", "/api/admin/org"}, {"GET", "/api/admin/org/resources"}, {"GET", "/api/admin/org/resources/provider:openai"},
		{"PUT", "/api/admin/org/resources/provider:openai"}, {"DELETE", "/api/admin/org/resources/provider:openai"},
		{"POST", "/api/admin/org/resources/provider:openai/test"}, {"PUT", "/api/admin/org/resources/webtool:tavily"},
		{"GET", "/api/admin/org/web-tools"}, {"PUT", "/api/admin/org/web-tools"}, {"GET", "/api/admin/org/usage"}, {"GET", "/api/admin/org/nothing"},
	}
	for _, r := range routes {
		if code, body := f.call(t, r[0], r[1], `{"name":"x","key":"synthetic-member-key-123456"}`, f.mc); code != 403 && code != 404 {
			t.Errorf("member %s %s: %d %s", r[0], r[1], code, body)
		}
		if code, _ := f.call(t, r[0], r[1], `{}`, nil); code != 401 {
			t.Errorf("anonymous %s %s: %d", r[0], r[1], code)
		}
	}
	// A new admin who has not changed the initial password is refused too.
	if _, err := f.g.root.store.db.Exec(`UPDATE accounts SET role='admin',must_change_password=1 WHERE id=?`, f.member.ID); err != nil {
		t.Fatal(err)
	}
	if code, body := f.call(t, "GET", "/api/admin/groups", "", f.mc); code != 403 {
		t.Errorf("admin with initial password: %d %s", code, body)
	}
	// And nothing happened to the data.
	var n int
	f.g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_groups`).Scan(&n)
	if n != 2 {
		t.Fatalf("groups after member attempts: %d", n)
	}
}

func TestOrgUsageIsAttributedPerAccount(t *testing.T) {
	f := newOrgFixture(t)
	ws, err := f.g.workspace(f.member)
	if err != nil {
		t.Fatal(err)
	}
	if ws.orgPolicy == nil || ws.orgCredential == nil || ws.orgUsage == nil {
		t.Fatal("workspace was not given the organization hooks")
	}
	// Hooks resolve live and, with nothing stored, a credential is missing (no fallback value).
	pol, err := ws.orgPolicy(context.Background())
	if err != nil || !reflect.DeepEqual(pol, selfServeDefaults()) {
		t.Fatalf("policy hook: %v %v", pol, err)
	}
	if _, err = ws.orgCredential(context.Background(), "provider:openai"); err != errOrgResourceMissing {
		t.Fatalf("missing credential: %v", err)
	}
	f.admin_(t, "PUT", "/api/admin/accounts/"+f.member.ID+"/policy", `{"group_id":"provided"}`, 200)
	if pol, _ = ws.orgPolicy(context.Background()); pol[capAIModels].Mode != "provided" {
		t.Fatalf("the hook did not see the change at the next call: %v", pol)
	}
	ws.orgUsage(OrgUsageEvent{Capability: capAIModels, Requests: 1, USD: 0.25})
	ws.orgUsage(OrgUsageEvent{Capability: capAIModels, Requests: 2, USD: 0.5})
	ws.orgUsage(OrgUsageEvent{Capability: "bogus", Requests: 1})
	var requests int
	var usd float64
	if err = f.g.root.store.db.QueryRow(`SELECT requests,usd FROM org_usage WHERE account_id=? AND capability=?`, f.member.ID, capAIModels).Scan(&requests, &usd); err != nil || requests != 3 || usd != 0.75 {
		t.Fatalf("usage row: %d %v %v", requests, usd, err)
	}
	var n int
	f.g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_usage WHERE account_id=?`, f.admin.ID).Scan(&n)
	if n != 0 {
		t.Fatal("usage booked to the wrong account")
	}
	// An organization credential never prints, whatever the verb.
	secret := orgSecret{ID: "provider:openai", Value: "synthetic-never-printed-value"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(verb, secret), "synthetic-never") || strings.Contains(fmt.Sprintf(verb, &secret), "synthetic-never") {
			t.Fatalf("orgSecret printed its value under %s", verb)
		}
	}
}

// ---- organization vault, resources and the write-only rule ----

const orgTestKey = "zq7Lm2Xv-orgKEY-9fHt4Rw1Pd6Bn3Yc"

func substr8(body, key string) string {
	for i := 0; i+8 <= len(key); i++ {
		if strings.Contains(body, key[i:i+8]) {
			return key[i : i+8]
		}
	}
	return ""
}

func fakeOpenAI(t *testing.T, key string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			// A real provider can echo the key it refused; nothing may relay this body.
			w.WriteHeader(401)
			fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, r.Header.Get("Authorization"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"gpt-5","created":1}]}`)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOrgSecretWriteOnly(t *testing.T) {
	f := newOrgFixture(t)
	up := fakeOpenAI(t, orgTestKey)
	f.g.root.providerEndpoints[providerOpenAI] = up.URL + "/v1"
	webProbe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"detail":"bad key `+orgTestKey+`"}`)
	}))
	defer webProbe.Close()
	f.g.orgWebEndpoints = map[string]string{"tavily": webProbe.URL}

	var seen []string
	collect := func(label string, code int, body string) {
		seen = append(seen, body)
		if s := substr8(body, orgTestKey); s != "" {
			t.Fatalf("%s (HTTP %d) leaks %q of the key: %s", label, code, s, body)
		}
	}
	both := func(method, path, body string) {
		t.Helper()
		code, out := f.call(t, method, path, body, f.ac)
		collect("admin "+method+" "+path, code, out)
		code, out = f.call(t, method, path, body, f.mc)
		collect("member "+method+" "+path, code, out)
	}

	// A key the provider refuses is rejected without relaying the provider's body.
	code, out := f.call(t, "PUT", "/api/admin/org/resources/provider:openai", `{"key":"wrong-`+orgTestKey+`"}`, f.ac)
	collect("refused key", code, out)
	if code != 400 || orgErrCode(t, out) != "invalid_key" {
		t.Fatalf("refused key: %d %s", code, out)
	}
	// The real key is stored; the answer shows only the two ends.
	code, out = f.call(t, "PUT", "/api/admin/org/resources/provider:openai", `{"key":"`+orgTestKey+`"}`, f.ac)
	collect("store key", code, out)
	if code != 200 {
		t.Fatalf("store: %d %s", code, out)
	}
	var view orgResourceView
	if err := json.Unmarshal([]byte(out), &view); err != nil || view.Status != "verified" || view.KeyHint != keyHint(orgTestKey) || view.KeyPrefix != keyPrefix(orgTestKey) {
		t.Fatalf("view: %+v %v", view, err)
	}
	// A web key (stored unchecked) and the endpoint order.
	code, out = f.call(t, "PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"`+orgTestKey+`"}`, f.ac)
	collect("store web key", code, out)
	if code != 200 || !strings.Contains(out, `"status":"stored"`) {
		t.Fatalf("store web key: %d %s", code, out)
	}
	code, out = f.call(t, "POST", "/api/admin/org/resources/webtool:tavily/test", ``, f.ac)
	collect("test web key", code, out)
	if code != 400 || orgErrCode(t, out) != "invalid_key" {
		t.Fatalf("web test with a refused key: %d %s", code, out)
	}
	if code, out = f.call(t, "GET", "/api/admin/org/resources/webtool:tavily", "", f.ac); !strings.Contains(out, `"status":"rejected"`) || !strings.Contains(out, "invalid_key") {
		t.Fatalf("rejection not recorded: %d %s", code, out)
	}
	code, out = f.call(t, "POST", "/api/admin/org/resources/provider:openai/test", ``, f.ac)
	collect("test model key", code, out)
	if code != 200 {
		t.Fatalf("model test: %d %s", code, out)
	}

	// Every read surface, as admin and as member.
	both("GET", "/api/admin/org/resources", "")
	both("GET", "/api/admin/org/resources/provider:openai", "")
	both("GET", "/api/admin/org/resources/webtool:tavily", "")
	both("GET", "/api/admin/org/web-tools", "")
	both("GET", "/api/admin/groups", "")
	both("GET", "/api/admin/accounts/"+f.member.ID+"/policy", "")
	for _, path := range []string{"/api/providers", "/api/web-tools", "/api/environments", "/api/config", "/api/computer/credentials", "/api/models", "/api/automation"} {
		both("GET", path, "")
	}
	if len(seen) < 20 {
		t.Fatalf("only %d responses were checked", len(seen))
	}

	// The key is on disk only sealed, and only in the organization vault.
	orgVault, err := os.ReadFile(filepath.Join(f.g.config.DataDir, "org", "secrets", "vault.json"))
	if err != nil || !strings.Contains(string(orgVault), "provider:openai") || strings.Contains(string(orgVault), orgTestKey) {
		t.Fatalf("organization vault: %v", err)
	}
	for _, path := range []string{filepath.Join(f.g.config.DataDir, "secrets", "vault.json")} {
		if raw, err := os.ReadFile(path); err == nil && (strings.Contains(string(raw), "provider:openai") || strings.Contains(string(raw), "webtool:tavily")) {
			t.Fatalf("the root vault holds organization records: %s", path)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(f.g.config.DataDir, "accounts", f.member.ID, "secrets", "vault.json")); err == nil && strings.Contains(string(raw), "provider:") {
		t.Fatal("a workspace vault holds a provider record")
	}
	// The server can still use it (in process only).
	secret, err := f.g.orgCredential(context.Background(), "provider:openai")
	if err != nil || secret.Value != orgTestKey {
		t.Fatalf("credential read: %v", err)
	}
	// Delete removes both the row and the sealed record; missing is reported.
	f.g.root.store.db.Exec(`UPDATE org_group_policies SET mode='provided' WHERE group_id='provided' AND capability='ai_models'`)
	code, out = f.call(t, "DELETE", "/api/admin/org/resources/provider:openai", "", f.ac)
	if code != 200 || !strings.Contains(out, `"capability":"ai_models"`) || !strings.Contains(out, `"provided"`) {
		t.Fatalf("delete: %d %s", code, out)
	}
	if _, err = f.g.orgCredential(context.Background(), "provider:openai"); err != errOrgResourceMissing {
		t.Fatalf("credential after delete: %v", err)
	}
	f.admin_(t, "DELETE", "/api/admin/org/resources/provider:openai", "", 404)
	f.admin_(t, "GET", "/api/admin/org/resources/provider:openai", "", 404)
}

func TestOrgResourceValidationAndKinds(t *testing.T) {
	f := newOrgFixture(t)
	put := func(id, body string, want int) string {
		return f.admin_(t, "PUT", "/api/admin/org/resources/"+id, body, want)
	}
	// Unknown ids, including the searxng address (it lives in web-tools).
	for _, id := range []string{"provider:nope", "webtool:searxng", "computer:other", "web_tools", "x"} {
		put(id, `{"key":"abcdefgh"}`, 404)
	}
	put("provider:openai", `{}`, 400)
	put("provider:openai", `{"key":"has space"}`, 400)
	put("provider:openai", `{"key":"abcdefgh","extra":1}`, 400)
	put("provider:openai", `{"key":"abcdefgh","workspace_id":"wrkspc_abcdefgh12"}`, 400)
	put("provider:anthropic", `{"key":"abcdefgh"}`, 400)
	if got := orgErrCode(t, put("provider:anthropic", `{"key":"abcdefgh","workspace_id":"nope"}`, 400)); got != "invalid_workspace" {
		t.Fatalf("anthropic workspace: %s", got)
	}
	if got := orgErrCode(t, put("webtool:brightdata", `{"key":"abcdefgh"}`, 400)); got != "invalid_zone" {
		t.Fatalf("brightdata zone: %s", got)
	}
	put("webtool:tavily", `{"key":"abcdefgh","zone":"z1"}`, 400)
	put("computer:e2b", `{"key":"abcdefgh","template_id":"bad id!"}`, 400)
	put("provider:openai_compat", `{"base_url":"ftp://x"}`, 400)

	// Web keys store; zone stays readable; delete works.
	out := put("webtool:brightdata", `{"key":"synthetic-bd-key-123456789","zone":"zone_a"}`, 200)
	if !strings.Contains(out, `"zone":"zone_a"`) || strings.Contains(out, "synthetic-bd-key") {
		t.Fatalf("brightdata view: %s", out)
	}
	secret, err := f.g.orgCredential(context.Background(), "webtool:brightdata")
	if err != nil || secret.Value != "synthetic-bd-key-123456789\x00zone_a" || secret.Kind != webProviderKind {
		t.Fatalf("brightdata secret reads in the workspace format: %v", err)
	}

	// E2B: checked against the API; refused and unreachable keys are different codes.
	e2bAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "e2b-synthetic-key-123456" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer e2bAPI.Close()
	t.Setenv("TOFI_E2B_API_BASE", e2bAPI.URL)
	if got := orgErrCode(t, put("computer:e2b", `{"key":"e2b-wrong-key-123456789"}`, 400)); got != "invalid_key" {
		t.Fatalf("e2b refused: %s", got)
	}
	out = put("computer:e2b", `{"key":"e2b-synthetic-key-123456","template_id":"tpl_1"}`, 200)
	if !strings.Contains(out, `"template_id":"tpl_1"`) || strings.Contains(out, "e2b-synthetic") {
		t.Fatalf("e2b view: %s", out)
	}
	e2bAPI.Close()
	if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/org/resources/computer:e2b/test", "", 502)); got != "provider_unreachable" {
		t.Fatalf("e2b unreachable: %s", got)
	}
	// An unreachable provider records nothing against the key.
	if row, _, _ := f.g.getOrgResource(context.Background(), "computer:e2b"); row.Status != "verified" {
		t.Fatalf("status after unreachable: %s", row.Status)
	}

	// OpenAI-compatible endpoint: address, optional key and typed models. A blank
	// key keeps the stored one for the same address only.
	compat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" && got != "Bearer compat-synthetic-key-1234" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"llama-test","context_length":8192}]}`)
	}))
	defer compat.Close()
	out = put("provider:openai_compat", `{"base_url":"`+compat.URL+`/v1","key":"compat-synthetic-key-1234","models":["extra-model"]}`, 200)
	if strings.Contains(out, "compat-synthetic") || !strings.Contains(out, `"base_url"`) || !strings.Contains(out, "extra-model") {
		t.Fatalf("compat view: %s", out)
	}
	put("provider:openai_compat", `{"base_url":"`+compat.URL+`/v1"}`, 200)
	if secret, err = f.g.orgCredential(context.Background(), "provider:openai_compat"); err != nil || !strings.Contains(secret.Value, "compat-synthetic-key-1234") {
		t.Fatal("a blank key did not keep the stored key for the same address")
	}
	put("provider:openai_compat", `{"base_url":"`+compat.URL+`/other"}`, 200)
	if secret, _ = f.g.orgCredential(context.Background(), "provider:openai_compat"); strings.Contains(secret.Value, "compat-synthetic-key-1234") {
		t.Fatal("the stored key followed the endpoint to a different address")
	}
	f.admin_(t, "POST", "/api/admin/org/resources/provider:openai_compat/test", "", 200)

	// The legacy personal-key path is not the organization's: the root server's
	// own vault stays empty of these records.
	if v := f.g.root.secretVault; v != nil {
		v.mu.Lock()
		defer v.mu.Unlock()
		for id := range v.records {
			if strings.HasPrefix(id, "provider:") || strings.HasPrefix(id, "webtool:") || strings.HasPrefix(id, "computer:") {
				t.Fatalf("the root vault holds %s", id)
			}
		}
	}
}

func TestOrgWebTools(t *testing.T) {
	f := newOrgFixture(t)
	var view struct {
		SearchOrder []string `json:"search_order"`
		FetchOrder  []string `json:"fetch_order"`
		Disabled    []string `json:"disabled"`
		SearXNGURL  string   `json:"searxng_url"`
		Providers   []orgWebProvider
	}
	read := func(out string) {
		view.Providers = nil
		if err := json.Unmarshal([]byte(out), &view); err != nil {
			t.Fatal(err)
		}
	}
	read(f.admin_(t, "GET", "/api/admin/org/web-tools", "", 200))
	if !reflect.DeepEqual(view.SearchOrder, defaultSearchOrder) || len(view.Disabled) != 0 || len(view.Providers) != len(webProviderSpecs) {
		t.Fatalf("defaults: %+v", view)
	}
	for _, p := range view.Providers {
		if p.Status != "needs_key" && p.Status != "needs_address" {
			t.Fatalf("provider %s: %s", p.ID, p.Status)
		}
	}
	read(f.admin_(t, "PUT", "/api/admin/org/web-tools", `{"search_order":["brave","tavily"],"disabled":["search/tavily","fetch/firecrawl","search/tavily"],"searxng_url":"http://searx.example.test:8080/","daily_search":50}`, 200))
	if view.SearchOrder[0] != "brave" || len(view.SearchOrder) != len(defaultSearchOrder) || len(view.Disabled) != 2 || view.SearXNGURL == "" {
		t.Fatalf("after put: %+v", view)
	}
	for _, body := range []string{`{}`, `{"search_order":["nope"]}`, `{"disabled":["search/nope"]}`, `{"disabled":["bad"]}`, `{"daily_fetch":-2}`, `{"searxng_url":"javascript:x"}`, `{"x":1}`} {
		f.admin_(t, "PUT", "/api/admin/org/web-tools", body, 400)
	}
	// SearXNG is an address, not a key, and counts as a search source in the missing list.
	_, err := f.g.root.store.db.Exec(`UPDATE org_group_policies SET mode='provided' WHERE group_id='provided'`)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Missing []orgMissing `json:"missing"`
	}
	json.Unmarshal([]byte(f.admin_(t, "GET", "/api/admin/org/resources", "", 200)), &list)
	caps := map[string]bool{}
	for _, m := range list.Missing {
		caps[m.Capability] = true
		if len(m.GroupIDs) != 1 || m.GroupIDs[0] != groupProvided {
			t.Fatalf("missing: %+v", m)
		}
	}
	if !caps[capAIModels] || !caps[capWebFetch] || caps[capWebSearch] {
		t.Fatalf("missing capabilities: %+v", list.Missing)
	}
	read(f.admin_(t, "PUT", "/api/admin/org/web-tools", `{"searxng_url":""}`, 200))
	if view.SearXNGURL != "" {
		t.Fatal("address not cleared")
	}
}

// The legacy owner's workspace shares the root's data directory and so its
// secrets directory (S1). Organization resources never touch it.
func TestLegacyOwnerVaultUntouched(t *testing.T) {
	dir := t.TempDir()
	legacy, err := NewServer(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	salt := []byte("synthetic-salt-16")
	hash, err := legacy.ownerAuth.hash(context.Background(), "SyntheticPassword123!", salt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.store.db.Exec(`INSERT INTO workspace_owner(id,username,email,salt,password_hash,created_at) VALUES(1,'legacy','legacy@example.test',?,?,1)`, salt, hash); err != nil {
		t.Fatal(err)
	}
	const personal = "personal-legacy-key-1234567890"
	if _, err = legacy.storeProviderKey(providerOpenAI, personal); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	g, err := NewAccountGateway(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	rootVault := filepath.Join(dir, "secrets", "vault.json")
	rootKey := filepath.Join(dir, "secrets", "key")
	snapshot := func() string {
		a, e1 := os.ReadFile(rootVault)
		b, e2 := os.ReadFile(rootKey)
		if e1 != nil || e2 != nil {
			t.Fatalf("root secrets: %v %v", e1, e2)
		}
		return string(a) + "\x00" + string(b)
	}
	before := snapshot()
	if !strings.Contains(before, "provider:openai") {
		t.Fatal("fixture: the legacy owner's key is missing")
	}
	owner := Account{ID: "legacy-owner", Username: "legacy", Role: "admin", Legacy: true}
	ws, err := g.workspace(owner)
	if err != nil {
		t.Fatal(err)
	}
	ownerCookie := accountCookie(t, g, owner)
	for _, req := range [][3]string{
		{"PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"org-tavily-key-1234567890"}`},
		{"PUT", "/api/admin/org/web-tools", `{"searxng_url":"http://searx.example.test"}`},
		{"POST", "/api/admin/groups", `{"name":"Team"}`},
	} {
		if w := accountRequest(g, req[0], req[1], req[2], ownerCookie); w.Code >= 300 {
			t.Fatalf("%s %s: %d %s", req[0], req[1], w.Code, w.Body.String())
		}
	}
	if after := snapshot(); after != before {
		t.Fatal("organization writes changed the root secrets directory")
	}
	if state, ok := ws.providerKey(providerOpenAI); !ok || state.Key != personal {
		t.Fatal("the legacy owner lost the personal key")
	}
	if _, ok := ws.webSecret("tavily"); ok {
		t.Fatal("the organization key appeared in the legacy owner's vault")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "org", "secrets", "vault.json"))
	if err != nil || !strings.Contains(string(raw), "webtool:tavily") {
		t.Fatalf("organization vault: %v", err)
	}
	if strings.Contains(string(raw), "provider:openai") {
		t.Fatal("the personal key reached the organization vault")
	}
	if strings.Contains(before, "webtool:tavily") {
		t.Fatal("fixture: root vault already held the org record")
	}
}

// Nothing under <data>/org changes when an admin is renamed, demoted or
// deleted; only the account's membership and override rows go with it.
func TestDeletingAdminKeepsOrgResources(t *testing.T) {
	g, _, data := deleteFixture(t)
	ctx := context.Background()
	first, err := g.create(ctx, "first-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.create(ctx, "second-admin", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	g.root.store.db.Exec(`UPDATE accounts SET role='admin' WHERE id=?`, second.ID)
	fc, sc := accountCookie(t, g, first), accountCookie(t, g, second)

	do := func(method, path, body string, c *http.Cookie, want int) {
		t.Helper()
		if w := accountRequest(g, method, path, body, c); w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	}
	// The second admin stores an organization resource and sits in a group with an override.
	do("PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"org-tavily-key-1234567890"}`, sc, 200)
	do("PUT", "/api/admin/accounts/"+second.ID+"/policy", `{"group_id":"provided","overrides":{"web_search":{"mode":"off"}}}`, fc, 200)
	orgFiles := func() string {
		var out strings.Builder
		for _, p := range []string{"key", "vault.json"} {
			raw, err := os.ReadFile(filepath.Join(data, "org", "secrets", p))
			if err != nil {
				t.Fatal(err)
			}
			out.Write(raw)
		}
		return out.String()
	}
	before := orgFiles()
	count := func(query string, args ...any) int {
		var n int
		if err := g.root.store.db.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(`SELECT COUNT(*) FROM org_group_members WHERE account_id=?`, second.ID) != 1 || count(`SELECT COUNT(*) FROM org_account_policies WHERE account_id=?`, second.ID) != 1 {
		t.Fatal("fixture: membership and override rows missing")
	}

	// Demote and rename: untouched.
	do("PATCH", "/api/admin/accounts/"+second.ID, `{"role":"user"}`, fc, 200)
	do("PATCH", "/api/admin/accounts/"+second.ID, `{"role":"admin"}`, fc, 200)
	if orgFiles() != before || count(`SELECT COUNT(*) FROM org_resources WHERE id='webtool:tavily'`) != 1 {
		t.Fatal("demoting an admin touched the organization resources")
	}

	// Delete the admin who stored it.
	seedAccountData(t, g, data, second)
	do("PATCH", "/api/admin/accounts/"+second.ID, `{"disabled":true}`, fc, 200)
	do("DELETE", "/api/admin/accounts/"+second.ID, deleteBody("second-admin"), fc, 200)
	if accountExists(g, second.ID) {
		t.Fatal("the account was not deleted")
	}
	if count(`SELECT COUNT(*) FROM org_group_members WHERE account_id=?`, second.ID) != 0 || count(`SELECT COUNT(*) FROM org_account_policies WHERE account_id=?`, second.ID) != 0 {
		t.Fatal("membership or override rows outlived the account")
	}
	if orgFiles() != before {
		t.Fatal("deleting an admin changed the organization vault")
	}
	if count(`SELECT COUNT(*) FROM org_resources WHERE id='webtool:tavily'`) != 1 || count(`SELECT COUNT(*) FROM org_groups`) != 2 {
		t.Fatal("deleting an admin removed organization rows")
	}
	secret, err := g.orgCredential(ctx, "webtool:tavily")
	if err != nil || secret.Value != "org-tavily-key-1234567890" {
		t.Fatalf("the organization key is gone: %v", err)
	}
}
