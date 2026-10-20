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

func TestServeUICompressionAndCaching(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("console.log('synthetic');\n", 400)
	_ = os.WriteFile(filepath.Join(dir, "assets", "index-abc123.js"), []byte(big), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"+strings.Repeat(" ", 2000)), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "logo.png"), bytes.Repeat([]byte{1}, 3000), 0o600)
	s := &Server{uiDir: dir}
	get := func(path, enc string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if enc != "" {
			req.Header.Set("Accept-Encoding", enc)
		}
		rec := httptest.NewRecorder()
		s.serveUI(rec, req)
		return rec
	}
	rec := get("/assets/index-abc123.js", "gzip, br")
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip, got %q", rec.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("content type %q", rec.Header().Get("Content-Type"))
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("asset cache-control %q", cc)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(zr)
	if string(out) != big {
		t.Fatal("gzip body does not round-trip")
	}
	if rec.Body.Len() >= len(big)/4 {
		t.Fatalf("not compressed: %d", rec.Body.Len())
	}
	if get("/assets/index-abc123.js", "").Header().Get("Content-Encoding") != "" {
		t.Fatal("must not compress without Accept-Encoding")
	}
	if get("/assets/index-abc123.js", "gzip;q=0").Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip;q=0 must be refused")
	}
	for _, p := range []string{"/", "/some/spa/route"} {
		if cc := get(p, "gzip").Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s cache-control %q", p, cc)
		}
	}
	if get("/logo.png", "gzip").Header().Get("Content-Encoding") != "" {
		t.Fatal("binary files must not be compressed")
	}
}
