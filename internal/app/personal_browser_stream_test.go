package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func browserJPEG(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if e := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 10)), nil); e != nil {
		t.Fatal(e)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func browserTestFrame(t *testing.T, h *browserHub) []byte {
	t.Helper()
	h.mu.Lock()
	v := browserFrame{Type: "frame", V: 1, Connection: h.upload.epoch, Observation: h.observation, Session: *h.state.Session, Target: *h.state.Target, Viewport: h.state.Viewport, Seq: h.frameSeq + 1, Captured: time.Now().UnixMilli(), Width: 16, Height: 10, JPEG: browserJPEG(t)}
	h.mu.Unlock()
	raw, _ := json.Marshal(v)
	return raw
}
func TestPersonalBrowserFrameValidationAndPrivacy(t *testing.T) {
	s, _, h, _ := browserFixture(t)
	h.mu.Lock()
	viewer := make(chan struct{}, 1)
	h.viewers[viewer] = struct{}{}
	h.observe(false)
	h.mu.Unlock()
	valid := browserTestFrame(t, h)
	if e := s.browserMessage(h, h.upload, valid); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	if h.frame == nil {
		t.Fatal("valid frame lost")
	}
	h.mu.Unlock()
	var frame map[string]any
	_ = json.Unmarshal(valid, &frame)
	for _, bad := range []struct {
		key   string
		value any
	}{{"jpeg", "fake"}, {"width", 42}, {"extra", true}, {"received_at_ms", 1}, {"jpeg", strings.Repeat("a", 353<<10)}} {
		v := map[string]any{}
		for k, x := range frame {
			v[k] = x
		}
		v[bad.key] = bad.value
		v["seq"] = 2
		raw, _ := json.Marshal(v)
		if e := s.browserMessage(h, h.upload, raw); e == nil {
			t.Fatalf("accepted invalid %s", bad.key)
		}
	}
	frame["observation_id"] = uuid.NewString()
	frame["seq"] = 2
	raw, _ := json.Marshal(frame)
	if e := s.browserMessage(h, h.upload, raw); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	if h.frame.Seq != 1 {
		t.Fatal("old observation accepted")
	}
	state := *h.state
	h.mu.Unlock()
	state.Seq++
	state.Phase = "human"
	state.Preview = "human"
	raw, _ = json.Marshal(state)
	if e := s.browserMessage(h, h.upload, raw); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	if h.frame != nil || h.observation != "" {
		t.Fatal("human retained preview")
	}
	h.mu.Unlock()
	state.Seq++
	state.Phase = "idle"
	state.Preview = "ready"
	state.Settings.Share = false
	raw, _ = json.Marshal(state)
	if e := s.browserMessage(h, h.upload, raw); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	if h.frame != nil || h.observation != "" {
		t.Fatal("share=false retained preview")
	}
	h.mu.Unlock()
	var jobs int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM computer_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("preview entered action queue")
	}
}
func TestPersonalBrowserObserveSharedAndStop(t *testing.T) {
	_, _, h, _ := browserFixture(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := 0; i < 8; i++ {
		h.viewers[make(chan struct{}, 1)] = struct{}{}
		h.observe(false)
	}
	if h.observation == "" || len(h.upload.out) != 1 {
		t.Fatal("viewers did not share a producer")
	}
	for ch := range h.viewers {
		delete(h.viewers, ch)
	}
	h.observe(false)
	if h.frame != nil || h.observation != "" || len(h.upload.out) != 2 {
		t.Fatal("last viewer did not immediately stop")
	}
	first := (<-h.upload.out).(map[string]any)
	last := (<-h.upload.out).(map[string]any)
	if first["observation_id"] != last["observation_id"] || last["enabled"] != false || first["lease_ms"] != 5000 {
		t.Fatalf("observe lease %v %v", first, last)
	}
}
func TestPersonalBrowserStrictState(t *testing.T) {
	s, _, h, _ := browserFixture(t)
	h.mu.Lock()
	state := *h.state
	state.Seq++
	h.mu.Unlock()
	raw, _ := json.Marshal(state)
	if e := s.browserMessage(h, h.upload, raw); e != nil {
		t.Fatal(e)
	}
	if e := s.browserMessage(h, h.upload, raw); e == nil {
		t.Fatal("reordered state accepted")
	}
	for _, bad := range []string{"unknown", "v", "settings"} {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		m["state_seq"] = 3
		if bad == "unknown" {
			m[bad] = true
		} else {
			delete(m, bad)
		}
		v, _ := json.Marshal(m)
		if e := s.browserMessage(h, h.upload, v); e == nil {
			t.Fatalf("accepted invalid %s", bad)
		}
	}
}
func TestPersonalBrowserNinthViewerBusy(t *testing.T) {
	_, m, h, _ := browserFixture(t)
	h.mu.Lock()
	for i := 0; i < 8; i++ {
		h.viewers[make(chan struct{}, 1)] = struct{}{}
	}
	h.mu.Unlock()
	w := m.call("GET", "/api/computers/"+m.id+"/browser/stream", "", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "browser_preview_busy") {
		t.Fatalf("ninth %d %s", w.Code, w.Body.String())
	}
}

// Real transport tests are opt-out only for the restricted implementation sandbox.
// The coordinator runs these with no TOFI_BROWSER_NO_SOCKETS environment variable.
func TestPersonalBrowserHTTPWebSocket(t *testing.T) {
	if os.Getenv("TOFI_BROWSER_NO_SOCKETS") == "1" {
		t.Skip("sandbox prohibits listening sockets; run outside sandbox")
	}
	s := computerTestServer(t)
	m := enrollMac(t, s, "Transport Mac", "browser.read")
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(id, token string) (*websocket.Conn, error) {
		c, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/computers/"+id+"/browser/channel", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}, Subprotocols: []string{browserProtocol}, CompressionMode: websocket.CompressionDisabled})
		return c, e
	}
	if c, e := dial(uuid.NewString(), m.token); e == nil {
		c.CloseNow()
		t.Fatal("token uploaded to another device")
	}
	c, e := dial(m.id, m.token)
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	write := func(v any) {
		raw, _ := json.Marshal(v)
		if e := c.Write(ctx, websocket.MessageText, raw); e != nil {
			t.Fatal(e)
		}
	}
	read := func() map[string]any {
		_, raw, e := c.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		if json.Unmarshal(raw, &v) != nil {
			t.Fatal("invalid message")
		}
		return v
	}
	write(map[string]any{"type": "hello", "v": 1})
	welcome := read()
	if welcome["type"] != "welcome" || welcome["account_id"] != "owner" || welcome["server_instance_id"] != s.instance.ID {
		t.Fatalf("welcome %v", welcome)
	}
	session, target := uuid.NewString(), uuid.NewString()
	state := browserState{Type: "state", V: 1, Connection: welcome["connection_epoch"].(string), Seq: 1, Session: &session, Target: &target, Viewport: 1, Phase: "idle", Preview: "ready"}
	state.Settings.Enabled = true
	state.Settings.Share = true
	write(state)
	request, e := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/computers/"+m.id+"/browser/stream", nil)
	if e != nil {
		t.Fatal(e)
	}
	resp, e := http.DefaultClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("stream %d %v", resp.StatusCode, resp.Header)
	}
	line, e := bufio.NewReader(resp.Body).ReadString('\n')
	if e != nil || !strings.Contains(line, `"type":"status"`) {
		t.Fatalf("immediate status %s %v", line, e)
	}
	observe := read()
	if observe["type"] != "observe" || observe["enabled"] != true {
		t.Fatalf("observe %v", observe)
	}
	resp.Body.Close()
	stop := read()
	if stop["type"] != "observe" || stop["enabled"] != false {
		t.Fatalf("stop %v", stop)
	}
	// A replacement channel cancels old queued work and gives a new connection epoch.
	r := activeComputerRun(t, s)
	args, e := s.prepareBrowser(m.id, "browser.read", []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.store.queueComputerJob(r, m.id, "browser.read", args)
	if e != nil {
		t.Fatal(e)
	}
	replacement, e := dial(m.id, m.token)
	if e != nil {
		t.Fatal(e)
	}
	defer replacement.CloseNow()
	raw := []byte(`{"type":"hello","v":1}`)
	if e = replacement.Write(ctx, websocket.MessageText, raw); e != nil {
		t.Fatal(e)
	}
	_, raw, e = replacement.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var next map[string]any
	_ = json.Unmarshal(raw, &next)
	if next["connection_epoch"] == welcome["connection_epoch"] {
		t.Fatal("connection epoch reused")
	}
	job, e := s.store.computerJob(j.ID, m.id)
	if e != nil || job.Status != "cancelled" {
		t.Fatalf("old job %v %v", job, e)
	}
}

func TestPersonalBrowserPreviewIsNotPersisted(t *testing.T) {
	s, m, h, _ := browserFixture(t)
	h.mu.Lock()
	h.viewers[make(chan struct{}, 1)] = struct{}{}
	h.observe(false)
	h.mu.Unlock()
	raw := browserTestFrame(t, h)
	if e := s.browserMessage(h, h.upload, raw); e != nil {
		t.Fatal(e)
	}
	var tables []string
	rows, e := s.store.db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var table string
		_ = rows.Scan(&table)
		tables = append(tables, table)
	}
	rows.Close()
	// The entire preview data path has no store write. Search database text cells
	// for the recognizable synthetic JPEG payload as well as the preview identity.
	var frame browserFrame
	_ = json.Unmarshal(raw, &frame)
	for _, table := range tables {
		rows, e := s.store.db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
		if e != nil {
			t.Fatal(e)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range ptrs {
				ptrs[i] = &values[i]
			}
			if e := rows.Scan(ptrs...); e != nil {
				t.Fatal(e)
			}
			for _, value := range values {
				var text string
				switch v := value.(type) {
				case string:
					text = v
				case []byte:
					text = string(v)
				}
				if strings.Contains(text, frame.JPEG) || strings.Contains(text, frame.Observation) {
					t.Fatalf("preview persisted in %s", table)
				}
			}
		}
		rows.Close()
	}
	var seq int
	var name, databasePath string
	if e := s.store.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &databasePath); e != nil {
		t.Fatal(e)
	}
	pixels, _ := base64.StdEncoding.DecodeString(frame.JPEG)
	if e := filepath.WalkDir(filepath.Dir(databasePath), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Contains(contents, []byte(frame.JPEG)) || bytes.Contains(contents, []byte(frame.Observation)) || bytes.Contains(contents, pixels) {
			t.Errorf("preview persisted on disk: %s", path)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	s.closeBrowser(m.id)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.frame != nil || h.observation != "" {
		t.Fatal("disconnect retained frame")
	}
}
func TestPersonalBrowserLimitedAndBackpressure(t *testing.T) {
	s, _, h, _ := browserFixture(t)
	h.mu.Lock()
	h.viewers[make(chan struct{}, 1)] = struct{}{}
	h.observe(false)
	h.samples = make([]browserSample, 11)
	for i := range h.samples {
		h.samples[i] = browserSample{time.Now(), 256 << 10}
	}
	h.limitedSince = time.Now().Add(-6 * time.Second)
	h.mu.Unlock()
	raw := browserTestFrame(t, h)
	if e := s.browserMessage(h, h.upload, raw); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	if !h.limited || h.frame != nil {
		t.Fatal("preview limit not exposed")
	}
	// A slow upload writer has a bounded queue and is disconnected, never waited on.
	for len(h.upload.out) < cap(h.upload.out) {
		h.upload.out <- map[string]any{"type": "synthetic"}
	}
	h.send(map[string]any{"type": "ping"})
	select {
	case <-h.upload.ctx.Done():
	default:
		t.Fatal("full control queue did not cancel transport")
	}
	h.mu.Unlock()
}

type browserNotifyWriter struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (w *browserNotifyWriter) Flush() {
	w.ResponseRecorder.Flush()
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}
func TestPersonalBrowserStreamRevocationAndLogout(t *testing.T) {
	for _, cause := range []string{"revoke", "logout"} {
		t.Run(cause, func(t *testing.T) {
			s, m, _, _ := browserFixture(t)
			var authenticated atomic.Bool
			authenticated.Store(true)
			s.browserSessionValid = func(*http.Request) bool { return authenticated.Load() }
			w := &browserNotifyWriter{httptest.NewRecorder(), make(chan struct{}, 1)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("GET", "/api/computers/"+m.id+"/browser/stream", nil).WithContext(ctx)
			done := make(chan struct{})
			go func() { defer close(done); s.browserStream(w, r, m.id) }()
			select {
			case <-w.flushed:
			case <-time.After(2 * time.Second):
				t.Fatal("status was not immediate")
			}
			if cause == "revoke" {
				if _, e := s.store.revokeComputer(m.id); e != nil {
					t.Fatal(e)
				}
			} else {
				authenticated.Store(false)
			}
			select {
			case <-done:
			case <-time.After(2500 * time.Millisecond):
				t.Fatal("unauthorized stream stayed open")
			}
			h := s.browserHub(m.id)
			h.mu.Lock()
			defer h.mu.Unlock()
			if len(h.viewers) != 0 || h.frame != nil || h.observation != "" {
				t.Fatal("closed viewer retained preview demand")
			}
		})
	}
}

type browserBlockedWriter struct {
	header   http.Header
	started  chan struct{}
	release  chan struct{}
	deadline time.Time
}

func (w *browserBlockedWriter) Header() http.Header { return w.header }
func (w *browserBlockedWriter) WriteHeader(int)     {}
func (w *browserBlockedWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *browserBlockedWriter) Flush() {}
func (w *browserBlockedWriter) Write([]byte) (int, error) {
	close(w.started)
	select {
	case <-w.release:
	case <-time.After(time.Until(w.deadline)):
	}
	return 0, context.DeadlineExceeded
}
func TestPersonalBrowserSlowViewerDoesNotBlockActions(t *testing.T) {
	s, m, h, r := browserFixture(t)
	w := &browserBlockedWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); s.browserStream(w, httptest.NewRequest("GET", "/", nil), m.id) }()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("no immediate stream write")
	}
	defer func() { close(w.release); <-done }()
	if remaining := time.Until(w.deadline); remaining <= 0 || remaining > 2*time.Second {
		t.Fatalf("unbounded viewer write %v", remaining)
	}
	actionDone := make(chan error, 1)
	go func() {
		_, e := s.computerActionOn(context.Background(), r, m.id, "browser.read", []byte(`{}`))
		actionDone <- e
	}()
	job := m.poll(1)
	if job == nil {
		t.Fatal("viewer blocked action dispatch")
	}
	browserAnswer(t, m, h, job, browserReadData(h))
	select {
	case e := <-actionDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("viewer blocked action completion")
	}
}

func TestPersonalBrowserRepeatedIdentityForgery(t *testing.T) {
	s, _, h, _ := browserFixture(t)
	h.mu.Lock()
	h.viewers[make(chan struct{}, 1)] = struct{}{}
	h.observe(false)
	h.mu.Unlock()
	var frame map[string]any
	_ = json.Unmarshal(browserTestFrame(t, h), &frame)
	frame["observation_id"] = uuid.NewString()
	raw, _ := json.Marshal(frame)
	for i := 0; i < 3; i++ {
		e := s.browserMessage(h, h.upload, raw)
		if (e != nil) != (i == 2) {
			t.Fatalf("identity mismatch %d: %v", i, e)
		}
	}
}
