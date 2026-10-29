package mcprunner

import (
	"net"
	"sync/atomic"
	"time"
)

// maxDNSInFlight caps the queries one account's forwarder works on at once. A
// plugin that floods port 53 loses the excess queries (a resolver retries) and
// cannot grow the Runner's goroutines or sockets past this.
const maxDNSInFlight = 64

// dnsLimiter counts the forwarder's work in flight.
type dnsLimiter struct {
	slots chan struct{}
	peak  atomic.Int64
	now   atomic.Int64
}

func newDNSLimiter(n int) *dnsLimiter { return &dnsLimiter{slots: make(chan struct{}, n)} }

// acquire takes a slot without waiting; false means drop the query.
func (l *dnsLimiter) acquire() bool {
	select {
	case l.slots <- struct{}{}:
		n := l.now.Add(1)
		for {
			p := l.peak.Load()
			if n <= p || l.peak.CompareAndSwap(p, n) {
				break
			}
		}
		return true
	default:
		return false
	}
}

func (l *dnsLimiter) release() {
	l.now.Add(-1)
	<-l.slots
}

// serveDNSUDP forwards each datagram read from udp to upstream and writes the
// answer back, until udp is closed. At most limit slots work at a time; each
// query uses one upstream socket for at most 5 seconds.
func serveDNSUDP(udp net.PacketConn, upstream string, limit *dnsLimiter) {
	buf := make([]byte, 4096)
	for {
		size, from, err := udp.ReadFrom(buf)
		if err != nil {
			return
		}
		if !limit.acquire() {
			continue
		}
		query := append([]byte(nil), buf[:size]...)
		go func() {
			defer limit.release()
			conn, err := net.DialTimeout("udp", upstream, 3*time.Second)
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Write(query); err != nil {
				return
			}
			answer := make([]byte, 4096)
			if got, err := conn.Read(answer); err == nil {
				_, _ = udp.WriteTo(answer[:got], from)
			}
		}()
	}
}
