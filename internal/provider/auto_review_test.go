package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAutoReviewProviderSlugIsPreserved(t *testing.T) {
	p := &openAICodex{}
	for _, tc := range []struct{ input, want string }{{"codex-auto-review", "codex-auto-review"}, {"codex-gpt-6.1-sol", "gpt-6.1-sol"}, {"codex-gpt-6-astra", "gpt-6-astra"}} {
		if got := p.request(&ChatRequest{Model: tc.input}); got.Model != tc.want {
			t.Fatalf("model=%s want=%s", got.Model, tc.want)
		}
	}
}

type internalErrorProvider struct {
	calls     int
	delivered bool
}

func (p *internalErrorProvider) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	p.calls++
	return nil, errors.New("stream error: INTERNAL_ERROR; received from peer")
}
func (p *internalErrorProvider) ChatStream(ctx context.Context, r *ChatRequest, emit func(StreamDelta)) (*ChatResponse, error) {
	if p.delivered {
		emit(StreamDelta{Content: "partial"})
	}
	return p.Chat(ctx, r)
}
func TestRepeatedInternalErrorHasBoundedRetriesAndNoPrefixReplay(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		p := &internalErrorProvider{delivered: delivered}
		r := NewRetryProvider(p, RetryConfig{MaxRetries: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
		_, err := r.ChatStream(context.Background(), &ChatRequest{Model: "synthetic"}, func(StreamDelta) {})
		want := 3
		if delivered {
			want = 1
		}
		if err == nil || p.calls != want {
			t.Fatalf("delivered=%v calls=%d err=%v", delivered, p.calls, err)
		}
	}
}
