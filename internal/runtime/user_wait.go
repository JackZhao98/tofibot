package runtime

import (
	"context"
	"sync"
	"time"
)

type userWaitKey struct{}

// userWaitBudget belongs to one sequential ToolsOnly run. Only backend-owned
// user-input waiters may pause it; tool names or model arguments cannot do so.
// It changes budget accounting, never context deadlines or cancellation.
type userWaitBudget struct {
	mu      sync.Mutex
	now     func() time.Time
	depth   int
	started time.Time
	total   time.Duration
}

func withUserWaitBudget(ctx context.Context) (context.Context, *userWaitBudget) {
	b := &userWaitBudget{now: time.Now}
	return context.WithValue(ctx, userWaitKey{}, b), b
}

// PauseForUserInput excludes a bounded human-input wait from active execution
// time. Always defer the returned resume function. This is a no-op outside a
// runtime run and does not extend question expiry or any caller deadline.
func PauseForUserInput(ctx context.Context) func() {
	if ctx == nil {
		return func() {}
	}
	// A human wait never counts against the current tool call's deadline.
	resumeDeadline := PauseToolDeadline(ctx)
	b, ok := ctx.Value(userWaitKey{}).(*userWaitBudget)
	if !ok {
		return resumeDeadline
	}
	b.mu.Lock()
	if b.depth == 0 {
		b.started = b.now()
	}
	b.depth++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			resumeDeadline()
			b.mu.Lock()
			defer b.mu.Unlock()
			b.depth--
			if b.depth == 0 {
				b.total += b.now().Sub(b.started)
			}
		})
	}
}

func (b *userWaitBudget) duration() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.total
	if b.depth > 0 {
		d += b.now().Sub(b.started)
	}
	return d
}
