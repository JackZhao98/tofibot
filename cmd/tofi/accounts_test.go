package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/app"
)

func TestMultiAccountEntrypointRequiresExplicitSafeConfiguration(t *testing.T) {
	cfg := app.Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true}
	values := map[string]string{"TOFI_MULTI_ACCOUNT": "1", "TOFI_ACCOUNT_PROVISIONER_SOCKET": "/tmp/synthetic-broker.sock", "TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT": "/tmp/synthetic-computers", "TOFI_ACCOUNT_COMPUTER_DISK_GIB": "8"}
	for _, test := range []struct{ key, value string }{{"TOFI_MULTI_ACCOUNT", "true"}, {"TOFI_ACCOUNT_PROVISIONER_SOCKET", "relative"}, {"TOFI_ACCOUNT_COMPUTER_DISK_GIB", "0"}, {"TOFI_ACCOUNT_DB_MAX_BYTES", "-1"}} {
		copy := map[string]string{}
		for k, v := range values {
			copy[k] = v
		}
		copy[test.key] = test.value
		if s, err := newConfiguredServer(cfg, func(k string) string { return copy[k] }); err == nil {
			s.Close()
			t.Fatalf("accepted invalid %s", test.key)
		}
	}
	s, err := newConfiguredServer(cfg, func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/session", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"multi_account":true`) {
		t.Fatalf("account handler missing %d %s", w.Code, w.Body.String())
	}
}

func TestComputerBackendResolution(t *testing.T) {
	for _, test := range []struct {
		value      string
		configured bool
		want       string
		fail       bool
	}{
		{"", false, "none", false}, {"", true, "kvm", false}, {"none", true, "none", false}, {"kvm", false, "kvm", false}, {"gvisor", false, "", true},
	} {
		got, err := computerBackend(test.value, test.configured)
		if (err != nil) != test.fail || got != test.want {
			t.Fatalf("computerBackend(%q,%v)=%q,%v", test.value, test.configured, got, err)
		}
	}
}

func TestMultiAccountRunsWithoutAComputerBackend(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unset":    {"TOFI_MULTI_ACCOUNT": "1"},
		"explicit": {"TOFI_MULTI_ACCOUNT": "1", "TOFI_COMPUTER_BACKEND": "none"},
		"ignored":  {"TOFI_MULTI_ACCOUNT": "1", "TOFI_COMPUTER_BACKEND": "none", "TOFI_ACCOUNT_PROVISIONER_SOCKET": "relative", "TOFI_ACCOUNT_COMPUTER_DISK_GIB": "0"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := app.Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true}
			s, err := newConfiguredServer(cfg, func(k string) string { return env[k] })
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/session", nil))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"multi_account":true`) {
				t.Fatalf("account handler missing %d %s", w.Code, w.Body.String())
			}
		})
	}
	// A half-configured Worker, or kvm without its settings, still refuses to start.
	for name, env := range map[string]map[string]string{
		"partial":  {"TOFI_MULTI_ACCOUNT": "1", "TOFI_ACCOUNT_PROVISIONER_SOCKET": "/tmp/synthetic-broker.sock"},
		"kvm-bare": {"TOFI_MULTI_ACCOUNT": "1", "TOFI_COMPUTER_BACKEND": "kvm"},
		"unknown":  {"TOFI_MULTI_ACCOUNT": "1", "TOFI_COMPUTER_BACKEND": "e2b"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := app.Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true}
			if s, err := newConfiguredServer(cfg, func(k string) string { return env[k] }); err == nil {
				s.Close()
				t.Fatal("started with an invalid computer configuration")
			}
		})
	}
}

func TestSingleAccountBackendNoneIgnoresComputerSocket(t *testing.T) {
	cfg := app.Config{DataDir: t.TempDir(), Environment: "acceptance", ComputerSocket: "/nonexistent/computer.sock"}
	env := map[string]string{"TOFI_COMPUTER_BACKEND": "none", "TOFI_COMPUTER_SOCKET": "/nonexistent/computer.sock"}
	s, err := newConfiguredServer(cfg, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/computer/capabilities", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"backend":"none"`) {
		t.Fatalf("capabilities %d %s", w.Code, w.Body.String())
	}
}

func TestRunnerContainerConfigurationIsAllOrNothingAndNeedsNoComputer(t *testing.T) {
	base := map[string]string{"TOFI_MULTI_ACCOUNT": "1", "TOFI_COMPUTER_BACKEND": "none"}
	with := func(extra map[string]string) func(string) string {
		return func(k string) string {
			if v, ok := extra[k]; ok {
				return v
			}
			return base[k]
		}
	}
	for name, extra := range map[string]map[string]string{
		"url only":     {"TOFI_RUNNER_URL": "http://runner:8390"},
		"key only":     {"TOFI_RUNNER_MASTER_KEY_FILE": "/app/data/runner/master.key"},
		"relative key": {"TOFI_RUNNER_URL": "http://runner:8390", "TOFI_RUNNER_MASTER_KEY_FILE": "master.key"},
	} {
		cfg := app.Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true}
		if s, err := newConfiguredServer(cfg, with(extra)); err == nil {
			s.Close()
			t.Fatalf("%s: started with a half-configured Runner", name)
		}
	}
	// With the computer backend none, a complete configuration starts.
	cfg := app.Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true}
	s, err := newConfiguredServer(cfg, with(map[string]string{"TOFI_RUNNER_URL": "http://runner:8390", "TOFI_RUNNER_MASTER_KEY_FILE": "/app/data/runner/master.key"}))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
