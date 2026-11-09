package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/provider"
)

// Synthetic keys only. Each is 16 or more characters, as an organization key must be.
const (
	enforceOrgKey = "org-synthetic-Kq8Zm4Vx7Rt2Wd9H"
	enforceOwnKey = "own-synthetic-Pj3Yn6Bc1Lf5Ts8G"
)

func (f *orgFixture) setOverrides(t *testing.T, overrides string) {
	t.Helper()
	f.admin_(t, "PUT", "/api/admin/accounts/"+f.member.ID+"/policy", `{"overrides":`+overrides+`}`, 200)
}

func (f *orgFixture) memberCall(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	return f.call(t, method, path, body, f.mc)
}

func (f *orgFixture) memberWorkspace(t *testing.T) *Server {
	t.Helper()
	ws, err := f.g.workspace(f.member)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func (f *orgFixture) memberDir() string {
	return filepath.Join(f.g.config.DataDir, "accounts", f.member.ID)
}

// compatUpstream is an OpenAI-compatible endpoint that accepts one bearer key and
// answers every other key with a 401 that echoes it back.
type compatUpstream struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
}

func newCompatUpstream(t *testing.T, key string) *compatUpstream {
	t.Helper()
	u := &compatUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		u.mu.Lock()
		u.auths = append(u.auths, got)
		u.mu.Unlock()
		if got != "Bearer "+key {
			w.WriteHeader(401)
			fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, strings.TrimPrefix(got, "Bearer "))
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			fmt.Fprint(w, `{"data":[{"id":"llama-test","context_length":8192}]}`)
		default:
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
		}
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *compatUpstream) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.auths...)
}

func (f *orgFixture) putOrgCompat(t *testing.T, up *compatUpstream, key string) {
	t.Helper()
	f.admin_(t, "PUT", "/api/admin/org/resources/provider:openai_compat", `{"base_url":"`+up.URL+`/v1","key":"`+key+`"}`, 200)
}

type refusingTransport struct{}

func (refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("no network in this test")
}

// builtinFetchDown makes the built-in fetcher fail, so a fetch reaches the keyed sources.
func builtinFetchDown(t *testing.T) {
	t.Helper()
	webBuiltinTransport = refusingTransport{}
	t.Cleanup(func() { webBuiltinTransport = nil })
}

func chatOnce(ctx context.Context, ws *Server, model string) error {
	p, err := ws.newModelProvider(ctx, model)
	if err != nil {
		return err
	}
	_, err = p.Chat(ctx, &provider.ChatRequest{Model: model, Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	return err
}

func TestMemberCannotWriteProvidedProviderKey(t *testing.T) {
	f := newOrgFixture(t)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	writes := [][3]string{
		{"PUT", "/api/providers/openai/key", `{"key":"` + enforceOwnKey + `"}`},
		{"DELETE", "/api/providers/anthropic/key", ``},
		{"PUT", "/api/providers/compatible/config", `{"base_url":"http://127.0.0.1:1/v1"}`},
		{"DELETE", "/api/providers/compatible/config", ``},
		{"POST", "/api/providers/compatible/models", `{"base_url":"http://127.0.0.1:1/v1"}`},
		{"POST", "/api/auth/codex/connect", ``},
	}
	for _, w := range writes {
		code, out := f.memberCall(t, w[0], w[1], w[2])
		if code != 403 || orgErrCode(t, out) != codeManagedByOrganization {
			t.Errorf("%s %s: %d %s", w[0], w[1], code, out)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(f.memberDir(), "secrets", "vault.json")); err == nil && strings.Contains(string(raw), "provider:") {
		t.Fatal("a refused write reached the workspace vault")
	}
	// Under own the same route is the person's: a malformed key is a 400, not a 403.
	f.setOverrides(t, `{"ai_models":{"mode":"own"}}`)
	if code, out := f.memberCall(t, "PUT", "/api/providers/openai/key", `{"key":"has space"}`); code != 400 {
		t.Fatalf("own policy PUT: %d %s", code, out)
	}
}

func TestProvidedCredentialNeverEntersWorkspaceVault(t *testing.T) {
	f := newOrgFixture(t)
	up := newCompatUpstream(t, enforceOrgKey)
	f.putOrgCompat(t, up, enforceOrgKey)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	ws := f.memberWorkspace(t)
	// The member is not an admin and the endpoint is on loopback: allowed because it is the organization's.
	if err := chatOnce(context.Background(), ws, "compat:llama-test"); err != nil {
		t.Fatalf("a model call on the organization endpoint: %v", err)
	}
	if got := up.seen(); len(got) == 0 || got[len(got)-1] != "Bearer "+enforceOrgKey {
		t.Fatalf("upstream saw %v", got)
	}
	if st := ws.providerStatus(providerCompat); !st.Configured || st.Source != sourceOrganization || st.BaseURL != "" || st.KeyHint != "" {
		t.Fatalf("member view of the provided endpoint: %+v", st)
	}
	if raw, err := os.ReadFile(filepath.Join(f.memberDir(), "secrets", "vault.json")); err == nil && strings.Contains(string(raw), "provider:") {
		t.Fatal("the workspace vault holds a provider record")
	}
	scanTreeForKey(t, f.memberDir(), enforceOrgKey)
	// A link-local address stays unreachable even for an organization endpoint.
	if err := orgEndpointControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("link-local reachable")
	}
	if err := orgEndpointControl("tcp", "10.0.0.8:80", nil); err != nil {
		t.Fatalf("private address refused for an organization endpoint: %v", err)
	}
}

func TestProvidedWithoutOrgKeyFailsNotFallsBack(t *testing.T) {
	f := newOrgFixture(t)
	ws := f.memberWorkspace(t)
	var hits int
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(401)
	}))
	defer up.Close()
	ws.providerEndpoints[providerOpenAI] = up.URL + "/v1"
	if _, err := ws.storeProviderKey(providerOpenAI, enforceOwnKey); err != nil {
		t.Fatal(err)
	}
	if !ws.anyProviderConfigured() {
		t.Fatal("own key not seen under own policy")
	}
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	if ws.anyProviderConfigured() || ws.modelConfigured() {
		t.Fatal("the person's own key still counts while the organization provides the models")
	}
	_, err := ws.providerCredential(context.Background(), providerOpenAI)
	if err == nil || !strings.Contains(err.Error(), codeOrgResourceMissing) || strings.Contains(err.Error(), enforceOwnKey) {
		t.Fatalf("credential: %v", err)
	}
	if err = chatOnce(context.Background(), ws, "gpt-5"); err == nil {
		t.Fatal("a call went out without an organization key")
	}
	mu.Lock()
	n := hits
	mu.Unlock()
	if n != 0 {
		t.Fatalf("the upstream saw %d requests (a fallback to the own key)", n)
	}
	run := Run{Status: "failed", Error: err.Error(), Model: "gpt-5"}
	if fl := run.failure(); fl == nil || fl.Code != codeOrgResourceMissing {
		t.Fatalf("run failure: %+v", fl)
	}
	// Codex is not a way around it either.
	if _, err = ws.providerCredential(context.Background(), providerCodex); err == nil {
		t.Fatal("Codex credential served while models are provided")
	}
	if ws.codexConnected() {
		t.Fatal("Codex counts as connected while models are provided")
	}
}

func TestPolicyChangeAppliesAtNextModelCall(t *testing.T) {
	f := newOrgFixture(t)
	ws := f.memberWorkspace(t)
	var mu sync.Mutex
	var auths []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(500)
	}))
	defer up.Close()
	ws.providerEndpoints[providerOpenAI] = up.URL + "/v1"
	// The organization holds an OpenAI key; the person holds their own.
	org, err := f.g.orgSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = org.put(secretRecord{ID: providerVaultID(providerOpenAI), Name: "k", Kind: modelProviderKind, Target: providerOpenAI, CreatedAt: now()}, enforceOrgKey); err != nil {
		t.Fatal(err)
	}
	if _, err = ws.storeProviderKey(providerOpenAI, enforceOwnKey); err != nil {
		t.Fatal(err)
	}
	call := func() string {
		_ = chatOnce(context.Background(), ws, "gpt-5")
		mu.Lock()
		defer mu.Unlock()
		return auths[len(auths)-1]
	}
	if got := call(); got != "Bearer "+enforceOwnKey {
		t.Fatalf("own policy used %q", got)
	}
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	if got := call(); got != "Bearer "+enforceOrgKey {
		t.Fatalf("after the change to provided the next call used %q", got)
	}
	f.setOverrides(t, `{"ai_models":{"mode":"own"}}`)
	if got := call(); got != "Bearer "+enforceOwnKey {
		t.Fatalf("after the change back the next call used %q", got)
	}
}

// ---- computer ----

type brokerLog struct {
	mu  sync.Mutex
	ops []string
}

func (b *brokerLog) count(op string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, o := range b.ops {
		if o == op {
			n++
		}
	}
	return n
}

type computerPolicyFixture struct {
	*orgFixture
	broker  *brokerLog
	configs map[string]Config
	vms     map[string]*fakeComputer
}

func newComputerPolicyFixture(t *testing.T) *computerPolicyFixture {
	t.Helper()
	sockets, err := os.MkdirTemp("/tmp", "tofi-policy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockets) })
	b := &brokerLog{}
	ln, err := net.Listen("unix", filepath.Join(sockets, "broker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Op        string `json:"op"`
			AccountID string `json:"account_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		b.mu.Lock()
		b.ops = append(b.ops, req.Op)
		b.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"account_id": req.AccountID, "socket": filepath.Join(sockets, req.AccountID, "control.sock")})
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	g, err := NewAccountGateway(Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true, AccountProvisionerSocket: filepath.Join(sockets, "broker.sock"), AccountComputerSocketRoot: sockets, AccountComputerDiskGiB: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	p := &computerPolicyFixture{broker: b, configs: map[string]Config{}, vms: map[string]*fakeComputer{}}
	g.runtimeFactory = func(c Config) (*Server, error) {
		p.configs[c.AccountID] = c
		c.ComputerSocket = "" // the fake computer stands in for the Worker's socket
		s, err := NewServer(c)
		if err != nil {
			return nil, err
		}
		vm := newFakeComputer("fake-" + c.AccountID)
		p.vms[c.AccountID] = vm
		s.computerFactory = func() (computer.Provider, error) { return vm, nil }
		return s, nil
	}
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
	p.orgFixture = &orgFixture{g: g, admin: admin, member: member, ac: accountCookie(t, g, admin), mc: accountCookie(t, g, member)}
	return p
}

func TestComputerNoneReservesNothing(t *testing.T) {
	f := newComputerPolicyFixture(t)
	// A new account is in self_serve, so each one reserves exactly one VM.
	if n := f.broker.count("reserve"); n != 2 {
		t.Fatalf("reservations at creation: %d", n)
	}
	f.setOverrides(t, `{"computer":{"mode":"none"}}`)
	if _, err := f.g.workspace(f.member); err != nil {
		t.Fatal(err)
	}
	c := f.configs[f.member.ID]
	if !c.ComputerStartDetached {
		t.Fatal("a computer=none workspace starts attached")
	}
	if err := c.ComputerEnsure(context.Background()); err == nil || !strings.Contains(err.Error(), codeCapabilityOff) {
		t.Fatalf("ensure under none: %v", err)
	}
	if n := f.broker.count("ensure"); n != 0 {
		t.Fatalf("the Worker was asked to ensure a computer for a none account: %d", n)
	}
	if ws := f.memberWorkspace(t); ws.computer() != nil {
		t.Fatal("a VM is attached under none")
	}
	// The host account (self_serve) is served as before.
	if _, err := f.g.workspace(f.admin); err != nil {
		t.Fatal(err)
	}
	ca := f.configs[f.admin.ID]
	if ca.ComputerStartDetached {
		t.Fatal("a host account starts detached")
	}
	if err := ca.ComputerEnsure(context.Background()); err != nil || f.broker.count("ensure") != 1 {
		t.Fatalf("host ensure: %v (%d)", err, f.broker.count("ensure"))
	}
	// own and provided/e2b hold no VM either.
	for _, pol := range []string{`{"computer":{"mode":"own"}}`, `{"computer":{"mode":"provided","source":"e2b"}}`} {
		f.setOverrides(t, pol)
		if err := ca.ComputerEnsure(context.Background()); err != nil {
			t.Fatal(err) // the admin is unaffected
		}
		if err := c.ComputerEnsure(context.Background()); err == nil {
			t.Fatalf("ensure allowed under %s", pol)
		}
	}
	// No admin attach, no E2B key, no environment switch for a none account.
	f.setOverrides(t, `{"computer":{"mode":"none"}}`)
	if code, out := f.call(t, "POST", "/api/admin/accounts/"+f.member.ID+"/computer/attach", ``, f.ac); code != 403 || orgErrCode(t, out) != codeCapabilityOff {
		t.Fatalf("admin attach: %d %s", code, out)
	}
	for _, r := range [][3]string{{"PUT", "/api/environments/e2b", `{"api_key":"` + enforceOwnKey + `"}`}, {"DELETE", "/api/environments/e2b", ``}, {"POST", "/api/environments/e2b/use", ``}} {
		if code, out := f.memberCall(t, r[0], r[1], r[2]); code != 403 || orgErrCode(t, out) != codeCapabilityOff {
			t.Errorf("%s %s: %d %s", r[0], r[1], code, out)
		}
	}
	// A provided computer is the organization's: a different code.
	f.setOverrides(t, `{"computer":{"mode":"provided","source":"e2b"}}`)
	if code, out := f.memberCall(t, "PUT", "/api/environments/e2b", `{"api_key":"`+enforceOwnKey+`"}`); code != 403 || orgErrCode(t, out) != codeManagedByOrganization {
		t.Fatalf("provided PUT: %d %s", code, out)
	}
	code, out := f.memberCall(t, "GET", "/api/environments", "")
	var view struct{ Policy capabilityView }
	if code != 200 || json.Unmarshal([]byte(out), &view) != nil || view.Policy.Source != sourceOrganization {
		t.Fatalf("environments.policy: %d %s", code, out)
	}
}

func TestComputerPolicyDetachDrains(t *testing.T) {
	f := newComputerPolicyFixture(t)
	ws := f.memberWorkspace(t)
	if err := ws.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ws.computer() == nil {
		t.Fatal("not attached")
	}
	// A policy that keeps the VM leaves it alone.
	f.setOverrides(t, `{"computer":{"mode":"host"}}`)
	f.g.policyApply.Wait()
	if ws.computer() == nil {
		t.Fatal("host policy detached the VM")
	}
	f.setOverrides(t, `{"computer":{"mode":"none"}}`)
	f.g.policyApply.Wait()
	if ws.computer() != nil {
		t.Fatal("the VM is still attached after the policy changed to none")
	}
	var status string
	if err := ws.store.db.QueryRow(`SELECT status FROM computers WHERE kind='vm' ORDER BY created_at LIMIT 1`).Scan(&status); err != nil || status != computerRowDetached {
		t.Fatalf("computer row %q %v", status, err)
	}
	// A group move applies the same way.
	f.admin_(t, "PUT", "/api/admin/accounts/"+f.member.ID+"/policy", `{"overrides":{}}`, 200)
	if err := ws.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	gid := f.admin_(t, "POST", "/api/admin/groups", `{"name":"no computers","policies":{"computer":{"mode":"none"}}}`, 201)
	var created struct{ ID string }
	_ = json.Unmarshal([]byte(gid), &created)
	f.admin_(t, "PUT", "/api/admin/groups/"+created.ID+"/members", `{"account_ids":["`+f.member.ID+`"]}`, 200)
	f.g.policyApply.Wait()
	if ws.computer() != nil {
		t.Fatal("moving into a none group left the VM attached")
	}
}

// ---- web search and fetch ----

func TestWebSearchOffRegistersNoTool(t *testing.T) {
	f := newOrgFixture(t)
	ws := f.memberWorkspace(t)
	if err := ws.storeWebSecret("tavily", "Tavily", enforceOwnKey, ""); err != nil {
		t.Fatal(err)
	}
	run := Run{Model: "gpt-5"}
	tools := ws.webTools(run)
	if !toolNames(tools)["web_search"] || !toolNames(tools)["web_fetch"] {
		t.Fatalf("own policy tools: %v", toolNames(tools))
	}
	var search Tool
	for _, tl := range tools {
		if tl.Name == "web_search" {
			search = tl
		}
	}
	f.setOverrides(t, `{"web_search":{"mode":"off"}}`)
	names := toolNames(ws.webTools(run))
	if names["web_search"] || !names["web_fetch"] {
		t.Fatalf("off hides only search: %v", names)
	}
	// A tool registered before the change refuses at Execute.
	_, err := search.Execute(context.Background(), json.RawMessage(`{"query":"x"}`))
	if outcomeCode(err) != codeCapabilityOff {
		t.Fatalf("registered tool under off: %v", err)
	}
	if ws.searchUses("claude-sonnet-5-5", nil) != 0 {
		t.Fatal("the model's own search stays on")
	}
	// The run context says so, once, and only then.
	if note := policyNotesFor(ws.policy()); !strings.Contains(note, policyNoSearch) || strings.Contains(note, policyNoComputer) {
		t.Fatalf("note: %q", note)
	}
	// The settings tool names the policy.
	if mode := managedSettingMode("automation.web_search", ws.policy()); mode != "off" {
		t.Fatalf("managed mode %q", mode)
	}
	// And the web-tools API refuses writes with capability_off.
	if code, out := f.memberCall(t, "PUT", "/api/web-tools/providers/tavily", `{"key":"`+enforceOwnKey+`"}`); code != 403 || orgErrCode(t, out) != codeCapabilityOff {
		t.Fatalf("PUT under off: %d %s", code, out)
	}
	code, out := f.memberCall(t, "GET", "/api/web-tools", "")
	var view struct {
		Managed map[string]string
		Search  []webEntry
	}
	if code != 200 || json.Unmarshal([]byte(out), &view) != nil || view.Managed["search"] != "off" || len(view.Search) != 0 {
		t.Fatalf("view: %d %s", code, out)
	}
}

func TestWebProvidedUsesOrgKeysOrderAndReportsUsage(t *testing.T) {
	builtinFetchDown(t)
	f := newOrgFixture(t)
	ws := f.memberWorkspace(t)
	var mu sync.Mutex
	var auths []string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if strings.Contains(r.URL.Path, "scrape") {
			fmt.Fprint(w, `{"success":true,"data":{"markdown":"page text","metadata":{"title":"T","sourceURL":"https://example.com/","statusCode":200}}}`)
			return
		}
		fmt.Fprint(w, `{"results":[{"title":"A","url":"https://example.com/a","content":"c"}]}`)
	}))
	defer vendor.Close()
	ws.webEndpoints = map[string]string{"tavily": vendor.URL + "/search", "firecrawl": vendor.URL + "/scrape"}
	if err := ws.storeWebSecret("tavily", "Tavily", enforceOwnKey, ""); err != nil {
		t.Fatal(err)
	}
	if err := ws.storeWebSecret("firecrawl", "Firecrawl", enforceOwnKey, ""); err != nil {
		t.Fatal(err)
	}
	f.admin_(t, "PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"`+enforceOrgKey+`"}`, 200)
	f.admin_(t, "PUT", "/api/admin/org/resources/webtool:firecrawl", `{"key":"`+enforceOrgKey+`"}`, 200)
	f.admin_(t, "PUT", "/api/admin/org/web-tools", `{"search_order":["tavily","brave","searxng","computer"]}`, 200)
	f.setOverrides(t, `{"web_search":{"mode":"provided"},"web_fetch":{"mode":"provided"}}`)
	tools := ws.webTools(Run{Model: "gpt-5"})
	run := func(name, args string) {
		for _, tl := range tools {
			if tl.Name == name {
				if _, err := tl.Execute(context.Background(), json.RawMessage(args)); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				return
			}
		}
		t.Fatalf("%s not registered", name)
	}
	run("web_search", `{"query":"tofu"}`)
	run("web_fetch", `{"url":"https://example.com/"}`)
	mu.Lock()
	got := append([]string(nil), auths...)
	mu.Unlock()
	for _, a := range got {
		if a != "Bearer "+enforceOrgKey {
			t.Fatalf("a call used %q, not the organization key", a)
		}
	}
	if len(got) < 2 {
		t.Fatalf("vendor calls: %v", got)
	}
	// Usage is booked to this account only, per capability.
	for _, c := range []string{capWebSearch, capWebFetch} {
		var n int
		f.g.root.store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM org_usage WHERE account_id=? AND capability=?`, f.member.ID, c).Scan(&n)
		if n != 1 {
			t.Fatalf("%s usage requests %d", c, n)
		}
	}
	// Own keys report nothing.
	f.setOverrides(t, `{"web_search":{"mode":"own"}}`)
	tools = ws.webTools(Run{Model: "gpt-5"})
	run("web_search", `{"query":"tofu"}`)
	mu.Lock()
	last := auths[len(auths)-1]
	mu.Unlock()
	if last != "Bearer "+enforceOwnKey {
		t.Fatalf("own policy used %q", last)
	}
	var n int
	f.g.root.store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM org_usage WHERE account_id=? AND capability=?`, f.member.ID, capWebSearch).Scan(&n)
	if n != 1 {
		t.Fatalf("an own-key search was reported: %d", n)
	}
	// builtin leaves only the built-in fetcher; its writes are capability_off.
	f.setOverrides(t, `{"web_fetch":{"mode":"builtin"}}`)
	if chain := ws.fetchChain(nil); len(chain) != 1 || chain[0].ID() != "builtin" {
		t.Fatalf("fetch chain under builtin: %d", len(chain))
	}
	if code, out := f.memberCall(t, "PUT", "/api/web-tools/providers/firecrawl", `{"key":"`+enforceOwnKey+`"}`); code != 403 || orgErrCode(t, out) != codeCapabilityOff {
		t.Fatalf("PUT under builtin: %d %s", code, out)
	}
	f.setOverrides(t, `{"web_fetch":{"mode":"provided"}}`)
	if code, out := f.memberCall(t, "PUT", "/api/web-tools/fetch/order", `{"order":["firecrawl"]}`); code != 403 || orgErrCode(t, out) != codeManagedByOrganization {
		t.Fatalf("order under provided: %d %s", code, out)
	}
}

// ---- usage ----

func TestOrgUsageAttributedPerAccountEnforced(t *testing.T) {
	f := newOrgFixture(t)
	up := newCompatUpstream(t, enforceOrgKey)
	f.putOrgCompat(t, up, enforceOrgKey)
	ws := f.memberWorkspace(t)
	other, err := f.g.workspace(f.admin)
	if err != nil {
		t.Fatal(err)
	}
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	ws.noteOrgModelUse("compat:llama-test", provider.Usage{InputTokens: 10, OutputTokens: 2})
	ws.noteOrgModelUse("gpt-5", provider.Usage{InputTokens: 1000, OutputTokens: 10})
	other.noteOrgModelUse("gpt-5", provider.Usage{InputTokens: 1000, OutputTokens: 10}) // the admin is self_serve: own key, nothing reported
	var n int
	var usd float64
	f.g.root.store.db.QueryRow(`SELECT requests,usd FROM org_usage WHERE account_id=? AND capability=?`, f.member.ID, capAIModels).Scan(&n, &usd)
	if n != 2 || usd <= 0 {
		t.Fatalf("member usage: %d %v", n, usd)
	}
	f.g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_usage WHERE account_id=?`, f.admin.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("an own-key call was reported for the admin: %d", n)
	}
}

// ---- the whole surface ----

func scanTreeForKey(t *testing.T, dir, key string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if raw, e := os.ReadFile(path); e == nil {
			if s := substr8(string(raw), key); s != "" {
				t.Errorf("%s holds %q of the organization key", path, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOrgSecretNeverInMemberResponses(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	builtinFetchDown(t)
	f := newOrgFixture(t)
	ws := f.memberWorkspace(t)
	up := newCompatUpstream(t, enforceOrgKey)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+enforceOrgKey {
			w.WriteHeader(401)
			fmt.Fprintf(w, `{"detail":"bad key %s"}`, r.Header.Get("Authorization"))
			return
		}
		if strings.Contains(r.URL.Path, "scrape") {
			fmt.Fprint(w, `{"success":true,"data":{"markdown":"page text","metadata":{"title":"T","sourceURL":"https://example.com/","statusCode":200}}}`)
			return
		}
		fmt.Fprint(w, `{"results":[{"title":"A","url":"https://example.com/a","content":"c"}]}`)
	}))
	defer vendor.Close()
	ws.webEndpoints = map[string]string{"tavily": vendor.URL + "/search", "firecrawl": vendor.URL + "/scrape"}
	f.putOrgCompat(t, up, enforceOrgKey)
	// OpenAI answers 401 and echoes whatever it was given.
	echo := newCompatUpstream(t, "a-different-key-nobody-has")
	ws.providerEndpoints[providerOpenAI] = echo.URL + "/v1"
	org, _ := f.g.orgSecrets()
	if _, _, err := org.put(secretRecord{ID: providerVaultID(providerOpenAI), Name: "k", Kind: modelProviderKind, Target: providerOpenAI, CreatedAt: now()}, enforceOrgKey); err != nil {
		t.Fatal(err)
	}
	if err := f.g.saveOrgResource(context.Background(), orgResourceRow{ID: providerVaultID(providerOpenAI), Kind: modelProviderKind, Config: "{}", Status: orgStatusVerified, UpdatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	f.admin_(t, "PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"`+enforceOrgKey+`"}`, 200)
	f.admin_(t, "PUT", "/api/admin/org/resources/webtool:firecrawl", `{"key":"`+enforceOrgKey+`"}`, 200)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"},"web_search":{"mode":"provided"},"web_fetch":{"mode":"provided"}}`)

	var responses []string
	record := func(label, body string) {
		responses = append(responses, body)
		if s := substr8(body, enforceOrgKey); s != "" {
			t.Fatalf("%s leaks %q of the organization key: %s", label, s, body)
		}
	}
	ctx := context.Background()
	// One model call, one search, one fetch.
	if err := chatOnce(ctx, ws, "compat:llama-test"); err != nil {
		t.Fatalf("model call: %v", err)
	}
	tools := ws.webTools(Run{Model: "gpt-5"})
	for _, c := range [][2]string{{"web_search", `{"query":"tofu"}`}, {"web_fetch", `{"url":"https://example.com/"}`}} {
		for _, tl := range tools {
			if tl.Name == c[0] {
				out, err := tl.Execute(ctx, json.RawMessage(c[1]))
				if err != nil {
					t.Fatalf("%s: %v", c[0], err)
				}
				record(c[0]+" result", out)
			}
		}
	}
	// One failing upstream authentication that echoes the key.
	err := chatOnce(ctx, ws, "gpt-5")
	if err == nil {
		t.Fatal("expected the upstream refusal")
	}
	record("model error", err.Error())
	// A down organization endpoint must not give its address to the member either.
	up.Close()
	f.setOverrides(t, `{"ai_models":{"mode":"provided"},"web_search":{"mode":"provided"},"web_fetch":{"mode":"provided"}}`)
	derr := chatOnce(ctx, ws, "compat:llama-test")
	if derr == nil {
		t.Fatal("a stopped endpoint answered")
	}
	hostPort := strings.TrimPrefix(up.URL, "http://")
	for _, text := range []string{derr.Error(), ws.redactRunError("compat:llama-test", fmt.Errorf("Post %q: dial tcp %s: connect: connection refused (key %s)", up.URL+"/v1/chat/completions", hostPort, enforceOrgKey))} {
		if strings.Contains(text, hostPort) || strings.Contains(text, up.URL) || substr8(text, enforceOrgKey) != "" {
			t.Fatalf("the organization endpoint or key reached an error: %s", text)
		}
	}
	derrRun := Run{Status: "failed", Error: ws.redactRunError("compat:llama-test", derr), Model: "compat:llama-test"}
	rawRun, _ := json.Marshal(derrRun)
	record("run record of a down endpoint", string(rawRun))
	if strings.Contains(string(rawRun), hostPort) {
		t.Fatal("the endpoint address is in the run record")
	}
	if orgFailure(err.Error()) != codeProviderAuthFailed {
		t.Fatalf("the refusal is not a named code: %v", err)
	}
	run := Run{Status: "failed", Error: err.Error(), Model: "gpt-5"}
	raw, _ := json.Marshal(run)
	record("run record", string(raw))
	if run.failure().Code != codeProviderAuthFailed {
		t.Fatalf("failure code %s", run.failure().Code)
	}
	// The refusal is recorded on the organization resource, not only logged.
	if row, _, _ := f.g.getOrgResource(ctx, providerVaultID(providerOpenAI)); row.Status != orgStatusRejected || row.ErrorCode != codeProviderAuthFailed {
		t.Fatalf("the model key's rejection was not recorded: %+v", row)
	}
	// A vendor that refuses the key and echoes it.
	f.admin_(t, "PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"`+enforceOrgKey+`-x"}`, 200)
	for _, tl := range tools {
		if tl.Name == "web_search" {
			_, e := tl.Execute(ctx, json.RawMessage(`{"query":"tofu"}`))
			if outcomeCode(e) != "web_auth_failed" {
				t.Fatalf("a refused organization key must be a named failure: %v", e)
			}
			record("search failure", e.Error())
		}
	}
	if row, _, _ := f.g.getOrgResource(ctx, webVaultID("tavily")); row.Status != orgStatusRejected {
		t.Fatalf("the web key's rejection was not recorded: %+v", row)
	}
	// The organization's E2B computer: a sandbox is made, the key is refused once and
	// echoed back by E2B, and the member's own surfaces show none of it.
	e2bFake := newOrgE2BFake(t, enforceOrgKey)
	f.seedOrgE2B(t, enforceOrgKey, `{"template_id":"tpl_custom"}`)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"},"web_search":{"mode":"provided"},"web_fetch":{"mode":"provided"},"computer":{"mode":"provided","source":"e2b"}}`)
	f.g.policyApply.Wait()
	orgWait(t, "the organization computer", func() bool { return ws.attachedIsOrgE2B() })
	orgWait(t, "the organization template", func() bool {
		_ = ws.orgE2BProvider().RefreshTemplate(ctx)
		return ws.orgE2BProvider().Status().Phase == "ready"
	})
	if e := e2bAction(ws); e != nil {
		t.Fatalf("E2B action: %v", e)
	}
	e2bFake.mu.Lock()
	for _, u := range e2bFake.uploads {
		record("write into the sandbox", u)
	}
	for _, b := range e2bFake.createBodies {
		record("create-sandbox request", b)
	}
	e2bFake.createStatus = 401
	e2bFake.mu.Unlock()
	if e := ws.orgE2BProvider().Remove(ctx); e != nil {
		t.Fatal(e)
	}
	e2bErr := e2bAction(ws)
	if e2bErr == nil {
		t.Fatal("the refused E2B key worked")
	}
	record("E2B error", e2bErr.Error())
	record("E2B error as a run failure", jsonText(Run{Status: "failed", Error: ws.redactRunError("gpt-5", e2bErr)}))
	if row, _, _ := f.g.getOrgResource(ctx, e2bKeyVaultID); row.Status != orgStatusRejected {
		t.Fatalf("the E2B key's rejection was not recorded: %+v", row)
	}
	// Every member-reachable response.
	for _, path := range []string{"/api/providers", "/api/web-tools", "/api/environments", "/api/config", "/api/models", "/api/automation-settings", "/api/model-settings", "/api/usage", "/api/computer/capabilities", "/api/secrets"} {
		code, out := f.memberCall(t, "GET", path, "")
		record("GET "+path+fmt.Sprintf(" (%d)", code), out)
	}
	// The real exports: the portable export of everything and the bundle the account-deletion export seals.
	code, out := f.memberCall(t, "POST", "/api/portability/export", `{"selection":{"categories":`+jsonText(portableDefaultCategories)+`}}`)
	if code != 200 || len(out) < 100 {
		t.Fatalf("portable export: %d %.200s", code, out)
	}
	record("portable export", out)
	bundle, _, err := f.g.exportBundle(ctx, f.member)
	if err != nil || len(bundle) < 100 {
		t.Fatalf("account export bundle: %v (%d bytes)", err, len(bundle))
	}
	record("account export bundle", string(bundle))
	// Disk: the workspace database, WAL, vault and every other file; and the log.
	scanTreeForKey(t, f.memberDir(), enforceOrgKey)
	if s := substr8(logs.String(), enforceOrgKey); s != "" {
		t.Fatalf("the server log holds %q of the key", s)
	}
	if len(responses) < 12 {
		t.Fatalf("only %d responses checked", len(responses))
	}
}

func TestCapabilitiesInConfigAndProviderSource(t *testing.T) {
	f := newOrgFixture(t)
	up := newCompatUpstream(t, enforceOrgKey)
	f.putOrgCompat(t, up, enforceOrgKey)
	code, out := f.memberCall(t, "GET", "/api/config", "")
	var cfg struct{ Capabilities map[string]capabilityView }
	if code != 200 || json.Unmarshal([]byte(out), &cfg) != nil || cfg.Capabilities[capAIModels].Source != sourceOwn || cfg.Capabilities[capComputer].Mode != "host" {
		t.Fatalf("config under self_serve: %d %s", code, out)
	}
	f.setOverrides(t, `{"ai_models":{"mode":"provided"},"web_search":{"mode":"off"}}`)
	_, out = f.memberCall(t, "GET", "/api/config", "")
	_ = json.Unmarshal([]byte(out), &cfg)
	if cfg.Capabilities[capAIModels].Source != sourceOrganization || cfg.Capabilities[capWebSearch].Mode != "off" || cfg.Capabilities[capWebSearch].Source != "none" {
		t.Fatalf("config under policy: %s", out)
	}
	_, out = f.memberCall(t, "GET", "/api/providers", "")
	var providers struct{ Providers []ProviderStatus }
	if json.Unmarshal([]byte(out), &providers) != nil {
		t.Fatal(out)
	}
	for _, p := range providers.Providers {
		if p.Source != sourceOrganization {
			t.Errorf("provider %s source %q", p.ID, p.Source)
		}
	}
	if strings.Contains(out, up.URL) {
		t.Fatalf("the organization endpoint address is in a member response: %s", out)
	}
}

func TestMissingReportsAccountOverrides(t *testing.T) {
	f := newOrgFixture(t)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	out := f.admin_(t, "GET", "/api/admin/org/resources", "", 200)
	var res struct{ Missing []orgMissing }
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range res.Missing {
		if m.Capability == capAIModels {
			for _, id := range m.AccountIDs {
				found = found || id == f.member.ID
			}
		}
	}
	if !found {
		t.Fatalf("override not reported: %s", out)
	}
}

func TestPolicyNotesAreShort(t *testing.T) {
	p := selfServeDefaults()
	if policyNotesFor(p) != "" {
		t.Fatal("a note under today's policy")
	}
	p[capWebSearch] = PolicyValue{Mode: "off"}
	p[capComputer] = PolicyValue{Mode: "none"}
	note := policyNotesFor(p)
	if !strings.HasPrefix(note, "[policy]") || !strings.HasSuffix(note, "[/policy]") || len(note) > 320 {
		t.Fatalf("note: %q (%d)", note, len(note))
	}
	if kind := contextKindFor(note); kind != ctxKindPolicy {
		t.Fatalf("kind %s", kind)
	}
}

func jsonText(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
