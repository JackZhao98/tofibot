package app

// Browser previews are volatile: one producer and one replaceable frame per device.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const browserProtocol = "tofi.bot-browser.v1"
const browserFrameLimit = 352 << 10

type browserState struct {
	Type       string  `json:"type"`
	V          int     `json:"v"`
	Connection string  `json:"connection_epoch"`
	Seq        int64   `json:"state_seq"`
	Session    *string `json:"session_epoch"`
	Target     *string `json:"target_id"`
	Viewport   int64   `json:"viewport_epoch"`
	Phase      string  `json:"phase"`
	Preview    string  `json:"preview"`
	Settings   struct {
		Enabled bool `json:"enabled"`
		Retain  bool `json:"retain_login"`
		Share   bool `json:"share_frames"`
	} `json:"settings"`
}
type browserFrame struct {
	Type        string `json:"type"`
	V           int    `json:"v"`
	Connection  string `json:"connection_epoch"`
	Observation string `json:"observation_id"`
	Session     string `json:"session_epoch"`
	Target      string `json:"target_id"`
	Viewport    int64  `json:"viewport_epoch"`
	Seq         int64  `json:"seq"`
	Captured    int64  `json:"captured_at_ms"`
	Received    int64  `json:"received_at_ms,omitempty"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	JPEG        string `json:"jpeg"`
}
type browserActivity struct {
	Bot          string `json:"bot_id"`
	Run          string `json:"run_id"`
	Conversation string `json:"conversation_id"`
}
type browserSource struct {
	Computer   string           `json:"computer_id"`
	Kind       string           `json:"kind"`
	Label      string           `json:"label_key"`
	Supported  bool             `json:"supported"`
	Enabled    bool             `json:"enabled"`
	Share      bool             `json:"share_frames"`
	State      string           `json:"state"`
	Code       *string          `json:"code"`
	Connection *string          `json:"connection_epoch"`
	Session    *string          `json:"session_epoch"`
	Target     *string          `json:"target_id"`
	Viewport   int64            `json:"viewport_epoch"`
	Activity   *browserActivity `json:"activity"`
}
type browserTarget struct {
	Target string `json:"target_id"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}
type browserUpload struct {
	conn          *websocket.Conn
	epoch         string
	out           chan any
	ctx           context.Context
	cancel        context.CancelFunc
	lastPong      time.Time
	badIdentities int
	ping          int64
}
type browserHub struct {
	mu           sync.Mutex
	supported    bool
	upload       *browserUpload
	state        *browserState
	observation  string
	frame        *browserFrame
	frameSeq     int64
	viewers      map[chan struct{}]struct{}
	targets      map[string]browserTarget
	activity     *browserActivity
	samples      []browserSample
	limitedSince time.Time
	limited      bool
}
type browserSample struct {
	at    time.Time
	bytes int
}

func (s *Server) browserHub(id string) *browserHub {
	h := &browserHub{viewers: map[chan struct{}]struct{}{}, targets: map[string]browserTarget{}}
	actual, _ := s.browserHubs.LoadOrStore(id, h)
	return actual.(*browserHub)
}
func (h *browserHub) notify() {
	for ch := range h.viewers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (h *browserHub) send(v any) {
	if h.upload != nil {
		select {
		case h.upload.out <- v:
		default:
			h.upload.cancel()
		}
	}
}
func browserValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (h *browserHub) ready() bool {
	v := h.state
	return h.upload != nil && v != nil && v.Settings.Enabled && v.Settings.Share && v.Preview == "ready" && (v.Phase == "idle" || v.Phase == "bot") && v.Session != nil && v.Target != nil
}
func (h *browserHub) observe(renew bool) {
	want := len(h.viewers) > 0 && h.ready()
	if !want {
		if h.observation != "" {
			h.send(map[string]any{"type": "observe", "v": 1, "connection_epoch": h.upload.epoch, "observation_id": h.observation, "session_epoch": h.state.Session, "target_id": h.state.Target, "enabled": false, "lease_ms": 5000})
		}
		h.observation = ""
		h.frame = nil
		h.frameSeq = 0
		h.samples = nil
		h.limited = false
		h.limitedSince = time.Time{}
		return
	}
	if h.observation == "" {
		h.observation = uuid.NewString()
		h.frameSeq = 0
		renew = true
	}
	if renew {
		h.send(map[string]any{"type": "observe", "v": 1, "connection_epoch": h.upload.epoch, "observation_id": h.observation, "session_epoch": h.state.Session, "target_id": h.state.Target, "enabled": true, "lease_ms": 5000})
	}
}
func (h *browserHub) invalidate() {
	if h.observation != "" && h.upload != nil && h.state != nil {
		h.send(map[string]any{"type": "observe", "v": 1, "connection_epoch": h.upload.epoch, "observation_id": h.observation, "session_epoch": h.state.Session, "target_id": h.state.Target, "enabled": false, "lease_ms": 5000})
	}
	h.observation = ""
	h.frame = nil
	h.frameSeq = 0
	h.samples = nil
	h.limited = false
	h.limitedSince = time.Time{}
}
func (s *Server) cancelBrowserJobs(id string) {
	_, _ = s.store.db.Exec(`UPDATE computer_jobs SET status='cancelled',error='browser_transport_unavailable',completed_at=? WHERE device_id=? AND action LIKE 'browser.%' AND status IN ('pending','running')`, now(), id)
}
func (s *Server) closeBrowser(id string) {
	h := s.browserHub(id)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.invalidate()
	if h.upload != nil {
		h.upload.cancel()
		h.upload = nil
	}
	h.state = nil
	h.targets = map[string]browserTarget{}
	h.notify()
}
func (s *Server) browserSessionOK(r *http.Request) bool {
	if bearerToken(r) != "" {
		return false
	}
	if s.browserSessionValid != nil {
		return s.browserSessionValid(r)
	}
	if s.ownerAuth != nil {
		_, _, ok := s.ownerAuth.session(r)
		return ok
	}
	return true // The existing unauthenticated self-host mode has no owner cookie.
}
func (s *Server) browserDevice(id string) bool {
	row, e := s.store.computerRow(id)
	return e == nil && row.Kind == "personal" && row.Status != "detached"
}
func (s *Server) source(id string) browserSource {
	h := s.browserHub(id)
	h.mu.Lock()
	defer h.mu.Unlock()
	out := browserSource{Computer: id, Kind: "mac_browser", Label: "browser.source", Supported: h.supported, State: "unsupported"}
	code := "browser_client_unsupported"
	if h.supported {
		out.State = "offline"
		code = "browser_transport_unavailable"
	}
	if h.upload != nil {
		out.Connection = &h.upload.epoch
	}
	if v := h.state; v != nil && h.upload != nil {
		out.Enabled = v.Settings.Enabled
		out.Share = v.Settings.Share
		out.Session = v.Session
		out.Target = v.Target
		out.Viewport = v.Viewport
		out.State = "idle"
		code = ""
		switch {
		case !v.Settings.Enabled:
			out.State = "disabled"
			code = "browser_not_enabled"
		case v.Phase == "human":
			out.State = "human"
			code = "user_takeover"
		case v.Phase == "stopped":
			out.State = "stopped"
			code = "user_stop"
		case v.Preview == "minimized" || v.Preview == "locked":
			out.State = v.Preview
		case v.Preview == "unavailable":
			out.State = "unavailable"
			code = "browser_control_lost"
		case v.Preview == "closed" || v.Phase == "closed":
			out.State = "closed"
			code = "browser_target_gone"
		case v.Phase == "bot" && h.activity != nil:
			out.State = "working"
		}
		if h.limited && h.ready() {
			code = "browser_preview_limited"
		}
	}
	if h.activity != nil {
		var active int
		_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=? AND status='running'`, h.activity.Run).Scan(&active)
		if active == 0 {
			h.activity = nil
			if out.State == "working" {
				out.State = "idle"
			}
		}
	}
	if h.activity != nil {
		a := *h.activity
		out.Activity = &a
	}
	if code != "" {
		out.Code = &code
	}
	return out
}
func (s *Server) routeBrowser(w http.ResponseWriter, r *http.Request, p string) bool {
	if p == "browser-sources" && r.Method == http.MethodGet {
		if !s.browserSessionOK(r) {
			writeErr(w, 401, "auth_required", "Account sign-in required")
			return true
		}
		rows, e := s.store.listComputerRows()
		if e != nil {
			writeErr(w, 500, "storage", "Cannot list browser sources")
			return true
		}
		sources := []browserSource{}
		for _, row := range rows {
			if row.Kind == "personal" {
				sources = append(sources, s.source(row.ID))
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"v": 1, "sources": sources})
		return true
	}
	parts := strings.Split(p, "/")
	if len(parts) != 4 || parts[0] != "computers" || parts[2] != "browser" || r.Method != http.MethodGet {
		return false
	}
	id := parts[1]
	if parts[3] == "channel" {
		s.browserChannel(w, r, id)
		return true
	}
	if parts[3] == "stream" {
		if !s.browserSessionOK(r) {
			writeErr(w, 401, "auth_required", "Account sign-in required")
			return true
		}
		if !s.browserDevice(id) {
			writeErr(w, 404, "not_found", "Browser source not found")
			return true
		}
		if !s.originOK(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeErr(w, 403, "csrf", "Origin rejected")
			return true
		}
		s.browserStream(w, r, id)
		return true
	}
	return false
}
func (s *Server) browserChannel(w http.ResponseWriter, r *http.Request, id string) {
	if bearerToken(r) == "" || authenticateDevice(s.store.db, id, bearerToken(r)) != nil || !s.browserDevice(id) {
		writeErr(w, 401, "unauthorized", "Invalid device token")
		return
	}
	offered := false
	for _, protocol := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if strings.TrimSpace(protocol) == browserProtocol {
			offered = true
		}
	}
	if !offered {
		writeErr(w, 400, "browser_client_unsupported", "Browser protocol v1 required")
		return
	}
	conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{browserProtocol}, CompressionMode: websocket.CompressionDisabled, InsecureSkipVerify: true})
	if e != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(browserFrameLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	helloCtx, helloCancel := context.WithTimeout(ctx, 5*time.Second)
	typ, raw, e := conn.Read(helloCtx)
	helloCancel()
	var hello struct {
		Type string `json:"type"`
		V    int    `json:"v"`
	}
	if e != nil || typ != websocket.MessageText || browserDecode(raw, &hello) != nil || hello.Type != "hello" || hello.V != 1 {
		browserProtocolError(conn, "browser_client_unsupported")
		return
	}
	h := s.browserHub(id)
	u := &browserUpload{conn: conn, epoch: uuid.NewString(), out: make(chan any, 8), ctx: ctx, cancel: cancel, lastPong: time.Now()}
	h.mu.Lock()
	h.invalidate()
	if h.upload != nil {
		h.upload.cancel()
	}
	s.cancelBrowserJobs(id)
	h.upload = u
	h.supported = true
	h.state = nil
	h.targets = map[string]browserTarget{}
	h.notify()
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.upload == u {
			h.invalidate()
			s.cancelBrowserJobs(id)
			h.upload = nil
			h.state = nil
			h.targets = map[string]browserTarget{}
			h.notify()
		}
		h.mu.Unlock()
	}()
	instance := s.browserInstanceID
	if instance == "" {
		instance = s.instance.ID
	}
	account := s.accountID
	if account == "" {
		account = "owner"
	}
	u.out <- map[string]any{"type": "welcome", "v": 1, "connection_epoch": u.epoch, "server_instance_id": instance, "account_id": account, "heartbeat_ms": 1000, "lease_ms": 5000}
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-u.out:
				raw, _ := json.Marshal(msg)
				writeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
				e := conn.Write(writeCtx, websocket.MessageText, raw)
				stop()
				if e != nil {
					return
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if authenticateDevice(s.store.db, id, bearerToken(r)) != nil {
					return
				}
				h.mu.Lock()
				if h.upload != u || time.Since(u.lastPong) >= 3*time.Second {
					h.mu.Unlock()
					return
				}
				u.ping++
				h.send(map[string]any{"type": "ping", "v": 1, "connection_epoch": u.epoch, "seq": u.ping})
				h.observe(true)
				h.mu.Unlock()
			}
		}
	}()
	for {
		typ, raw, e = conn.Read(ctx)
		if e != nil {
			return
		}
		if typ != websocket.MessageText || s.browserMessage(h, u, raw) != nil {
			browserProtocolError(conn, "browser_control_lost")
			return
		}
	}
}
func (s *Server) browserMessage(h *browserHub, u *browserUpload, raw []byte) error {
	var header struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return errors.New("invalid JSON")
	}
	if header.Type != "frame" && len(raw) > 16<<10 {
		return errors.New("oversized message")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.upload != u {
		return errors.New("stale connection")
	}
	switch header.Type {
	case "state":
		// Check presence independently: zero values must not make missing permission fields valid.
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if !browserInteger(fields, "viewport_epoch", 0, 9007199254740991) {
			return errors.New("invalid viewport epoch")
		}
		if len(fields) != 10 {
			return errors.New("invalid state fields")
		}
		for _, k := range strings.Fields("type v connection_epoch state_seq session_epoch target_id viewport_epoch phase preview settings") {
			if _, ok := fields[k]; !ok {
				return errors.New("missing state field")
			}
		}
		var v browserState
		if browserDecode(raw, &v) != nil || v.V != 1 || v.Connection != u.epoch || v.Seq < 1 || !browserSafe(v.Seq) || !browserSafe(v.Viewport) || (h.state != nil && v.Seq <= h.state.Seq) {
			return errors.New("invalid state")
		}
		var settings map[string]json.RawMessage
		_ = json.Unmarshal(fields["settings"], &settings)
		if len(settings) != 3 {
			return errors.New("invalid settings")
		}
		for _, k := range []string{"enabled", "retain_login", "share_frames"} {
			var b bool
			if json.Unmarshal(settings[k], &b) != nil || string(settings[k]) == "null" {
				return errors.New("invalid setting")
			}
		}
		if !strings.Contains("|idle|bot|human|stopped|closed|", "|"+v.Phase+"|") || !strings.Contains("|ready|disabled|human|minimized|locked|closed|unavailable|", "|"+v.Preview+"|") {
			return errors.New("invalid phase")
		}
		if (v.Session != nil && !browserUUID(*v.Session)) || (v.Target != nil && (!browserUUID(*v.Target) || v.Session == nil)) || (v.Target == nil && v.Viewport != 0) || (v.Target != nil && v.Viewport < 1) {
			return errors.New("invalid state identity")
		}
		old := h.state
		if old == nil || browserValue(old.Session) != browserValue(v.Session) || browserValue(old.Target) != browserValue(v.Target) || v.Phase != old.Phase || v.Preview != old.Preview || v.Settings != old.Settings {
			h.invalidate()
		}
		if old != nil && browserValue(old.Session) != browserValue(v.Session) {
			h.targets = map[string]browserTarget{}
		}
		if old != nil && old.Viewport != v.Viewport {
			h.frame = nil
		}
		h.state = &v
		u.badIdentities = 0
		h.observe(false)
		h.notify()
		return nil
	case "ping", "pong":
		var v struct {
			Type       string `json:"type"`
			V          int    `json:"v"`
			Connection string `json:"connection_epoch"`
			Seq        int64  `json:"seq"`
		}
		m, e := browserObject(raw, "type v connection_epoch seq", "")
		_ = m
		if e != nil || browserDecode(raw, &v) != nil || v.V != 1 || v.Connection != u.epoch || !browserSafe(v.Seq) {
			return errors.New("invalid heartbeat")
		}
		if v.Type == "pong" {
			if v.Seq != u.ping {
				return errors.New("invalid pong")
			}
			u.lastPong = time.Now()
		} else {
			h.send(map[string]any{"type": "pong", "v": 1, "connection_epoch": u.epoch, "seq": v.Seq})
		}
		return nil
	case "frame":
		var v browserFrame
		m, e := browserObject(raw, "type v connection_epoch observation_id session_epoch target_id viewport_epoch seq captured_at_ms width height jpeg", "")
		_ = m
		if e != nil || len(raw) > browserFrameLimit || browserDecode(raw, &v) != nil || v.V != 1 || !browserUUID(v.Observation) || !browserUUID(v.Session) || !browserUUID(v.Target) || !browserSafe(v.Seq) || v.Seq < 1 || !browserSafe(v.Captured) || !browserSafe(v.Viewport) {
			return errors.New("invalid frame")
		}
		if !h.ready() {
			return nil
		}
		if v.Connection != u.epoch || v.Observation != h.observation || v.Session != browserValue(h.state.Session) || v.Target != browserValue(h.state.Target) {
			// One stale frame may have been in flight. Repeated identity mismatches on
			// the authenticated current socket are a protocol violation.
			u.badIdentities++
			if u.badIdentities >= 3 {
				return errors.New("repeated browser identity mismatch")
			}
			return nil
		}
		u.badIdentities = 0
		if v.Viewport != h.state.Viewport || v.Seq <= h.frameSeq {
			return nil
		}
		b, e := base64.StdEncoding.DecodeString(v.JPEG)
		if e != nil || len(b) > 256<<10 {
			return errors.New("invalid JPEG")
		}
		config, e := jpeg.DecodeConfig(bytes.NewReader(b))
		if e != nil || v.Width < 1 || v.Height < 1 || v.Width > 1280 || v.Height > 800 || config.Width != v.Width || config.Height != v.Height {
			return errors.New("invalid JPEG dimensions")
		}
		h.frameSeq = v.Seq
		now := time.Now()
		cut := 0
		total := 0
		for _, sample := range h.samples {
			if now.Sub(sample.at) < time.Second {
				total += sample.bytes
			} else {
				cut++
			}
		}
		h.samples = h.samples[cut:]
		if len(h.samples) >= 11 || total > 1<<20 {
			if h.limitedSince.IsZero() {
				h.limitedSince = now
			}
			if now.Sub(h.limitedSince) >= 5*time.Second {
				h.limited = true
				h.frame = nil
				h.notify()
			}
			return nil
		}
		h.samples = append(h.samples, browserSample{now, len(b)})
		h.limitedSince = time.Time{}
		h.limited = false
		v.Received = now.UnixMilli()
		h.frameSeq = v.Seq
		h.frame = &v
		h.notify()
		return nil
	}
	return errors.New("unknown message type")
}
func (s *Server) browserStream(w http.ResponseWriter, r *http.Request, id string) {
	h := s.browserHub(id)
	wake := make(chan struct{}, 1)
	h.mu.Lock()
	if len(h.viewers) >= 8 {
		h.mu.Unlock()
		writeErr(w, 409, "browser_preview_busy", "Eight viewers are already watching")
		return
	}
	h.viewers[wake] = struct{}{}
	h.observe(false)
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.viewers, wake); h.observe(false); h.mu.Unlock() }()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	rc := http.NewResponseController(w)
	send := func(v any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if json.NewEncoder(w).Encode(v) != nil {
			return false
		}
		return rc.Flush() == nil
	}
	var lastStatus string
	var lastFrame string
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if !s.browserSessionOK(r) || !s.browserDevice(id) {
			return
		}
		source := s.source(id)
		raw, _ := json.Marshal(source)
		if string(raw) != lastStatus {
			if !send(map[string]any{"type": "status", "v": 1, "source": source}) {
				return
			}
			lastStatus = string(raw)
		}
		h.mu.Lock()
		frame := h.frame
		ready := h.ready()
		h.mu.Unlock()
		if ready && frame != nil {
			key := fmt.Sprintf("%s:%d", frame.Observation, frame.Seq)
			if key != lastFrame {
				if !send(frame) {
					return
				}
				lastFrame = key
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-tick.C:
			if !send(map[string]any{"type": "ping", "v": 1, "server_time_ms": time.Now().UnixMilli()}) {
				return
			}
		}
	}
}

func browserProtocolError(conn *websocket.Conn, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, _ := json.Marshal(map[string]any{"type": "error", "v": 1, "code": code})
	_ = conn.Write(ctx, websocket.MessageText, raw)
	_ = conn.Close(websocket.StatusPolicyViolation, code)
}
