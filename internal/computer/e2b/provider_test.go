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
	lastTimeout  int // seconds in the last /timeout or /connect body
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
	// tplConflictN: answer this many template creates with 409, then accept.
	tplConflictN int
	// tplBuildsJSON replaces the build list of the team's template.
	tplBuildsJSON string
	// onAlias runs when the alias lookup arrives (the answer then waits a moment).
	onAlias func()
	// createMsg: answer a sandbox create with 400 and this message.
	createMsg string
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
		if f.createMsg != "" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"code": 400, "message": f.createMsg})
			return
		}
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
		var body struct {
			Timeout int `json:"timeout"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastTimeout = body.Timeout
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
		var body struct {
			Timeout int `json:"timeout"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastTimeout = body.Timeout
		if f.sandboxState == "paused" { // E2B: a paused sandbox is not in the running set
			w.WriteHeader(404)
			w.Write([]byte(`{"code":404,"message":"sandbox not found"}`))
			return
		}
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
		if f.onAlias != nil {
			f.onAlias()
			time.Sleep(80 * time.Millisecond)
		}
		w.Write([]byte(`{"templateID":"tpl1","public":false}`))
	case r.Method == "GET" && p == "/templates/tpl1":
		f.tplCalls = append(f.tplCalls, "builds")
		if f.tplBuildsJSON != "" {
			w.Write([]byte(f.tplBuildsJSON))
			return
		}
		w.Write([]byte(`{"templateID":"tpl1","builds":[{"buildID":"old0","status":"error","createdAt":"2026-11-01T00:00:00Z","updatedAt":"2026-11-01T00:00:00Z","cpuCount":2,"memoryMB":4096},{"buildID":"bld1","status":"ready","createdAt":"2026-11-04T00:00:00Z","updatedAt":"2026-11-04T00:00:00Z","cpuCount":2,"memoryMB":4096}]}`))
	case r.Method == "POST" && p == "/v3/templates":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.tplSize = b
		name, _ := b["name"].(string)
		f.tplCalls = append(f.tplCalls, "create:"+name)
		if f.tplConflict || f.tplConflictN > 0 {
			if f.tplConflictN > 0 {
				f.tplConflictN--
			}
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

// The person's "turn off when idle" setting: a running sandbox gets the new
// timeout at once; a paused one gets it when it resumes.
func TestSetIdleTimeoutAppliesNowOrOnResume(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(ready)
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	if err := p.SetIdleTimeout(context.Background(), 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if f.timeouts != 1 || f.lastTimeout != 1800 {
		t.Fatalf("running sandbox: timeouts=%d last=%d, want 1 and 1800", f.timeouts, f.lastTimeout)
	}
	// The same value again asks nothing.
	if err := p.SetIdleTimeout(context.Background(), 30*time.Minute); err != nil || f.timeouts != 1 {
		t.Fatalf("unchanged value: err=%v timeouts=%d", err, f.timeouts)
	}
	if err := p.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.SetIdleTimeout(context.Background(), 20*time.Minute); err != nil || f.timeouts != 1 {
		t.Fatalf("paused sandbox: err=%v timeouts=%d, want no call", err, f.timeouts)
	}
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	if f.connects != 1 || f.lastTimeout != 1200 {
		t.Fatalf("resume: connects=%d last=%d, want 1 and 1200", f.connects, f.lastTimeout)
	}
	// Zero restores the 15 minute default.
	if err := p.SetIdleTimeout(context.Background(), 0); err != nil || f.lastTimeout != 900 {
		t.Fatalf("default: err=%v last=%d", err, f.lastTimeout)
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
			if r.URL.Path != "/v2/sandboxes" || r.URL.Query().Get("limit") != "1" || r.Header.Get("X-API-Key") != "k" {
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

func TestRetryWakesAPausedSandbox(t *testing.T) {
	f := newFake(t)
	f.sandboxState = "paused"
	st := ready
	st.SandboxID = "sbx1"
	p, store := f.provider(st)
	if err := p.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.connects != 1 || f.sandboxState != "running" || f.creates != 0 || store.state.SandboxID != "sbx1" {
		t.Fatalf("retry on a paused sandbox: connects=%d state=%s creates=%d stored=%+v", f.connects, f.sandboxState, f.creates, store.state)
	}
}

func TestRetryCreatesASandboxWhenThereIsNone(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(ready)
	if err := p.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || store.state.SandboxID == "" {
		t.Fatalf("retry with no sandbox: creates=%d stored=%+v", f.creates, store.state)
	}
}

func TestASandboxThatAutoPausedResumesInsteadOfLookingGone(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(ready)
	now := time.Unix(1000, 0)
	p.cfg.Now = func() time.Time { return now }
	if _, err := act(p); err != nil { // creates and marks it running
		t.Fatal(err)
	}
	f.mu.Lock()
	f.sandboxState = "paused" // idle timeout: E2B paused it behind our back
	f.mu.Unlock()
	now = now.Add(16 * time.Minute)
	if _, err := act(p); err != nil {
		t.Fatalf("an auto-paused sandbox was not resumed: %v", err)
	}
	if f.creates != 1 || f.connects != 1 || store.state.Phase != PhaseReady || store.state.SandboxID != "sbx1" {
		t.Fatalf("creates=%d connects=%d state=%+v", f.creates, f.connects, store.state)
	}
}

func TestA403IsNotAnInvalidKey(t *testing.T) {
	if got := statusCode(403, CodeSandboxFailed); got != CodeSandboxFailed {
		t.Fatalf("403 maps to %s", got)
	}
	if got := statusCode(401, CodeSandboxFailed); got != CodeInvalidKey {
		t.Fatalf("401 maps to %s", got)
	}
}

func TestABuildFailureNamesItsLastErrors(t *testing.T) {
	b := buildStatus{LogEntries: []buildLogEntry{{Message: "pulling image", Level: "info"}, {Message: "manifest unknown", Level: "error"}, {Message: "disk full", Level: "error"}}}
	if got := b.lastErrors(3); got != " (manifest unknown | disk full)" {
		t.Fatalf("got %q", got)
	}
}

// ---- slice 3 review fixes ----

const errorOnlyBuilds = `{"templateID":"tpl1","builds":[{"buildID":"old0","status":"error","createdAt":"2026-11-01T00:00:00Z"}]}`

func TestAnExistingSandboxIsUsableWhileTheTemplateBuildsOrFailed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase string
		code  string
	}{{"building", PhasePreparing, ""}, {"failed", PhaseFailed, CodeTemplateBuildFailed}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			p, _ := f.provider(ready)
			if _, err := act(p); err != nil {
				t.Fatal(err)
			}
			// A new TOFI version starts a build for the person's own computer (or fails it).
			if err := p.update(func(s *State) { s.Phase, s.ErrorCode = tc.phase, tc.code }); err != nil {
				t.Fatal(err)
			}
			if _, err := act(p); err != nil {
				t.Fatalf("an action on the existing sandbox during a %s template: %v", tc.name, err)
			}
			info, err := p.Info(context.Background())
			if err != nil || info.State != "ready" || info.Phase != PhaseReady {
				t.Fatalf("info %+v err %v", info, err)
			}
			if st := p.Status(); st.TemplatePhase != tc.phase || st.TemplateErrorCode != tc.code {
				t.Fatalf("the template phase is not reported on its own: %+v", st)
			}
			if err := p.Retry(context.Background()); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if f.creates != 1 || f.kills != 0 {
				t.Fatalf("creates %d kills %d: the sandbox was replaced", f.creates, f.kills)
			}
		})
	}
}

func TestTheTemplateGatesOnlyCreation(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(State{Phase: PhasePreparing})
	if _, err := act(p); err == nil || err.(*Error).Code != CodeNotReady {
		t.Fatalf("create during a build: %v", err)
	}
	if f.creates != 0 {
		t.Fatal("a sandbox was created without a template")
	}
}

func TestPrepareKeepsASandboxOfAnOlderTemplate(t *testing.T) {
	f := newFake(t)
	f.buildStatus = []string{"ready"}
	p, store := f.provider(State{Phase: PhaseReady, TemplateID: "tpl0", TemplateVersion: "v0.0.1", SandboxID: "sbx9", SandboxVersion: "v0.0.1"})
	if err := p.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := store.state
	if f.kills != 0 || st.SandboxID != "sbx9" || st.TemplateVersion != "v1.2.3+rc.1" || st.TemplateID != "tpl1" {
		t.Fatalf("kills %d state %+v: the old sandbox must stay", f.kills, st)
	}
	if got := p.Status(); !got.GuestOutdated {
		t.Fatalf("status %+v", got)
	}
}

func TestOutdatedSandboxStaysUsableUntilTheUpgrade(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(State{Phase: PhaseReady, TemplateID: "tpl1", TemplateVersion: "v1"})
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	if got := store.state.SandboxVersion; got != "v1" {
		t.Fatalf("version recorded at create: %q", got)
	}
	if p.Status().GuestOutdated {
		t.Fatal("a current sandbox reported outdated")
	}
	// The template moves to a newer version.
	_ = p.update(func(s *State) { s.TemplateVersion = "v2" })
	if st := p.Status(); !st.GuestOutdated || st.GuestOutdatedRequired {
		t.Fatalf("status %+v", st)
	}
	if info, _ := p.Info(context.Background()); !info.GuestOutdated || info.State != "ready" {
		t.Fatalf("info %+v", info)
	}
	if _, err := act(p); err != nil || f.kills != 0 {
		t.Fatalf("an outdated sandbox must keep working: err %v kills %d", err, f.kills)
	}
	if err := p.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.kills != 1 || f.creates != 2 || store.state.SandboxVersion != "v2" || p.Status().GuestOutdated {
		t.Fatalf("kills %d creates %d state %+v", f.kills, f.creates, store.state)
	}
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	err := p.Upgrade(context.Background())
	if e, ok := err.(*Error); !ok || e.Code != CodeNotOutdated {
		t.Fatalf("upgrade of a current computer: %v", err)
	}
	if f.kills != 1 {
		t.Fatal("a current computer was removed")
	}
}

func TestSandboxWithoutARecordedVersionIsOutdatedButUsable(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(ready)
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	_ = p.update(func(s *State) { s.SandboxVersion = "" })
	if !p.Status().GuestOutdated {
		t.Fatal("a sandbox of unknown version is not flagged")
	}
	if _, err := act(p); err != nil {
		t.Fatalf("unknown version must stay usable: %v", err)
	}
}

func TestUpgradeNeedsAReadyTemplate(t *testing.T) {
	f := newFake(t)
	p, _ := f.provider(ready)
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	// The new template is still building: the old computer is not removed for it.
	_ = p.update(func(s *State) { s.Phase, s.TemplateVersion, s.SandboxVersion = PhasePreparing, "v2", "v1" })
	if err := p.Upgrade(context.Background()); err == nil || err.(*Error).Code != CodeNotOutdated {
		t.Fatalf("upgrade while building: %v", err)
	}
	if f.kills != 0 {
		t.Fatal("the sandbox was removed without a template to replace it")
	}
}

func TestMinimumGuestVersionFailsClosedUntilUpgrade(t *testing.T) {
	f := newFake(t)
	p, store := f.provider(State{Phase: PhaseReady, TemplateID: "tpl1", TemplateVersion: "v2.1.0"})
	p.cfg.MinGuestVersion = "v2.0.0"
	if _, err := act(p); err != nil {
		t.Fatal(err)
	}
	_ = p.update(func(s *State) { s.SandboxVersion = "v1.9.0" })
	_, err := act(p)
	if e, ok := err.(*Error); !ok || e.Code != CodeGuestOutdatedRequired {
		t.Fatalf("below the minimum: %v", err)
	}
	if info, _ := p.Info(context.Background()); !info.GuestOutdatedRequired || info.State != "error" {
		t.Fatalf("info %+v", info)
	}
	if f.actions != 1 {
		t.Fatalf("an action ran on a sandbox below the minimum (%d)", f.actions)
	}
	if err := p.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := act(p); err != nil || store.state.SandboxVersion != "v2.1.0" {
		t.Fatalf("after the upgrade: %v %+v", err, store.state)
	}
	// Unset: not enforced, even for an unknown version.
	p.cfg.MinGuestVersion = ""
	_ = p.update(func(s *State) { s.SandboxVersion = "" })
	if _, err := act(p); err != nil {
		t.Fatalf("no minimum set: %v", err)
	}
}

func TestVersionBelow(t *testing.T) {
	for _, tc := range []struct {
		v, min string
		want   bool
	}{
		{"v1.0.0", "", false}, {"v1.0.0", "v1.0.0", false}, {"v1.0.0", "v1.0.1", true}, {"v1.10.0", "v1.9.0", false},
		{"v2.0.0-rc.1", "v2.0.0", true}, {"v2.0.0", "v2.0.0-rc.1", false}, {"v2.0.0-rc.2", "v2.0.0-rc.10", true},
		{"", "v1.0.0", true}, {"dev", "v1.0.0", true}, {"dev", "dev", false}, {"v1.0.0+build5", "v1.0.0", false},
	} {
		if got := VersionBelow(tc.v, tc.min); got != tc.want {
			t.Errorf("VersionBelow(%q,%q)=%v want %v", tc.v, tc.min, got, tc.want)
		}
	}
}

func TestACanceledAdoptDoesNotPersistFailed(t *testing.T) {
	f := newFake(t)
	f.tplConflict = true
	p, store := f.provider(State{Phase: PhaseNone})
	ctx, cancel := context.WithCancel(context.Background())
	f.onAlias = cancel // the server closes while the alias lookup is in flight
	err := p.Prepare(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if st := store.state; st.Phase != PhasePreparing {
		t.Fatalf("a close left the build as %+v; the next use must resume it", st)
	}
	// The next use resumes the build.
	f.onAlias = nil
	if err := p.Prepare(context.Background()); err != nil || store.state.Phase != PhaseReady {
		t.Fatalf("resume: %v %+v", err, store.state)
	}
}

func TestAnErroredAdoptedBuildStartsANewBuild(t *testing.T) {
	f := newFake(t)
	f.tplConflictN = 1 // the first create meets a race, the second is accepted
	f.tplBuildsJSON = errorOnlyBuilds
	p, store := f.provider(State{Phase: PhaseNone})
	if err := p.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.tplCalls, ",")
	if store.state.Phase != PhaseReady || !strings.HasSuffix(got, "create:tofi-computer-v1-2-3-rc-1,build") {
		t.Fatalf("state %+v calls %s", store.state, got)
	}
}

func TestAStaleTemplateInTheTeamHasItsOwnCode(t *testing.T) {
	f := newFake(t)
	f.tplConflict = true // E2B keeps refusing a new build under the taken name
	f.tplBuildsJSON = errorOnlyBuilds
	p, store := f.provider(State{Phase: PhaseNone})
	err := p.Prepare(context.Background())
	if e, ok := err.(*Error); !ok || e.Code != CodeTemplateStale {
		t.Fatalf("err %v", err)
	}
	if st := store.state; st.Phase != PhaseFailed || st.ErrorCode != CodeTemplateStale {
		t.Fatalf("state %+v", st)
	}
}

func orgProviderWith(f *fake, tpl State, st State) (*Provider, *memStore) {
	store := &memStore{state: st}
	p, err := New(Config{KeySource: func(context.Context) (string, error) { return testKey, nil }, APIBase: f.srv.URL, Version: "v1", Store: store,
		TemplateSource: func(context.Context) (State, error) { return tpl, nil },
		HostURL:        func(port int, id, dom string) string { return f.srv.URL + "/p" + itoa(port) },
	})
	if err != nil {
		f.t.Fatal(err)
	}
	p.pollEvery, p.healthEvery = time.Millisecond, time.Millisecond
	return p, store
}

func TestMemberSeesNoOrganizationTemplateOrBuildIds(t *testing.T) {
	f := newFake(t)
	const tplID, bldID = "tpl_org_secret_123", "bld_org_secret_456"
	tpl := State{Phase: PhaseReady, TemplateID: tplID, BuildID: bldID, TemplateVersion: "v1"}
	f.createMsg = "template " + tplID + " build " + bldID + " could not start"
	p, _ := orgProviderWith(f, tpl, State{Phase: PhaseReady})
	_, err := act(p)
	if err == nil || strings.Contains(err.Error(), tplID) || strings.Contains(err.Error(), bldID) {
		t.Fatalf("the action error names the organization's ids: %v", err)
	}
	info, _ := p.Info(context.Background())
	if strings.Contains(info.Error, tplID) || strings.Contains(info.Error, bldID) || info.Error == "" {
		t.Fatalf("info error %q", info.Error)
	}
	// Text stored before the ids were known to this provider is cleaned when read.
	_ = p.update(func(s *State) { s.Phase, s.ErrorCode, s.Error = PhaseFailed, CodeSandboxFailed, "old text with "+tplID })
	info, _ = p.Info(context.Background())
	if strings.Contains(info.Error, tplID) {
		t.Fatalf("stored error shown with the template id: %q", info.Error)
	}
}

func TestOnlyTeamLevelCallsRejectTheKey(t *testing.T) {
	f := newFake(t)
	var used []string
	mk := func(key string) *Provider {
		store := &memStore{state: ready}
		p, err := New(Config{KeySource: func(context.Context) (string, error) { return key, nil }, APIBase: f.srv.URL, Version: "v1", Store: store,
			HostURL:       func(port int, id, dom string) string { return f.srv.URL + "/p" + itoa(port) },
			OnKeyRejected: func(k string) { used = append(used, k) }})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// A sandbox call answered 401 is not a verdict on the key.
	good := mk(testKey)
	if _, err := act(good); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.getCode = 401
	f.mu.Unlock()
	good.invalidate()
	_, _ = good.Info(context.Background())
	if len(used) != 0 {
		t.Fatalf("a per-sandbox 401 rejected the key: %v", used)
	}
	// A sandbox create with a wrong key is, and says which key it used.
	bad := mk("older-key-0000000")
	bad.state = State{Phase: PhaseReady, TemplateID: "tpl1", TemplateVersion: "v1"}
	_, _ = act(bad)
	if len(used) != 1 || used[0] != "older-key-0000000" {
		t.Fatalf("rejections %v", used)
	}
}
