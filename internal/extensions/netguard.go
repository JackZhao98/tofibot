package extensions

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a guarded connection resolves to an
// address a Bot-managed MCP server may not reach.
var ErrBlockedAddress = errors.New("MCP server address is on a private, loopback, link-local, CGNAT or metadata network")

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8",      // "this" network
		"10.0.0.0/8",     // private
		"100.64.0.0/10",  // CGNAT
		"127.0.0.0/8",    // loopback
		"169.254.0.0/16", // link-local, cloud metadata
		"172.16.0.0/12",  // private
		"192.0.0.0/24",   // IETF protocol assignments
		"192.168.0.0/16", // private
		"198.18.0.0/15",  // benchmarking
		"224.0.0.0/3",    // multicast, reserved, broadcast
		"::/128",         // unspecified
		"::1/128",        // loopback
		"fc00::/7",       // unique local (includes fd00:ec2::254 metadata)
		"fe80::/10",      // link-local
		"ff00::/8",       // multicast
		"100::/64",       // discard-only
		"64:ff9b:1::/48", // local-use NAT64 (RFC 8215)
		"2001:db8::/32",  // documentation
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// BlockedAddr reports whether a resolved address is off limits. IPv4-mapped
// IPv6, NAT64 (64:ff9b::/96) and 6to4 (2002::/16) forms are judged by the IPv4
// address they carry.
func BlockedAddr(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if !a.IsValid() {
		return true
	}
	if hostOwnAddr(a) {
		return true
	}
	if a.Is6() {
		b := a.As16()
		// IPv4-compatible ::a.b.c.d (deprecated): judged by the IPv4 it carries.
		if allZero(b[:12]) && !allZero(b[12:]) {
			return BlockedAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
		if a.Is6() && netip.MustParsePrefix("64:ff9b::/96").Contains(a) {
			return BlockedAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
		if b[0] == 0x20 && b[1] == 0x02 {
			return BlockedAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// The addresses of this host itself and of the server's public origin are off
// limits too: a service bound to the host's LAN or public address is not
// "private" by prefix, yet it is the server's own side of the network. The set
// is refreshed at most once a minute, on use.
var (
	ownMu          sync.Mutex
	ownAt          time.Time
	ownAddrs       map[netip.Addr]bool
	ownTTL         = time.Minute
	interfaceAddrs = net.InterfaceAddrs
	lookupHost     = func(ctx context.Context, host string) ([]string, error) {
		return net.DefaultResolver.LookupHost(ctx, host)
	}
)

func hostOwnAddr(a netip.Addr) bool {
	ownMu.Lock()
	defer ownMu.Unlock()
	if ownAddrs == nil || time.Since(ownAt) > ownTTL {
		next := map[netip.Addr]bool{}
		if addrs, err := interfaceAddrs(); err == nil {
			for _, ia := range addrs {
				if p, err := netip.ParsePrefix(ia.String()); err == nil {
					next[p.Addr().Unmap().WithZone("")] = true
				} else if ip, err := netip.ParseAddr(ia.String()); err == nil {
					next[ip.Unmap().WithZone("")] = true
				}
			}
		}
		if raw := strings.TrimSpace(os.Getenv("TOFI_PUBLIC_ORIGIN")); raw != "" {
			if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
				if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
					next[ip.Unmap().WithZone("")] = true
				} else {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					names, _ := lookupHost(ctx, u.Hostname())
					cancel()
					for _, n := range names {
						if ip, err := netip.ParseAddr(n); err == nil {
							next[ip.Unmap().WithZone("")] = true
						}
					}
				}
			}
		}
		ownAddrs, ownAt = next, time.Now()
	}
	return ownAddrs[a]
}

// guardControl runs for every outgoing connection attempt with the address
// already resolved, so a name that resolved to a public address earlier
// (DNS rebinding) or to an unusual literal form is judged by where it connects.
func guardControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: unparseable address", ErrBlockedAddress)
	}
	a, err := netip.ParseAddr(host)
	if err != nil || BlockedAddr(a) {
		return ErrBlockedAddress
	}
	return nil
}

var (
	guardedOnce      sync.Once
	guardedTransport *http.Transport
)

// GuardedTransport is the HTTP transport for Bot-managed MCP servers: direct
// connections only (an environment proxy would resolve the name itself and
// bypass the check), refused at dial time for blocked addresses.
func GuardedTransport() http.RoundTripper {
	guardedOnce.Do(func() {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: guardControl}
		t.DialContext = dialer.DialContext
		guardedTransport = t
	})
	return guardedTransport
}

// IsBotManaged reports whether the named server's connections are subject to
// the dial-time address guard (it was created or last changed by a Bot).
func (m *Manager) IsBotManaged(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, err := loadServers(m.cfg.MCPConfigPath)
	return err == nil && cfg[name].BotManaged
}
