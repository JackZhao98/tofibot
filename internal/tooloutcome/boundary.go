package tooloutcome

import "context"

type boundaryKey struct{}
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
