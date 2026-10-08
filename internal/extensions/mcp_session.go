package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpReadinessTTL bounds how long one successful server/discover handshake
// (or answered tools/call) proves endpoint readiness. A human approval wait
// longer than this window forces a fresh handshake before dispatch.
const mcpReadinessTTL = 30 * time.Second

type mcpReadyEntry struct {
	fingerprint string
	at          time.Time
}

// mcpReadinessCache is process-wide and keyed by server name. An entry only
// counts for the exact configuration fingerprint that produced it, so a
// settings edit invalidates it without explicit bookkeeping.
type mcpReadinessCache struct {
	mu          sync.Mutex
	ready       map[string]mcpReadyEntry
	invalidated map[string]time.Time
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Manager) markMCPReady(name string, cfg MCPServerConfig) {
	fingerprint := metadataFingerprint(name, cfg)
	if fingerprint == "" {
		return
	}
	c := &m.readiness
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready == nil {
		c.ready = map[string]mcpReadyEntry{}
	}
	c.ready[name] = mcpReadyEntry{fingerprint: fingerprint, at: m.clock()}
}

func (m *Manager) mcpReadyFresh(name string, cfg MCPServerConfig) bool {
	fingerprint := metadataFingerprint(name, cfg)
	c := &m.readiness
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.ready[name]
	return ok && fingerprint != "" && entry.fingerprint == fingerprint && m.clock().Sub(entry.at) < mcpReadinessTTL
}

// invalidateMCPReadiness drops cached readiness after a connection-level
// failure. Run sessions established before this moment stop counting as
// proof too; they must be re-proven by a fresh handshake.
func (m *Manager) invalidateMCPReadiness(name string) {
	c := &m.readiness
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.ready, name)
	if c.invalidated == nil {
		c.invalidated = map[string]time.Time{}
	}
	c.invalidated[name] = m.clock()
}

func (m *Manager) mcpProofCurrent(name string, at time.Time) bool {
	c := &m.readiness
	c.mu.Lock()
	invalidated, ok := c.invalidated[name]
	c.mu.Unlock()
	return (!ok || !at.Before(invalidated)) && m.clock().Sub(at) < mcpReadinessTTL
}

// mcpConnectionFailure separates transport failures from answers. A JSON-RPC
// error from the endpoint means it was reached; the SDK's local transport
// codes (rejected by transport, client/server closing) and a closed
// connection mean it was not.
func mcpConnectionFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) {
		return true
	}
	switch rpc.Code {
	case -32005, -32004, -32003: // rejected by transport, server closing, client closing
		return true
	}
	return false
}

// mcpSessionSlot is one run's reusable MCP session for one server. Discovery,
// readiness and calls share it, so a call needs at most one handshake.
type mcpSessionSlot struct {
	m    *Manager
	life context.Context
	name string
	cfg  MCPServerConfig

	openMu sync.Mutex // Serializes handshakes so parallel calls share one.
	mu     sync.Mutex
	cli    *mcp.ClientSession
	ended  <-chan struct{}
	at     time.Time
	all    []*mcp.ClientSession
}

func newMCPSessionSlot(m *Manager, life context.Context, name string, cfg MCPServerConfig) *mcpSessionSlot {
	return &mcpSessionSlot{m: m, life: life, name: name, cfg: cfg}
}

// adopt makes an initialized session the slot's current one. A session that
// later ends (closed, run cancelled) is detected through Wait.
func (s *mcpSessionSlot) adopt(cli *mcp.ClientSession) {
	ended := make(chan struct{})
	go func() {
		_ = cli.Wait()
		close(ended)
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cli, s.ended, s.at = cli, ended, s.m.clock()
	s.all = append(s.all, cli)
}

// live returns the current session if it has not ended, and whether it still
// proves readiness (recent handshake or answered call, not invalidated since).
func (s *mcpSessionSlot) live() (*mcp.ClientSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cli == nil {
		return nil, false
	}
	select {
	case <-s.ended:
		s.cli = nil
		return nil, false
	default:
	}
	return s.cli, s.m.mcpProofCurrent(s.name, s.at)
}

func (s *mcpSessionSlot) touch(cli *mcp.ClientSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cli == cli {
		s.at = s.m.clock()
	}
}

// drop forgets a session after a connection failure. It stays tracked so the
// run closes it, but no later call or readiness check reuses it.
func (s *mcpSessionSlot) drop(cli *mcp.ClientSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cli == cli {
		s.cli = nil
	}
}

func (s *mcpSessionSlot) close() error {
	s.mu.Lock()
	all := s.all
	s.all, s.cli = nil, nil
	s.mu.Unlock()
	var first error
	for _, cli := range all {
		if err := cli.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// readinessCheck keeps the cheap transport check on every call and memoizes
// only the network handshake: a fresh run session or a fresh process-wide
// result is reused; otherwise one handshake runs and becomes the run session.
func (s *mcpSessionSlot) readinessCheck(ctx context.Context) (runtime.MethodReadiness, error) {
	ctx, cancel := context.WithTimeout(ctx, s.m.cfg.DiscoveryTimeout)
	defer cancel()
	if state, err := s.m.mcpTransportReadiness(ctx, s.cfg); state != runtime.MethodReady || err != nil {
		return state, err
	}
	if _, fresh := s.live(); fresh || s.m.mcpReadyFresh(s.name, s.cfg) {
		return runtime.MethodReady, nil
	}
	s.openMu.Lock()
	defer s.openMu.Unlock()
	if _, fresh := s.live(); fresh || s.m.mcpReadyFresh(s.name, s.cfg) {
		return runtime.MethodReady, nil
	}
	if err := s.life.Err(); err != nil {
		return runtime.MethodUnavailable, err
	}
	cli, err := s.m.openMCPClient(s.life, ctx, s.name, s.cfg)
	if err != nil {
		return mcpErrorReadiness(err), err
	}
	s.adopt(cli)
	return runtime.MethodReady, nil
}

// session returns the live run session, opening one only when none exists.
func (s *mcpSessionSlot) session(ctx context.Context) (*mcp.ClientSession, error) {
	if cli, _ := s.live(); cli != nil {
		return cli, nil
	}
	s.openMu.Lock()
	defer s.openMu.Unlock()
	if cli, _ := s.live(); cli != nil {
		return cli, nil
	}
	if err := s.life.Err(); err != nil {
		return nil, err
	}
	if _, err := s.m.prepareTransport(ctx, s.cfg); err != nil {
		return nil, err
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, s.m.cfg.DiscoveryTimeout)
	defer cancel()
	cli, err := s.m.openMCPClient(s.life, discoveryCtx, s.name, s.cfg)
	if err != nil {
		return nil, err
	}
	s.adopt(cli)
	return cli, nil
}

// callTool executes one remote tool through the slot's session. A connection
// failure drops the session and the cached readiness; an answer refreshes both.
func (s *mcpSessionSlot) callTool(remoteName string, onError func()) func(context.Context, json.RawMessage) (string, error) {
	return func(callCtx context.Context, args json.RawMessage) (string, error) {
		if callCtx == nil {
			callCtx = context.Background()
		}
		cli, err := s.session(callCtx)
		if err != nil {
			if callCtx.Err() != nil {
				return "", callCtx.Err()
			}
			return "", mcpReadinessOutcome(mcpErrorReadiness(err)).Err()
		}
		out, err := s.m.callMCPToolObserved(callCtx, cli, remoteName, args, trustedReadOnlyTool(s.cfg, remoteName), mcpCallObserver{
			connectionFailed: func() {
				s.drop(cli)
				s.m.invalidateMCPReadiness(s.name)
			},
			reached: func() {
				s.touch(cli)
				s.m.markMCPReady(s.name, s.cfg)
			},
		})
		if err != nil && onError != nil {
			onError()
		}
		return out, err
	}
}
