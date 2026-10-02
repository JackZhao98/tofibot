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
