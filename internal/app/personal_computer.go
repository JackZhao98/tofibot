package app

// A person's own computer as a runtime computer (multi-node-system N1).
//
// The Mac holds no inbound port. It claims a one-time code, keeps a device
// token and then reaches the server only outward: it long-polls
// GET /api/computers/{id}/jobs, posts results, and posts a heartbeat every few
// seconds. The server runtime runs the Bot; this provider turns one tool call
// into one job on that queue and waits for the answer. The computer's slot in
// the registry (computer_registry.go) supplies the rest: one operator lock per
// computer, a lease per call, calls ended with computer_lost when the heartbeat
// stops, and detach on revoke.
//
// Nothing here is shown to the administrator (see adminVMSummary).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// personalLostAfter is how long a personal computer may stay silent before the
// registry calls it lost. The Mac beats every 10 seconds, so this is four missed
// beats. A variable so tests can shorten it.
var personalLostAfter = 45 * time.Second

// personalPollEvery is how often a waiting call reads its job row.
var personalPollEvery = 40 * time.Millisecond

// codeComputerLost is the tool error of a call that was in flight when its
// computer stopped answering. The effect on the computer is unknown.
const codeComputerLost = "computer_lost"

// codeComputerNoAnswer is the tool error of a call whose job the computer never
// picked up before the time ran out: it did not run.
const codeComputerNoAnswer = "computer_no_answer"

// deviceTagFor is the account tag of a claim code or device token: the account
// id when it is a plain identifier, otherwise none.
func deviceTagFor(accountID string) string {
	if accountID == "" || len(accountID) > 64 {
		return ""
	}
	for _, r := range accountID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return ""
		}
	}
	return accountID
}

func (s *Store) taggedSecret(secret string) string {
	if s.deviceTag == "" {
		return secret
	}
	return s.deviceTag + "." + secret
}

// deviceTagOf reads the account tag out of a code or token ("" when untagged).
func deviceTagOf(secret string) string {
	tag, _, ok := strings.Cut(secret, ".")
	if !ok {
		return ""
	}
	return deviceTagFor(tag)
}

// personalAliases maps the VM's tool vocabulary onto the Mac's action names, so
// a Bot that learned shell.exec on the VM can use it on a Mac too.
var personalAliases = map[string]string{"shell.exec": "sandbox.exec", "screen.capture": "desktop.capture"}

func personalActionName(name string) string {
	if alias, ok := personalAliases[name]; ok {
		return alias
	}
	return name
}

// personalActionAllowed reports whether a Bot may name this action on a personal computer.
func personalActionAllowed(name string) bool {
	return computerActions[personalActionName(name)]
}

// personalProvider implements computer.Provider (and Heartbeater) over the job queue.
type personalProvider struct {
	s  *Server
	id string
}

var (
	_ computer.Provider    = (*personalProvider)(nil)
	_ computer.Heartbeater = (*personalProvider)(nil)
)

func (p *personalProvider) ID() string   { return p.id }
func (p *personalProvider) Kind() string { return "personal" }

func (p *personalProvider) Info(ctx context.Context) (computer.Info, error) {
	return computer.Info{Kind: "personal", State: "ready"}, nil
}

func (p *personalProvider) Retry(context.Context) error { return errors.New("not supported") }

func (p *personalProvider) Purge(context.Context) (computer.Info, error) {
	return computer.Info{}, errors.New("a personal computer is wiped by its owner, not by the server")
}

func (p *personalProvider) Action(ctx context.Context, a computer.Action) (computer.ActionResult, error) {
	return p.ActionWithin(ctx, a, computerActionTimeout)
}

// LastSeen is the time of the device's last request. The row is the single
// source: every authenticated device request stamps it.
func (p *personalProvider) LastSeen() time.Time {
	var seen string
	if p.s.store.db.QueryRow(`SELECT last_seen FROM computers WHERE id=? AND revoked_at IS NULL`, p.id).Scan(&seen) != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, seen)
	return t
}

// LostAfter lets the sweep use the personal threshold.
func (p *personalProvider) LostAfter() time.Duration { return personalLostAfter }

// SelfStamped tells the sweep not to copy a heartbeat into the row: the row is the source.
func (p *personalProvider) SelfStamped() {}

// ActionWithin queues one job and waits for the Mac's answer.
func (p *personalProvider) ActionWithin(ctx context.Context, a computer.Action, limit time.Duration) (computer.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return computer.ActionResult{}, err
	}
	if limit <= 0 {
		limit = computerActionTimeout
	}
	job, err := p.s.store.queueComputerJob(Run{ID: a.RunID, BotID: a.BotID}, p.id, personalActionName(a.Name), a.Args)
	if err != nil {
		return computer.ActionResult{}, err
	}
	p.s.store.wakeDevice(p.id)
	waitCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(personalPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			reason := "tool call ended"
			if waitCtx.Err() == context.DeadlineExceeded {
				reason = "no answer in time"
			}
			wasClaimed := p.s.store.cancelJob(job.ID, reason)
			if waitCtx.Err() == context.DeadlineExceeded {
				// The Mac took the job and may have run it: say so by name instead
				// of a bare context error. A result that arrives later is refused
				// (the job is cancelled) and logged.
				if wasClaimed {
					log.Printf("[computer] job %s (%s) on %s timed out after claim; a late result will be refused", job.ID, a.Name, p.id)
					return computer.ActionResult{}, runtime.ToolTimeoutOutcome("Computer action "+a.Name, limit, false).Err()
				}
				return computer.ActionResult{}, tooloutcome.New(tooloutcome.Transient, codeComputerNoAnswer, "not_executed", "The computer did not pick up this step in time, so it was not run. Try again later or continue without it.", "explain_blocker").Err()
			}
			return computer.ActionResult{}, waitCtx.Err()
		case <-ticker.C:
			current, e := p.s.store.computerJob(job.ID, job.DeviceID)
			if e != nil {
				return computer.ActionResult{}, e
			}
			switch current.Status {
			case "completed":
				return computer.ActionResult{OK: true, Result: current.Result}, nil
			case "failed", "cancelled", "expired", "interrupted":
				msg := current.Error
				if msg == "" {
					msg = current.Status
				}
				// A job the Mac ran and refused carries its own words (folder not
				// granted, Bot not allowed). Everything else is the server's.
				return computer.ActionResult{}, personalJobError(current.Status, msg, job.Action)
			}
			// A claimed job whose Mac went quiet ends here as computer_lost; the
			// sweep would find it too, but only every few seconds.
			if current.Status == "running" && p.silentFor() > personalLostAfter {
				p.s.markComputerLost(p.id)
				return computer.ActionResult{}, tooloutcome.New(tooloutcome.Permanent, codeComputerLost, "unknown", "The computer stopped answering while this step ran. Whether the step finished on it is unknown; continue without it.", "explain_blocker").Err()
			}
		}
	}
}

func (p *personalProvider) silentFor() time.Duration {
	seen := p.LastSeen()
	if seen.IsZero() {
		return 0
	}
	return time.Since(seen)
}

// personalJobError turns a finished-but-not-completed job into a tool error.
// The Mac names why it refused in its first word (bot_not_allowed, user_stop,
// ...); the words the Bot reads are the server's, so they stay the same across
// desktop app versions and never carry the Mac's own text.
func personalJobError(status, message, action string) error {
	code := deviceErrorCode(message)
	switch code {
	case "bot_not_allowed":
		return tooloutcome.New(tooloutcome.Denied, code, "not_executed", "The person has not allowed this Bot to use their Mac. They must allow this Bot in the TOFI desktop app under Mac Device Settings. Do not retry this action or any other on this Mac; tell the person what to enable and stop.", "explain_blocker").Err()
	case "user_stop":
		return tooloutcome.New(tooloutcome.Denied, code, "unknown", "The person pressed Stop in the TOFI desktop app, which cancelled this step; whether it partly ran is unknown. Do not retry automatically and do not continue the plan. Tell the person it was stopped and wait for their instruction.", "explain_blocker").Err()
	case "user_paused":
		return tooloutcome.New(tooloutcome.Denied, code, "not_executed", "The person paused this Mac's control in the TOFI desktop app, so nothing was run. Do not retry automatically. Tell the person control is paused and that they can resume it in the desktop app when they want you to continue.", "explain_blocker").Err()
	case "user_takeover":
		return tooloutcome.New(tooloutcome.Denied, code, "not_executed", "The person took over the mouse or keyboard, so the TOFI desktop app paused this step and nothing was run. Do not retry automatically. Tell the person you stopped because they took over and ask whether to continue; capture the screen again before the next action.", "explain_blocker").Err()
	case "secure_input":
		return tooloutcome.New(tooloutcome.Denied, code, "not_executed", "A secure text field on the Mac has keyboard focus (a password field or similar), so typing was refused and nothing was typed. Do not type secrets or passwords. Ask the person to enter the value themselves, then continue.", "explain_blocker").Err()
	case "capability_not_granted":
		return capabilityNotGrantedOutcome(action).Err()
	case "folder_not_granted":
		_, rest, _ := strings.Cut(message, ":")
		rest = strings.TrimSpace(rest)
		if rest == "" {
			rest = "The person has not granted this folder to TOFI. They can add it in the TOFI desktop app's folder settings."
		}
		return tooloutcome.New(tooloutcome.Denied, code, "not_executed", rest, "explain_blocker").Err()
	case codeComputerLost:
		return tooloutcome.New(tooloutcome.Permanent, codeComputerLost, "unknown", "The computer stopped answering while this step ran. Whether the step finished on it is unknown; continue without it.", "explain_blocker").Err()
	}
	_ = status
	return errors.New(message)
}

// deviceErrorCode is the leading word of a Mac's refusal ("user_stop: ..." or "user_stop").
func deviceErrorCode(message string) string {
	message = strings.TrimSpace(message)
	if i := strings.IndexAny(message, ": \t\n"); i >= 0 {
		message = message[:i]
	}
	return strings.ToLower(message)
}

// capabilityNotGrantedOutcome names the capability a Mac has not granted and
// where the person turns it on. It serves both the server's own check and the
// Mac's refusal.
func capabilityNotGrantedOutcome(action string) tooloutcome.Outcome {
	what := "the capability for " + action
	switch {
	case action == "desktop.capture" || action == "screen.capture":
		what = "Screen capture (" + action + "), which also needs the macOS Screen Recording permission"
	case strings.HasPrefix(action, "desktop."):
		what = "Desktop control (" + action + "), which also needs the macOS Accessibility permission"
	case action == "sandbox.exec":
		what = "Command execution (sandbox.exec)"
	case strings.HasPrefix(action, "files."):
		what = "File access (" + action + ")"
	}
	return tooloutcome.New(tooloutcome.Denied, "capability_not_granted", "not_executed", "This Mac has not granted "+what+" to TOFI. The person can turn it on in the TOFI desktop app under Mac Device Settings. Do not retry; tell the person what to enable, or continue with another computer.", "explain_blocker")
}

// ---- store helpers ----

// deviceWakes lets a long-polling device learn of a new job at once.
type deviceWakes struct {
	mu sync.Mutex
	ch map[string]chan struct{}
}

var wakes = &deviceWakes{ch: map[string]chan struct{}{}}

func (w *deviceWakes) channel(id string) chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.ch[id]
	if c == nil {
		c = make(chan struct{}, 1)
		w.ch[id] = c
	}
	return c
}

func (s *Store) wakeDevice(id string) {
	select {
	case wakes.channel(id) <- struct{}{}:
	default:
	}
}

// cancelJob ends an open job and reports whether the computer had already
// claimed it (so the step may have run).
func (s *Store) cancelJob(id, reason string) (wasClaimed bool) {
	tx, err := s.db.Begin()
	if err != nil {
		return true // cannot tell: assume it may have run
	}
	defer tx.Rollback()
	var status string
	if tx.QueryRow(`SELECT status FROM computer_jobs WHERE id=?`, id).Scan(&status) != nil {
		return true
	}
	if status != "pending" && status != "running" {
		return status == "completed" // already finished; nothing to cancel
	}
	if _, err = tx.Exec(`UPDATE computer_jobs SET status='cancelled',error=?,completed_at=? WHERE id=? AND status IN ('pending','running')`, reason, now(), id); err != nil {
		return true
	}
	_ = tx.Commit()
	return status == "running"
}

// cancelDeviceJobs ends every open job of a computer, as the Mac is gone.
func (s *Store) cancelDeviceJobs(deviceID, reason string) {
	_, _ = s.db.Exec(`UPDATE computer_jobs SET status='cancelled',error=?,completed_at=? WHERE device_id=? AND status IN ('pending','running')`, reason, now(), deviceID)
}

// stampDevice records that an authenticated device spoke now.
func (s *Store) stampDevice(id string, protocol int) {
	if protocol > 0 {
		_, _ = s.db.Exec(`UPDATE computers SET last_seen=?,protocol_version=? WHERE id=? AND revoked_at IS NULL`, now(), protocol, id)
		return
	}
	s.touchComputer(id)
}

// heartbeatComputer authenticates a device and stamps it.
func (s *Store) heartbeatComputer(id, token string, protocol int) error {
	if err := authenticateDevice(s.db, id, token); err != nil {
		return err
	}
	s.stampDevice(id, protocol)
	return nil
}

// ---- server helpers ----

// ensurePersonalSlot makes sure a non-revoked personal computer has a slot.
func (s *Server) ensurePersonalSlot(id string) *computerSlot {
	if slot := s.slotFor(id); slot != nil {
		return slot
	}
	if s.store == nil {
		return nil
	}
	row, err := s.store.computerRow(id)
	if err != nil || row.Kind != computer.RowKindPersonal {
		return nil
	}
	region := ""
	if row.Region != nil {
		region = *row.Region
	}
	if err = s.RegisterComputer(&personalProvider{s: s, id: id}, row.Name, region); err != nil {
		return nil
	}
	return s.slotFor(id)
}

// registerPersonalSlots gives every paired computer its slot after a start.
func (s *Server) registerPersonalSlots() {
	rows, err := s.store.listComputerRows()
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Kind == computer.RowKindPersonal {
			s.ensurePersonalSlot(row.ID)
		}
	}
}

// UnregisterComputer removes a computer's slot. Calls in flight end as detached.
func (s *Server) UnregisterComputer(id string) {
	s.computerMu.Lock()
	slot := s.computerSlots[id]
	if slot != nil && s.microVM != nil && s.microVM.ID() == id {
		slot = nil // the VM is detached with DetachVM
	}
	if slot != nil {
		delete(s.computerSlots, id)
	}
	s.computerMu.Unlock()
	if slot != nil {
		slot.mu.Lock()
		slot.state = slotDraining
		slot.mu.Unlock()
		slot.forceFree()
	}
}

// deviceSeen is called for every authenticated device request: a computer that
// was marked lost is attached again.
func (s *Server) deviceSeen(id string) {
	if slot := s.slotFor(id); slot != nil && slot.currentState() == slotLost {
		s.markComputerAlive(id)
	}
}

// deviceJobPayload is the claim answer: the job plus the name of the Bot, which
// the Mac shows in its own consent window. The Bot id is the key of its local allow-list.
type deviceJobPayload struct {
	ComputerJob
	BotName string `json:"bot_name,omitempty"`
}

func (s *Server) withBotName(j *ComputerJob) any {
	if j == nil {
		return nil
	}
	out := deviceJobPayload{ComputerJob: *j}
	if j.BotID != "" {
		if bot, err := s.store.GetBot(j.BotID); err == nil {
			out.BotName = bot.Name
		}
	}
	return out
}

// waitForJob long-polls: it claims a job for the device, or waits up to wait.
func (s *Server) waitForJob(ctx context.Context, id, token string, wait time.Duration) (*ComputerJob, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	wake := wakes.channel(id)
	for {
		job, err := s.store.claimComputerJob(id, token)
		if err != nil || job != nil || wait <= 0 {
			return job, err
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-deadline.C:
			return nil, nil
		case <-wake:
		case <-time.After(2 * time.Second): // stamps last_seen again while idle
		}
	}
}

// parseWait reads the optional ?wait=SECONDS of a jobs poll, capped at 25.
func parseWait(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	var n int
	for _, r := range raw {
		if r < '0' || r > '9' || n > 1000 {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return min(time.Duration(n), 25) * time.Second
}

// writeDeviceAuthErr answers a failed device request. A token that was revoked
// gets its own code, so the Mac knows to forget it instead of retrying.
func (s *Server) writeDeviceAuthErr(w http.ResponseWriter, id string, err error) {
	if err == nil || err.Error() != "unauthorized" {
		writeErr(w, 500, "storage", "device request failed")
		return
	}
	var revoked sql.NullString
	if s.store.db.QueryRow(`SELECT revoked_at FROM computers WHERE id=?`, id).Scan(&revoked) == nil && revoked.Valid {
		writeErr(w, 401, "device_revoked", "this computer was removed")
		return
	}
	writeErr(w, 401, "unauthorized", "invalid device token")
}

// heartbeatBody is what a Mac may send with a heartbeat.
type heartbeatBody struct {
	ProtocolVersion int `json:"protocol_version"`
}

func decodeHeartbeat(raw []byte) (heartbeatBody, error) {
	var v heartbeatBody
	if len(raw) == 0 {
		return v, nil
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}
