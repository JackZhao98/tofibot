package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestDesktopContractReportedByServerInfo(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(DesktopMinClient) || DesktopContract < 1 {
		t.Fatalf("invalid desktop constants: %d %q", DesktopContract, DesktopMinClient)
	}
	want := map[string]any{"contract": float64(DesktopContract), "min_client": DesktopMinClient}
	check := func(label string, body []byte) {
		var info struct {
			Desktop map[string]any `json:"desktop"`
		}
		if err := json.Unmarshal(body, &info); err != nil {
			t.Fatal(err)
		}
		if len(info.Desktop) != 2 || info.Desktop["contract"] != want["contract"] || info.Desktop["min_client"] != want["min_client"] {
			t.Fatalf("%s: desktop = %v", label, info.Desktop)
		}
	}
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/server-info", nil))
	check("single", response.Body.Bytes())

	g := accountFixture(t)
	check("accounts", accountRequest(g, "GET", "/api/server-info", "", nil).Body.Bytes())
}

// The export script reads the constant from source; keep the pattern it relies on.
func TestDesktopContractConstantMatchesExportPattern(t *testing.T) {
	source, err := os.ReadFile("desktop_contract.go")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^const DesktopContract = (\d+)$`).FindSubmatch(source)
	if match == nil {
		t.Fatal("export-desktop-ui.cjs cannot find DesktopContract")
	}
	if n, _ := strconv.Atoi(string(match[1])); n != DesktopContract {
		t.Fatal("mismatch")
	}
}
