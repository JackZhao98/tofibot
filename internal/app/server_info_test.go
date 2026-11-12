package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestServerInfoBeforeModelConfigurationAndAfterRestart(t *testing.T) {
	t.Setenv("TOFI_HOSTING", "")
	config := Config{DataDir: t.TempDir(), Environment: "acceptance"}
	var firstIdentity string
	for attempt := 0; attempt < 2; attempt++ {
		s, err := NewServer(config)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer s.Close()
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/server-info", nil))
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("discovery status=%d headers=%v", response.Code, response.Header())
			}
			var info map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			if len(info) != 9 || string(info["hosting"]) != `"self"` || string(info["bot_browser"]) != `{"upload":"websocket-json","version":1,"view":"ndjson-jpeg"}` || string(info["notifications"]) != `{"delivery":1,"max_wait":25}` || string(info["service"]) != `"tofi"` || string(info["protocol_version"]) != "1" || string(info["auth"]) != `{"mode":"none"}` || string(info["tenancy"]) != `{"mode":"single"}` || string(info["desktop"]) != `{"contract":1,"min_client":"0.2.1"}` {
				t.Fatalf("unexpected discovery metadata: %s", response.Body.String())
			}
			var identity string
			if err := json.Unmarshal(info["instance_id"], &identity); err != nil {
				t.Fatal(err)
			}
			if _, err := uuid.Parse(identity); err != nil {
				t.Fatalf("invalid identity: %q", identity)
			}
			if attempt == 0 {
				firstIdentity = identity
			} else if identity != firstIdentity {
				t.Fatalf("identity changed after restart: %s -> %s", firstIdentity, identity)
			}
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
				response = httptest.NewRecorder()
				s.Handler().ServeHTTP(response, httptest.NewRequest(method, "/api/server-info", nil))
				if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
					t.Fatalf("discovery accepted %s: %d", method, response.Code)
				}
			}
		}()
	}
}

func TestServerInfoHosting(t *testing.T) {
	for _, hosting := range []string{"self", "cloud"} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/gateway=%t", hosting, gateway), func(t *testing.T) {
				t.Setenv("TOFI_HOSTING", hosting)
				config := Config{DataDir: t.TempDir(), Environment: "acceptance"}
				var handler http.Handler
				if gateway {
					g, err := NewAccountGateway(config)
					if err != nil {
						t.Fatal(err)
					}
					defer g.Close()
					handler = g.Handler()
				} else {
					s, err := NewServer(config)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					handler = s.Handler()
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/server-info", nil))
				var info map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusOK || info["hosting"] != hosting {
					t.Fatalf("unexpected discovery metadata: status=%d body=%s", response.Code, response.Body.String())
				}
			})
		}
	}
}
