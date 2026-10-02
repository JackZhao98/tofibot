package computer

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const RunnerOrigin = "http://account-computer/v1/runner"

type runnerTransport struct{ client *Client }

// RunnerTransport can reach only this client's fixed account Unix socket.
// The private manager/vsock bridge provides authorization; no shared Runner
// credential or caller-selected account address is accepted here.
func (c *Client) RunnerTransport() http.RoundTripper { return runnerTransport{c} }

// Prepare separates bounded cold VM readiness from MCP catalog discovery.
// It never sends a tool request or retries a tool operation.
func (t runnerTransport) Prepare(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if t.client.ensure != nil {
		if err := t.client.ensure(ctx); err != nil {
			return err
		}
	}
	return t.client.waitForBlobGuest(ctx)
}

func (t runnerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u := r.URL
	if u.Scheme != "http" || u.Host != "account-computer" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/v1/runner/") || strings.Contains(u.Path, "..") {
		return nil, errors.New("invalid account Runner endpoint")
	}
	if t.client.ensure != nil {
		if err := t.client.ensure(r.Context()); err != nil {
			return nil, err
		}
	}
	if err := t.client.waitForBlobGuest(r.Context()); err != nil {
		return nil, err
	}
	tr := t.client.http.Transport
	if tr == nil {
		return nil, errors.New("private account Runner transport unavailable")
	}
	req := r.Clone(r.Context())
	req.Header.Del("Authorization")
	return tr.RoundTrip(req)
}
