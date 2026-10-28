package webtools

import (
	"context"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// plain is a transport without the address guard, for tests against httptest
// servers (which listen on loopback). The guard itself is tested with the
// default transport below.
var plain = http.DefaultTransport

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	// By name, so the literal-address check passes and only the transport decides.
	return strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
}

func TestValidateTarget(t *testing.T) {
	for in, want := range map[string]Code{
		"":                               BadRequest,
		"ftp://example.com/x":            BadRequest,
		"file:///etc/passwd":             BadRequest,
		"https://user:pw@example.com/":   BadRequest,
		"http://127.0.0.1/":              TargetRefused,
		"http://127.1.2.3:8080/":         TargetRefused,
		"http://10.0.0.5/":               TargetRefused,
		"http://192.168.1.1/":            TargetRefused,
		"http://172.16.0.1/":             TargetRefused,
		"http://169.254.169.254/latest/": TargetRefused,
		"http://100.64.0.1/":             TargetRefused,
		"http://[::1]/":                  TargetRefused,
		"http://[fd00:ec2::254]/":        TargetRefused,
		"http://[::ffff:127.0.0.1]/":     TargetRefused,
		"http://[64:ff9b::7f00:1]/":      TargetRefused,
		"http://0.0.0.0/":                TargetRefused,
		"https://example.com/":           "",
		"http://93.184.216.34/":          "",
	} {
		_, err := ValidateTarget(in)
		got := Code("")
		if err != nil {
			got = CodeOf(err)
		}
		if got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestGuardedTransportRefusesLoopbackAtDialTime(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) })
	// "localhost" passes the literal check and resolves to loopback: the case
	// DNS rebinding relies on. Only the dial-time guard can refuse it.
	target := url
	_, err := Builtin{Transport: extensions.GuardedTransport()}.Fetch(context.Background(), target, 0)
	if CodeOf(err) != TargetRefused {
		t.Fatalf("got %v", err)
	}
}

func TestRedirectToPrivateIsRefused(t *testing.T) {
	// Literal private target in a redirect: re-checked by CheckRedirect.
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	_, err := Builtin{Transport: plain}.Fetch(context.Background(), url, 0)
	if CodeOf(err) != TargetRefused {
		t.Fatalf("redirect to metadata: %v", err)
	}
	// A redirect to a name that resolves to loopback dies at the dial guard
	// even with the default transport: the first hop is already refused.
	if _, err := (Builtin{Transport: extensions.GuardedTransport()}).Fetch(context.Background(), url, 0); CodeOf(err) != TargetRefused {
		t.Fatalf("guarded: %v", err)
	}
}

func TestRedirectLimit(t *testing.T) {
	var self string
	self = serve(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, self+"/again", http.StatusFound) })
	if _, err := (Builtin{Transport: plain}).Fetch(context.Background(), self, 0); CodeOf(err) != Network {
		t.Fatalf("loop: %v", err)
	}
}

const article = `<!doctype html><html><head><title>The &amp; Title</title><style>.x{}</style><script>var a="<p>nope</p>";</script></head>
<body><nav><a href="/home">Home</a></nav>
<main><h1>Heading</h1><p>First paragraph with <a href="/rel?x=1">a link</a> and <strong>bold</strong>.</p>
<ul><li>one<li>two</ul><pre><code>a  b
c</code></pre><p hidden>hidden text</p><p>` + "more words here to pass the main minimum length so main wins over the whole body. " + `Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.</p></main>
<footer>Footer junk</footer></body></html>`

func TestBuiltinExtractsReadableMarkdown(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(article))
	})
	p, err := Builtin{Transport: plain}.Fetch(context.Background(), url+"/dir/page", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "The & Title" {
		t.Errorf("title %q", p.Title)
	}
	for _, want := range []string{"# Heading", "[a link](" + url + "/rel?x=1)", "**bold**", "- one", "- two", "```\na  b\nc\n```"} {
		if !strings.Contains(p.Content, want) {
			t.Errorf("missing %q in:\n%s", want, p.Content)
		}
	}
	for _, bad := range []string{"nope", "hidden text", "Footer junk", "Home", ".x{}"} {
		if strings.Contains(p.Content, bad) {
			t.Errorf("should not contain %q:\n%s", bad, p.Content)
		}
	}
	if strings.Contains(p.Content, "\n\n\n") {
		t.Error("blank lines not collapsed")
	}
}

func TestBodyFallbackKeepsContentOutsideMain(t *testing.T) {
	doc := ParseHTML(context.Background(), `<body><nav>menu</nav><div><p>`+strings.Repeat("body words ", 20)+`</p></div><footer>foot</footer></body>`, nil)
	if !strings.Contains(doc.Markdown, "body words") || strings.Contains(doc.Markdown, "menu") || strings.Contains(doc.Markdown, "foot") {
		t.Fatalf("%q", doc.Markdown)
	}
}

func TestBuiltinCodes(t *testing.T) {
	cases := map[string]struct {
		h    http.HandlerFunc
		want Code
	}{
		"404": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }, NotFound},
		"403": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }, Blocked},
		"429": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }, Blocked},
		"500": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, Network},
		"pdf": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF"))
		}, Unsupported},
		"png": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("x"))
		}, Unsupported},
		"empty": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body><p>hi</p></body></html>"))
		}, EmptyContent},
		"spa": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body><div id="root"></div><script src="/app.js"></script></body></html>`))
		}, NeedsJavaScript},
	}
	for name, c := range cases {
		url := serve(t, c.h)
		if _, err := (Builtin{Transport: plain}).Fetch(context.Background(), url, 0); CodeOf(err) != c.want {
			t.Errorf("%s: got %v want %s", name, err, c.want)
		}
	}
}

func TestBuiltinSizeCaps(t *testing.T) {
	huge := strings.Repeat("<p>"+strings.Repeat("word ", 40)+"</p>", 30000) // far over 2 MiB
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>" + huge))
	})
	p, err := Builtin{Transport: plain}.Fetch(context.Background(), url, 5000)
	if err != nil || !p.Truncated || len([]rune(p.Content)) != 5000 {
		t.Fatalf("truncated=%v len=%d err=%v", p.Truncated, len([]rune(p.Content)), err)
	}
	// Plain text keeps its own form and respects the cap too.
	txt := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(strings.Repeat("x", MaxBodyBytes+100)))
	})
	p, err = Builtin{Transport: plain}.Fetch(context.Background(), txt, MaxMaxChars)
	if err != nil || !p.Truncated || len(p.Content) != MaxMaxChars {
		t.Fatalf("text: truncated=%v len=%d err=%v", p.Truncated, len(p.Content), err)
	}
}

// bestOf is the smallest of n samples of fn's cost: noise only ever adds cost, so the minimum is the cleanest sample.
func bestOf(n int, fn func() float64) float64 {
	best := -1.0
	for i := 0; i < n; i++ {
		if c := fn(); best < 0 || c < best {
			best = c
		}
	}
	return best
}

// Regression: 2 MiB inputs that were quadratic (raw-text close-tag search copied the rest of the page per tag; newline
// handling rescanned the output per block). The check is the growth rate, never a wall-clock bound: the same page at 4x the
// size must cost about 4x (linear), not about 16x (quadratic).
//   - Bytes allocated are deterministic, so that ratio runs everywhere, CI included, and catches the copying paths.
//   - Wall-clock time also catches rescanning, but GC and a busy shared runner move big and small runs apart, so the time ratio
//     follows the repo-wide TOFI_SKIP_TIMING switch (docs/process.md 3.3) and runs on a quiet machine only.
func assertLinear(t *testing.T, name string, page func(bytes int) string) {
	t.Helper()
	small, big := page(MaxBodyBytes/8), page(MaxBodyBytes/2)
	allocated := func(src string) func() float64 {
		return func() float64 {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			ParseHTML(context.Background(), src, nil)
			runtime.ReadMemStats(&after)
			return float64(after.TotalAlloc - before.TotalAlloc)
		}
	}
	if as, ab := bestOf(3, allocated(small)), bestOf(3, allocated(big)); ab/as > 8 {
		t.Errorf("%s: 4x the input allocated %.1fx the bytes (%.0f -> %.0f); linear is about 4x, quadratic about 16x", name, ab/as, as, ab)
	}
	if os.Getenv("TOFI_SKIP_TIMING") == "1" {
		return
	}
	timed := func(src string) func() float64 {
		return func() float64 {
			start := time.Now()
			ParseHTML(context.Background(), src, nil)
			return float64(time.Since(start))
		}
	}
	ts, tb := bestOf(5, timed(small)), bestOf(5, timed(big))
	if floor := float64(5 * time.Millisecond); ts < floor {
		ts = floor // below this the small run is timer noise, not a baseline
	}
	if tb/ts > 12 {
		t.Errorf("%s: 4x the input took %.1fx the time (%v -> %v); linear is about 4x, quadratic about 16x", name, tb/ts, time.Duration(ts), time.Duration(tb))
	}
}

func TestExtractorStaysLinearOnHostileInput(t *testing.T) {
	assertLinear(t, "script storm", func(n int) string { return "<html><body>" + strings.Repeat("<SCRIPT>x</SCRIPT>", n/18) })
	assertLinear(t, "br/p storm", func(n int) string { return "<html><body>" + strings.Repeat("<br><p>", n/7) })
	assertLinear(t, "nested anchors", func(n int) string { return strings.Repeat("<div><a href=x>", n/15) })
	assertLinear(t, "unclosed scripts", func(n int) string { return strings.Repeat("<script>", n/8) })
}

// A cancelled context stops the walk: the page is not rendered. Asserted on the output, not on how fast it returned.
func TestExtractorStopsOnCancel(t *testing.T) {
	page := strings.Repeat("<p>word</p>", 40000)
	full := ParseHTML(context.Background(), page, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cut := ParseHTML(ctx, page, nil)
	if len(full.Markdown) < 100000 || len(cut.Markdown) > len(full.Markdown)/10 {
		t.Fatalf("a cancelled parse must leave most of the page unrendered: cancelled %d bytes, full %d bytes", len(cut.Markdown), len(full.Markdown))
	}
}

func TestIndexFold(t *testing.T) {
	for _, c := range []struct {
		s     string
		start int
		want  int
	}{{"ab</SCRIPT>", 0, 2}, {"ab</script>", 2, 0}, {"<</Script ", 0, 1}, {"no tag", 0, -1}, {"</scrip", 0, -1}} {
		if got := indexFold(c.s, c.start, "</script"); got != c.want {
			t.Errorf("%q from %d: %d want %d", c.s, c.start, got, c.want)
		}
	}
}

func TestBuiltinPortRuleAndBrightDataCredentialCodes(t *testing.T) {
	// A port outside 80/443/8080/8443 is not fetched by the built-in; the chain goes on.
	if _, err := (Builtin{}).Fetch(context.Background(), "http://example.com:8000/", 0); CodeOf(err) != Unsupported {
		t.Fatalf("port 8000: %v", err)
	}
	for _, p := range []string{"", ":80", ":443", ":8080", ":8443"} {
		if _, err := (Builtin{}).checkTarget("https://example.com" + p + "/"); err != nil {
			t.Errorf("port %q refused: %v", p, err)
		}
	}
	for status, want := range map[int]Code{401: Blocked, 403: Blocked, 429: Blocked, 407: AuthFailed, 402: QuotaExceeded, 502: Blocked} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		_, err := BrightData{Key: "k", Zone: "z", Endpoint: srv.URL}.Fetch(context.Background(), "https://example.com", 0)
		srv.Close()
		if CodeOf(err) != want {
			t.Errorf("bright data %d: got %v want %s", status, err, want)
		}
	}
}

func TestCheckResolvedRefusesAnyBlockedAnswer(t *testing.T) {
	old := resolveIPs
	defer func() { resolveIPs = old }()
	answers := map[string][]string{"ok.example": {"93.184.216.34"}, "mixed.example": {"93.184.216.34", "10.0.0.7"}, "meta.example": {"169.254.169.254"}}
	resolveIPs = func(_ context.Context, host string) ([]net.IPAddr, error) {
		var out []net.IPAddr
		for _, a := range answers[host] {
			out = append(out, net.IPAddr{IP: net.ParseIP(a)})
		}
		return out, nil
	}
	for host, want := range map[string]Code{"ok.example": "", "mixed.example": TargetRefused, "meta.example": TargetRefused, "none.example": Network} {
		err := CheckResolved(context.Background(), "https://"+host+"/")
		got := Code("")
		if err != nil {
			got = CodeOf(err)
		}
		if got != want {
			t.Errorf("%s: got %q want %q", host, got, want)
		}
	}
}

func TestCleanBaseURLKeepsSchemeAndHostOnly(t *testing.T) {
	u, err := CleanBaseURL(" http://searx.lan:8080/some/path?x=1#frag ")
	if err != nil || u.String() != "http://searx.lan:8080" {
		t.Fatalf("%v %v", u, err)
	}
	for _, bad := range []string{"ftp://x", "http://u:p@x", "", "http://"} {
		if _, err := CleanBaseURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
