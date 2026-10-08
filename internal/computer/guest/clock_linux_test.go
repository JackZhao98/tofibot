//go:build linux

package guest

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type fakeVsockConn struct {
	net.Conn
	remote net.Addr
}

func (c fakeVsockConn) RemoteAddr() net.Addr { return c.remote }

func clockExchange(t *testing.T, cid uint32, request string, set func(time.Time) error) string {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleClockSync(fakeVsockConn{Conn: server, remote: &vsockAddr{cid: cid, port: 1}}, set)
		close(done)
	}()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	go func() { _, _ = client.Write([]byte(request)) }()
	line, _ := bufio.NewReader(client).ReadString('\n')
	client.Close()
	<-done
	return strings.TrimSpace(line)
}

func TestClockSyncSetsHostTimeOnlyFromHost(t *testing.T) {
	want := time.Date(2026, 10, 8, 23, 40, 0, 123, time.UTC)
	var got time.Time
	set := func(v time.Time) error { got = v; return nil }
	if reply := clockExchange(t, unix.VMADDR_CID_HOST, "1791502800000000123\n", set); reply != "OK" || !got.Equal(want) {
		t.Fatalf("reply %q, set %v", reply, got)
	}
	got = time.Time{}
	// A guest-local process (loopback CID) can never change the clock.
	if reply := clockExchange(t, 1, "1791502800000000123\n", set); reply != "ERR host only" || !got.IsZero() {
		t.Fatalf("non-host reply %q, set %v", reply, got)
	}
	for _, bad := range []string{"abc\n", "1\n", "99999999999999999999\n", "\n"} {
		if reply := clockExchange(t, unix.VMADDR_CID_HOST, bad, set); !strings.HasPrefix(reply, "ERR") || !got.IsZero() {
			t.Fatalf("%q accepted: %q", bad, reply)
		}
	}
}
