// Package webtools holds the server-side web_search and web_fetch providers
// and the ordered fallback chains that run them. A provider failure is
// reported as a stable Code; a chain moves on by code, never by message text,
// and no upstream message or credential is ever carried in an error.
package webtools

import (
	"context"
	"errors"
	"strings"
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

// Error is a provider failure. Its text is fixed per code.
type Error struct{ Code Code }

func (e *Error) Error() string { return string(e.Code) }

func fail(c Code) error { return &Error{Code: c} }

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
}

// ChainError is returned when no provider of a chain served the request.
type ChainError struct {
	Attempts []Attempt
	Last     Code
}

func (e *ChainError) Error() string {
	parts := make([]string, 0, len(e.Attempts))
	for _, a := range e.Attempts {
		parts = append(parts, a.Source+":"+string(a.Code))
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
}

type Page struct {
	Source    string `json:"source"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Content   string `json:"content"`
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

type FetchProvider interface {
	ID() string
	Fetch(ctx context.Context, url string, maxChars int) (Page, error)
}

// SearchFunc and FetchFunc adapt a function (the Bot computer) to a provider.
type SearchFunc struct {
	Name string
	Fn   func(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

func (f SearchFunc) ID() string { return f.Name }
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
		results, err := p.Search(ctx, query, limit)
		if err == nil && len(results) == 0 {
			err = fail(EmptyContent)
		}
		if err == nil {
			return SearchResponse{Source: p.ID(), Query: query, Results: results}, nil
		}
		code := CodeOf(err)
		attempts = append(attempts, Attempt{Source: p.ID(), Code: code})
		if !code.Falls() {
			return SearchResponse{}, &ChainError{Attempts: attempts, Last: code}
		}
	}
	if len(attempts) == 0 {
		return SearchResponse{}, &ChainError{Last: NotConfigured}
	}
	return SearchResponse{}, &ChainError{Attempts: attempts, Last: attempts[len(attempts)-1].Code}
}

// Fetch runs the chain in order until one provider returns readable content.
// The address is checked once before any provider sees it.
func Fetch(ctx context.Context, chain []FetchProvider, rawURL string, maxChars int) (Page, error) {
	u, err := ValidateTarget(rawURL)
	if err != nil {
		return Page{}, &ChainError{Attempts: []Attempt{{Source: "request", Code: CodeOf(err)}}, Last: CodeOf(err)}
	}
	if maxChars < 1000 {
		maxChars = DefaultMaxChars
	}
	if maxChars > MaxMaxChars {
		maxChars = MaxMaxChars
	}
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
		attempts = append(attempts, Attempt{Source: p.ID(), Code: code})
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
