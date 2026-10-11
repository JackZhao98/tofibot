package tooloutcome

import (
	"context"
	"strings"
)

type boundaryKey struct{}
type executionIdentityKey struct{}

// ExecutionIdentity is supplied only by runtime after its final recovery check.
// It is separate from model arguments and binds the backend mutation boundary.
func WithExecutionIdentity(ctx context.Context, i Identity) context.Context {
	return context.WithValue(ctx, executionIdentityKey{}, i)
}

func ExecutionIdentity(ctx context.Context) (Identity, bool) {
	i, ok := ctx.Value(executionIdentityKey{}).(Identity)
	return i, ok
}

type recoveryBoundary struct {
	check    func(Identity) *Outcome
	resolved func(Identity)
}

// WithBoundary lets an executor supply its resolved backend identity immediately
// before dispatch. The callback and ledger are backend-owned, never result text.
func WithBoundary(ctx context.Context, check func(Identity) *Outcome, resolved func(Identity)) context.Context {
	return context.WithValue(ctx, boundaryKey{}, recoveryBoundary{check: check, resolved: resolved})
}

func CheckBoundary(ctx context.Context, i Identity) error {
	if i.ResolutionRequired {
		return New(Permanent, "identity_resolution_required", "not_executed", "The backend target has not been resolved; this call was not executed.", "verify_target").Err()
	}
	if b, ok := ctx.Value(boundaryKey{}).(recoveryBoundary); ok {
		if b.resolved != nil {
			b.resolved(i)
		}
		if b.check != nil {
			if o := b.check(i); o != nil {
				return o.Err()
			}
		}
	}
	return nil
}

func RecordIdentity(ctx context.Context, i Identity) {
	if b, ok := ctx.Value(boundaryKey{}).(recoveryBoundary); ok && b.resolved != nil {
		b.resolved(i)
	}
}

func InvalidArguments(message string) error {
	o := New(Validation, "invalid_arguments", "not_executed", message, "repair_arguments")
	o.RepairLimit = 3
	return o.Err()
}

// Rejected reports a precondition refusal by a local tool: the call was
// understood but refused before anything was written. It is a definite
// failure, never an uncertain effect.
func Rejected(message string) error {
	return New(Permanent, "tool_rejected", "not_executed", message+" Nothing was changed.", "repair_arguments").Err()
}

// ApprovalAlreadyClaimed refuses a call before dispatch because the identical
// action already spent its one-shot approval in this run. Nothing was sent by
// the refused call, so its certainty is not_executed: the earlier dispatch's
// certainty stays on the earlier record, and a refusal never fences later
// work as an unverified effect. The identical action stays refused.
func ApprovalAlreadyClaimed(detail string) error {
	return New(Denied, "approval_already_claimed", "not_executed", strings.TrimSpace(detail)+" This call was refused before dispatch; nothing was sent now. Inspect the earlier call's recorded result instead of repeating this exact call.", "verify_effect").Err()
}
