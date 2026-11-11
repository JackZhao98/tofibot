package app

// Organization resources over the admin API (design section 6): model-provider
// keys, the OpenAI-compatible endpoint, E2B and web-tool keys, plus the
// organization's web-tool order. Keys are write-only. A key is checked against
// its provider with the same helpers a workspace uses, run on the root Server
// with the key passed in (the root's own vault is never read: on an upgraded
// install it holds the legacy owner's personal keys, risk S1). The helpers'
// catalog caches are not used, because they would write the root's own
// database. Upstream error text is never returned and never logged.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer/e2b"
	"github.com/JackZhao98/tofibot/internal/webtools"
)

func (g *AccountGateway) serveOrg(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	switch {
	case rest == "web-tools":
		switch r.Method {
		case http.MethodGet:
			g.writeOrgWebTools(w, r.Context())
		case http.MethodPut:
			g.putOrgWebTools(w, r)
		default:
			methodNotAllowed(w, "GET, PUT")
		}
	case rest == "resources":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		g.listOrgResourcesJSON(w, r)
	case len(parts) == 2 && parts[0] == "resources":
		sp, ok := orgSpecFor(parts[1])
		if !ok {
			writeErr(w, 404, "not_found", "resource not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			row, found, err := g.getOrgResource(r.Context(), sp.ID)
			if err != nil {
				storageFailure(w, "read resource", err)
				return
			}
			if !found {
				writeErr(w, 404, "not_found", "resource not found")
				return
			}
			writeJSON(w, 200, row.view())
		case http.MethodPut:
			if sp.ID == orgFileStoreID {
				g.putOrgFileStore(w, r, sp)
				return
			}
			if sp.ID == orgBackupStoreID {
				g.putOrgBackupStore(w, r, sp)
				return
			}
			g.putOrgResource(w, r, sp)
		case http.MethodDelete:
			g.deleteOrgResource(w, r, sp)
		default:
			methodNotAllowed(w, "GET, PUT, DELETE")
		}
	case len(parts) == 3 && parts[0] == "resources" && parts[1] == e2bKeyVaultID && parts[2] == "build":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		g.retryOrgE2BBuild(w, r)
	case len(parts) == 3 && parts[0] == "resources" && parts[2] == "test":
		sp, ok := orgSpecFor(parts[1])
		if !ok {
			writeErr(w, 404, "not_found", "resource not found")
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		g.testOrgResource(w, r, sp)
	default:
		writeErr(w, 404, "not_found", "not found")
	}
}

// ---- what is missing ----

// orgNeeds lists, per capability, the resource ids any one of which satisfies it.
func orgNeeds(capability string, v PolicyValue) []string {
	if v.Mode != policyModeProvided {
		return nil
	}
	switch capability {
	case capAIModels:
		return []string{providerVaultID(providerAnthropic), providerVaultID(providerOpenAI), providerVaultID(providerCompat)}
	case capComputer:
		if v.Source == computerSourceE2B {
			return []string{e2bKeyVaultID}
		}
	case capWebSearch:
		return []string{webVaultID("tavily"), webVaultID("brave"), webVaultID("searxng")}
	case capWebFetch:
		return []string{webVaultID("firecrawl"), webVaultID("brightdata")}
	}
	return nil
}

type orgMissing struct {
	Capability string   `json:"capability"`
	GroupIDs   []string `json:"group_ids"`
	// AccountIDs are accounts whose own override provides the capability with no resource to serve it.
	AccountIDs []string `json:"account_ids"`
}

// missingResources names the capabilities some group provides without the
// organization holding any resource for them (the runtime answer is
// org_resource_missing; there is no fallback to a member's own key).
func (g *AccountGateway) missingResources(ctx context.Context) ([]orgMissing, error) {
	have := map[string]bool{}
	rows, err := g.listOrgResources(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		have[row.ID] = true
	}
	if cfg, err := g.orgWebConfig(ctx); err == nil && cfg.SearXNGURL != "" {
		have[webVaultID("searxng")] = true
	}
	db := g.root.store.db
	headers, err := readGroupHeaders(ctx, db)
	if err != nil {
		return nil, err
	}
	byCap := map[string][]string{}
	for _, h := range headers {
		stored, err := readPolicyRows(ctx, db, `SELECT capability,mode,source,params FROM org_group_policies WHERE group_id=?`, h.id)
		if err != nil {
			return nil, err
		}
		for c, v := range resolvePolicy(stored, nil) {
			if c == capComputer && v.Mode == policyModeProvided && v.Source == computerSourceVM && !g.hostIsKVM() {
				byCap[c] = append(byCap[c], h.id) // a VM cannot be provided where none can be reserved
				continue
			}
			needs := orgNeeds(c, v)
			if len(needs) == 0 || slices.ContainsFunc(needs, func(id string) bool { return have[id] }) {
				continue
			}
			byCap[c] = append(byCap[c], h.id)
		}
	}
	// Per-account overrides set to provided count too.
	byAccount := map[string][]string{}
	overrides, err := db.QueryContext(ctx, `SELECT account_id,capability,mode,source,params FROM org_account_policies ORDER BY account_id`)
	if err != nil {
		return nil, err
	}
	defer overrides.Close()
	for overrides.Next() {
		var id string
		var row policyRow
		if err := overrides.Scan(&id, &row.Capability, &row.Mode, &row.Source, &row.Params); err != nil {
			return nil, err
		}
		if !validPolicy(row.Capability, row.Mode, row.Source) {
			continue
		}
		v := PolicyValue{Mode: row.Mode, Source: row.Source}
		needs := orgNeeds(row.Capability, v)
		vmWithoutHost := row.Capability == capComputer && v.Mode == policyModeProvided && v.Source == computerSourceVM && !g.hostIsKVM()
		if vmWithoutHost || (len(needs) > 0 && !slices.ContainsFunc(needs, func(id string) bool { return have[id] })) {
			byAccount[row.Capability] = append(byAccount[row.Capability], id)
		}
	}
	if err := overrides.Err(); err != nil {
		return nil, err
	}
	result := []orgMissing{}
	for _, c := range orgCapabilities {
		if len(byCap[c]) > 0 || len(byAccount[c]) > 0 {
			m := orgMissing{Capability: c, GroupIDs: byCap[c], AccountIDs: byAccount[c]}
			if m.GroupIDs == nil {
				m.GroupIDs = []string{}
			}
			if m.AccountIDs == nil {
				m.AccountIDs = []string{}
			}
			result = append(result, m)
		}
	}
	return result, nil
}

func (g *AccountGateway) listOrgResourcesJSON(w http.ResponseWriter, r *http.Request) {
	rows, err := g.listOrgResources(r.Context())
	if err != nil {
		storageFailure(w, "list resources", err)
		return
	}
	views := []orgResourceView{}
	for _, row := range rows {
		views = append(views, row.view())
	}
	missing, err := g.missingResources(r.Context())
	if err != nil {
		storageFailure(w, "compute missing resources", err)
		return
	}
	writeJSON(w, 200, map[string]any{"resources": views, "missing": missing})
}

// ---- verification, shared by PUT and test ----

// orgVerifyResult is the outcome of checking a key with its provider.
type orgVerifyResult struct {
	ok bool
	// status and code are the HTTP answer for a failure; reject marks a key the
	// provider refused (recorded on the resource), as opposed to a provider we
	// could not reach (nothing recorded).
	status int
	code   string
	msg    string
	reject bool
	// stored: the key was saved without a provider check (web keys; Test checks them).
	stored bool
}

func orgVerifyFailed(status int, code, msg string, reject bool) orgVerifyResult {
	return orgVerifyResult{status: status, code: code, msg: msg, reject: reject}
}

// verifyOrgModelKey checks an OpenAI or Anthropic key (sealed form for Claude:
// key, NUL, workspace id) with the workspace helpers. Logs carry the provider
// and the HTTP status only.
func (g *AccountGateway) verifyOrgModelKey(ctx context.Context, name, sealed string) orgVerifyResult {
	models, status, err := g.root.fetchProviderModels(ctx, name, sealed)
	if err != nil || len(models) == 0 {
		log.Printf("[org] %s key verification failed: status=%d models=%d", name, status, len(models))
		switch {
		case errors.Is(err, errProviderUnreachable) || status >= 500 || status == http.StatusTooManyRequests:
			return orgVerifyFailed(502, "provider_unreachable", providerLabel(name)+" could not be reached to verify the key.", false)
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return orgVerifyFailed(400, "invalid_key", providerLabel(name)+" rejected this API key.", true)
		case err == nil:
			return orgVerifyFailed(400, "no_models", "This "+providerLabel(name)+" API key has no usable models.", true)
		default:
			return orgVerifyFailed(400, "invalid_key", providerLabel(name)+" did not accept this API key.", true)
		}
	}
	if g.root.providerKeyOutOfQuota(ctx, name, sealed, models[0].ID) {
		return orgVerifyFailed(402, "no_quota", "This "+providerLabel(name)+" API key is valid but its account has no usage left.", true)
	}
	return orgVerifyResult{ok: true}
}

// verifyOrgCompat checks the compatible endpoint the way a workspace does. An
// endpoint that answers no model list but has typed model IDs is accepted as
// stored, not verified (the address could not be proven).
func (g *AccountGateway) verifyOrgCompat(ctx context.Context, cfg compatConfig) orgVerifyResult {
	root := g.root
	hostErr := root.compatHostCheck(ctx, cfg.BaseURL)
	if hostErr != nil && (errors.Is(hostErr, errCompatBlocked) || len(cfg.Models) == 0) {
		return compatVerifyFailure(0, hostErr)
	}
	models, status, err := root.listCompatModels(ctx, cfg)
	switch {
	case err == nil && len(models) > 0 && hostErr == nil:
		return orgVerifyResult{ok: true}
	case err != nil && (status == http.StatusUnauthorized || status == http.StatusForbidden || errors.Is(err, errCompatBlocked)):
		log.Printf("[org] compatible endpoint verification failed: status=%d", status)
		return compatVerifyFailure(status, err)
	case len(cfg.Models) > 0:
		return orgVerifyResult{ok: true, stored: true}
	}
	log.Printf("[org] compatible endpoint verification failed: status=%d", status)
	return compatVerifyFailure(status, err)
}

func compatVerifyFailure(status int, err error) orgVerifyResult {
	switch {
	case errors.Is(err, errCompatBlocked):
		return orgVerifyFailed(400, "endpoint_not_allowed", "This server may not connect to that address.", false)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return orgVerifyFailed(400, "invalid_key", "The endpoint rejected this API key.", true)
	case errors.Is(err, errProviderUnreachable) || status >= 500 || status == http.StatusTooManyRequests:
		return orgVerifyFailed(502, "provider_unreachable", "The endpoint could not be reached from this server.", false)
	}
	return orgVerifyFailed(400, "no_models", "The endpoint did not return a model list; add the model IDs by hand.", false)
}

func verifyOrgE2B(ctx context.Context, key string) orgVerifyResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := e2b.CheckKey(ctx, nil, e2bAPIBase(), key); err != nil {
		if e2bErrorCode(err) == e2b.CodeInvalidKey {
			return orgVerifyFailed(400, "invalid_key", "E2B rejected this key.", true)
		}
		return orgVerifyFailed(502, "provider_unreachable", "E2B did not answer. Try again in a minute.", false)
	}
	return orgVerifyResult{ok: true}
}

// orgWebProbe makes one real call with the key and returns the outcome code
// (empty = worked), never the vendor's text. endpoints is a test seam.
func (g *AccountGateway) orgWebProbe(ctx context.Context, id, key, zone string) webtools.Code {
	ctx, cancel := context.WithTimeout(ctx, webProbeTimeout)
	defer cancel()
	ep := g.orgWebEndpoints[id]
	var err error
	switch id {
	case "tavily":
		_, err = webtools.Tavily{Key: key, Endpoint: ep}.Search(ctx, "tofi connection test", 1)
	case "brave":
		_, err = webtools.Brave{Key: key, Endpoint: ep}.Search(ctx, "tofi connection test", 1)
	case "firecrawl":
		_, err = webtools.Firecrawl{Key: key, Endpoint: ep}.Fetch(ctx, "https://example.com/", 2000)
	case "brightdata":
		_, err = webtools.BrightData{Key: key, Zone: zone, Endpoint: ep}.Fetch(ctx, "https://example.com/", 2000)
	default:
		return webtools.NotConfigured
	}
	if err == nil || webtools.CodeOf(err) == webtools.EmptyContent {
		return ""
	}
	return webtools.CodeOf(err)
}

func verifyOrgWeb(code webtools.Code) orgVerifyResult {
	switch code {
	case "":
		return orgVerifyResult{ok: true}
	case webtools.AuthFailed:
		return orgVerifyFailed(400, "invalid_key", "The provider rejected this API key.", true)
	case webtools.QuotaExceeded:
		return orgVerifyFailed(402, "no_quota", "The provider reports no usage left for this key.", true)
	}
	return orgVerifyFailed(502, "provider_unreachable", "The provider could not be reached to verify the key.", false)
}

// ---- PUT ----

type orgResourceIn struct {
	Key         string   `json:"key"`
	BaseURL     string   `json:"base_url"`
	Models      []string `json:"models"`
	Zone        string   `json:"zone"`
	TemplateID  string   `json:"template_id"`
	WorkspaceID string   `json:"workspace_id"`
}

// minOrgKeyLen is the shortest organization key accepted: a shorter secret
// could not be masked, so none of it would be safe to show an admin.
const minOrgKeyLen = 16

func validSecretKey(key string, max int) bool {
	return len(key) >= minOrgKeyLen && len(key) <= max && !strings.ContainsAny(key, " \t\r\n\x00")
}

// orgKeyHint and orgKeyPrefix are what an admin may see of an organization key:
// the last four characters of a key of 16 or more, the first four of one of 32
// or more. A member sees neither.
func orgKeyHint(key string) string {
	if len(key) < 16 {
		return ""
	}
	return keyHint(key)
}

func orgKeyPrefix(key string) string {
	if len(key) < 32 {
		return ""
	}
	return key[:4]
}

func (g *AccountGateway) putOrgResource(w http.ResponseWriter, r *http.Request, sp orgResourceSpec) {
	var in orgResourceIn
	if decodeStrict(r, orgBodyLimit, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	key := strings.TrimSpace(in.Key)
	ctx := r.Context()
	var (
		sealed, label, hintKey string
		config                 = map[string]any{}
		res                    orgVerifyResult
	)
	switch {
	case sp.ID == providerVaultID(providerOpenAI) || sp.ID == providerVaultID(providerAnthropic):
		if !validSecretKey(key, 4096) {
			writeErr(w, 400, "invalid_key", "Enter the full API key.")
			return
		}
		if in.BaseURL != "" || len(in.Models) > 0 || in.Zone != "" || in.TemplateID != "" {
			writeErr(w, 400, "invalid_request", "this resource takes only a key")
			return
		}
		sealed = key
		workspace := strings.TrimSpace(in.WorkspaceID)
		if sp.Target == providerAnthropic {
			if !anthropicWorkspaceID.MatchString(workspace) {
				writeErr(w, 400, "invalid_workspace", "Enter the workspace ID shown in Claude Console (wrkspc_...).")
				return
			}
			sealed += "\x00" + workspace
		} else if workspace != "" {
			writeErr(w, 400, "invalid_workspace", "Only Claude keys take a workspace ID.")
			return
		}
		hintKey, label = key, orgKeyHint(key)
		res = g.verifyOrgModelKey(ctx, sp.Target, sealed)
	case sp.ID == providerVaultID(providerCompat):
		cfg, code, msg := g.orgCompatFromBody(ctx, in)
		if code != "" {
			writeErr(w, 400, code, msg)
			return
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			storageFailure(w, "encode endpoint", err)
			return
		}
		sealed, hintKey = string(raw), cfg.Key
		u, _ := url.Parse(cfg.BaseURL)
		label = u.Host
		config["base_url"], config["models"] = cfg.BaseURL, cfg.Models
		res = g.verifyOrgCompat(ctx, cfg)
	case sp.ID == e2bKeyVaultID:
		if !validSecretKey(key, e2bMaxKeyBytes) {
			writeErr(w, 400, "invalid_key", "That does not look like an E2B API key.")
			return
		}
		template := strings.TrimSpace(in.TemplateID)
		if template != "" && !e2bTemplateIDPattern.MatchString(template) {
			writeErr(w, 400, "invalid_template", "That does not look like an E2B template ID.")
			return
		}
		if in.BaseURL != "" || len(in.Models) > 0 || in.Zone != "" {
			writeErr(w, 400, "invalid_request", "this resource takes a key and an optional template_id")
			return
		}
		sealed, hintKey, label = key, key, orgKeyHint(key)
		if template != "" {
			config["template_id"] = template
		}
		res = verifyOrgE2B(ctx, key)
	default: // webtool:*
		zone := strings.TrimSpace(in.Zone)
		if !validSecretKey(key, 1024) {
			writeErr(w, 400, "invalid_key", "Enter the full API key.")
			return
		}
		if sp.Zone {
			if zone == "" || len(zone) > 120 || strings.ContainsAny(zone, " \t\r\n\x00") {
				writeErr(w, 400, "invalid_zone", "Enter the zone name.")
				return
			}
			config["zone"] = zone
		} else if zone != "" {
			writeErr(w, 400, "invalid_zone", "This provider takes no zone.")
			return
		}
		if in.BaseURL != "" || len(in.Models) > 0 || in.TemplateID != "" || in.WorkspaceID != "" {
			writeErr(w, 400, "invalid_request", "this resource takes a key and an optional zone")
			return
		}
		sealed, hintKey, label = key, key, orgKeyHint(key)
		if zone != "" {
			sealed += "\x00" + zone
		}
		// A web key is stored first and checked by Test, as in a workspace.
		res = orgVerifyResult{ok: true, stored: true}
	}
	if !res.ok {
		writeErr(w, res.status, res.code, res.msg)
		return
	}
	status, verifiedAt := orgStatusVerified, sql.NullString{String: now(), Valid: true}
	if res.stored {
		status, verifiedAt = orgStatusStored, sql.NullString{}
	}
	cfgJSON, err := json.Marshal(config)
	if err != nil {
		storageFailure(w, "encode config", err)
		return
	}
	row := orgResourceRow{ID: sp.ID, Kind: sp.Kind, Config: string(cfgJSON), KeyHint: "", KeyPrefix: "", Status: status, VerifiedAt: verifiedAt, UpdatedAt: now()}
	if hintKey != "" {
		row.KeyHint, row.KeyPrefix = orgKeyHint(hintKey), orgKeyPrefix(hintKey)
	}
	rec := secretRecord{ID: sp.ID, Name: sp.Name, Kind: sp.Kind, Target: sp.Target, Label: label, Status: "stored", CreatedAt: now()}
	if status == orgStatusVerified {
		rec.VerifiedAt = row.VerifiedAt.String
	}
	if err = g.storeOrgResource(ctx, rec, sealed, row); err != nil {
		storageFailure(w, "store resource", err)
		return
	}
	if sp.ID == e2bKeyVaultID {
		// Build the template now, so the first member does not wait for it.
		g.kickOrgE2BBuild()
		g.markOrgE2BPreparing(ctx)
		if fresh, ok, e := g.getOrgResource(ctx, sp.ID); e == nil && ok {
			row = fresh
		}
	}
	writeJSON(w, 200, row.view())
}

// storeOrgResource writes the sealed secret and then its row; if the row fails
// the secret goes back to what it was.
func (g *AccountGateway) storeOrgResource(ctx context.Context, rec secretRecord, sealed string, row orgResourceRow) error {
	v, err := g.orgSecrets()
	if err != nil {
		return err
	}
	g.orgWriteMu.Lock()
	defer g.orgWriteMu.Unlock()
	prev, existed, err := v.put(rec, sealed)
	if err != nil {
		return err
	}
	if rec.ID == e2bKeyVaultID {
		// A new key may belong to another E2B team: a build of the old one ends, and
		// the new config carries no build.
		g.orgE2BReset()
	}
	if err = g.saveOrgResource(ctx, row); err != nil {
		v.restore(rec.ID, prev, existed)
		return err
	}
	return nil
}

// orgCompatFromBody validates the endpoint request. A blank key keeps the
// organization's stored key only while the address is unchanged, so a stored key
// is never sent to a different host. The workspace helper is not used: it
// would read the root vault.
func (g *AccountGateway) orgCompatFromBody(ctx context.Context, in orgResourceIn) (compatConfig, string, string) {
	if in.Zone != "" || in.TemplateID != "" || in.WorkspaceID != "" {
		return compatConfig{}, "invalid_request", "this resource takes base_url, an optional key and models"
	}
	base, err := normalizeCompatBaseURL(in.BaseURL)
	if err != nil {
		return compatConfig{}, "invalid_base_url", err.Error()
	}
	models, err := normalizeCompatModelIDs(in.Models)
	if err != nil {
		return compatConfig{}, "invalid_models", err.Error()
	}
	key := strings.TrimSpace(in.Key)
	if len(key) > compatMaxKeyLen || strings.ContainsAny(key, " \t\r\n\x00") {
		return compatConfig{}, "invalid_key", "Enter the full API key, or leave it empty."
	}
	if key == "" {
		if stored, ok := g.orgCompatConfig(ctx); ok && stored.BaseURL == base {
			key = stored.Key
		}
	}
	return compatConfig{BaseURL: base, Key: key, Models: models}, "", ""
}

func (g *AccountGateway) orgCompatConfig(ctx context.Context) (compatConfig, bool) {
	secret, err := g.orgCredential(ctx, providerVaultID(providerCompat))
	if err != nil {
		return compatConfig{}, false
	}
	var cfg compatConfig
	if json.Unmarshal([]byte(secret.Value), &cfg) != nil || cfg.BaseURL == "" {
		return compatConfig{}, false
	}
	return cfg, true
}

// ---- DELETE and test ----

func (g *AccountGateway) deleteOrgResource(w http.ResponseWriter, r *http.Request, sp orgResourceSpec) {
	ctx := r.Context()
	if _, found, err := g.getOrgResource(ctx, sp.ID); err != nil {
		storageFailure(w, "read resource", err)
		return
	} else if !found {
		writeErr(w, 404, "not_found", "resource not found")
		return
	}
	if sp.ID == orgFileStoreID {
		// Rows that live in the bucket are read from it: its credentials stay while any exist.
		if n, cerr := g.objectRowsAnywhere(ctx); cerr != nil {
			storageFailure(w, "count stored files", cerr)
			return
		} else if n > 0 {
			writeErr(w, 409, "file_store_in_use", "Files are stored in this bucket. It cannot be removed until they are moved.")
			return
		}
	}
	v, err := g.orgSecrets()
	if err != nil {
		storageFailure(w, "open organization vault", err)
		return
	}
	g.orgWriteMu.Lock()
	err = v.remove(sp.ID)
	if err == nil && sp.ID == e2bKeyVaultID {
		g.orgE2BReset()
	}
	if err == nil {
		_, err = g.root.store.db.ExecContext(ctx, `DELETE FROM org_resources WHERE id=?`, sp.ID)
	}
	g.orgWriteMu.Unlock()
	if err != nil {
		storageFailure(w, "delete resource", err)
		return
	}
	missing, err := g.missingResources(ctx)
	if err != nil {
		storageFailure(w, "compute missing resources", err)
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true, "id": sp.ID, "missing": missing})
}

// testOrgResource re-checks the stored key. A refusal by the provider is
// recorded on the resource; an unreachable provider changes nothing.
func (g *AccountGateway) testOrgResource(w http.ResponseWriter, r *http.Request, sp orgResourceSpec) {
	ctx := r.Context()
	row, found, err := g.getOrgResource(ctx, sp.ID)
	if err != nil {
		storageFailure(w, "read resource", err)
		return
	}
	if !found {
		writeErr(w, 404, "not_found", "resource not found")
		return
	}
	secret, err := g.orgCredential(ctx, sp.ID)
	if err != nil {
		writeErr(w, 404, "not_found", "resource not found")
		return
	}
	var res orgVerifyResult
	switch {
	case sp.Target == providerOpenAI || sp.Target == providerAnthropic:
		res = g.verifyOrgModelKey(ctx, sp.Target, secret.Value)
	case sp.Target == providerCompat:
		var cfg compatConfig
		if json.Unmarshal([]byte(secret.Value), &cfg) != nil {
			writeErr(w, 404, "not_found", "resource not found")
			return
		}
		res = g.verifyOrgCompat(ctx, cfg)
	case sp.ID == e2bKeyVaultID:
		res = verifyOrgE2B(ctx, secret.Value)
	case sp.ID == orgFileStoreID || sp.ID == orgBackupStoreID:
		var cfg fileStoreConfig
		accessKey, key, _ := strings.Cut(secret.Value, "\x00")
		if json.Unmarshal([]byte(row.Config), &cfg) != nil {
			writeErr(w, 404, "not_found", "resource not found")
			return
		}
		res = g.verifyFileStore(ctx, cfg, accessKey, key)
	default:
		key, zone, _ := strings.Cut(secret.Value, "\x00")
		res = verifyOrgWeb(g.orgWebProbe(ctx, sp.Target, key, zone))
	}
	if res.ok || res.reject {
		status, code, verified := orgStatusVerified, "", sql.NullString{String: now(), Valid: true}
		switch {
		case res.reject:
			status, code, verified = orgStatusRejected, res.code, sql.NullString{}
		case res.stored:
			status, verified = orgStatusStored, sql.NullString{}
		}
		// Only the outcome is written, and only if no PUT replaced the resource
		// while the check ran (updated_at is the version read before it).
		g.orgWriteMu.Lock()
		result, uerr := g.root.store.db.ExecContext(ctx, `UPDATE org_resources SET status=?,error_code=?,verified_at=?,updated_at=? WHERE id=? AND updated_at=?`, status, code, verified, now(), sp.ID, row.UpdatedAt)
		g.orgWriteMu.Unlock()
		if uerr != nil {
			storageFailure(w, "record test", uerr)
			return
		}
		if n, _ := result.RowsAffected(); n == 1 {
			if fresh, ok, e := g.getOrgResource(ctx, sp.ID); e == nil && ok {
				row = fresh
			}
		}
	}
	if !res.ok {
		writeErr(w, res.status, res.code, res.msg)
		return
	}
	writeJSON(w, 200, row.view())
}

// ---- organization web tools ----

func (g *AccountGateway) orgWebConfig(ctx context.Context) (webToolsConfig, error) {
	var c webToolsConfig
	row, found, err := g.getOrgResource(ctx, orgWebToolsID)
	if err != nil {
		return c, err
	}
	if found {
		_ = json.Unmarshal([]byte(row.Config), &c)
	}
	c.SearchOrder = normalizeOrder(c.SearchOrder, defaultSearchOrder)
	c.FetchOrder = normalizeOrder(c.FetchOrder, defaultFetchOrder)
	if c.Disabled == nil {
		c.Disabled = []string{}
	}
	return c, nil
}

type orgWebProvider struct {
	ID        string `json:"id"`
	Chain     string `json:"chain"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Paid      bool   `json:"paid"`
	Enabled   bool   `json:"enabled"`
	Status    string `json:"status"` // ready, needs_key, needs_address, error
	KeyPrefix string `json:"key_prefix,omitempty"`
	KeyHint   string `json:"key_hint,omitempty"`
	Zone      string `json:"zone,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	TestedAt  string `json:"tested_at,omitempty"`
}

func (g *AccountGateway) writeOrgWebTools(w http.ResponseWriter, ctx context.Context) {
	cfg, err := g.orgWebConfig(ctx)
	if err != nil {
		storageFailure(w, "read web tools", err)
		return
	}
	rows, err := g.listOrgResources(ctx)
	if err != nil {
		storageFailure(w, "read web tools", err)
		return
	}
	byID := map[string]orgResourceRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	providers := []orgWebProvider{}
	for _, sp := range webProviderSpecs {
		p := orgWebProvider{ID: sp.ID, Chain: sp.Chain, Label: sp.Label, Kind: sp.Kind, Paid: sp.Paid, Enabled: cfg.enabled(sp.Chain, sp.ID)}
		if sp.Kind == "self_hosted" {
			p.BaseURL = cfg.SearXNGURL
			p.Status = "needs_address"
			if cfg.SearXNGURL != "" {
				p.Status = "ready"
			}
			providers = append(providers, p)
			continue
		}
		row, ok := byID[webVaultID(sp.ID)]
		switch {
		case !ok:
			p.Status = "needs_key"
		case row.Status == orgStatusRejected:
			p.Status, p.ErrorCode = "error", row.ErrorCode
		default:
			p.Status = "ready"
		}
		if ok {
			p.KeyPrefix, p.KeyHint = row.KeyPrefix, row.KeyHint
			if row.VerifiedAt.Valid {
				p.TestedAt = row.VerifiedAt.String
			}
			var c struct {
				Zone string `json:"zone"`
			}
			_ = json.Unmarshal([]byte(row.Config), &c)
			p.Zone = c.Zone
		}
		providers = append(providers, p)
	}
	writeJSON(w, 200, map[string]any{
		"search_order": cfg.SearchOrder, "fetch_order": cfg.FetchOrder, "disabled": cfg.Disabled,
		"searxng_url": cfg.SearXNGURL, "daily_search": cfg.DailySearch, "daily_fetch": cfg.DailyFetch,
		"providers": providers,
	})
}

func validOrder(in []string, def []string) bool {
	if len(in) > len(def) {
		return false
	}
	for _, id := range in {
		if !slices.Contains(def, id) {
			return false
		}
	}
	return true
}

func (g *AccountGateway) putOrgWebTools(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SearchOrder *[]string `json:"search_order"`
		FetchOrder  *[]string `json:"fetch_order"`
		Disabled    *[]string `json:"disabled"`
		SearXNGURL  *string   `json:"searxng_url"`
		DailySearch *int      `json:"daily_search"`
		DailyFetch  *int      `json:"daily_fetch"`
	}
	if decodeStrict(r, orgBodyLimit, &in) != nil || (in.SearchOrder == nil && in.FetchOrder == nil && in.Disabled == nil && in.SearXNGURL == nil && in.DailySearch == nil && in.DailyFetch == nil) {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	ctx := r.Context()
	cfg, err := g.orgWebConfig(ctx)
	if err != nil {
		storageFailure(w, "read web tools", err)
		return
	}
	capOK := func(p *int) bool { return p == nil || *p == -1 || (*p >= 0 && *p <= 100000) }
	if (in.SearchOrder != nil && !validOrder(*in.SearchOrder, defaultSearchOrder)) || (in.FetchOrder != nil && !validOrder(*in.FetchOrder, defaultFetchOrder)) || !capOK(in.DailySearch) || !capOK(in.DailyFetch) {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	if in.SearchOrder != nil {
		cfg.SearchOrder = normalizeOrder(*in.SearchOrder, defaultSearchOrder)
	}
	if in.FetchOrder != nil {
		cfg.FetchOrder = normalizeOrder(*in.FetchOrder, defaultFetchOrder)
	}
	if in.Disabled != nil {
		disabled := []string{}
		for _, entry := range *in.Disabled {
			chain, id, ok := strings.Cut(entry, "/")
			def := defaultSearchOrder
			if chain == webChainFetch {
				def = defaultFetchOrder
			}
			if !ok || (chain != webChainSearch && chain != webChainFetch) || !slices.Contains(def, id) {
				writeErr(w, 400, "invalid_request", "invalid request")
				return
			}
			if !slices.Contains(disabled, entry) {
				disabled = append(disabled, entry)
			}
		}
		cfg.Disabled = disabled
	}
	if in.SearXNGURL != nil {
		if strings.TrimSpace(*in.SearXNGURL) == "" {
			cfg.SearXNGURL = ""
		} else {
			u, err := webtools.CleanBaseURL(*in.SearXNGURL)
			if err != nil || len(*in.SearXNGURL) > 300 {
				writeErr(w, 400, "invalid_address", "Enter the address of your instance, starting with http:// or https://.")
				return
			}
			cfg.SearXNGURL = u.String()
		}
	}
	if in.DailySearch != nil {
		cfg.DailySearch = in.DailySearch
	}
	if in.DailyFetch != nil {
		cfg.DailyFetch = in.DailyFetch
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		storageFailure(w, "encode web tools", err)
		return
	}
	g.orgWriteMu.Lock()
	err = g.saveOrgResource(ctx, orgResourceRow{ID: orgWebToolsID, Kind: orgWebToolsKind, Config: string(raw), Status: orgStatusStored, UpdatedAt: now()})
	g.orgWriteMu.Unlock()
	if err != nil {
		storageFailure(w, "save web tools", err)
		return
	}
	g.writeOrgWebTools(w, ctx)
}
