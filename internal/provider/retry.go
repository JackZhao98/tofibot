package provider

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"
)

// RetryConfig controls retry behavior for API calls.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (default 5).
	MaxRetries int
	// BaseDelay is the initial delay before the first retry (default 1s).
	BaseDelay time.Duration
	// MaxDelay caps the exponential backoff (default 30s).
	MaxDelay time.Duration
	// FallbackModel is used when rate limits persist after MaxRateLimitRetries (optional).
	FallbackModel string
	// MaxRateLimitRetries triggers fallback after this many consecutive 429/529s (default 3).
	MaxRateLimitRetries int
	// OnRetry is called before each retry attempt (optional, for logging/metrics).
	OnRetry func(attempt int, err error, delay time.Duration)
}

func (c RetryConfig) maxRetries() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return 5
}

func (c RetryConfig) baseDelay() time.Duration {
	if c.BaseDelay > 0 {
		return c.BaseDelay
	}
	return 1 * time.Second
}

func (c RetryConfig) maxDelay() time.Duration {
	if c.MaxDelay > 0 {
		return c.MaxDelay
	}
	return 30 * time.Second
}

func (c RetryConfig) maxRateLimitRetries() int {
	if c.MaxRateLimitRetries > 0 {
		return c.MaxRateLimitRetries
	}
	return 3
}

// RetryProvider wraps a Provider with automatic retry, backoff, and model fallback.
type RetryProvider struct {
	inner  Provider
	config RetryConfig
}

// NewRetryProvider creates a RetryProvider wrapping the given provider.
func NewRetryProvider(inner Provider, config RetryConfig) *RetryProvider {
	return &RetryProvider{
		inner:  inner,
		config: config,
	}
}

func (r *RetryProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var lastErr error
	rateLimitCount := 0

	for attempt := 0; attempt <= r.config.maxRetries(); attempt++ {
		if attempt > 0 {
			delay := r.calculateDelay(attempt)
			if r.config.OnRetry != nil {
				r.config.OnRetry(attempt, lastErr, delay)
			} else {
				log.Printf("[retry] attempt %d/%d after %v (error: %v)",
					attempt, r.config.maxRetries(), delay, lastErr)
			}
			observeRetry(ctx, attempt, lastErr, delay)

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		resp, err := r.inner.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Context cancelled — don't retry
		if ctx.Err() != nil {
			return nil, err
		}

		// Context overflow — don't retry, caller should compact
		if IsContextOverflow(err) {
			return nil, err
		}

		// Rate limit tracking for fallback
		if IsRateLimited(err) {
			rateLimitCount++
			if rateLimitCount >= r.config.maxRateLimitRetries() && r.config.FallbackModel != "" {
				log.Printf("[retry] %d consecutive rate limits, falling back to model %s",
					rateLimitCount, r.config.FallbackModel)
				fallbackReq := copyRequestWithModel(req, r.config.FallbackModel)
				return r.inner.Chat(ctx, fallbackReq)
			}
		} else {
			rateLimitCount = 0
		}

		if !IsRetryable(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", r.config.maxRetries(), lastErr)
}

func (r *RetryProvider) ChatStream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	var lastErr error
	rateLimitCount := 0

	for attempt := 0; attempt <= r.config.maxRetries(); attempt++ {
		if attempt > 0 {
			delay := r.calculateDelay(attempt)
			if r.config.OnRetry != nil {
				r.config.OnRetry(attempt, lastErr, delay)
			} else {
				log.Printf("[retry] stream attempt %d/%d after %v (error: %v)",
					attempt, r.config.maxRetries(), delay, lastErr)
			}
			observeRetry(ctx, attempt, lastErr, delay)

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		// Forward chunks immediately. Once a provider has emitted a chunk,
		// retrying would replay an unknown prefix, so that attempt is terminal.
		delivered := false
		forward := func(delta StreamDelta) {
			delivered = true
			if onDelta != nil {
				onDelta(delta)
			}
		}
		resp, err := r.inner.ChatStream(ctx, req, forward)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		if ctx.Err() != nil || delivered {
			return nil, err
		}

		if IsContextOverflow(err) {
			return nil, err
		}

		if IsRateLimited(err) {
			rateLimitCount++
			if rateLimitCount >= r.config.maxRateLimitRetries() && r.config.FallbackModel != "" {
				log.Printf("[retry] stream: %d consecutive rate limits, falling back to model %s",
					rateLimitCount, r.config.FallbackModel)
				fallbackReq := copyRequestWithModel(req, r.config.FallbackModel)
				return r.inner.ChatStream(ctx, fallbackReq, forward)
			}
		} else {
			rateLimitCount = 0
		}

		if !IsRetryable(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", r.config.maxRetries(), lastErr)
}

// calculateDelay returns exponential backoff with 25% jitter.
// Formula: min(baseDelay * 2^(attempt-1), maxDelay) * (1 + rand(0, 0.25))
func (r *RetryProvider) calculateDelay(attempt int) time.Duration {
	base := r.config.baseDelay()
	maxD := r.config.maxDelay()

	delay := float64(base) * math.Pow(2, float64(attempt-1))
	if delay > float64(maxD) {
		delay = float64(maxD)
	}

	// Add 25% jitter
	jitter := delay * 0.25 * rand.Float64()
	return time.Duration(delay + jitter)
}

func copyRequestWithModel(req *ChatRequest, model string) *ChatRequest {
	copy := *req
	copy.Model = model
	return &copy
}

type retryObserverKey struct{}

// WithRetryObserver lets one caller observe RetryProvider backoffs for requests
// made with ctx, independent of the provider's shared RetryConfig.
func WithRetryObserver(ctx context.Context, observe func(attempt int, err error, delay time.Duration)) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, retryObserverKey{}, observe)
}

func observeRetry(ctx context.Context, attempt int, err error, delay time.Duration) {
	if observe, ok := ctx.Value(retryObserverKey{}).(func(int, error, time.Duration)); ok {
		observe(attempt, err, delay)
	}
}
