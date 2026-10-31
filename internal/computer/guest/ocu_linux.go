//go:build linux

package guest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ocuIdle is how long a Bot's engine process may sit unused before it is
// stopped (a fixed constant, not a setting). The next call starts a new one.
// A var only so a test can shorten it.
var ocuIdle = 10 * time.Minute

// ocuHost holds one engine process per Bot (its element indexes belong to the
// Bot's last get_app_state, so two Bots must not share a process); it lives
// on the Service. It reuses the stdio MCP session of the browser engine,
// including replies routed by JSON-RPC id.
type ocuHost struct {
	mu       sync.Mutex
	sessions map[string]*playwrightSession
	// callMu serializes calls: they drive one display and one pointer.
	callMu sync.Mutex
}

func (s *Service) desktopMCP(ctx context.Context, botID string, in ocuCallArgs) (any, error) {
	args, err := checkOCUCall(in)
	if err != nil {
		return nil, err
	}
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	if d.runtimeDir == "" || d.display == "" {
		return nil, errors.New("desktop is not running")
	}
	if info, err := os.Stat(ocuBinary); err != nil || info.IsDir() {
		return nil, errOCUMissing
	}
	h := &s.ocu
	h.callMu.Lock()
	defer h.callMu.Unlock()
	sess, err := s.ocuFor(ctx, botID, d)
	if err != nil {
		return nil, err
	}
	raw, err := sess.call(ctx, "tools/call", map[string]any{"name": in.Tool, "arguments": args})
	if err != nil {
		s.dropOCU(sess)
		return nil, err
	}
	app, _ := args["app"].(string)
	return ocuResult(in.Tool, raw, func(text string) string {
		if app != "" && hasCheckable(text) {
			if states, err := ocuCheckStates(ctx, s.root, d, app); err == nil {
				text, _ = annotateCheckStates(text, states)
			}
		}
		return text
	})
}

func (s *Service) ocuFor(ctx context.Context, botID string, d *desktop) (*playwrightSession, error) {
	h := &s.ocu
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur := h.sessions[botID]; cur != nil {
		select {
		case <-cur.dead:
		default:
			// A new desktop (new bus) needs a new process.
			if cur.key == d.runtimeDir+"|"+d.display && cur.idle.Reset(ocuIdle) {
				return cur, nil
			}
		}
		cur.stop()
		delete(h.sessions, botID)
	}
	sess, err := startOCU(ctx, s.root, d)
	if err != nil {
		return nil, err
	}
	sess.key = d.runtimeDir + "|" + d.display
	sess.idle = time.AfterFunc(ocuIdle, func() { s.dropOCUFor(botID, sess) })
	if h.sessions == nil {
		h.sessions = map[string]*playwrightSession{}
	}
	h.sessions[botID] = sess
	return sess, nil
}

func (s *Service) dropOCU(sess *playwrightSession) {
	h := &s.ocu
	h.mu.Lock()
	defer h.mu.Unlock()
	sess.stop()
	for id, cur := range h.sessions {
		if cur == sess {
			delete(h.sessions, id)
		}
	}
}

func (s *Service) dropOCUFor(botID string, sess *playwrightSession) {
	h := &s.ocu
	h.mu.Lock()
	defer h.mu.Unlock()
	sess.stop()
	if h.sessions[botID] == sess {
		delete(h.sessions, botID)
	}
}

// stopOCU ends every engine process when the desktop goes away.
func (s *Service) stopOCU() {
	h := &s.ocu
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, sess := range h.sessions {
		sess.stop()
		delete(h.sessions, k)
	}
}

// startOCU starts the engine on the desktop's display and session bus. The
// engine looks for a session by itself only when these are missing, so the
// desktop's own are set explicitly.
func startOCU(ctx context.Context, root string, d *desktop) (*playwrightSession, error) {
	cmd := exec.Command(ocuBinary, "mcp")
	cmd.Dir = d.botDir
	env := append(cleanEnv(root, d.botDir), "DISPLAY="+d.display)
	cmd.Env = withDesktopA11yEnv(env, d.runtimeDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return startStdioMCP(ctx, "desktop engine", cmd)
}
