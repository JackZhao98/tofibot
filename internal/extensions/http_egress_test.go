package extensions

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixtureResolver func(context.Context, string, string) ([]netip.Addr, error)

func (f fixtureResolver) LookupNetIP(c context.Context, n, h string) ([]netip.Addr, error) {
	return f(c, n, h)
}

func TestPublicEgressAddressPolicy(t *testing.T) {
	for _, raw := range []string{"0.0.0.0", "127.0.0.1", "10.0.0.1", "172.16.1.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "168.63.129.16", "192.0.0.8", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "::", "::1", "fc00::1", "fe80::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "::ffff:100.100.100.200", "64:ff9b::a00:1", "64:ff9b:1::1", "2002:a00:1::", "2001:0:4136:e378:8000:63bf:3fff:fdd2", "2001:db8::1", "3fff::1", "fe80::1%en0"} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("allowed special-use fixture %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888", "::ffff:8.8.8.8"} {
		if !publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("denied public fixture %s", raw)
		}
	}
	for _, raw := range []string{"http://user@public.fixture.test", "ftp://public.fixture.test", "http://public.fixture.test:0", "http://public.fixture.test:65536", "http://public.fixture.test:", "http://[fe80::1%25en0]", "http://public.fixture.test/#fragment", "http://bad..fixture.test", "http://[8.8.8.8]", "http://[public.fixture.test]", "http://[2606:4700::1111"} {
		u, err := url.Parse(raw)
		if err == nil && validateOutboundURL(u) == nil {
			t.Errorf("allowed malformed fixture %s", raw)
		}
	}
}

func TestPublicEgressDNSAndNumericDialBinding(t *testing.T) {
	public := netip.MustParseAddr("8.8.8.8")
	for _, tc := range []struct {
		name     string
		ips      []netip.Addr
		err      error
		canceled bool
	}{
		{"mixed", []netip.Addr{public, netip.MustParseAddr("10.0.0.1")}, nil, false},
		{"invalid", []netip.Addr{{}}, nil, false}, {"empty", nil, nil, false},
		{"error", nil, errors.New("synthetic DNS error"), false}, {"canceled", []netip.Addr{public}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dials := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			dial := publicDialContext(fixtureResolver(func(context.Context, string, string) ([]netip.Addr, error) { return tc.ips, tc.err }), func(context.Context, string, string) (net.Conn, error) { dials++; return nil, errors.New("trap") })
			_, err := dial(ctx, "tcp", "public.fixture.test:443")
			if err == nil || dials != 0 {
				t.Fatalf("err=%v dials=%d", err, dials)
			}
		})
	}
	lookups, dials := 0, 0
	dial := publicDialContext(fixtureResolver(func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		if lookups > 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{public, netip.MustParseAddr("2606:4700::1111")}, nil
	}), func(ctx context.Context, n, a string) (net.Conn, error) {
		dials++
		if _, err := netip.ParseAddrPort(a); err != nil {
			t.Fatal("raw dial received hostname")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded dial")
		}
		return nil, errors.New("synthetic connect failure")
	})
	_, _ = dial(context.Background(), "tcp", "public.fixture.test:8443")
	if lookups != 1 || dials != 2 {
		t.Fatalf("lookups=%d dials=%d", lookups, dials)
	}
	_, err := dial(context.Background(), "tcp", "public.fixture.test:8443")
	if !errors.Is(err, ErrOutboundDestinationDenied) || dials != 2 {
		t.Fatal("rebinding escaped policy")
	}
}

// All HTTP/TLS bytes travel over net.Pipe. The numeric addresses are data only.
func syntheticPublicClient(t *testing.T, handler func(*http.Request) (int, http.Header, string)) (*http.Client, *atomic.Int32) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic"}, DNSNames: []string{"public.fixture.test", "issuer.fixture.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	count := new(atomic.Int32)
	client := newPublicHTTPClient(fixtureResolver(func(ctx context.Context, n, h string) ([]netip.Addr, error) {
		if h == "blocked.fixture.test" {
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		}
		if h != "public.fixture.test" && h != "issuer.fixture.test" {
			t.Errorf("unexpected resolver target %s", h)
			return nil, errors.New("DNS trap")
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}), func(ctx context.Context, n, a string) (net.Conn, error) {
		if a != "8.8.8.8:443" && a != "8.8.8.8:80" {
			t.Errorf("unexpected numeric dial %s", a)
		}
		count.Add(1)
		left, right := net.Pipe()
		go func() {
			defer right.Close()
			_ = right.SetDeadline(time.Now().Add(5 * time.Second))
			var conn net.Conn = right
			if strings.HasSuffix(a, ":443") {
				conn = tls.Server(right, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
					if hello.ServerName != "public.fixture.test" && hello.ServerName != "issuer.fixture.test" {
						t.Errorf("lost SNI %s", hello.ServerName)
					}
					return nil, nil
				}})
			}
			r, err := http.ReadRequest(bufio.NewReader(conn))
			if err != nil {
				return
			}
			defer r.Body.Close()
			status, headers, body := handler(r)
			_, _ = io.Copy(io.Discard, r.Body)
			if headers == nil {
				headers = http.Header{}
			}
			headers.Set("Connection", "close")
			resp := &http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: headers, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Close: true}
			_ = resp.Write(conn)
		}()
		return left, nil
	})
	client.Transport.(*publicHTTPTransport).transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	t.Cleanup(client.CloseIdleConnections)
	return client, count
}

func TestPublicEgressHTTPProxyHostTLSAndRedirects(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.fixture.test:8080")
	t.Setenv("HTTPS_PROXY", "http://proxy.fixture.test:8080")
	t.Setenv("ALL_PROXY", "http://proxy.fixture.test:8080")
	t.Setenv("NO_PROXY", "")
	var redirect atomic.Int32
	client, dials := syntheticPublicClient(t, func(r *http.Request) (int, http.Header, string) {
		if r.Host != "public.fixture.test" {
			t.Errorf("lost HTTP Host %s", r.Host)
		}
		if redirect.Load() > 0 {
			return int(redirect.Load()), http.Header{"Location": []string{"https://blocked.fixture.test/steal"}}, ""
		}
		return 200, nil, "ok"
	})
	tr := client.Transport.(*publicHTTPTransport).transport
	if tr.Proxy != nil || tr.DialTLS != nil || tr.DialTLSContext != nil || tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("transport bypass or disabled verification")
	}
	for _, scheme := range []string{"http", "https"} {
		resp, err := client.Get(scheme + "://public.fixture.test/test")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	before := dials.Load()
	req, _ := http.NewRequest("GET", "https://public.fixture.test/test", nil)
	req.Host = "other.fixture.test"
	if _, err := client.Do(req); !errors.Is(err, ErrOutboundDestinationDenied) || dials.Load() != before {
		t.Fatal("inconsistent Host dialed")
	}
	for _, status := range []int{301, 302, 303, 307, 308} {
		redirect.Store(int32(status))
		before = dials.Load()
		req, _ := http.NewRequest("POST", "https://public.fixture.test/test", strings.NewReader("synthetic-secret"))
		req.Header.Set("Authorization", "Bearer synthetic-token")
		if _, err := client.Do(req); !errors.Is(err, ErrOutboundRedirectDenied) || dials.Load() != before+1 {
			t.Fatalf("redirect %d escaped or followed: %v", status, err)
		}
	}
}

func TestPublicEgressMCPAndPoolOwnership(t *testing.T) {
	for i := 0; i < 8; i++ {
		m := NewManager(Config{HostedEgress: true})
		tr, ok := m.httpClient.Transport.(*publicHTTPTransport)
		if !ok || m.httpClient == http.DefaultClient {
			t.Fatal("shared/default hosted pool")
		}
		if tr.transport.MaxConnsPerHost == 0 || tr.transport.IdleConnTimeout == 0 || m.httpClient.Timeout == 0 {
			t.Fatal("unbounded pool/client")
		}
		m.httpClient, _ = syntheticPublicClient(t, func(*http.Request) (int, http.Header, string) {
			t.Fatal("private URL reached HTTP")
			return 500, nil, ""
		})
		state, err := m.mcpMethodReadiness("local_spoof", MCPServerConfig{URL: "http://169.254.169.254/mcp", Headers: map[string]string{"X-Trusted": "true"}})(context.Background())
		if string(state) != "unavailable" || !errors.Is(err, ErrOutboundDestinationDenied) {
			t.Fatalf("state=%s err=%v", state, err)
		}
		m.CloseIdleConnections()
	}
}

func TestPublicEgressMCPPublicProtocolAndCachedDispatch(t *testing.T) {
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: oauthDestinationTransport(func(*http.Request) (*http.Response, error) {
		t.Error("MCP default client bypass")
		return nil, errors.New("trap")
	})}
	defer func() { http.DefaultClient = old }()
	backend := fixtureServer("synthetic", "1")
	var calls, discovers, lists atomic.Int32
	backend.AddTool(fixtureTool("echo"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return fixtureText("synthetic-result"), nil
	})
	handler := fixtureHTTPHandler(backend)
	client, _ := syntheticPublicClient(t, func(r *http.Request) (int, http.Header, string) {
		if r.Header.Get("X-Fixture") != "retained" || r.Header.Get("MCP-Protocol-Version") == "2025-11-25" || r.Header.Get("Authorization") != "Bearer synthetic-access" {
			t.Error("MCP credential/custom/protocol header changed")
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string
			Params struct {
				Meta map[string]any `json:"_meta"`
			}
		}
		_ = json.Unmarshal(body, &req)
		if req.Params.Meta[mcp.MetaKeyProtocolVersion] != requiredMCPProtocolVersion {
			t.Error("current protocol meta lost")
		}
		switch req.Method {
		case "server/discover":
			discovers.Add(1)
		case "tools/list":
			lists.Add(1)
		case "tools/call":
		default:
			t.Errorf("unexpected MCP method %s", req.Method)
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Header(), w.Body.String()
	})
	m := NewManager(Config{HostedEgress: true, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	m.CloseIdleConnections()
	m.httpClient = client
	defer m.CloseIdleConnections()
	cfg := MCPServerConfig{URL: "https://public.fixture.test/mcp", Headers: map[string]string{"X-Fixture": "retained", "MCP-Protocol-Version": "2025-11-25"}, OAuth: &OAuthConfig{ClientID: "synthetic-client"}}
	if err := m.SaveMCP("local_spoof", cfg, false); err != nil {
		t.Fatal(err)
	}
	if err := m.tokenStore("local_spoof", cfg).SaveToken(context.Background(), &Token{AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if result := m.InspectMCP(context.Background(), "local_spoof"); result.ToolCount != 1 || len(result.Diagnostics) != 0 {
		t.Fatalf("inspect failed: %+v", result)
	}
	prepared, err := m.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if len(prepared.Tools) != 1 || len(prepared.Diagnostics) != 0 {
		t.Fatalf("public prepare failed: %+v", prepared.Diagnostics)
	}
	text, err := prepared.Tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || text != "synthetic-result" {
		t.Fatalf("public call failed: %v", err)
	}
	state, err := m.mcpMethodReadiness("local_spoof", cfg)(context.Background())
	if string(state) != "ready" || err != nil {
		t.Fatalf("public readiness failed: %v", err)
	}
	cached := CachedMCPTool{Name: "mcp_local_spoof__echo", Server: "local_spoof", RemoteName: "echo", Parameters: map[string]any{"type": "object"}, SchemaVersion: m.schemaVersion("local_spoof", cfg)}
	approvals := 0
	discovered, err := m.PrepareDiscoverableForBotWithCallGate(context.Background(), "synthetic-bot", []CachedMCPTool{cached}, func(context.Context, MCPCallApproval) error { approvals++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer discovered.Close()
	_, err = discoveryTool(t, discovered, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_local_spoof__echo","arguments":{}}`))
	if err != nil || approvals != 1 {
		t.Fatalf("cached dispatch failed: %v approvals=%d", err, approvals)
	}
	if calls.Load() != 2 || discovers.Load() < 3 || lists.Load() != 2 {
		t.Fatalf("protocol counts calls=%d discovers=%d lists=%d", calls.Load(), discovers.Load(), lists.Load())
	}
}

// A listener interface over in-memory pipes; never binds an OS socket.
type pipeHTTPListener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func (l *pipeHTTPListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeHTTPListener) Close() error { l.once.Do(func() { close(l.done) }); return nil }
func (*pipeHTTPListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 80} }
func pipePublicHTTPClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	listener := &pipeHTTPListener{connections: make(chan net.Conn), done: make(chan struct{})}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client := newPublicHTTPClient(fixtureResolver(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}), func(ctx context.Context, n, a string) (net.Conn, error) {
		if a != "8.8.8.8:80" {
			t.Errorf("numeric target changed %s", a)
		}
		left, right := net.Pipe()
		select {
		case listener.connections <- right:
			return left, nil
		case <-ctx.Done():
			left.Close()
			right.Close()
			return nil, ctx.Err()
		}
	})
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestPublicEgressMCPSubscriptionOutlivesOAuthTimeout(t *testing.T) {
	backend := mcp.NewServer(&mcp.Implementation{Name: "synthetic", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{requiredMCPProtocolVersion}, Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	backend.AddTool(fixtureTool("before"), fixtureNoop)
	client := pipePublicHTTPClient(t, fixtureHTTPHandler(backend))
	// Compress the former fixed client deadline; the actual SDK subscription
	// must survive it, still deliver changes, then close with its owning run.
	client.Timeout = 40 * time.Millisecond
	m := NewManager(Config{HostedEgress: true, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	m.CloseIdleConnections()
	m.httpClient = client
	defer m.CloseIdleConnections()
	cfg := MCPServerConfig{URL: "http://public.fixture.test/mcp"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cli, err := m.openMCPClient(ctx, ctx, "changing", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	version := m.schemaVersion("changing", cfg)
	if err := m.catalog.Put("changing", version, "fixture", []MCPCatalogTool{{RemoteName: "before", InputSchema: map[string]any{"type": "object"}}}); err != nil {
		t.Fatal(err)
	}
	<-time.After(100 * time.Millisecond)
	backend.AddTool(fixtureTool("after"), fixtureNoop)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, ok := m.catalog.Get("changing", version, "fixture"); !ok {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("subscription stopped at OAuth deadline")
		}
	}
	cancel()
	closed := make(chan struct{})
	go func() { _ = cli.Wait(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("subscription did not close with run")
	}
}

func TestPublicEgressMCPHeadersRespectExistingToolBudget(t *testing.T) {
	const budget = 200 * time.Millisecond
	m := NewManager(Config{HostedEgress: true, ToolTimeout: budget, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	defer m.CloseIdleConnections()
	if m.httpClient.Transport.(*publicHTTPTransport).transport.ResponseHeaderTimeout != budget {
		t.Fatal("headers shorten configured tool budget")
	}
	backend := fixtureServer("slow", "1")
	backend.AddTool(fixtureTool("slow"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-time.After(120 * time.Millisecond)
		return fixtureText("complete"), nil
	})
	client := pipePublicHTTPClient(t, fixtureHTTPHandler(backend))
	client.Transport.(*publicHTTPTransport).transport.ResponseHeaderTimeout = budget
	m.CloseIdleConnections()
	m.httpClient = client
	if err := m.SaveMCP("slow", MCPServerConfig{URL: "http://public.fixture.test/mcp"}, false); err != nil {
		t.Fatal(err)
	}
	p, err := m.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Tools) != 1 {
		t.Fatal("slow fixture missing")
	}
	result, err := p.Tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || result != "complete" {
		t.Fatalf("valid slow tool failed: %v", err)
	}
}
