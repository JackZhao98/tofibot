package extensions

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
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
	if a.Is6() {
		b := a.As16()
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
