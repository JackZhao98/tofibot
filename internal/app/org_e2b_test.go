package app

// Slice 3: the organization's E2B computer (provided/e2b). The fake below is the
// E2B API, the sandbox's envd and its guest in one server. Keys are synthetic.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/e2b"
	"github.com/JackZhao98/tofibot/internal/extensions"
)

const (
	orgE2BKeyText = "org-e2b-synthetic-Zq4Hn8Vx2Lw7Rt"
	ownE2BKeyText = "own-e2b-synthetic-Mc5Jb1Ys9Kd3Pf"
	e2bTestBotID  = "11111111-2222-3333-4444-555555555555"
)

type orgFakeSandbox struct {
	id, state  string
	envd, traf string
	guestToken string
	metadata   map[string]string
}

type orgE2BFake struct {
	t   *testing.T
	srv *httptest.Server

	mu              sync.Mutex
	key             string
	nextSbx         int
	nextTpl         int
	sandboxes       map[string]*orgFakeSandbox
	createBodies    []string
	uploads         []string // "sandbox|user|path|body"
	keysSeen        []string
	templateCreates int
	builds          int
	gate            chan struct{} // builds stay "building" until it is closed
	buildFails      bool
	createStatus    int // answer every sandbox create with this status (echoing the key it was given)
	createMessage   string
	actions         int
	gateTpl         string // when set, only this template's build waits on gate
	sandboxStatus   int    // answer every per-sandbox API call with this status
}

func newOrgE2BFake(t *testing.T, key string) *orgE2BFake {
	t.Helper()
	f := &orgE2BFake{t: t, key: key, sandboxes: map[string]*orgFakeSandbox{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	t.Setenv("TOFI_E2B_API_BASE", f.srv.URL)
	t.Setenv("TOFI_E2B_IMAGE", testE2BImage)
	e2bHostURL = func(port int, sandboxID, _ string) string {
		return fmt.Sprintf("%s/p%d/%s", f.srv.URL, port, sandboxID)
	}
	t.Cleanup(func() { e2bHostURL, e2bNow = nil, nil })
	return f
}

func (f *orgE2BFake) sandboxJSON(w http.ResponseWriter, code int, sb *orgFakeSandbox) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"sandboxID": sb.id, "templateID": "tpl", "envdVersion": "0.2.4", "envdAccessToken": sb.envd, "trafficAccessToken": sb.traf, "domain": "e2b.app", "state": sb.state})
}

func (f *orgE2BFake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	// envd and guest, per sandbox: /p49983/<sbx>/files and /p8331/<sbx>/...
	if strings.HasPrefix(p, "/p49983/") || strings.HasPrefix(p, "/p8331/") {
		parts := strings.SplitN(strings.TrimPrefix(p, "/p"), "/", 3)
		port, id, rest := parts[0], parts[1], "/"+parts[2]
		sb := f.sandboxes[id]
		if sb == nil {
			http.Error(w, "no such sandbox", http.StatusBadGateway) // E2B's proxy for a sandbox that is gone
			return
		}
		if r.Header.Get("e2b-traffic-access-token") != sb.traf {
			http.Error(w, "no", 403)
			return
		}
		if port == "49983" {
			if r.Header.Get("X-Access-Token") != sb.envd {
				http.Error(w, "no", 401)
				return
			}
			body, _ := io.ReadAll(r.Body)
			f.uploads = append(f.uploads, id+"|"+r.URL.Query().Get("username")+"|"+r.URL.Query().Get("path")+"|"+string(body))
			sb.guestToken = string(body)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if rest == "/health" {
			return
		}
		if sb.guestToken == "" {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"code":"token_missing"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+sb.guestToken {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch rest {
		case "/v1/action":
			f.actions++
			_ = json.NewEncoder(w).Encode(computer.ActionResult{OK: true, Result: json.RawMessage(`{"ok":true}`)})
		case "/v1/info":
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "firecracker", "state": "ready", "workspace_root": "/workspace"})
		}
		return
	}
	got := r.Header.Get("X-API-Key")
	f.keysSeen = append(f.keysSeen, got)
	if got != f.key && !(r.Method == http.MethodPost && p == "/v2/sandboxes" && f.createStatus != 0) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"code":401,"message":"invalid api key %s"}`, got)
		return
	}
	switch {
	case r.Method == http.MethodGet && p == "/v2/sandboxes":
		// The list, filtered by the metadata query the way E2B does.
		want := map[string]string{}
		if q := r.URL.Query().Get("metadata"); q != "" {
			for _, kv := range strings.Split(q, "&") {
				k, v, _ := strings.Cut(kv, "=")
				want[k] = v
			}
		}
		rows := []map[string]any{}
		for _, sb := range f.sandboxes {
			match := true
			for k, v := range want {
				if sb.metadata[k] != v {
					match = false
				}
			}
			if match {
				rows = append(rows, map[string]any{"sandboxID": sb.id, "state": sb.state, "metadata": sb.metadata})
			}
		}
		_ = json.NewEncoder(w).Encode(rows)
	case r.Method == http.MethodPost && p == "/v3/templates":
		f.templateCreates++
		f.nextTpl++
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"templateID":"tpl_org_%d","buildID":"bld_org_%d"}`, f.nextTpl, f.nextTpl)
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/v2/templates/") && strings.Contains(p, "/builds/"):
		f.builds++
		w.WriteHeader(202)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/templates/") && strings.HasSuffix(p, "/status"):
		switch {
		case f.buildFails:
			_, _ = w.Write([]byte(`{"status":"error","reason":{"message":"boom"}}`))
		case f.gate != nil && (f.gateTpl == "" || strings.Contains(p, "/"+f.gateTpl+"/")):
			select {
			case <-f.gate:
				_, _ = w.Write([]byte(`{"status":"ready"}`))
			default:
				_, _ = w.Write([]byte(`{"status":"building"}`))
			}
		default:
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		}
	case r.Method == http.MethodPost && p == "/v2/sandboxes":
		body, _ := io.ReadAll(r.Body)
		f.createBodies = append(f.createBodies, string(body))
		if f.createStatus != 0 {
			w.WriteHeader(f.createStatus)
			if f.createMessage != "" {
				fmt.Fprintf(w, `{"code":%d,"message":%q}`, f.createStatus, f.createMessage)
				return
			}
			fmt.Fprintf(w, `{"code":%d,"message":"invalid api key %s"}`, f.createStatus, got)
			return
		}
		var in struct {
			Metadata map[string]string `json:"metadata"`
		}
		_ = json.Unmarshal(body, &in)
		f.nextSbx++
		sb := &orgFakeSandbox{id: fmt.Sprintf("sbx%d", f.nextSbx), state: "running", envd: fmt.Sprintf("envd-tok-%d", f.nextSbx), traf: fmt.Sprintf("traffic-tok-%d", f.nextSbx), metadata: in.Metadata}
		f.sandboxes[sb.id] = sb
		f.sandboxJSON(w, 201, sb)
	case f.sandboxStatus != 0 && strings.HasPrefix(p, "/sandboxes/"):
		w.WriteHeader(f.sandboxStatus)
		_, _ = w.Write([]byte(`{"code":1,"message":"nope"}`))
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/sandboxes/"):
		sb := f.sandboxes[strings.TrimPrefix(p, "/sandboxes/")]
		if sb == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"code":404,"message":"gone"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sandboxID": sb.id, "state": sb.state})
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/connect"):
		sb := f.sandboxes[strings.TrimSuffix(strings.TrimPrefix(p, "/v2/sandboxes/"), "/connect")]
		if sb == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"code":404,"message":"sandbox not found"}`))
			return
		}
		sb.state = "running"
		f.sandboxJSON(w, 200, sb)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/timeout"):
		sb := f.sandboxes[strings.TrimSuffix(strings.TrimPrefix(p, "/sandboxes/"), "/timeout")]
		if sb == nil || sb.state != "running" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(204)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/pause"):
		if sb := f.sandboxes[strings.TrimSuffix(strings.TrimPrefix(p, "/sandboxes/"), "/pause")]; sb != nil {
			sb.state = "paused"
		}
		w.WriteHeader(204)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/sandboxes/"):
		delete(f.sandboxes, strings.TrimPrefix(p, "/sandboxes/"))
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
	}
}

func (f *orgE2BFake) counts() (templates, builds, sandboxes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.templateCreates, f.builds, f.nextSbx
}

func (f *orgE2BFake) sandbox(id string) *orgFakeSandbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sandboxes[id]
}

// seedOrgE2B stores the organization's E2B key and config as an admin PUT would,
// but without the PUT's own start of a template build.
func (f *orgFixture) seedOrgE2B(t *testing.T, key, config string) {
	t.Helper()
	org, err := f.g.orgSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := org.put(secretRecord{ID: e2bKeyVaultID, Name: "E2B API key", Kind: computerProviderKind, Target: envKindE2B, CreatedAt: now()}, key); err != nil {
		t.Fatal(err)
	}
	if err := f.g.saveOrgResource(context.Background(), orgResourceRow{ID: e2bKeyVaultID, Kind: computerProviderKind, Config: config, Status: orgStatusVerified, UpdatedAt: now()}); err != nil {
		t.Fatal(err)
	}
}

func (f *orgFixture) addMember(t *testing.T, name string) (Account, *http.Cookie) {
	t.Helper()
	a, err := f.g.create(context.Background(), name, "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`); err != nil {
		t.Fatal(err)
	}
	return a, accountCookie(t, f.g, a)
}

func (f *orgFixture) setOverridesFor(t *testing.T, id, overrides string) {
	t.Helper()
	f.admin_(t, "PUT", "/api/admin/accounts/"+id+"/policy", `{"overrides":`+overrides+`}`, 200)
}

const providedE2BPolicy = `{"computer":{"mode":"provided","source":"e2b"}}`

func orgE2BPhase(t *testing.T, f *orgFixture) orgE2BBuildView {
	t.Helper()
	out := f.admin_(t, "GET", "/api/admin/org/resources/computer:e2b", "", 200)
	var v orgResourceView
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Build == nil {
		t.Fatalf("no build view: %s", out)
	}
	return *v.Build
}

func orgWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitOrgTemplate(t *testing.T, p *e2b.Provider) {
	t.Helper()
	orgWait(t, "the organization template", func() bool {
		_ = p.RefreshTemplate(context.Background())
		return p.Status().Phase == e2b.PhaseReady
	})
}

func e2bAction(ws *Server) error {
	_, err := ws.computer().Action(context.Background(), computer.Action{BotID: e2bTestBotID, Name: "shell.exec"})
	return err
}

func e2bErrCode(err error) string {
	var e *e2b.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestE2BSandboxGetsNoOrgSecret(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	if !ws.attachedIsOrgE2B() {
		t.Fatal("the organization's computer is not attached")
	}
	waitOrgTemplate(t, ws.orgE2BProvider())
	if err := e2bAction(ws); err != nil {
		t.Fatalf("action on the organization computer: %v", err)
	}

	fake.mu.Lock()
	creates := append([]string(nil), fake.createBodies...)
	uploads := append([]string(nil), fake.uploads...)
	fake.mu.Unlock()
	if len(creates) != 1 || len(uploads) == 0 {
		t.Fatalf("creates=%d uploads=%d", len(creates), len(uploads))
	}
	// The create request: no environment variables, no key anywhere.
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(creates[0]), &body); err != nil {
		t.Fatal(err)
	}
	for k := range body {
		switch k {
		case "templateID", "timeout", "autoPause", "network", "metadata":
		default:
			t.Errorf("the create-sandbox request carries %q", k)
		}
	}
	if s := substr8(creates[0], orgE2BKeyText); s != "" {
		t.Fatalf("the create-sandbox request carries %q of the organization key", s)
	}
	// Every write into the sandbox (envd): only its own guest token.
	for _, u := range uploads {
		parts := strings.SplitN(u, "|", 4)
		if len(parts) != 4 || parts[2] != guestTokenPathForTest {
			t.Errorf("unexpected write into the sandbox: %.60s", u)
			continue
		}
		if s := substr8(u, orgE2BKeyText); s != "" {
			t.Fatalf("a write into the sandbox carries %q of the organization key", s)
		}
		if sb := fake.sandbox(parts[0]); sb == nil || parts[3] != sb.guestToken {
			t.Errorf("the sandbox was given something other than its guest token")
		}
	}
	// The member's workspace holds none of it, and no response shows it.
	scanTreeForKey(t, f.memberDir(), orgE2BKeyText)
	for _, path := range []string{"/api/environments", "/api/config", "/api/computers/firecracker/info"} {
		code, out := f.memberCall(t, "GET", path, "")
		if s := substr8(out, orgE2BKeyText); s != "" {
			t.Fatalf("GET %s (%d) shows %q of the organization key", path, code, s)
		}
	}
}

const guestTokenPathForTest = "/var/lib/tofi/guest-token"

func TestOrgE2BTemplateBuiltOncePerOrg(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	fake.gate = make(chan struct{})
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	members := []Account{f.member}
	for _, n := range []string{"org-member-2", "org-member-3"} {
		a, _ := f.addMember(t, n)
		members = append(members, a)
	}
	for _, m := range members {
		f.setOverridesFor(t, m.ID, providedE2BPolicy)
	}
	// All three start at once and use the computer while the build is still running.
	var wg sync.WaitGroup
	workspaces := make([]*Server, len(members))
	for i, m := range members {
		wg.Add(1)
		go func(i int, m Account) {
			defer wg.Done()
			ws, err := f.g.workspace(m)
			if err != nil {
				t.Error(err)
				return
			}
			workspaces[i] = ws
			for j := 0; j < 3; j++ {
				_, _ = ws.computer().Info(context.Background())
			}
		}(i, m)
	}
	wg.Wait()
	orgWait(t, "the build to start", func() bool { n, _, _ := fake.counts(); return n >= 1 })
	time.Sleep(150 * time.Millisecond) // a second build, if one were started, would have started by now
	if n, b, _ := fake.counts(); n != 1 || b != 1 {
		t.Fatalf("builds started by three accounts: %d template creates, %d builds (want 1, 1)", n, b)
	}
	if v := orgE2BPhase(t, f); v.Phase != e2b.PhasePreparing || v.Custom {
		t.Fatalf("the admin does not see the build running: %+v", v)
	}
	if err := e2bAction(workspaces[0]); e2bErrCode(err) != e2b.CodeNotReady {
		t.Fatalf("an action during the build: %v", err)
	}
	close(fake.gate)
	for _, ws := range workspaces {
		waitOrgTemplate(t, ws.orgE2BProvider())
	}
	if v := orgE2BPhase(t, f); v.Phase != e2b.PhaseReady || v.Version != e2bTemplateVersion() {
		t.Fatalf("build view after the build: %+v", v)
	}
	for _, ws := range workspaces {
		if err := e2bAction(ws); err != nil {
			t.Fatal(err)
		}
	}
	if n, b, s := fake.counts(); n != 1 || b != 1 || s != 3 {
		t.Fatalf("template creates %d, builds %d, sandboxes %d (want 1, 1, 3)", n, b, s)
	}
	// A new TOFI version builds once more, for all of them.
	old := Version
	Version = "v-next"
	t.Cleanup(func() { Version = old })
	var wg2 sync.WaitGroup
	for _, ws := range workspaces {
		wg2.Add(1)
		go func(ws *Server) {
			defer wg2.Done()
			_ = ws.orgE2BProvider().RefreshTemplate(context.Background())
		}(ws)
	}
	wg2.Wait()
	orgWait(t, "the build of the new version", func() bool { n, _, _ := fake.counts(); return n >= 2 })
	time.Sleep(100 * time.Millisecond)
	if n, _, _ := fake.counts(); n != 2 {
		t.Fatalf("template creates after an upgrade: %d (want 2)", n)
	}
}

func TestOrgE2BBuildFailureAndAdminRetry(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	fake.buildFails = true
	f.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	orgWait(t, "the failed build", func() bool { return orgE2BPhase(t, f).Phase == e2b.PhaseFailed })
	v := orgE2BPhase(t, f)
	if v.ErrorCode != e2b.CodeTemplateBuildFailed {
		t.Fatalf("failure code %q", v.ErrorCode)
	}
	// A named code for the member, never the member's own key.
	if err := e2bAction(ws); e2bErrCode(err) != e2b.CodeTemplateBuildFailed {
		t.Fatalf("action after a failed build: %v", err)
	}
	// Members do not each retry a failed build.
	if n, _, _ := fake.counts(); n != 1 {
		t.Fatalf("template creates %d", n)
	}
	if code, _ := f.memberCall(t, "POST", "/api/admin/org/resources/computer:e2b/build", ""); code != 403 {
		t.Fatalf("a member retried the build: %d", code)
	}
	fake.mu.Lock()
	fake.buildFails = false
	fake.mu.Unlock()
	f.admin_(t, "POST", "/api/admin/org/resources/computer:e2b/build", "", 202)
	orgWait(t, "the retried build", func() bool { return orgE2BPhase(t, f).Phase == e2b.PhaseReady })
	waitOrgTemplate(t, ws.orgE2BProvider())
	if err := e2bAction(ws); err != nil {
		t.Fatalf("action after the retry: %v", err)
	}

	// A template the admin supplied is used as it is and never rebuilt.
	out := f.admin_(t, "PUT", "/api/admin/org/resources/computer:e2b", `{"key":"`+orgE2BKeyText+`","template_id":"tpl_custom"}`, 200)
	if !strings.Contains(out, `"custom":true`) {
		t.Fatalf("custom template view: %s", out)
	}
	before, _, _ := fake.counts()
	if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/org/resources/computer:e2b/build", "", 409)); got != codeCustomTemplate {
		t.Fatalf("retry on a custom template: %s", got)
	}
	Version2 := Version
	Version = "v-custom-check"
	t.Cleanup(func() { Version = Version2 })
	waitOrgTemplate(t, ws.orgE2BProvider())
	if after, _, _ := fake.counts(); after != before {
		t.Fatalf("a custom template was rebuilt (%d -> %d)", before, after)
	}
	if st := ws.orgE2BProvider().Status(); st.TemplateID != "tpl_custom" || !st.CustomTemplate {
		t.Fatalf("template of the account: %+v", st)
	}
}

func TestOrgE2BSandboxPerAccountWithAccountMetadata(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	other, _ := f.addMember(t, "org-member-2")
	f.setOverrides(t, providedE2BPolicy)
	f.setOverridesFor(t, other.ID, providedE2BPolicy)
	wsA := f.memberWorkspace(t)
	wsB, err := f.g.workspace(other)
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*Server{wsA, wsB} {
		waitOrgTemplate(t, ws.orgE2BProvider())
		if err := e2bAction(ws); err != nil {
			t.Fatal(err)
		}
	}
	fake.mu.Lock()
	bodies := append([]string(nil), fake.createBodies...)
	fake.mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("sandboxes created: %d", len(bodies))
	}
	seen := map[string]bool{}
	for _, b := range bodies {
		var in struct {
			TemplateID string            `json:"templateID"`
			Metadata   map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(b), &in); err != nil {
			t.Fatal(err)
		}
		if in.Metadata["app"] != "tofi" || in.TemplateID != "tpl_custom" {
			t.Fatalf("create body: %s", b)
		}
		seen[in.Metadata["account"]] = true
	}
	if !seen[f.member.ID] || !seen[other.ID] || len(seen) != 2 {
		t.Fatalf("sandbox metadata names accounts %v, want %s and %s", seen, f.member.ID, other.ID)
	}
	idOf := func(ws *Server) string { return ws.orgE2BProvider().Status().SandboxID }
	if idOf(wsA) == "" || idOf(wsB) == "" || idOf(wsA) == idOf(wsB) {
		t.Fatalf("sandbox ids: %q %q", idOf(wsA), idOf(wsB))
	}
	// The sandbox and guest token are the account's own state, under ids its own E2B computer does not use.
	raw, ok := wsA.autoStored(orgE2BStateKey)
	if !ok || !strings.Contains(raw, idOf(wsA)) || strings.Contains(raw, idOf(wsB)) || strings.Contains(raw, "tpl_custom") {
		t.Fatalf("stored workspace part: %s", raw)
	}
	if _, ok := wsA.autoStored(e2bStateKey); ok {
		t.Fatal("the account's own E2B state key was written")
	}
	if _, _, ok := wsA.vaultValue(orgE2BGuestVaultID); !ok {
		t.Fatal("no guest token in the workspace vault")
	}
	if _, _, ok := wsA.vaultValue(e2bGuestVaultID); ok {
		t.Fatal("the guest token took the id of an own computer's")
	}

	// Idle auto-pause and resume: a paused sandbox resumes, it is not replaced.
	if err := wsA.computer().(computerSleeper).Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sb := fake.sandbox(idOf(wsA)); sb == nil || sb.state != "paused" {
		t.Fatal("the power button did not pause the sandbox")
	}
	if err := e2bAction(wsA); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, _, n := fake.counts(); n != 2 {
		t.Fatalf("a resume created a sandbox (%d)", n)
	}
	// A sandbox that vanished is reported, never silently replaced; Retry makes a new one.
	gone := idOf(wsA)
	fake.mu.Lock()
	delete(fake.sandboxes, gone)
	fake.mu.Unlock()
	if err := e2bAction(wsA); err == nil { // the guest no longer answers
		t.Fatal("a call to a vanished sandbox worked")
	}
	if err := e2bAction(wsA); e2bErrCode(err) != e2b.CodeSandboxGone {
		t.Fatalf("a vanished sandbox: %v", err)
	}
	if _, _, n := fake.counts(); n != 2 {
		t.Fatalf("a vanished sandbox was replaced silently (%d)", n)
	}
	if err := wsA.computer().(interface{ Retry(context.Context) error }).Retry(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, _, n := fake.counts(); n != 3 {
		t.Fatalf("retry created %d sandboxes in all", n)
	}
}

func TestProvidedE2BWithoutOrgKeyFailsNotFallsBack(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText) // the organization stores no key
	ws := f.memberWorkspace(t)
	// The person has an E2B key and a ready computer of their own.
	if err := ws.storeVaultValue(e2bKeyVaultID, "E2B API key", keyHint(ownE2BKeyText), ownE2BKeyText); err != nil {
		t.Fatal(err)
	}
	if err := (e2bStore{ws}).SaveState(e2b.State{TemplateID: "tpl_own", TemplateVersion: e2bTemplateVersion(), Phase: e2b.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	f.setOverrides(t, providedE2BPolicy)
	f.g.policyApply.Wait()
	if !ws.attachedIsOrgE2B() {
		t.Fatal("the organization computer is not attached")
	}
	err := e2bAction(ws)
	if e2bErrCode(err) != codeOrgResourceMissing || orgFailure(err.Error()) != codeOrgResourceMissing {
		t.Fatalf("a missing organization key must be named: %v", err)
	}
	fake.mu.Lock()
	seen := append([]string(nil), fake.keysSeen...)
	fake.mu.Unlock()
	if len(seen) != 0 {
		t.Fatalf("E2B was asked with keys %q although the organization holds none", seen)
	}
	ws.e2bEnv.mu.Lock()
	own := ws.e2bEnv.provider
	ws.e2bEnv.mu.Unlock()
	if own != nil {
		t.Fatal("the person's own E2B computer was built under provided")
	}
	v := environments(t, ws)
	item, ok := environmentByID(v, e2bEnvironmentID)
	if !ok || item.State != "failed" || item.ErrorCode != codeOrgResourceMissing {
		t.Fatalf("environments item: %+v", item)
	}
	// The computer status the workspace banner reads names the same reason.
	info, err := ws.microVMInfo(context.Background())
	if err != nil || info.State != "error" || info.ErrorCode != codeOrgResourceMissing {
		t.Fatalf("computer info: %+v, %v", info, err)
	}
}

func TestPolicyMoveIntoAndOutOfProvidedE2B(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	ws := f.memberWorkspace(t)
	// The account starts on its own E2B computer.
	if err := ws.storeVaultValue(e2bKeyVaultID, "E2B API key", keyHint(ownE2BKeyText), ownE2BKeyText); err != nil {
		t.Fatal(err)
	}
	if err := (e2bStore{ws}).SaveState(e2b.State{TemplateID: "tpl_own", TemplateVersion: e2bTemplateVersion(), Phase: e2b.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	ws.saveBotComputerChoice(envKindE2B)
	own := ws.e2bCurrent()
	if own == nil || ws.attachProvider(own) != nil {
		t.Fatal("could not attach the own computer")
	}
	if ws.attachedIsOrgE2B() {
		t.Fatal("own computer taken for the organization's")
	}

	// Into provided/e2b: the own computer is detached and the organization's attached.
	f.setOverrides(t, providedE2BPolicy)
	f.g.policyApply.Wait()
	if !ws.attachedIsOrgE2B() || ws.computer() == nil || ws.computer().Kind() != envKindE2B {
		t.Fatal("moving into provided/e2b did not attach the organization's computer")
	}
	waitOrgTemplate(t, ws.orgE2BProvider())
	if err := e2bAction(ws); err != nil {
		t.Fatal(err)
	}
	// Out to none: detached through DetachVM, the sandbox turned off.
	f.setOverrides(t, `{"computer":{"mode":"none"}}`)
	f.g.policyApply.Wait()
	if ws.computer() != nil {
		t.Fatal("moving out of provided/e2b left the computer attached")
	}
	orgWait(t, "the sandbox to be paused", func() bool {
		sb := fake.sandbox(ws.orgE2BProvider().Status().SandboxID)
		return sb != nil && sb.state == "paused"
	})
	var status string
	if err := ws.store.db.QueryRow(`SELECT status FROM computers WHERE kind='vm' LIMIT 1`).Scan(&status); err != nil || status != computerRowDetached {
		t.Fatalf("computer row %q %v", status, err)
	}
	// And back in: attached again, with the same sandbox.
	was := ws.orgE2BProvider().Status().SandboxID
	f.setOverrides(t, providedE2BPolicy)
	f.g.policyApply.Wait()
	if !ws.attachedIsOrgE2B() {
		t.Fatal("moving back into provided/e2b did not attach")
	}
	if err := e2bAction(ws); err != nil {
		t.Fatal(err)
	}
	if got := ws.orgE2BProvider().Status().SandboxID; got != was {
		t.Fatalf("sandbox changed from %s to %s", was, got)
	}
	// Out to own: the person's own computer comes back, not the organization's.
	f.setOverrides(t, `{"computer":{"mode":"own"}}`)
	f.g.policyApply.Wait()
	if ws.computer() == nil || ws.attachedIsOrgE2B() || ws.computer() != computer.Provider(own) {
		t.Fatal("moving to own did not hand the person's own computer back")
	}
}

func TestMemberCannotChangeOrgE2B(t *testing.T) {
	f := newOrgFixture(t)
	newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	waitOrgTemplate(t, ws.orgE2BProvider())
	for _, r := range [][3]string{
		{"PUT", "/api/environments/e2b", `{"api_key":"` + ownE2BKeyText + `"}`},
		{"DELETE", "/api/environments/e2b", ``},
		{"POST", "/api/environments/e2b/use", ``},
		{"POST", "/api/environments/firecracker/use", ``},
	} {
		if code, out := f.memberCall(t, r[0], r[1], r[2]); code != 403 || orgErrCode(t, out) != codeManagedByOrganization {
			t.Errorf("%s %s: %d %s", r[0], r[1], code, out)
		}
	}
	if !ws.attachedIsOrgE2B() {
		t.Fatal("a refused write changed the computer")
	}
	if _, _, ok := ws.vaultValue(e2bKeyVaultID); ok {
		t.Fatal("a key was stored in the workspace")
	}
	code, out := f.memberCall(t, "GET", "/api/environments", "")
	var v environmentsView
	if code != 200 || json.Unmarshal([]byte(out), &v) != nil {
		t.Fatalf("%d %s", code, out)
	}
	if v.Policy != (capabilityView{Mode: "provided", Source: sourceOrganization, Via: "e2b"}) || v.Add.E2B || v.Add.VMCommand != "" {
		t.Fatalf("policy view: %+v add %+v", v.Policy, v.Add)
	}
	item, ok := environmentByID(v, e2bEnvironmentID)
	if !ok || !item.BotComputer {
		t.Fatalf("no organization computer listed: %s", out)
	}
	for _, a := range item.Actions {
		if a == "use" || a == "remove" || a == "set_key" {
			t.Errorf("a managed computer offers %q", a)
		}
	}
}

func TestOrgE2BUsageAttributed(t *testing.T) {
	f := newOrgFixture(t)
	newOrgE2BFake(t, orgE2BKeyText)
	var clockMu sync.Mutex
	clock := time.Now()
	e2bNow = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	idle, _ := f.addMember(t, "org-member-idle")
	f.setOverrides(t, providedE2BPolicy)
	f.setOverridesFor(t, idle.ID, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	if _, err := f.g.workspace(idle); err != nil {
		t.Fatal(err)
	}
	waitOrgTemplate(t, ws.orgE2BProvider())
	if err := e2bAction(ws); err != nil { // the sandbox starts
		t.Fatal(err)
	}
	advance(10 * time.Minute)
	if err := e2bAction(ws); err != nil { // still in use: the time so far is reported
		t.Fatal(err)
	}
	advance(3 * time.Minute)
	if err := ws.computer().(computerSleeper).Pause(context.Background()); err != nil { // the power button
		t.Fatal(err)
	}
	var ms int64
	month := time.Now().UTC().Format("2006-01")
	if err := f.g.root.store.db.QueryRow(`SELECT compute_ms FROM org_usage WHERE account_id=? AND month=? AND capability='computer'`, f.member.ID, month).Scan(&ms); err != nil {
		t.Fatalf("no compute usage row for the account: %v", err)
	}
	if want := int64(13 * 60 * 1000); ms != want {
		t.Fatalf("compute_ms = %d, want %d (13 minutes of sandbox time)", ms, want)
	}
	// Attributed to the account that ran it, and only to it.
	var n int
	if err := f.g.root.store.db.QueryRow(`SELECT COUNT(*) FROM org_usage WHERE account_id=? AND capability='computer'`, idle.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("an account that used nothing has %d usage rows (%v)", n, err)
	}
	// A sandbox left running when the server stops ends at its idle timeout, not at shutdown.
	if err := e2bAction(ws); err != nil { // resumes
		t.Fatal(err)
	}
	advance(time.Hour)
	ws.settleOrgE2B()
	if err := f.g.root.store.db.QueryRow(`SELECT compute_ms FROM org_usage WHERE account_id=? AND month=? AND capability='computer'`, f.member.ID, month).Scan(&ms); err != nil {
		t.Fatal(err)
	}
	if want := int64(13*60*1000 + 15*60*1000); ms != want {
		t.Fatalf("compute_ms after settling = %d, want %d (the idle timeout after the last use)", ms, want)
	}
}

func TestOrgE2BKeyRejectionIsRecorded(t *testing.T) {
	f := newOrgFixture(t)
	fake := newOrgE2BFake(t, orgE2BKeyText)
	f.seedOrgE2B(t, orgE2BKeyText, `{"template_id":"tpl_custom"}`)
	f.setOverrides(t, providedE2BPolicy)
	ws := f.memberWorkspace(t)
	waitOrgTemplate(t, ws.orgE2BProvider())
	fake.mu.Lock()
	fake.createStatus = 403
	fake.mu.Unlock()
	err := e2bAction(ws)
	if err == nil {
		t.Fatal("the refused key worked")
	}
	if s := substr8(err.Error(), orgE2BKeyText); s != "" {
		t.Fatalf("the error shows %q of the organization key: %v", s, err)
	}
	row, _, _ := f.g.getOrgResource(context.Background(), e2bKeyVaultID)
	if row.Status != orgStatusRejected || row.ErrorCode != e2b.CodeInvalidKey {
		t.Fatalf("the rejection was not recorded on the resource: %+v", row)
	}
	// The same on the template build.
	f2 := newOrgFixture(t)
	fake2 := newOrgE2BFake(t, "a-different-org-key-12345")
	_ = fake2
	f2.seedOrgE2B(t, orgE2BKeyText, `{}`)
	f2.setOverrides(t, providedE2BPolicy)
	f2.memberWorkspace(t)
	orgWait(t, "the rejection of the build", func() bool {
		row, _, _ := f2.g.getOrgResource(context.Background(), e2bKeyVaultID)
		return row.Status == orgStatusRejected
	})
	if v := orgE2BPhase(t, f2); v.Phase != e2b.PhaseFailed {
		t.Fatalf("build view after a refused key: %+v", v)
	}
}

func TestCapabilitiesReportProvidedE2B(t *testing.T) {
	f := newOrgFixture(t)
	f.setOverrides(t, providedE2BPolicy)
	code, out := f.memberCall(t, "GET", "/api/config", "")
	var cfg struct{ Capabilities map[string]capabilityView }
	if code != 200 || json.Unmarshal([]byte(out), &cfg) != nil {
		t.Fatalf("%d %s", code, out)
	}
	if got := cfg.Capabilities[capComputer]; got != (capabilityView{Mode: "provided", Source: "organization", Via: "e2b"}) {
		t.Fatalf("capabilities.computer = %+v", got)
	}
	code, out = f.memberCall(t, "GET", "/api/environments", "")
	var env struct{ Policy capabilityView }
	if code != 200 || json.Unmarshal([]byte(out), &env) != nil || env.Policy != (capabilityView{Mode: "provided", Source: "organization", Via: "e2b"}) {
		t.Fatalf("environments.policy: %d %s", code, out)
	}
	// The computer's capability matrix is the E2B one once it is attached.
	var caps ComputerCapabilities
	_, out = f.memberCall(t, "GET", "/api/computer/capabilities", "")
	_ = json.Unmarshal([]byte(out), &caps)
	if caps.Attached && caps.Backend != ComputerBackendE2B {
		t.Fatalf("computer capabilities: %+v", caps)
	}
}

func TestOrgSearXNGClientIsBoundToItsHost(t *testing.T) {
	var elsewhereHits int
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereHits++
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer elsewhere.Close()
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, elsewhere.URL+"/search", http.StatusFound)
			return
		}
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer home.Close()
	c := orgEndpointClientFor(home.URL)
	if resp, err := c.Get(home.URL + "/search"); err != nil {
		t.Fatalf("the configured host: %v", err)
	} else {
		resp.Body.Close()
	}
	if resp, err := c.Get(home.URL + "/redirect"); err == nil {
		resp.Body.Close()
		t.Fatal("a redirect to another host was followed")
	}
	if _, err := c.Get(elsewhere.URL + "/search"); err == nil || elsewhereHits != 0 {
		t.Fatalf("a request to another host was made (%d hits, %v)", elsewhereHits, err)
	}
}

func TestReviewerFailureLogIsScrubbed(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f := newOrgFixture(t)
	org, _ := f.g.orgSecrets()
	if _, _, err := org.put(secretRecord{ID: providerVaultID(providerOpenAI), Name: "k", Kind: modelProviderKind, Target: providerOpenAI, CreatedAt: now()}, enforceOrgKey); err != nil {
		t.Fatal(err)
	}
	f.setOverrides(t, `{"ai_models":{"mode":"provided"}}`)
	ws := f.memberWorkspace(t)
	call := extensions.MCPCallApproval{Server: "srv-" + enforceOrgKey, Tool: "tool"}
	ws.logMCPReviewFailure(Run{ID: "run_1"}, call, "gpt-5", 1, time.Second, &mcpReviewFailure{Category: mcpReviewFailureProvider, Detail: "http_401"})
	if !strings.Contains(logs.String(), "reviewer failure run=run_1") {
		t.Fatalf("no failure line: %q", logs.String())
	}
	if s := substr8(logs.String(), enforceOrgKey); s != "" {
		t.Fatalf("the reviewer failure line holds %q of the organization key: %s", s, logs.String())
	}
}
