//go:build !linux

package guest

import (
	"context"
	"errors"
)

var errLinuxGuestOnly = errors.New("desktop actions are available only in the Linux Firecracker guest")

func (s *Service) startDesktop(context.Context, string) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) desktopCapture(context.Context, string) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) desktopClick(context.Context, string, int, int, int, int, int) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) desktopType(context.Context, string, string) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) desktopKey(context.Context, string, string, []string) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
func stopDesktopProcesses(context.Context, *desktop) error { return errLinuxGuestOnly }
func (s *Service) finishDesktop(_ string, d *desktop, err error) {
	d.startErr = err
	if d.ready != nil && !d.readyClosed {
		close(d.ready)
		d.readyClosed = true
	}
}

func (s *Service) browserNavigate(context.Context, string, string) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) browserSnapshot(context.Context, string) (any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) browserTypePrivate(context.Context, string, privateBrowserInput) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) browserAction(context.Context, string, string, string) (any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) browserControl(context.Context, string, browserCommand) (any, error) {
	return nil, errLinuxGuestOnly
}
func (s *Service) desktopScroll(context.Context, string, string, int) (map[string]any, error) {
	return nil, errLinuxGuestOnly
}
