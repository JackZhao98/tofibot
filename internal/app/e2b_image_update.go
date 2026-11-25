package app

// Keeping the E2B computer on the current image (owner 2026-11-24: no Upgrade button, no "files are removed" dialog;
// the computer updates by itself and keeps its home folder). When the E2B computer runs an older image and nothing
// uses it, the server packs /workspace/home on it, parks the parts on the server's disk, rebuilds the sandbox from
// the current image (Upgrade), pushes the parts back and unpacks them. Installed software and browser sign-ins are
// what the new image brings. The setting computer_image_auto_update (default on) turns it off.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

const (
	autoComputerImageUpdate = "computer_image_auto_update"
	e2bImageCheckEvery      = 10 * time.Minute
)

// e2bImageUpdater is what updateE2BImage needs of the E2B provider (computerUpgrader plus its status).
type e2bImageUpdater interface {
	computer.Provider
	Upgrade(ctx context.Context) error
}

// startE2BImageUpdates checks every ten minutes, and once soon after start.
func (s *Server) startE2BImageUpdates() {
	go func() {
		time.Sleep(time.Minute)
		t := time.NewTicker(e2bImageCheckEvery)
		defer t.Stop()
		for {
			s.maybeUpdateE2BImage()
			<-t.C
		}
	}()
}

// maybeUpdateE2BImage starts one update when the setting is on, the E2B computer is outdated, and it is idle.
func (s *Server) maybeUpdateE2BImage() {
	if on, ok := s.autoStored(autoComputerImageUpdate); ok && on == "false" {
		return
	}
	p := s.e2bCurrent()
	if p == nil {
		return
	}
	attachedNow := s.computer() != nil && s.computer().Kind() == envKindE2B
	if parked := s.parkedHomeMove(); parked != "" {
		// An earlier update rebuilt the computer but could not put the home folder back yet.
		s.startHomeMove("e2b_update", func(ctx context.Context, bot, _ string) error {
			call := standbyGuest(p, bot, parked)
			if attachedNow {
				call = s.attachedGuest(bot, parked)
			}
			return restoreParkedHome(ctx, call, filepath.Join(s.homeMoveParkRoot(), parked), parked)
		})
		return
	}
	st := p.Status()
	if !st.GuestOutdated || st.SandboxID == "" {
		return
	}
	attached := s.computer() != nil && s.computer().Kind() == envKindE2B
	if attached && s.computerInUse(s.computer().ID()) {
		return // a Bot is working on it: try again at the next check
	}
	s.startHomeMove("e2b_update", func(ctx context.Context, bot, moveID string) error {
		var call guestCall
		if attached {
			call = s.attachedGuest(bot, moveID)
		} else {
			call = standbyGuest(p, bot, moveID)
		}
		return s.updateE2BImage(ctx, p, call, moveID)
	})
}

// updateE2BImage rebuilds the computer from the current image and keeps /workspace/home. The parked parts are
// removed only after they were unpacked on the new computer.
func (s *Server) updateE2BImage(ctx context.Context, p e2bImageUpdater, call guestCall, moveID string) error {
	parts, used, err := packHome(ctx, call, moveID)
	if err != nil {
		cleanHomeMove(call, moveID)
		return err
	}
	park := filepath.Join(s.homeMoveParkRoot(), moveID)
	if err := os.MkdirAll(park, 0o700); err != nil {
		cleanHomeMove(call, moveID)
		return err
	}
	dir := homeMoveDir(moveID)
	for _, name := range append(parts, "sums") {
		if err := downloadGuestFile(ctx, call, dir+"/"+name, filepath.Join(park, name)); err != nil {
			cleanHomeMove(call, moveID)
			_ = os.RemoveAll(park)
			return err
		}
	}
	// From here the old computer is gone; the parts live only in park until they are unpacked.
	if err := p.Upgrade(ctx); err != nil {
		_ = os.RemoveAll(park)
		cleanHomeMove(call, moveID)
		return err
	}
	if err := checkHomeSpace(ctx, call, used); err != nil {
		log.Printf("[e2b] image update %s: home kept on the server at %s: %v", moveID, park, err)
		return err
	}
	return restoreParkedHome(ctx, call, park, moveID)
}

// restoreParkedHome pushes parked parts into the computer and unpacks them; the parked copy goes only after that
// worked, so a failure is retried at the next check (maybeUpdateE2BImage).
func restoreParkedHome(ctx context.Context, call guestCall, park, moveID string) error {
	entries, err := os.ReadDir(park)
	if err != nil {
		return err
	}
	dir := homeMoveDir(moveID)
	if _, err := guestShell(ctx, call, "rm -rf "+dir+" && mkdir -p "+dir, 30); err != nil {
		return err
	}
	for _, e := range entries {
		if err := uploadGuestFile(ctx, call, filepath.Join(park, e.Name()), dir+"/"+e.Name()); err != nil {
			log.Printf("[e2b] image update %s: home kept on the server at %s: %v", moveID, park, err)
			return err
		}
	}
	if err := unpackHome(ctx, call, moveID); err != nil {
		log.Printf("[e2b] image update %s: home kept on the server at %s: %v", moveID, park, err)
		return err
	}
	cleanHomeMove(call, moveID)
	return os.RemoveAll(park)
}

// parkedHomeMove names a home folder an earlier image update left on the server, if any.
func (s *Server) parkedHomeMove() string {
	entries, err := os.ReadDir(s.homeMoveParkRoot())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			return e.Name()
		}
	}
	return ""
}

// homeMoveParkRoot is where an image update parks the home parts: the server's data folder, not /tmp (it can be
// gigabytes and must survive a restart).
func (s *Server) homeMoveParkRoot() string {
	if s.homeMoveRoot != "" {
		return s.homeMoveRoot
	}
	return filepath.Join(os.TempDir(), "tofi-home-move")
}

// downloadGuestFile writes one guest file to a local file, page by page.
func downloadGuestFile(ctx context.Context, from guestCall, path, local string) error {
	f, err := os.OpenFile(local, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var offset int64
	version := ""
	for {
		raw, err := from(ctx, "files.export_chunk", map[string]any{"path": path, "offset": offset, "limit": 512 << 10, "version": version})
		if err != nil {
			return err
		}
		var w guestWireChunk
		if json.Unmarshal(raw, &w) != nil || w.Next < offset {
			return errors.New("invalid chunk")
		}
		if offset == 0 {
			version = w.Version
		}
		b, err := base64.StdEncoding.DecodeString(w.Data)
		if err != nil || int64(len(b)) != w.Next-offset {
			return errors.New("invalid chunk")
		}
		if _, err := f.Write(b); err != nil {
			return err
		}
		offset = w.Next
		if w.EOF || offset >= w.Size {
			return f.Sync()
		}
	}
}

// uploadGuestFile writes one local file into the guest, page by page, in order.
func uploadGuestFile(ctx context.Context, to guestCall, local, path string) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	total := info.Size()
	buf := make([]byte, 512<<10)
	var offset int64
	for {
		n, err := io.ReadFull(f, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return err
		}
		if n == 0 && offset > 0 {
			return nil
		}
		res, cerr := to(ctx, "files.import_chunk", map[string]any{"path": path, "offset": offset, "total_size": total, "data_base64": base64.StdEncoding.EncodeToString(buf[:n])})
		if cerr != nil {
			return cerr
		}
		var status struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(res, &status)
		if status.Status == "exists" {
			return errors.New("stale part on the target")
		}
		offset += int64(n)
		if offset >= total {
			return nil
		}
	}
}
