package tooloutcome

import "strings"

// Codes of a failed browser navigation, classified from Chrome's own net error
// name (Page.navigate errorText, "net::ERR_*"), never from prose around it.
// The guest builds them; the host passes them through to the model unchanged.
const (
	BrowserTLSError          = "browser_tls_error"
	BrowserDNSError          = "browser_dns_error"
	BrowserConnectionRefused = "browser_connection_refused"
	BrowserConnectionFailed  = "browser_connection_failed"
	BrowserUnsafePort        = "browser_unsafe_port"
	BrowserNavigationFailed  = "browser_navigation_failed"
)

// NoSideEffects is the certainty of a navigation Chrome refused to complete:
// nothing the page could have changed was sent.
const NoSideEffects = "no_side_effects"

// IsBrowserNavigation reports whether o is a coded navigation failure the
// guest produced, which the host may trust as definite and side-effect free.
func IsBrowserNavigation(o Outcome) bool {
	switch o.Code {
	case BrowserTLSError, BrowserDNSError, BrowserConnectionRefused, BrowserConnectionFailed, BrowserUnsafePort, BrowserNavigationFailed:
		return o.Certainty == NoSideEffects
	}
	return false
}

// NetErrorName extracts the Chrome net error name ("net::ERR_X" -> "ERR_X")
// from a Page.navigate errorText. It returns "" when the text is not one.
func NetErrorName(errorText string) string {
	text := strings.TrimPrefix(strings.TrimSpace(errorText), "net::")
	if !strings.HasPrefix(text, "ERR_") {
		return ""
	}
	end := strings.IndexFunc(text, func(r rune) bool { return !(r == '_' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') })
	if end < 0 {
		return text
	}
	return text[:end]
}

// ClassifyNavigation turns a Page.navigate errorText and the host being loaded
// into a coded outcome whose next action fits the failure. A certificate or
// resolver failure belongs to that host: loading it again cannot help, but
// another source for the same facts can.
func ClassifyNavigation(errorText, host string) Outcome {
	name := NetErrorName(errorText)
	shown := name
	if shown == "" {
		shown = strings.TrimSpace(errorText)
	}
	status, code, next := Permanent, BrowserNavigationFailed, "explain_blocker"
	what := "The browser could not load " + host + " (" + shown + "). Nothing on the page was changed. Try a different page or source, or explain the blocker."
	switch {
	case strings.HasPrefix(name, "ERR_CERT_") || strings.HasPrefix(name, "ERR_SSL_") || name == "ERR_BAD_SSL_CLIENT_AUTH_CERT":
		code, next = BrowserTLSError, "try_another_source"
		what = "The browser rejected the TLS certificate that answered for " + host + " (" + name + "). This is a property of the connection to that host, so loading it again will fail the same way; do not retry it and do not ask to bypass the check. Get the information from another site or source, or explain the blocker."
	case strings.HasPrefix(name, "ERR_DNS_") || name == "ERR_NAME_NOT_RESOLVED" || name == "ERR_NAME_RESOLUTION_FAILED":
		code, next = BrowserDNSError, "try_another_source"
		what = "The host name " + host + " did not resolve from this computer (" + name + "). Check the spelling of the address; if it is right, the name is unreachable from here, so use another source or explain the blocker."
	case name == "ERR_CONNECTION_REFUSED":
		code, next = BrowserConnectionRefused, "try_another_source"
		what = host + " refused the connection (" + name + "). Nothing is listening at that address and port, or it blocks this computer. Try another source or explain the blocker."
	case name == "ERR_UNSAFE_PORT" || name == "ERR_UNSAFE_REDIRECT":
		code, next = BrowserUnsafePort, "repair_arguments"
		what = "Chrome refuses to load " + host + " on that port (" + name + "). Use the site's standard http or https address without a custom port, or another source."
	case name == "ERR_CONNECTION_RESET" || name == "ERR_CONNECTION_CLOSED" || name == "ERR_CONNECTION_TIMED_OUT" || name == "ERR_TIMED_OUT" ||
		name == "ERR_CONNECTION_FAILED" || name == "ERR_EMPTY_RESPONSE" || name == "ERR_ADDRESS_UNREACHABLE" || name == "ERR_NETWORK_CHANGED" ||
		name == "ERR_INTERNET_DISCONNECTED" || name == "ERR_NETWORK_ACCESS_DENIED" || name == "ERR_TUNNEL_CONNECTION_FAILED":
		status, code, next = Transient, BrowserConnectionFailed, "retry"
		what = "The connection to " + host + " failed (" + name + "). It may be a momentary network problem: try once more, and if it fails again use another source or explain the blocker."
	}
	o := New(status, code, NoSideEffects, what, next)
	o.Host, o.NetError = host, name
	return o
}
