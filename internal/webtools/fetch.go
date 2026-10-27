package webtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

var (
	FirecrawlEndpoint  = "https://api.firecrawl.dev/v2/scrape"
	BrightDataEndpoint = "https://api.brightdata.com/request"
)

const (
	// MaxBodyBytes caps what the built-in fetch reads from a page.
	MaxBodyBytes = 2 << 20
	// FetchTimeout is the whole-request limit of the built-in fetch.
	FetchTimeout = 20 * time.Second
	MaxRedirects = 5
)

// ValidateTarget parses an address a Bot asked for. Only http and https
// without embedded credentials, and no literal private, loopback, link-local,
// CGNAT or metadata address. A name that resolves to one is refused at dial
// time by the guarded transport.
func ValidateTarget(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 {
		return nil, fail(BadRequest)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, fail(BadRequest)
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	if a, err := netip.ParseAddr(host); err == nil && extensions.BlockedAddr(a) {
		return nil, fail(TargetRefused)
	}
	return u, nil
}

// resolveIPs is the resolver CheckResolved uses; tests replace it.
var resolveIPs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// CheckResolved resolves the host of rawURL now and refuses when any answer is
// a blocked address. It is for callers that cannot dial through the guarded
// transport (the Bot computer's browser). It narrows the DNS-rebinding window
// to the time between this check and the browser's own lookup; it does not
// close it, and it cannot see redirects the browser follows.
func CheckResolved(ctx context.Context, rawURL string) error {
	u, err := ValidateTarget(rawURL)
	if err != nil {
		return err
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	if _, err := netip.ParseAddr(host); err == nil {
		return nil // a literal was judged by ValidateTarget
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := resolveIPs(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fail(Network)
	}
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok || extensions.BlockedAddr(ip) {
			return fail(TargetRefused)
		}
	}
	return nil
}

// Builtin fetches a page with a plain GET through the guarded transport and
// turns it into readable Markdown.
type Builtin struct {
	// Transport defaults to extensions.GuardedTransport(). Tests may replace it.
	Transport http.RoundTripper
}

func (Builtin) ID() string { return "builtin" }

func (b Builtin) client() *http.Client {
	rt := b.Transport
	if rt == nil {
		rt = extensions.GuardedTransport()
	}
	return &http.Client{
		Transport: rt,
		Timeout:   FetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return errTooManyRedirects
			}
			if _, err := b.checkTarget(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

// checkBuiltinTarget is ValidateTarget plus the port rule of the built-in
// fetch: only the ports websites use. Other ports fall through to the next
// source (a hosted fetcher or the computer), they are not refused outright.
func (b Builtin) checkTarget(raw string) (*url.URL, error) {
	u, err := ValidateTarget(raw)
	if err != nil {
		return nil, err
	}
	if b.Transport != nil {
		return u, nil // a replaced transport is a test seam; httptest ports are random
	}
	switch u.Port() {
	case "", "80", "443", "8080", "8443":
		return u, nil
	}
	return nil, fail(Unsupported)
}

var errTooManyRedirects = errors.New("too many redirects")

func readableType(ct string) (kind string, ok bool) {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(ct))
	}
	switch {
	case mt == "text/html" || mt == "application/xhtml+xml" || mt == "":
		return "html", true
	case mt == "text/plain" || mt == "text/markdown" || mt == "text/csv" || mt == "application/json" || mt == "text/xml" || mt == "application/xml" || strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml"):
		return "text", true
	}
	return "", false
}

func (b Builtin) Fetch(ctx context.Context, rawURL string, maxChars int) (Page, error) {
	u, err := b.checkTarget(rawURL)
	if err != nil {
		return Page{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, fail(BadRequest)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TOFI/1.0; +https://github.com/JackZhao98/tofibot)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	resp, err := b.client().Do(req)
	if err != nil {
		var coded *Error
		switch {
		case errors.Is(err, extensions.ErrBlockedAddress):
			return Page{}, fail(TargetRefused)
		case errors.As(err, &coded):
			return Page{}, coded
		}
		return Page{}, fail(Network)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return Page{}, fail(NotFound)
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 451:
		return Page{}, fail(Blocked)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return Page{}, fail(Network)
	}
	kind, ok := readableType(resp.Header.Get("Content-Type"))
	if !ok {
		return Page{}, fail(Unsupported)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return Page{}, fail(Network)
	}
	cut := len(body) > MaxBodyBytes
	if cut {
		body = body[:MaxBodyBytes]
	}
	if !utf8.Valid(body) {
		body = bytes.ToValidUTF8(body, []byte("�"))
	}
	final := resp.Request.URL
	var title, text string
	if kind == "html" {
		doc := ParseHTML(ctx, string(body), final)
		title, text = doc.Title, doc.Markdown
		if len([]rune(strings.TrimSpace(text))) < 80 {
			if doc.Scripts > 0 {
				return Page{}, fail(NeedsJavaScript)
			}
			return Page{}, fail(EmptyContent)
		}
	} else {
		text = strings.TrimSpace(string(body))
	}
	return capPage(Page{URL: final.String(), Title: title, Content: text, Truncated: cut}, maxChars), nil
}

func capPage(p Page, maxChars int) Page {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	if r := []rune(p.Content); len(r) > maxChars {
		p.Content, p.Truncated = string(r[:maxChars]), true
	}
	return p
}

// Firecrawl scrapes through the Firecrawl scrape API (Markdown output).
type Firecrawl struct {
	Key      string
	Endpoint string // default FirecrawlEndpoint
	Client   *http.Client
}

func (Firecrawl) ID() string { return "firecrawl" }

func (f Firecrawl) Fetch(ctx context.Context, rawURL string, maxChars int) (Page, error) {
	if f.Key == "" {
		return Page{}, fail(NotConfigured)
	}
	ep := f.Endpoint
	if ep == "" {
		ep = FirecrawlEndpoint
	}
	body, _ := json.Marshal(map[string]any{"url": rawURL, "formats": []string{"markdown"}, "onlyMainContent": true, "timeout": 45000})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(body))
	if err != nil {
		return Page{}, fail(BadRequest)
	}
	req.Header.Set("Authorization", "Bearer "+f.Key)
	req.Header.Set("Content-Type", "application/json")
	c := f.Client
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second} // above the 45 s scrape timeout it asks for
	}
	raw, err := do(c, req)
	if err != nil {
		return Page{}, err
	}
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
			Metadata struct {
				Title      string `json:"title"`
				SourceURL  string `json:"sourceURL"`
				StatusCode int    `json:"statusCode"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || !out.Success {
		return Page{}, fail(Network)
	}
	if sc := out.Data.Metadata.StatusCode; sc == 404 || sc == 410 {
		return Page{}, fail(NotFound)
	} else if sc >= 400 {
		return Page{}, fail(Blocked)
	}
	u := out.Data.Metadata.SourceURL
	if u == "" {
		u = rawURL
	}
	return capPage(Page{URL: u, Title: out.Data.Metadata.Title, Content: strings.TrimSpace(out.Data.Markdown)}, maxChars), nil
}

// BrightData fetches through a Web Unlocker zone. The stored key holds the API
// key and the zone name.
type BrightData struct {
	Key      string
	Zone     string
	Endpoint string // default BrightDataEndpoint
	Client   *http.Client
}

func (BrightData) ID() string { return "brightdata" }

func (b BrightData) Fetch(ctx context.Context, rawURL string, maxChars int) (Page, error) {
	if b.Key == "" || b.Zone == "" {
		return Page{}, fail(NotConfigured)
	}
	ep := b.Endpoint
	if ep == "" {
		ep = BrightDataEndpoint
	}
	body, _ := json.Marshal(map[string]any{"zone": b.Zone, "url": rawURL, "format": "raw"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(body))
	if err != nil {
		return Page{}, fail(BadRequest)
	}
	req.Header.Set("Authorization", "Bearer "+b.Key)
	req.Header.Set("Content-Type", "application/json")
	c := b.Client
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	req.Header.Set("User-Agent", "tofi/1.0")
	resp, err := c.Do(req)
	if err != nil {
		return Page{}, fail(Network)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return Page{}, fail(NotFound)
	case resp.StatusCode == 400:
		return Page{}, fail(NotConfigured) // unknown zone
	case resp.StatusCode == 407:
		// Web Unlocker passes the target's own status through on format raw, so a
		// 401, 403 or 429 is usually the site refusing, not our key. Only 407
		// (proxy authentication) and 402 (no credit) speak for the account.
		return Page{}, fail(AuthFailed)
	case resp.StatusCode == 402:
		return Page{}, fail(QuotaExceeded)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return Page{}, fail(Blocked)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return Page{}, fail(Network)
	}
	cut := len(data) > MaxBodyBytes
	if cut {
		data = data[:MaxBodyBytes]
	}
	data = bytes.ToValidUTF8(data, []byte("�"))
	base, _ := url.Parse(rawURL)
	text := string(data)
	title := ""
	if kind, _ := readableType(resp.Header.Get("Content-Type")); kind == "text" {
		text = strings.TrimSpace(text)
	} else {
		doc := ParseHTML(ctx, text, base)
		title, text = doc.Title, doc.Markdown
		if len([]rune(strings.TrimSpace(text))) < 80 {
			if doc.Scripts > 0 {
				return Page{}, fail(NeedsJavaScript)
			}
			return Page{}, fail(EmptyContent)
		}
	}
	return capPage(Page{URL: rawURL, Title: title, Content: text, Truncated: cut}, maxChars), nil
}
