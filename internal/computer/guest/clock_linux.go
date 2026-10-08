//go:build linux

package guest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ClockSyncPort is the vsock port of the root-only wall-clock setter.
const ClockSyncPort = 1053

// A restored snapshot resumes with the wall clock of the moment it was taken
// (KVM_CLOCK_REALTIME is not available on every host, e.g. nested KVM). The
// manager sends the host's current time once after every restore. Only the
// host (CID 2) may connect; the request is one decimal Unix-nanosecond line.
var clockSyncFloor = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// ServeClockSync runs the setter until ctx ends. It must run as root, apart
// from the unprivileged guest agent, and does nothing else.
func ServeClockSync(ctx context.Context, port uint32) error {
	ln, err := listenVSock(port)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		handleClockSync(conn, setWallClock)
	}
}

func setWallClock(t time.Time) error {
	ts := unix.NsecToTimespec(t.UnixNano())
	return unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
}

func handleClockSync(conn net.Conn, set func(time.Time) error) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reply := func(message string) { _, _ = conn.Write([]byte(message + "\n")) }
	if addr, ok := conn.RemoteAddr().(*vsockAddr); !ok || addr.cid != unix.VMADDR_CID_HOST {
		reply("ERR host only")
		return
	}
	value, err := parseClockSync(bufio.NewReader(conn))
	if err != nil {
		reply("ERR " + err.Error())
		return
	}
	if err := set(value); err != nil {
		reply("ERR set clock")
		return
	}
	reply("OK")
}

func parseClockSync(r *bufio.Reader) (time.Time, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return time.Time{}, errors.New("incomplete request")
	}
	line = strings.TrimSpace(line)
	if len(line) == 0 || len(line) > 20 {
		return time.Time{}, errors.New("invalid time")
	}
	ns, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return time.Time{}, errors.New("invalid time")
	}
	value := time.Unix(0, ns).UTC()
	if value.Before(clockSyncFloor) || value.Year() > 2200 {
		return time.Time{}, fmt.Errorf("time out of range")
	}
	return value, nil
}
