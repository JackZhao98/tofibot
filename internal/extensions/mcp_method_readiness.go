package extensions

import (
	"context"
	"errors"
	"net"
	"net/url"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func mcpErrorReadiness(err error) runtime.MethodReadiness {
	if outboundPolicyDenied(err) {
		return runtime.MethodUnavailable
	}
	if errors.Is(err, ErrOAuthAuthorizationRequired) || errors.Is(err, ErrNoToken) {
		return runtime.MethodAuthRequired
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return runtime.MethodUnavailable
	}
	return runtime.MethodUnknown
}

func mcpReadinessOutcome(state runtime.MethodReadiness) tooloutcome.Outcome {
	// Fixed messages prevent private URLs, credential headers and provider
	// errors from escaping. A readiness failure always precedes tools/call.
	return tooloutcome.New(tooloutcome.Permanent, "mcp_"+string(state), "not_executed", "The MCP method is not ready ("+string(state)+"). The operation was not sent. Preserve the user's task and consider another independently reviewed method, or explain the blocker.", "replan")
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
func (m *Manager) mcpMethodReadiness(name string, cfg MCPServerConfig) func(context.Context) (runtime.MethodReadiness, error) {
	return func(ctx context.Context) (runtime.MethodReadiness, error) {
		ctx, cancel := context.WithTimeout(ctx, m.cfg.DiscoveryTimeout)
		defer cancel()
		if state, err := m.mcpTransportReadiness(ctx, cfg); state != runtime.MethodReady || err != nil {
			return state, err
		}
		cli, err := m.openMCPClient(ctx, ctx, name, cfg)
		if err != nil {
			return mcpErrorReadiness(err), err
		}
		defer cli.Close()
		return runtime.MethodReady, nil
	}
}
