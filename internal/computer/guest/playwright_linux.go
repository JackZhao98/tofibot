//go:build linux

package guest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	playwrightStartTimeout = 40 * time.Second
	playwrightCallTimeout  = 100 * time.Second
	playwrightLineMax      = 8 << 20
)

// playwrightIdle is how long a Bot's server may sit unused before its process
// is stopped (a fixed constant on purpose: not a setting). The next call starts
// a new one. A var only so a test can shorten it.
var playwrightIdle = 10 * time.Minute

// playwrightSession is one running @playwright/mcp process, bound to the Bot
// dir (its environment) and the Chrome DevTools port it was started for. A new
// Chrome (new port) gets a new session.
type playwrightSession struct {
	key    string
	label  string // names the engine in errors; empty means the browser engine
	port   int
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex // one request at a time
	dead   chan struct{}
	nextID int
	log    *limitedBuffer
	idle   *time.Timer

	pendMu  sync.Mutex
	pending map[int]chan json.RawMessage // replies routed by JSON-RPC id
}

// playwrightHost holds one session per Bot dir; it lives on the Service.
type playwrightHost struct {
	mu       sync.Mutex
	sessions map[string]*playwrightSession
}

func (s *Service) browserMCP(ctx context.Context, botID string, in playwrightCallArgs) (any, error) {
	args, err := checkPlaywrightCall(s.root, in)
	if err != nil {
		return nil, err
	}
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	if d.remotePort <= 0 {
		return nil, errors.New("browser debugging endpoint is unavailable")
	}
	if _, err := os.Stat(filepath.Join(playwrightMCPDir, "node_modules", "@playwright", "mcp", "cli.js")); err != nil {
		return nil, errPlaywrightMissing
	}
	// Serialize calls: the server keeps one page state, and two interleaved
	// actions would act on each other's snapshot.
	s.browserImplicitMu.Lock()
	defer s.browserImplicitMu.Unlock()
	sess, err := s.playwrightFor(ctx, d.botDir, d.remotePort)
	if err != nil {
		return nil, err
	}
	raw, err := sess.call(ctx, "tools/call", map[string]any{"name": in.Tool, "arguments": args})
	if err != nil {
		s.dropPlaywright(sess)
		return nil, err
	}
	return playwrightResult(s.root, in.Tool, raw)
}

func (s *Service) playwrightFor(ctx context.Context, botDir string, port int) (*playwrightSession, error) {
	h := &s.playwright
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur := h.sessions[botDir]; cur != nil {
		select {
		case <-cur.dead:
		default:
			if cur.port == port && cur.idle.Reset(playwrightIdle) {
				return cur, nil
			}
		}
		cur.stop()
		delete(h.sessions, botDir)
	}
	sess, err := startPlaywright(ctx, s.root, botDir, port)
	if err != nil {
		return nil, err
	}
	sess.key = botDir
	sess.idle = time.AfterFunc(playwrightIdle, func() { s.dropPlaywright(sess) })
	if h.sessions == nil {
		h.sessions = map[string]*playwrightSession{}
	}
	h.sessions[botDir] = sess
	return sess, nil
}

func (s *Service) dropPlaywright(sess *playwrightSession) {
	h := &s.playwright
	h.mu.Lock()
	defer h.mu.Unlock()
	sess.stop()
	if h.sessions[sess.key] == sess {
		delete(h.sessions, sess.key)
	}
}

// stopPlaywright ends every session when the desktop goes away.
func (s *Service) stopPlaywright() {
	h := &s.playwright
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, sess := range h.sessions {
		sess.stop()
		delete(h.sessions, k)
	}
}

func startPlaywright(ctx context.Context, root, botDir string, port int) (*playwrightSession, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("browser engine needs node in the computer image")
	}
	cli := filepath.Join(playwrightMCPDir, "node_modules", "@playwright", "mcp", "cli.js")
	cmd := exec.Command(node, cli,
		"--cdp-endpoint", playwrightEndpoint(port),
		"--image-responses", "omit",
		"--no-webmcp",
		"--output-dir", playwrightOutputDir(root),
		"--timeout-action", "8000",
		"--timeout-navigation", "30000",
	)
	// The workspace root is the only place the server may read files from.
	cmd.Dir = root
	env := append(cleanEnv(root, botDir), "PLAYWRIGHT_MCP_NO_TELEMETRY=1")
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := os.MkdirAll(playwrightOutputDir(root), 0o700); err != nil {
		return nil, err
	}
	sess, err := startStdioMCP(ctx, "browser engine", cmd)
	if err != nil {
		return nil, err
	}
	sess.port = port
	return sess, nil
}

// startStdioMCP starts a prepared command as an MCP server on stdio and
// completes the initialize handshake. label names the engine in errors.
func startStdioMCP(ctx context.Context, label string, cmd *exec.Cmd) (*playwrightSession, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	logBuf := &limitedBuffer{limit: 16 * 1024, tail: true}
	cmd.Stderr = logBuf
	cmd.WaitDelay = 1500 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sess := &playwrightSession{label: label, cmd: cmd, stdin: stdin, pending: map[int]chan json.RawMessage{}, dead: make(chan struct{}), log: logBuf}
	go func() {
		reader := bufio.NewReaderSize(stdout, 64*1024)
		for {
			line, err := readLimitedLine(reader, playwrightLineMax)
			if len(line) > 0 {
				sess.route(line)
			}
			if err != nil {
				close(sess.dead)
				_ = cmd.Wait()
				return
			}
		}
	}()
	startCtx, cancel := context.WithTimeout(ctx, playwrightStartTimeout)
	defer cancel()
	if _, err := sess.call(startCtx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tofi-guest", "version": "1"},
	}); err != nil {
		sess.stop()
		return nil, fmt.Errorf("%s did not start: %w%s", label, err, tailNote(logBuf))
	}
	if err := sess.notify("notifications/initialized"); err != nil {
		sess.stop()
		return nil, err
	}
	return sess, nil
}

func tailNote(b *limitedBuffer) string {
	text := strings.TrimSpace(b.String())
	if text == "" {
		return ""
	}
	if len(text) > 600 {
		text = text[len(text)-600:]
	}
	return " (" + strings.ReplaceAll(text, "\n", " ") + ")"
}

func readLimitedLine(r *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if len(out)+len(chunk) > max {
			return nil, errors.New("browser engine line too long")
		}
		out = append(out, chunk...)
		if err != nil {
			return out, err
		}
		if !isPrefix {
			return out, nil
		}
	}
}

// route hands a reply to the call waiting for its id. A line without an id (a
// server notification) or for a call that gave up is dropped; neither can ever
// take the place of a reply, because each call has its own one-slot channel.
func (p *playwrightSession) route(line []byte) {
	var head struct {
		ID     *int   `json:"id"`
		Method string `json:"method"`
	}
	if json.Unmarshal(line, &head) != nil || head.ID == nil || head.Method != "" {
		return
	}
	p.pendMu.Lock()
	ch := p.pending[*head.ID]
	delete(p.pending, *head.ID)
	p.pendMu.Unlock()
	if ch != nil {
		ch <- json.RawMessage(append([]byte(nil), line...)) // buffered 1, sent once
	}
}

func (p *playwrightSession) name() string {
	if p.label == "" {
		return "browser engine"
	}
	return p.label
}

func (p *playwrightSession) notify(method string) error {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	_, err := p.stdin.Write(append(b, '\n'))
	return err
}

// call sends one request and waits for its reply. A tool error the server
// reports (isError) is a result, not a failure; a JSON-RPC error is one.
func (p *playwrightSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	id := p.nextID
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	reply := make(chan json.RawMessage, 1)
	p.pendMu.Lock()
	p.pending[id] = reply
	p.pendMu.Unlock()
	defer func() {
		p.pendMu.Lock()
		delete(p.pending, id)
		p.pendMu.Unlock()
	}()
	if _, err := p.stdin.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("%s is not running: %w", p.name(), err)
	}
	timeout := time.NewTimer(playwrightCallTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("%s did not answer in time", p.name())
		case <-p.dead:
			return nil, errors.New(p.name() + " stopped" + tailNote(p.log))
		case line := <-reply:
			var msg struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(line, &msg) != nil {
				return nil, fmt.Errorf("%s sent an unreadable reply", p.name())
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("%s: %s", p.name(), msg.Error.Message)
			}
			return msg.Result, nil
		}
	}
}

func (p *playwrightSession) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.stdin.Close()
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	go func(cmd *exec.Cmd, dead chan struct{}) {
		select {
		case <-dead:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}(p.cmd, p.dead)
}
