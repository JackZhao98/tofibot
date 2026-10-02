//go:build linux

package guest

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"time"
)

// 20 fps and two-frame (100 ms) fragments reduce capture/mux latency without
// changing the one-thread encoder or 2 Mbit/s VBV budget of the two-vCPU VM.
// The one-second GOP bounds decoder recovery. x11grab reads the exact Xvfb display
// used by screenshots and mouse/keyboard actions, including windows/popups.
func desktopStreamArgs(display string, hideCursor ...bool) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "x11grab", "-framerate", "20", "-video_size", "1280x800"}
	if len(hideCursor) > 0 && hideCursor[0] {
		args = append(args, "-draw_mouse", "0")
	}
	args = append(args, "-i", display,
		"-an", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-threads", "1", "-filter_threads", "1", "-pix_fmt", "yuv420p", "-profile:v", "baseline", "-level:v", "3.2",
		"-crf", "25", "-maxrate", "2M", "-bufsize", "1M", "-g", "20", "-keyint_min", "20", "-sc_threshold", "0",
		"-movflags", "+empty_moov+default_base_moof+frag_keyframe", "-frag_duration", "100000",
		"-flush_packets", "1", "-f", "mp4", "pipe:1")
	return args
}

func (s *Service) encodeDesktopStream(ctx context.Context, w http.ResponseWriter, d *desktop, hideCursor bool) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		writeError(w, 503, "desktop_stream_unavailable")
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", desktopStreamArgs(d.display, hideCursor)...)
	cmd.Env = cleanEnv(s.root, s.root)
	cmd.Stderr = &limitedBuffer{limit: 8192}
	out, err := cmd.StdoutPipe()
	if err != nil {
		writeError(w, 503, "desktop_stream_unavailable")
		return
	}
	if err = cmd.Start(); err != nil {
		writeError(w, 503, "desktop_stream_unavailable")
		return
	}
	defer func() { cancel(); _ = out.Close(); _ = cmd.Wait() }()
	// Wait for real output before claiming a live stream. Bound startup even when
	// the display disappears between reservation and ffmpeg's first X11 read.
	startup := time.AfterFunc(10*time.Second, cancel)
	defer startup.Stop()
	buf := make([]byte, 32*1024)
	n, err := out.Read(buf)
	startup.Stop()
	if n == 0 || err != nil {
		writeError(w, 503, "desktop_stream_unavailable")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	control := http.NewResponseController(w)
	defer control.SetWriteDeadline(time.Time{})
	for {
		// A slow/non-reading viewer cannot keep an encoder blocked indefinitely.
		_ = control.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err = w.Write(buf[:n]); err != nil {
			return
		}
		if err = control.Flush(); err != nil {
			return
		}
		n, err = out.Read(buf)
		if err != nil && err != io.EOF {
			return
		}
		if n == 0 {
			return
		}
	}
}
