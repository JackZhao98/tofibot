package extensions

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// These fixed errors carry no destination, resolver answer, or credential data.
var ErrOutboundDestinationDenied = errors.New("outbound destination denied by network policy")
var ErrOutboundRedirectDenied = errors.New("outbound redirect denied by network policy")

func outboundPolicyDenied(err error) bool {
	return errors.Is(err, ErrOutboundDestinationDenied) || errors.Is(err, ErrOutboundRedirectDenied)
}

// Conservative special-use policy, including cloud platform/metadata addresses.
// IPv6 must additionally be in the ordinary global allocation 2000::/3.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func validateOutboundURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.Host == "" {
		return ErrOutboundDestinationDenied
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, "%\\") {
		return ErrOutboundDestinationDenied
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ErrOutboundDestinationDenied
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return ErrOutboundDestinationDenied
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicAddress(ip) {
			return ErrOutboundDestinationDenied
		}
		if ip.Is6() && !strings.HasPrefix(u.Host, "[") {
			return ErrOutboundDestinationDenied
		}
	} else {
		if len(host) > 253 {
			return ErrOutboundDestinationDenied
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return ErrOutboundDestinationDenied
			}
			for _, c := range label {
				if c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
					return ErrOutboundDestinationDenied
				}
			}
		}
	}
	expectedHost := host
	if strings.Contains(host, ":") {
		expectedHost = "[" + host + "]"
	}
	if u.Port() != "" {
		expectedHost += ":" + u.Port()
	}
	if !strings.EqualFold(u.Host, expectedHost) {
		return ErrOutboundDestinationDenied
	}
	return nil
}

type egressResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type numericDial func(context.Context, string, string) (net.Conn, error)

// Resolve once per connection, validate the entire answer set, then dial only
// those numeric addresses. Sequential fallback shares one ten-second budget.
func publicDialContext(resolver egressResolver, dial numericDial) numericDial {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		host, port, err := net.SplitHostPort(address)
		if err != nil || (network != "tcp" && network != "tcp4" && network != "tcp6") {
			return nil, ErrOutboundDestinationDenied
		}
		var ips []netip.Addr
		if ip, err := netip.ParseAddr(host); err == nil {
			ips = []netip.Addr{ip}
		} else {
			ips, err = resolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, ErrOutboundDestinationDenied
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(ips) == 0 || len(ips) > 16 {
			return nil, ErrOutboundDestinationDenied
		}
		for _, ip := range ips {
			if !publicAddress(ip) {
				return nil, ErrOutboundDestinationDenied
			}
		}
		for _, ip := range ips {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var conn net.Conn
			conn, err = dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, err
	}
}

type publicHTTPTransport struct{ transport *http.Transport }

func (t *publicHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := validateOutboundURL(r.URL); err != nil {
		return nil, err
	}
	if (r.Host != "" && !strings.EqualFold(r.Host, r.URL.Host)) || r.Header.Get("Host") != "" {
		return nil, ErrOutboundDestinationDenied
	}
	resp, err := t.transport.RoundTrip(r)
	if err == nil && resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		resp.Body.Close()
		return nil, ErrOutboundRedirectDenied
	}
	return resp, err
}
func (t *publicHTTPTransport) CloseIdleConnections() { t.transport.CloseIdleConnections() }

// Test seams are package-private; tenants cannot supply resolvers or dialers.
func newPublicHTTPClient(resolver egressResolver, dial numericDial) *http.Client {
	tr := &http.Transport{
		Proxy: nil, DialContext: publicDialContext(resolver, dial),
		ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: defaultToolTimeout, ExpectContinueTimeout: time.Second,
		MaxIdleConns: 64, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 8,
		IdleConnTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: &publicHTTPTransport{tr}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrOutboundRedirectDenied }}
}
