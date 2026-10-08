package guest

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

// The host manager hibernates an idle computer by pausing the VM and saving a
// Firecracker snapshot. It asks the guest first through POST /v1/quiesce: the
// guest reports work the host cannot see (a desktop action in flight, a run
// lease, human control, a live viewer, a terminal that is running a job or
// still printing) and otherwise flushes the filesystems so the workspace disk
// is complete on the host before the memory image is written.

// terminalQuietDefault is how long a terminal must be silent to count as idle
// when the manager does not say.
const terminalQuietDefault = 60 * time.Second

type quiesceRequest struct {
	// IdleSeconds is the manager's idle window; terminal output inside it
	// still counts as activity.
	IdleSeconds int `json:"idle_seconds"`
}

func (s *Service) handleQuiesce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req quiesceRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err == nil && len(body) > 0 {
		err = json.Unmarshal(body, &req)
	}
	if err != nil || req.IdleSeconds < 0 || req.IdleSeconds > 86400 {
		writeError(w, http.StatusBadRequest, "invalid quiesce request")
		return
	}
	quiet := terminalQuietDefault
	if req.IdleSeconds > 0 {
		quiet = time.Duration(req.IdleSeconds) * time.Second
	}
	if busy := s.busyReasons(time.Now(), quiet); len(busy) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"idle": false, "busy": busy})
		return
	}
	// Flush dirty pages so the host disk holds everything the saved memory
	// believes is on it. A sync of a mostly clean workspace is quick.
	unix.Sync()
	writeJSON(w, http.StatusOK, map[string]any{"idle": true})
}

// busyReasons lists in-guest work that must not be paused by hibernation.
func (s *Service) busyReasons(now time.Time, quiet time.Duration) []string {
	reasons := map[string]bool{}
	var terminals []*terminal
	s.mu.Lock()
	seen := map[*desktop]bool{}
	desktops := make([]*desktop, 0, len(s.desktops)+1)
	if s.sharedDesktop != nil {
		desktops = append(desktops, s.sharedDesktop)
	}
	for _, d := range s.desktops {
		desktops = append(desktops, d)
	}
	for _, d := range desktops {
		if d == nil || seen[d] {
			continue
		}
		seen[d] = true
		switch {
		case d.inFlight > 0:
			reasons["desktop_operation"] = true
		case d.stopping:
			reasons["desktop_stopping"] = true
		case !d.readyClosed:
			reasons["desktop_starting"] = true
		}
		if d.streamViewers > 0 || d.streamActive {
			reasons["viewer"] = true
		}
	}
	if s.hasAnyActiveHoldLocked(now) {
		reasons["run_lease"] = true
	}
	if s.activeInput != nil || len(s.inputs) > 0 {
		reasons["human_control"] = true
	}
	for _, t := range s.terminals {
		terminals = append(terminals, t)
	}
	oauth := s.oauth
	s.mu.Unlock()

	for _, t := range terminals {
		t.mu.Lock()
		open := !t.closed && !t.exited
		recent := !t.lastOutput.IsZero() && now.Sub(t.lastOutput) < quiet
		var shell int
		if t.cmd != nil && t.cmd.Process != nil {
			shell = t.cmd.Process.Pid
		}
		ptmx := t.pty
		t.mu.Unlock()
		if !open {
			continue
		}
		if recent {
			reasons["terminal_output"] = true
		}
		// An interactive shell runs a job in its own foreground process group.
		if ptmx != nil && shell > 0 {
			if group, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP); err == nil && group > 0 && group != shell {
				reasons["terminal_job"] = true
			}
		}
	}
	if oauth != nil {
		oauth.mu.Lock()
		pending := oauth.status == "" || oauth.status == "pending"
		oauth.mu.Unlock()
		if pending {
			reasons["oauth"] = true
		}
	}
	out := make([]string, 0, len(reasons))
	for reason := range reasons {
		out = append(out, reason)
	}
	sort.Strings(out)
	return out
}

// noteResume runs when the idle reaper sees its own ticks stall: the VM was
// paused (hibernated) and restored. Time spent hibernated is not desktop use,
// but it is not idleness either; restart every idle clock from now so a
// restored desktop is not reaped the moment the guest runs again.
func (s *Service) noteResume(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.desktops {
		if d != nil && !d.stopping {
			d.lastActivity = now
		}
	}
	if d := s.sharedDesktop; d != nil && !d.stopping {
		d.lastActivity = now
	}
}
