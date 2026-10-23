package computer

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestGuestBrowserNavigationErrorKeepsItsCode(t *testing.T) {
	o := tooloutcome.ClassifyNavigation("net::ERR_CERT_COMMON_NAME_INVALID", "www.google.com")
	body, _ := json.Marshal(map[string]any{"ok": false, "error": o.Message, "outcome": o})
	client, err := New(Config{Client: &http.Client{Transport: actionReadinessTransport(func(r *http.Request) (*http.Response, error) {
		resp := actionReadinessResponse(string(body))
		resp.StatusCode, resp.Status = http.StatusBadGateway, "502 Bad Gateway"
		return resp, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Action(context.Background(), Action{BotID: "b", Name: "browser.navigate"})
	got, ok := tooloutcome.FromError(err)
	if !ok || got.Code != tooloutcome.BrowserTLSError || got.Host != "www.google.com" || got.NetError != "ERR_CERT_COMMON_NAME_INVALID" || got.NextAction != "try_another_source" {
		t.Fatalf("err=%v, want typed browser_tls_error", err)
	}
}
