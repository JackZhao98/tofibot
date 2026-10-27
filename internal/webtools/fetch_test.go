package webtools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	_, err := Builtin{}.Fetch(context.Background(), target, 0)
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
	if _, err := (Builtin{}).Fetch(context.Background(), url, 0); CodeOf(err) != TargetRefused {
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
	doc := ParseHTML(`<body><nav>menu</nav><div><p>`+strings.Repeat("body words ", 20)+`</p></div><footer>foot</footer></body>`, nil)
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
