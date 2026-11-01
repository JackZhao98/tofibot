package extensions

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func mcpErrorReadiness(err error) runtime.MethodReadiness {
	if errors.Is(err, ErrOAuthAuthorizationRequired) || errors.Is(err, ErrNoToken) {
		return runtime.MethodAuthRequired
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return runtime.MethodUnavailable
	}
	return runtime.MethodUnknown
}

// mcpConnectionLink is the Settings page of one connection.
func mcpConnectionLink(server string) string {
	return "/settings/connections/" + url.PathEscape(server)
}

// mcpReadinessOutcome refuses a call before dispatch. The message names the
// connection, its state and the fix for a run with a person and for a run
// without one. Fixed text prevents private URLs, credential headers and
// provider errors from escaping. A readiness failure always precedes tools/call.
func mcpReadinessOutcome(server string, state runtime.MethodReadiness) tooloutcome.Outcome {
	link := mcpConnectionLink(server)
	var msg string
	switch state {
	case runtime.MethodAuthRequired:
		msg = "Connection " + server + " needs sign-in (auth_required). The operation was not sent. With a person in the chat, call request_connection with service " + server + ". Otherwise the person signs in at " + link + " in Settings. A scheduled run cannot sign in: finish the other work and report in the summary that " + server + " needs sign-in."
	case runtime.MethodNotConfigured:
		msg = "Connection " + server + " is not configured, or its configuration changed (not_configured). The operation was not sent. Search again with search_mcp_tools to read the current tools. The person can check the connection at " + link + " in Settings."
	case runtime.MethodUnavailable:
		msg = "Connection " + server + " did not answer (unavailable). The operation was not sent. Try again later or use another permitted method. The person can check the connection at " + link + " in Settings. A scheduled run reports in the summary that " + server + " was unreachable."
	default:
		msg = "Connection " + server + " is not ready (" + string(state) + "). The operation was not sent. Preserve the task and use another independently reviewed method, or explain the blocker. The person can check the connection at " + link + " in Settings."
	}
	out := tooloutcome.New(tooloutcome.Permanent, "mcp_"+string(state), "not_executed", msg, "replan")
	out.Server, out.Link = server, link
	return out
}

// mcpConnectionState is the cached state shown by list_mcp_servers. It never
// opens a connection: a fresh handshake is connected, a recorded failure is
// needs_sign_in or error, an OAuth server without a token file needs sign-in,
// and anything else is unchecked.
func (m *Manager) mcpConnectionState(name string, cfg MCPServerConfig) string {
	if m.mcpReadyFresh(name, cfg) {
		return "connected"
	}
	c := &m.readiness
	c.mu.Lock()
	failed := c.failed[name]
	c.mu.Unlock()
	switch failed {
	case runtime.MethodAuthRequired:
		return "needs_sign_in"
	case "":
	default:
		return "error"
	}
	if cfg.OAuth != nil && strings.TrimSpace(m.cfg.MCPConfigPath) != "" {
		if _, err := os.Stat(m.tokenPath(name)); err != nil {
			return "needs_sign_in"
		}
	}
	return "unchecked"
}

// noteMCPReadiness records the last readiness failure of a server for
// list_mcp_servers; a ready answer clears it.
func (m *Manager) noteMCPReadiness(name string, state runtime.MethodReadiness) {
	c := &m.readiness
	c.mu.Lock()
	defer c.mu.Unlock()
	if state == runtime.MethodReady {
		delete(c.failed, name)
		return
	}
	if c.failed == nil {
		c.failed = map[string]runtime.MethodReadiness{}
	}
	c.failed[name] = state
}

func (m *Manager) mcpTransportReadiness(ctx context.Context, cfg MCPServerConfig) (runtime.MethodReadiness, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return runtime.MethodNotConfigured, err
	}
	if m.cfg.HTTPTransport != nil {
		tr, err := m.cfg.HTTPTransport(cfg.URL)
		if err != nil {
			return mcpErrorReadiness(err), err
		}
		if check, ok := tr.(interface {
			Readiness(context.Context) (string, error)
		}); ok {
			state, err := check.Readiness(ctx)
			switch runtime.MethodReadiness(state) {
			case runtime.MethodReady, runtime.MethodNotConfigured, runtime.MethodAuthRequired, runtime.MethodUnavailable:
				return runtime.MethodReadiness(state), err
			default:
				return runtime.MethodUnknown, err
			}
		}
	}
	return runtime.MethodReady, nil
}

// The current MCP protocol removed legacy ping. A fresh server/discover
// connection proves endpoint readiness without listing or invoking any tool.
// A recent successful handshake for the same configuration is reused for
// mcpReadinessTTL; the cheap transport check still runs every time.
func (m *Manager) mcpMethodReadiness(name string, cfg MCPServerConfig) func(context.Context) (runtime.MethodReadiness, error) {
	return func(ctx context.Context) (state runtime.MethodReadiness, err error) {
		defer func() { m.noteMCPReadiness(name, state) }()
		ctx, cancel := context.WithTimeout(ctx, m.cfg.DiscoveryTimeout)
		defer cancel()
		if state, err := m.mcpTransportReadiness(ctx, cfg); state != runtime.MethodReady || err != nil {
			return state, err
		}
		if m.mcpReadyFresh(name, cfg) {
			return runtime.MethodReady, nil
		}
		cli, err := m.openMCPClient(ctx, ctx, name, cfg)
		if err != nil {
			return mcpErrorReadiness(err), err
		}
		defer cli.Close()
		return runtime.MethodReady, nil
	}
}
