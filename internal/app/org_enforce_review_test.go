package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// A client that carries the organization's credential reaches only the
// organization's endpoint, and a member's own client never gets private
// addresses from a policy read (including a failed one).
func TestOrgClientIsBoundToTheOrgEndpoint(t *testing.T) {
	f := newOrgFixture(t)
	up := newCompatUpstream(t, enforceOrgKey)
	other := newCompatUpstream(t, enforceOrgKey) // another loopback host
	f.putOrgCompat(t, up, enforceOrgKey)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	ws := f.memberWorkspace(t)
	req := func(c *http.Client, base string) error {
		r, _ := http.NewRequest("GET", base+"/v1/models", nil)
		resp, err := c.Do(r)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := req(ws.orgModelClient(providerCompat), up.URL); err != nil {
		t.Fatalf("the organization endpoint: %v", err)
	}
	if err := req(ws.orgModelClient(providerCompat), other.URL); err == nil {
		t.Fatal("the organization client reached a host that is not the organization's endpoint")
	}
	// The member's own client: loopback is refused for a non-admin, whatever the policy says.
	if err := req(ws.ownCompatClient(), up.URL); err == nil {
		t.Fatal("the own client reached loopback because the policy is provided")
	}
	// A control-database failure resolves to the restricted policy (provided); it must not open the own client either.
	ws.orgPolicy = func(context.Context) (ResolvedPolicy, error) { return nil, errors.New("database is locked") }
	if err := req(ws.ownCompatClient(), up.URL); err == nil {
		t.Fatal("a failed policy read opened private addresses for the own client")
	}
	// Link-local is refused even for the organization client.
	if err := orgEndpointControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("link-local reachable")
	}
}

// A refusal of an organization credential is recorded on the resource, and a
// concurrent replacement of the key is not overwritten.
func TestOrgRejectionRecordedWithOptimisticLock(t *testing.T) {
	f := newOrgFixture(t)
	f.admin_(t, "PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"old-synthetic-key-AAAA1111"}`, 200)
	f.g.recordOrgRejection("webtool:tavily", "auth_failed")
	row, _, _ := f.g.getOrgResource(context.Background(), "webtool:tavily")
	if row.Status != orgStatusRejected || row.ErrorCode != "auth_failed" {
		t.Fatalf("not recorded: %+v", row)
	}
	// A new key replaces the row; a rejection read from before it must not win.
	stale := row
	f.admin_(t, "PUT", "/api/admin/org/resources/webtool:tavily", `{"key":"new-synthetic-key-BBBB2222"}`, 200)
	if _, err := f.g.root.store.db.Exec(`UPDATE org_resources SET status=?,error_code=?,updated_at=? WHERE id=? AND updated_at=?`, orgStatusRejected, "auth_failed", now(), "webtool:tavily", stale.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	fresh, _, _ := f.g.getOrgResource(context.Background(), "webtool:tavily")
	if fresh.Status != orgStatusStored || fresh.KeyHint != keyHint("new-synthetic-key-BBBB2222") {
		t.Fatalf("a stale rejection overwrote the new key: %+v", fresh)
	}
	f.g.recordOrgRejection("provider:nope", "x") // an unknown resource is a no-op
}

// A policy that changes while an attach runs leaves no VM behind.
func TestAttachVMRechecksThePolicyAfterAttaching(t *testing.T) {
	f := newComputerPolicyFixture(t)
	ws := f.memberWorkspace(t)
	var calls atomic.Int32
	real := ws.orgPolicy
	// The first read (the check before attaching) allows; every later read denies.
	ws.orgPolicy = func(ctx context.Context) (ResolvedPolicy, error) {
		if calls.Add(1) == 1 {
			return real(ctx)
		}
		p := selfServeDefaults()
		p[capComputer] = PolicyValue{Mode: "none"}
		return p, nil
	}
	if err := ws.AttachVM(context.Background()); !errors.Is(err, errComputerPolicy) {
		t.Fatalf("attach: %v", err)
	}
	if ws.computer() != nil {
		t.Fatal("a VM stayed attached after the policy denied it")
	}
}

// Detach waits for a call in flight (drains) before the VM goes.
func TestComputerPolicyDetachWaitsForAnInFlightCall(t *testing.T) {
	f := newComputerPolicyFixture(t)
	ws := f.memberWorkspace(t)
	vm := f.vms[f.member.ID]
	vm.blockOn["shell.exec"] = true
	if err := ws.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ws.microVMAction(context.Background(), Run{ID: "run-1", BotID: "bot-1"}, "shell.exec", shellArgs())
		done <- err
	}()
	for vm.inFlight.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	f.setOverrides(t, `{"computer":{"mode":"none"}}`)
	// The policy has changed; the call in flight still holds the computer.
	for i := 0; i < 20 && ws.computer() != nil; i++ {
		select {
		case err := <-done:
			t.Fatalf("the call ended before it was released: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if ws.computer() == nil {
		t.Fatal("the VM was detached under a call in flight")
	}
	close(vm.release)
	f.g.policyApply.Wait()
	if err := <-done; err != nil {
		t.Fatalf("the call that finished in time failed: %v", err)
	}
	if ws.computer() != nil {
		t.Fatal("the VM is attached after the drain")
	}
}

// Nothing starts once the gateway is closed (Close joins the wait group under the same lock).
func TestApplyPolicyChangeAfterCloseStartsNothing(t *testing.T) {
	f := newComputerPolicyFixture(t)
	ws := f.memberWorkspace(t)
	if err := ws.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The policy now gives no VM, written without the API so nothing is applied yet.
	if _, err := f.g.root.store.db.Exec(`INSERT INTO org_account_policies(account_id,capability,mode) VALUES(?,?,?)`, f.member.ID, capComputer, "none"); err != nil {
		t.Fatal(err)
	}
	f.g.mu.Lock()
	f.g.closed = true
	f.g.mu.Unlock()
	f.g.applyPolicyChange([]string{f.member.ID})
	f.g.policyApply.Wait()
	f.g.mu.Lock()
	f.g.closed = false
	f.g.mu.Unlock()
	if ws.computer() == nil {
		t.Fatal("a detach started after the gateway closed")
	}
	f.g.applyPolicyChange([]string{f.member.ID}) // open again: it applies
	f.g.policyApply.Wait()
	if ws.computer() != nil {
		t.Fatal("the policy change did not apply")
	}
}

// A stored choice of an E2B computer, then a policy that gives the VM back, returns the VM.
func TestRestoreFallsBackToTheVMWhenPolicyAllowsIt(t *testing.T) {
	f := newComputerPolicyFixture(t)
	ws := f.memberWorkspace(t)
	f.setOverrides(t, `{"computer":{"mode":"provided","source":"vm"}}`)
	if ws.computer() != nil {
		t.Fatal("precondition: no VM attached")
	}
	ws.restoreE2BEnvironment(true)
	if ws.computer() == nil {
		t.Fatal("the VM did not come back under provided/vm")
	}
}

// The settings tool refuses to undo a change to a setting the organization now manages.
func TestSettingsRevertRespectsPolicy(t *testing.T) {
	e := newSettingsEnv(t)
	var mu sync.Mutex
	pol := selfServeDefaults()
	e.s.orgPolicy = func(context.Context) (ResolvedPolicy, error) {
		mu.Lock()
		defer mu.Unlock()
		out := ResolvedPolicy{}
		for k, v := range pol {
			out[k] = v
		}
		return out, nil
	}
	set := e.mustCall(`{"action":"set","key":"automation.web_search","value":false}`)
	id := set["change_id"].(string)
	mu.Lock()
	pol[capWebSearch] = PolicyValue{Mode: "off"}
	mu.Unlock()
	_, o := e.call(`{"action":"revert","change_id":"` + id + `"}`)
	if o == nil || o.Code != "settings_forbidden" || o.Detail == nil || o.Detail.Code != codeManagedByOrganization {
		t.Fatalf("revert under policy: %+v", o)
	}
	_, o = e.call(`{"action":"set","key":"automation.web_search","value":true}`)
	if o == nil || o.Detail == nil || o.Detail.Code != codeManagedByOrganization {
		t.Fatalf("set under policy: %+v", o)
	}
}

// Usage follows the credential source fixed when the call started.
func TestOrgUsageFollowsTheSourceFixedAtCallStart(t *testing.T) {
	f := newOrgFixture(t)
	ws := f.memberWorkspace(t)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	ws.noteOrgModelUseAs(false, "gpt-5", providerUsage(1000, 10)) // started under own, completed under provided
	f.setOverrides(t, `{"ai_models":{"mode":"own"}}`)
	ws.noteOrgModelUseAs(true, "gpt-5", providerUsage(1000, 10)) // started under provided, completed under own
	var n int
	f.g.root.store.db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM org_usage WHERE account_id=?`, f.member.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("usage requests %d, want the one call that started under provided", n)
	}
}

// An error that came inside a 200 stream is cleaned by the backstop.
func TestOrgBackstopScrubsInStreamErrors(t *testing.T) {
	f := newOrgFixture(t)
	up := newCompatUpstream(t, enforceOrgKey)
	f.putOrgCompat(t, up, enforceOrgKey)
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	ws := f.memberWorkspace(t)
	text := ws.redactRunError("compat:llama-test", errors.New(`stream event: {"error":{"message":"bad key `+enforceOrgKey+` at `+strings.TrimPrefix(up.URL, "http://")+`"}}`))
	if substr8(text, enforceOrgKey) != "" || strings.Contains(text, strings.TrimPrefix(up.URL, "http://")) {
		t.Fatalf("not scrubbed: %s", text)
	}
	// Own credentials are the person's own and pass unchanged.
	f.setOverrides(t, `{"ai_models":{"mode":"own"}}`)
	if got := ws.redactRunError("gpt-5", errors.New("plain "+up.URL)); !strings.Contains(got, up.URL) {
		t.Fatalf("own error altered: %s", got)
	}
}

func providerUsage(in, out int64) provider.Usage {
	return provider.Usage{InputTokens: in, OutputTokens: out}
}
