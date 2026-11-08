package app

// The organization's E2B computer (docs/design/user-groups.md 2.5 S4, 4.4;
// slice 3). One E2B team belongs to the organization, so there is one template
// per TOFI version, built by the gateway, and one sandbox per account, made by
// the member's own workspace with the organization key asked for at each request.
//
//   - Organization part: the key (organization vault, computer:e2b), the template
//     id, the build phase (org_resources['computer:e2b'].config) and the template
//     build itself (orgE2BTemplate, kickOrgE2BBuild: single flight).
//   - Workspace part: the sandbox id, the guest token and the sandbox error
//     (orgE2BStore: the automation value environments.e2b_org and the vault id
//     computer:e2b-org:guest, never the ids an account's own E2B computer uses).
//
// Nothing of the organization's goes into a sandbox: the key is only the
// X-API-Key header of a request to the E2B API, the sandbox gets no environment
// and no file from us but its own guest token, and the per-sandbox envd and
// traffic tokens stay in the provider's memory on the server.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/e2b"
)

const (
	orgE2BStateKey     = "environments.e2b_org"
	orgE2BGuestVaultID = "computer:e2b-org:guest"
	orgE2BTemplateID   = "org-template"

	codeCustomTemplate = "custom_template"
)

// e2bHostURL and e2bNow are test seams (nil in production): where the guest and
// envd of a sandbox are reached, and the clock that times the sandbox.
var (
	e2bHostURL func(port int, sandboxID, domain string) string
	e2bNow     func() time.Time
)

// ---- organization part: configuration and build state ----

// orgE2BBuild is the build state stored under "build" in the resource's config.
type orgE2BBuild struct {
	TemplateID string `json:"template_id,omitempty"`
	BuildID    string `json:"build_id,omitempty"`
	Version    string `json:"version,omitempty"`
	Phase      string `json:"phase"`
	ErrorCode  string `json:"error_code,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// orgE2BConfig is the resource's config: a template the admin built (never
// rebuilt) and the build the server made.
type orgE2BConfig struct {
	TemplateID string       `json:"template_id,omitempty"`
	Build      *orgE2BBuild `json:"build,omitempty"`
}

func parseOrgE2BConfig(raw string) (orgE2BConfig, map[string]json.RawMessage) {
	var c orgE2BConfig
	_ = json.Unmarshal([]byte(raw), &c)
	rest := map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(raw), &rest)
	return c, rest
}

// orgE2BBuildView is what an admin sees of the template: custom (the admin's own
// template, never rebuilt), or the build phase for this TOFI version.
type orgE2BBuildView struct {
	// Phase: none (not built for this version yet), preparing, ready, failed.
	Phase     string `json:"phase"`
	Custom    bool   `json:"custom"`
	Version   string `json:"version"`
	ErrorCode string `json:"error_code,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func orgE2BViewOf(row orgResourceRow) *orgE2BBuildView {
	if row.ID != e2bKeyVaultID {
		return nil
	}
	cfg, _ := parseOrgE2BConfig(row.Config)
	v := &orgE2BBuildView{Phase: e2b.PhaseNone, Version: e2bTemplateVersion()}
	switch {
	case cfg.TemplateID != "":
		v.Phase, v.Custom = e2b.PhaseReady, true
	case cfg.Build != nil && cfg.Build.Version == v.Version:
		v.Phase, v.ErrorCode, v.UpdatedAt = cfg.Build.Phase, cfg.Build.ErrorCode, cfg.Build.UpdatedAt
	}
	return v
}

// orgE2BGate owns the one build at a time.
type orgE2BGate struct {
	mu sync.Mutex
	// gen changes whenever the key or the config is replaced: a build of an older
	// generation can no longer write its state or be mistaken for the current one.
	gen int
	run *orgE2BRun
}

type orgE2BRun struct {
	gen    int
	cancel context.CancelFunc
}

// orgE2BTemplate is the template part of an account's E2B state, read at each
// use (Config.OrgE2BTemplate). It starts the build when this TOFI version has
// none; any number of callers at once start one.
func (g *AccountGateway) orgE2BTemplate(ctx context.Context) (e2b.State, error) {
	missing := e2b.State{Phase: e2b.PhaseFailed, ErrorCode: codeOrgResourceMissing}
	if _, err := g.orgCredential(ctx, e2bKeyVaultID); err != nil {
		if errors.Is(err, errOrgResourceMissing) {
			return missing, nil
		}
		return e2b.State{}, err
	}
	row, found, err := g.getOrgResource(ctx, e2bKeyVaultID)
	if err != nil {
		return e2b.State{}, err
	}
	if !found {
		return missing, nil
	}
	cfg, _ := parseOrgE2BConfig(row.Config)
	version := e2bTemplateVersion()
	if cfg.TemplateID != "" {
		return e2b.State{TemplateID: cfg.TemplateID, TemplateVersion: version, Phase: e2b.PhaseReady, CustomTemplate: true}, nil
	}
	if b := cfg.Build; b != nil && b.Version == version {
		switch {
		case b.Phase == e2b.PhaseReady && b.TemplateID != "":
			return e2b.State{TemplateID: b.TemplateID, BuildID: b.BuildID, TemplateVersion: version, Phase: e2b.PhaseReady}, nil
		case b.Phase == e2b.PhaseFailed:
			// A failed build stays failed until the admin retries it: members do not each retry it.
			return e2b.State{Phase: e2b.PhaseFailed, ErrorCode: b.ErrorCode}, nil
		}
	}
	g.kickOrgE2BBuild()
	return e2b.State{Phase: e2b.PhasePreparing}, nil
}

// kickOrgE2BBuild starts the organization's template build unless one of the
// current generation runs. It returns at once; the build runs in the gateway, in
// the organization's context, never inside a member's workspace.
func (g *AccountGateway) kickOrgE2BBuild() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.orgE2B.mu.Lock()
	if r := g.orgE2B.run; r != nil && r.gen == g.orgE2B.gen {
		g.orgE2B.mu.Unlock()
		g.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e2bPrepareTimeout)
	run := &orgE2BRun{gen: g.orgE2B.gen, cancel: cancel}
	g.orgE2B.run = run
	g.orgBuilds.Add(1)
	g.orgE2B.mu.Unlock()
	g.mu.Unlock()
	go g.runOrgE2BBuild(ctx, run)
}

func (g *AccountGateway) runOrgE2BBuild(ctx context.Context, run *orgE2BRun) {
	defer g.orgBuilds.Done()
	defer func() {
		g.orgE2B.mu.Lock()
		if g.orgE2B.run == run {
			g.orgE2B.run = nil
		}
		g.orgE2B.mu.Unlock()
		run.cancel()
	}()
	row, found, err := g.getOrgResource(ctx, e2bKeyVaultID)
	if err != nil || !found {
		return
	}
	if cfg, _ := parseOrgE2BConfig(row.Config); cfg.TemplateID != "" {
		return // the admin's own template is used as it is
	}
	key, err := g.orgCredential(ctx, e2bKeyVaultID)
	if err != nil {
		return
	}
	store := orgE2BBuildStore{g: g, gen: run.gen}
	p, err := e2b.New(e2b.Config{
		ID:            orgE2BTemplateID,
		APIKey:        key.Value,
		APIBase:       e2bAPIBase(),
		Image:         e2bImage(),
		Version:       e2bTemplateVersion(),
		Store:         store,
		HostURL:       e2bHostURL,
		OnKeyRejected: func() { g.recordOrgRejection(e2bKeyVaultID, e2b.CodeInvalidKey) },
	})
	if err != nil {
		log.Printf("[org] E2B template build: %v", err)
		return
	}
	if err := p.Prepare(ctx); err != nil {
		// Prepare saved the phase and the code; only the code is logged.
		log.Printf("[org] E2B template build for version %s ended: %s", e2bTemplateVersion(), e2bErrorCode(err))
	}
}

// orgE2BReset ends the build of the current generation and makes the next one a
// new generation. Caller holds orgWriteMu (the writes of build state take it too),
// so no write of the old build lands after the reset.
func (g *AccountGateway) orgE2BReset() {
	g.orgE2B.mu.Lock()
	g.orgE2B.gen++
	if g.orgE2B.run != nil {
		g.orgE2B.run.cancel()
	}
	g.orgE2B.mu.Unlock()
}

func (g *AccountGateway) orgE2BBuilding() bool {
	g.orgE2B.mu.Lock()
	defer g.orgE2B.mu.Unlock()
	return g.orgE2B.run != nil && g.orgE2B.run.gen == g.orgE2B.gen
}

// stopOrgE2BBuilds cancels a running build and waits for it (Close).
func (g *AccountGateway) stopOrgE2BBuilds() {
	g.orgE2B.mu.Lock()
	if g.orgE2B.run != nil {
		g.orgE2B.run.cancel()
	}
	g.orgE2B.mu.Unlock()
	g.orgBuilds.Wait()
}

var errOrgBuildSuperseded = errors.New("organization E2B build superseded")

// writeOrgE2BBuild stores the build state in the resource's config, keeping the
// rest of the config. gen < 0 writes unconditionally (a retry clearing the state).
// It touches only the config column, so the test and rejection paths' version
// stamp (updated_at) is not disturbed.
func (g *AccountGateway) writeOrgE2BBuild(ctx context.Context, gen int, b *orgE2BBuild) error {
	g.orgWriteMu.Lock()
	defer g.orgWriteMu.Unlock()
	return g.writeOrgE2BBuildLocked(ctx, gen, b)
}

func (g *AccountGateway) writeOrgE2BBuildLocked(ctx context.Context, gen int, b *orgE2BBuild) error {
	g.orgE2B.mu.Lock()
	current := g.orgE2B.gen
	g.orgE2B.mu.Unlock()
	if gen >= 0 && gen != current {
		return errOrgBuildSuperseded
	}
	row, found, err := g.getOrgResource(ctx, e2bKeyVaultID)
	if err != nil {
		return err
	}
	if !found {
		return errOrgBuildSuperseded
	}
	_, rest := parseOrgE2BConfig(row.Config)
	if b == nil {
		delete(rest, "build")
	} else {
		raw, err := json.Marshal(b)
		if err != nil {
			return err
		}
		rest["build"] = raw
	}
	out, err := json.Marshal(rest)
	if err != nil {
		return err
	}
	_, err = g.root.store.db.ExecContext(ctx, `UPDATE org_resources SET config=? WHERE id=?`, string(out), e2bKeyVaultID)
	return err
}

// orgE2BBuildStore is the e2b.Store of the template builder: the template part
// only, kept in the organization's resource. It has no guest token.
type orgE2BBuildStore struct {
	g   *AccountGateway
	gen int
}

func (st orgE2BBuildStore) LoadState() (e2b.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), policyReadTimeout)
	defer cancel()
	row, found, err := st.g.getOrgResource(ctx, e2bKeyVaultID)
	if err != nil || !found {
		return e2b.State{Phase: e2b.PhaseNone}, err
	}
	cfg, _ := parseOrgE2BConfig(row.Config)
	if cfg.Build == nil || cfg.Build.Version != e2bTemplateVersion() {
		return e2b.State{Phase: e2b.PhaseNone}, nil
	}
	b := cfg.Build
	return e2b.State{TemplateID: b.TemplateID, BuildID: b.BuildID, TemplateVersion: b.Version, Phase: b.Phase, ErrorCode: b.ErrorCode}, nil
}

func (st orgE2BBuildStore) SaveState(v e2b.State) error {
	ctx, cancel := context.WithTimeout(context.Background(), policyReadTimeout)
	defer cancel()
	version := v.TemplateVersion
	if version == "" || v.Phase != e2b.PhaseReady {
		version = e2bTemplateVersion() // a build in progress or failed is of this version
	}
	return st.g.writeOrgE2BBuild(ctx, st.gen, &orgE2BBuild{
		TemplateID: v.TemplateID, BuildID: v.BuildID, Version: version, Phase: v.Phase, ErrorCode: v.ErrorCode, UpdatedAt: now(),
	})
}

func (orgE2BBuildStore) GuestToken() (string, error) { return "", nil }
func (orgE2BBuildStore) SaveGuestToken(string) error { return nil }

// retryOrgE2BBuild is the admin's "retry build": POST /api/admin/org/resources/computer:e2b/build.
func (g *AccountGateway) retryOrgE2BBuild(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, found, err := g.getOrgResource(ctx, e2bKeyVaultID)
	if err != nil {
		storageFailure(w, "read resource", err)
		return
	}
	if !found {
		writeErr(w, 404, "not_found", "resource not found")
		return
	}
	if cfg, _ := parseOrgE2BConfig(row.Config); cfg.TemplateID != "" {
		writeErr(w, 409, codeCustomTemplate, "This computer uses a template you supplied. It is never rebuilt.")
		return
	}
	if !g.orgE2BBuilding() {
		g.orgWriteMu.Lock()
		g.orgE2BReset()
		err = g.writeOrgE2BBuildLocked(ctx, -1, nil)
		g.orgWriteMu.Unlock()
		if err != nil {
			storageFailure(w, "reset build", err)
			return
		}
		g.kickOrgE2BBuild()
		g.markOrgE2BPreparing(ctx)
	}
	row, _, err = g.getOrgResource(ctx, e2bKeyVaultID)
	if err != nil {
		storageFailure(w, "read resource", err)
		return
	}
	writeJSON(w, 202, row.view())
}

// markOrgE2BPreparing shows the build as running at once, before the builder's
// first save of its own.
func (g *AccountGateway) markOrgE2BPreparing(ctx context.Context) {
	g.orgE2B.mu.Lock()
	gen := g.orgE2B.gen
	g.orgE2B.mu.Unlock()
	row, found, err := g.getOrgResource(ctx, e2bKeyVaultID)
	if err != nil || !found {
		return
	}
	if cfg, _ := parseOrgE2BConfig(row.Config); cfg.Build != nil || cfg.TemplateID != "" {
		return
	}
	_ = g.writeOrgE2BBuild(ctx, gen, &orgE2BBuild{Version: e2bTemplateVersion(), Phase: e2b.PhasePreparing, UpdatedAt: now()})
}

// ---- workspace part ----

// orgE2BStore is the provider's store for a provided/e2b workspace: the template
// part comes from the organization (Provider's TemplateSource), so what is kept
// here is only the sandbox (id, its error) and its guest token. Both live in the
// member's workspace under ids its own E2B computer does not use.
type orgE2BStore struct{ s *Server }

type orgE2BSandboxState struct {
	SandboxID string `json:"sandbox_id,omitempty"`
	Phase     string `json:"phase"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (st orgE2BStore) LoadState() (e2b.State, error) {
	raw, ok := st.s.autoStored(orgE2BStateKey)
	out := e2b.State{Phase: e2b.PhaseReady}
	if !ok || strings.TrimSpace(raw) == "" {
		return out, nil
	}
	var v orgE2BSandboxState
	if json.Unmarshal([]byte(raw), &v) != nil {
		return out, nil
	}
	out.SandboxID = v.SandboxID
	if v.Phase == e2b.PhaseFailed {
		out.Phase, out.ErrorCode, out.Error = e2b.PhaseFailed, v.ErrorCode, v.Error
	}
	return out, nil
}

// SaveState keeps the sandbox part only: whatever the template fields say, they
// are the organization's and are never written here.
func (st orgE2BStore) SaveState(v e2b.State) error {
	phase := e2b.PhaseReady
	if v.Phase == e2b.PhaseFailed {
		phase = e2b.PhaseFailed
	}
	b, err := json.Marshal(orgE2BSandboxState{SandboxID: v.SandboxID, Phase: phase, ErrorCode: v.ErrorCode, Error: v.Error})
	if err != nil {
		return err
	}
	value := string(b)
	return st.s.saveAutomationValue(orgE2BStateKey, &value)
}

func (st orgE2BStore) GuestToken() (string, error) {
	value, _, _ := st.s.vaultValue(orgE2BGuestVaultID)
	return value, nil
}

func (st orgE2BStore) SaveGuestToken(token string) error {
	if token == "" {
		return st.s.deleteVaultValue(orgE2BGuestVaultID)
	}
	return st.s.storeVaultValue(orgE2BGuestVaultID, "Organization E2B computer access token", "", token)
}

// orgE2BKey asks the organization for its E2B key. It is used for one request
// and not kept.
func (s *Server) orgE2BKey(ctx context.Context) (string, error) {
	if s.orgCredential == nil {
		return "", e2b.ErrKeyMissing
	}
	sec, err := s.orgCredential(ctx, e2bKeyVaultID)
	switch {
	case errors.Is(err, errOrgResourceMissing):
		return "", e2b.ErrKeyMissing
	case err != nil:
		return "", errors.New("organization key unavailable")
	}
	return sec.Value, nil
}

// orgE2BScrub removes the organization's key (every 8-character stretch of it)
// from text an E2B error carries.
func (s *Server) orgE2BScrub(text string) string {
	if s.orgCredential == nil || text == "" {
		return text
	}
	ctx, cancel := context.WithTimeout(context.Background(), policyReadTimeout)
	defer cancel()
	sec, err := s.orgCredential(ctx, e2bKeyVaultID)
	if err != nil {
		return text
	}
	return scrubSecrets(text, []string{sec.Value})
}

func (s *Server) noteOrgComputerUse(d time.Duration) {
	if s.orgUsage != nil && d > 0 {
		s.orgUsage(OrgUsageEvent{Capability: capComputer, ComputeMS: d.Milliseconds()})
	}
}

// orgE2BProvider is this account's E2B computer under provided/e2b, built once.
// It holds no key: each request asks the gateway for it.
func (s *Server) orgE2BProvider() *e2b.Provider {
	if s.orgCredential == nil || s.orgE2BTemplate == nil {
		return nil
	}
	s.e2bEnv.mu.Lock()
	defer s.e2bEnv.mu.Unlock()
	if s.e2bEnv.orgProvider != nil {
		return s.e2bEnv.orgProvider
	}
	cfg := e2b.Config{
		ID:              computer.DefaultID,
		KeySource:       s.orgE2BKey,
		APIBase:         e2bAPIBase(),
		Version:         e2bTemplateVersion(),
		Store:           orgE2BStore{s},
		IdleTimeout:     s.e2bIdleTimeout(),
		TemplateSource:  s.orgE2BTemplate,
		SandboxMetadata: map[string]string{"account": s.accountID},
		OnKeyRejected: func() {
			if s.orgOutcome != nil {
				s.orgOutcome(e2bKeyVaultID, e2b.CodeInvalidKey)
			}
		},
		OnUsage: s.noteOrgComputerUse,
		Scrub:   s.orgE2BScrub,
		HostURL: e2bHostURL,
		Now:     e2bNow,
	}
	p, err := e2b.New(cfg)
	if err != nil {
		log.Printf("[environments] organization E2B provider for account %s: %v", s.accountID, err)
		return nil
	}
	s.e2bEnv.orgProvider = p
	return p
}

// computerUsesOrgE2B says whether the policy gives this account the organization's E2B computer.
func computerUsesOrgE2B(v PolicyValue) bool {
	return v.Mode == policyModeProvided && v.Source == computerSourceE2B
}

// attachedIsOrgE2B says whether the Bot computer now attached is the organization's.
func (s *Server) attachedIsOrgE2B() bool {
	cur := s.computer()
	if cur == nil {
		return false
	}
	s.e2bEnv.mu.Lock()
	defer s.e2bEnv.mu.Unlock()
	return s.e2bEnv.orgProvider != nil && cur == computer.Provider(s.e2bEnv.orgProvider)
}

// attachOrgE2B puts the organization's E2B computer into the Bot computer slot.
// It attaches even when the organization holds no key or the template failed:
// the computer then reports that named code (org_resource_missing, a build code)
// and Retry reaches it, and nothing falls back to a key of the person's own.
func (s *Server) attachOrgE2B() error {
	p := s.orgE2BProvider()
	if p == nil {
		return errComputerBackend
	}
	if err := s.attachProvider(p); err != nil {
		return err
	}
	// Reading the template starts the organization's build when this version has none.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), policyReadTimeout)
		defer cancel()
		_ = p.RefreshTemplate(ctx)
	}()
	// The policy may have changed while the attach ran.
	if s.accountID != "" && s.orgPolicy != nil && !computerUsesOrgE2B(s.policy()[capComputer]) {
		s.enforceComputerPolicy(context.Background())
	}
	return nil
}

// settleOrgE2B reports the running time of a sandbox left running when the
// workspace stops.
func (s *Server) settleOrgE2B() {
	s.e2bEnv.mu.Lock()
	p := s.e2bEnv.orgProvider
	s.e2bEnv.mu.Unlock()
	if p != nil {
		p.Settle()
	}
}

// orgE2BEnvironmentView fills the Environments view for a provided/e2b account:
// the organization's computer, with the actions a member keeps (Retry) and none
// that change whose computer it is.
func (s *Server) orgE2BEnvironmentView(ctx context.Context, out *environmentsView) {
	p := s.orgE2BProvider()
	if p == nil {
		out.E2B = e2bView{Phase: "none"}
		return
	}
	_ = p.RefreshTemplate(ctx)
	st := p.Status()
	phase := st.Phase
	if phase == e2b.PhaseNone {
		phase = "preparing"
	}
	// An organization failure is a code only: no upstream text for a member.
	out.E2B = e2bView{Configured: true, Phase: phase, ErrorCode: st.ErrorCode}
	item := environmentItem{ID: e2bEnvironmentID, Kind: envKindE2B, State: "asleep", ErrorCode: st.ErrorCode, Access: "none", Actions: []string{}}
	switch {
	case phase == "preparing":
		item.State = "preparing"
	case phase == e2b.PhaseFailed:
		item.State = "failed"
		item.Actions = append(item.Actions, "retry")
	case s.attachedIsOrgE2B():
		item.BotComputer, item.Access = true, "all_bots"
		if info, err := s.microVMInfo(ctx); err == nil {
			item.State = environmentState(info.State)
			if info.State == "error" {
				item.Actions = append(item.Actions, "retry")
			}
		}
	}
	if item.BotComputer {
		out.Environments = append([]environmentItem{item}, out.Environments...)
	} else {
		out.Environments = append(out.Environments, item)
	}
}
