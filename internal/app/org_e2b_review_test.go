package app

// Slice 3 review fixes: an existing sandbox is never locked out by the template,
// a closed gateway leaves a build resumable, template upgrades keep the sandbox
// until the member consents, and the organization's ids and key stay server-side.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/e2b"
)

func setTestVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func memberEnvView(t *testing.T, f *orgFixture) environmentsView {
	t.Helper()
	code, out := f.memberCall(t, "GET", "/api/environments", "")
	if code != 200 {
		t.Fatalf("environments: %d %s", code, out)
	}
	var v environmentsView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func e2bItem(t *testing.T, v environmentsView) environmentItem {
	t.Helper()
	for _, it := range v.Environments {
		if it.Kind == envKindE2B {
			return it
		}
	}
	t.Fatalf("no E2B item in %+v", v.Environments)
	return environmentItem{}
}

// A sandbox that exists keeps working while the next TOFI version's template is
// built, after that build fails, and until the member upgrades it.
func TestOrgE2BExistingSandboxSurvivesNewTemplateAndUpgradeNeedsConsent(t *testing.T) {
	setTestVersion(t, "v1.0.0")
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	waitOrgTemplate(t, ws.orgE2BProvider())
	if err := e2bAction(ws); err != nil {
		t.Fatal(err)
	}
	p := ws.orgE2BProvider()
	oldSandbox := p.Status().SandboxID
	if raw, _ := ws.autoStored(orgE2BStateKey); !strings.Contains(raw, `"template_version":"v1.0.0"`) {
		t.Fatalf("the sandbox does not record its template version: %s", raw)
	}

	// A new version whose build fails.
	fake.mu.Lock()
	fake.buildFails = true
	fake.mu.Unlock()
	setTestVersion(t, "v1.1.0")
	_ = p.RefreshTemplate(context.Background())
	orgWait(t, "the failed build", func() bool { return orgE2BPhase(t, f).Phase == e2b.PhaseFailed })
	_ = p.RefreshTemplate(context.Background())
	if err := e2bAction(ws); err != nil {
		t.Fatalf("an action on the existing sandbox after a failed build: %v", err)
	}
	if info, err := p.Info(context.Background()); err != nil || info.State != "ready" || info.GuestOutdated {
		t.Fatalf("info %+v err %v", info, err)
	}
	st := p.Status()
	if st.Phase != e2b.PhaseReady || st.TemplatePhase != e2b.PhaseFailed || st.TemplateErrorCode != e2b.CodeTemplateBuildFailed {
		t.Fatalf("status %+v", st)
	}
	if err := p.Retry(context.Background()); err != nil {
		t.Fatalf("retry on the existing sandbox: %v", err)
	}
	item := e2bItem(t, memberEnvView(t, f))
	if item.State == "failed" || hasAction(item, "upgrade") {
		t.Fatalf("view while the new image failed: %+v", item)
	}
	if v := memberEnvView(t, f); v.E2B.Phase != e2b.PhaseReady || v.E2B.TemplatePhase != e2b.PhaseFailed {
		t.Fatalf("e2b view %+v", v.E2B)
	}

	// The admin retries and the new template becomes ready: the sandbox is offered an upgrade, nothing more.
	fake.mu.Lock()
	fake.buildFails = false
	fake.mu.Unlock()
	f.admin_(t, "POST", "/api/admin/org/resources/computer:e2b/build", "", 202)
	orgWait(t, "the new template", func() bool { return orgE2BPhase(t, f).Phase == e2b.PhaseReady })
	waitOrgTemplate(t, p)
	if err := e2bAction(ws); err != nil {
		t.Fatalf("an outdated sandbox must keep working: %v", err)
	}
	if fake.sandbox(oldSandbox) == nil {
		t.Fatal("the old sandbox was removed without the member's consent")
	}
	item = e2bItem(t, memberEnvView(t, f))
	if !item.GuestOutdated || !hasAction(item, "upgrade") || item.State == "failed" {
		t.Fatalf("view with a newer image: %+v", item)
	}
	if info, _ := ws.computer().Info(context.Background()); !info.GuestOutdated {
		t.Fatalf("computer info does not say outdated: %+v", info)
	}

	// The member consents.
	code, out := f.memberCall(t, "POST", "/api/computers/firecracker/upgrade", "")
	if code != 202 {
		t.Fatalf("upgrade: %d %s", code, out)
	}
	if fake.sandbox(oldSandbox) != nil {
		t.Fatal("the old sandbox is still there after the upgrade")
	}
	newSandbox := p.Status().SandboxID
	if newSandbox == "" || newSandbox == oldSandbox || p.Status().GuestOutdated {
		t.Fatalf("after the upgrade: %+v", p.Status())
	}
	if err := e2bAction(ws); err != nil {
		t.Fatal(err)
	}
	code, out = f.memberCall(t, "POST", "/api/computers/firecracker/upgrade", "")
	if code != 409 || orgErrCode(t, out) != e2b.CodeNotOutdated {
		t.Fatalf("a second upgrade: %d %s", code, out)
	}
	if fake.sandbox(newSandbox) == nil {
		t.Fatal("a current sandbox was removed")
	}
}

// Without an organization sandbox the upgrade route has nothing to do.
func TestUpgradeRouteRefusesWhenThereIsNoE2BComputer(t *testing.T) {
	f := newOrgFixture(t)
	if code, _ := f.memberCall(t, "POST", "/api/computers/firecracker/upgrade", ""); code != 404 {
		t.Fatalf("upgrade with no E2B computer: %d", code)
	}
}

// The server's minimum guest version fails closed until the member upgrades.
func TestOrgE2BMinimumGuestVersionRefusesUntilUpgrade(t *testing.T) {
	setTestVersion(t, "v1.0.0")
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	p := ws.orgE2BProvider()
	waitOrgTemplate(t, p)
	if err := e2bAction(ws); err != nil {
		t.Fatal(err)
	}
	setTestVersion(t, "v1.2.0")
	if v := orgE2BPhase(t, f); v.MinGuestVersion != "" {
		t.Fatalf("a minimum is shown while none is set: %+v", v)
	}
	t.Setenv("TOFI_E2B_MIN_GUEST_VERSION", "v1.1.0")
	ws.e2bEnv.mu.Lock()
	ws.e2bEnv.orgProvider = nil // the minimum is read when the provider is made
	ws.e2bEnv.mu.Unlock()
	f.admin_(t, "POST", "/api/admin/org/resources/computer:e2b/build", "", 202)
	orgWait(t, "the new template", func() bool { return orgE2BPhase(t, f).Phase == e2b.PhaseReady })
	if v := orgE2BPhase(t, f); v.MinGuestVersion != "v1.1.0" {
		t.Fatalf("the admin cannot see the minimum: %+v", v)
	}
	// A fresh provider reads the stored sandbox.
	p = ws.orgE2BProvider()
	waitOrgTemplate(t, p)
	if err := ws.attachProvider(p); err != nil && err != errComputerAttached {
		t.Logf("attach: %v", err)
	}
	if err := p.Retry(context.Background()); e2bErrCode(err) != e2b.CodeGuestOutdatedRequired {
		t.Fatalf("a sandbox below the minimum: %v", err)
	}
	if _, err := p.Action(context.Background(), computer.Action{BotID: e2bTestBotID, Name: "shell.exec"}); e2bErrCode(err) != e2b.CodeGuestOutdatedRequired {
		t.Fatalf("an action below the minimum: %v", err)
	}
	fake.mu.Lock()
	actions := fake.actions
	fake.mu.Unlock()
	if actions != 1 {
		t.Fatalf("a call reached a sandbox below the minimum (%d)", actions)
	}
	if err := p.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Action(context.Background(), computer.Action{BotID: e2bTestBotID, Name: "shell.exec"}); err != nil {
		t.Fatalf("after the upgrade: %v", err)
	}
}

// A PUT of a new key while a build runs ends that build: its late writes are refused.
func TestOrgE2BBuildOfAReplacedKeyCannotWrite(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	fake.gate = make(chan struct{})
	fake.gateTpl = "tpl_org_1"
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	_ = ws.orgE2BProvider().RefreshTemplate(context.Background())
	orgWait(t, "the first build", func() bool { n, _, _ := fake.counts(); return n >= 1 })
	f.g.orgE2B.mu.Lock()
	oldGen := f.g.orgE2B.gen
	f.g.orgE2B.mu.Unlock()
	// The admin sets a key again; the second build (tpl_org_2) is not gated.
	f.admin_(t, "PUT", "/api/admin/org/resources/computer:e2b", `{"key":"`+orgE2BKeyText+`"}`, 200)
	orgWait(t, "the second build", func() bool { n, _, _ := fake.counts(); return n >= 2 })
	orgWait(t, "the second build to be ready", func() bool { return orgE2BPhase(t, f).Phase == e2b.PhaseReady })
	// A write of the first build that was already in flight is refused outright.
	if err := f.g.writeOrgE2BBuild(context.Background(), oldGen, &orgE2BBuild{TemplateID: "tpl_org_1", Version: e2bTemplateVersion(), Phase: e2b.PhaseReady}); err != errOrgBuildSuperseded {
		t.Fatalf("a write of the replaced build: %v", err)
	}
	close(fake.gate) // the first build now ends
	time.Sleep(300 * time.Millisecond)
	row, _, _ := f.g.getOrgResource(context.Background(), e2bKeyVaultID)
	cfg, _ := parseOrgE2BConfig(row.Config)
	if cfg.Build == nil || cfg.Build.TemplateID != "tpl_org_2" || cfg.Build.Phase != e2b.PhaseReady {
		t.Fatalf("the replaced build wrote its state: %+v", cfg.Build)
	}
}

// Close (or a restart) leaves a running build resumable, never failed.
func TestOrgE2BCloseLeavesTheBuildResumable(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	fake.gate = make(chan struct{})
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	p := ws.orgE2BProvider()
	_ = p.RefreshTemplate(context.Background())
	orgWait(t, "the build to run", func() bool { _, b, _ := fake.counts(); return b >= 1 })
	f.g.orgE2B.mu.Lock()
	runGen := f.g.orgE2B.gen
	f.g.orgE2B.mu.Unlock()
	f.g.stopOrgE2BBuilds() // what Close does first
	// A write the build still had in flight after Close is refused.
	if err := f.g.writeOrgE2BBuild(context.Background(), runGen, &orgE2BBuild{Version: e2bTemplateVersion(), Phase: e2b.PhaseFailed, ErrorCode: e2b.CodeTemplateBuildFailed}); err != errOrgBuildSuperseded {
		t.Fatalf("a write of the build after Close: %v", err)
	}
	row, _, _ := f.g.getOrgResource(context.Background(), e2bKeyVaultID)
	cfg, _ := parseOrgE2BConfig(row.Config)
	if cfg.Build == nil || cfg.Build.Phase != e2b.PhasePreparing {
		t.Fatalf("a close left the build as %+v", cfg.Build)
	}
	// The next use resumes it.
	close(fake.gate)
	_ = p.RefreshTemplate(context.Background())
	waitOrgTemplate(t, p)
	if err := e2bAction(ws); err != nil {
		t.Fatalf("after the resume: %v", err)
	}
}

// Another account's guest token opens nothing on a sandbox that is not its own.
func TestOrgE2BGuestTokenOfAnotherAccountIsRefused(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	other, _ := f.addMember(t, "org-member-2")
	f.setOverrides(t, providedE2BPolicy)
	f.setOverridesFor(t, other.ID, providedE2BPolicy)
	wsA := f.memberWorkspace(t)
	wsB, err := f.g.workspace(other)
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*Server{wsA, wsB} {
		waitOrgTemplate(t, ws.orgE2BProvider())
		if err := e2bAction(ws); err != nil {
			t.Fatal(err)
		}
	}
	tokenA, _, ok := wsA.vaultValue(orgE2BGuestVaultID)
	if !ok || tokenA == "" {
		t.Fatal("no token for account A")
	}
	sbB := fake.sandbox(wsB.orgE2BProvider().Status().SandboxID)
	req, _ := http.NewRequest("POST", fake.srv.URL+"/p8331/"+sbB.id+"/v1/action", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	req.Header.Set("e2b-traffic-access-token", sbB.traf)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("account A's guest token on account B's sandbox: %d (want 401)", resp.StatusCode)
	}
	tokenB, _, _ := wsB.vaultValue(orgE2BGuestVaultID)
	if tokenA == tokenB {
		t.Fatal("two accounts share one guest token")
	}
}

// Only a team-level refusal, made with the key in force, marks the key rejected.
func TestOrgE2BKeyRejectionNeedsTeamLevelCallAndCurrentKey(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	p := ws.orgE2BProvider()
	waitOrgTemplate(t, p)
	if err := e2bAction(ws); err != nil {
		t.Fatal(err)
	}
	rejected := func() bool {
		row, _, _ := f.g.getOrgResource(context.Background(), e2bKeyVaultID)
		return row.Status == orgStatusRejected
	}
	// A sandbox answering 401 for its own call says nothing about the key.
	fake.mu.Lock()
	fake.sandboxStatus = 401
	fake.mu.Unlock()
	_, _ = p.Info(context.Background())
	_ = e2bAction(ws)
	if rejected() {
		t.Fatal("a per-sandbox 401 marked the organization key rejected")
	}
	fake.mu.Lock()
	fake.sandboxStatus = 0
	fake.mu.Unlock()
	// A refusal that answered an older key says nothing about the key set since.
	ws.orgE2BKeyRejected("an-older-key-0000000")
	f.g.rejectOrgE2BKey(-1, "an-older-key-0000000")
	if rejected() {
		t.Fatal("a refusal of an older key marked the current key rejected")
	}
	// A build of a past generation does not either.
	f.g.orgWriteMu.Lock()
	f.g.orgE2BReset()
	f.g.orgWriteMu.Unlock()
	f.g.orgE2B.mu.Lock()
	past := f.g.orgE2B.gen - 1
	f.g.orgE2B.mu.Unlock()
	f.g.rejectOrgE2BKey(past, orgE2BKeyText)
	if rejected() {
		t.Fatal("a build of a past generation marked the key rejected")
	}
	// The current key, on a sandbox create, does.
	fake.mu.Lock()
	fake.createStatus = 403
	fake.mu.Unlock()
	_ = p.Remove(context.Background())
	if err := e2bAction(ws); err == nil {
		t.Fatal("the refused create worked")
	}
	if !rejected() {
		t.Fatal("a refusal of the current key on a sandbox create was not recorded")
	}
}

// Members get codes and text we make, never the organization's template or build ids.
func TestMemberViewsCarryNoOrgTemplateOrBuildIds(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	p := ws.orgE2BProvider()
	waitOrgTemplate(t, p)
	st := p.Status()
	if st.TemplateID == "" || st.BuildID == "" {
		t.Fatalf("no ids to look for: %+v", st)
	}
	fake.mu.Lock()
	fake.createStatus = 400
	fake.createMessage = "template " + st.TemplateID + " build " + st.BuildID + " cannot start"
	fake.mu.Unlock()
	leaks := func(what, text string) {
		t.Helper()
		if strings.Contains(text, st.TemplateID) || strings.Contains(text, st.BuildID) {
			t.Fatalf("%s names the organization's template or build: %s", what, text)
		}
	}
	err := e2bAction(ws)
	if err == nil {
		t.Fatal("the refused create worked")
	}
	leaks("the action error", err.Error())
	info, _ := p.Info(context.Background())
	leaks("the computer info", info.Error)
	_, out := f.memberCall(t, "GET", "/api/environments", "")
	leaks("the environments view", out)
	_, out = f.memberCall(t, "POST", "/api/computers/firecracker/retry", "")
	leaks("the retry answer", out)
	_, out = f.memberCall(t, "GET", "/api/computers/firecracker/info", "")
	leaks("the computer route", out)
}

// Deleting an account removes the organization sandbox it left in the team.
func TestAccountDeleteReclaimsOrgE2BSandbox(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	keeper, _ := f.addMember(t, "org-keeper")
	f.setOverrides(t, providedE2BPolicy)
	f.setOverridesFor(t, keeper.ID, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	wsK, err := f.g.workspace(keeper)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []*Server{ws, wsK} {
		waitOrgTemplate(t, w.orgE2BProvider())
		if err := e2bAction(w); err != nil {
			t.Fatal(err)
		}
	}
	victimSbx, keepSbx := ws.orgE2BProvider().Status().SandboxID, wsK.orgE2BProvider().Status().SandboxID
	f.admin_(t, "PATCH", "/api/admin/accounts/"+f.member.ID, `{"disabled":true}`, 200)
	f.admin_(t, "DELETE", "/api/admin/accounts/"+f.member.ID, deleteBody("org-member"), 200)
	if fake.sandbox(victimSbx) != nil {
		t.Fatal("the deleted account's sandbox is still in the organization's E2B team")
	}
	if fake.sandbox(keepSbx) == nil {
		t.Fatal("another account's sandbox was removed")
	}
}
