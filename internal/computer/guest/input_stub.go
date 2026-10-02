//go:build !linux

package guest

import "context"

type inputClipboard struct{}

func awaitInputCopy(context.Context, *inputSession) (bool, error) { return false, errLinuxGuestOnly }

func inputDependencies() error { return errLinuxGuestOnly }
func executeInputEvents(context.Context, *inputSession, []DesktopInputEvent) error {
	return errLinuxGuestOnly
}
func resetInputState(context.Context, *inputSession) error              { return errLinuxGuestOnly }
func readInputClipboard(context.Context, *inputSession) (string, error) { return "", errLinuxGuestOnly }
func writeInputClipboard(context.Context, *inputSession, string) error  { return errLinuxGuestOnly }
func closeInputClipboard(*inputSession)                                 {}
