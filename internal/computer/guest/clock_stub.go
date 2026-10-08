//go:build !linux

package guest

import (
	"context"
	"errors"
)

// ClockSyncPort is the vsock port of the root-only wall-clock setter.
const ClockSyncPort = 1053

// ServeClockSync is only available inside the Linux guest.
func ServeClockSync(context.Context, uint32) error {
	return errors.New("tofi guest clock sync requires Linux AF_VSOCK")
}
