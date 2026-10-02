//go:build linux

package guest

import "net"

// ListenVSock exposes the existing transport for isolated, non-HTTP fixtures.
// It does not start or alter the remote guest service.
func ListenVSock(port uint32) (net.Listener, error) { return listenVSock(port) }
