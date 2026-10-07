package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

// blockingProvider holds the model call open until its context is cancelled,
// like a provider stream that is in flight when the service shuts down.
type blockingProvider struct{ started chan struct{} }

func (p blockingProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p blockingProvider) ChatStream(ctx context.Context, _ *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

// Regression for run e8cb6243: the agent loop treats cancellation as a
// partial, empty result. The runtime must report it as an error rather than a
// successful Result with no content, which callers finished as "done".
func TestRunReportsCancelledLoopAsError(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := blockingProvider{started: make(chan struct{})}
	e := &engine{provider: p, model: "test-model"}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-p.started
		cancel()
	}()
	result, err := e.Run(ctx, Request{
		RunID:    "shutdown-run",
		Messages: []Message{{Role: "user", Content: "initial"}},
		OnDelta:  func(string) {},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run returned result=%+v err=%v, want context.Canceled", result, err)
	}
	if result.Content != "" {
		t.Fatalf("cancelled run carried content %q", result.Content)
	}
}
