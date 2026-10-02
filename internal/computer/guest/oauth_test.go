package guest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func oauthPost(t *testing.T, h http.Handler, path string, body any, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code >= 500 {
		t.Logf("%s response: %s", path, w.Body.String())
	}
	if out != nil {
		_ = json.Unmarshal(w.Body.Bytes(), out)
	}
	return w.Code
}

func TestOAuthLoopbackSuccessAndBadState(t *testing.T) {
	s := newTestService(t)
	h := s.Handler()
	var start oauthStartResponse
	if got := oauthPost(t, h, "/v1/oauth/start", map[string]any{}, &start); got != http.StatusOK {
		t.Fatalf("start=%d", got)
	}
	if got := oauthPost(t, h, "/v1/oauth/arm", oauthArmRequest{SessionID: start.SessionID, State: "state-1"}, nil); got != http.StatusOK {
		t.Fatalf("arm=%d", got)
	}
	u, _ := url.Parse(start.RedirectURI)
	badResp, badErr := (&http.Client{}).Get("http://" + u.Host + u.Path + "?code=secret&state=wrong")
	if badErr != nil || badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad state response=%v err=%v", badResp, badErr)
	} else {
		badResp.Body.Close()
	}
	resp, err := (&http.Client{}).Get(start.RedirectURI + "?code=secret&state=state-1")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("callback err=%v status=%v", err, resp)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil || !strings.Contains(string(body), "Authorization complete") || strings.Contains(string(body), "secret") {
		t.Fatalf("callback body=%q err=%v", body, readErr)
	}
	var poll oauthPollResponse
	if got := oauthPost(t, h, "/v1/oauth/poll", oauthPollRequest{SessionID: start.SessionID}, &poll); got != http.StatusOK || poll.Status != "complete" || poll.Code != "secret" || poll.State != "state-1" {
		t.Fatalf("poll=%d %#v", got, poll)
	}
	resp, err = (&http.Client{}).Get(start.RedirectURI + "?code=second&state=state-1")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("duplicate callback accepted")
		}
	}
	var second oauthPollResponse
	oauthPost(t, h, "/v1/oauth/poll", oauthPollRequest{SessionID: start.SessionID}, &second)
	if second.Code != "" || second.State != "" {
		t.Fatal("authorization code delivered twice")
	}
}

func TestOAuthCancelTimeoutAndCleanup(t *testing.T) {
	s := newTestService(t)
	s.oauthTTL = 20 * time.Millisecond
	h := s.Handler()
	var start oauthStartResponse
	if got := oauthPost(t, h, "/v1/oauth/start", nil, &start); got != http.StatusOK {
		t.Fatal("start failed")
	}
	var cancelled struct {
		OK bool `json:"ok"`
	}
	if oauthPost(t, h, "/v1/oauth/cancel", oauthCancelRequest{SessionID: start.SessionID}, &cancelled) != http.StatusOK || !cancelled.OK {
		t.Fatalf("cancel=%#v", cancelled)
	}
	var again struct {
		OK bool `json:"ok"`
	}
	if oauthPost(t, h, "/v1/oauth/cancel", oauthCancelRequest{SessionID: start.SessionID}, &again) != http.StatusOK || !again.OK {
		t.Fatalf("idempotent cancel=%#v", again)
	}
	var next oauthStartResponse
	if oauthPost(t, h, "/v1/oauth/start", nil, &next) != http.StatusOK {
		t.Fatal("new start after cancel failed")
	}
	time.Sleep(40 * time.Millisecond)
	var expired oauthPollResponse
	if oauthPost(t, h, "/v1/oauth/poll", oauthPollRequest{SessionID: next.SessionID}, &expired) != http.StatusOK || expired.Status != "expired" {
		t.Fatalf("expired=%#v", expired)
	}
	_ = s.Close(nil)
	if oauthPost(t, h, "/v1/oauth/start", nil, nil) != http.StatusServiceUnavailable {
		t.Fatal("new listener started after close")
	}
}
