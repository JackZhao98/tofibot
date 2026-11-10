package app

// Own models alongside the organization's (docs/design/user-groups-own-alongside.md, P1):
// under ai_models=provided a member may also sign in with ChatGPT (Codex) unless
// an administrator sets params.allow_own=false.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

const (
	provAllowOwn  = `{"ai_models":{"mode":"provided","params":{"allow_own":true}}}`
	provForbidOwn = `{"ai_models":{"mode":"provided","params":{"allow_own":false}}}`
	codexTestID   = "codex-gpt-6-luna"
)

func signInCodex(t *testing.T, ws *Server, expires time.Time) {
	t.Helper()
	if err := ws.codex.SaveAccessOnlyCredential("synthetic-codex-access", "", expires.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

// seedOrgOpenAI gives the organization an OpenAI key.
func (f *orgFixture) seedOrgOpenAI(t *testing.T) {
	t.Helper()
	org, err := f.g.orgSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = org.put(secretRecord{ID: providerVaultID(providerOpenAI), Name: "k", Kind: modelProviderKind, Target: providerOpenAI, CreatedAt: now()}, enforceOrgKey); err != nil {
		t.Fatal(err)
	}
	if err = f.g.saveOrgResource(context.Background(), orgResourceRow{ID: providerVaultID(providerOpenAI), Kind: modelProviderKind, Config: "{}", Status: orgStatusVerified, UpdatedAt: now()}); err != nil {
		t.Fatal(err)
	}
}

func memberModels(t *testing.T, f *orgFixture) []string {
	t.Helper()
	code, out := f.memberCall(t, "GET", "/api/models", "")
	if code != 200 {
		t.Fatalf("GET /api/models: %d %s", code, out)
	}
	var body struct {
		Models []ModelOption `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range body.Models {
		ids = append(ids, m.ID)
	}
	return ids
}

func hasID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestAllowOwnParamResolves(t *testing.T) {
	row := func(allow string) policyRow {
		return policyRow{Capability: capAIModels, Mode: "provided", Params: allow}
	}
	own := func(rows ...policyRow) bool { return allowOwnOf(resolvePolicy(rows, nil)[capAIModels]) }
	if !own(row("{}")) || !own(row(`{"allow_own":true}`)) || own(row(`{"allow_own":false}`)) {
		t.Fatal("group value")
	}
	// An override beats the group, both ways.
	if got := allowOwnOf(resolvePolicy([]policyRow{row(`{"allow_own":false}`)}, []policyRow{row(`{"allow_own":true}`)})[capAIModels]); !got {
		t.Fatal("override true lost to group false")
	}
	if got := allowOwnOf(resolvePolicy([]policyRow{row("{}")}, []policyRow{row(`{"allow_own":false}`)})[capAIModels]); got {
		t.Fatal("override false lost to group default")
	}
	// A damaged or non-boolean value never widens.
	if own(row(`{"allow_own":`)) || own(row(`{"allow_own":"no"}`)) {
		t.Fatal("damaged params widened access")
	}
	// Params are accepted only on ai_models under provided.
	if validPolicyParams(capAIModels, "own", []byte(`{"allow_own":true}`)) || validPolicyParams(capWebSearch, "provided", []byte(`{"allow_own":true}`)) ||
		validPolicyParams(capAIModels, "provided", []byte(`{"other":1}`)) || validPolicyParams(capAIModels, "provided", []byte(`{"allow_own":1}`)) {
		t.Fatal("params accepted where they do not belong")
	}
	// An unreadable policy forbids own sources.
	if allowOwnOf(restrictedPolicy()[capAIModels]) {
		t.Fatal("restricted policy allows own models")
	}
	if got := managedSettingMode("models.providers", ResolvedPolicy{capAIModels: {Mode: "provided"}}); got != "" {
		t.Fatalf("models.providers managed while own sources are allowed: %q", got)
	}
	if got := managedSettingMode("models.providers", restrictedPolicy()); got != "provided" {
		t.Fatalf("models.providers not managed while own sources are forbidden: %q", got)
	}
}

func TestAdminPolicyParamsOverTheWire(t *testing.T) {
	f := newOrgFixture(t)
	f.admin_(t, "PUT", "/api/admin/accounts/"+f.member.ID+"/policy", `{"overrides":{"ai_models":{"mode":"provided","params":{"allow_own":"yes"}}}}`, 400)
	f.admin_(t, "PUT", "/api/admin/accounts/"+f.member.ID+"/policy", `{"overrides":{"ai_models":{"mode":"own","params":{"allow_own":false}}}}`, 400)
	f.admin_(t, "PUT", "/api/admin/accounts/"+f.member.ID+"/policy", `{"overrides":{"web_search":{"mode":"provided","params":{"allow_own":false}}}}`, 400)
	f.setOverrides(t, provForbidOwn)
	out := f.admin_(t, "GET", "/api/admin/accounts/"+f.member.ID+"/policy", "", 200)
	var got struct {
		Overrides map[string]policyOut
		Effective map[string]PolicyValue
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.Overrides["ai_models"].Params), `"allow_own":false`) || allowOwnOf(got.Effective["ai_models"]) {
		t.Fatalf("stored shape: %s", out)
	}
	ws := f.memberWorkspace(t)
	if v := ws.capabilitiesView()[capAIModels]; v.OwnAllowed == nil || *v.OwnAllowed {
		t.Fatalf("own_allowed for the member: %+v", v)
	}
	f.setOverrides(t, provAllowOwn)
	if v := ws.capabilitiesView()[capAIModels]; v.OwnAllowed == nil || !*v.OwnAllowed {
		t.Fatalf("own_allowed for the member: %+v", v)
	}
}

func TestPerAccountOverrideBeatsGroup(t *testing.T) {
	f := newOrgFixture(t)
	out := f.admin_(t, "POST", "/api/admin/groups", `{"name":"Staff","policies":`+provForbidOwn+`}`, 201)
	var made groupView
	if err := json.Unmarshal([]byte(out), &made); err != nil {
		t.Fatal(err)
	}
	f.admin_(t, "PUT", "/api/admin/groups/"+made.ID+"/members", `{"account_ids":["`+f.member.ID+`"]}`, 200)
	ws := f.memberWorkspace(t)
	if ws.ownModelsAllowed() {
		t.Fatal("group forbids own models")
	}
	f.setOverrides(t, provAllowOwn)
	if !ws.ownModelsAllowed() {
		t.Fatal("the account override did not beat the group")
	}
	f.setOverrides(t, `{}`)
	if ws.ownModelsAllowed() {
		t.Fatal("removing the override must restore the group value")
	}
}

func TestCodexServesOwnBotsUnderProvided(t *testing.T) {
	f := newOrgFixture(t)
	f.seedOrgOpenAI(t)
	f.setOverrides(t, provAllowOwn)
	ws := f.memberWorkspace(t)

	// Connecting is allowed: the request is not refused by policy (a cancelled
	// context stops it before any network call).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	ws.codexAuth(rec, httptest.NewRequest(http.MethodPost, "/api/auth/codex/connect", nil).WithContext(ctx), "/connect")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("connect refused under provided with allow_own: %s", rec.Body.String())
	}

	// Not signed in: no Codex models. Signed in: listed beside the organization's.
	if ids := memberModels(t, f); hasID(ids, codexTestID) {
		t.Fatalf("Codex models listed before sign-in: %v", ids)
	}
	signInCodex(t, ws, time.Now().Add(time.Hour))
	ws.modelCatalog = []ModelOption{codexSnapshotOption()}
	ws.modelCatalogAt, ws.modelCatalogSource = time.Now(), "live"
	if !ws.codexConnected() {
		t.Fatal("signed in but not connected")
	}
	if st := ws.providerStatus(providerCodex); st.Status != "connected" || st.Source != sourceOwn {
		t.Fatalf("codex status: %+v", st)
	}

	// A Codex-model call resolves the Codex credential, never an organization key.
	cred, err := ws.providerCredential(context.Background(), providerCodex)
	if err != nil || !strings.HasPrefix(cred, "synthetic-codex-access") || strings.Contains(cred, enforceOrgKey) {
		t.Fatalf("Codex credential: %q %v", cred, err)
	}
	if _, err = ws.newModelProvider(context.Background(), codexTestID); err != nil {
		t.Fatalf("Codex model provider: %v", err)
	}
	if c, err := ws.providerCredential(context.Background(), providerOpenAI); err != nil || c != enforceOrgKey {
		t.Fatalf("organization credential beside Codex: %v", err)
	}
	// An organization model still takes organization credentials.
	if ws.credentialSource(providerCodex) != sourceOwn || ws.credentialSource(providerOpenAI) != sourceOrganization {
		t.Fatal("credential sources")
	}
	// Codex calls are not the organization's usage.
	ws.noteOrgModelUse(codexTestID, provider.Usage{InputTokens: 1000, OutputTokens: 10})
	var n int
	f.g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_usage WHERE account_id=?`, f.member.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("a Codex call was booked to org_usage: %d rows", n)
	}
	ws.noteOrgModelUse("gpt-5", provider.Usage{InputTokens: 1000, OutputTokens: 10})
	f.g.root.store.db.QueryRow(`SELECT requests FROM org_usage WHERE account_id=? AND capability=?`, f.member.ID, capAIModels).Scan(&n)
	if n != 1 {
		t.Fatalf("the organization call was not booked: %d", n)
	}
}

func TestAdminForbidsOwn(t *testing.T) {
	f := newOrgFixture(t)
	f.seedOrgOpenAI(t)
	f.setOverrides(t, provAllowOwn)
	ws := f.memberWorkspace(t)
	signInCodex(t, ws, time.Now().Add(time.Hour))
	ws.modelCatalog = []ModelOption{codexSnapshotOption()}
	ws.modelCatalogAt, ws.modelCatalogSource = time.Now(), "live"
	if !hasID(memberModels(t, f), codexTestID) {
		t.Fatal("precondition: Codex model listed while allowed")
	}

	f.setOverrides(t, provForbidOwn)
	if code, out := f.memberCall(t, "POST", "/api/auth/codex/connect", ""); code != 403 || orgErrCode(t, out) != codeManagedByOrganization {
		t.Fatalf("connect: %d %s", code, out)
	}
	if ids := memberModels(t, f); hasID(ids, codexTestID) {
		t.Fatalf("Codex models still listed: %v", ids)
	}
	if ws.codexConnected() {
		t.Fatal("Codex counts as connected")
	}
	// The saved Codex Bot fails with its own named error; nothing falls back.
	err := chatOnce(context.Background(), ws, codexTestID)
	if err == nil || !strings.Contains(err.Error(), codeOwnModelsNotAllowed) {
		t.Fatalf("run error: %v", err)
	}
	run := Run{Status: "failed", Error: err.Error(), Model: codexTestID}
	if fl := run.failure(); fl == nil || fl.Code != codeOwnModelsNotAllowed || fl.Message == "" {
		t.Fatalf("run failure: %+v", fl)
	}
	// The sign-in itself is kept, and works again once the administrator allows it.
	f.setOverrides(t, provAllowOwn)
	if !ws.codexConnected() || !hasID(memberModels(t, f), codexTestID) {
		t.Fatal("the sign-in did not come back after allowing")
	}
}

func TestDefaultModelNeverCrossesSource(t *testing.T) {
	f := newOrgFixture(t)
	f.seedOrgOpenAI(t)
	f.setOverrides(t, provAllowOwn)
	ws := f.memberWorkspace(t)
	save := func(model string) {
		t.Helper()
		if err := ws.saveModelDefaults(modelSettings{Model: model, ReasoningEffort: "medium"}); err != nil {
			t.Fatal(err)
		}
	}
	// A saved Codex default with an expired sign-in stays Codex; it does not become an organization model.
	signInCodex(t, ws, time.Now().Add(-time.Hour))
	ws.providerMu.Lock()
	ws.providerCatalogs[ws.catalogKey(providerOpenAI)] = providerCatalog{models: []ModelOption{{ID: "gpt-5", Name: "GPT-5"}}, at: time.Now(), source: "live"}
	ws.providerMu.Unlock()
	save(codexTestID)
	if got, _ := ws.modelDefaults(); got != codexTestID {
		t.Fatalf("an expired Codex default moved to %q", got)
	}
	if got := ws.backgroundDefaultModel(backgroundReview); !strings.HasPrefix(got, "codex-") {
		t.Fatalf("the background model left Codex: %q", got)
	}
	// A saved organization default with no organization key stays; it does not become Codex.
	signInCodex(t, ws, time.Now().Add(time.Hour))
	org, _ := f.g.orgSecrets()
	if err := org.remove(providerVaultID(providerOpenAI)); err != nil {
		t.Fatal(err)
	}
	save("gpt-5")
	if got, _ := ws.modelDefaults(); got != "gpt-5" {
		t.Fatalf("an organization default moved to %q", got)
	}
	if got := ws.backgroundDefaultModel(backgroundReview); strings.HasPrefix(got, "codex-") {
		t.Fatalf("the background model crossed into Codex: %q", got)
	}
	// The run fails with the existing named error.
	if err := chatOnce(context.Background(), ws, "gpt-5"); err == nil || !strings.Contains(err.Error(), codeOrgResourceMissing) {
		t.Fatalf("run error: %v", err)
	}
}

func TestRedactionFollowsPolicyNotReference(t *testing.T) {
	f := newOrgFixture(t)
	f.seedOrgOpenAI(t)
	ws := f.memberWorkspace(t)
	text := "boom " + enforceOrgKey
	f.setOverrides(t, `{"ai_models":{"mode":"own"}}`)
	if got := ws.redactOrgText(codexTestID, text); got != text {
		t.Fatalf("redacted under own: %q", got)
	}
	f.setOverrides(t, provAllowOwn)
	for _, model := range []string{codexTestID, "gpt-5", "claude-sonnet-5-5"} {
		if got := ws.redactOrgText(model, text); strings.Contains(got, enforceOrgKey[:12]) {
			t.Fatalf("%s: organization key survives under provided: %q", model, got)
		}
	}
}
