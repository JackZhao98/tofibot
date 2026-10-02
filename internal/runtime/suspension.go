package runtime

import (
	"context"
	"errors"
	"strings"
)

type suspensionKey struct{}

type suspensionControl struct {
	enabled bool
}

// userInputSuspensionError is a control-flow signal, never an ordinary tool
// failure. The agent recognizes it structurally to avoid importing runtime.
type userInputSuspensionError struct {
	questionID string
}

func (e *userInputSuspensionError) Error() string {
	return "human input suspension requested"
}

func (e *userInputSuspensionError) UserInputQuestionID() string {
	return e.questionID
}

var errSuspensionUnavailable = errors.New("human input suspension is unavailable in this runtime")

func withSuspensionControl(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, suspensionKey{}, suspensionControl{enabled: enabled})
}

// CanSuspend reports whether ctx belongs to a bounded runtime Run with a
// durable suspension callback. Legacy callers keep their existing blocking
// question behavior instead of receiving a control-flow error.
func CanSuspend(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	control, ok := ctx.Value(suspensionKey{}).(suspensionControl)
	return ok && control.enabled
}

// SuspendForUserInput asks the active bounded runtime to park at this tool.
// The returned typed error is consumed by the agent loop and therefore must
// be returned directly by a requesting tool, not wrapped as user output.
func SuspendForUserInput(ctx context.Context, questionID string) error {
	if ctx == nil {
		return errSuspensionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !CanSuspend(ctx) {
		return errSuspensionUnavailable
	}
	questionID = strings.TrimSpace(questionID)
	if questionID == "" {
		return errors.New("human input question id is required")
	}
	return &userInputSuspensionError{questionID: questionID}
}
