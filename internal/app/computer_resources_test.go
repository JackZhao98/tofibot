package app

import (
	"context"
	"github.com/JackZhao98/tofibot/internal/computer"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type resourceTransport func(*http.Request) (*http.Response, error)

func (f resourceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestComputerResourceRouteStoresDesiredWithoutLifecycleAction(t *testing.T) {
	calls := 0
	client, err := computer.New(computer.Config{Socket: "/tmp/acceptance-resource.sock", Client: &http.Client{Transport: resourceTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/resources" || r.Method != http.MethodPost {
			t.Fatalf("unexpected lifecycle request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"vcpus":2,"memory_mib":6144,"disk_gib":24}` {
			t.Fatalf("unexpected desired allocation: %s", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"scope":"workspace","pending":true,"apply_policy":"next_vm_start","current":{"vcpus":2,"memory_mib":4096,"disk_gib":8},"desired":{"vcpus":2,"memory_mib":6144,"disk_gib":24}}`)), Header: make(http.Header)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/computer/resources", strings.NewReader(`{"vcpus":2,"memory_mib":6144,"disk_gib":24}`)).WithContext(context.Background())
	if !s.routeComputerResources(w, r, "computer/resources") {
		t.Fatal("route not handled")
	}
	if w.Code != 200 || calls != 1 || !strings.Contains(w.Body.String(), `"pending":true`) {
		t.Fatalf("unexpected response %d %s calls=%d", w.Code, w.Body.String(), calls)
	}
}

func TestComputerResourceRouteRejectsInvalidAllocation(t *testing.T) {
	client, _ := computer.New(computer.Config{Socket: "/tmp/acceptance-resource.sock", Client: &http.Client{Transport: resourceTransport(func(*http.Request) (*http.Response, error) { t.Fatal("invalid allocation reached VM"); return nil, nil })}})
	s := &Server{microVM: client}
	for _, body := range []string{`{"vcpus":0,"memory_mib":4096,"disk_gib":8}`, `{"vcpus":2,"memory_mib":4096,"disk_gib":4}`, `{"vcpus":2,"memory_mib":4096,"disk_gib":8,"restart":true}`} {
		w := httptest.NewRecorder()
		s.routeComputerResources(w, httptest.NewRequest(http.MethodPut, "/api/computer/resources", strings.NewReader(body)), "computer/resources")
		if w.Code != 400 {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	}
}

func TestApplyResourcesRequiresConfirmationAndIdleRuns(t *testing.T) {
	s := computerTestServer(t)
	calls := 0
	client, _ := computer.New(computer.Config{Socket: "/tmp/test.sock", Client: &http.Client{Transport: resourceTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/resources/apply" || string(body) != `{"confirm":true}` {
			t.Fatalf("unexpected restart %s %s", r.URL.Path, body)
		}
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(`{"state":"restarting"}`)), Header: make(http.Header)}, nil
	})}})
	s.microVM = client
	call := func(body string) int {
		w := httptest.NewRecorder()
		s.routeComputerResources(w, httptest.NewRequest(http.MethodPost, "/api/computer/resources/apply", strings.NewReader(body)), "computer/resources/apply")
		return w.Code
	}
	for _, body := range []string{`{}`, `{"confirm":false}`, `{"confirm":true,"force":true}`} {
		if code := call(body); code != 400 {
			t.Fatalf("unconfirmed request %s: %d", body, code)
		}
	}
	if calls != 0 {
		t.Fatal("unconfirmed request restarted VM")
	}
	if code := call(`{"confirm":true}`); code != 202 || calls != 1 {
		t.Fatalf("idle confirmed restart: %d calls %d", code, calls)
	}
	activeComputerRun(t, s)
	if code := call(`{"confirm":true}`); code != 409 || calls != 1 {
		t.Fatalf("active task interrupted: %d calls %d", code, calls)
	}
}
