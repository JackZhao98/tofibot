package guest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func authServer(t *testing.T, file string) *httptest.Server {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("inner")) })
	srv := httptest.NewServer(TokenAuth(next, file))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, auth string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 512)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res.StatusCode, b.String()
}

func TestTokenAuth(t *testing.T) {
	file := filepath.Join(t.TempDir(), "guest-token")
	srv := authServer(t, file)

	if code, body := do(t, srv, "GET", "/health", ""); code != 200 || body != "inner" {
		t.Fatalf("/health must be open without a token file: %d %s", code, body)
	}
	if code, body := do(t, srv, "GET", "/v1/info", "Bearer x"); code != 503 || !strings.Contains(body, `"code":"token_missing"`) || !strings.Contains(body, "guest token not provisioned") {
		t.Fatalf("missing file: %d %s", code, body)
	}
	os.WriteFile(file, []byte(" \n"), 0o600)
	if code, body := do(t, srv, "GET", "/v1/info", "Bearer x"); code != 503 || !strings.Contains(body, "token_missing") {
		t.Fatalf("empty file: %d %s", code, body)
	}
	os.WriteFile(file, []byte("secret-one\n"), 0o600)
	if code, body := do(t, srv, "POST", "/v1/action", "Bearer wrong"); code != 401 || !strings.Contains(body, `"error":"unauthorized"`) {
		t.Fatalf("wrong token: %d %s", code, body)
	}
	if code, _ := do(t, srv, "POST", "/v1/action", ""); code != 401 {
		t.Fatalf("absent header: %d", code)
	}
	if code, _ := do(t, srv, "POST", "/v1/action", "secret-one"); code != 401 {
		t.Fatalf("non-bearer scheme: %d", code)
	}
	if code, body := do(t, srv, "POST", "/v1/action", "Bearer secret-one"); code != 200 || body != "inner" {
		t.Fatalf("right token: %d %s", code, body)
	}
	if code, _ := do(t, srv, "POST", "/health", ""); code != 401 {
		t.Fatalf("only GET /health is open: %d", code)
	}
	// Rewrite is picked up.
	os.WriteFile(file, []byte("secret-two"), 0o600)
	if code, _ := do(t, srv, "GET", "/v1/info", "Bearer secret-one"); code != 401 {
		t.Fatalf("old token after rewrite: %d", code)
	}
	if code, _ := do(t, srv, "GET", "/v1/info", "Bearer secret-two"); code != 200 {
		t.Fatalf("new token after rewrite: %d", code)
	}
}

func TestParseListen(t *testing.T) {
	cases := []struct {
		listen, token string
		ok, tcp       bool
		addr          string
	}{
		{"vsock", "", true, false, ""},
		{"", "", true, false, ""},
		{"vsock", "/t", false, false, ""},
		{"tcp://0.0.0.0:8331", "/t", true, true, "0.0.0.0:8331"},
		{"tcp://0.0.0.0:8331", "", false, false, ""},
		{"tcp://nohostport", "/t", false, false, ""},
		{"http://0.0.0.0:1", "/t", false, false, ""},
	}
	for _, c := range cases {
		spec, err := ParseListen(c.listen, c.token)
		if (err == nil) != c.ok || spec.TCP() != c.tcp || spec.Addr != c.addr {
			t.Errorf("ParseListen(%q,%q) = %+v, %v", c.listen, c.token, spec, err)
		}
	}
}

func TestServeTCPEndToEnd(t *testing.T) {
	svc, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Skip("service unavailable here: ", err)
	}
	file := filepath.Join(t.TempDir(), "tok")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.serveTCPOn(ctx, ln, file) }()
	base := "http://" + ln.Addr().String()
	res, err := http.Get(base + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("want 503 got %d", res.StatusCode)
	}
	os.WriteFile(file, []byte("tok"), 0o600)
	req, _ := http.NewRequest("GET", base+"/v1/info", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("want 200 got %d", res.StatusCode)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}
