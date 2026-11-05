package e2b

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

const (
	testKey     = "e2b_secretkey_123456"
	testEnvd    = "envd-secret-token-abc"
	testTraffic = "traffic-secret-token-xyz"
	botID       = "11111111-2222-3333-4444-555555555555"
)

type memStore struct {
	mu    sync.Mutex
	state State
	token string
}

func (m *memStore) LoadState() (State, error) { m.mu.Lock(); defer m.mu.Unlock(); return m.state, nil }
func (m *memStore) SaveState(s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
	return nil
}
func (m *memStore) GuestToken() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token, nil
}
func (m *memStore) SaveGuestToken(t string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = t
	return nil
}

// fake is an E2B plus envd plus guest in one server. Guest and envd paths are
// prefixed by the port through HostURL.
type fake struct {
	t   *testing.T
	srv *httptest.Server

	mu           sync.Mutex
	sandboxState string // "", running, paused
	nextID       int
	creates      int
	connects     int
	connectCode  int // 0 => 200/201 by state, else the error status
	timeouts     int
	kills        int
	uploaded     string
	guestToken   string // token the guest accepts (what was uploaded)
	missingOnce  bool   // guest answers 503 token_missing once
	actions      int
	buildStatus  []string
	buildReason  string
	tplCalls     []string
	getCode      int
	// rootUnreadable: a token written as root is not readable by the guest (uid 1000).
	rootUnreadable bool
	uploadUsers    []string
	// tplConflict: the team already has the template name.
	tplConflict bool
	tplSize     map[string]any
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, nextID: 1}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) sandboxJSON(w http.ResponseWriter, code int, id string) {
	dom := "e2b.app"
	tt := testTraffic
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"sandboxID": id, "templateID": "tpl1", "envdVersion": "0.2.4", "envdAccessToken": testEnvd, "trafficAccessToken": tt, "domain": dom, "clientID": "c"})
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/p49983/"):
		if r.Header.Get("X-Access-Token") != testEnvd || r.Header.Get("e2b-traffic-access-token") != testTraffic {
			http.Error(w, "no", 401)
			return
		}
		user := r.URL.Query().Get("username")
		if r.URL.Query().Get("path") != "/var/lib/tofi/guest-token" || (user != "root" && user != "ubuntu") || r.Header.Get("Content-Type") != "application/octet-stream" {
			http.Error(w, "bad upload", 400)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.uploadUsers = append(f.uploadUsers, user)
		f.uploaded, f.guestToken = string(b), string(b)
		if f.rootUnreadable && user == "root" {
			f.guestToken = ""
		}
		w.Write([]byte(`[]`))
		return
	case strings.HasPrefix(p, "/p8331/"):
		if r.Header.Get("e2b-traffic-access-token") != testTraffic {
			http.Error(w, "no traffic", 403)
			return
		}
		if p == "/p8331/health" {
			w.WriteHeader(200)
			return
		}
		if f.missingOnce {
			f.missingOnce = false
			f.guestToken = ""
		}
		if f.guestToken == "" {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":"guest token not set","code":"token_missing"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.guestToken {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch p {
		case "/p8331/v1/action":
			f.actions++
			var a computer.Action
			_ = json.NewDecoder(r.Body).Decode(&a)
			json.NewEncoder(w).Encode(computer.ActionResult{OK: true, Result: json.RawMessage(`{"echo":"` + a.Name + `"}`)})
		case "/p8331/v1/info":
			json.NewEncoder(w).Encode(map[string]any{"kind": "firecracker", "state": "ready", "workspace_root": "/workspace", "browser": "chromium"})
		}
		return
	}
	if r.Header.Get("X-API-Key") != testKey {
		w.WriteHeader(401)
		w.Write([]byte(`{"code":401,"message":"bad key"}`))
		return
	}
	switch {
	case r.Method == "POST" && p == "/v2/sandboxes":
		if r.Header.Get("Idempotency-Key") == "" {
			f.t.Error("missing Idempotency-Key")
		}
		var body struct {
			TemplateID string `json:"templateID"`
			Timeout    int    `json:"timeout"`
			AutoPause  bool   `json:"autoPause"`
			Network    struct {
				AllowPublicTraffic bool `json:"allowPublicTraffic"`
			} `json:"network"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Timeout != 900 || !body.AutoPause || body.Network.AllowPublicTraffic || body.TemplateID != "tpl1" {
			f.t.Errorf("bad create body %+v", body)
		}
		f.creates++
		id := "sbx" + string(rune('0'+f.nextID))
		f.nextID++
		f.sandboxState = "running"
		f.guestToken = ""
		f.sandboxJSON(w, 201, id)
	case r.Method == "GET" && strings.HasPrefix(p, "/sandboxes/"):
		if f.getCode != 0 {
			w.WriteHeader(f.getCode)
			w.Write([]byte(`{"code":1,"message":"gone"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sandboxID": strings.TrimPrefix(p, "/sandboxes/"), "state": f.sandboxState})
	case r.Method == "POST" && strings.HasSuffix(p, "/connect"):
		f.connects++
		if f.connectCode != 0 {
			w.WriteHeader(f.connectCode)
			w.Write([]byte(`{"code":404,"message":"sandbox not found"}`))
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v2/sandboxes/"), "/connect")
		code := 200
		if f.sandboxState == "paused" {
			code = 201
		}
		f.sandboxState = "running"
		f.sandboxJSON(w, code, id)
	case r.Method == "POST" && strings.HasSuffix(p, "/timeout"):
		f.timeouts++
		w.WriteHeader(204)
	case r.Method == "POST" && strings.HasSuffix(p, "/pause"):
		f.sandboxState = "paused"
		w.WriteHeader(204)
	case r.Method == "DELETE" && strings.HasPrefix(p, "/sandboxes/"):
		f.kills++
		f.sandboxState = ""
		w.WriteHeader(204)
	case r.Method == "GET" && p == "/v2/sandboxes":
		w.Write([]byte(`[]`))
	case r.Method == "GET" && strings.HasPrefix(p, "/templates/aliases/"):
		f.tplCalls = append(f.tplCalls, "alias:"+strings.TrimPrefix(p, "/templates/aliases/"))
		w.Write([]byte(`{"templateID":"tpl1","public":false}`))
	case r.Method == "GET" && p == "/templates/tpl1":
		f.tplCalls = append(f.tplCalls, "builds")
		w.Write([]byte(`{"templateID":"tpl1","builds":[{"buildID":"old0","status":"error","createdAt":"2026-11-01T00:00:00Z","updatedAt":"2026-11-01T00:00:00Z","cpuCount":2,"memoryMB":4096},{"buildID":"bld1","status":"ready","createdAt":"2026-11-04T00:00:00Z","updatedAt":"2026-11-04T00:00:00Z","cpuCount":2,"memoryMB":4096}]}`))
	case r.Method == "POST" && p == "/v3/templates":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.tplSize = b
		name, _ := b["name"].(string)
		f.tplCalls = append(f.tplCalls, "create:"+name)
		if f.tplConflict {
			w.WriteHeader(409)
			w.Write([]byte(`{"code":409,"message":"template name already exists"}`))
			return
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"templateID":"tpl1","buildID":"bld1","public":false,"names":[],"tags":[],"aliases":[]}`))
	case r.Method == "POST" && p == "/v2/templates/tpl1/builds/bld1":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["fromImage"] != "ghcr.io/x/tofi-worker:v1-e2b" || b["startCmd"] == "" || b["readyCmd"] == "" {
			f.t.Errorf("bad build body %v", b)
		}
		f.tplCalls = append(f.tplCalls, "build")
		w.WriteHeader(202)
	case r.Method == "GET" && p == "/templates/tpl1/builds/bld1/status":
		st := "ready"
		if len(f.buildStatus) > 0 {
			st, f.buildStatus = f.buildStatus[0], f.buildStatus[1:]
		}
		resp := map[string]any{"status": st, "templateID": "tpl1", "buildID": "bld1"}
		if st == "error" {
			resp["reason"] = map[string]any{"message": f.buildReason, "step": "start"}
		}
		json.NewEncoder(w).Encode(resp)
	default:
		f.t.Errorf("unexpected %s %s", r.Method, p)
		w.WriteHeader(500)
	}
}

func (f *fake) provider(st State) (*Provider, *memStore) {
	store := &memStore{state: st}
	p, err := New(Config{APIKey: testKey, APIBase: f.srv.URL, Image: "ghcr.io/x/tofi-worker:v1-e2b", Version: "v1.2.3+rc.1", Store: store,
		HostURL: func(port int, id, dom string) string { return f.srv.URL + "/p" + itoa(port) },
	})
	if err != nil {
		f.t.Fatal(err)
	}
	p.pollEvery, p.healthEvery = time.Millisecond, time.Millisecond
	return p, store
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

var ready = State{Phase: PhaseReady, TemplateID: "tpl1", TemplateVersion: "v1.2.3+rc.1"}

func act(p *Provider) (computer.ActionResult, error) {
	return p.Action(context.Background(), computer.Action{BotID: botID, Name: "shell.exec"})
}

func TestCreateUploadActionRoundTrip(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(ready)
	res, err := act(p)
	if err != nil || !res.OK || string(res.Result) != `{"echo":"shell.exec"}` {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if f.creates != 1 || len(f.uploaded) != 64 || store.token != f.uploaded {
		t.Fatalf("creates=%d uploaded=%q stored=%q", f.creates, f.uploaded, store.token)
	}
	if store.state.SandboxID != "sbx1" {
		t.Fatalf("state %+v", store.state)
	}
	if p.Kind() != "e2b" || p.ID() != "e2b" {
		t.Fatal("kind/id")
	}
}

func TestActionErrorsMatchClient(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(ready)
	if _, err := p.Action(context.Background(), computer.Action{Name: "x"}); err == nil || err.Error() != "bot_id is required" {
		t.Fatalf("err=%v", err)
	}
}

func TestConcurrentActionsCreateOneSandbox(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(ready)
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := act(p); err != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 || f.creates != 1 || f.actions != 8 {
		t.Fatalf("bad=%d creates=%d actions=%d", bad.Load(), f.creates, f.actions)
	}
}

func TestPausedConnectsThenActs(t *testing.T) {
	f := newFake(t)
	f.sandboxState = "paused"
	st := ready
	st.SandboxID = "sbx9"
	p, store := f.provider(st)
	store.token = "existing-token"
	f.guestToken = "existing-token"
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	if f.connects != 1 || f.creates != 0 || f.uploaded != "" {
		t.Fatalf("connects=%d creates=%d uploaded=%q", f.connects, f.creates, f.uploaded)
	}
}

func TestGoneSandboxIsNotRecreatedUntilRetry(t *testing.T) {
	f := newFake(t)
	f.connectCode = 404
	st := ready
	st.SandboxID = "gone"
	p, store := f.provider(st)
	_, err := act(p)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeSandboxGone {
		t.Fatalf("want sandbox_gone, got %v", err)
	}
	if f.creates != 0 || store.state.Phase != PhaseFailed || store.state.ErrorCode != CodeSandboxGone || store.state.SandboxID != "" {
		t.Fatalf("a gone sandbox was replaced silently: creates=%d state=%+v", f.creates, store.state)
	}
	if err := p.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.state.Phase != PhaseReady {
		t.Fatalf("retry did not clear sandbox_gone: %+v", store.state)
	}
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || store.state.SandboxID != "sbx1" {
		t.Fatalf("after retry: creates=%d state=%+v", f.creates, store.state)
	}
}

func TestTimeoutRefreshThrottled(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(ready)
	now := time.Unix(1000, 0)
	p.cfg.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if _, err := act(p); err != nil {
			t.Fatal(err)
		}
	}
	if f.timeouts != 0 {
		t.Fatalf("timeouts=%d", f.timeouts)
	}
	now = now.Add(61 * time.Second)
	act(p)
	act(p)
	if f.timeouts != 1 {
		t.Fatalf("timeouts=%d", f.timeouts)
	}
}

func TestInfoMappingNeverConnects(t *testing.T) {
	f := newFake(t)
	cases := []struct {
		name  string
		st    State
		sb    string
		get   int
		state string
	}{
		{"preparing", State{Phase: PhasePreparing}, "", 0, "starting"},
		{"failed", State{Phase: PhaseFailed, ErrorCode: CodeInvalidKey, Error: "bad key"}, "", 0, "error"},
		{"no sandbox", ready, "", 0, computer.StateHibernated},
		{"running", State{Phase: PhaseReady, TemplateID: "tpl1", SandboxID: "s1"}, "running", 0, "ready"},
		{"paused", State{Phase: PhaseReady, TemplateID: "tpl1", SandboxID: "s1"}, "paused", 0, computer.StateHibernated},
		{"gone", State{Phase: PhaseReady, TemplateID: "tpl1", SandboxID: "s1"}, "", 404, "error"},
	}
	for _, c := range cases {
		f.sandboxState, f.getCode = c.sb, c.get
		p, store := f.provider(c.st)
		info, err := p.Info(context.Background())
		if err != nil || info.State != c.state || info.Kind != "e2b" || info.ID != "e2b" {
			t.Errorf("%s: %+v err=%v", c.name, info, err)
		}
		if c.name == "failed" && info.Error != "bad key" {
			t.Errorf("error %q", info.Error)
		}
		if c.name == "gone" && store.state.SandboxID != "" {
			t.Errorf("sandbox not cleared")
		}
	}
	if f.connects != 0 || f.creates != 0 {
		t.Fatalf("connects=%d creates=%d", f.connects, f.creates)
	}
}

func TestInfoCachedFiveSeconds(t *testing.T) {
	f := newFake(t)
	f.sandboxState = "paused"
	p, _ := f.provider(State{Phase: PhaseReady, TemplateID: "tpl1", SandboxID: "s1"})
	now := time.Unix(1000, 0)
	p.cfg.Now = func() time.Time { return now }
	p.Info(context.Background())
	f.mu.Lock()
	f.sandboxState = "running"
	f.mu.Unlock()
	if i, _ := p.Info(context.Background()); i.State != computer.StateHibernated {
		t.Fatalf("not cached: %s", i.State)
	}
	now = now.Add(6 * time.Second)
	if i, _ := p.Info(context.Background()); i.State != "ready" {
		t.Fatalf("stale: %s", i.State)
	}
}

func TestPrepareBuildingToReady(t *testing.T) {
	f := newFake(t)
	f.buildStatus = []string{"waiting", "building", "building", "ready"}
	p, store := f.provider(State{Phase: PhaseNone})
	if err := p.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := store.state
	if st.Phase != PhaseReady || st.TemplateID != "tpl1" || st.BuildID != "bld1" || st.TemplateVersion != "v1.2.3+rc.1" {
		t.Fatalf("%+v", st)
	}
	if f.tplCalls[0] != "create:tofi-computer-v1-2-3-rc-1" {
		t.Fatalf("%v", f.tplCalls)
	}
	if err := p.Prepare(context.Background()); err != nil || len(f.tplCalls) != 2 {
		t.Fatalf("second prepare should be a no-op: %v %v", err, f.tplCalls)
	}
}

func TestPrepareBuildError(t *testing.T) {
	f := newFake(t)
	f.buildStatus = []string{"building", "error"}
	f.buildReason = "start command exited with code 1"
	p, store := f.provider(State{Phase: PhaseNone})
	err := p.Prepare(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeTemplateBuildFailed || !strings.Contains(e.Message, "start command exited") {
		t.Fatalf("err=%v", err)
	}
	if st := store.state; st.Phase != PhaseFailed || st.ErrorCode != CodeTemplateBuildFailed || !strings.Contains(st.Error, "start command") {
		t.Fatalf("%+v", st)
	}
	f.buildStatus = []string{"error"}
	f.buildReason = "pull access denied for x"
	if err := p.Prepare(context.Background()); !errors.As(err, &e) || e.Code != CodeImageMissing {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareNeedsImage(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(State{})
	p.cfg.Image = ""
	var e *Error
	if err := p.Prepare(context.Background()); !errors.As(err, &e) || e.Code != CodeImageMissing {
		t.Fatalf("%v", err)
	}
}

func TestCheckKey(t *testing.T) {
	for _, c := range []struct {
		status int
		code   string
	}{{200, ""}, {401, CodeInvalidKey}, {429, CodeLimit}, {500, CodeUnreachable}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v2/sandboxes" || r.URL.Query().Get("paginationLimit") != "1" || r.Header.Get("X-API-Key") != "k" {
				t.Errorf("bad request %s", r.URL)
			}
			w.WriteHeader(c.status)
			w.Write([]byte(`{"code":1,"message":"nope"}`))
		}))
		err := CheckKey(context.Background(), nil, srv.URL, "k")
		srv.Close()
		var e *Error
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%d: %v", c.status, err)
		case c.code != "" && (!errors.As(err, &e) || e.Code != c.code):
			t.Errorf("%d: %v", c.status, err)
		}
	}
	if err := CheckKey(context.Background(), nil, "http://127.0.0.1:1", "k"); err == nil || !strings.Contains(err.Error(), CodeUnreachable) {
		t.Fatalf("%v", err)
	}
}

func TestTokenMissingReuploadsAndRetriesOnce(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(ready)
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.missingOnce = true
	f.uploaded = ""
	f.mu.Unlock()
	res, err := act(p)
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if f.uploaded != store.token || f.uploaded == "" {
		t.Fatalf("uploaded=%q", f.uploaded)
	}
	if f.creates != 1 {
		t.Fatalf("creates=%d", f.creates)
	}
}

func TestPauseRemove(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(ready)
	act(p)
	if err := p.Pause(context.Background()); err != nil || f.sandboxState != "paused" {
		t.Fatalf("%v %s", err, f.sandboxState)
	}
	if err := p.Remove(context.Background()); err != nil || f.kills != 1 || store.state.SandboxID != "" || store.token != "" {
		t.Fatalf("%v kills=%d %+v tok=%q", err, f.kills, store.state, store.token)
	}
	info, _ := p.Info(context.Background())
	if info.State != computer.StateHibernated {
		t.Fatalf("%s", info.State)
	}
}

func TestDomainValidation(t *testing.T) {
	for d, ok := range map[string]bool{"e2b.app": true, "a-b.c.d": true, "evil.com/x": false, "a.b:80": false, "x@y.z": false, "a..b": false, "-a.b": false, "": false} {
		if validDomain(d) != ok {
			t.Errorf("%q => %v", d, !ok)
		}
	}
	f := newFake(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/sandboxes" {
			w.WriteHeader(201)
			w.Write([]byte(`{"sandboxID":"s1","templateID":"t","envdVersion":"1","domain":"evil.com/x?"}`))
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	store := &memStore{state: ready}
	p, _ := New(Config{APIKey: testKey, APIBase: srv.URL, Store: store, Version: "v1"})
	_, err := act(p)
	if err == nil || !strings.Contains(err.Error(), "invalid domain") {
		t.Fatalf("%v", err)
	}
	_ = f
}

func TestSecretsNeverInErrors(t *testing.T) {
	// E2B echoes secrets in its message; the key and tokens must be scrubbed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"code":400,"message":"bad request from ` + testKey + ` and ` + testTraffic + `"}`))
	}))
	defer srv.Close()
	store := &memStore{state: ready}
	p, _ := New(Config{APIKey: testKey, APIBase: srv.URL, Store: store, Version: "v1"})
	p.mu.Lock()
	p.trafficTok = testTraffic
	p.mu.Unlock()
	_, err := act(p)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, s := range []string{testKey, testTraffic, testEnvd, store.token} {
		if s != "" && strings.Contains(err.Error(), s) {
			t.Fatalf("secret leaked: %v", err)
		}
	}
	if err := CheckKey(context.Background(), nil, srv.URL, testKey); err != nil && strings.Contains(err.Error(), testKey) {
		t.Fatalf("leak: %v", err)
	}
	// An unreachable server must not echo the key either.
	bad, _ := New(Config{APIKey: testKey, APIBase: "http://127.0.0.1:1", Store: &memStore{state: ready}, Version: "v1"})
	if _, err := act(bad); err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("%v", err)
	}
}

func TestHealthAndStream(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(ready)
	h, err := p.Health(context.Background())
	if err != nil || h.Guest != "not_ready" {
		t.Fatalf("%+v %v", h, err)
	}
	act(p)
	h, _ = p.Health(context.Background())
	if h.Guest != "ok" || h.State != "ready" {
		t.Fatalf("%+v", h)
	}
	info, _ := p.Info(context.Background())
	if info.Browser != "chromium" {
		t.Fatalf("%+v", info)
	}
	if _, err := p.DesktopStreamWithCursor(context.Background(), "bad", ""); err == nil {
		t.Fatal("bad bot id accepted")
	}
}

func TestTokenIsRewrittenAsTheGuestUserWhenRootsFileIsUnreadable(t *testing.T) {
	f := newFake(t)
	f.rootUnreadable = true
	p, _ := f.provider(ready)
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.uploadUsers, ",") != "root,ubuntu" {
		t.Fatalf("uploads as %v", f.uploadUsers)
	}
}

func TestATemplateTheTeamHasIsAdopted(t *testing.T) {
	f := newFake(t)
	f.tplConflict = true
	p, store := f.provider(State{Phase: PhaseNone})
	if err := p.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := store.state; st.Phase != PhaseReady || st.TemplateID != "tpl1" || st.BuildID != "bld1" {
		t.Fatalf("state %+v", st)
	}
	got := strings.Join(f.tplCalls, ",")
	if !strings.Contains(got, "alias:tofi-computer-v1-2-3-rc-1") || !strings.Contains(got, "builds") || strings.Contains(got, "build,") || strings.HasSuffix(got, ",build") {
		t.Fatalf("calls %s", got)
	}
	if f.tplSize["cpuCount"] != float64(2) || f.tplSize["memoryMB"] != float64(4096) {
		t.Fatalf("template size %v", f.tplSize)
	}
}

func TestACustomTemplateIsNeverBuilt(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(State{Phase: PhaseNone, TemplateID: "my-template", CustomTemplate: true})
	p.cfg.Image = ""
	if err := p.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.state.Phase != PhaseReady || len(f.tplCalls) != 0 {
		t.Fatalf("state %+v calls %v", store.state, f.tplCalls)
	}
}

func TestSandboxURLsStayOnE2BDomains(t *testing.T) {
	p, err := New(Config{APIKey: testKey, Store: &memStore{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.hostURL(8331, "sbx1", "evil.example"); err == nil {
		t.Fatal("a foreign domain was accepted with the default API")
	}
	for _, d := range []string{"e2b.app", "eu.e2b.app", "e2b.dev"} {
		if _, err := p.hostURL(8331, "sbx1", d); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
	}
}
