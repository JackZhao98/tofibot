//go:build !linux

package guest

import "net/http"

func (s *Service) healthStatus() (int, map[string]any) {
	return http.StatusOK, map[string]any{"service": "tofi-guest", "vsock_port": VsockPort, "workspace": s.root}
}
