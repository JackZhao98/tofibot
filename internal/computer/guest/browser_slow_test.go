package guest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestNoPageMessageListsOpenTabs(t *testing.T) {
	msg := noPageMessage([]tabSummary{{ID: "AB12", Title: "Inbox", URL: "https://mail.test/"}, {ID: "CD34", Title: "Docs", URL: "https://docs.test/"}})
	for _, want := range []string{"target_id=AB12", `title="Inbox"`, "target_id=CD34", "https://docs.test/", "browser.navigate"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q misses %q", msg, want)
		}
	}
	if empty := noPageMessage(nil); !strings.Contains(empty, "browser.navigate") || strings.Contains(empty, "Open tabs") {
		t.Fatalf("empty message = %q", empty)
	}
}

func TestChromeTimeoutIsTransientAndOtherErrorsPass(t *testing.T) {
	if got := chromeTimeout(context.Background(), context.DeadlineExceeded); !errors.As(got, new(*transientError)) {
		t.Fatalf("deadline = %v, want transientError", got)
	}
	plain := errors.New("connection refused")
	if got := chromeTimeout(context.Background(), plain); got != plain {
		t.Fatalf("refused connection changed: %v", got)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := chromeTimeout(cancelled, context.Canceled); errors.As(got, new(*transientError)) {
		t.Fatalf("caller cancel became transient: %v", got)
	}
}

func TestChromeHTTPSlowEndpointIsTransient(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	// Reach the same code path with a short client deadline through chromeTimeout.
	client := &http.Client{Timeout: 50 * time.Millisecond}
	_, err := client.Get(srv.URL)
	if got := chromeTimeout(context.Background(), err); !errors.As(got, new(*transientError)) {
		t.Fatalf("client timeout = %v, want transientError", got)
	}
}

func TestTransientOutcomeIsRetryNotFenced(t *testing.T) {
	o := transientOutcome(&transientError{msg: "busy"})
	if o.Status != tooloutcome.Transient || o.Code != tooloutcome.BrowserSlow || o.NextAction != "retry" {
		t.Fatalf("outcome = %+v", o)
	}
}
