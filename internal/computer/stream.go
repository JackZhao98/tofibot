package computer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var streamBotID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// DesktopStream returns a caller-owned body. Closing it or canceling ctx tears
// down both the manager connection and guest encoder. No per-action timeout is
// applied; the guest owns a bounded stream lifetime and desktop idle cleanup.
func (c *Client) DesktopStream(ctx context.Context, botID string) (*http.Response, error) {
	return c.DesktopStreamWithCursor(ctx, botID, "")
}

// DesktopStreamWithCursor opens the observer stream with an optional cursor
// policy. "hidden" asks the guest encoder to burn no remote pointer into the
// frames while a local pointer is rendered by the takeover UI.
func (c *Client) DesktopStreamWithCursor(ctx context.Context, botID, cursor string) (*http.Response, error) {
	if !streamBotID.MatchString(botID) {
		return nil, fmt.Errorf("invalid bot_id")
	}
	if cursor != "" && cursor != "hidden" {
		return nil, fmt.Errorf("invalid cursor")
	}
	endpoint := "http://tofi-computer/v1/desktop/stream?bot_id=" + url.QueryEscape(botID)
	if cursor != "" {
		endpoint += "&cursor=" + url.QueryEscape(cursor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	streamClient := *c.http
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK && resp.Header.Get("Content-Type") != "video/mp4" {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("invalid desktop stream content type")
	}
	return resp, nil
}

// CopyDesktopStream uses fixed-size backpressure and bounded writes, never an
// accumulating frame queue. Cancellation closes the upstream response body.
func CopyDesktopStream(w http.ResponseWriter, body io.Reader) error {
	control := http.NewResponseController(w)
	defer control.SetWriteDeadline(time.Time{})
	buffer := make([]byte, 32*1024)
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			_ = control.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			if flushErr := control.Flush(); flushErr != nil {
				return flushErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
