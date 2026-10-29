package mcprunner

import (
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

func openFDs() int {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}

func TestDNSFloodStaysWithinTheInFlightCap(t *testing.T) {
	// An upstream that reads queries and never answers keeps every worker busy.
	upstream, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			if _, _, err := upstream.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	front, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limit := newDNSLimiter(maxDNSInFlight)
	done := make(chan struct{})
	go func() { serveDNSUDP(front, upstream.LocalAddr().String(), limit); close(done) }()

	baseG, baseFD := runtime.NumGoroutine(), openFDs()
	client, err := net.Dial("udp", front.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i := 0; i < 5000; i++ {
		_, _ = client.Write([]byte("query"))
	}
	time.Sleep(500 * time.Millisecond)
	if peak := limit.peak.Load(); peak > maxDNSInFlight {
		t.Fatalf("peak in flight %d, cap %d", peak, maxDNSInFlight)
	}
	if g := runtime.NumGoroutine() - baseG; g > maxDNSInFlight+8 {
		t.Fatalf("goroutines grew by %d", g)
	}
	if fd := openFDs() - baseFD; fd > maxDNSInFlight+8 {
		t.Fatalf("descriptors grew by %d", fd)
	}
	front.Close()
	<-done
}
