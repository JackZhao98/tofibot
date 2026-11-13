package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"
)

// dualListener serves TLS and, for LAN peers only, plain HTTP on the same port (owner 11-12: "allow HTTP on LAN").
// A connection whose first byte is a TLS handshake record (0x16) becomes a *tls.Conn, so net/http sets r.TLS and
// session cookies stay Secure. Any other first byte is plain HTTP: accepted from a private or loopback peer when
// allowLAN is set, otherwise answered "400 HTTPS required" and closed (the same status a TLS-only port gives today).
type dualListener struct {
	net.Listener
	tls      *tls.Config
	allowLAN bool
	ready    chan net.Conn
	failed   chan error
	done     chan struct{}
	once     sync.Once
}

const tlsHandshakeRecord = 0x16

// sniffTimeout bounds the wait for a connection's first byte. Each connection is classified in its own goroutine, so a
// silent client never holds up the others.
const sniffTimeout = 10 * time.Second

func newDualListener(inner net.Listener, config *tls.Config, allowLAN bool) net.Listener {
	served := config.Clone()
	// Serve is used instead of ServeTLS, which would set up HTTP/2: offer HTTP/1.1 only so ALPN never picks h2.
	served.NextProtos = []string{"http/1.1"}
	l := &dualListener{Listener: inner, tls: served, allowLAN: allowLAN, ready: make(chan net.Conn), failed: make(chan error), done: make(chan struct{})}
	go l.acceptLoop()
	return l
}

func (l *dualListener) acceptLoop() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			// net/http's Serve decides: it backs off on temporary errors and stops on the rest. Keep accepting until closed.
			select {
			case l.failed <- err:
			case <-l.done:
				return
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go func() {
			out := l.classify(conn)
			if out == nil {
				return
			}
			select {
			case l.ready <- out:
			case <-l.done:
				_ = out.Close()
			}
		}()
	}
}

func (l *dualListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ready:
		return conn, nil
	case err := <-l.failed:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *dualListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// classify peeks one byte. It returns nil when the connection was refused and closed.
func (l *dualListener) classify(conn net.Conn) net.Conn {
	_ = conn.SetReadDeadline(time.Now().Add(sniffTimeout))
	reader := bufio.NewReader(conn)
	first, err := reader.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return nil
	}
	peeked := &peekedConn{Conn: conn, reader: reader}
	if first[0] == tlsHandshakeRecord {
		return tls.Server(peeked, l.tls)
	}
	if l.allowLAN && lanPeer(conn.RemoteAddr()) {
		return peeked
	}
	_, _ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nThis TOFI server needs HTTPS from outside the local network.\n"))
	_ = conn.Close()
	return nil
}

// lanPeer is true for loopback and private (RFC 1918, RFC 4193) addresses, the same rule as owner sign-in over LAN HTTP.
func lanPeer(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	ip := tcp.IP
	if mapped := ip.To4(); mapped != nil {
		ip = mapped
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

// peekedConn replays the byte read while classifying.
type peekedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
