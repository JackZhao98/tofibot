package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "tofi-test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

// serveDual starts the real dual listener with a handler that reports whether the request came over TLS.
func serveDual(t *testing.T, allowLAN bool) string {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			_, _ = io.WriteString(w, "tls")
		} else {
			_, _ = io.WriteString(w, "plain")
		}
	})}
	listener := newDualListener(inner, selfSignedTLS(t), allowLAN)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return inner.Addr().String()
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, strings.TrimSpace(string(body))
}

func TestDualListenerServesTLSAndLANHTTPOnOnePort(t *testing.T) {
	addr := serveDual(t, true)
	if code, body := get(t, "https://"+addr+"/"); code != 200 || body != "tls" {
		t.Fatalf("https: %d %q", code, body)
	}
	if code, body := get(t, "http://"+addr+"/"); code != 200 || body != "plain" {
		t.Fatalf("http from loopback: %d %q", code, body)
	}
}

func TestDualListenerRefusesPlainHTTPWhenLANHTTPIsOff(t *testing.T) {
	addr := serveDual(t, false)
	if code, body := get(t, "http://"+addr+"/"); code != 400 || !strings.Contains(body, "HTTPS") {
		t.Fatalf("http with LAN HTTP off: %d %q", code, body)
	}
	if code, body := get(t, "https://"+addr+"/"); code != 200 || body != "tls" {
		t.Fatalf("https still served: %d %q", code, body)
	}
}

func TestDualListenerSilentClientDoesNotBlockOthers(t *testing.T) {
	addr := serveDual(t, true)
	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	start := time.Now()
	if code, _ := get(t, "https://"+addr+"/"); code != 200 {
		t.Fatalf("https behind a silent client: %d", code)
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Fatalf("a silent client held the port for %s", waited)
	}
}

func TestLANPeer(t *testing.T) {
	for addr, want := range map[string]bool{"10.0.10.12": true, "192.168.1.5": true, "172.16.0.9": true, "127.0.0.1": true, "::1": true, "fd9b::1": true, "8.8.8.8": false, "2001:4860::8888": false} {
		if got := lanPeer(&net.TCPAddr{IP: net.ParseIP(addr), Port: 1}); got != want {
			t.Errorf("lanPeer(%s) = %v, want %v", addr, got, want)
		}
	}
}
