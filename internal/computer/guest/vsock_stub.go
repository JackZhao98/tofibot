//go:build !linux

package guest

import (
	"context"
	"errors"
)

func (s *Service) ListenAndServe(context.Context, uint32) error {
	return errors.New("tofi guest requires Linux AF_VSOCK")
}
