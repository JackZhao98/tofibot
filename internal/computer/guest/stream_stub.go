//go:build !linux

package guest

import (
	"context"
	"net/http"
)

func (s *Service) encodeDesktopStream(ctx context.Context, w http.ResponseWriter, d *desktop, hideCursor bool) {
	writeError(w, http.StatusServiceUnavailable, "desktop_stream_unavailable")
}
