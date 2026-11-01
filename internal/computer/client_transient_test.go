package computer

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestGuestBrowserSlowOutcomeStaysTransient(t *testing.T) {
	body := `{"ok":false,"error":"slow","outcome":{"version":1,"status":"transient_failure","code":"browser_slow","execution_certainty":"unknown","message":"The browser did not answer in time.","next_action":"retry"}}`
	client, err := New(Config{Client: &http.Client{Transport: actionReadinessTransport(func(r *http.Request) (*http.Response, error) {
		resp := actionReadinessResponse(body)
		resp.StatusCode, resp.Status = http.StatusServiceUnavailable, "503 Service Unavailable"
		return resp, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Action(context.Background(), Action{BotID: "b", Name: "browser.snapshot"})
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Status != tooloutcome.Transient || o.NextAction != "retry" || o.Code != "browser_slow" {
		t.Fatalf("err=%v, want transient retry outcome", err)
	}
}

func TestControlSocketEOFOnActionIsTransient(t *testing.T) {
	client, err := New(Config{Client: &http.Client{Transport: actionReadinessTransport(func(r *http.Request) (*http.Response, error) {
		return nil, io.EOF
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"shell.exec": "verify_effect", "files.write": "verify_effect", "files.read": "retry", "browser.snapshot": "retry"} {
		_, err = client.Action(context.Background(), Action{BotID: "b", Name: name})
		o, ok := tooloutcome.FromError(err)
		if !ok || o.Status != tooloutcome.Transient || o.Code != tooloutcome.ConnectionLost || o.NextAction != want {
			t.Fatalf("%s: err=%v, want transient computer_connection_lost with %s", name, err, want)
		}
	}
}
