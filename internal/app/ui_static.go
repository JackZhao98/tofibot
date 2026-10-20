package app

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Static UI delivery: hashed build assets are immutable and cacheable for a
// year; every other file (index.html, the SPA fallback) must revalidate so a
// new build is picked up. Text files are served gzip-compressed to clients
// that accept it; the compressed bytes are kept in memory per path and mtime.

const uiCompressMin = 1024

var uiGzipCache sync.Map // key path|mtime|size -> []byte

func uiCompressible(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".css", ".html", ".svg", ".json", ".txt", ".map", ".webmanifest":
		return true
	}
	return false
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		if q, ok := strings.CutPrefix(strings.ReplaceAll(strings.TrimSpace(params), " ", ""), "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

func serveUIFile(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, immutable bool) {
	if immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if !uiCompressible(path) || info.Size() < uiCompressMin || r.Header.Get("Range") != "" || !acceptsGzip(r) {
		if uiCompressible(path) {
			w.Header().Add("Vary", "Accept-Encoding")
		}
		http.ServeFile(w, r, path)
		return
	}
	key := path + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "|" + strconv.FormatInt(info.Size(), 10)
	var body []byte
	if v, ok := uiGzipCache.Load(key); ok {
		body = v.([]byte)
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(raw)
		_ = zw.Close()
		body = buf.Bytes()
		uiGzipCache.Store(key, body)
	}
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
