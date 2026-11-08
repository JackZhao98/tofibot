package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/e2b"
)

// Environments are the places a Bot works (docs/design/environments.md). An
// account has at most one Bot computer, which every Bot uses with the full tool
// set: the VM on this server or an E2B cloud computer the person added with
// their own key. Paired Macs are extra environments, decided on the Mac.
//
// The Bot computer always sits in the VM slot under computer.DefaultID, whatever
// its kind, so the routes, the shared-desktop lease, recovery identities and the
// web app's /api/computers/firecracker/* calls work unchanged for an E2B computer.

const (
	envKindVM  = "vm"
	envKindE2B = "e2b"
	envKindMac = "mac"

	// e2bEnvironmentID is the E2B environment in the API. Its provider still
	// takes the Bot computer slot id when it is the Bot computer.
	e2bEnvironmentID = "e2b"

	computerProviderKind = "computer_provider"
	e2bKeyVaultID        = "computer:e2b"
	e2bGuestVaultID      = "computer:e2b:guest"

	// Non-secret state: one JSON value each in the account store.
	e2bStateKey    = "environments.e2b"
	botComputerKey = "environments.bot_computer"

	ComputerBackendE2B = "e2b"

	e2bPrepareTimeout   = 45 * time.Minute
	e2bDefaultImageRepo = "ghcr.io/jackzhao98/tofi-worker"
	e2bMaxKeyBytes      = 512
)

var e2bTemplateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// e2bCapabilities: the Guest serves everything the VM does except the size
// controls (E2B sizes come from the template), the in-guest MCP Runner (the
// Runner is its own container) and the vsock OAuth bridge.
var e2bCapabilities = []string{CapShell, CapFiles, CapBrowser, CapDesktop, CapDesktopStream, CapTerminal, CapHibernate, CapSecretSink}

// e2bEnvironment is the runtime state of the account's E2B computer.
type e2bEnvironment struct {
	mu       sync.Mutex
	provider *e2b.Provider
	// preparing is the provider whose template build runs now; cancel stops it
	// when a new key or a removal replaces that provider.
	preparing *e2b.Provider
	cancel    context.CancelFunc
	// orgProvider is the organization's E2B computer under provided/e2b (org_e2b.go).
	orgProvider *e2b.Provider
}

// stopE2BPrepare cancels a running template build. Caller holds e2bEnv.mu.
func (e *e2bEnvironment) stopPrepareLocked() {
	if e.cancel != nil {
		e.cancel()
	}
	e.preparing, e.cancel = nil, nil
}

// ---- secrets and stored state ----

func (s *Server) vaultValue(id string) (string, secretRecord, bool) {
	v := s.secretVault
	if v == nil {
		return "", secretRecord{}, false
	}
	v.mu.Lock()
	rec, ok := v.records[id]
	v.mu.Unlock()
	if !ok || rec.Kind != computerProviderKind {
		return "", secretRecord{}, false
	}
	value, err := v.reveal(rec)
	if err != nil || value == "" {
		return "", secretRecord{}, false
	}
	return value, rec, true
}

func (s *Server) storeVaultValue(id, name, label, value string) error {
	v := s.secretVault
	if v == nil {
		return errors.New("secret storage unavailable")
	}
	rec := secretRecord{ID: id, Name: name, Kind: computerProviderKind, Target: envKindE2B, Label: label, Status: "stored", CreatedAt: now()}
	var err error
	if rec.Ciphertext, err = v.seal(id, value); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	prev, existed := v.records[id]
	v.records[id] = rec
	if err = v.saveLocked(); err != nil {
		if existed {
			v.records[id] = prev
		} else {
			delete(v.records, id)
		}
		return err
	}
	return nil
}

func (s *Server) deleteVaultValue(id string) error {
	v := s.secretVault
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	prev, existed := v.records[id]
	if !existed {
		return nil
	}
	delete(v.records, id)
	if err := v.saveLocked(); err != nil {
		v.records[id] = prev
		return err
	}
	return nil
}

// e2bStore keeps the provider's state: sealed tokens in the vault, the rest in
// the account store.
type e2bStore struct{ s *Server }

func (st e2bStore) LoadState() (e2b.State, error) {
	raw, ok := st.s.autoStored(e2bStateKey)
	if !ok || strings.TrimSpace(raw) == "" {
		return e2b.State{Phase: "none"}, nil
	}
	var v e2b.State
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return e2b.State{Phase: "none"}, nil
	}
	return v, nil
}

func (st e2bStore) SaveState(v e2b.State) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	value := string(b)
	return st.s.saveAutomationValue(e2bStateKey, &value)
}

func (st e2bStore) GuestToken() (string, error) {
	value, _, _ := st.s.vaultValue(e2bGuestVaultID)
	return value, nil
}

func (st e2bStore) SaveGuestToken(token string) error {
	if token == "" {
		return st.s.deleteVaultValue(e2bGuestVaultID)
	}
	return st.s.storeVaultValue(e2bGuestVaultID, "E2B computer access token", "", token)
}

// storedBotComputerChoice reads the person's choice before the server exists
// (start-up decides whether the VM is attached).
func storedBotComputerChoice(st *Store) string {
	values, err := st.automationValues()
	if err != nil {
		return ""
	}
	return values[botComputerKey]
}

func (s *Server) botComputerChoice() string {
	v, _ := s.autoStored(botComputerKey)
	return v
}

func (s *Server) saveBotComputerChoice(kind string) {
	var value *string
	if kind != "" {
		value = &kind
	}
	if err := s.saveAutomationValue(botComputerKey, value); err != nil {
		log.Printf("[environments] could not save the Bot computer choice: %v", err)
	}
}

// ---- provider ----

// e2bImage is the Guest image the E2B template is built from. Source builds
// have none unless TOFI_E2B_IMAGE names one.
func e2bImage() string {
	if v := strings.TrimSpace(os.Getenv("TOFI_E2B_IMAGE")); v != "" {
		return v
	}
	if Version == "" || Version == "dev" {
		return ""
	}
	return e2bDefaultImageRepo + ":" + Version + "-e2b"
}

// e2bMinGuestVersion is the oldest guest image a sandbox may run
// (TOFI_E2B_MIN_GUEST_VERSION); empty, the default, enforces nothing. A sandbox
// below it is refused (guest_outdated_required) until its owner upgrades it.
func e2bMinGuestVersion() string { return strings.TrimSpace(os.Getenv("TOFI_E2B_MIN_GUEST_VERSION")) }

func e2bAPIBase() string { return strings.TrimSpace(os.Getenv("TOFI_E2B_API_BASE")) }

func e2bTemplateVersion() string {
	if v := strings.TrimSpace(os.Getenv("TOFI_E2B_IMAGE")); v != "" && (Version == "" || Version == "dev") {
		// A custom image on a source build: one template per image reference.
		return "custom-" + v
	}
	return Version
}

func (s *Server) newE2BProvider(key string) (*e2b.Provider, error) {
	return e2b.New(e2b.Config{
		ID:              computer.DefaultID,
		APIKey:          key,
		APIBase:         e2bAPIBase(),
		Image:           e2bImage(),
		Version:         e2bTemplateVersion(),
		MinGuestVersion: e2bMinGuestVersion(),
		Store:           e2bStore{s},
		IdleTimeout:     s.e2bIdleTimeout(),
		HostURL:         e2bHostURL,
		Now:             e2bNow,
	})
}

// autoComputerIdleOff is how many idle minutes an E2B computer runs before E2B
// turns it off (pauses it: files kept, billing stops). E2B's Hobby plan caps a
// sandbox's timeout at one hour, so the setting stops there.
const (
	autoComputerIdleOff        = "computer_idle_off_minutes"
	defaultComputerIdleOffMins = 15
)

func (s *Server) e2bIdleTimeout() time.Duration {
	return time.Duration(s.autoInt(autoComputerIdleOff)) * time.Minute
}

// applyE2BIdleTimeout hands a changed setting to the E2B computer: a running
// sandbox gets the new timeout now, a paused one when it resumes.
func (s *Server) applyE2BIdleTimeout() {
	s.e2bEnv.mu.Lock()
	providers := []*e2b.Provider{s.e2bEnv.provider, s.e2bEnv.orgProvider}
	s.e2bEnv.mu.Unlock()
	d := s.e2bIdleTimeout()
	for _, p := range providers {
		if p == nil {
			continue
		}
		go func(p *e2b.Provider) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := p.SetIdleTimeout(ctx, d); err != nil {
				log.Printf("[environments] E2B idle timeout: %s", e2bErrorCode(err))
			}
		}(p)
	}
}

// computerSleeper is a computer the person can turn off from its power button:
// today the E2B computer, whose off is E2B's pause.
type computerSleeper interface {
	Pause(ctx context.Context) error
}

// computerUpgrader is a computer whose image can be replaced by the person's
// explicit action (the E2B computer: Upgrade removes the sandbox and its files and
// makes a new one from the current template).
type computerUpgrader interface {
	Upgrade(ctx context.Context) error
}

// canSleep reports that the attached computer has a power button.
func canSleep(p computer.Provider) bool {
	_, ok := p.(computerSleeper)
	return ok && p.Kind() == envKindE2B
}

// e2bCurrent returns the provider for the stored key, built once.
func (s *Server) e2bCurrent() *e2b.Provider {
	s.e2bEnv.mu.Lock()
	defer s.e2bEnv.mu.Unlock()
	if s.e2bEnv.provider != nil {
		return s.e2bEnv.provider
	}
	key, _, ok := s.vaultValue(e2bKeyVaultID)
	if !ok {
		return nil
	}
	p, err := s.newE2BProvider(key)
	if err != nil {
		log.Printf("[environments] E2B provider: %v", err)
		return nil
	}
	s.e2bEnv.provider = p
	return p
}

func (s *Server) botComputerIsE2B() bool {
	cur := s.computer()
	return cur != nil && cur.Kind() == envKindE2B
}

// startE2BPrepare builds the template in the background. With attach set, the
// computer becomes the Bot computer when it is ready, if there is none by then.
func (s *Server) startE2BPrepare(p *e2b.Provider, attach bool) {
	s.e2bEnv.mu.Lock()
	if s.e2bEnv.preparing == p {
		s.e2bEnv.mu.Unlock()
		return
	}
	s.e2bEnv.stopPrepareLocked()
	ctx, cancel := context.WithTimeout(context.Background(), e2bPrepareTimeout)
	s.e2bEnv.preparing, s.e2bEnv.cancel = p, cancel
	s.e2bEnv.mu.Unlock()
	go func() {
		defer func() {
			s.e2bEnv.mu.Lock()
			if s.e2bEnv.preparing == p {
				s.e2bEnv.preparing, s.e2bEnv.cancel = nil, nil
			}
			s.e2bEnv.mu.Unlock()
			cancel()
			s.touchWorkspace()
		}()
		if err := p.Prepare(ctx); err != nil {
			log.Printf("[environments] E2B template: %s", e2bErrorCode(err))
			return
		}
		s.e2bEnv.mu.Lock()
		current := s.e2bEnv.provider == p
		s.e2bEnv.mu.Unlock()
		if !current {
			return // removed or replaced while building
		}
		if attach && s.computer() == nil {
			if err := s.attachProvider(p); err != nil {
				log.Printf("[environments] attach E2B computer: %v", err)
				return
			}
			s.saveBotComputerChoice(envKindE2B)
		}
	}()
}

func e2bErrorCode(err error) string {
	var e *e2b.Error
	if errors.As(err, &e) && e.Code != "" {
		return e.Code
	}
	return "e2b_unreachable"
}

// attachProvider puts a ready provider into the Bot computer slot. AttachVM is
// the VM's own path (it builds its provider from the control socket).
func (s *Server) attachProvider(p computer.Provider) error {
	s.computerMu.Lock()
	if s.microVM != nil {
		s.computerMu.Unlock()
		return errComputerAttached
	}
	s.microVM = p
	if s.computerSlots == nil {
		s.computerSlots = map[string]*computerSlot{}
	}
	if old := s.computerSlots[p.ID()]; old != nil {
		old.forceFree()
	}
	s.computerSlots[p.ID()] = newComputerSlot(p, computer.RowKindVM, "")
	s.computerMu.Unlock()
	s.afterVMAttached(p)
	return nil
}

// restoreE2BEnvironment runs at start-up after the vault opens. preferE2B is
// the stored choice; start-up did not attach the VM when it is set.
func (s *Server) restoreE2BEnvironment(preferE2B bool) {
	if v := s.policy()[capComputer]; computerUsesOrgE2B(v) {
		// The organization's computer: attached whatever the stored choice says. A
		// missing organization key or a failed build shows as a named error on it.
		if s.computer() == nil {
			if err := s.attachOrgE2B(); err != nil {
				log.Printf("[environments] attach the organization E2B computer for account %s: %v", s.accountID, err)
			}
		}
		return
	} else if !computerAllowsOwnE2B(v) {
		// The policy gives this account no E2B computer of its own. A stored choice
		// of one must not leave a VM-allowed account without its VM.
		if preferE2B && computerAllowsVM(v) {
			s.fallBackToVM("the policy gives no computer of its own")
		}
		return
	}
	p := s.e2bCurrent()
	if p == nil {
		if preferE2B {
			s.fallBackToVM("its key is gone")
		}
		return
	}
	st := p.Status()
	switch {
	case st.Phase == e2b.PhasePreparing || st.Phase == e2b.PhaseNone:
		// A build cut short by the restart: run it again.
		s.startE2BPrepare(p, preferE2B)
	case preferE2B && s.computer() == nil:
		if st.TemplateID == "" {
			s.fallBackToVM("it has no template")
			return
		}
		// A failed computer is attached too: its error shows and Retry reaches it.
		if err := s.attachProvider(p); err != nil {
			s.fallBackToVM(err.Error())
		}
	}
}

// fallBackToVM gives the account its VM back when the chosen E2B computer
// cannot be restored, so a restart never leaves the account without one.
func (s *Server) fallBackToVM(reason string) {
	log.Printf("[environments] the E2B computer could not be restored (%s); using the VM", reason)
	s.saveBotComputerChoice("")
	if s.computerFactory != nil && s.computer() == nil {
		if err := s.AttachVM(context.Background()); err != nil && !errors.Is(err, errComputerAttached) {
			log.Printf("[environments] attach VM: %v", err)
		}
	}
}

// ---- API ----

type environmentItem struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	ErrorCode   string   `json:"error_code"`
	Platform    string   `json:"platform,omitempty"`
	LastSeen    string   `json:"last_seen,omitempty"`
	BotComputer bool     `json:"bot_computer"`
	Access      string   `json:"access"`
	Actions     []string `json:"actions"`
	// GuestOutdated: the computer runs an older image than the current one. It
	// works; the "upgrade" action replaces it and removes its files.
	GuestOutdated bool `json:"guest_outdated,omitempty"`
}

type environmentsAdd struct {
	E2B       bool   `json:"e2b"`
	Mac       bool   `json:"mac"`
	VMCommand string `json:"vm_command,omitempty"`
}

type e2bView struct {
	Configured bool   `json:"configured"`
	KeyHint    string `json:"key_hint,omitempty"`
	Phase      string `json:"phase"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
	// TemplatePhase is the image's own phase (none, preparing, ready, failed);
	// Phase is the computer's. An image being built blocks only a new computer.
	TemplatePhase string `json:"template_phase,omitempty"`
	GuestOutdated bool   `json:"guest_outdated,omitempty"`
}

type environmentsView struct {
	Environments []environmentItem `json:"environments"`
	Add          environmentsAdd   `json:"add"`
	E2B          e2bView           `json:"e2b"`
	// Policy is the account's computer policy: whose Bot computer this is.
	Policy capabilityView `json:"policy"`
}

// environmentState maps a computer state to the words the page shows.
func environmentState(state string) string {
	switch state {
	case "ready":
		return "ready"
	case "starting", "restarting":
		return "starting"
	case computer.StateResuming:
		return "resuming"
	case computer.StateHibernating, computer.StateHibernated:
		return "asleep"
	case "stopped":
		return "stopped"
	case "error":
		return "failed"
	}
	return "unknown"
}

func (s *Server) accountAdmin() bool {
	if s.accountIsAdmin == nil {
		return true // a single-account install: the owner runs the host
	}
	return s.accountIsAdmin()
}

func (s *Server) environmentsView(ctx context.Context) environmentsView {
	out := environmentsView{Environments: []environmentItem{}, Policy: s.capabilitiesView()[capComputer]}
	if computerUsesOrgE2B(s.policy()[capComputer]) {
		// The organization's computer is the only one listed: no own key, no choice, no add.
		s.orgE2BEnvironmentView(ctx, &out)
		s.appendPairedMacs(ctx, &out)
		out.Add.Mac = s.personalComputers
		return out
	}
	cur := s.computer()

	// The VM: attached, or set aside by the person for the E2B computer.
	vmAttached := cur != nil && cur.Kind() != envKindE2B
	if vmAttached {
		item := environmentItem{ID: computer.DefaultID, Kind: envKindVM, State: "unknown", BotComputer: true, Access: "all_bots", Actions: []string{}}
		if info, err := s.microVMInfo(ctx); err == nil {
			item.State = environmentState(info.State)
		}
		if item.State == "failed" || item.State == "stopped" {
			item.Actions = append(item.Actions, "retry")
		}
		if _, ok := cur.(computer.Resourcer); ok {
			item.Actions = append(item.Actions, "configure")
		}
		out.Environments = append(out.Environments, item)
	} else if s.computerFactory != nil && s.botComputerChoice() == envKindE2B {
		// Set aside, not stopped: the person can put it back with Use for Bots.
		out.Environments = append(out.Environments, environmentItem{ID: computer.DefaultID, Kind: envKindVM, State: "standby", Access: "none", Actions: []string{"use"}})
	}

	// The E2B computer.
	p := s.e2bCurrent()
	if p != nil {
		st := p.Status()
		_, rec, _ := s.vaultValue(e2bKeyVaultID)
		s.e2bEnv.mu.Lock()
		preparing := s.e2bEnv.preparing == p
		s.e2bEnv.mu.Unlock()
		phase := st.Phase
		if preparing && phase != "ready" {
			phase = "preparing"
		}
		out.E2B = e2bView{Configured: true, KeyHint: rec.Label, Phase: phase, ErrorCode: st.ErrorCode, Error: st.Error}
		item := environmentItem{ID: e2bEnvironmentID, Kind: envKindE2B, State: "asleep", ErrorCode: st.ErrorCode, Access: "none", Actions: []string{}}
		switch {
		case phase == "preparing" || phase == "none":
			item.State = "preparing"
		case phase == "failed":
			item.State = "failed"
		case s.botComputerIsE2B():
			item.BotComputer, item.Access = true, "all_bots"
			if info, err := s.microVMInfo(ctx); err == nil {
				item.State = environmentState(info.State)
				if info.State == "error" {
					item.Actions = append(item.Actions, "retry")
				}
			}
		default:
			item.Actions = append(item.Actions, "use")
		}
		item.Actions = append(item.Actions, "remove")
		// The Bot computer is listed first.
		if item.BotComputer {
			out.Environments = append([]environmentItem{item}, out.Environments...)
		} else {
			out.Environments = append(out.Environments, item)
		}
	} else {
		out.E2B = e2bView{Phase: "none"}
	}

	s.appendPairedMacs(ctx, &out)

	out.Add.E2B = p == nil || out.E2B.Phase == "failed"
	if out.Add.E2B && e2bImage() == "" {
		out.Add.E2B = false // this build ships no E2B image
	}
	out.Add.Mac = s.personalComputers
	if cur == nil && s.computerFactory == nil && s.accountAdmin() {
		out.Add.VMCommand = "sudo tofi computer add kvm"
	}
	if v := s.policy()[capComputer]; !computerAllowsOwnE2B(v) {
		// Managed by the organization (or none): no way to add or remove a computer here.
		out.Add.E2B, out.Add.VMCommand = false, ""
		for i := range out.Environments {
			out.Environments[i].Actions = slices.DeleteFunc(out.Environments[i].Actions, func(a string) bool { return a == "use" || a == "remove" })
		}
	}
	return out
}

// appendPairedMacs lists the paired Macs (decided on the Mac, not by the policy).
func (s *Server) appendPairedMacs(ctx context.Context, out *environmentsView) {
	items, err := s.listComputers(ctx)
	if err != nil {
		return
	}
	for _, c := range items {
		if c.Kind != computer.RowKindPersonal {
			continue
		}
		state := "offline"
		if c.Online {
			state = "online"
		}
		out.Environments = append(out.Environments, environmentItem{ID: c.ID, Kind: envKindMac, Name: c.Name, State: state, Platform: c.Platform, LastSeen: c.LastSeen, Access: "allowed_on_mac", Actions: []string{"disconnect"}})
	}
}

func (s *Server) routeEnvironments(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "environments" && !strings.HasPrefix(p, "environments/") {
		return false
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(p, "environments"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return true
		}
		writeJSON(w, http.StatusOK, s.environmentsView(r.Context()))
	case len(parts) == 1 && parts[0] == e2bEnvironmentID && r.Method == http.MethodPut:
		s.putE2BKey(w, r)
	case len(parts) == 1 && parts[0] == e2bEnvironmentID && r.Method == http.MethodDelete:
		s.removeE2B(w, r)
	case len(parts) == 2 && parts[1] == "use" && r.Method == http.MethodPost:
		s.useEnvironment(w, r, parts[0])
	default:
		writeErr(w, http.StatusNotFound, "not_found", "no such environment route")
	}
	return true
}

// computerPolicyRefusal refuses a write to the person's own computers while the
// policy gives none (capability_off) or provides them (managed_by_organization).
func (s *Server) computerPolicyRefusal(w http.ResponseWriter) bool {
	switch v := s.policy()[capComputer]; {
	case computerAllowsOwnE2B(v):
		return false
	case v.Mode == policyModeProvided:
		refusePolicy(w, false)
	default:
		refusePolicy(w, true)
	}
	return true
}

func (s *Server) putE2BKey(w http.ResponseWriter, r *http.Request) {
	if s.computerPolicyRefusal(w) {
		return
	}
	var body struct {
		APIKey     string `json:"api_key"`
		TemplateID string `json:"template_id"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil || json.Unmarshal(raw, &body) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "send {\"api_key\": \"...\"}")
		return
	}
	key := strings.TrimSpace(body.APIKey)
	if key == "" || len(key) > e2bMaxKeyBytes || strings.ContainsAny(key, " \t\r\n") {
		writeErr(w, http.StatusBadRequest, "invalid_key", "That does not look like an E2B API key.")
		return
	}
	templateID := strings.TrimSpace(body.TemplateID)
	if templateID != "" && !e2bTemplateIDPattern.MatchString(templateID) {
		writeErr(w, http.StatusBadRequest, "invalid_template", "That does not look like an E2B template ID.")
		return
	}
	if templateID == "" && e2bImage() == "" {
		writeErr(w, http.StatusConflict, "image_missing", "This TOFI build has no E2B computer image.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := e2b.CheckKey(ctx, nil, e2bAPIBase(), key); err != nil {
		code := e2bErrorCode(err)
		status := http.StatusBadGateway
		msg := "E2B did not answer. Try again in a minute."
		switch code {
		case "invalid_key":
			status, msg = http.StatusBadRequest, "E2B rejected this key."
		case "e2b_limit":
			status, msg = http.StatusTooManyRequests, "E2B is limiting requests for this key. Try again in a minute."
		}
		writeErr(w, status, code, msg)
		return
	}
	// A computer in use is released first; it comes back with the new key once
	// its template is ready.
	wasBotComputer := s.botComputerIsE2B()
	if wasBotComputer {
		if _, err := s.DetachVM(r.Context(), detachWaitDefault); err != nil && !errors.Is(err, errComputerNone) {
			writeErr(w, http.StatusConflict, "busy", "The E2B computer could not be released. Try again.")
			return
		}
	}
	// A different key may belong to another E2B team, where the stored template
	// and sandbox do not exist; a different template makes the old sandbox
	// useless. Either way forget them (the old sandbox is killed with the old key).
	stored, _ := e2bStore{s}.LoadState()
	oldKey, _, hadKey := s.vaultValue(e2bKeyVaultID)
	if hadKey && (oldKey != key || (templateID != "" && templateID != stored.TemplateID) || (templateID == "" && stored.CustomTemplate)) {
		if old := s.e2bCurrent(); old != nil {
			rmCtx, rmCancel := context.WithTimeout(r.Context(), 30*time.Second)
			if err := old.Remove(rmCtx); err != nil {
				log.Printf("[environments] remove old E2B sandbox: %s", e2bErrorCode(err))
			}
			rmCancel()
		}
		_ = s.saveAutomationValue(e2bStateKey, nil)
		_ = s.deleteVaultValue(e2bGuestVaultID)
	}
	if err := s.storeVaultValue(e2bKeyVaultID, "E2B API key", keyHint(key), key); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "Could not save the key.")
		return
	}
	if templateID != "" {
		// A template the person built: no build here, and it is used as given.
		if cur, _ := (e2bStore{s}).LoadState(); cur.TemplateID != templateID || !cur.CustomTemplate {
			if err := (e2bStore{s}).SaveState(e2b.State{TemplateID: templateID, TemplateVersion: e2bTemplateVersion(), Phase: e2b.PhaseReady, CustomTemplate: true}); err != nil {
				writeErr(w, http.StatusInternalServerError, "storage", "Could not save the template.")
				return
			}
		}
	}
	p, err := s.newE2BProvider(key)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "Could not set up the E2B computer.")
		return
	}
	s.e2bEnv.mu.Lock()
	s.e2bEnv.provider = p
	s.e2bEnv.mu.Unlock()
	s.startE2BPrepare(p, wasBotComputer || s.computer() == nil)
	s.touchWorkspace()
	writeJSON(w, http.StatusOK, s.environmentsView(r.Context()))
}

func (s *Server) removeE2B(w http.ResponseWriter, r *http.Request) {
	if s.computerPolicyRefusal(w) {
		return
	}
	wasBotComputer := s.botComputerIsE2B()
	if wasBotComputer {
		if _, err := s.DetachVM(r.Context(), detachWaitDefault); err != nil && !errors.Is(err, errComputerNone) {
			writeErr(w, http.StatusConflict, "busy", "The E2B computer could not be released. Try again.")
			return
		}
	}
	s.e2bEnv.mu.Lock()
	p := s.e2bEnv.provider
	s.e2bEnv.provider = nil
	s.e2bEnv.stopPrepareLocked()
	s.e2bEnv.mu.Unlock()
	if p == nil {
		if key, _, ok := s.vaultValue(e2bKeyVaultID); ok {
			p, _ = s.newE2BProvider(key)
		}
	}
	if p != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		if err := p.Remove(ctx); err != nil {
			// The sandbox may stay in the person's E2B team; they see it there.
			log.Printf("[environments] remove E2B sandbox: %s", e2bErrorCode(err))
		}
		cancel()
	}
	_ = s.deleteVaultValue(e2bGuestVaultID)
	if err := s.deleteVaultValue(e2bKeyVaultID); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "Could not delete the key.")
		return
	}
	_ = s.saveAutomationValue(e2bStateKey, nil)
	if s.botComputerChoice() == envKindE2B {
		s.saveBotComputerChoice("")
	}
	if wasBotComputer {
		if s.computerFactory != nil && s.computer() == nil {
			// The VM was set aside for the E2B computer: it comes back.
			if err := s.AttachVM(r.Context()); err != nil && !errors.Is(err, errComputerAttached) {
				log.Printf("[environments] attach VM after removing E2B: %v", err)
			}
		} else if s.computerFactory == nil && s.store != nil {
			// No VM here: drop the detached row so a VM assigned later starts attached.
			if err := s.store.forgetBotComputerRow(computer.DefaultID); err != nil {
				log.Printf("[environments] forget computer row: %v", err)
			}
		}
	}
	s.touchWorkspace()
	writeJSON(w, http.StatusOK, s.environmentsView(r.Context()))
}

func (s *Server) useEnvironment(w http.ResponseWriter, r *http.Request, id string) {
	if v := s.policy()[capComputer]; v.Mode == "none" || v.Mode == policyModeProvided || (v.Mode == policyModeOwn && id != e2bEnvironmentID) {
		refusePolicy(w, v.Mode != policyModeProvided)
		return
	}
	cur := s.computer()
	switch id {
	case e2bEnvironmentID:
		p := s.e2bCurrent()
		if p == nil || p.Status().Phase != "ready" {
			writeErr(w, http.StatusConflict, "not_ready", "The E2B computer is not ready yet.")
			return
		}
		if cur != nil && cur.Kind() == envKindE2B {
			break
		}
		if cur != nil {
			if _, err := s.DetachVM(r.Context(), detachWaitDefault); err != nil && !errors.Is(err, errComputerNone) {
				writeErr(w, http.StatusConflict, "busy", "The Bot computer could not be released. Try again.")
				return
			}
		}
		if err := s.attachProvider(p); err != nil {
			writeErr(w, http.StatusConflict, "busy", "Could not switch the Bot computer. Try again.")
			return
		}
		s.saveBotComputerChoice(envKindE2B)
	case computer.DefaultID, envKindVM:
		if s.computerFactory == nil {
			writeErr(w, http.StatusConflict, "not_available", "This account has no computer on this server.")
			return
		}
		if cur != nil && cur.Kind() != envKindE2B {
			break
		}
		if cur != nil {
			if _, err := s.DetachVM(r.Context(), detachWaitDefault); err != nil && !errors.Is(err, errComputerNone) {
				writeErr(w, http.StatusConflict, "busy", "The Bot computer could not be released. Try again.")
				return
			}
			if p := s.e2bCurrent(); p != nil {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					if err := p.Pause(ctx); err != nil {
						log.Printf("[environments] pause E2B computer: %s", e2bErrorCode(err))
					}
				}()
			}
		}
		if err := s.AttachVM(r.Context()); err != nil && !errors.Is(err, errComputerAttached) {
			writeErr(w, http.StatusConflict, "busy", "Could not switch the Bot computer. Try again.")
			return
		}
		s.saveBotComputerChoice(envKindVM)
	default:
		writeErr(w, http.StatusNotFound, "not_found", "No such environment.")
		return
	}
	s.touchWorkspace()
	writeJSON(w, http.StatusAccepted, s.environmentsView(r.Context()))
}

// forgetBotComputerRow deletes the Bot computer's row after its E2B computer
// was removed on an account with no VM.
func (s *Store) forgetBotComputerRow(id string) error {
	_, err := s.db.Exec(`DELETE FROM computers WHERE id=? AND kind=? AND status=?`, id, computer.RowKindVM, computerRowDetached)
	return err
}
