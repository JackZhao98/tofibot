package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The install manifest and icons are public (the sign-in page must be installable) and cacheable for an hour.
func TestManifestAndIconsServedBeforeSignIn(t *testing.T) {
	s, _ := ownerTestServer(t)
	if s.ownerAuth == nil {
		t.Fatal("owner auth must be on for this test")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "manifest.webmanifest"), []byte(`{"name":"Tofi","start_url":"/"}`), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "pwa", "icon-192.png"), []byte("\x89PNG\r\n\x1a\nxxxx"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o600)
	s.uiDir = dir
	for path, wantType := range map[string]string{"/manifest.webmanifest": "application/manifest+json", "/pwa/icon-192.png": "image/png"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.9:4000" // a stranger with no session
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != wantType {
			t.Fatalf("%s: content type %q, want %q", path, ct, wantType)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
			t.Fatalf("%s: cache-control %q", path, cc)
		}
	}
	// A real manifest is served as-is, not replaced by the SPA fallback.
	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Body.String() != `{"name":"Tofi","start_url":"/"}` {
		t.Fatalf("body %q", rec.Body.String())
	}
}
