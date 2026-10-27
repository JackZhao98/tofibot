package extensions

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlockedAddrCoversInternalRangesAndEmbeddedForms(t *testing.T) {
	for _, a := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "100.64.0.1", "100.127.255.254",
		"0.0.0.0", "198.18.0.1", "224.0.0.1", "255.255.255.255", "::1", "::", "fe80::1", "fd00:ec2::254", "fc00::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "64:ff9b::7f00:1", "2002:a00:1::",
	} {
		if !BlockedAddr(netip.MustParseAddr(a)) {
			t.Errorf("%s must be blocked", a)
		}
	}
	for _, a := range []string{"93.184.216.34", "8.8.8.8", "1.1.1.1", "100.128.0.1", "172.32.0.1", "2606:4700:4700::1111", "64:ff9b::808:808"} {
		if BlockedAddr(netip.MustParseAddr(a)) {
			t.Errorf("%s must be allowed", a)
		}
	}
}

func hitCounter(hits *atomic.Int32, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func TestGuardedTransportRefusesLoopbackAtDialTime(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(hitCounter(&hits, http.NotFoundHandler()))
	defer srv.Close()
	client := &http.Client{Transport: GuardedTransport(), Timeout: 5 * time.Second}
	// A literal address and a name that resolves to loopback only when dialed
	// (the rebinding shape: any earlier check saw something else) are both refused.
	_, portOnly, _ := net.SplitHostPort(srv.Listener.Addr().String())
	for _, target := range []string{srv.URL, "http://localhost:" + portOnly, "http://[::ffff:127.0.0.1]:" + portOnly} {
		_, err := client.Get(target)
		if !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: want ErrBlockedAddress, got %v", target, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("a request reached the internal server %d times", hits.Load())
	}
}

func TestBotManagedServerNeverConnectsInternallyButOwnerServerStillDoes(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(hitCounter(&hits, http.NotFoundHandler()))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	_, err := m.openMCPClient(ctx, ctx, "bot", MCPServerConfig{URL: srv.URL, BotManaged: true})
	if err == nil || !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("Bot-managed loopback server: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("Bot-managed server reached the internal host (%d hits)", hits.Load())
	}
	// Owner-added servers (Settings) keep working against the LAN: the guard is not global.
	_, _ = m.openMCPClient(ctx, ctx, "owner", MCPServerConfig{URL: srv.URL})
	if hits.Load() == 0 {
		t.Fatal("an owner-added server was blocked")
	}
}

func TestMCPClientDoesNotFollowRedirectsToInternalHosts(t *testing.T) {
	var inner atomic.Int32
	target := httptest.NewServer(hitCounter(&inner, http.NotFoundHandler()))
	defer target.Close()
	var outer atomic.Int32
	redirector := httptest.NewServer(hitCounter(&outer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/secret", http.StatusFound)
	})))
	defer redirector.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	// Unflagged, so the first hop connects; the redirect must still not be followed.
	_, _ = m.openMCPClient(ctx, ctx, "redir", MCPServerConfig{URL: redirector.URL})
	if outer.Load() == 0 {
		t.Fatal("test setup: the redirecting server was never reached")
	}
	if inner.Load() != 0 {
		t.Fatalf("a redirect was followed to an internal host (%d hits)", inner.Load())
	}
}

func TestBlockedAddrCoversHostAndOriginAndNewRanges(t *testing.T) {
	oldIf, oldLook, oldTTL := interfaceAddrs, lookupHost, ownTTL
	defer func() {
		interfaceAddrs, lookupHost, ownTTL = oldIf, oldLook, oldTTL
		ownMu.Lock()
		ownAddrs = nil
		ownMu.Unlock()
	}()
	ownTTL = 0
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("203.0.113.9"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	lookupHost = func(ctx context.Context, host string) ([]string, error) { return []string{"198.51.100.7"}, nil }
	t.Setenv("TOFI_PUBLIC_ORIGIN", "https://tofi.example.test")
	for in, want := range map[string]bool{
		"203.0.113.9": true, "198.51.100.7": true, "203.0.113.10": false, "93.184.216.34": false,
		"64:ff9b:1::1": true, "::127.0.0.1": true, "::10.0.0.1": true, "::8.8.8.8": false, "2606:4700::1": false,
	} {
		if got := BlockedAddr(netip.MustParseAddr(in)); got != want {
			t.Errorf("%s: got %v want %v", in, got, want)
		}
	}
}
