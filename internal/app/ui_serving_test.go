package app

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeUICachePolicyAndCompression(t *testing.T) {
	uiDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(uiDir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := strings.Repeat("console.log('synthetic bundle');\n", 200)
	for name, content := range map[string]string{"index.html": "<!doctype html><script src=\"/assets/app-abc.js\"></script>" + strings.Repeat(" ", 600), "assets/app-abc.js": bundle} {
		if err := os.WriteFile(filepath.Join(uiDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewServer(Config{DataDir: t.TempDir(), UIDir: uiDir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	get := func(path, encoding string) *http.Response {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Result()
	}

	asset := get("/assets/app-abc.js", "gzip, br")
	if got := asset.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("hashed asset Cache-Control = %q", got)
	}
	if asset.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(asset.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("asset not served as gzip javascript: %v", asset.Header)
	}
	zr, err := gzip.NewReader(asset.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !bytes.Equal(plain, []byte(bundle)) {
		t.Fatal("gzip body does not round-trip")
	}

	raw := get("/assets/app-abc.js", "")
	if raw.Header.Get("Content-Encoding") != "" {
		t.Fatal("a client that did not ask for gzip must get the plain file")
	}
	if body, _ := io.ReadAll(raw.Body); string(body) != bundle {
		t.Fatal("plain body changed")
	}

	for _, path := range []string{"/", "/index.html", "/some/client/route"} {
		page := get(path, "gzip")
		if got := page.Header.Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s Cache-Control = %q, want no-cache", path, got)
		}
	}
}
