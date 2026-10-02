package guest

import (
	"context"
	"net/http"
	"time"
)

const desktopStreamLifetime = 30 * time.Minute
const maxSharedDesktopViewers = 8

// reserveDesktopStream only observes an already-running desktop. Unlike an
// action, a stream must not allocate a display, hold a slot or renew idle time.
func (s *Service) reserveDesktopStream(botID string) (*desktop, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.desktopLocked(botID)
	if s.closed || d == nil || d.stopping || !d.readyClosed || d.startErr != nil {
		return nil, http.StatusGone
	}
	if d.shared {
		if d.streamViewers >= maxSharedDesktopViewers {
			return nil, http.StatusConflict
		}
		d.streamViewers++
	} else if d.streamActive {
		return nil, http.StatusConflict
	} else {
		d.streamActive = true
	}
	return d, http.StatusOK
}

func (s *Service) handleDesktopStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	botID := r.URL.Query().Get("bot_id")
	values := r.URL.Query()
	cursor := values.Get("cursor")
	_, hasBotID := values["bot_id"]
	if !botIDPattern.MatchString(botID) || len(values["bot_id"]) != 1 || len(values["cursor"]) > 1 || len(values) > 2 || !hasBotID || (cursor != "" && cursor != "hidden") {
		writeError(w, 400, "bot_id must be a canonical UUID")
		return
	}
	for key := range values {
		if key != "bot_id" && key != "cursor" {
			writeError(w, 400, "unsupported stream option")
			return
		}
	}
	d, status := s.reserveDesktopStream(botID)
	if status != http.StatusOK {
		code := "desktop_stopped"
		if status == http.StatusConflict {
			code = "desktop_stream_busy"
		}
		writeError(w, status, code)
		return
	}
	defer func() {
		s.mu.Lock()
		if d.shared {
			if d.streamViewers > 0 {
				d.streamViewers--
			}
		} else {
			d.streamActive = false
		}
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(r.Context(), desktopStreamLifetime)
	defer cancel()
	// Stop/viewing remain independent: the reaper need not wait for this stream.
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				alive := !s.closed && s.desktopLocked(botID) == d && !d.stopping
				s.mu.Unlock()
				if !alive {
					cancel()
					return
				}
			}
		}
	}()
	s.encodeDesktopStream(ctx, w, d, cursor == "hidden")
}
