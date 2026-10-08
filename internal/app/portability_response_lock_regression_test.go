package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type reviewBlockedPortableWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *reviewBlockedPortableWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func TestReviewPortableResponseDoesNotHoldRuntimeLock(t *testing.T) {
	for _, endpoint := range []string{"import", "import-failure", "ordinary-settings", "ordinary-settings-failure"} {
		t.Run(endpoint, func(t *testing.T) {
			store, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			server := &Server{store: store, provider: "local", defaultModel: "before", modelCatalog: []ModelOption{{ID: "ordinary", ReasoningEfforts: []string{"medium"}}}, modelCatalogAt: time.Now(), modelCatalogSource: "synthetic"}
			w := &reviewBlockedPortableWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
			done := make(chan struct{})
			if strings.HasPrefix(endpoint, "import") {
				b, e := parsePortableBundle([]byte(portableLegacyFixture))
				if e != nil {
					t.Fatal(e)
				}
				b.Kind = "account"
				b.Included = append(b.Included, "settings")
				b.Settings = &portableSettings{Timezone: "UTC", Model: "imported", ReasoningEffort: "low"}
				b.Counts = b.counts()
				preview, e := store.previewPortable(context.Background(), b)
				if e != nil {
					t.Fatal(e)
				}
				if endpoint == "import-failure" {
					preview.ID = "00000000-0000-0000-0000-000000000099"
				}
				raw, _ := json.Marshal(b)
				body, _ := json.Marshal(portableImportRequest{Bundle: raw, Selection: portableSelection{Categories: b.Included}, PreviewID: preview.ID})
				go func() {
					defer close(done)
					server.routePortability(w, httptest.NewRequest("POST", "/api/portability/apply", strings.NewReader(string(body))), "portability/apply")
				}()
			} else {
				if endpoint == "ordinary-settings-failure" {
					if _, err = store.db.Exec(`CREATE TRIGGER synthetic_settings_failure BEFORE INSERT ON model_settings BEGIN SELECT RAISE(ABORT,'synthetic fixture storage failure'); END`); err != nil {
						t.Fatal(err)
					}
				}
				go func() {
					defer close(done)
					server.modelSettings(w, httptest.NewRequest("PUT", "/api/model-settings", strings.NewReader(`{"model":"ordinary","reasoning_effort":"medium"}`)))
				}()
			}
			defer func() { close(w.release); <-done }()
			select {
			case <-w.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("response did not start")
			}
			expected := 200
			if endpoint == "import-failure" {
				expected = 409
			}
			if endpoint == "ordinary-settings-failure" {
				expected = 500
			}
			if w.Code != expected {
				t.Fatalf("response status=%d, expected=%d", w.Code, expected)
			}
			available := make(chan struct{})
			go func() { server.cancelInactiveRuns(); close(available) }()
			select {
			case <-available:
			case <-time.After(200 * time.Millisecond):
				t.Error("HTTP response backpressure blocks runtime cancellation on the shared Server mutex")
			}
		})
	}
}
