package guest

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
)

const (
	terminalRingBytes       = 256 * 1024
	terminalMaxPerBot       = 8
	terminalMaxGlobal       = 32
	terminalClosedRetention = 5 * time.Minute
	terminalMaxClosed       = 64
	terminalMaxDimension    = 4096
	terminalDrainTimeout    = 250 * time.Millisecond
	terminalWriteTimeout    = 2 * time.Second
)

type terminal struct {
	id, botID, runID, command string
	cmd                       *exec.Cmd
	pty                       *os.File
	readDone                  chan struct{}
	mu                        sync.Mutex
	writeMu                   sync.Mutex
	buf                       []byte
	base, end                 uint64
	cols, rows                int
	closed, exited            bool
	exitCode                  int
	closeReason               string
	createdAt                 time.Time
	closedAt                  time.Time
}

func (t *terminal) appendOutput(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	t.end += uint64(len(p))
	if len(t.buf) > terminalRingBytes {
		n := len(t.buf) - terminalRingBytes
		copy(t.buf, t.buf[n:])
		t.buf = t.buf[:terminalRingBytes]
		t.base += uint64(n)
	}
}

func (t *terminal) snapshot(cursor uint64, maxBytes int) map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	if maxBytes <= 0 || maxBytes > MaxOutput {
		maxBytes = MaxOutput
	}
	gap := cursor < t.base
	if gap {
		cursor = t.base
	}
	if cursor > t.end {
		cursor = t.end
	}
	i := int(cursor - t.base)
	j := min(i+maxBytes, len(t.buf))
	nextCursor := cursor + uint64(j-i)
	return map[string]any{
		"id": t.id, "terminal_id": t.id,
		"data_base64": base64.StdEncoding.EncodeToString(t.buf[i:j]),
		"cursor":      cursor, "next_cursor": nextCursor, "gap": gap,
		"closed": t.closed, "exited": t.exited, "exit_code": t.exitCode,
		"close_reason": t.closeReason, "drained": t.closed && nextCursor == t.end,
	}
}

func (t *terminal) summary() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{
		"id": t.id, "terminal_id": t.id, "bot_id": t.botID, "command": t.command,
		"run_id": t.runID, "created_at": t.createdAt.UTC().Format(time.RFC3339Nano),
		"cols": t.cols, "rows": t.rows, "cursor": t.end, "closed": t.closed,
		"exited": t.exited, "exit_code": t.exitCode, "close_reason": t.closeReason,
	}
}

type terminalOpenArgs struct {
	Command string `json:"command"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}
type terminalReadArgs struct {
	TerminalID string `json:"terminal_id"`
	Cursor     uint64 `json:"cursor"`
	MaxBytes   int    `json:"max_bytes"`
}
type terminalIDArgs struct {
	TerminalID string `json:"terminal_id"`
}
type terminalWriteArgs struct {
	TerminalID string `json:"terminal_id"`
	Data       string `json:"data"`
	DataBase64 string `json:"data_base64"`
}
type terminalResizeArgs struct {
	TerminalID string `json:"terminal_id"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
}

func validTerminalSize(cols, rows int) bool {
	return cols > 0 && rows > 0 && cols <= terminalMaxDimension && rows <= terminalMaxDimension
}

func (s *Service) terminalOpen(_ context.Context, botID, runID string, a terminalOpenArgs) (map[string]any, error) {
	if strings.TrimSpace(a.Command) == "" {
		return nil, errors.New("command is required")
	}
	if a.Cols == 0 {
		a.Cols = 80
	}
	if a.Rows == 0 {
		a.Rows = 24
	}
	if !validTerminalSize(a.Cols, a.Rows) {
		return nil, fmt.Errorf("cols and rows must be between 1 and %d", terminalMaxDimension)
	}
	bot, err := s.botDir(botID)
	if err != nil {
		return nil, err
	}
	cwd := bot
	if alias, ok := s.workspaceAlias(botID); ok {
		cwd = alias
	}

	s.mu.Lock()
	s.pruneTerminalsLocked(time.Now())
	active, botActive := 0, 0
	for _, x := range s.terminals {
		x.mu.Lock()
		closed := x.closed
		x.mu.Unlock()
		if !closed {
			active++
			if x.botID == botID {
				botActive++
			}
		}
	}
	if botActive >= terminalMaxPerBot {
		s.mu.Unlock()
		return nil, fmt.Errorf("terminal limit reached for Bot (%d)", terminalMaxPerBot)
	}
	if active >= terminalMaxGlobal {
		s.mu.Unlock()
		return nil, fmt.Errorf("global terminal limit reached (%d)", terminalMaxGlobal)
	}
	// Reserve before starting so concurrent opens cannot race past the limits.
	t := &terminal{id: uuid.NewString(), botID: botID, runID: runID, command: a.Command, cols: a.Cols, rows: a.Rows, exitCode: -1, createdAt: time.Now(), readDone: make(chan struct{})}
	s.terminals[t.id] = t
	s.mu.Unlock()

	// The action context ends after open returns; the PTY owns its lifecycle.
	cmd := exec.Command("/bin/bash", "-lc", a.Command)
	cmd.Dir = cwd
	env := setEnv(cleanEnv(s.root, bot), "PWD", cwd)
	env = setEnv(env, "TMPDIR", filepath.Join(cwd, "tmp"))
	cmd.Env = append(env, "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(a.Cols), Rows: uint16(a.Rows)})
	if err != nil {
		s.mu.Lock()
		delete(s.terminals, t.id)
		s.mu.Unlock()
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	t.mu.Lock()
	t.cmd, t.pty = cmd, ptmx
	closedDuringStart := t.closed
	if closedDuringStart {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = ptmx.Close()
	}
	t.mu.Unlock()
	go t.readOutput()
	go t.wait()
	return t.summary(), nil
}

func (t *terminal) readOutput() {
	defer close(t.readDone)
	defer t.pty.Close()
	b := make([]byte, 16*1024)
	for {
		n, err := t.pty.Read(b)
		if n > 0 {
			t.appendOutput(b[:n])
		}
		if err != nil {
			return
		}
	}
}

func (t *terminal) wait() {
	err := t.cmd.Wait()
	// Wait can return before the master side drains its final buffered bytes.
	// Give it a bounded drain window. A detached descendant may inherit the
	// slave fd indefinitely, so waiting for EOF without a deadline leaks the
	// session slot even though the requested command has exited.
	select {
	case <-t.readDone:
	case <-time.After(terminalDrainTimeout):
		_ = t.pty.Close()
		// Closing the master normally wakes the reader, but Linux can leave a
		// read blocked while a detached descendant still owns the slave fd.
		// Never hold terminal exit publication on that goroutine.
		select {
		case <-t.readDone:
		case <-time.After(terminalDrainTimeout):
		}
	}
	code := 0
	if err != nil {
		code = -1
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exited, t.exitCode = true, code
	if !t.closed {
		t.closed, t.closeReason = true, "process_exit"
	}
	if t.closedAt.IsZero() {
		t.closedAt = time.Now()
	}
}

func (s *Service) pruneTerminalsLocked(now time.Time) {
	type oldTerm struct {
		id string
		at time.Time
	}
	var closed []oldTerm
	for id, t := range s.terminals {
		t.mu.Lock()
		isClosed, at := t.closed, t.closedAt
		t.mu.Unlock()
		if !isClosed {
			continue
		}
		if !at.IsZero() && now.Sub(at) >= terminalClosedRetention {
			delete(s.terminals, id)
			continue
		}
		closed = append(closed, oldTerm{id, at})
	}
	if len(closed) > terminalMaxClosed {
		sort.Slice(closed, func(i, j int) bool { return closed[i].at.Before(closed[j].at) })
		for _, x := range closed[:len(closed)-terminalMaxClosed] {
			delete(s.terminals, x.id)
		}
	}
}

func (s *Service) getTerminal(bot, id string) (*terminal, error) {
	s.mu.Lock()
	s.pruneTerminalsLocked(time.Now())
	t := s.terminals[id]
	s.mu.Unlock()
	if t == nil || t.botID != bot {
		return nil, errors.New("terminal not found")
	}
	return t, nil
}

func (s *Service) terminalWrite(bot, runID, source string, a terminalWriteArgs) (map[string]any, error) {
	t, err := s.getTerminal(bot, a.TerminalID)
	if err != nil {
		return nil, err
	}
	if a.Data != "" && a.DataBase64 != "" {
		return nil, errors.New("provide data or data_base64, not both")
	}
	data := []byte(a.Data)
	if a.DataBase64 != "" {
		data, err = base64.StdEncoding.DecodeString(a.DataBase64)
		if err != nil {
			return nil, errors.New("data_base64 is invalid")
		}
	}
	if len(data) > MaxActionBody {
		return nil, errors.New("terminal write too large")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.mu.Lock()
	if t.closed || t.pty == nil {
		t.mu.Unlock()
		return nil, errors.New("terminal is closed")
	}
	// A model continuing an existing terminal transfers cancellation ownership
	// to its run. Human input detaches it from an old model run so that run's
	// deferred cleanup cannot kill a terminal the user has taken over.
	if source == ActionSourceModel {
		t.runID = runID
	} else if source == ActionSourceHuman {
		t.runID = runID
	}
	ptmx := t.pty
	t.mu.Unlock()
	if err := ptmx.SetWriteDeadline(time.Now().Add(terminalWriteTimeout)); err != nil {
		return nil, fmt.Errorf("set terminal write deadline: %w", err)
	}
	defer ptmx.SetWriteDeadline(time.Time{})
	written := 0
	for written < len(data) {
		n, writeErr := ptmx.Write(data[written:])
		written += n
		if writeErr != nil {
			return map[string]any{"id": t.id, "terminal_id": t.id, "written": written}, writeErr
		}
		if n == 0 {
			return map[string]any{"id": t.id, "terminal_id": t.id, "written": written}, io.ErrShortWrite
		}
	}
	return map[string]any{"id": t.id, "terminal_id": t.id, "written": written}, nil
}

func (s *Service) terminalRead(bot string, a terminalReadArgs) (map[string]any, error) {
	t, err := s.getTerminal(bot, a.TerminalID)
	if err != nil {
		return nil, err
	}
	return t.snapshot(a.Cursor, a.MaxBytes), nil
}

func (s *Service) terminalList(bot string) []map[string]any {
	s.mu.Lock()
	s.pruneTerminalsLocked(time.Now())
	var terms []*terminal
	for _, t := range s.terminals {
		if t.botID == bot {
			terms = append(terms, t)
		}
	}
	s.mu.Unlock()
	sort.Slice(terms, func(i, j int) bool { return terms[i].createdAt.Before(terms[j].createdAt) })
	out := make([]map[string]any, 0, len(terms))
	for _, t := range terms {
		out = append(out, t.summary())
	}
	return out
}

func (s *Service) terminalResize(bot string, a terminalResizeArgs) (map[string]any, error) {
	t, err := s.getTerminal(bot, a.TerminalID)
	if err != nil {
		return nil, err
	}
	if !validTerminalSize(a.Cols, a.Rows) {
		return nil, fmt.Errorf("cols and rows must be between 1 and %d", terminalMaxDimension)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.pty == nil {
		return nil, errors.New("terminal is closed")
	}
	if err := pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(a.Cols), Rows: uint16(a.Rows)}); err != nil {
		return nil, fmt.Errorf("resize terminal: %w", err)
	}
	t.cols, t.rows = a.Cols, a.Rows
	return map[string]any{"id": t.id, "terminal_id": t.id, "cols": a.Cols, "rows": a.Rows}, nil
}

func (s *Service) terminalClose(bot string, a terminalIDArgs) (map[string]any, error) {
	t, err := s.getTerminal(bot, a.TerminalID)
	if err != nil {
		return nil, err
	}
	t.close("closed")
	return map[string]any{"id": t.id, "terminal_id": t.id, "closed": true}, nil
}

func (t *terminal) close(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed, t.closeReason, t.closedAt = true, reason, time.Now()
	// creack/pty makes the child a session leader, so -pid reaches its group.
	if t.cmd != nil && t.cmd.Process != nil {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
	}
	if t.pty != nil {
		_ = t.pty.Close()
	}
}

func (s *Service) terminalCancelRun(bot, runID string) (map[string]any, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("run_id is required")
	}
	s.mu.Lock()
	terms := make([]*terminal, 0)
	for _, t := range s.terminals {
		t.mu.Lock()
		matches := t.botID == bot && t.runID == runID && !t.closed
		t.mu.Unlock()
		if matches {
			terms = append(terms, t)
		}
	}
	s.mu.Unlock()
	cancelled := 0
	for _, t := range terms {
		// Recheck ownership under the terminal lock: a human or newer model may
		// have taken over after collection but before cancellation.
		t.mu.Lock()
		if t.botID == bot && t.runID == runID && !t.closed {
			cancelled++
			t.closed, t.closeReason, t.closedAt = true, "run_cancelled", time.Now()
			if t.cmd != nil && t.cmd.Process != nil {
				_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
			}
			if t.pty != nil {
				_ = t.pty.Close()
			}
		}
		t.mu.Unlock()
	}
	return map[string]any{"cancelled": cancelled}, nil
}
