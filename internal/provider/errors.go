package provider

import (
	"fmt"
	"strings"
)

// APIError represents a structured error from an LLM provider API call.
type APIError struct {
	StatusCode int
	Provider   string
	Body       string
	Err        error
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

// IsRetryable returns true if the error is a transient failure that should be retried.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	apiErr, ok := AsAPIError(err)
	if ok {
		switch {
		case apiErr.StatusCode == 429: // Rate limited
			return true
		case apiErr.StatusCode == 529: // Overloaded
			return true
		case apiErr.StatusCode >= 500 && apiErr.StatusCode < 600: // Server errors
			return true
		default:
			return false
		}
	}

	// Connection-level errors (no HTTP response received)
	msg := err.Error()
	return isConnectionError(msg)
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
		strings.Contains(msg, "input is too long")
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
	}
	for _, pattern := range connectionPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
