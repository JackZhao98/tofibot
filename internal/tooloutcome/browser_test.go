package tooloutcome

import "testing"

func TestClassifyNavigationByNetErrorCode(t *testing.T) {
	cases := []struct {
		text, code, status, next string
	}{
		{"net::ERR_CERT_COMMON_NAME_INVALID", BrowserTLSError, Permanent, "try_another_source"},
		{"net::ERR_CERT_DATE_INVALID", BrowserTLSError, Permanent, "try_another_source"},
		{"net::ERR_SSL_PROTOCOL_ERROR", BrowserTLSError, Permanent, "try_another_source"},
		{"net::ERR_NAME_NOT_RESOLVED", BrowserDNSError, Permanent, "try_another_source"},
		{"net::ERR_DNS_TIMED_OUT", BrowserDNSError, Permanent, "try_another_source"},
		{"net::ERR_CONNECTION_REFUSED", BrowserConnectionRefused, Permanent, "try_another_source"},
		{"net::ERR_UNSAFE_PORT", BrowserUnsafePort, Permanent, "repair_arguments"},
		{"net::ERR_CONNECTION_RESET", BrowserConnectionFailed, Transient, "retry"},
		{"net::ERR_TIMED_OUT", BrowserConnectionFailed, Transient, "retry"},
		{"net::ERR_ABORTED", BrowserNavigationFailed, Permanent, "explain_blocker"},
		{"something unexpected", BrowserNavigationFailed, Permanent, "explain_blocker"},
	}
	for _, c := range cases {
		o := ClassifyNavigation(c.text, "example.com")
		if o.Code != c.code || o.Status != c.status || o.NextAction != c.next || o.Certainty != NoSideEffects || o.Host != "example.com" || o.Version != 1 {
			t.Errorf("%s -> %+v", c.text, o)
		}
		if !IsBrowserNavigation(o) {
			t.Errorf("%s: not recognized as a browser navigation outcome", c.text)
		}
	}
	if got := ClassifyNavigation("net::ERR_CERT_COMMON_NAME_INVALID", "a.test").NetError; got != "ERR_CERT_COMMON_NAME_INVALID" {
		t.Fatalf("net error = %q", got)
	}
}

func TestIsBrowserNavigationRequiresTheCertainty(t *testing.T) {
	o := ClassifyNavigation("net::ERR_NAME_NOT_RESOLVED", "x.test")
	o.Certainty = "unknown"
	if IsBrowserNavigation(o) {
		t.Fatal("a navigation code with unknown certainty must not be trusted")
	}
}
