package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type desktopWaiter struct {
	BotID    string `json:"bot_id"`
	RunID    string `json:"run_id"`
	QueuedAt string `json:"queued_at"`
}

// desktopPointer describes only a confirmed graphical action, not an inferred
// or simulated cursor. Coordinates retain the screenshot space used by the Bot.
type desktopPointer struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Action    string `json:"action"`
	UpdatedAt string `json:"updated_at"`
	BotID     string `json:"bot_id"`
	RunID     string `json:"run_id"`
}

func (s *Server) recordDesktopAction(r Run, name string, args json.RawMessage) {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	if s.computerOwners[r.BotID] != r.ID {
		return
	}
	// Scroll moves the pointer inside the guest; navigation may replace the
	// screen. Neither supplies a confirmed click coordinate for this overlay.
	switch name {
	case "desktop.scroll", "desktop.start", "desktop.stop", "browser.navigate", "browser.action":
		s.desktopPointer = nil
	case "desktop.click":
		s.desktopPointer = nil
		var point struct {
			X      *int `json:"x"`
			Y      *int `json:"y"`
			Width  *int `json:"screenshot_width"`
			Height *int `json:"screenshot_height"`
		}
		if json.Unmarshal(args, &point) != nil || point.X == nil || point.Y == nil {
			return
		}
		// Without both dimensions, the guest uses its latest capture size,
		// which may have changed through another read-only viewer. Do not guess.
		if point.Width == nil || point.Height == nil {
			return
		}
		width, height := *point.Width, *point.Height
		if width < 1 || height < 1 || width > 1280 || height > 800 || *point.X < 0 || *point.Y < 0 || *point.X >= width || *point.Y >= height {
			return
		}
		s.desktopPointer = &desktopPointer{X: *point.X, Y: *point.Y, Width: width, Height: height, Action: "click", UpdatedAt: now(), BotID: r.BotID, RunID: r.ID}
	}
}

func isGraphicAction(name string) bool {
	return strings.HasPrefix(name, "desktop.") || strings.HasPrefix(name, "browser.")
}
func (s *Server) desktopChangedLocked() {
	if s.desktopChanged != nil {
		close(s.desktopChanged)
	}
	s.desktopChanged = make(chan struct{})
}
func (s *Server) waitComputerOwner(ctx context.Context, r Run) error {
	s.computerOwnerMu.Lock()
	if s.computerOwners[r.BotID] == r.ID {
		s.computerOwnerMu.Unlock()
		return ctx.Err()
	}
	// Queueing behind another run's desktop use is not this call's active
	// time; only the computer's own answer is bounded by the tool deadline.
	resumeDeadline := runtime.PauseToolDeadline(ctx)
	defer resumeDeadline()
	waiter := &desktopWaiter{BotID: r.BotID, RunID: r.ID, QueuedAt: now()}
	s.desktopWaiters = append(s.desktopWaiters, waiter)
	s.desktopChangedLocked()
	remove := func() {
		for i, v := range s.desktopWaiters {
			if v == waiter {
				s.desktopWaiters = append(s.desktopWaiters[:i], s.desktopWaiters[i+1:]...)
				s.desktopChangedLocked()
				break
			}
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			remove()
			s.computerOwnerMu.Unlock()
			return err
		}
		free := true
		for _, owner := range s.computerOwners {
			if owner != "" {
				free = false
				break
			}
		}
		if s.computerOwners[r.BotID] == r.ID || (free && len(s.desktopWaiters) > 0 && s.desktopWaiters[0] == waiter) {
			if s.computerOwners == nil {
				s.computerOwners = map[string]string{}
			}
			s.computerOwners[r.BotID] = r.ID
			remove()
			s.noteDesktopQueueLeftLocked(r.ID, time.Now())
			s.computerOwnerMu.Unlock()
			return nil
		}
		changed := s.desktopChanged
		s.computerOwnerMu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		s.computerOwnerMu.Lock()
	}
}
func (s *Server) handleDesktopOwnership(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/computer/desktop-ownership" {
		return false
	}
	if r.Method != "GET" {
		writeErr(w, 405, "method_not_allowed", "Method not allowed")
		return true
	}
	s.computerOwnerMu.Lock()
	var owner map[string]string
	for bot, run := range s.computerOwners {
		if run != "" {
			kind := "bot"
			if strings.HasPrefix(run, "human") {
				kind = "human"
			}
			owner = map[string]string{"bot_id": strings.TrimPrefix(bot, "oauth:"), "run_id": run, "kind": kind}
			break
		}
	}
	waiting := make([]desktopWaiter, 0, len(s.desktopWaiters))
	for _, v := range s.desktopWaiters {
		waiting = append(waiting, *v)
	}
	var pointer *desktopPointer
	if s.desktopPointer != nil && owner != nil && owner["kind"] == "bot" && s.desktopPointer.BotID == owner["bot_id"] && s.desktopPointer.RunID == owner["run_id"] {
		copy := *s.desktopPointer
		pointer = &copy
	}
	s.computerOwnerMu.Unlock()
	if owner != nil && owner["kind"] == "bot" && s.store != nil {
		if run, err := s.store.GetRun(owner["run_id"]); err == nil && run.BotID == owner["bot_id"] {
			owner["conversation_id"] = run.ConversationID
		}
	}
	writeJSON(w, 200, map[string]any{"resource": "shared-desktop", "owner": owner, "waiting": waiting, "pointer": pointer})
	return true
}

// Handoff yields only the graphical resource. Bash/terminal activity remains
// independent. Wait for an in-flight screen operation before releasing its hold.
func (s *Server) yieldComputerOwner(r Run) error {
	if !s.ownsComputer(r) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	lease := s.computerLease(r.BotID)
	if err := lockComputerLease(ctx, lease); err != nil {
		return err
	}
	defer lease.Unlock()
	if !s.ownsComputer(r) {
		return nil
	}
	if s.microVM != nil {
		if _, err := s.microVM.Action(ctx, computer.Action{BotID: r.BotID, RunID: r.ID, Name: "desktop.release", Source: "model", Args: json.RawMessage(`{}`)}); err != nil {
			return err
		}
	}
	s.releaseComputerOwner(r.BotID, r.ID)
	return nil
}
func (s *Server) terminalLease(botID string) *sync.Mutex { return s.resourceLease("terminal:" + botID) }
func (s *Server) claimTerminalOwner(botID, runID string) bool {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	if s.terminalOwners == nil {
		s.terminalOwners = map[string]string{}
	}
	owner := s.terminalOwners[botID]
	if owner != "" && owner != runID {
		return false
	}
	s.terminalOwners[botID] = runID
	return true
}
func (s *Server) terminalAvailable(botID, runID string) bool {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	owner := s.terminalOwners[botID]
	return owner == "" || owner == runID
}

func (s *Server) desktopObservedFor(r Run) bool {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	return s.desktopObserved[r.ID] && s.computerOwners[r.BotID] == r.ID
}
func (s *Server) markDesktopObserved(r Run) {
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	if s.computerOwners[r.BotID] == r.ID {
		if s.desktopObserved == nil {
			s.desktopObserved = map[string]bool{}
		}
		s.desktopObserved[r.ID] = true
	}
}
func actionNeedsObservation(name string) bool {
	switch name {
	case "desktop.click", "desktop.type", "desktop.key", "desktop.scroll", "browser.action":
		return true
	}
	return false
}
