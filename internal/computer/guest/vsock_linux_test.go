//go:build linux

package guest

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// AF_VSOCK is only available inside the guest, so use a nonblocking Unix
// socketpair to exercise the exact os.File/net.Conn adapter used after
// Accept4. This catches regressions where accepted descriptors stop being
// visible to Go's runtime poller and HTTP request deadlines hang forever.
func TestVsockConnDeadlineUsesRuntimePoller(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	left := os.NewFile(uintptr(fds[0]), "vsock-test-left")
	right := os.NewFile(uintptr(fds[1]), "vsock-test-right")
	if left == nil || right == nil {
		t.Fatal("failed to wrap socketpair")
	}
	defer left.Close()
	defer right.Close()

	conn := &vsockConn{file: left, local: &vsockAddr{}, remote: &vsockAddr{}}
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := conn.Read(buf)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("read unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read did not honor deadline")
	}
}
