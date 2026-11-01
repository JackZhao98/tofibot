// Package webtools holds the server-side web_search and web_fetch providers
// and the ordered fallback chains that run them. A provider failure is
// reported as a stable Code; a chain moves on by code, never by message text,
// and no upstream message or credential is ever carried in an error.
package webtools

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Code is the stable reason a provider could not serve a request.
type Code string

const (
	AuthFailed      Code = "auth_failed"      // the key was rejected
	QuotaExceeded   Code = "quota_exceeded"   // credits used up or rate limited
	Blocked         Code = "blocked"          // the site or provider refused the request
	EmptyContent    Code = "empty_content"    // nothing readable came back
	NeedsJavaScript Code = "needs_javascript" // the page builds its content in a browser
	Network         Code = "network"          // timeout, connection or upstream failure
	NotConfigured   Code = "not_configured"   // no key, address or computer
	Unsupported     Code = "unsupported_content"
	// The next three end the chain: no other provider may try the request.
	TargetRefused Code = "target_refused" // private, loopback or metadata address
	BadRequest    Code = "bad_request"    // the arguments cannot be served
	NotFound      Code = "not_found"      // the page does not exist (404, 410)
)

// Falls reports whether a chain moves on to the next provider after c.
func (c Code) Falls() bool {
	switch c {
	case AuthFailed, QuotaExceeded, Blocked, EmptyContent, NeedsJavaScript, Network, NotConfigured, Unsupported:
		return true
	}
	return false
}

// Error is a provider failure. Its text is fixed per code. Status is the HTTP
// status behind the code when there is one (0 otherwise); it is never an
// upstream message.
type Error struct {
	Code   Code
	Status int
}

func (e *Error) Error() string { return string(e.Code) }

func fail(c Code) error { return &Error{Code: c} }

// failStatus is fail for a code that came from an HTTP status.
func failStatus(c Code, status int) error { return &Error{Code: c, Status: status} }

// StatusOf returns the HTTP status carried by err, 0 when it has none.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// CodeOf returns the code of err, Network when err carries none.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Network
	}
	return Network
}

// Attempt is one provider tried by a chain.
type Attempt struct {
	Source string `json:"source"`
	Code   Code   `json:"code"`
	Status int    `json:"status,omitempty"`
}

// Skip is a source of the chain that was not tried, and why.
type Skip struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// ChainError is returned when no provider of a chain served the request.
// Skipped is filled by the caller that built the chain, which knows what it
// left out.
type ChainError struct {
	Attempts []Attempt
	Skipped  []Skip
	Last     Code
}

func (e *ChainError) Error() string {
	if len(e.Attempts) == 0 {
		return "no source was available for the request"
	}
	parts := make([]string, 0, len(e.Attempts))
	for _, a := range e.Attempts {
		part := a.Source + ":" + string(a.Code)
		if a.Status != 0 {
			part += " HTTP " + strconv.Itoa(a.Status)
		}
		parts = append(parts, part)
	}
	return "no source could serve the request (" + strings.Join(parts, ", ") + ")"
}

// Code is the code of the last attempt, so a caller can still branch on it.
func (e *ChainError) Code() Code { return e.Last }

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type SearchResponse struct {
	Source  string         `json:"source"`
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
	// Note says when a filter was only best effort at the source that served the results.
	Note string `json:"note,omitempty"`
}

// SearchOptions are the optional filters of a search. Every source applies
// them as far as it can; the chain also drops results whose address breaks a
// domain filter.
type SearchOptions struct {
	Recency        string // day, week, month or year
	Domains        []string
	ExcludeDomains []string
}

// Empty reports whether no filter is set.
func (o SearchOptions) Empty() bool {
	return o.Recency == "" && len(o.Domains) == 0 && len(o.ExcludeDomains) == 0
}

// Clean validates the options: a known recency and bare host names.
func (o SearchOptions) Clean() (SearchOptions, error) {
	o.Recency = strings.ToLower(strings.TrimSpace(o.Recency))
	switch o.Recency {
	case "", "day", "week", "month", "year":
	default:
		return o, fail(BadRequest)
	}
	var err error
	if o.Domains, err = cleanDomains(o.Domains); err != nil {
		return o, err
	}
	if o.ExcludeDomains, err = cleanDomains(o.ExcludeDomains); err != nil {
		return o, err
	}
	return o, nil
}

const maxFilterDomains = 20

// cleanDomains reduces each entry to a lower-case host name: a pasted address
// loses its scheme, path and port, and a leading "www." or "*." is dropped.
func cleanDomains(in []string) ([]string, error) {
	var out []string
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if i := strings.Index(d, "://"); i >= 0 {
			d = d[i+3:]
		}
		if i := strings.IndexAny(d, "/?#"); i >= 0 {
			d = d[:i]
		}
		if i := strings.LastIndex(d, ":"); i >= 0 {
			d = d[:i]
		}
		d = strings.TrimPrefix(strings.TrimPrefix(d, "*."), "www.")
		d = strings.Trim(d, ".")
		if d == "" || len(d) > 253 || !validDomain(d) {
			return nil, fail(BadRequest)
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	if len(out) > maxFilterDomains {
		return nil, fail(BadRequest)
	}
	return out, nil
}

// validDomain accepts dotted host names whose labels hold letters, digits and inner hyphens. Anything else
// (| * % quotes, any whitespace such as U+3000) would reach the search query as an operator or a stray token.
func validDomain(d string) bool {
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}

func hostMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// filterResults drops results outside the include list or inside the exclude list.
func filterResults(in []SearchResult, o SearchOptions) []SearchResult {
	if len(o.Domains) == 0 && len(o.ExcludeDomains) == 0 {
		return in
	}
	var out []SearchResult
	for _, r := range in {
		u, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		if len(o.Domains) > 0 && !slices.ContainsFunc(o.Domains, func(d string) bool { return hostMatches(host, d) }) {
			continue
		}
		if slices.ContainsFunc(o.ExcludeDomains, func(d string) bool { return hostMatches(host, d) }) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// QueryWithSites appends site: operators to a query, for engines with no
// domain parameter.
func QueryWithSites(query string, o SearchOptions) string {
	if len(o.Domains) > 0 {
		parts := make([]string, len(o.Domains))
		for i, d := range o.Domains {
			parts[i] = "site:" + d
		}
		if len(parts) == 1 {
			query += " " + parts[0]
		} else {
			query += " (" + strings.Join(parts, " OR ") + ")"
		}
	}
	for _, d := range o.ExcludeDomains {
		query += " -site:" + d
	}
	return query
}

type Page struct {
	Source    string `json:"source"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Content   string `json:"content"`
	// Offset, NextOffset and TotalChars describe a part of a long page, in characters:
	// the part starts at Offset, the next part at NextOffset (absent on the last part).
	Offset     int `json:"offset,omitempty"`
	NextOffset int `json:"next_offset,omitempty"`
	TotalChars int `json:"total_chars,omitempty"`
	// Note tells the model the text may be incomplete; it decides whether to read the page another way.
	Note string `json:"note,omitempty"`
}

// MaybePartialNote goes with plain-HTTP text from a page that loads content with scripts.
const MaybePartialNote = "This page builds part of its content with scripts, so this text may be incomplete. If it lacks what you need, read the page another way, such as a browser."

// shellText reports whether text from a plain fetch is too thin to trust: almost
// nothing, or a script-built page whose readable part stays short.
func shellText(doc Doc, text string) (needsJS, partial bool) {
	n := len([]rune(strings.TrimSpace(text)))
	switch {
	case n < 80:
		return doc.Scripts > 0, false
	case doc.Shell && n < 1500:
		return true, false
	}
	return false, doc.Scripts > 0 && n < 1500
}

type SearchProvider interface {
	ID() string
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// FilteredSearcher is a provider that takes the search filters. One that does
// not is called without them and the result says so.
type FilteredSearcher interface {
	SearchWith(ctx context.Context, query string, limit int, opts SearchOptions) ([]SearchResult, error)
}

// BestEffortFilters marks a provider whose filters are requests to an engine
// that may ignore them, not parameters the service honours.
type BestEffortFilters interface{ BestEffortFilters() bool }

type FetchProvider interface {
	ID() string
	Fetch(ctx context.Context, url string, maxChars int) (Page, error)
}

// SearchFunc and FetchFunc adapt a function (the Bot computer) to a provider.
type SearchFunc struct {
	Name string
	Fn   func(ctx context.Context, query string, limit int) ([]SearchResult, error)
	// FnWith, when set, takes the filters (best effort, like the engine behind it).
	FnWith func(ctx context.Context, query string, limit int, opts SearchOptions) ([]SearchResult, error)
}

func (f SearchFunc) ID() string { return f.Name }

func (f SearchFunc) SearchWith(ctx context.Context, q string, n int, o SearchOptions) ([]SearchResult, error) {
	if f.FnWith == nil {
		return f.Fn(ctx, q, n)
	}
	return f.FnWith(ctx, q, n, o)
}

func (f SearchFunc) BestEffortFilters() bool { return f.FnWith != nil }
func (f SearchFunc) Search(ctx context.Context, q string, n int) ([]SearchResult, error) {
	return f.Fn(ctx, q, n)
}

type FetchFunc struct {
	Name string
	Fn   func(ctx context.Context, url string, maxChars int) (Page, error)
}

func (f FetchFunc) ID() string { return f.Name }
func (f FetchFunc) Fetch(ctx context.Context, u string, n int) (Page, error) {
	return f.Fn(ctx, u, n)
}

// Search runs the chain in order until one provider returns results.
func Search(ctx context.Context, chain []SearchProvider, query string, limit int) (SearchResponse, error) {
	return SearchWith(ctx, chain, query, limit, SearchOptions{})
}

// SearchWith is Search with filters. A source without filter support is still
// asked, and the response says its results are not filtered.
func SearchWith(ctx context.Context, chain []SearchProvider, query string, limit int, opts SearchOptions) (SearchResponse, error) {
	opts, err := opts.Clean()
	if err != nil {
		return SearchResponse{}, &ChainError{Attempts: []Attempt{{Source: "request", Code: BadRequest}}, Last: BadRequest}
	}
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 2000 {
		return SearchResponse{}, fail(BadRequest)
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	var attempts []Attempt
	for _, p := range chain {
		if err := ctx.Err(); err != nil {
			return SearchResponse{}, fail(Network)
		}
		var results []SearchResult
		var err error
		note := ""
		fs, filtered := p.(FilteredSearcher)
		switch {
		case opts.Empty() || !filtered:
			results, err = p.Search(ctx, query, limit)
			if !opts.Empty() {
				note = "This source ignores the recency and domain filters."
			}
		default:
			results, err = fs.SearchWith(ctx, query, limit, opts)
			if be, ok := p.(BestEffortFilters); ok && be.BestEffortFilters() {
				note = "This source applies the recency and domain filters on a best-effort basis; check the dates and addresses."
			}
		}
		if err == nil {
			results = filterResults(results, opts)
		}
		if err == nil && len(results) == 0 {
			err = fail(EmptyContent)
		}
		if err == nil {
			return SearchResponse{Source: p.ID(), Query: query, Results: results, Note: note}, nil
		}
		code := CodeOf(err)
		attempts = append(attempts, Attempt{Source: p.ID(), Code: code, Status: StatusOf(err)})
		if !code.Falls() {
			return SearchResponse{}, &ChainError{Attempts: attempts, Last: code}
		}
	}
	if len(attempts) == 0 {
		return SearchResponse{}, &ChainError{Last: NotConfigured}
	}
	return SearchResponse{}, &ChainError{Attempts: attempts, Last: attempts[len(attempts)-1].Code}
}

// Fetch runs the chain and returns the first part of the page, at most
// maxChars characters.
func Fetch(ctx context.Context, chain []FetchProvider, rawURL string, maxChars int) (Page, error) {
	page, err := FetchFull(ctx, chain, rawURL)
	if err != nil {
		return Page{}, err
	}
	return Slice(page, 0, maxChars), nil
}

// Slice returns the part of a full page that starts at offset (characters) and
// holds at most maxChars. It names the offset of the next part when the page
// goes on.
func Slice(page Page, offset, maxChars int) Page {
	if maxChars < 1000 {
		maxChars = DefaultMaxChars
	}
	if maxChars > MaxMaxChars {
		maxChars = MaxMaxChars
	}
	runes := []rune(page.Content)
	total := len(runes)
	if offset < 0 {
		offset = 0
	}
	sourceCut := page.Truncated && page.TotalChars == 0
	if offset >= total {
		page.Content, page.Offset, page.NextOffset, page.TotalChars = "", total, 0, total
		page.Truncated = false
		page.Note = "The offset is at or past the end of the page. The page has " + strconv.Itoa(total) + " characters."
		return page
	}
	end := offset + maxChars
	if end >= total {
		end = total
	}
	page.Content = string(runes[offset:end])
	page.TotalChars = total
	page.Offset = offset
	page.NextOffset = 0
	page.Truncated = sourceCut
	if end < total {
		page.NextOffset = end
		page.Truncated = true
		page.Note = "Part of a longer page (" + strconv.Itoa(total) + " characters). Call again with offset " + strconv.Itoa(end) + " to read the next part."
	}
	return page
}

// MaxPageChars is how much text of one page the chain holds so it can be read in parts.
const MaxPageChars = 400000

// FetchFull runs the chain in order until one provider returns readable
// content and returns all of it up to MaxPageChars. The address is checked once
// before any provider sees it.
func FetchFull(ctx context.Context, chain []FetchProvider, rawURL string) (Page, error) {
	u, err := ValidateTarget(rawURL)
	if err != nil {
		return Page{}, &ChainError{Attempts: []Attempt{{Source: "request", Code: CodeOf(err)}}, Last: CodeOf(err)}
	}
	maxChars := MaxPageChars
	var attempts []Attempt
	for _, p := range chain {
		if err := ctx.Err(); err != nil {
			return Page{}, fail(Network)
		}
		page, err := p.Fetch(ctx, u.String(), maxChars)
		if err == nil && strings.TrimSpace(page.Content) == "" {
			err = fail(EmptyContent)
		}
		if err == nil {
			page.Source = p.ID()
			return page, nil
		}
		code := CodeOf(err)
		attempts = append(attempts, Attempt{Source: p.ID(), Code: code, Status: StatusOf(err)})
		if !code.Falls() {
			return Page{}, &ChainError{Attempts: attempts, Last: code}
		}
	}
	if len(attempts) == 0 {
		return Page{}, &ChainError{Last: NotConfigured}
	}
	return Page{}, &ChainError{Attempts: attempts, Last: attempts[len(attempts)-1].Code}
}

const (
	DefaultMaxChars = 20000
	MaxMaxChars     = 60000
)
