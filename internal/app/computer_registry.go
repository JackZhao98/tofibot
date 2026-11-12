package app

// The computer registry (computer-decoupling S5/S6): which computers this
// account has right now, how a call reaches one by id, and what happens when one
// is attached, detached or lost. Today an account has at most one VM; the same
// types hold several personal computers once the client exists.
//
// Rules, each enforced here and proved in computer_attach_test.go:
//   - one lock per computer: a computer is operated by one Bot at a time (the
//     lock is a lease per tool call, handed to waiting Bots in arrival order),
//     and two computers are operated by different Bots at the same time;
//   - attach and detach need no restart; a run keeps working across both;
//   - a detach first stops new calls, waits for the running ones (with a
//     timeout), copies the outbox to the server, and only then cancels what is
//     left, so a call in flight ends as an ordinary tool error instead of hanging;
//   - a computer that stops answering without a detach is "lost": its lock is
//     released, its calls fail the same way, and the Bot goes on.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Slot states while a computer is part of the workspace.
const (
	slotAttached = "attached"
	slotDraining = "draining"
	slotLost     = "lost"
)

// Stable tool-error codes. The model-facing text never carries a path or a socket.
const (
	codeComputerDetached = "computer_detached"
	codeComputerOffline  = "computer_offline"
	codeComputerBusy     = "computer_busy"
	codeComputerUnknown  = "computer_not_found"
)

// computerLostAfter is how long a heartbeat-connected computer may stay silent
// before it counts as lost. A variable so tests can shorten it.
var computerLostAfter = 90 * time.Second

// computerBeatEvery is how often the registry looks at heartbeats.
var computerBeatEvery = 15 * time.Second

// detachWaitDefault is how long a detach waits for running calls.
const detachWaitDefault = 30 * time.Second

// computerSlot is the runtime state of one computer.
type computerSlot struct {
	id       string
	rowKind  string
	region   string
	provider computer.Provider

	mu      sync.Mutex
	state   string
	ctx     context.Context
	cancel  context.CancelFunc
	active  int
	owner   string // Bot holding the operator lock ("" = free)
	ownerBy string // run that took it
	depth   int
	waiters []*computerWaiter // Bots queued for the lock, in arrival order
	changed chan struct{}     // closed and replaced on every change of state, active or owner
}

func newComputerSlot(p computer.Provider, rowKind, region string) *computerSlot {
	ctx, cancel := context.WithCancel(context.Background())
	return &computerSlot{id: p.ID(), rowKind: rowKind, region: region, provider: p, state: slotAttached, ctx: ctx, cancel: cancel, changed: make(chan struct{})}
}

// notifyLocked wakes everything waiting on the slot. Caller holds mu.
func (c *computerSlot) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *computerSlot) currentState() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// refusal is the tool error for a slot that cannot take a call now.
func slotRefusal(state string) error {
	switch state {
	case slotLost:
		return tooloutcome.New(tooloutcome.Transient, codeComputerOffline, "not_executed", "This computer is not answering. It may come back; continue without it for now.", "explain_blocker").Err()
	default:
		return tooloutcome.New(tooloutcome.Permanent, codeComputerDetached, "not_executed", "This computer is detached. Continue without it.", "explain_blocker").Err()
	}
}

// begin counts one call in flight. The returned context is canceled when the
// computer is detached or lost, and the returned function ends the call. During
// a drain only the detach's own outbox copy (allowDraining) may start.
func (c *computerSlot) begin(ctx context.Context, allowDraining bool) (context.Context, func(), error) {
	c.mu.Lock()
	if c.state == slotLost || (c.state == slotDraining && !allowDraining) {
		state := c.state
		c.mu.Unlock()
		return nil, nil, slotRefusal(state)
	}
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return nil, nil, slotRefusal(slotDraining)
	}
	c.active++
	slotCtx := c.ctx
	c.notifyLocked()
	c.mu.Unlock()
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(slotCtx, cancel)
	return callCtx, func() {
		stop()
		cancel()
		c.mu.Lock()
		c.active--
		c.notifyLocked()
		c.mu.Unlock()
	}, nil
}

// endedByComputer reports whether err came from the slot being canceled (not from the caller's own context).
func (c *computerSlot) endedByComputer(ctx context.Context) bool {
	return ctx.Err() == nil && c.ctx.Err() != nil
}

// lostOrDetachedError is the ordinary tool error for a call that was in flight
// when its computer went away. The effect on the computer is unknown.
func (c *computerSlot) lostOrDetachedError() error {
	code, msg := codeComputerDetached, "The computer was detached while this step ran. Whether the step finished on it is unknown; continue without it."
	if c.currentState() == slotLost {
		code, msg = codeComputerLost, "The computer stopped answering while this step ran. Whether the step finished on it is unknown; continue without it."
	}
	return tooloutcome.New(tooloutcome.Permanent, code, "unknown", msg, "explain_blocker").Err()
}

// computerWaiter is a Bot queued for the operator lock.
type computerWaiter struct {
	botID, runID string
	ch           chan struct{} // closed when the waiter is granted the lock or the queue is cleared
	granted      bool
}

// acquire takes the operator lock for one call of a Bot. The lock is a lease
// per tool call (like the VM's desktop lease): the caller releases it when the
// call ends, so Bots alternate on a busy computer instead of the first one
// keeping it until its run is over. Waiters are served in arrival order and the
// lock is handed straight to the next one, so a Bot that calls again at once
// queues behind them. The same run may take it again while it holds it (parallel
// calls of one run); another Bot waits until the call context ends or the
// computer goes away. This is "one lock per computer".
func (c *computerSlot) acquire(ctx context.Context, botID, runID string) (func(), error) {
	var w *computerWaiter
	granted := func() (func(), error) { return func() { c.release(botID, runID) }, nil }
	for {
		c.mu.Lock()
		if c.state != slotAttached {
			state := c.state
			held := w != nil && w.granted
			c.dequeueLocked(w)
			c.mu.Unlock()
			if held { // the lock was handed over as the computer went away
				c.release(botID, runID)
			}
			return nil, slotRefusal(state)
		}
		if w != nil && w.granted {
			c.mu.Unlock()
			return granted()
		}
		if c.owner == botID && c.ownerBy == runID {
			c.dequeueLocked(w)
			c.depth++
			c.notifyLocked()
			c.mu.Unlock()
			return granted()
		}
		if c.owner == "" && (len(c.waiters) == 0 || (w != nil && c.waiters[0] == w)) {
			c.dequeueLocked(w)
			c.owner, c.ownerBy, c.depth = botID, runID, 1
			c.notifyLocked()
			c.mu.Unlock()
			return granted()
		}
		if w == nil {
			w = &computerWaiter{botID: botID, runID: runID, ch: make(chan struct{})}
			c.waiters = append(c.waiters, w)
		}
		slotDone := c.ctx.Done()
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			c.mu.Lock()
			if w.granted { // the lock arrived as the wait ended: pass it on
				c.mu.Unlock()
				c.release(botID, runID)
			} else {
				c.dequeueLocked(w)
				c.mu.Unlock()
			}
			return nil, tooloutcome.New(tooloutcome.Transient, codeComputerBusy, "not_executed", "Another Bot is using this computer. Try again later or continue without it.", "retry").Err()
		case <-w.ch:
			c.mu.Lock()
			if !w.granted { // the queue was cleared (detach or loss): look at the state again
				w = nil
			}
			c.mu.Unlock()
		case <-slotDone:
			c.mu.Lock()
			c.dequeueLocked(w)
			w = nil
			c.mu.Unlock()
		}
	}
}

// dequeueLocked removes a waiter that has not been granted. Caller holds mu.
func (c *computerSlot) dequeueLocked(w *computerWaiter) {
	if w == nil || w.granted {
		return
	}
	for i, q := range c.waiters {
		if q == w {
			c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
			return
		}
	}
}

// grantNextLocked hands a free lock to the first waiter. Caller holds mu.
func (c *computerSlot) grantNextLocked() {
	if c.owner != "" || len(c.waiters) == 0 {
		return
	}
	w := c.waiters[0]
	c.waiters = c.waiters[1:]
	w.granted = true
	c.owner, c.ownerBy, c.depth = w.botID, w.runID, 1
	close(w.ch)
}

func (c *computerSlot) release(botID, runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner == botID && c.ownerBy == runID && c.depth > 0 {
		c.depth--
		if c.depth == 0 {
			c.owner, c.ownerBy = "", ""
			c.grantNextLocked()
		}
		c.notifyLocked()
	}
}

// releaseRun drops every hold a run has, at the end of the run.
func (c *computerSlot) releaseRun(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ownerBy == runID && c.owner != "" {
		c.owner, c.ownerBy, c.depth = "", "", 0
		c.grantNextLocked()
		c.notifyLocked()
	}
}

// forceFree drops the lock whoever holds it and ends in-flight calls. Used on
// detach and loss.
func (c *computerSlot) forceFree() {
	c.mu.Lock()
	c.owner, c.ownerBy, c.depth = "", "", 0
	for _, w := range c.waiters { // they look at the state again and are refused
		close(w.ch)
	}
	c.waiters = nil
	c.cancel()
	c.notifyLocked()
	c.mu.Unlock()
}

func (c *computerSlot) holder() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owner
}

// ---- Server side ----

// computer returns the account's VM, or nil when there is none. It is the one
// question the rest of the server asks (AGENTS.md: "Computer is optional").
func (s *Server) computer() computer.Provider {
	s.computerMu.RLock()
	defer s.computerMu.RUnlock()
	return s.microVM
}

// slotFor returns the runtime slot of a computer id, or nil. The VM's slot is
// created on first use so a provider set directly (tests) behaves the same.
func (s *Server) slotFor(id string) *computerSlot {
	s.computerMu.Lock()
	defer s.computerMu.Unlock()
	return s.slotForLocked(id)
}

func (s *Server) slotForLocked(id string) *computerSlot {
	if s.microVM != nil && s.microVM.ID() == id {
		slot := s.computerSlots[id]
		if slot == nil || slot.provider != s.microVM {
			if slot != nil {
				slot.forceFree()
			}
			if s.computerSlots == nil {
				s.computerSlots = map[string]*computerSlot{}
			}
			slot = newComputerSlot(s.microVM, computer.RowKindVM, "")
			s.computerSlots[id] = slot
		}
		return slot
	}
	return s.computerSlots[id]
}

// attachedSlots lists the computers of the workspace, the VM first.
func (s *Server) attachedSlots() []*computerSlot {
	s.computerMu.Lock()
	defer s.computerMu.Unlock()
	var out []*computerSlot
	if s.microVM != nil {
		out = append(out, s.slotForLocked(s.microVM.ID()))
	}
	var extra []*computerSlot
	for id, slot := range s.computerSlots {
		if s.microVM != nil && id == s.microVM.ID() {
			continue
		}
		extra = append(extra, slot)
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].id < extra[j].id })
	return append(out, extra...)
}

// hasOtherComputers reports an attached computer that is not the VM.
func (s *Server) hasOtherComputers() bool {
	for _, slot := range s.attachedSlots() {
		if slot.rowKind != computer.RowKindVM {
			return true
		}
	}
	return false
}

// RegisterComputer adds a computer other than the VM (a person's own machine,
// once its client exists) to the registry and records its row. The VM is
// attached with AttachVM; this path never replaces it.
func (s *Server) RegisterComputer(p computer.Provider, name, region string) error {
	if p == nil || strings.TrimSpace(p.ID()) == "" {
		return errors.New("computer id required")
	}
	s.computerMu.Lock()
	if s.microVM != nil && p.ID() == s.microVM.ID() {
		s.computerMu.Unlock()
		return errors.New("computer id is taken by the VM")
	}
	if old := s.computerSlots[p.ID()]; old != nil {
		old.forceFree()
	}
	if s.computerSlots == nil {
		s.computerSlots = map[string]*computerSlot{}
	}
	slot := newComputerSlot(p, computer.RowKindPersonal, region)
	s.computerSlots[p.ID()] = slot
	s.computerMu.Unlock()
	log.Printf("[computer] registered %s (%s)", p.ID(), p.Kind())
	return nil
}

// computerByID resolves a computer id to its slot for a call. Unknown ids are
// a tool error, not a panic.
func (s *Server) computerByID(id string) (*computerSlot, error) {
	slot := s.slotFor(id)
	if slot == nil {
		return nil, tooloutcome.New(tooloutcome.Permanent, codeComputerUnknown, "not_executed", "No such computer is attached.", "explain_blocker").Err()
	}
	return slot, nil
}

// detachSyncKey lets the detach's own outbox copy run while the slot drains.
type detachSyncKey struct{}

// computerActionOn runs one action on a computer other than (or including) the
// VM, under that computer's lock. The VM keeps its own desktop lease and goes
// through microVMAction.
func (s *Server) computerActionOn(ctx context.Context, r Run, id, name string, args []byte) (string, error) {
	if vm := s.computer(); vm != nil && vm.ID() == id {
		return s.microVMAction(ctx, r, name, args)
	}
	slot, err := s.computerByID(id)
	if err != nil {
		return "", err
	}
	if slot.rowKind == computer.RowKindPersonal {
		if strings.HasPrefix(name, "browser.") && !browserActions[name] {
			return "", browserError("browser_action_unsupported", "not_executed")
		}
		if !personalActionAllowed(name) {
			return "", tooloutcome.InvalidArguments(fmt.Sprintf("unsupported computer action %q", name))
		}
	} else if !microVMActions[name] {
		return "", tooloutcome.InvalidArguments(fmt.Sprintf("unsupported computer action %q", name))
	}
	isBrowser := slot.rowKind == computer.RowKindPersonal && strings.HasPrefix(name, "browser.")
	var browserStamp browserGeneration
	if isBrowser {
		if r.BotID == "" {
			return "", browserError("app_not_allowed", "not_executed")
		}
		if _, e := s.store.GetBot(r.BotID); e != nil {
			return "", browserError("app_not_allowed", "not_executed")
		}
		browserStamp = s.browserGeneration(id)
		args, err = s.prepareBrowser(id, name, args)
		if err != nil {
			return "", err
		}
	}
	// A personal computer goes through the same approval gate as the VM: a
	// consequential input or write raises the approval card before it queues.
	settle := func(error) {}
	if slot.rowKind == computer.RowKindPersonal {
		var guardErr error
		if isBrowser {
			settle, guardErr = s.guardBrowser(ctx, r, id, name, args)
		} else {
			settle, guardErr = s.guardPersonalAction(ctx, r, slot, name, args)
		}
		if guardErr != nil {
			return "", guardErr
		}
		args = withoutEffect(args)
	}
	releaseLock, err := slot.acquire(ctx, r.BotID, r.ID)
	if err != nil {
		settle(err)
		return "", err
	}
	if isBrowser {
		if err = s.recheckBrowser(id, name, args); err == nil {
			err = s.browserGenerationCheck(id, browserStamp)
		}
		if err != nil {
			releaseLock()
			settle(err)
			return "", err
		}
	}
	defer releaseLock() // a lease per call: the next Bot in line gets the computer when this call ends
	callCtx, done, err := slot.begin(ctx, false)
	if err != nil {
		settle(err)
		return "", err
	}
	defer done()
	if isBrowser {
		callCtx = context.WithValue(callCtx, browserGenerationContextKey{}, browserStamp)
	}
	botName := ""
	if s.store != nil {
		if bot, e := s.store.GetBot(r.BotID); e == nil {
			botName = bot.Name
		}
	}
	if isBrowser && r.ID != "" {
		h := s.browserHub(id)
		h.mu.Lock()
		h.activity = &browserActivity{Bot: r.BotID, Run: r.ID, Conversation: r.ConversationID}
		h.notify()
		h.mu.Unlock()
	}
	started := time.Now()
	result, err := slot.provider.ActionWithin(callCtx, computer.Action{BotID: r.BotID, BotName: botName, RunID: r.ID, Name: name, Args: args, Source: "model"}, computerDispatchTimeoutFor(name, args))
	s.store.recordRunComputer(r.ID, slot.id, slot.region, time.Since(started))
	settle(err)
	if err != nil {
		if slot.endedByComputer(ctx) {
			return "", slot.lostOrDetachedError()
		}
		return "", err
	}
	if isBrowser {
		s.noteBrowserResult(id, name, result.Result)
		return browserModelResult(name, result.Result), nil
	}
	return string(result.Result), nil
}

// releaseComputerHolds frees every computer lock a run holds. Called where the
// run's desktop ownership is released.
func (s *Server) releaseComputerHolds(runID string) {
	s.computerMu.RLock()
	slots := make([]*computerSlot, 0, len(s.computerSlots))
	for _, slot := range s.computerSlots {
		slots = append(slots, slot)
	}
	s.computerMu.RUnlock()
	for _, slot := range slots {
		slot.releaseRun(runID)
	}
}

// ---- listing for the Bot ----

// computerSummary is what the Bot learns about each computer.
type computerSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Online       bool     `json:"online"`
	Default      bool     `json:"default,omitempty"`
	InUse        bool     `json:"in_use,omitempty"`
	Region       string   `json:"region,omitempty"`
	Capabilities []string `json:"capabilities"`
}

// botComputers lists the attached computers for a Bot: the VM (the default)
// and any personal computers. Nothing here names a path, a socket or a token.
func (s *Server) botComputers(ctx context.Context) []computerSummary {
	var out []computerSummary
	for _, slot := range s.attachedSlots() {
		item := computerSummary{ID: slot.id, Name: slot.id, Kind: slot.rowKind, Region: slot.region, Capabilities: []string{}}
		state := slot.currentState()
		if slot.rowKind == computer.RowKindVM {
			item.Default = true
			item.Name = "Bot workspace"
			item.Capabilities = append(item.Capabilities, s.computerCapabilities().Capabilities...)
			if info, err := s.microVMInfo(ctx); err == nil {
				item.Online = state == slotAttached && computer.Available(info.State)
			}
		} else {
			item.Online = state == slotAttached
			if s.store != nil {
				if row, err := s.store.computerRow(slot.id); err == nil {
					item.Online = item.Online && computerRecentlySeen(row.LastSeen)
					item.Name = row.Name
					item.Capabilities = row.Capabilities
				}
			}
		}
		item.InUse = slot.holder() != ""
		out = append(out, item)
	}
	return out
}

// computersRunContext is the one run-context line that tells a Bot about more
// than one computer. With only the VM it is empty (the VM line already exists).
func (s *Server) computersRunContext(ctx context.Context) string {
	if !s.hasOtherComputers() {
		return ""
	}
	var parts []string
	for _, c := range s.botComputers(ctx) {
		state := "online"
		if !c.Online {
			state = "offline"
		}
		label := fmt.Sprintf("%s (%s, %s", c.Name, c.Kind, state)
		if c.Default {
			label += ", default"
		}
		parts = append(parts, label+") id="+c.ID)
	}
	return "Computers: " + strings.Join(parts, "; ") + ". Use computer_action with a computer_id to act on one; the default is the Bot workspace. Call list_computers for capabilities."
}

// ---- lifecycle ----

// DetachReport says what a detach did.
type DetachReport struct {
	ComputerID string `json:"computer_id"`
	// Drained is true when every running call ended before the timeout.
	Drained bool `json:"drained"`
	// Interrupted counts calls that were still running at the timeout and ended with a tool error.
	Interrupted int `json:"interrupted"`
	Outbox      struct {
		Synced   int `json:"synced"`
		Deferred int `json:"deferred"`
		Refused  int `json:"refused"`
	} `json:"outbox"`
}

var (
	errComputerBackend  = errors.New("computer_backend_unavailable")
	errComputerAttached = errors.New("computer_already_attached")
	errComputerNone     = errors.New("computer_not_attached")
)

// IsComputerBackendUnavailable reports an attach that cannot be served because no backend is configured.
func IsComputerBackendUnavailable(err error) bool { return errors.Is(err, errComputerBackend) }

// AttachVM puts the account's VM into use without a restart. It builds the
// provider from the configured control socket, records the row, restores the
// legacy-file reader and starts the guest watchdog. Calling it on an attached
// VM is a no-op that reports errComputerAttached.
func (s *Server) AttachVM(ctx context.Context) error {
	if s.computerFactory == nil {
		return errComputerBackend
	}
	if s.accountID != "" && s.orgPolicy != nil && !computerAllowsVM(s.policy()[capComputer]) {
		return errComputerPolicy // the account's policy gives it no VM: no path attaches one around it
	}
	// The container keeps serving local connections after the attach.
	s.recordContainerRunnerNoComputer()
	s.computerMu.Lock()
	if s.microVM != nil {
		s.computerMu.Unlock()
		return errComputerAttached
	}
	p, err := s.computerFactory()
	if err != nil {
		s.computerMu.Unlock()
		return err
	}
	s.microVM = p
	if s.computerSlots == nil {
		s.computerSlots = map[string]*computerSlot{}
	}
	s.computerSlots[p.ID()] = newComputerSlot(p, computer.RowKindVM, "")
	s.computerMu.Unlock()
	s.afterVMAttached(p)
	// A kept disk may hold an old guest Runner: check it against the container.
	s.stopLocalRunnerMigration()
	s.startLocalRunnerMigration()
	// The policy may have changed while the attach ran (applyPolicyChange saw nothing to detach).
	if s.accountID != "" && s.orgPolicy != nil && !computerAllowsVM(s.policy()[capComputer]) {
		s.enforceComputerPolicy(ctx)
		return errComputerPolicy
	}
	return nil
}

// afterVMAttached is everything that follows a VM becoming reachable, shared by start-up and a hot attach.
func (s *Server) afterVMAttached(p computer.Provider) {
	if s.store != nil {
		if blobs, ok := p.(guestBlobStorage); ok {
			s.store.setLegacyBlobs(blobs)
			s.store.cleanupDeletedAttachments()
		}
		name, caps := botComputerRowLabel(p)
		if err := s.store.upsertVMComputer(p.ID(), name, "linux", "", caps, 1, computerRowAttached); err != nil {
			log.Printf("[computer] could not record the computer row: %v", err)
		}
	}
	if client, ok := p.(*computer.Client); ok {
		w := newComputerWatchdog(client)
		s.computerMu.Lock()
		old := s.computerWatchdog
		s.computerWatchdog = w
		s.computerMu.Unlock()
		old.stop()
		w.start()
	}
	s.microVMInfoCache.reset()
	// A new computer has not been told the person's time zone.
	s.guestTimezoneMu.Lock()
	s.guestTimezoneSynced = false
	s.guestTimezoneMu.Unlock()
	s.touchWorkspace()
	log.Printf("[computer] attached %s", p.ID())
}

// afterVMDetached is the matching clean-up.
func (s *Server) afterVMDetached(id, status string) {
	if s.store != nil {
		s.store.setLegacyBlobs(nil)
		if err := s.store.setComputerStatus(id, status); err != nil {
			log.Printf("[computer] could not record the computer status: %v", err)
		}
	}
	s.computerMu.Lock()
	w := s.computerWatchdog
	s.computerWatchdog = nil
	s.computerMu.Unlock()
	w.stop()
	s.microVMInfoCache.reset()
	s.touchWorkspace()
	log.Printf("[computer] detached %s", id)
}

// DetachVM takes the VM out of use without a restart. Order: stop new calls,
// wait for running ones (up to wait), copy the outbox to the server, then
// cancel what is left and mark the row detached. Files that exist only on the
// VM show as "on a disconnected computer" afterwards (file_on_detached_computer).
func (s *Server) DetachVM(ctx context.Context, wait time.Duration) (DetachReport, error) {
	var report DetachReport
	s.computerMu.RLock()
	vm := s.microVM
	s.computerMu.RUnlock()
	if vm == nil {
		return report, errComputerNone
	}
	report.ComputerID = vm.ID()
	slot := s.slotFor(vm.ID())
	if wait <= 0 {
		wait = detachWaitDefault
	}
	slot.mu.Lock()
	slot.state = slotDraining
	slot.notifyLocked()
	slot.mu.Unlock()

	report.Drained = s.waitComputerIdle(ctx, slot, wait)
	// The outbox is copied while the computer still answers, whether or not it drained.
	s.syncOutboxForDetach(context.WithValue(ctx, detachSyncKey{}, true), slot, &report)

	slot.mu.Lock()
	report.Interrupted = slot.active
	slot.mu.Unlock()
	slot.forceFree()

	s.computerMu.Lock()
	if s.microVM == vm {
		s.microVM = nil
	}
	delete(s.computerSlots, vm.ID())
	s.computerMu.Unlock()
	s.clearDesktopOwners()
	s.afterVMDetached(vm.ID(), computerRowDetached)
	return report, nil
}

// waitComputerIdle waits until no call is in flight and no run holds the
// shared desktop, or until the timeout. It reports whether the computer went idle.
func (s *Server) waitComputerIdle(ctx context.Context, slot *computerSlot, wait time.Duration) bool {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		slot.mu.Lock()
		active, wake := slot.active, slot.changed
		slot.mu.Unlock()
		if active == 0 && s.desktopIdle() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-wake:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// computerInUse reports that a call is in flight on the computer or that a run
// or the person holds its desktop: the same signals DetachVM waits on.
func (s *Server) computerInUse(id string) bool {
	slot := s.slotFor(id)
	active := 0
	if slot != nil {
		slot.mu.Lock()
		active = slot.active
		slot.mu.Unlock()
	}
	return active > 0 || !s.desktopIdle()
}

// desktopIdle reports that no run holds the shared desktop of the VM.
func (s *Server) desktopIdle() bool {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	return len(s.computerOwners) == 0
}

// clearDesktopOwners drops the shared-desktop holds of the VM that just left.
func (s *Server) clearDesktopOwners() {
	s.computerOwnerMu.Lock()
	s.computerOwners = map[string]string{}
	s.desktopObserved = nil
	s.desktopPointer = nil
	s.desktopChangedLocked()
	s.computerOwnerMu.Unlock()
}

// markComputerLost handles an unclean loss: the lock is released, calls in
// flight end with a tool error, the row says "lost", and the Bot continues.
func (s *Server) markComputerLost(id string) {
	slot := s.slotFor(id)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	if slot.state == slotLost {
		slot.mu.Unlock()
		return
	}
	slot.state = slotLost
	slot.mu.Unlock()
	slot.forceFree()
	if s.store != nil {
		_ = s.store.setComputerStatus(id, computerRowLost)
		if slot.rowKind == computer.RowKindPersonal {
			s.store.cancelDeviceJobs(id, codeComputerLost)
		}
	}
	if vm := s.computer(); vm != nil && vm.ID() == id {
		s.clearDesktopOwners()
	}
	s.touchWorkspace()
	log.Printf("[computer] %s stopped answering; its lock was released", id)
}

// markComputerAlive undoes markComputerLost when the computer answers again.
func (s *Server) markComputerAlive(id string) {
	slot := s.slotFor(id)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	if slot.state != slotLost {
		slot.mu.Unlock()
		return
	}
	slot.state = slotAttached
	slot.ctx, slot.cancel = context.WithCancel(context.Background())
	slot.notifyLocked()
	slot.mu.Unlock()
	if s.store != nil {
		_ = s.store.setComputerStatus(id, computerRowAttached)
	}
	s.touchWorkspace()
}

// sweepComputerHeartbeats marks computers lost or alive from their last answer.
func (s *Server) sweepComputerHeartbeats(now time.Time) {
	for _, slot := range s.attachedSlots() {
		hb, ok := slot.provider.(computer.Heartbeater)
		if !ok {
			continue
		}
		seen := hb.LastSeen()
		if seen.IsZero() {
			continue
		}
		limit := computerLostAfter
		if l, ok := slot.provider.(interface{ LostAfter() time.Duration }); ok {
			limit = l.LostAfter()
		}
		silent := now.Sub(seen) > limit
		switch state := slot.currentState(); {
		case silent && state == slotAttached:
			s.markComputerLost(slot.id)
		case !silent && state == slotLost:
			s.markComputerAlive(slot.id)
		case !silent && s.store != nil:
			if _, own := slot.provider.(interface{ SelfStamped() }); !own {
				s.store.touchComputer(slot.id)
			}
		}
	}
}

// startComputerHeartbeat runs the sweep until stopComputerHeartbeat.
func (s *Server) startComputerHeartbeat() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.computerBeatCancel, s.computerBeatDone = cancel, done
	go func() {
		defer close(done)
		t := time.NewTicker(computerBeatEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.sweepComputerHeartbeats(now)
			}
		}
	}()
}

func (s *Server) stopComputerHeartbeat() {
	if s.computerBeatCancel == nil {
		return
	}
	s.computerBeatCancel()
	<-s.computerBeatDone
	s.computerBeatCancel = nil
}

// touchWorkspace tells open clients that the computer set changed, so the
// computer button appears or goes away without a reload.
func (s *Server) touchWorkspace() {
	if s.store == nil {
		return
	}
	_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
}

// watchdog returns the guest watchdog of the attached VM, or nil.
func (s *Server) watchdog() *computerWatchdog {
	s.computerMu.RLock()
	defer s.computerMu.RUnlock()
	return s.computerWatchdog
}

// legacyBlobs is the reader of `vm:` attachment bytes, nil while no VM is attached.
func (s *Store) legacyBlobs() guestBlobStorage {
	s.guestBlobsMu.RLock()
	defer s.guestBlobsMu.RUnlock()
	return s.guestBlobs
}

func (s *Store) setLegacyBlobs(b guestBlobStorage) {
	s.guestBlobsMu.Lock()
	s.guestBlobs = b
	s.guestBlobsMu.Unlock()
}

// vmDetachedByAdmin reports a VM row whose last state is "detached": a restart
// must not attach it again until an administrator does.
func (s *Store) vmDetachedByAdmin() bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM computers WHERE kind='vm' AND status='detached' AND revoked_at IS NULL`).Scan(&n)
	return n > 0
}

// reset forgets the cached computer info, after an attach or detach.
func (c *microVMInfoCache) reset() {
	c.mu.Lock()
	c.info, c.err, c.at = computer.Info{}, nil, time.Time{}
	c.mu.Unlock()
}

// computerSocket is the control socket of a provider that has one, else "".
// Terminal ownership is recorded against it so a restart cleans the right computer.
func computerSocket(p computer.Provider) string {
	if sp, ok := p.(computer.Socketed); ok {
		return sp.Socket()
	}
	return ""
}

// oauthBridge is the sign-in bridge of the attached computer. A computer without
// one (or none attached) gives a bridge whose calls fail with a plain error.
func (s *Server) oauthBridge() computer.OAuthBridge {
	if b, ok := s.computer().(computer.OAuthBridge); ok {
		return b
	}
	return noOAuthBridge{}
}

type noOAuthBridge struct{}

var errNoOAuthBridge = errors.New("this computer has no sign-in bridge")

func (noOAuthBridge) OAuthStart(context.Context) (computer.OAuthStartResult, error) {
	return computer.OAuthStartResult{}, errNoOAuthBridge
}
func (noOAuthBridge) OAuthArm(context.Context, string, string) (computer.OAuthArmResult, error) {
	return computer.OAuthArmResult{}, errNoOAuthBridge
}
func (noOAuthBridge) OAuthPoll(context.Context, string) (computer.OAuthPollResult, error) {
	return computer.OAuthPollResult{}, errNoOAuthBridge
}
func (noOAuthBridge) OAuthCancel(context.Context, string) (computer.OAuthCancelResult, error) {
	return computer.OAuthCancelResult{}, errNoOAuthBridge
}

// isVMRoute reports that a /api/computers/{id}/ segment names this account's VM.
// With no VM attached the default id still answers, so the old "not configured" reply is kept.
func (s *Server) isVMRoute(id string) bool {
	if vm := s.computer(); vm != nil {
		return vm.ID() == id
	}
	return id == computer.DefaultID
}

// Values of the personal_input_review setting: which input on a person's own
// Mac goes to the reviewer besides what the Bot declares as consequential.
const (
	personalReviewDeclared = "declared"
	personalReviewCommands = "commands" // also every Cmd or Ctrl chord
	personalReviewAll      = "all"      // every input
)

// What the reviewer sees as the effect of input the Bot declared as none but
// that personal_input_review sends to review.
const (
	keyCommandEffect       = "key command on the person's own computer (the Bot declared none)"
	reviewedAllInputEffect = "input on the person's own computer (the Bot declared none)"
)

// personalInspectTimeout bounds the look at the Mac before a review. A Mac that
// does not answer in time, or an older desktop app, leaves the review without it.
var personalInspectTimeout = 2 * time.Second

// guardPersonalAction runs the VM's approval gate for a personal computer.
// This is the person's own machine, so it fails closed: desktop input (click,
// double and triple click, drag, scroll, type, key) that declares no effect is
// reviewed as consequential. A Bot that declares effect "none" skips the review,
// unless personal_input_review sends that input too (key commands by default).
// Capture and move never need one. Writes (files.write, sandbox.exec) are always
// reviewed, also when the Bot declares "none"; the Bot may declare a more
// specific effect. Reads are never gated. Before a review the Mac is asked what
// is in front and under the point, so the reviewer and the approval card name
// the real target.
func (s *Server) guardPersonalAction(ctx context.Context, r Run, slot *computerSlot, name string, args []byte) (func(error), error) {
	action := personalActionName(name)
	type point struct {
		X *float64 `json:"x"`
		Y *float64 `json:"y"`
	}
	var declared struct {
		Effect           string   `json:"effect"`
		Text             string   `json:"text"`
		Key              string   `json:"key"`
		Modifiers        []string `json:"modifiers"`
		Path             string   `json:"path"`
		Cmd              string   `json:"command"`
		X                *float64 `json:"x"`
		Y                *float64 `json:"y"`
		To               *point   `json:"to"`
		ScreenshotWidth  *int     `json:"screenshot_width"`
		ScreenshotHeight *int     `json:"screenshot_height"`
	}
	_ = json.Unmarshal(args, &declared)
	review := actionReview{Kind: strings.TrimPrefix(action, "desktop."), Effect: declared.Effect, Text: declared.Text}
	switch action {
	case "desktop.click", "desktop.double_click", "desktop.triple_click", "desktop.drag", "desktop.scroll", "desktop.type", "desktop.key":
		command := false
		if action == "desktop.key" {
			// The chord as the Mac presses it: a reviewer that sees only the key
			// cannot tell a letter from an application command.
			mods, key := keyChord(declared.Key, declared.Modifiers)
			review.Text = "key " + strings.Join(append(mods, key), "+")
			command = slices.Contains(mods, "cmd") || slices.Contains(mods, "ctrl")
		}
		mode := s.autoValue(autoPersonalInputReview)
		switch {
		case strings.TrimSpace(review.Effect) == "":
			review.Effect = undeclaredInputEffect
		case consequentialEffect(review.Effect):
		case mode == personalReviewAll:
			review.Effect = reviewedAllInputEffect
		case command && mode != personalReviewDeclared:
			// A Cmd or Ctrl chord is an application command, not text.
			review.Effect = keyCommandEffect
		}
		if !consequentialEffect(review.Effect) {
			return func(error) {}, nil
		}
		var at point
		switch action {
		case "desktop.drag":
			if declared.To != nil {
				at = *declared.To
			}
		case "desktop.key", "desktop.type":
		default:
			at = point{declared.X, declared.Y}
		}
		look := map[string]any{}
		if at.X != nil && at.Y != nil {
			look["x"], look["y"] = *at.X, *at.Y
			if declared.ScreenshotWidth != nil && declared.ScreenshotHeight != nil {
				look["screenshot_width"], look["screenshot_height"] = *declared.ScreenshotWidth, *declared.ScreenshotHeight
			}
		}
		seen := s.personalInputContext(ctx, r, slot, look)
		review.App, review.Window = oneLine(seen.App.Name, 60), oneLine(seen.Window, 120)
		if action != "desktop.key" && action != "desktop.type" {
			review.Element = seen.elementLabel()
			if review.Element == "" {
				review.Element = screenPointLabel(action, at.X, at.Y, review.App)
			}
		}
	case "files.write":
		review.Element, review.Text = declared.Path, ""
		if !consequentialEffect(review.Effect) {
			review.Effect = "writes a file on the person's computer"
		}
	case "sandbox.exec":
		review.Text = declared.Cmd
		if !consequentialEffect(review.Effect) {
			review.Effect = "runs a command that changes files on the person's computer"
		}
	default:
		return func(error) {}, nil
	}
	return s.guardAction(ctx, r, review)
}

// inputContext is the Mac's answer to desktop.inspect: the app and window in
// front and, for a point, the element under it. All of it is untrusted text.
type inputContext struct {
	App struct {
		Name     string `json:"name"`
		BundleID string `json:"bundle_id"`
	} `json:"app"`
	Window  string `json:"window"`
	Element *struct {
		Role            string `json:"role"`
		RoleDescription string `json:"role_description"`
		Title           string `json:"title"`
		Description     string `json:"description"`
		App             string `json:"app"`
	} `json:"element"`
}

// personalInputContext asks the Mac what is in front and under the point. It
// returns nothing when the Mac cannot answer within personalInspectTimeout.
func (s *Server) personalInputContext(ctx context.Context, r Run, slot *computerSlot, look map[string]any) inputContext {
	var out inputContext
	if slot == nil || slot.provider == nil {
		return out
	}
	raw, _ := json.Marshal(look)
	ctx, cancel := context.WithTimeout(ctx, personalInspectTimeout)
	defer cancel()
	res, err := slot.provider.ActionWithin(ctx, computer.Action{BotID: r.BotID, RunID: r.ID, Name: "desktop.inspect", Args: raw, Source: "review"}, personalInspectTimeout)
	if err == nil && res.OK {
		_ = json.Unmarshal(res.Result, &out)
	}
	return out
}

// elementLabel names the element under the point: its name, its kind and its
// app. It is empty when the element has no name, so the label never matches a
// different unnamed element.
func (c inputContext) elementLabel() string {
	e := c.Element
	if e == nil {
		return ""
	}
	name := oneLine(e.Title, 80)
	if name == "" {
		name = oneLine(e.Description, 80)
	}
	kind := oneLine(e.RoleDescription, 40)
	if kind == "" {
		kind = oneLine(strings.TrimPrefix(e.Role, "AX"), 40)
	}
	if name == "" || kind == "" {
		return ""
	}
	label := `"` + name + `" ` + kind
	app := oneLine(e.App, 60)
	if app == "" {
		app = oneLine(c.App.Name, 60)
	}
	if app != "" {
		label += " in " + app
	}
	return label
}

// screenPointLabel names a point when the Mac named no element there.
func screenPointLabel(action string, x, y *float64, app string) string {
	if x == nil || y == nil {
		return "the front window" + inApp(app)
	}
	prefix := "screen point"
	if action == "desktop.drag" {
		prefix = "drag to screen point"
	}
	return fmt.Sprintf("%s %g,%g", prefix, *x, *y) + inApp(app)
}

func inApp(app string) string {
	if app == "" {
		return ""
	}
	return " in " + app
}

// oneLine folds whitespace (newlines included) and bounds the length, for text
// shown on one line of a review or an approval card.
func oneLine(text string, max int) string {
	return trimRunes(strings.Join(strings.Fields(text), " "), max)
}

// undeclaredInputEffect is what the reviewer sees when a Bot sent input to a
// person's own computer without saying what it does.
const undeclaredInputEffect = "not declared by the Bot (input on the person's own computer)"

// withoutEffect drops the Bot's declared "effect" from desktop input args: it
// is for the approval gate and is not part of what the Mac is asked to do.
func withoutEffect(args []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil {
		return args
	}
	if _, ok := m["effect"]; !ok {
		return args
	}
	delete(m, "effect")
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}
