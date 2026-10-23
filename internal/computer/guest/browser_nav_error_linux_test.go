//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestNavigateNetErrorIsCodedWithHost(t *testing.T) {
	f := newNopageChrome(t)
	f.pages = []*fakePage{{id: "p1", url: "about:blank", focused: true}}
	f.navError = "net::ERR_CERT_COMMON_NAME_INVALID"
	s := &Service{}
	_, err := s.browserNavigatePage(context.Background(), nopageBot, &desktop{remotePort: f.port}, "https://www.google.com/search?q=x", "")
	var nav *navigationError
	if !errors.As(err, &nav) {
		t.Fatalf("err = %v, want navigationError", err)
	}
	o := nav.outcome
	if o.Code != tooloutcome.BrowserTLSError || o.Host != "www.google.com" || o.NetError != "ERR_CERT_COMMON_NAME_INVALID" || o.NextAction != "try_another_source" {
		t.Fatalf("outcome = %+v", o)
	}
	w := httptest.NewRecorder()
	writeNavigation(w, nav)
	var resp ActionResponse
	if w.Code != http.StatusBadGateway || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.Outcome == nil || !tooloutcome.IsBrowserNavigation(*resp.Outcome) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
