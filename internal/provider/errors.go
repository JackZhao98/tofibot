package provider

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// APIError represents a structured error from an LLM provider API call.
type APIError struct {
	StatusCode int
	Provider   string
	Body       string
	Err        error
	// RetryAfter is the wait the server asked for (Retry-After header); zero
	// when it sent none.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s API error (HTTP %d): %s: %v", e.Provider, e.StatusCode, e.Body, e.Err)
	}
	return fmt.Sprintf("%s API error (HTTP %d): %s", e.Provider, e.StatusCode, e.Body)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// NewAPIError creates a new APIError.
func NewAPIError(provider string, statusCode int, body string) *APIError {
	return &APIError{
		StatusCode: statusCode,
		Provider:   provider,
		Body:       body,
	}
}

// NewAPIErrorFor is NewAPIError for a failed HTTP response: it also records
// the wait the server asked for.
func NewAPIErrorFor(provider string, resp *http.Response, body string) *APIError {
	e := NewAPIError(provider, resp.StatusCode, body)
	e.RetryAfter = parseRetryAfter(resp.Header, time.Now())
	return e
}

// parseRetryAfter reads retry-after-ms, then Retry-After as seconds or an
// HTTP date. A missing, malformed or negative value is zero.
func parseRetryAfter(h http.Header, now time.Time) time.Duration {
	if ms, err := strconv.ParseFloat(strings.TrimSpace(h.Get("Retry-After-Ms")), 64); err == nil && ms > 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if sec, err := strconv.ParseFloat(v, 64); err == nil {
		if sec < 0 {
			return 0
		}
		return time.Duration(sec * float64(time.Second))
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// RetryAfterOf returns the wait a failed response asked for, or zero.
func RetryAfterOf(err error) time.Duration {
	if apiErr, ok := AsAPIError(err); ok {
		return apiErr.RetryAfter
	}
	return 0
}

// QuotaMarkers are the provider body fragments that mean the account has no
// usage left. Waiting cannot fix them, so they are never retried.
var QuotaMarkers = []string{"usage_limit_reached", "insufficient_quota", "credit balance is too low"}

// IsQuotaExhausted reports whether text names an exhausted account quota.
func IsQuotaExhausted(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range QuotaMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// IsRetryable returns true if the error is a transient failure that should be retried.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	if IsRequestRejection(err) {
		return false // the same request would be refused the same way
	}

	apiErr, ok := AsAPIError(err)
	if ok {
		switch {
		case apiErr.StatusCode == 429: // Rate limited, unless the account is out of quota
			return !IsQuotaExhausted(apiErr.Body)
		case apiErr.StatusCode == 529: // Overloaded
			return true
		case apiErr.StatusCode >= 500 && apiErr.StatusCode < 600: // Server errors
			return true
		default:
			return false
		}
	}

	if errors.Is(err, ErrStreamIncomplete) {
		return true
	}
	var failed *ResponseFailedError
	if errors.As(err, &failed) {
		// An in-stream failure arrives with HTTP 200, so the provider's own
		// code is the only status there is.
		return retryableResponseFailure(failed.Code)
	}
	var incomplete *IncompleteResponseError
	var refusal *RefusalError
	if errors.As(err, &incomplete) || errors.As(err, &refusal) {
		return false // the same request would stop the same way
	}

	// Connection-level errors (no HTTP response received)
	msg := err.Error()
	return isConnectionError(msg)
}

// retryableResponseFailure reports the in-stream failure codes that are
// transient on the provider's side: server faults, overload and rate limits.
// Quota exhaustion, invalid input and policy codes are not.
func retryableResponseFailure(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "server_error", "internal_error", "server_is_overloaded", "overloaded", "overloaded_error",
		"service_unavailable", "rate_limit_exceeded", "rate_limit_error", "timeout", "bad_gateway", "gateway_timeout":
		return true
	}
	return false
}

// IsRateLimited returns true if the error is a rate limit (429/529).
func IsRateLimited(err error) bool {
	apiErr, ok := AsAPIError(err)
	if !ok {
		return false
	}
	return apiErr.StatusCode == 429 || apiErr.StatusCode == 529
}

// IsContextOverflow returns true if the error indicates context window exceeded.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context length") ||
		strings.Contains(msg, "context window") ||
		strings.Contains(msg, "maximum context") ||
		strings.Contains(msg, "max_tokens") && strings.Contains(msg, "exceed") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "input is too long") ||
		strings.Contains(msg, "prompt is too long")
}

// AsAPIError extracts an APIError from an error chain.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if err == nil {
		return nil, false
	}
	// Direct type assertion first
	if ae, ok := err.(*APIError); ok {
		return ae, true
	}
	// Check wrapped errors
	type unwrapper interface {
		Unwrap() error
	}
	if u, ok := err.(unwrapper); ok {
		return AsAPIError(u.Unwrap())
	}
	return apiErr, false
}

func isConnectionError(msg string) bool {
	lower := strings.ToLower(msg)
	connectionPatterns := []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"eof",
		"timeout",
		"tls handshake",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"internal_error",
	}
	for _, pattern := range connectionPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// StreamIdleError reports a streaming response that delivered no bytes for
// Idle. It is transient: RetryProvider retries it when nothing was forwarded.
type StreamIdleError struct{ Idle time.Duration }

func (e *StreamIdleError) Error() string {
	return fmt.Sprintf("stream idle timeout: no data for %s", e.Idle)
}

// IsStreamIdle reports whether err is (or wraps) a StreamIdleError.
func IsStreamIdle(err error) bool {
	var idle *StreamIdleError
	return errors.As(err, &idle)
}

// StreamWallCapError reports a single streaming attempt that ran past Cap
// after its response headers arrived.
type StreamWallCapError struct{ Cap time.Duration }

func (e *StreamWallCapError) Error() string {
	return fmt.Sprintf("stream wall cap: attempt exceeded %s", e.Cap)
}

// IsStreamWatchdog reports an idle or wall-cap abort. Each already cost
// minutes, so RetryProvider leaves the single retry to the caller.
func IsStreamWatchdog(err error) bool {
	var wall *StreamWallCapError
	return IsStreamIdle(err) || errors.As(err, &wall)
}

// IncompleteResponseError reports a response the model stopped before
// finishing (Anthropic stop_reason max_tokens or
// model_context_window_exceeded). Its partial output, possibly a truncated
// tool call, is discarded. A context-window stop matches IsContextOverflow.
type IncompleteResponseError struct {
	Provider string
	Reason   string
}

func (e *IncompleteResponseError) Error() string {
	if e.Reason == "model_context_window_exceeded" {
		return fmt.Sprintf("%s response incomplete: model context window exceeded", e.Provider)
	}
	return fmt.Sprintf("%s response incomplete: %s", e.Provider, e.Reason)
}

// RefusalError reports a response the model's safety classifiers declined
// (Anthropic stop_reason refusal). It is not retryable.
type RefusalError struct {
	Provider    string
	Category    string
	Explanation string
}

func (e *RefusalError) Error() string {
	msg := e.Provider + " model declined the request"
	if e.Category != "" {
		msg += " (" + e.Category + ")"
	}
	if e.Explanation != "" {
		msg += ": " + e.Explanation
	}
	return msg
}

// ErrStreamIncomplete reports a stream that ended without a terminal event.
var ErrStreamIncomplete = errors.New("stream ended before response.completed")

var reasoningItemIDPattern = regexp.MustCompile(`\brs_[A-Za-z0-9]`)

// ResponseFailedError is a response.failed event inside an accepted stream.
type ResponseFailedError struct{ Code, Message string }

func (e *ResponseFailedError) Error() string {
	return fmt.Sprintf("response failed: [%s] %s", e.Code, e.Message)
}

// isReasoningReplayRejection matches a rejection of replayed reasoning items,
// as a 400/404 or an in-stream response.failed. Other errors keep their
// normal handling.
func isReasoningReplayRejection(err error) bool {
	var failed *ResponseFailedError
	if errors.As(err, &failed) {
		return mentionsReasoningReplay(failed.Message)
	}
	apiErr, ok := AsAPIError(err)
	if !ok || (apiErr.StatusCode != 400 && apiErr.StatusCode != 404) {
		return false
	}
	return mentionsReasoningReplay(apiErr.Body)
}

func mentionsReasoningReplay(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "encrypted_content") || strings.Contains(lower, "reasoning item") ||
		reasoningItemIDPattern.MatchString(text)
}

// RequestPrefillRejected is the code of a request the provider refused because
// it ended with an assistant message (assistant prefill is not supported by the
// model). The request builder never sends one, so this marks a defect to fix,
// not a condition to retry.
const RequestPrefillRejected = "provider_invalid_request_prefill"

// RequestRejectionError is a provider 400 that has a stable code. It wraps the
// original APIError, so status and body classification still work, and it is
// never retried: the identical request would be refused again.
type RequestRejectionError struct {
	Code string
	Err  *APIError
}

func (e *RequestRejectionError) Error() string { return "[" + e.Code + "] " + e.Err.Error() }
func (e *RequestRejectionError) Unwrap() error { return e.Err }

// IsRequestRejection reports whether err carries a request-rejection code.
func IsRequestRejection(err error) bool {
	var rejection *RequestRejectionError
	return errors.As(err, &rejection)
}

// RequestRejectionCode returns the code of a request rejection, or "".
func RequestRejectionCode(err error) string {
	var rejection *RequestRejectionError
	if errors.As(err, &rejection) {
		return rejection.Code
	}
	return ""
}

// codeRequestRejection tags a provider 400 that refuses an assistant-final
// conversation. The match is on the API's own sentence about prefill in a 400
// body; any other error is returned unchanged.
func codeRequestRejection(err error) error {
	if err == nil || IsRequestRejection(err) {
		return err
	}
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.StatusCode != 400 {
		return err
	}
	body := strings.ToLower(apiErr.Body)
	if strings.Contains(body, "prefill") || strings.Contains(body, "must end with a user message") {
		return &RequestRejectionError{Code: RequestPrefillRejected, Err: apiErr}
	}
	return err
}
