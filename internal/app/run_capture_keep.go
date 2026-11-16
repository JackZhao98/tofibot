package app

// Screen captures kept in memory for send_message. A capture result carries a
// capture_id; the full picture (before any detail scaling) is held here, on the
// server, per run, and is stored in the database only when the Bot sends it
// with send_message attachments. Nothing is written for a capture that is not
// sent, so the privacy rules of the work trace are not widened.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	keptCapturesPerRun = 8
	// keptCaptureTTL drops what a run that never reported its end still holds.
	keptCaptureTTL = 6 * time.Hour
)

// keptCapture is one picture a run may still send.
type keptCapture struct {
	ID         string
	ComputerID string
	// ComputerName names a person's computer for the approval card; empty for the Bot computer.
	ComputerName string
	// Personal is true for a picture of a person's own computer: sending it goes through guardShare.
	Personal bool
	// Withheld is decided when the picture is taken, by the work-trace privacy rules: a Secret Input
	// window was open, or an origin rule makes the page private.
	Withheld bool
	Mime     string
	Pixels   []byte
	At       time.Time
}

type runCaptureKeep struct {
	mu    sync.Mutex
	items []keptCapture
}

// keepCapture stores the picture of a capture result and returns the result
// with a capture_id field. A result with no decodable picture is returned
// unchanged.
func (s *Server) keepCapture(runID string, k keptCapture, picture, result string) string {
	if runID == "" {
		return result
	}
	_, uri, ok := captureResultFields(picture)
	if !ok {
		return result
	}
	mime, encoded, found := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	if !found {
		return result
	}
	pixels, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(pixels) == 0 {
		return result
	}
	fields, _, ok := captureResultFields(result)
	if !ok {
		return result
	}
	k.ID = "cap-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	k.Mime, k.Pixels, k.At = mime, pixels, time.Now()
	raw, _ := json.Marshal(k.ID)
	fields["capture_id"] = raw
	out, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	s.sweepKeptCaptures()
	v, _ := s.keptCaptures.LoadOrStore(runID, &runCaptureKeep{})
	keep := v.(*runCaptureKeep)
	keep.mu.Lock()
	keep.items = append(keep.items, k)
	if len(keep.items) > keptCapturesPerRun {
		keep.items = append([]keptCapture(nil), keep.items[len(keep.items)-keptCapturesPerRun:]...)
	}
	keep.mu.Unlock()
	return string(out)
}

// keptCaptureOf finds a capture of this run.
func (s *Server) keptCaptureOf(runID, id string) (keptCapture, bool) {
	v, ok := s.keptCaptures.Load(runID)
	if !ok {
		return keptCapture{}, false
	}
	keep := v.(*runCaptureKeep)
	keep.mu.Lock()
	defer keep.mu.Unlock()
	for _, k := range keep.items {
		if k.ID == id {
			return k, true
		}
	}
	return keptCapture{}, false
}

// forgetKeptCaptures drops a finished run's pictures.
func (s *Server) forgetKeptCaptures(runID string) { s.keptCaptures.Delete(runID) }

func (s *Server) sweepKeptCaptures() {
	cutoff := time.Now().Add(-keptCaptureTTL)
	s.keptCaptures.Range(func(key, v any) bool {
		keep := v.(*runCaptureKeep)
		keep.mu.Lock()
		stale := len(keep.items) == 0 || keep.items[len(keep.items)-1].At.Before(cutoff)
		keep.mu.Unlock()
		if stale {
			s.keptCaptures.Delete(key)
		}
		return true
	})
}

// keepBotCapture keeps a Bot computer capture (desktop.capture or
// browser.snapshot) and decides at capture time whether the work-trace rules
// withhold it from being sent. picture is the guest's result before detail
// scaling; result is what the model reads.
func (s *Server) keepBotCapture(ctx context.Context, r Run, action, picture, result string) string {
	if s.store == nil {
		return result
	}
	if _, _, ok := captureResultFields(picture); !ok {
		return result
	}
	pageURL, resolved := "", true
	if action == "browser.snapshot" {
		pageURL = pageURLFromResult(picture)
		resolved = pageURL != ""
	} else if !s.store.secretWindowOpen() && s.traceOriginRulesExist() {
		pageURL, resolved = s.resolveTracePage(ctx, r)
	}
	withheld := s.traceRedaction(pageURL, resolved) != ""
	return s.keepCapture(r.ID, keptCapture{ComputerID: microVMComputerID, Withheld: withheld}, picture, result)
}

// keptCaptureFile returns the bytes and a file name for a kept capture.
func keptCaptureFile(k keptCapture) (*bytes.Reader, string) {
	ext := ".png"
	if k.Mime == "image/jpeg" {
		ext = ".jpg"
	}
	return bytes.NewReader(k.Pixels), "screenshot-" + k.At.UTC().Format("20060102-150405") + ext
}
