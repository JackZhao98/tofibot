package provider

import (
	"fmt"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *APIError
		contains string
	}{
		{"basic error", NewAPIError("openai", 429, "rate limited"), "openai API error (HTTP 429)"},
		{"with body", NewAPIError("anthropic", 500, "internal server error"), "anthropic API error (HTTP 500)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if msg == "" {
				t.Fatal("error message should not be empty")
			}
			if !containsStr(msg, tt.contains) {
				t.Errorf("expected error to contain %q, got %q", tt.contains, msg)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil error", nil, false}, {"429 rate limit", NewAPIError("openai", 429, ""), true},
		{"529 overloaded", NewAPIError("anthropic", 529, ""), true}, {"500 server error", NewAPIError("gemini", 500, ""), true},
		{"502 bad gateway", NewAPIError("openai", 502, ""), true}, {"503 unavailable", NewAPIError("openai", 503, ""), true},
		{"400 bad request", NewAPIError("openai", 400, ""), false}, {"401 unauthorized", NewAPIError("openai", 401, ""), false},
		{"404 not found", NewAPIError("openai", 404, ""), false}, {"connection refused", fmt.Errorf("dial tcp: connection refused"), true},
		{"connection reset", fmt.Errorf("read: connection reset by peer"), true}, {"timeout", fmt.Errorf("context deadline exceeded (i/o timeout)"), true},
		{"random error", fmt.Errorf("something went wrong"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryable(tt.err); got != tt.retryable {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		limited bool
	}{
		{"429", NewAPIError("openai", 429, ""), true}, {"529", NewAPIError("anthropic", 529, ""), true},
		{"500", NewAPIError("openai", 500, ""), false}, {"non-api error", fmt.Errorf("random error"), false}, {"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRateLimited(tt.err); got != tt.limited {
				t.Errorf("IsRateLimited() = %v, want %v", got, tt.limited)
			}
		})
	}
}

func TestIsContextOverflow(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		overflow bool
	}{
		{"context length", fmt.Errorf("context length exceeded"), true}, {"context window", fmt.Errorf("exceeds context window"), true},
		{"too many tokens", fmt.Errorf("too many tokens in request"), true}, {"input too long", fmt.Errorf("input is too long"), true},
		{"max_tokens exceed", fmt.Errorf("max_tokens would exceed the limit"), true}, {"normal error", fmt.Errorf("something else"), false}, {"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflow(tt.err); got != tt.overflow {
				t.Errorf("IsContextOverflow() = %v, want %v", got, tt.overflow)
			}
		})
	}
}

func TestAsAPIError(t *testing.T) {
	apiErr := NewAPIError("openai", 429, "rate limited")
	wrapped := fmt.Errorf("wrapper: %w", apiErr)
	if extracted, ok := AsAPIError(apiErr); !ok || extracted.StatusCode != 429 {
		t.Error("should extract direct APIError")
	}
	if extracted, ok := AsAPIError(wrapped); !ok || extracted.StatusCode != 429 {
		t.Error("should extract wrapped APIError")
	}
	if _, ok := AsAPIError(fmt.Errorf("not an api error")); ok {
		t.Error("should not extract from non-APIError")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstr(s, substr))
}
func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
