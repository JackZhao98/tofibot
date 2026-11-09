package e2b

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// Phases of State.Phase.
const (
	PhaseNone      = "none"
	PhasePreparing = "preparing"
	PhaseReady     = "ready"
	PhaseFailed    = "failed"
)

const guestTokenPath = "/var/lib/tofi/guest-token"

// guestTokenFallbackUser owns uid 1000 in the Guest image (ubuntu:24.04). The
// token is written as root first; if the guest (uid 1000) cannot read that
// file, it is written again as this user.
const guestTokenFallbackUser = "ubuntu"
const envdPort = 49983

// State is the non-secret, persisted state of the E2B computer.
type State struct {
	TemplateID      string `json:"template_id,omitempty"`
	BuildID         string `json:"build_id,omitempty"`
	TemplateVersion string `json:"template_version,omitempty"`
	SandboxID       string `json:"sandbox_id,omitempty"`
	Phase           string `json:"phase"` // none, preparing, ready, failed
	ErrorCode       string `json:"error_code,omitempty"`
	Error           string `json:"error,omitempty"` // E2B's own short message, never a secret
	// CustomTemplate: the person gave a template they built themselves; Prepare never builds.
	CustomTemplate bool `json:"custom_template,omitempty"`
	// SandboxVersion names the template the sandbox was created from: the TOFI
	// version of a built template, or "custom:<template id>". Empty when the
	// sandbox was made before this was recorded (it counts as outdated, but usable).
	SandboxVersion string `json:"sandbox_version,omitempty"`

	// Computed by Status, never stored. TemplatePhase is the template's own phase:
	// Phase is the computer's, and a template that is being built or failed gates
	// only the creation of a sandbox, never one that exists.
	TemplatePhase     string `json:"template_phase,omitempty"`
	TemplateErrorCode string `json:"template_error_code,omitempty"`
	// GuestOutdated: the sandbox runs an older image than the template that is
	// ready now. It stays usable; replacing it (Upgrade) needs the person's consent.
	GuestOutdated bool `json:"guest_outdated,omitempty"`
	// GuestOutdatedRequired: the sandbox is below Config.MinGuestVersion and is refused.
	GuestOutdatedRequired bool `json:"guest_outdated_required,omitempty"`
}

// Store persists State and the sealed guest token.
type Store interface {
	LoadState() (State, error)
	SaveState(State) error
	GuestToken() (string, error) // "" when none
	SaveGuestToken(string) error
}

type Config struct {
	ID          string        // default "e2b"
	APIKey      string        //
	APIBase     string        // default https://api.e2b.app
	Image       string        // fromImage for template builds; required by Prepare
	Version     string        // TOFI version
	GuestPort   int           // default 8331
	IdleTimeout time.Duration // default 15m
	HTTP        *http.Client
	Store       Store
	Now         func() time.Time
	HostURL     func(port int, sandboxID, domain string) string

	// The fields below serve a computer whose E2B team belongs to an organization
	// (the member's workspace never holds the key or builds the template).

	// KeySource replaces APIKey: the key is asked for at each request and never kept.
	KeySource func(ctx context.Context) (string, error)
	// TemplateSource gives the template part of the state at each use: the template
	// is the organization's, so only the sandbox part of State is this provider's own.
	TemplateSource func(ctx context.Context) (State, error)
	// SandboxMetadata is added to the sandbox's metadata (account marker).
	SandboxMetadata map[string]string
	// OnKeyRejected is told when E2B answers 401 or 403 to a team-level request
	// (creating a sandbox, a template call) made with the key; usedKey is the key
	// that request carried, so the owner can ignore an answer to an older key.
	OnKeyRejected func(usedKey string)
	// MinGuestVersion, when set, refuses (guest_outdated_required) a sandbox made
	// from an image older than it until the person upgrades. Empty: not enforced.
	MinGuestVersion string
	// OnUsage receives sandbox running time as it accrues.
	OnUsage func(time.Duration)
	// Scrub cleans an upstream message further (the organization's key).
	Scrub func(string) string
}

// Provider is the E2B Bot computer.
type Provider struct {
	cfg Config
	api *api

	// pinDomain: with E2B's own API, sandbox URLs must be on E2B's domains.
	pinDomain bool

	ensureMu sync.Mutex // single-flight create/connect/remove
	prepMu   sync.Mutex

	mu          sync.Mutex // guards everything below
	state       State
	token       string
	running     bool
	envdToken   string
	trafficTok  string
	domain      string
	lastRefresh time.Time
	creating    bool
	info        computer.Info
	infoAt      time.Time
	infoOK      bool
	idle        time.Duration // how long E2B lets the sandbox sit idle before it pauses it (SetIdleTimeout)
	tpl         *State        // the template part from Config.TemplateSource, last read
	usageFrom   time.Time     // running time before this is reported; zero while not running

	pollEvery   time.Duration
	healthEvery time.Duration
	healthWait  time.Duration
}

var (
	_ computer.Provider = (*Provider)(nil)
	_ computer.Streamer = (*Provider)(nil)
	_ computer.Healther = (*Provider)(nil)
)

var streamBotID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// New builds a provider and loads its persisted state.
func New(cfg Config) (*Provider, error) {
	if cfg.Store == nil {
		return nil, errors.New("e2b: store is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" && cfg.KeySource == nil {
		return nil, errors.New("e2b: api key is required")
	}
	if cfg.ID == "" {
		cfg.ID = "e2b"
	}
	pin := cfg.APIBase == "" || strings.TrimRight(cfg.APIBase, "/") == defaultAPIBase
	if cfg.APIBase == "" {
		cfg.APIBase = defaultAPIBase
	}
	if cfg.GuestPort == 0 {
		cfg.GuestPort = 8331
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 15 * time.Minute
	}
	if cfg.HTTP == nil {
		cfg.HTTP = defaultHTTP()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HostURL == nil {
		cfg.HostURL = func(port int, sandboxID, domain string) string {
			return fmt.Sprintf("https://%d-%s.%s", port, sandboxID, domain)
		}
	}
	st, err := cfg.Store.LoadState()
	if err != nil {
		return nil, fmt.Errorf("e2b: load state: %w", err)
	}
	if st.Phase == "" {
		st.Phase = PhaseNone
	}
	p := &Provider{cfg: cfg, state: st, pinDomain: pin, idle: cfg.IdleTimeout, pollEvery: 5 * time.Second, healthEvery: time.Second, healthWait: 60 * time.Second}
	p.api = &api{hc: cfg.HTTP, base: strings.TrimRight(cfg.APIBase, "/"), key: cfg.APIKey, keyFn: cfg.KeySource, secrets: p.secretValues, extraScrub: cfg.Scrub}
	p.api.memberSafe = cfg.TemplateSource != nil
	if cfg.OnKeyRejected != nil {
		p.api.onReject = func(_ int, key string) { cfg.OnKeyRejected(key) }
	}
	return p, nil
}

func (p *Provider) ID() string   { return p.cfg.ID }
func (p *Provider) Kind() string { return "e2b" }

func (p *Provider) secretValues() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []string{p.token, p.envdToken, p.trafficTok}
	if p.cfg.TemplateSource != nil && p.tpl != nil {
		// The organization's template and build belong to the organization: a
		// member's view of an upstream message never names them.
		out = append(out, p.tpl.TemplateID, p.tpl.BuildID)
	}
	return out
}

// Status returns the persisted state. With a TemplateSource the template part
// is the organization's (read by RefreshTemplate), and a template that is not
// ready shows as the computer's own phase.
func (p *Provider) Status() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statusLocked()
}

func (p *Provider) statusLocked() State {
	st := p.state
	tplPhase, tplCode, tplErr := PhaseReady, "", ""
	if p.tpl != nil {
		st.TemplateID, st.BuildID, st.TemplateVersion, st.CustomTemplate = p.tpl.TemplateID, p.tpl.BuildID, p.tpl.TemplateVersion, p.tpl.CustomTemplate
		tplPhase, tplCode, tplErr = p.tpl.Phase, p.tpl.ErrorCode, p.tpl.Error
	} else if st.Phase == PhaseNone || st.Phase == PhasePreparing || (st.Phase == PhaseFailed && templateFailure(st.ErrorCode)) {
		// The person's own computer keeps the template's progress in its Phase.
		tplPhase, tplCode, tplErr = st.Phase, st.ErrorCode, st.Error
		if st.SandboxID != "" {
			st.Phase, st.ErrorCode, st.Error = PhaseReady, "", ""
		}
	}
	if tplPhase != PhaseReady && st.SandboxID == "" {
		// No sandbox yet: the template gates its creation.
		st.Phase, st.ErrorCode, st.Error = tplPhase, tplCode, tplErr
	}
	st.TemplatePhase, st.TemplateErrorCode = tplPhase, tplCode
	if st.SandboxID != "" {
		if tplPhase == PhaseReady && st.TemplateID != "" {
			if ident := templateIdentity(st); ident != "" && st.SandboxVersion != ident {
				st.GuestOutdated = true
			}
		}
		if min := p.cfg.MinGuestVersion; min != "" && !strings.HasPrefix(st.SandboxVersion, "custom:") && VersionBelow(st.SandboxVersion, min) {
			st.GuestOutdatedRequired = true
		}
	}
	return st
}

// templateFailure says whether a failure code belongs to the template, not the sandbox.
func templateFailure(code string) bool {
	return code == CodeTemplateBuildFailed || code == CodeImageMissing || code == CodeTemplateStale
}

// templateIdentity names the template a new sandbox would be made from.
func templateIdentity(st State) string {
	if st.CustomTemplate {
		if st.TemplateID == "" {
			return ""
		}
		// A digest, not the id: the id is the organization's, and the identity is
		// kept in a member's workspace.
		sum := sha256.Sum256([]byte(st.TemplateID))
		return "custom:" + hex.EncodeToString(sum[:6])
	}
	return st.TemplateVersion
}

func (p *Provider) rawState() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// RefreshTemplate reads the organization's template state (no-op without a
// TemplateSource). Every sandbox action does it first, so a build that finished,
// a new version or a replaced key is seen at the next use.
func (p *Provider) RefreshTemplate(ctx context.Context) error {
	if p.cfg.TemplateSource == nil {
		return nil
	}
	t, err := p.cfg.TemplateSource(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: CodeUnreachable, Message: "the organization's template could not be read"}
	}
	p.mu.Lock()
	if p.tpl == nil || *p.tpl != t {
		p.infoOK = false
	}
	p.tpl = &t
	p.mu.Unlock()
	return nil
}

// update applies fn to the state and persists it.
func (p *Provider) update(fn func(*State)) error {
	p.mu.Lock()
	fn(&p.state)
	st := p.state
	p.infoOK = false
	p.mu.Unlock()
	return p.cfg.Store.SaveState(st)
}

func (p *Provider) fail(code, msg string) {
	_ = p.update(func(s *State) { s.Phase, s.ErrorCode, s.Error = PhaseFailed, code, msg })
}

func (p *Provider) invalidate() {
	p.mu.Lock()
	p.infoOK = false
	p.mu.Unlock()
}

func (p *Provider) idleTimeout() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idle
}

// SetIdleTimeout changes how long the sandbox may sit idle before E2B pauses it
// (the person's "turn off when idle" setting). A running sandbox gets the new
// timeout now; a paused or new one gets it when it resumes or is created.
// d <= 0 restores the 15 minute default.
func (p *Provider) SetIdleTimeout(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		d = 15 * time.Minute
	}
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	p.mu.Lock()
	changed := p.idle != d
	p.idle = d
	running := p.running
	p.mu.Unlock()
	id := p.Status().SandboxID
	if !changed || !running || id == "" {
		return nil
	}
	if err := p.api.setTimeout(ctx, id, d); err != nil {
		// Paused meanwhile, or gone: the next resume or create applies the new timeout.
		p.setRunning(false)
		return err
	}
	p.mu.Lock()
	p.lastRefresh = p.cfg.Now()
	p.mu.Unlock()
	return nil
}

func (p *Provider) setRunning(v bool) {
	p.mu.Lock()
	was := p.running
	p.running = v
	p.infoOK = false
	now := p.cfg.Now()
	var d time.Duration
	switch {
	case v && !was:
		p.usageFrom = now
	case !v && was:
		d = p.endUsageLocked(now, false)
	}
	p.mu.Unlock()
	p.reportUsage(d)
}

// Sandbox running time, for whoever pays for the sandbox (Config.OnUsage). It
// accrues from the moment the sandbox is created or resumed; while it is used
// the time so far is reported about once a minute (the timeout refresh), and
// when it stops the rest is. A stop nobody saw (E2B paused it after the idle
// timeout) ends at the last refresh plus that timeout, not at the time it was noticed.

func (p *Provider) reportUsage(d time.Duration) {
	if d > 0 && p.cfg.OnUsage != nil {
		p.cfg.OnUsage(d)
	}
}

// endUsageLocked closes the running interval. explicit: the stop was ours (pause, kill).
func (p *Provider) endUsageLocked(now time.Time, explicit bool) time.Duration {
	if p.usageFrom.IsZero() {
		return 0
	}
	end := now
	if !explicit {
		if limit := p.lastRefresh.Add(p.idle); !p.lastRefresh.IsZero() && limit.Before(end) {
			end = limit
		}
	}
	from := p.usageFrom
	p.usageFrom = time.Time{}
	if !end.After(from) {
		return 0
	}
	return end.Sub(from)
}

func (p *Provider) endUsage(explicit bool) {
	p.mu.Lock()
	d := p.endUsageLocked(p.cfg.Now(), explicit)
	p.mu.Unlock()
	p.reportUsage(d)
}

// accrueUsage reports the running time so far and keeps counting.
func (p *Provider) accrueUsage() {
	p.mu.Lock()
	now := p.cfg.Now()
	var d time.Duration
	if !p.usageFrom.IsZero() && now.After(p.usageFrom) {
		d = now.Sub(p.usageFrom)
		p.usageFrom = now
	}
	p.mu.Unlock()
	p.reportUsage(d)
}

// Settle reports the running time of a sandbox that is left running when its
// owner goes away (the server stops). E2B pauses it after the idle timeout.
func (p *Provider) Settle() { p.endUsage(false) }

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TemplateName is the E2B template name for a TOFI version.
func TemplateName(version string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(version) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	v := strings.Trim(b.String(), "-")
	if v == "" {
		v = "dev"
	}
	return "tofi-computer-" + v
}

// ---- template preparation ----

// Prepare ensures a ready template exists for cfg.Version. It blocks until the
// build finishes, fails or ctx is done, persisting progress as it goes.
func (p *Provider) Prepare(ctx context.Context) error {
	p.prepMu.Lock()
	defer p.prepMu.Unlock()
	st := p.rawState()
	if st.CustomTemplate && st.TemplateID != "" {
		if st.Phase != PhaseReady {
			return p.update(func(s *State) { s.Phase, s.ErrorCode, s.Error = PhaseReady, "", "" })
		}
		return nil
	}
	if strings.TrimSpace(p.cfg.Image) == "" {
		return &Error{Code: CodeImageMissing, Message: "no computer image is configured"}
	}
	if st.Phase == PhaseReady && st.TemplateID != "" && st.TemplateVersion == p.cfg.Version {
		return nil
	}
	// A sandbox of an older template is kept: it reports guest_outdated and the
	// person decides when to replace it (Upgrade). Its files are never dropped here.
	_ = p.update(func(s *State) { s.Phase, s.ErrorCode, s.Error = PhasePreparing, "", "" })
	failed := func(err error, fallback string) error {
		if errors.Is(ctx.Err(), context.Canceled) {
			// The server is closing (or the build was replaced): the build is not over, so
			// the phase stays preparing and the next use resumes it.
			return ctx.Err()
		}
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: fallback, Message: describe(err)}
		}
		p.fail(e.Code, e.Message)
		return e
	}
	tpl, err := p.api.createTemplate(ctx, TemplateName(p.cfg.Version))
	var conflict *Error
	if err != nil && errors.As(err, &conflict) && conflict.Status == http.StatusConflict {
		// The team already has this template: use its newest build.
		return p.adoptTemplate(ctx, TemplateName(p.cfg.Version), failed)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failed(err, CodeTemplateBuildFailed)
	}
	return p.runBuild(ctx, tpl, failed)
}

// runBuild starts the build E2B registered and waits for it.
func (p *Provider) runBuild(ctx context.Context, tpl templateInfo, failed func(error, string) error) error {
	if !idPattern.MatchString(tpl.TemplateID) || !idPattern.MatchString(tpl.BuildID) {
		return failed(&Error{Message: "E2B sent an invalid template id"}, CodeTemplateBuildFailed)
	}
	_ = p.update(func(s *State) { s.TemplateID, s.BuildID = tpl.TemplateID, tpl.BuildID })
	if err := p.api.startBuild(ctx, tpl.TemplateID, tpl.BuildID, p.cfg.Image); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failed(err, CodeTemplateBuildFailed)
	}
	return p.waitBuild(ctx, tpl.TemplateID, tpl.BuildID, failed)
}

// waitBuild polls a template build until it is ready or failed.
func (p *Provider) waitBuild(ctx context.Context, templateID, buildID string, failed func(error, string) error) error {
	misses := 0
	for {
		bs, err := p.api.buildStatus(ctx, templateID, buildID)
		switch {
		case err != nil && ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			var e *Error
			if errors.As(err, &e) && e.Code == CodeInvalidKey {
				return failed(err, CodeInvalidKey)
			}
			if misses++; misses >= 5 {
				return failed(err, CodeTemplateBuildFailed)
			}
		default:
			misses = 0
			switch bs.Status {
			case "ready":
				return p.update(func(s *State) {
					s.Phase, s.ErrorCode, s.Error, s.TemplateVersion = PhaseReady, "", "", p.cfg.Version
				})
			case "error":
				msg := "template build failed"
				if bs.Reason != nil && strings.TrimSpace(bs.Reason.Message) != "" {
					msg = p.api.scrub(bs.Reason.Message + bs.lastErrors(3))
				}
				code := CodeTemplateBuildFailed
				if imageProblem(msg) {
					code = CodeImageMissing
				}
				p.fail(code, msg)
				return &Error{Code: code, Message: msg}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.pollEvery):
		}
	}
}

// adoptTemplate takes over a template the team already has under the name.
func (p *Provider) adoptTemplate(ctx context.Context, name string, failed func(error, string) error) error {
	id, err := p.api.templateByName(ctx, name)
	if err != nil || !idPattern.MatchString(id) {
		if err == nil {
			err = &Error{Message: "E2B sent an invalid template id"}
		}
		return failed(err, CodeTemplateBuildFailed)
	}
	builds, err := p.api.templateBuilds(ctx, id)
	if err != nil {
		return failed(err, CodeTemplateBuildFailed)
	}
	var last *templateBuildRow
	for i := range builds {
		if last == nil || builds[i].CreatedAt >= last.CreatedAt {
			last = &builds[i]
		}
	}
	if last == nil || !idPattern.MatchString(last.BuildID) || last.Status == "error" {
		// The newest build of the team's template failed (or there is none): start a
		// new build on that template. E2B's template API registers a new build on a
		// team's own template when the name is posted again (the 409 an earlier call
		// met was a race or an older answer), so one more create is the recovery.
		tpl, cerr := p.api.createTemplate(ctx, name)
		if cerr == nil {
			return p.runBuild(ctx, tpl, failed)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var conflict *Error
		if errors.As(cerr, &conflict) && conflict.Status == http.StatusConflict {
			// E2B still refuses a new build under the taken name: only the team's owner can clear it.
			return failed(&Error{Code: CodeTemplateStale, Message: "the E2B team already has a template named " + name + " whose last build failed, and E2B did not accept a new build on it"}, CodeTemplateStale)
		}
		return failed(cerr, CodeTemplateBuildFailed)
	}
	_ = p.update(func(s *State) { s.TemplateID, s.BuildID = id, last.BuildID })
	if last.Status == "ready" {
		return p.update(func(s *State) {
			s.Phase, s.ErrorCode, s.Error, s.TemplateVersion = PhaseReady, "", "", p.cfg.Version
		})
	}
	return p.waitBuild(ctx, id, last.BuildID, failed)
}

func imageProblem(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range []string{"manifest unknown", "pull access denied", "failed to pull", "image not found", "no such image", "not found: manifest"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// ---- sandbox lifecycle ----

func (p *Provider) notReady() error {
	st := p.Status()
	switch st.Phase {
	case PhaseReady:
		return nil
	case PhaseFailed:
		code := st.ErrorCode
		if code == "" {
			code = CodeSandboxFailed
		}
		return &Error{Code: code, Message: st.Error}
	case PhasePreparing:
		return &Error{Code: CodeNotReady, Message: "the E2B computer is still being prepared"}
	}
	return &Error{Code: CodeNotReady, Message: "E2B is not set up"}
}

func (p *Provider) hostURL(port int, sandboxID, domain string) (string, error) {
	if !idPattern.MatchString(sandboxID) {
		return "", &Error{Code: CodeSandboxFailed, Message: "E2B sent an invalid sandbox id"}
	}
	if !validDomain(domain) || (p.pinDomain && !e2bDomain(domain)) {
		return "", &Error{Code: CodeSandboxFailed, Message: "E2B sent an invalid domain"}
	}
	return strings.TrimRight(p.cfg.HostURL(port, sandboxID, domain), "/"), nil
}

func (p *Provider) remember(info sandboxInfo) error {
	dom := info.domain()
	if !validDomain(dom) {
		return &Error{Code: CodeSandboxFailed, Message: "E2B sent an invalid domain"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if info.EnvdAccessToken != "" {
		p.envdToken = info.EnvdAccessToken
	}
	if t := info.traffic(); t != "" {
		p.trafficTok = t
	}
	p.domain = dom
	p.lastRefresh = p.cfg.Now()
	return nil
}

func (p *Provider) forget() {
	p.mu.Lock()
	var d time.Duration
	if p.running {
		d = p.endUsageLocked(p.cfg.Now(), false)
	}
	p.running, p.envdToken, p.trafficTok, p.domain, p.infoOK = false, "", "", "", false
	p.mu.Unlock()
	p.reportUsage(d)
}

type creds struct{ sandbox, domain, traffic, envd, token string }

func (p *Provider) creds() creds {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.domain
	if d == "" {
		d = defaultDomain
	}
	return creds{p.state.SandboxID, d, p.trafficTok, p.envdToken, p.token}
}

func (p *Provider) loadToken() (string, error) {
	p.mu.Lock()
	t := p.token
	p.mu.Unlock()
	if t != "" {
		return t, nil
	}
	t, err := p.cfg.Store.GuestToken()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	p.token = t
	p.mu.Unlock()
	return t, nil
}

func (p *Provider) newToken() (string, error) {
	t := randomHex(32)
	if err := p.cfg.Store.SaveGuestToken(t); err != nil {
		return "", &Error{Code: CodeSandboxFailed, Message: "could not save the guest token"}
	}
	p.mu.Lock()
	p.token = t
	p.mu.Unlock()
	return t, nil
}

// provisionToken writes the guest token into the sandbox and proves the guest
// can read it with one authenticated call. Caller holds ensureMu or is creating.
func (p *Provider) provisionToken(ctx context.Context, token string) error {
	for _, user := range []string{"root", guestTokenFallbackUser} {
		if err := p.uploadToken(ctx, token, user); err != nil {
			return err
		}
		missing, err := p.tokenMissing(ctx)
		if err != nil {
			return err
		}
		if !missing {
			return nil
		}
	}
	return &Error{Code: CodeTokenUnreadable, Message: "the computer cannot read its access token"}
}

// tokenMissing asks the guest an authenticated question: true when it answers
// that it has no readable token.
func (p *Provider) tokenMissing(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := p.guestRequest(ctx, http.MethodGet, "/v1/info", nil)
	if err != nil {
		return false, err
	}
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		return false, &Error{Code: CodeGuestUnreachable, Message: "the computer did not answer"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusServiceUnavailable && bytes.Contains(body, []byte("token_missing")) {
		return true, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return false, &Error{Code: CodeGuestUnreachable, Message: "the computer refused its access token"}
	}
	return false, nil
}

// uploadToken writes the guest token into the sandbox through envd, as user.
func (p *Provider) uploadToken(ctx context.Context, token, user string) error {
	c := p.creds()
	base, err := p.hostURL(envdPort, c.sandbox, c.domain)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()
	target := base + "/files?path=" + url.QueryEscape(guestTokenPath) + "&username=" + url.QueryEscape(user)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(token))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Access-Token", c.envd)
	if c.traffic != "" {
		req.Header.Set("e2b-traffic-access-token", c.traffic)
	}
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return &Error{Code: CodeGuestUnreachable, Message: "the sandbox did not accept the guest token"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Code: CodeGuestUnreachable, Status: resp.StatusCode, Message: p.api.scrub(fmt.Sprintf("the sandbox refused the guest token (%d) %s", resp.StatusCode, firstLine(raw)))}
	}
	return nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// guestRequest builds an authenticated request to the guest port.
func (p *Provider) guestRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	c := p.creds()
	if c.sandbox == "" {
		return nil, &Error{Code: CodeGuestUnreachable, Message: "no sandbox"}
	}
	base, err := p.hostURL(p.cfg.GuestPort, c.sandbox, c.domain)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.traffic != "" {
		req.Header.Set("e2b-traffic-access-token", c.traffic)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (p *Provider) guestHealthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := p.guestRequest(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return false
	}
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}

func (p *Provider) waitHealthy(ctx context.Context) error {
	deadline := time.Now().Add(p.healthWait)
	for {
		if p.guestHealthy(ctx) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return &Error{Code: CodeGuestUnreachable, Message: "the guest did not answer in time"}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.healthEvery):
		}
	}
}

func (p *Provider) killQuietly(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = p.api.kill(ctx, id)
}

// create makes a new sandbox. Caller holds ensureMu.
func (p *Provider) create(ctx context.Context) error {
	p.mu.Lock()
	p.creating = true
	p.infoOK = false
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.creating = false
		p.infoOK = false
		p.mu.Unlock()
	}()
	st := p.Status()
	version := templateIdentity(st)
	if version == "" {
		version = p.cfg.Version
	}
	token, err := p.newToken()
	if err != nil {
		return err
	}
	info, err := p.api.createSandbox(ctx, st.TemplateID, p.idleTimeout(), randomHex(16), p.cfg.SandboxMetadata)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && (e.Code == CodeInvalidKey || e.Code == CodeSandboxFailed) {
			p.fail(e.Code, e.Message)
		}
		return err
	}
	if !idPattern.MatchString(info.SandboxID) {
		return &Error{Code: CodeSandboxFailed, Message: "E2B sent an invalid sandbox id"}
	}
	if err := p.remember(info); err != nil {
		p.killQuietly(info.SandboxID)
		return err
	}
	p.mu.Lock()
	p.state.SandboxID = info.SandboxID // visible to creds(); persisted below
	p.mu.Unlock()
	abort := func(err error) error {
		p.killQuietly(info.SandboxID)
		p.forget()
		_ = p.update(func(s *State) { s.SandboxID, s.SandboxVersion = "", "" })
		var e *Error
		if errors.As(err, &e) && e.Code == CodeGuestUnreachable {
			p.fail(e.Code, e.Message)
		}
		return err
	}
	if err := p.update(func(s *State) { s.SandboxID, s.SandboxVersion = info.SandboxID, version }); err != nil {
		return abort(&Error{Code: CodeSandboxFailed, Message: "could not save the sandbox id"})
	}
	p.setRunning(true)
	if err := p.waitHealthy(ctx); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return abort(err)
	}
	if err := p.provisionToken(ctx, token); err != nil {
		if ctx.Err() != nil {
			return err
		}
		var e *Error
		if errors.As(err, &e) && e.Code == CodeTokenUnreadable {
			p.fail(e.Code, e.Message)
		}
		return abort(err)
	}
	return nil
}

// ensure makes the sandbox running and reachable. It never runs concurrently
// with itself, so parallel actions cannot create two sandboxes.
func (p *Provider) ensure(ctx context.Context) error {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	if err := p.RefreshTemplate(ctx); err != nil {
		return err
	}
	if err := p.notReady(); err != nil {
		return err
	}
	st := p.Status()
	if st.SandboxID == "" {
		return p.create(ctx)
	}
	if st.GuestOutdatedRequired {
		return guestOutdatedRequired()
	}
	p.mu.Lock()
	running, last := p.running, p.lastRefresh
	p.mu.Unlock()
	if running {
		if p.cfg.Now().Sub(last) < 60*time.Second {
			return nil
		}
		err := p.api.setTimeout(ctx, st.SandboxID, p.idleTimeout())
		if err == nil {
			p.mu.Lock()
			p.lastRefresh = p.cfg.Now()
			p.mu.Unlock()
			p.accrueUsage()
			return nil
		}
		var e *Error
		if errors.As(err, &e) && e.Code == CodeInvalidKey {
			return err
		}
		// E2B auto-paused it after the idle timeout: a paused sandbox is not in the running set and the timeout call
		// answers 404. Connect below resumes it; only a 404 from connect means the sandbox is gone.
		p.setRunning(false)
	}
	info, err := p.api.connect(ctx, st.SandboxID, p.idleTimeout())
	if isNotFound(err) {
		return p.gone(st.SandboxID)
	}
	if err != nil {
		return err
	}
	if info.SandboxID == "" {
		info.SandboxID = st.SandboxID
	}
	if info.SandboxID != st.SandboxID {
		return &Error{Code: CodeSandboxFailed, Message: "E2B answered for another sandbox"}
	}
	if err := p.remember(info); err != nil {
		return err
	}
	if tok, err := p.loadToken(); err != nil {
		return &Error{Code: CodeSandboxFailed, Message: "could not read the guest token"}
	} else if tok == "" {
		tok, err := p.newToken()
		if err != nil {
			return err
		}
		p.setRunning(true)
		if err := p.provisionToken(ctx, tok); err != nil {
			p.setRunning(false)
			return err
		}
	}
	p.setRunning(true)
	return nil
}

func guestOutdatedRequired() error {
	return &Error{Code: CodeGuestOutdatedRequired, Message: "this computer runs an image that is too old; upgrading replaces it and removes the files on it"}
}

// gone records that the stored sandbox no longer exists on E2B. Its disk went
// with it, so no new sandbox is made behind the person's back: the computer
// shows sandbox_gone and Retry makes a new one.
func (p *Provider) gone(id string) error {
	p.forget()
	msg := "the cloud computer no longer exists on E2B; retry makes a new one (its files are gone)"
	_ = p.update(func(s *State) {
		if s.SandboxID == id {
			s.SandboxID, s.SandboxVersion = "", ""
		}
		s.Phase, s.ErrorCode, s.Error = PhaseFailed, CodeSandboxGone, msg
	})
	return &Error{Code: CodeSandboxGone, Message: msg}
}

// Pause snapshots the sandbox. A sandbox that is already paused is fine.
func (p *Provider) Pause(ctx context.Context) error {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	id := p.Status().SandboxID
	if id == "" {
		return nil
	}
	err := p.api.pause(ctx, id)
	var e *Error
	if err != nil && !(errors.As(err, &e) && e.Status == http.StatusConflict) && !isNotFound(err) {
		return err
	}
	p.endUsage(true)
	p.setRunning(false)
	return nil
}

// Remove kills the sandbox, clears its id and forgets the guest token.
func (p *Provider) Remove(ctx context.Context) error {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	return p.removeLocked(ctx)
}

// Upgrade replaces a sandbox of an older image with one of the template that is
// ready now. It destroys the files on the old one, so it runs only when the
// person asked (the consent model of sandbox_gone then Retry), and only when the
// new template is ready: the old computer is never removed for one that cannot
// be made.
func (p *Provider) Upgrade(ctx context.Context) error {
	if err := p.RefreshTemplate(ctx); err != nil {
		return err
	}
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	st := p.Status()
	if st.SandboxID == "" || !st.GuestOutdated {
		return &Error{Code: CodeNotOutdated, Message: "this computer already runs the current image"}
	}
	if err := p.removeLocked(ctx); err != nil {
		return err
	}
	return p.create(ctx)
}

func (p *Provider) removeLocked(ctx context.Context) error {
	if id := p.Status().SandboxID; id != "" {
		if err := p.api.kill(ctx, id); err != nil && !isNotFound(err) {
			return err
		}
	}
	p.endUsage(true)
	p.forget()
	if err := p.update(func(s *State) { s.SandboxID, s.SandboxVersion = "", "" }); err != nil {
		return err
	}
	p.mu.Lock()
	p.token = ""
	p.mu.Unlock()
	return p.cfg.Store.SaveGuestToken("")
}

// Retry clears a sandbox problem (a sandbox whose guest does not answer is
// killed) and then brings the computer up the way a Bot call does: a paused
// sandbox resumes, a missing one is created. The server's wake route (the
// person opened the computer window on an asleep computer) relies on that.
func (p *Provider) Retry(ctx context.Context) error {
	if err := p.RefreshTemplate(ctx); err != nil {
		return err
	}
	if err := p.clearProblem(ctx); err != nil {
		return err
	}
	if p.Status().Phase != PhaseReady {
		return nil
	}
	return p.ensure(ctx)
}

func (p *Provider) clearProblem(ctx context.Context) error {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	p.setRunning(false)
	st := p.Status()
	if st.SandboxID != "" {
		unreachable := false
		info, err := p.api.getSandbox(ctx, st.SandboxID)
		switch {
		case isNotFound(err):
			unreachable = true
		case err == nil && info.State == "running":
			// Refresh the traffic and envd tokens, then probe the guest.
			if cinfo, cerr := p.api.connect(ctx, st.SandboxID, p.idleTimeout()); cerr == nil && p.remember(cinfo) == nil {
				p.setRunning(true)
				unreachable = !p.guestHealthy(ctx)
				p.setRunning(false)
			}
		}
		if unreachable {
			if err := p.api.kill(ctx, st.SandboxID); err != nil && !isNotFound(err) {
				return err
			}
			p.forget()
			_ = p.update(func(s *State) { s.SandboxID, s.SandboxVersion = "", "" })
		}
	}
	if st.Phase == PhaseFailed && st.TemplateID != "" && st.TemplateVersion != "" &&
		(st.ErrorCode == CodeSandboxFailed || st.ErrorCode == CodeGuestUnreachable || st.ErrorCode == CodeInvalidKey || st.ErrorCode == CodeUnreachable || st.ErrorCode == CodeLimit || st.ErrorCode == CodeSandboxGone || st.ErrorCode == CodeTokenUnreadable) {
		return p.update(func(s *State) { s.Phase, s.ErrorCode, s.Error = PhaseReady, "", "" })
	}
	return nil
}

func (p *Provider) Recover(ctx context.Context) error { return p.Retry(ctx) }

// Purge removes the sandbox; the next use creates a fresh one.
func (p *Provider) Purge(ctx context.Context) (computer.Info, error) {
	if err := p.Remove(ctx); err != nil {
		return computer.Info{}, err
	}
	return p.Info(ctx)
}

// ---- info and health ----

const infoTTL = 5 * time.Second

func (p *Provider) Info(ctx context.Context) (computer.Info, error) {
	p.mu.Lock()
	if p.infoOK && p.cfg.Now().Sub(p.infoAt) < infoTTL {
		out := p.info
		p.mu.Unlock()
		return out, nil
	}
	creating, running := p.creating, p.running
	p.mu.Unlock()
	if err := p.RefreshTemplate(ctx); err != nil && ctx.Err() != nil {
		return computer.Info{}, ctx.Err()
	}
	p.mu.Lock()
	st := p.statusLocked()
	p.mu.Unlock()

	out := computer.Info{Kind: "e2b", ID: p.cfg.ID, Phase: st.Phase, Hibernation: true, WorkspaceRoot: "/workspace", GuestOutdated: st.GuestOutdated || st.GuestOutdatedRequired, GuestOutdatedRequired: st.GuestOutdatedRequired}
	cache := true
	switch {
	case creating || st.Phase == PhasePreparing:
		out.State = "starting"
	case st.Phase == PhaseFailed:
		out.State, out.Error, out.ErrorCode = "error", p.scrubForView(st.Error), st.ErrorCode
	case st.Phase != PhaseReady:
		out.State = "stopped"
	case st.SandboxID == "":
		out.State = computer.StateHibernated
	default:
		detail, err := p.api.getSandbox(ctx, st.SandboxID)
		switch {
		case isNotFound(err):
			gerr := p.gone(st.SandboxID)
			out.State, out.Error = "error", gerr.(*Error).Message
		case err != nil:
			out.State, out.Error = "error", p.scrubForView(describe(err))
			cache = false
		case detail.State == "running":
			out.State = "ready"
			if running {
				p.mergeGuestInfo(ctx, &out)
			}
		default:
			out.State = computer.StateHibernated
			p.setRunning(false)
		}
	}
	if st.GuestOutdatedRequired && out.State != "starting" {
		out.State, out.Error, out.ErrorCode = "error", guestOutdatedRequired().(*Error).Message, CodeGuestOutdatedRequired
	}
	if cache {
		p.mu.Lock()
		p.info, p.infoAt, p.infoOK = out, p.cfg.Now(), true
		p.mu.Unlock()
	}
	return out, nil
}

// scrubForView cleans a stored message for what a person sees: the key and, for
// the organization's computer, its template and build ids.
func (p *Provider) scrubForView(s string) string {
	if s == "" {
		return s
	}
	return p.api.scrub(s)
}

func (p *Provider) mergeGuestInfo(ctx context.Context, out *computer.Info) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := p.guestRequest(ctx, http.MethodGet, "/v1/info", nil)
	if err != nil {
		return
	}
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var g computer.Info
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&g) != nil {
		return
	}
	if g.WorkspaceRoot != "" {
		out.WorkspaceRoot = g.WorkspaceRoot
	}
	out.Browser = g.Browser
	out.VCPUs, out.MemoryMiB, out.DiskGiB = g.VCPUs, g.MemoryMiB, g.DiskGiB
}

func (p *Provider) Health(ctx context.Context) (computer.GuestHealth, error) {
	info, err := p.Info(ctx)
	if err != nil {
		return computer.GuestHealth{}, err
	}
	h := computer.GuestHealth{State: info.State, Guest: "not_ready"}
	p.mu.Lock()
	running := p.running
	p.mu.Unlock()
	if info.State == "ready" && running {
		if p.guestHealthy(ctx) {
			h.Guest = "ok"
		} else {
			h.Guest = "unresponsive"
		}
	}
	return h, nil
}

// ---- guest calls ----

func (p *Provider) Action(ctx context.Context, action computer.Action) (computer.ActionResult, error) {
	return p.ActionWithin(ctx, action, 0)
}

func (p *Provider) ActionWithin(ctx context.Context, action computer.Action, limit time.Duration) (computer.ActionResult, error) {
	action, err := computer.PrepareAction(action)
	if err != nil {
		return computer.ActionResult{}, err
	}
	if err := p.ensure(ctx); err != nil {
		return computer.ActionResult{}, err
	}
	payload, err := json.Marshal(action)
	if err != nil {
		return computer.ActionResult{}, err
	}
	var out computer.ActionResult
	if err := p.sendAction(ctx, action, payload, limit, &out); err != nil {
		return out, err
	}
	return computer.FinishActionResult(out)
}

func (p *Provider) sendAction(ctx context.Context, action computer.Action, payload []byte, limit time.Duration, out *computer.ActionResult) error {
	callCtx := ctx
	if limit > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	reuploaded := false
	for {
		req, err := p.guestRequest(callCtx, http.MethodPost, "/v1/action", payload)
		if err != nil {
			return err
		}
		resp, err := p.cfg.HTTP.Do(req)
		if err != nil {
			p.setRunning(false)
			if err2 := computer.ActionConnectionError(ctx, action, err); err2 != nil {
				return err2
			}
			if limit > 0 && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
				return &computer.ActionDeadlineError{Limit: limit}
			}
			if ctx.Err() != nil {
				return err
			}
			return &Error{Code: CodeGuestUnreachable, Message: "the computer did not answer"}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("invalid computer control response: %w", err)
			}
			return nil
		}
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		if resp.StatusCode == http.StatusServiceUnavailable && !reuploaded && bytes.Contains(message, []byte("token_missing")) {
			// The guest refused before running anything, so one retry is safe.
			reuploaded = true
			token, terr := p.loadToken()
			if terr == nil && token == "" {
				token, terr = p.newToken()
			}
			if terr == nil {
				terr = p.provisionToken(callCtx, token)
			}
			if terr != nil {
				return terr
			}
			continue
		}
		if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusServiceUnavailable {
			p.setRunning(false) // the sandbox may have paused or ended; reconnect next time
		}
		return computer.ActionStatusError(resp.Status, []byte(p.api.scrub(string(message))), true)
	}
}

// DesktopStreamWithCursor opens the guest's observer stream, resuming the sandbox first.
func (p *Provider) DesktopStreamWithCursor(ctx context.Context, botID, cursor string) (*http.Response, error) {
	if !streamBotID.MatchString(botID) {
		return nil, fmt.Errorf("invalid bot_id")
	}
	if cursor != "" && cursor != "hidden" {
		return nil, fmt.Errorf("invalid cursor")
	}
	if err := p.ensure(ctx); err != nil {
		return nil, err
	}
	path := "/v1/desktop/stream?bot_id=" + url.QueryEscape(botID)
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	req, err := p.guestRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	hc := *p.cfg.HTTP
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		p.setRunning(false)
		return nil, &Error{Code: CodeGuestUnreachable, Message: "the computer did not answer"}
	}
	if resp.StatusCode == http.StatusOK && resp.Header.Get("Content-Type") != "video/mp4" {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("invalid desktop stream content type")
	}
	return resp, nil
}
