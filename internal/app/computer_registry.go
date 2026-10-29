package app

// The computer registry (computer-decoupling S5/S6): which computers this
// account has right now, how a call reaches one by id, and what happens when one
// is attached, detached or lost. Today an account has at most one VM; the same
// types hold several personal computers once the client exists.
//
// Rules, each enforced here and proved in computer_attach_test.go:
//   - one lock per computer: a computer is operated by one Bot at a time, and
//     two computers are operated by different Bots at the same time;
//   - attach and detach need no restart; a run keeps working across both;
//   - a detach first stops new calls, waits for the running ones (with a
//     timeout), copies the outbox to the server, and only then cancels what is
//     left, so a call in flight ends as an ordinary tool error instead of hanging;
//   - a computer that stops answering without a detach is "lost": its lock is
//     released, its calls fail the same way, and the Bot goes on.

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	changed chan struct{} // closed and replaced on every change of state, active or owner
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
		code, msg = codeComputerOffline, "The computer stopped answering while this step ran. Whether the step finished on it is unknown; continue without it."
	}
	return tooloutcome.New(tooloutcome.Permanent, code, "unknown", msg, "explain_blocker").Err()
}

// acquire takes the operator lock for a Bot. The same Bot may take it again
// (reentrant per run); another Bot waits until the holder's run ends, the call
// context ends, or the computer goes away. This is "one lock per computer".
func (c *computerSlot) acquire(ctx context.Context, botID, runID string) (func(), error) {
	for {
		c.mu.Lock()
		if c.state != slotAttached {
			state := c.state
			c.mu.Unlock()
			return nil, slotRefusal(state)
		}
		if c.owner == "" || (c.owner == botID && c.ownerBy == runID) {
			c.owner, c.ownerBy = botID, runID
			c.depth++
			c.notifyLocked()
			c.mu.Unlock()
			return func() { c.release(botID, runID) }, nil
		}
		wait := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, tooloutcome.New(tooloutcome.Transient, codeComputerBusy, "not_executed", "Another Bot is using this computer. Try again later or continue without it.", "retry").Err()
		case <-c.ctx.Done():
		case <-wait:
		}
	}
}

func (c *computerSlot) release(botID, runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner == botID && c.ownerBy == runID && c.depth > 0 {
		c.depth--
		if c.depth == 0 {
			c.owner, c.ownerBy = "", ""
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
		c.notifyLocked()
	}
}

// forceFree drops the lock whoever holds it and ends in-flight calls. Used on
// detach and loss.
func (c *computerSlot) forceFree() {
	c.mu.Lock()
	c.owner, c.ownerBy, c.depth = "", "", 0
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
	if !microVMActions[name] {
		return "", tooloutcome.InvalidArguments(fmt.Sprintf("unsupported computer action %q", name))
	}
	releaseLock, err := slot.acquire(ctx, r.BotID, r.ID)
	if err != nil {
		return "", err
	}
	_ = releaseLock // held until the run ends: releaseComputerHolds(runID)
	callCtx, done, err := slot.begin(ctx, false)
	if err != nil {
		return "", err
	}
	defer done()
	botName := ""
	if s.store != nil {
		if bot, e := s.store.GetBot(r.BotID); e == nil {
			botName = bot.Name
		}
	}
	started := time.Now()
	result, err := slot.provider.ActionWithin(callCtx, computer.Action{BotID: r.BotID, BotName: botName, RunID: r.ID, Name: name, Args: args, Source: "model"}, computerDispatchTimeoutFor(name, args))
	s.store.recordRunComputer(r.ID, slot.id, slot.region, time.Since(started))
	if err != nil {
		if slot.endedByComputer(ctx) {
			return "", slot.lostOrDetachedError()
		}
		return "", err
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
	return nil
}

// afterVMAttached is everything that follows a VM becoming reachable, shared by start-up and a hot attach.
func (s *Server) afterVMAttached(p computer.Provider) {
	if s.store != nil {
		if blobs, ok := p.(guestBlobStorage); ok {
			s.store.setLegacyBlobs(blobs)
			s.store.cleanupDeletedAttachments()
		}
		if err := s.store.upsertVMComputer(p.ID(), "Bot workspace", "linux", "", kvmCapabilities, 1, computerRowAttached); err != nil {
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
		silent := now.Sub(seen) > computerLostAfter
		switch state := slot.currentState(); {
		case silent && state == slotAttached:
			s.markComputerLost(slot.id)
		case !silent && state == slotLost:
			s.markComputerAlive(slot.id)
		case !silent && s.store != nil:
			s.store.touchComputer(slot.id)
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
