package guest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// browserCommand is shared by the Linux implementation and the non-Linux
// stub so the action dispatcher can expose the same target contract.
type browserCommand struct {
	Action   string
	URL      string
	TargetID string
}

// transientError is a guest failure that is neither a refusal nor a verdict:
// the browser did not answer. It travels as a Transient outcome with next
// action retry, so the host neither fences the call nor counts it as a
// repair.
type transientError struct{ msg string }

func (e *transientError) Error() string { return e.msg }

func browserSlowf(format string, args ...any) error {
	return &transientError{msg: fmt.Sprintf(format, args...)}
}

func transientOutcome(e *transientError) tooloutcome.Outcome {
	return tooloutcome.New(tooloutcome.Transient, tooloutcome.BrowserSlow, "unknown", e.msg, "retry")
}

// chromeTimeout turns a Chrome DevTools HTTP timeout into a transient error.
// A caller cancel and every other failure pass through unchanged (a refused
// connection means Chrome is gone, which the host restarts on).
func chromeTimeout(ctx context.Context, err error) error {
	if err == nil || ctx.Err() != nil {
		return err
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return browserSlowf("The browser did not answer in time; it is busy or short of memory. Wait a few seconds and repeat the call. If it keeps happening, call desktop.stop, then desktop.start.")
	}
	return err
}

type tabSummary struct{ ID, Title, URL string }

const noPageTabLimit = 12

// noPageMessage is the refusal for a browser call that names no tab while no
// page is both focused and visible. It lists the open tabs so the next call
// can name one.
func noPageMessage(tabs []tabSummary) string {
	var b strings.Builder
	b.WriteString("No focused visible browser page.")
	if len(tabs) == 0 {
		b.WriteString(" No tab is open; use browser.navigate with a url to open one.")
		return b.String()
	}
	b.WriteString(" Open tabs:")
	for i, tab := range tabs {
		if i == noPageTabLimit {
			fmt.Fprintf(&b, " (+%d more)", len(tabs)-noPageTabLimit)
			break
		}
		fmt.Fprintf(&b, " [target_id=%s title=%q url=%s]", tab.ID, clipRunes(tab.Title, 80), clipRunes(tab.URL, 160))
	}
	b.WriteString(" Pass one target_id, or use browser.navigate with a url.")
	return b.String()
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
