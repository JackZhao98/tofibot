package app

// Enforcement of organization policy inside a workspace (docs/design/user-groups.md
// 4.2 to 4.8, slice 2). Every decision reads the policy live through
// Config.OrgPolicy (no cache), so a change applies at the next check. A workspace
// outside a gateway has no policy and behaves as "own" everywhere. An unreadable
// policy fails closed: the most restrictive value of each capability.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	policyReadTimeout = 5 * time.Second

	sourceOwn          = "own"
	sourceOrganization = "organization"

	codeManagedByOrganization = "managed_by_organization"
	codeCapabilityOff         = "capability_off"
	codeOrgResourceMissing    = "org_resource_missing"
	codeProviderAuthFailed    = "provider_auth_failed"
	codeProviderQuota         = "provider_quota_exhausted"
	codeProviderError         = "provider_error"

	// orgFailureMarker leads the body that replaces an upstream error caused by an
	// organization credential. Run failures read the code after it.
	orgFailureMarker = "organization_credential:"
)

// restrictedPolicy is what an unreadable policy resolves to.
func restrictedPolicy() ResolvedPolicy {
	return ResolvedPolicy{
		capAIModels:  {Mode: policyModeProvided},
		capComputer:  {Mode: "none"},
		capWebSearch: {Mode: "off"},
		capWebFetch:  {Mode: "builtin"},
	}
}

// policy is the workspace's resolved policy, read now.
func (s *Server) policy() ResolvedPolicy {
	if s == nil || s.orgPolicy == nil {
		return selfServeDefaults()
	}
	ctx, cancel := context.WithTimeout(context.Background(), policyReadTimeout)
	defer cancel()
	p, err := s.orgPolicy(ctx)
	if err != nil {
		log.Printf("[org] policy read for account %s failed: %v", s.accountID, err)
		return restrictedPolicy()
	}
	return p
}

func (s *Server) aiProvided() bool {
	return s != nil && s.orgPolicy != nil && s.policy()[capAIModels].Mode == policyModeProvided
}

// credentialSource is the one decision point for model credentials: the
// organization's vault when policy says provided, else the workspace's own.
// A Codex sign-in is never an organization resource.
func (s *Server) credentialSource(name string) string {
	if name != providerCodex && s.aiProvided() {
		return sourceOrganization
	}
	return sourceOwn
}

func webModeOf(p ResolvedPolicy, chain string) string {
	if chain == webChainFetch {
		return p[capWebFetch].Mode
	}
	return p[capWebSearch].Mode
}

// webMode is the policy mode of a chain: own, provided, and off (search) or builtin (fetch).
func (s *Server) webMode(chain string) string { return webModeOf(s.policy(), chain) }

// webSearchAllowed is the stored master switch and a policy that is not off.
func (s *Server) webSearchAllowed() bool {
	return s.webSearchEnabled() && s.webMode(webChainSearch) != "off"
}

// computerAllowsVM says whether the policy lets this workspace hold a VM.
// provided/e2b waits for slice 3 and so holds no computer yet.
func computerAllowsVM(v PolicyValue) bool {
	return v.Mode == "host" || (v.Mode == policyModeProvided && v.Source == computerSourceVM)
}

// computerAllowsOwnE2B says whether the person may bring an E2B key.
func computerAllowsOwnE2B(v PolicyValue) bool { return v.Mode == "host" || v.Mode == policyModeOwn }

// refusePolicy writes the refusal for a managed capability: 403 managed_by_organization
// while the organization provides it, 403 capability_off while it is off.
func refusePolicy(w http.ResponseWriter, off bool) {
	if off {
		writeErr(w, http.StatusForbidden, codeCapabilityOff, "This is turned off for your account by your organization.")
		return
	}
	writeErr(w, http.StatusForbidden, codeManagedByOrganization, "This is managed by your organization.")
}

// ---- organization credentials ----

// errComputerPolicy refuses a VM attach the account's policy does not allow.
var errComputerPolicy = errors.New(codeCapabilityOff + ": the policy gives this account no VM")

// errOrgCodex is returned for a Codex model while models are provided.
var errOrgCodex = errors.New(codeManagedByOrganization + ": models are provided by the organization, not a personal sign-in")

// orgCredentialError names a failed organization read without any inner text.
func orgCredentialError(name string, err error) error {
	if errors.Is(err, errOrgResourceMissing) {
		return fmt.Errorf("%s: %s", codeOrgResourceMissing, name)
	}
	log.Printf("[org] credential for %s unavailable: %v", name, err)
	return errors.New(codeOrgResourceMissing + ": " + name + " (unavailable)")
}

// orgProviderKey reads the organization's key (the sealed form of the provider:
// "key\x00workspace" for Claude, the settings JSON for a compatible endpoint).
func (s *Server) orgProviderKey(ctx context.Context, name string) (providerKeyState, error) {
	if s.orgCredential == nil {
		return providerKeyState{}, errOrgResourceMissing
	}
	sec, err := s.orgCredential(ctx, providerVaultID(name))
	if err != nil {
		return providerKeyState{}, err
	}
	return providerKeyState{Key: sec.Value}, nil
}

func (s *Server) orgProviderKeyNow(name string) (providerKeyState, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), policyReadTimeout)
	defer cancel()
	st, err := s.orgProviderKey(ctx, name)
	return st, err == nil
}

// ---- model calls: error redaction and usage ----

// orgSanitizeTransport sits between a member's workspace and the model provider
// while the credential is the organization's. A refusal of the credential (401,
// 402, 403, a quota 429) is replaced by a named code, so no upstream text, which
// may echo the key, reaches an error, a run record, a capture or a log. Other
// error bodies pass with every 8-character stretch of the credential removed.
type orgSanitizeTransport struct{ base http.RoundTripper }

func (t orgSanitizeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode < 400 {
		return resp, err
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	body := string(raw)
	var secrets []string
	for _, h := range []string{"Authorization", "X-Api-Key", "Api-Key"} {
		if v := strings.TrimSpace(strings.TrimPrefix(req.Header.Get(h), "Bearer ")); v != "" {
			secrets = append(secrets, v)
		}
	}
	code := ""
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		code = codeProviderAuthFailed
	case resp.StatusCode == http.StatusPaymentRequired:
		code = codeProviderQuota
	case resp.StatusCode == http.StatusTooManyRequests && provider.IsQuotaExhausted(body):
		code = codeProviderQuota
	}
	if code != "" {
		body = orgFailureMarker + code
		if code == codeProviderQuota {
			body += " insufficient_quota" // keeps the no-retry rule for an exhausted account
		}
		log.Printf("[org] model provider answered HTTP %d for an organization credential (%s)", resp.StatusCode, code)
	} else {
		body = scrubSecrets(body, secrets)
	}
	out := &http.Response{
		Status: resp.Status, StatusCode: resp.StatusCode, Proto: resp.Proto, ProtoMajor: resp.ProtoMajor, ProtoMinor: resp.ProtoMinor,
		Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)), Request: req,
	}
	for _, h := range []string{"Retry-After", "Retry-After-Ms"} {
		if v := resp.Header.Get(h); v != "" {
			out.Header.Set(h, v)
		}
	}
	return out, nil
}

// scrubSecrets masks every stretch of 8 or more characters of any secret found in text.
func scrubSecrets(text string, secrets []string) string {
	for _, sec := range secrets {
		if len(sec) < 8 {
			if sec != "" {
				text = strings.ReplaceAll(text, sec, "[redacted]")
			}
			continue
		}
		for i := 0; i+8 <= len(sec); i++ {
			if w := sec[i : i+8]; strings.Contains(text, w) {
				text = strings.ReplaceAll(text, w, "[redacted]")
			}
		}
	}
	return text
}

// orgFailure reads the named code out of a run error caused by an organization
// credential: org_resource_missing, managed_by_organization or a provider code.
func orgFailure(errorText string) string {
	switch {
	case strings.Contains(errorText, codeOrgResourceMissing):
		return codeOrgResourceMissing
	case strings.Contains(errorText, codeManagedByOrganization+": models are provided"):
		return codeManagedByOrganization
	}
	if i := strings.Index(errorText, orgFailureMarker); i >= 0 {
		rest := errorText[i+len(orgFailureMarker):]
		for _, c := range []string{codeProviderAuthFailed, codeProviderQuota} {
			if strings.HasPrefix(rest, c) {
				return c
			}
		}
		return codeProviderError
	}
	return ""
}

func orgFailureMessage(code string) string {
	switch code {
	case codeOrgResourceMissing:
		return "Your administrator has not provided the key for this model provider yet. Ask your administrator."
	case codeManagedByOrganization:
		return "Your organization provides the models for this account. This model is not one of them; choose another."
	case codeProviderQuota:
		return "The organization's account with this model provider has reached its usage or billing limit. Ask your administrator."
	case codeProviderAuthFailed:
		return "The organization's key for this model provider was rejected. Ask your administrator."
	}
	return "The model provider failed on the organization's credential. Ask your administrator."
}

// noteOrgModelUse reports one model call to the organization's usage when it
// used an organization credential. Own keys report nothing.
func (s *Server) noteOrgModelUse(model string, usage provider.Usage) {
	if s.orgUsage == nil || s.credentialSource(runtime.ModelProvider(model)) != sourceOrganization {
		return
	}
	usd, _ := usageEquivalent(model, usage)
	s.orgUsage(OrgUsageEvent{Capability: capAIModels, Requests: 1, USD: usd})
}

// noteOrgWebUse reports one paid web call made with an organization key.
func (s *Server) noteOrgWebUse(chain, id string) {
	if s.orgUsage == nil {
		return
	}
	if sp, ok := webSpec(id); !ok || !sp.Paid {
		return
	}
	c := capWebSearch
	if chain == webChainFetch {
		c = capWebFetch
	}
	s.orgUsage(OrgUsageEvent{Capability: c, Requests: 1})
}

// orgModelClient is the client of a model provider while the credential is the
// organization's: the same destination rules as the own client (the dial check
// runs for a compatible endpoint), with refusals of the credential redacted.
func (s *Server) orgModelClient(name string) *http.Client {
	s.providerMu.Lock()
	c := s.orgHTTPClients[name]
	s.providerMu.Unlock()
	if c != nil {
		return c
	}
	var base http.RoundTripper = http.DefaultTransport
	timeout := time.Duration(0)
	if name == providerCompat {
		own := s.ownCompatClient()
		base, timeout = own.Transport, own.Timeout
	}
	c = &http.Client{Timeout: timeout, Transport: orgSanitizeTransport{base: base}}
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if s.orgHTTPClients == nil {
		s.orgHTTPClients = map[string]*http.Client{}
	}
	if prev := s.orgHTTPClients[name]; prev != nil {
		return prev
	}
	s.orgHTTPClients[name] = c
	return c
}

// ---- model-facing text ----

const (
	policyNoSearch   = "Web search is turned off for this person's workspace. Say so when asked to search the web; do not offer to set it up."
	policyNoComputer = "No Bot computer is available in this workspace. Say so when a request needs one."
)

// policyNotesFor is the on-demand policy block of the run context: a line only
// where a capability is off. A provided or builtin capability needs no text.
func policyNotesFor(p ResolvedPolicy) string {
	var lines []string
	if p[capWebSearch].Mode == "off" {
		lines = append(lines, policyNoSearch)
	}
	if p[capComputer].Mode == "none" {
		lines = append(lines, policyNoComputer)
	}
	if len(lines) == 0 {
		return ""
	}
	return "[policy]\n" + strings.Join(lines, "\n") + "\n[/policy]"
}

// ---- settings tool and the client view ----

// managedSettingMode names the policy mode that governs a settings key while it
// is managed: "provided", "off", "builtin", "none", or "" when the key is free.
func managedSettingMode(key string, p ResolvedPolicy) string {
	switch key {
	case "automation.web_search":
		if m := p[capWebSearch].Mode; m == policyModeProvided || m == "off" {
			return m
		}
	case "web_tools.keys":
		for _, c := range []string{capWebSearch, capWebFetch} {
			if m := p[c].Mode; m != policyModeOwn {
				return m
			}
		}
	case "models.providers":
		if p[capAIModels].Mode == policyModeProvided {
			return policyModeProvided
		}
	case "system.computer_resources":
		if m := p[capComputer].Mode; m == "none" || m == policyModeProvided {
			return m
		}
	}
	return ""
}

func settingsManagedError(message string) error {
	o := tooloutcome.New(tooloutcome.Denied, "settings_forbidden", "not_executed", message, "explain_blocker")
	o.Detail = &tooloutcome.Detail{Code: codeManagedByOrganization}
	return o.Err()
}

// capabilityView is one capability on GET /api/config.
type capabilityView struct {
	Mode   string `json:"mode"`
	Source string `json:"source"`
	// Via is the computer source of a provided computer: vm or e2b.
	Via string `json:"via,omitempty"`
}

// capabilitiesView is the policy a client may rely on. Source says whose
// resource serves the capability: the account's own, the organization's, or none.
func (s *Server) capabilitiesView() map[string]capabilityView {
	p := s.policy()
	out := map[string]capabilityView{}
	for _, c := range orgCapabilities {
		v := p[c]
		src := sourceOwn
		switch {
		case v.Mode == policyModeProvided:
			src = sourceOrganization
		case v.Mode == "off" || v.Mode == "none" || v.Mode == "builtin":
			src = "none"
		}
		out[c] = capabilityView{Mode: v.Mode, Source: src, Via: v.Source}
	}
	return out
}

// orgEndpointControl is the dial check for an address an administrator set as an
// organization resource: loopback, private and carrier-grade NAT are fine, and
// link-local (cloud metadata), unspecified and multicast are refused at every
// dial, so a DNS change or a redirect meets the same rule.
func orgEndpointControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errCompatBlocked
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !compatAddrAllowed(addr, true) {
		return errCompatBlocked
	}
	return nil
}

var orgEndpointHTTP = &http.Client{
	Timeout: webToolTimeout,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, Control: orgEndpointControl}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	},
}

func orgEndpointClient() *http.Client { return orgEndpointHTTP }

// ---- gateway: what follows a policy change ----

func (g *AccountGateway) groupMemberIDs(ctx context.Context, groupID string) []string {
	rows, err := g.root.store.db.QueryContext(ctx, `SELECT account_id FROM org_group_members WHERE group_id=?`, groupID)
	if err != nil {
		log.Printf("[org] list members of %s: %v", groupID, err)
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// applyPolicyChange takes the VM away from every running workspace whose new
// computer policy gives it none (none, own, provided/e2b), through the existing
// DetachVM path: stop new calls, drain, copy the outbox, then detach. It runs in
// the background so an admin request does not wait on a drain; Close waits for it.
// A workspace that is not loaded follows the policy when it starts.
func (g *AccountGateway) applyPolicyChange(accountIDs []string) {
	for _, id := range accountIDs {
		g.mu.Lock()
		ws := g.workspaces[id]
		g.mu.Unlock()
		if ws == nil {
			continue
		}
		pol, err := g.policyFor(context.Background(), id)
		if err != nil {
			continue
		}
		v := pol[capComputer]
		cur := ws.computer()
		if cur == nil || (cur.Kind() == envKindE2B && computerAllowsOwnE2B(v)) || (cur.Kind() != envKindE2B && computerAllowsVM(v)) {
			continue // nothing attached, or what is attached is what the policy allows
		}
		g.policyApply.Add(1)
		go func(ws *Server, id string, v PolicyValue) {
			defer g.policyApply.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*detachWaitDefault)
			defer cancel()
			if _, err := ws.DetachVM(ctx, detachWaitDefault); err != nil && !errors.Is(err, errComputerNone) {
				log.Printf("[org] detach after policy change for account %s: %v", id, err)
			}
		}(ws, id, pol[capComputer])
	}
}
