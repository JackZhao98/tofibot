package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

const (
	testE2BKey   = "e2b_synthetic_key_7f3a"
	testE2BImage = "ghcr.io/example/tofi-worker:test-e2b"
)

// fakeE2B answers the E2B REST calls the server makes while it sets up a
// computer, in the shapes of the OpenAPI spec. No sandbox is created here:
// creating one needs the Guest and is covered in internal/computer/e2b.
type fakeE2B struct {
	mu        sync.Mutex
	builds    []map[string]any
	pauses    int
	kills     int
	buildFail string
}

func (f *fakeE2B) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != testE2BKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401,"message":"Invalid API key"}`))
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/sandboxes":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/v3/templates":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"templateID":"tpl_1","buildID":"bld_1","public":false,"names":["tofi-computer-test"],"tags":[],"aliases":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/templates/tpl_1/builds/bld_1":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.builds = append(f.builds, body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/templates/tpl_1/builds/bld_1/status":
			if f.buildFail != "" {
				_, _ = w.Write([]byte(`{"templateID":"tpl_1","buildID":"bld_1","status":"error","reason":{"message":"` + f.buildFail + `"},"logs":[],"logEntries":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"templateID":"tpl_1","buildID":"bld_1","status":"ready","logs":[],"logEntries":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pause"):
			f.pauses++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			f.kills++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TOFI_E2B_API_BASE", srv.URL)
	t.Setenv("TOFI_E2B_IMAGE", testE2BImage)
	return srv
}

func environments(t *testing.T, s *Server) environmentsView {
	t.Helper()
	rec := providerRequest(t, s, http.MethodGet, "/api/environments", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/environments: %d %s", rec.Code, rec.Body.String())
	}
	var v environmentsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func environmentByID(v environmentsView, id string) (environmentItem, bool) {
	for _, e := range v.Environments {
		if e.ID == id {
			return e, true
		}
	}
	return environmentItem{}, false
}

func hasAction(e environmentItem, action string) bool {
	for _, a := range e.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func envWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestE2BKeyIsCheckedAndTheComputerAttachesWhenThereIsNone(t *testing.T) {
	fake := &fakeE2B{}
	fake.serve(t)
	s := newRegistryServer(t)

	v := environments(t, s)
	if !v.Add.E2B || v.E2B.Configured || len(v.Environments) != 0 {
		t.Fatalf("fresh workspace: %+v", v)
	}
	rec := providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"e2b_wrong_key_0000"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_key") {
		t.Fatalf("bad key: %d %s", rec.Code, rec.Body.String())
	}
	if _, _, ok := s.vaultValue(e2bKeyVaultID); ok {
		t.Fatal("a rejected key was stored")
	}

	rec = providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("good key: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testE2BKey) {
		t.Fatal("the response carries the key")
	}
	envWait(t, "the E2B computer to attach", s.botComputerIsE2B)

	caps := s.computerCapabilities()
	if caps.Backend != ComputerBackendE2B || !caps.Available || caps.HostBackend != ComputerBackendNone {
		t.Fatalf("capabilities: %+v", caps)
	}
	for _, c := range caps.Capabilities {
		if c == CapResources || c == CapMCPRunner {
			t.Fatalf("E2B lists %s", c)
		}
	}
	if s.botComputerChoice() != envKindE2B {
		t.Fatalf("choice %q", s.botComputerChoice())
	}
	if got := s.computer().ID(); got != computer.DefaultID {
		t.Fatalf("the Bot computer slot id is %q", got)
	}
	if !s.isVMRoute(computer.DefaultID) {
		t.Fatal("/api/computers/firecracker does not reach the E2B computer")
	}

	v = environments(t, s)
	e, ok := environmentByID(v, e2bEnvironmentID)
	if !ok || !e.BotComputer || e.Access != "all_bots" || hasAction(e, "use") || !hasAction(e, "remove") || e.State != "asleep" {
		t.Fatalf("E2B item: %+v", e)
	}
	if v.Add.E2B || v.E2B.Phase != "ready" || v.E2B.KeyHint != keyHint(testE2BKey) {
		t.Fatalf("view: %+v", v)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.builds) != 1 || fake.builds[0]["fromImage"] != testE2BImage || fake.builds[0]["startCmd"] != "/usr/local/bin/tofi-e2b-start" {
		t.Fatalf("build request: %+v", fake.builds)
	}
	var name string
	if s.store.db.QueryRow(`SELECT name FROM computers WHERE id=? AND kind='vm' AND status='attached'`, computer.DefaultID).Scan(&name) != nil || name != "E2B cloud computer" {
		t.Fatalf("row name %q", name)
	}
}

func TestSwitchTheBotComputerBetweenVMAndE2B(t *testing.T) {
	fake := &fakeE2B{}
	fake.serve(t)
	s := newRegistryServer(t)
	vm := newFakeComputer(computer.DefaultID)
	vm.kind = computer.KindFirecracker
	s.computerFactory = func() (computer.Provider, error) { return vm, nil }
	if err := s.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec := providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("key: %d %s", rec.Code, rec.Body.String())
	}
	envWait(t, "the template", func() bool {
		p := s.e2bCurrent()
		return p != nil && p.Status().Phase == "ready"
	})
	if s.botComputerIsE2B() {
		t.Fatal("the E2B computer replaced the VM without being asked")
	}
	v := environments(t, s)
	if e, _ := environmentByID(v, computer.DefaultID); !e.BotComputer || e.Kind != envKindVM {
		t.Fatalf("VM item: %+v", e)
	}
	if e, _ := environmentByID(v, e2bEnvironmentID); e.BotComputer || !hasAction(e, "use") || e.Access != "none" {
		t.Fatalf("E2B item: %+v", e)
	}

	if rec := providerRequest(t, s, http.MethodPost, "/api/environments/e2b/use", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("use e2b: %d %s", rec.Code, rec.Body.String())
	}
	if !s.botComputerIsE2B() || s.botComputerChoice() != envKindE2B {
		t.Fatal("the E2B computer is not the Bot computer")
	}
	v = environments(t, s)
	if e, ok := environmentByID(v, computer.DefaultID); !ok || e.BotComputer || !hasAction(e, "use") {
		t.Fatalf("set-aside VM: %+v", e)
	}
	if v.Environments[0].ID != e2bEnvironmentID {
		t.Fatalf("the Bot computer is not listed first: %+v", v.Environments)
	}

	if rec := providerRequest(t, s, http.MethodPost, "/api/environments/firecracker/use", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("use vm: %d %s", rec.Code, rec.Body.String())
	}
	if s.computer() != vm || s.botComputerChoice() != envKindVM {
		t.Fatal("the VM did not come back")
	}
	if s.computerCapabilities().Backend != ComputerBackendKVM {
		t.Fatal("capabilities did not follow the VM")
	}

	// Removing an E2B computer that is not in use leaves the VM alone.
	if rec := providerRequest(t, s, http.MethodDelete, "/api/environments/e2b", ""); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	if s.computer() != vm {
		t.Fatal("removing the E2B computer touched the VM")
	}
	if _, _, ok := s.vaultValue(e2bKeyVaultID); ok {
		t.Fatal("the key survived the removal")
	}
	if v = environments(t, s); !v.Add.E2B || v.E2B.Configured {
		t.Fatalf("after remove: %+v", v)
	}
}

func TestRemovingTheE2BBotComputerBringsTheVMBack(t *testing.T) {
	fake := &fakeE2B{}
	fake.serve(t)
	s := newRegistryServer(t)
	vm := newFakeComputer(computer.DefaultID)
	vm.kind = computer.KindFirecracker
	s.computerFactory = func() (computer.Provider, error) { return vm, nil }
	if err := s.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`)
	envWait(t, "the template", func() bool { p := s.e2bCurrent(); return p != nil && p.Status().Phase == "ready" })
	if rec := providerRequest(t, s, http.MethodPost, "/api/environments/e2b/use", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("use: %d", rec.Code)
	}
	if rec := providerRequest(t, s, http.MethodDelete, "/api/environments/e2b", ""); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	if s.computer() != vm || s.botComputerChoice() != "" {
		t.Fatalf("after removing the E2B Bot computer: computer=%v choice=%q", s.computer(), s.botComputerChoice())
	}
}

func TestTheE2BChoiceSurvivesARestart(t *testing.T) {
	fake := &fakeE2B{}
	fake.serve(t)
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`)
	envWait(t, "the E2B computer to attach", s.botComputerIsE2B)
	s.Close()

	s2, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.botComputerIsE2B() {
		t.Fatal("the E2B Bot computer did not come back after a restart")
	}
	fake.mu.Lock()
	builds := len(fake.builds)
	fake.mu.Unlock()
	if builds != 1 {
		t.Fatalf("the restart rebuilt the template (%d builds)", builds)
	}
}

func TestAFailedTemplateBuildShowsE2BsReason(t *testing.T) {
	fake := &fakeE2B{buildFail: "failed to pull image: manifest unknown"}
	fake.serve(t)
	s := newRegistryServer(t)
	providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`)
	envWait(t, "the build to fail", func() bool { return environments(t, s).E2B.Phase == "failed" })
	v := environments(t, s)
	e, _ := environmentByID(v, e2bEnvironmentID)
	if e.State != "failed" || e.ErrorCode != "image_missing" || !strings.Contains(v.E2B.Error, "manifest unknown") || !v.Add.E2B {
		t.Fatalf("failed build: item=%+v e2b=%+v add=%+v", e, v.E2B, v.Add)
	}
	if s.computer() != nil {
		t.Fatal("a computer attached after a failed build")
	}
}

func TestE2BNeedsAnImage(t *testing.T) {
	t.Setenv("TOFI_E2B_IMAGE", "")
	s := newRegistryServer(t)
	if environments(t, s).Add.E2B {
		t.Fatal("a source build without an image offers E2B")
	}
	rec := providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "image_missing") {
		t.Fatalf("no image: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAnExistingTemplateSkipsTheBuild(t *testing.T) {
	fake := &fakeE2B{}
	fake.serve(t)
	t.Setenv("TOFI_E2B_IMAGE", "") // a build with no image can still use a template the person built
	s := newRegistryServer(t)
	rec := providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`","template_id":"my-tofi-template"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	envWait(t, "the E2B computer to attach", s.botComputerIsE2B)
	fake.mu.Lock()
	builds := len(fake.builds)
	fake.mu.Unlock()
	if builds != 0 {
		t.Fatalf("a given template was rebuilt (%d builds)", builds)
	}
	if st := s.e2bCurrent().Status(); st.TemplateID != "my-tofi-template" || !st.CustomTemplate || st.Phase != "ready" {
		t.Fatalf("state %+v", st)
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`","template_id":"bad id!"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad template id: %d", rec.Code)
	}
}

func TestARestartWithoutTheE2BKeyFallsBackToTheVM(t *testing.T) {
	fake := &fakeE2B{}
	fake.serve(t)
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`)
	envWait(t, "the E2B computer to attach", s.botComputerIsE2B)
	if err := s.deleteVaultValue(e2bKeyVaultID); err != nil { // the key went missing out of band
		t.Fatal(err)
	}
	s.Close()

	s2, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.computer() != nil || s2.botComputerChoice() != "" {
		t.Fatalf("restart without the key: computer=%v choice=%q", s2.computer(), s2.botComputerChoice())
	}
}

func TestE2BSecretsStayOutOfTheCredentialsList(t *testing.T) {
	fake := &fakeE2B{}
	fake.serve(t)
	s := newRegistryServer(t)
	providerRequest(t, s, http.MethodPut, "/api/environments/e2b", `{"api_key":"`+testE2BKey+`"}`)
	envWait(t, "the E2B computer to attach", s.botComputerIsE2B)
	if err := s.storeVaultValue(e2bGuestVaultID, "E2B computer access token", "", "guest-token-value"); err != nil {
		t.Fatal(err)
	}
	rec := providerRequest(t, s, http.MethodGet, "/api/computer/credentials", "")
	if strings.Contains(rec.Body.String(), "E2B") || strings.Contains(rec.Body.String(), keyHint(testE2BKey)) {
		t.Fatalf("credentials list shows E2B secrets: %s", rec.Body.String())
	}
	for _, id := range []string{e2bKeyVaultID, e2bGuestVaultID} {
		if rec := providerRequest(t, s, http.MethodDelete, "/api/computer/credentials/"+id, ""); rec.Code == http.StatusOK {
			t.Fatalf("the credentials route deleted %s", id)
		}
	}
}
