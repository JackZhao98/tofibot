//go:build linux

package guest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

func (s *Service) ListenAndServe(ctx context.Context, port uint32) error {
	if port == 0 {
		port = VsockPort
	}
	ln, err := listenVSock(port)
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = s.Close(context.Background())
	}()
	if err := http.Serve(ln, s.Handler()); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}

func listenVSock(port uint32) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("create AF_VSOCK socket: %w", err)
	}
	addr := &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("bind AF_VSOCK port %d: %w", port, err)
	}
	if err := unix.Listen(fd, 64); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("listen AF_VSOCK port %d: %w", port, err)
	}
	return &vsockListener{fd: fd, port: port, closed: make(chan struct{})}, nil
}

type vsockListener struct {
	fd     int
	port   uint32
	closed chan struct{}
	once   sync.Once
}

func (l *vsockListener) Accept() (net.Conn, error) {
	for {
		// Keep accepted sockets nonblocking so os.NewFile adopts them into the
		// runtime poller.  This is what makes SetDeadline and cancellation work
		// for net/http; a blocking *os.File can leave finishRequest waiting on a
		// read held by a disconnected caller.
		fd, raw, err := unix.Accept4(l.fd, unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
		if err == nil {
			file := os.NewFile(uintptr(fd), "tofi-guest-vsock-conn")
			if file == nil {
				_ = unix.Close(fd)
				return nil, fmt.Errorf("create AF_VSOCK connection file")
			}
			return &vsockConn{file: file, local: &vsockAddr{cid: unix.VMADDR_CID_ANY, port: l.port}, remote: sockaddrAddr(raw)}, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) {
			select {
			case <-l.closed:
				return nil, net.ErrClosed
			default:
			}
			return nil, err
		}
		select {
		case <-l.closed:
			return nil, net.ErrClosed
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (l *vsockListener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.closed)
		err = unix.Close(l.fd)
	})
	return err
}
func (l *vsockListener) Addr() net.Addr { return &vsockAddr{cid: unix.VMADDR_CID_ANY, port: l.port} }

type vsockAddr struct {
	cid  uint32
	port uint32
}

func (a *vsockAddr) Network() string { return "vsock" }
func (a *vsockAddr) String() string  { return fmt.Sprintf("%d:%d", a.cid, a.port) }

func sockaddrAddr(raw unix.Sockaddr) net.Addr {
	if a, ok := raw.(*unix.SockaddrVM); ok {
		return &vsockAddr{cid: a.CID, port: a.Port}
	}
	return &vsockAddr{}
}

type vsockConn struct {
	file   *os.File
	local  net.Addr
	remote net.Addr
	once   sync.Once
}

func (c *vsockConn) Read(p []byte) (int, error)  { return c.file.Read(p) }
func (c *vsockConn) Write(p []byte) (int, error) { return c.file.Write(p) }
func (c *vsockConn) Close() error {
	var err error
	c.once.Do(func() { err = c.file.Close() })
	return err
}
func (c *vsockConn) LocalAddr() net.Addr                { return c.local }
func (c *vsockConn) RemoteAddr() net.Addr               { return c.remote }
func (c *vsockConn) SetDeadline(t time.Time) error      { return c.file.SetDeadline(t) }
func (c *vsockConn) SetReadDeadline(t time.Time) error  { return c.file.SetReadDeadline(t) }
func (c *vsockConn) SetWriteDeadline(t time.Time) error { return c.file.SetWriteDeadline(t) }
