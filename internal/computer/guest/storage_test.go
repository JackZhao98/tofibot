//go:build linux || darwin

package guest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceStorageFixedFilesystemMetrics(t *testing.T) {
	s, err := New(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/storage", nil))
	if w.Code != 200 {
		t.Fatalf("storage %d", w.Code)
	}
	var stats StorageStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.TotalBytes == 0 || stats.UsedBytes+stats.FreeBytes != stats.TotalBytes || stats.AvailableBytes > stats.FreeBytes {
		t.Fatalf("invalid stats %+v", stats)
	}
	for _, request := range [][2]string{{"GET", "/v1/storage?path=/"}, {"POST", "/v1/storage"}} {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(request[0], request[1], nil))
		if w.Code == 200 {
			t.Fatal("caller selected storage path/mutation")
		}
	}
	s.root = "/nonexistent-tofi-storage-fixture"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/storage", nil))
	if w.Code != 503 {
		t.Fatal("missing metric converted to zero")
	}
}
