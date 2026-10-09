package app

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// parseTrustedProxies reads TOFI_TRUSTED_PROXIES: comma-separated IPs or CIDRs.
// Empty means no proxy is trusted (X-Forwarded-For is ignored).
func parseTrustedProxies(value string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("TOFI_TRUSTED_PROXIES: %q is not an IP or CIDR", item)
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
				ip = ip.To4()
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("TOFI_TRUSTED_PROXIES: %q is not an IP or CIDR", item)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

func ipTrusted(ip net.IP, trusted []*net.IPNet) bool {
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP is the address used to key authentication limits. X-Forwarded-For is
// consulted only when the direct peer is a trusted proxy; the chain is walked
// from the right and the first hop that is not itself trusted wins. A missing or
// malformed header falls back to the direct peer.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || len(trusted) == 0 || !ipTrusted(peer, trusted) {
		return host
	}
	var hops []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(line, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(hops[i]))
		if ip == nil {
			return host
		}
		if !ipTrusted(ip, trusted) {
			return ip.String()
		}
	}
	return host
}
