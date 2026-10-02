package extensions

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type preparedFixtureTransport struct {
	delay    time.Duration
	prepared atomic.Bool
	requests atomic.Int32
}

func (p *preparedFixtureTransport) Prepare(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.delay):
		p.prepared.Store(true)
		return nil
	}
}
func (p *preparedFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.requests.Add(1)
	if !p.prepared.Load() {
		panic("request before readiness")
	}
	return http.DefaultTransport.RoundTrip(r)
}
func TestMCPColdReadinessPrecedesFreshCatalogDeadline(t *testing.T) {
	remote := fixtureHTTPServer(fixtureServer("ready", "1"))
	defer remote.Close()
	tr := &preparedFixtureTransport{delay: 30 * time.Millisecond}
	m := NewManager(Config{DiscoveryTimeout: 20 * time.Millisecond, HTTPTransport: func(string) (http.RoundTripper, error) { return tr, nil }})
	expired, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	tools, cli, err := m.prepareServer(context.Background(), expired, "fixture", MCPServerConfig{URL: remote.URL}, nil, map[string]int{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if len(tools) != 0 || !tr.prepared.Load() || tr.requests.Load() == 0 {
		t.Fatal("readiness/discovery evidence missing")
	}
}
func TestMCPCancelledReadinessSendsNoProtocolOrToolRequest(t *testing.T) {
	tr := &preparedFixtureTransport{delay: time.Hour}
	m := NewManager(Config{HTTPTransport: func(string) (http.RoundTripper, error) { return tr, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := m.prepareServer(ctx, ctx, "fixture", MCPServerConfig{URL: "http://unused"}, nil, map[string]int{}, nil)
	if err == nil || tr.requests.Load() != 0 {
		t.Fatal("cancelled readiness must stop before HTTP")
	}
}
