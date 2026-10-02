package app

import (
	"github.com/JackZhao98/tofibot/internal/computer"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type desktopStreamTransport struct {
	status int
	calls  int
}

func (t *desktopStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: t.status, Header: http.Header{"Content-Type": []string{"video/mp4"}}, Body: io.NopCloser(strings.NewReader("video"))}, nil
}
func TestDesktopVideoRouteScopeStatusAndOwnership(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("Viewer acceptance", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	transport := &desktopStreamTransport{status: 200}
	s.microVM, _ = computer.New(computer.Config{Client: &http.Client{Transport: transport}})
	route := "bots/" + b.ID + "/computer/stream"
	for _, test := range []struct {
		method, origin, site, path string
		want                       int
	}{
		{"POST", "", "", route, 405}, {"GET", "http://evil.example", "", route, 403}, {"GET", "", "cross-site", route, 403},
		{"GET", "", "", "bots/00000000-0000-0000-0000-000000000000/computer/stream", 404},
	} {
		req := httptest.NewRequest(test.method, "http://localhost/api/"+test.path, nil)
		req.Header.Set("Origin", test.origin)
		req.Header.Set("Sec-Fetch-Site", test.site)
		w := httptest.NewRecorder()
		if !s.routeDesktopStream(w, req, test.path) || w.Code != test.want {
			t.Fatalf("route status=%d want %d", w.Code, test.want)
		}
	}
	if transport.calls != 0 {
		t.Fatal("invalid requests reached VM")
	}
	s.claimComputerOwner(b.ID, "model-run")
	for _, status := range []int{200, 409, 410, 503} {
		transport.status = status
		req := httptest.NewRequest("GET", "http://localhost/api/"+route, nil)
		w := httptest.NewRecorder()
		s.routeDesktopStream(w, req, route)
		if w.Code != status {
			t.Fatalf("upstream %d got %d", status, w.Code)
		}
		if s.computerOwners[b.ID] != "model-run" {
			t.Fatal("stream changed model owner")
		}
	}
}
