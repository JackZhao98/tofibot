package app

import (
	"github.com/JackZhao98/tofibot/internal/computer"
	"log"
	"net/http"
	"strings"
)

// This endpoint is an observer, never a computer owner or a desktop start.
func (s *Server) routeDesktopStream(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) != 4 || parts[0] != "bots" || parts[2] != "computer" || parts[3] != "stream" {
		return false
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method_not_allowed", "method not allowed")
		return true
	}
	// Unlike ordinary GET JSON, a cross-site stream would consume an encoder.
	if !s.originOK(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeErr(w, 403, "csrf", "origin rejected")
		return true
	}
	if _, err := s.store.GetBot(parts[1]); err != nil {
		writeErr(w, 404, "not_found", "Bot not found")
		return true
	}
	if s.microVM == nil {
		writeErr(w, 404, "not_configured", "computer VM is not configured")
		return true
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && cursor != "hidden" {
		writeErr(w, http.StatusBadRequest, "invalid_cursor", "unsupported cursor policy")
		return true
	}
	response, err := s.microVM.DesktopStreamWithCursor(r.Context(), parts[1], cursor)
	if err != nil {
		log.Printf("[desktop-stream] unavailable bot=%s error=%v", parts[1], err)
		writeErr(w, 502, "desktop_stream_unavailable", "desktop video is unavailable")
		return true
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		status, code := response.StatusCode, "desktop_stream_unavailable"
		if status == 409 {
			code = "desktop_stream_busy"
		} else if status == 410 {
			code = "desktop_stopped"
		} else {
			status = 503
		}
		log.Printf("[desktop-stream] rejected bot=%s status=%d code=%s", parts[1], response.StatusCode, code)
		writeErr(w, status, code, code)
		return true
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = computer.CopyDesktopStream(w, response.Body)
	return true
}
