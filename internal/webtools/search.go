package webtools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiTimeout = 25 * time.Second
	maxAPIBody = 4 << 20
)

// Vendor endpoints. Variables so a test can point a provider at a local server.
var (
	TavilyEndpoint = "https://api.tavily.com/search"
	BraveEndpoint  = "https://api.search.brave.com/res/v1/web/search"
)

// APIClient is the shared HTTP client for the fixed vendor hosts and the
// owner's SearXNG address. It follows no proxy settings it was not given.
func APIClient() *http.Client { return &http.Client{Timeout: apiTimeout} }

// statusCode maps a vendor HTTP status to a code, never by body text.
func statusCode(status int) Code {
	switch {
	case status == 401 || status == 403:
		return AuthFailed
	case status == 402 || status == 429 || status == 432 || status == 433:
		return QuotaExceeded
	}
	return Network
}

func readBody(resp *http.Response) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBody))
	if err != nil {
		return nil, fail(Network)
	}
	return b, nil
}

// do sends the request and returns the body of a 2xx answer, else a coded error.
func do(c *http.Client, req *http.Request) ([]byte, error) {
	if c == nil {
		c = APIClient()
	}
	req.Header.Set("User-Agent", "tofi/1.0")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fail(Network)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fail(statusCode(resp.StatusCode))
	}
	return readBody(resp)
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}
	return string(r)
}

// Tavily searches through the Tavily Search API.
type Tavily struct {
	Key      string
	Endpoint string // default TavilyEndpoint
	Client   *http.Client
}

func (Tavily) ID() string { return "tavily" }

func (t Tavily) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if t.Key == "" {
		return nil, fail(NotConfigured)
	}
	ep := t.Endpoint
	if ep == "" {
		ep = TavilyEndpoint
	}
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": limit, "search_depth": "basic", "include_answer": false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(body))
	if err != nil {
		return nil, fail(BadRequest)
	}
	req.Header.Set("Authorization", "Bearer "+t.Key)
	req.Header.Set("Content-Type", "application/json")
	raw, err := do(t.Client, req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct{ Title, URL, Content string } `json:"results"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil, fail(Network)
	}
	var res []SearchResult
	for _, r := range out.Results {
		if r.URL != "" {
			res = append(res, SearchResult{Title: clip(r.Title, 300), URL: r.URL, Snippet: clip(r.Content, 600)})
		}
	}
	return res, nil
}

// Brave searches through the Brave Search API.
type Brave struct {
	Key      string
	Endpoint string // default BraveEndpoint
	Client   *http.Client
}

func (Brave) ID() string { return "brave" }

func (b Brave) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if b.Key == "" {
		return nil, fail(NotConfigured)
	}
	ep := b.Endpoint
	if ep == "" {
		ep = BraveEndpoint
	}
	q := url.Values{"q": {query}, "count": {strconv.Itoa(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fail(BadRequest)
	}
	req.Header.Set("X-Subscription-Token", b.Key)
	req.Header.Set("Accept", "application/json")
	raw, err := do(b.Client, req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Web struct {
			Results []struct{ Title, URL, Description string } `json:"results"`
		} `json:"web"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil, fail(Network)
	}
	var res []SearchResult
	for _, r := range out.Web.Results {
		if r.URL != "" {
			res = append(res, SearchResult{Title: clip(StripTags(r.Title), 300), URL: r.URL, Snippet: clip(StripTags(r.Description), 600)})
		}
	}
	return res, nil
}

// SearXNG searches an instance the owner runs. The address is the owner's own
// setting, so it may be a private one; it is not dialed through the guard.
type SearXNG struct {
	BaseURL string
	Client  *http.Client
}

func (SearXNG) ID() string { return "searxng" }

func (s SearXNG) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(s.BaseURL), "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fail(NotConfigured)
	}
	q := url.Values{"q": {query}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, fail(BadRequest)
	}
	req.Header.Set("Accept", "application/json")
	c := s.Client
	if c == nil {
		c = APIClient()
	}
	req.Header.Set("User-Agent", "tofi/1.0")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fail(Network)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden:
		// The instance does not serve format=json (search.formats in settings.yml).
		return nil, fail(NotConfigured)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fail(Network)
	}
	raw, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct{ Title, URL, Content string } `json:"results"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil, fail(NotConfigured)
	}
	var res []SearchResult
	for _, r := range out.Results {
		if r.URL != "" && len(res) < limit {
			res = append(res, SearchResult{Title: clip(r.Title, 300), URL: r.URL, Snippet: clip(r.Content, 600)})
		}
	}
	return res, nil
}
