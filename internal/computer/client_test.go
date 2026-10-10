package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestClientUsesFixedUnixSocketAndGuestEnvelope(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable in this test environment: %v", err)
	}
	defer listener.Close()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" {
			if r.Method != http.MethodGet {
				t.Errorf("info method=%s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(Info{Kind: "firecracker", State: "ready", WorkspaceRoot: "/workspace", Browser: "Google Chrome"})
			return
		}
		var in Action
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode: %v", err)
		}
		if in.BotID != "bot-1" || in.RunID != "run-1" || in.Name != "desktop.capture" {
			t.Errorf("action=%+v", in)
		}
		_ = json.NewEncoder(w).Encode(ActionResult{OK: true, Result: json.RawMessage(`{"image_url":"data:image/png;base64,AA=="}`)})
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	client, err := New(Config{Socket: socket, Client: NewUnixHTTPClient(socket, time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Info(context.Background())
	if err != nil || info.State != "ready" || info.WorkspaceRoot != "/workspace" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	result, err := client.Action(context.Background(), Action{BotID: "bot-1", RunID: "run-1", Name: "desktop.capture"})
	if err != nil || !result.OK || len(result.Result) == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClientRejectsFailedGuestAction(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable in this test environment: %v", err)
	}
	defer listener.Close()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ActionResult{OK: false, Error: "human control is locked"})
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	client, err := New(Config{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Action(context.Background(), Action{BotID: "b", Name: "desktop.click"})
	if err == nil || err.Error() != "human control is locked" {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("socket unexpectedly missing: %v", err)
	}
}

func TestClientOAuthTransportAndTypedResponses(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tf-oauth-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable in this test environment: %v", err)
	}
	defer listener.Close()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
		}
		switch r.URL.Path {
		case "/v1/oauth/start":
			if len(in) != 0 {
				t.Errorf("start request=%v", in)
			}
			_ = json.NewEncoder(w).Encode(OAuthStartResult{SessionID: "sid", RedirectURI: "http://guest/callback"})
		case "/v1/oauth/arm":
			if in["session_id"] != "sid" || in["state"] != "state" {
				t.Errorf("arm request=%v", in)
			}
			_ = json.NewEncoder(w).Encode(OAuthArmResult{OK: true})
		case "/v1/oauth/poll":
			if in["session_id"] != "sid" {
				t.Errorf("poll request=%v", in)
			}
			_ = json.NewEncoder(w).Encode(OAuthPollResult{Status: "complete", Code: "code", State: "state"})
		case "/v1/oauth/cancel":
			if in["session_id"] != "sid" {
				t.Errorf("cancel request=%v", in)
			}
			_ = json.NewEncoder(w).Encode(OAuthCancelResult{OK: true})
		default:
			http.NotFound(w, r)
		}
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	client, err := New(Config{Socket: socket, Client: NewUnixHTTPClient(socket, time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	start, err := client.OAuthStart(context.Background())
	if err != nil || start.SessionID != "sid" || start.RedirectURI == "" {
		t.Fatalf("start=%+v err=%v", start, err)
	}
	arm, err := client.OAuthArm(context.Background(), start.SessionID, "state")
	if err != nil || !arm.OK {
		t.Fatalf("arm=%+v err=%v", arm, err)
	}
	poll, err := client.OAuthPoll(context.Background(), start.SessionID)
	if err != nil || poll.Status != "complete" || poll.Code != "code" || poll.State != "state" {
		t.Fatalf("poll=%+v err=%v", poll, err)
	}
	cancel, err := client.OAuthCancel(context.Background(), start.SessionID)
	if err != nil || !cancel.OK {
		t.Fatalf("cancel=%+v err=%v", cancel, err)
	}
}

type actionReadinessTransport func(*http.Request) (*http.Response, error)

func (f actionReadinessTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func actionReadinessResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestAdmittedActionWaitsThroughColdManagerAndSendsOnce(t *testing.T) {
	ensures, probes, actions := 0, 0, 0
	client, err := New(Config{Ensure: func(context.Context) error { ensures++; return nil }, Client: &http.Client{Transport: actionReadinessTransport(func(r *http.Request) (*http.Response, error) {
		if ensures != 1 {
			t.Fatalf("request before admission: %d", ensures)
		}
		switch r.URL.Path {
		case "/v1/info":
			probes++
			switch probes {
			case 1:
				return nil, &net.OpError{Op: "dial", Net: "unix", Err: syscall.ECONNREFUSED}
			case 2:
				return actionReadinessResponse(`{"state":"starting"}`), nil
			default:
				return actionReadinessResponse(`{"state":"ready"}`), nil
			}
		case "/v1/action":
			actions++
			if probes != 3 {
				t.Errorf("action sent before readiness: probes=%d", probes)
			}
			return actionReadinessResponse(`{"ok":false,"error":"guest mutation failed"}`), nil
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
			return nil, nil
		}
	})}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.Action(ctx, Action{BotID: "b", Name: "files.write"})
	if err == nil || err.Error() != "guest mutation failed" || ensures != 1 || probes != 3 || actions != 1 {
		t.Fatalf("err=%v ensures=%d probes=%d actions=%d", err, ensures, probes, actions)
	}
}

func TestAdmittedActionReadinessFailureNeverSendsAction(t *testing.T) {
	for _, mode := range []string{"cancel", "guest-error", "admission-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			actions, probes := 0, 0
			client, err := New(Config{Ensure: func(context.Context) error {
				if mode == "admission-error" {
					return errors.New("capacity exhausted")
				}
				return nil
			}, Client: &http.Client{Transport: actionReadinessTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v1/action" {
					actions++
					return actionReadinessResponse(`{"ok":true}`), nil
				}
				probes++
				if mode == "cancel" {
					cancel()
					return actionReadinessResponse(`{"state":"starting"}`), nil
				}
				return actionReadinessResponse(`{"state":"error"}`), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Action(ctx, Action{BotID: "b", Name: "files.write"})
			if err == nil || actions != 0 {
				t.Fatalf("err=%v actions=%d", err, actions)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if mode == "admission-error" && probes != 0 {
				t.Fatalf("probed after rejected admission: %d", probes)
			}
		})
	}
}

func TestReadinessWaitIsLimitedToAdmittedGuestActions(t *testing.T) {
	for _, mode := range []string{"personal-action", "info", "retry"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			cfg := Config{Client: &http.Client{Transport: actionReadinessTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				return actionReadinessResponse(`{"ok":true,"state":"starting"}`), nil
			})}}
			if mode != "personal-action" {
				cfg.Ensure = func(context.Context) error { return nil }
			}
			client, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "personal-action":
				_, err = client.Action(context.Background(), Action{BotID: "b", Name: "files.read"})
			case "info":
				_, err = client.Info(context.Background())
			case "retry":
				err = client.Retry(context.Background())
			}
			if err != nil || requests != 1 {
				t.Fatalf("err=%v requests=%d", err, requests)
			}
		})
	}
}

func TestGuestStructuredRefusalIsDefiniteNotExecuted(t *testing.T) {
	respond := func(status int, body string) (*Client, error) {
		return New(Config{Client: &http.Client{Transport: actionReadinessTransport(func(r *http.Request) (*http.Response, error) {
			resp := actionReadinessResponse(body)
			resp.StatusCode, resp.Status = status, fmt.Sprintf("%d %s", status, http.StatusText(status))
			return resp, nil
		})}})
	}
	refusal := `{"ok":false,"error":"no focused visible browser page","outcome":{"version":1,"status":"validation_error","code":"action_rejected","execution_certainty":"not_executed","message":"no focused visible browser page Nothing was changed.","next_action":"repair_arguments"}}`
	client, _ := respond(http.StatusBadRequest, refusal)
	_, err := client.Action(context.Background(), Action{BotID: "b", Name: "browser.navigate"})
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Certainty != "not_executed" || o.Code != "action_rejected" {
		t.Fatalf("err=%v, want structured not_executed outcome", err)
	}
	// A bare 500 without the structured marker stays an unclassified error.
	client, _ = respond(http.StatusInternalServerError, `{"ok":false,"error":"boom"}`)
	_, err = client.Action(context.Background(), Action{BotID: "b", Name: "browser.navigate"})
	if _, typed := tooloutcome.FromError(err); err == nil || typed {
		t.Fatalf("err=%v, want plain untyped error", err)
	}
}
