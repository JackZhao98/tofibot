//go:build linux

package guest

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDesktopStreamArgsCursorPolicy(t *testing.T) {
	visible := strings.Join(desktopStreamArgs(":99"), " ")
	hidden := strings.Join(desktopStreamArgs(":99", true), " ")
	if strings.Contains(visible, "-draw_mouse") {
		t.Fatal("default stream must preserve the remote cursor")
	}
	if !strings.Contains(hidden, "-draw_mouse 0") {
		t.Fatal("hidden stream must disable the burned-in cursor")
	}
	if strings.Index(hidden, "-draw_mouse 0") > strings.Index(hidden, "-i :99") {
		t.Fatal("draw_mouse must apply to the x11grab input")
	}
}

// Run explicitly inside the built Linux image. This verifies the real encoder
// and decoder, including 30 seconds of a static screen before visible changes.
func TestDesktopVideoRealEncoderLifecycle(t *testing.T) {
	if os.Getenv("TOFI_REAL_STREAM_TEST") != "1" {
		t.Skip("requires isolated Linux image with real Xvfb/FFmpeg")
	}
	for _, tool := range []string{"Xvfb", "ffmpeg", "ffprobe", "fbsetroot"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	display := ":298"
	log := &limitedBuffer{limit: 8192}
	xvfb, err := startDesktopProcess("Xvfb", []string{display, "-screen", "0", "1280x800x24", "-nolisten", "tcp", "-noreset"}, cleanEnv(s.root, s.root), log)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if xvfb.ProcessState == nil {
			_ = stopProcess(xvfb)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err = s.waitForTestDisplay(ctx, display, log); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	close(ready)
	old := time.Now()
	d := &desktop{botID: testBot, display: display, ready: ready, readyClosed: true, lastActivity: old, xvfb: xvfb, stopDone: make(chan struct{})}
	s.mu.Lock()
	s.desktops[testBot] = d
	s.mu.Unlock()
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	url := server.URL + "/v1/desktop/stream?bot_id=" + testBot
	streamCtx, stopStream := context.WithCancel(ctx)
	req, _ := http.NewRequestWithContext(streamCtx, "GET", url, nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		b, _ := io.ReadAll(response.Body)
		t.Fatalf("stream=%d %s", response.StatusCode, b)
	}
	busy, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	busy.Body.Close()
	if busy.StatusCode != 409 {
		t.Fatal("second viewer accepted")
	}
	var captured bytes.Buffer
	done := make(chan struct{})
	var fragmentArrival []time.Time
	var captureErr error
	go func() {
		defer close(done)
		// Observe complete mdat boxes at the HTTP consumer, not ffmpeg's
		// requested flags. Small fragments must actually leave the encoder.
		reader := io.LimitReader(response.Body, 16<<20)
		for {
			var header [8]byte
			if _, captureErr = io.ReadFull(reader, header[:]); captureErr != nil {
				return
			}
			size := int64(binary.BigEndian.Uint32(header[:4]))
			if size < 8 || size > 4<<20 {
				captureErr = fmt.Errorf("invalid video box size %d", size)
				return
			}
			captured.Write(header[:])
			if _, captureErr = io.CopyN(&captured, reader, size-8); captureErr != nil {
				return
			}
			if string(header[4:]) == "mdat" {
				fragmentArrival = append(fragmentArrival, time.Now())
			}
		}
	}()
	time.Sleep(30 * time.Second)
	for _, color := range []string{"red", "green", "blue"} {
		cmd := exec.CommandContext(ctx, "fbsetroot", "-display", display, "-solid", color)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("visible change %s %v", output, err)
		}
		time.Sleep(time.Second)
	}
	stopStream()
	response.Body.Close()
	<-done
	if len(fragmentArrival) < 250 {
		t.Fatalf("too few complete fragments: %d (read error %v)", len(fragmentArrival), captureErr)
	}
	intervals := make([]time.Duration, 0, len(fragmentArrival)-1)
	for i := 1; i < len(fragmentArrival); i++ {
		intervals = append(intervals, fragmentArrival[i].Sub(fragmentArrival[i-1]))
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i] < intervals[j] })
	median, p95 := intervals[len(intervals)/2], intervals[len(intervals)*95/100]
	if median > 175*time.Millisecond {
		t.Fatalf("fragments still buffered too long: median=%s p95=%s", median, p95)
	}
	t.Logf("HTTP complete-fragment cadence: count=%d median=%s p95=%s", len(fragmentArrival), median, p95)
	waitForStreamRelease(t, s, d)
	s.mu.Lock()
	if !d.lastActivity.Equal(old) || d.inFlight != 0 {
		t.Error("video renewed desktop activity")
	}
	s.mu.Unlock()
	raw := captured.Bytes()
	// Cancellation may truncate the last box. Retain complete ISO BMFF boxes;
	// decoding all complete fragments must remain valid after a static period.
	end := 0
	for end+8 <= len(raw) {
		size := int(binary.BigEndian.Uint32(raw[end : end+4]))
		if size < 8 || end+size > len(raw) {
			break
		}
		end += size
	}
	if end < 1000 {
		t.Fatalf("not enough encoded video: %d bytes", end)
	}
	file := filepath.Join(t.TempDir(), "desktop.mp4")
	if err = os.WriteFile(file, raw[:end], 0600); err != nil {
		t.Fatal(err)
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height,level,nb_read_frames,avg_frame_rate", "-of", "json", file).CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v %s", err, probe)
	}
	var metadata struct {
		Streams []struct {
			Codec  string `json:"codec_name"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Level  int    `json:"level"`
			Frames string `json:"nb_read_frames"`
			Rate   string `json:"avg_frame_rate"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(probe, &metadata); err != nil || len(metadata.Streams) != 1 {
		t.Fatalf("metadata %s", probe)
	}
	v := metadata.Streams[0]
	if v.Codec != "h264" || v.Width != 1280 || v.Height != 800 || v.Level != 32 || v.Rate != "20/1" {
		t.Fatalf("wrong codec: %s", probe)
	}
	frames, err := strconv.Atoi(v.Frames)
	if err != nil || frames < 600 {
		t.Fatalf("interactive capture dropped frames: %s", probe)
	}
	decode, err := exec.Command("ffmpeg", "-v", "error", "-i", file, "-f", "null", "-").CombinedOutput()
	if err != nil || len(decode) != 0 {
		t.Fatalf("decode %v %s", err, decode)
	}
	t.Logf("real encoded video: %d bytes, metadata=%s", end, probe)
	// Reconnect, then force idle without stopping viewing: the stream must end
	// and the desktop must disappear. No slot remains reserved by the viewer.
	again, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	if again.StatusCode != 200 {
		t.Fatal(again.StatusCode)
	}
	ended := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, again.Body); again.Body.Close(); close(ended) }()
	s.mu.Lock()
	s.idleTimeout = time.Millisecond
	d.lastActivity = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	s.reapIdleDesktops()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("idle did not stop encoder")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		exists := s.desktops[testBot] != nil
		s.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("idle viewer retained desktop slot")
}
func (s *Service) waitForTestDisplay(ctx context.Context, display string, log *limitedBuffer) error {
	return waitForDisplay(ctx, display, s.root, log)
}
func waitForStreamRelease(t *testing.T, s *Service, d *desktop) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		active := d.streamActive
		s.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("encoder reservation survived disconnect")
}

// Run in a disposable --cpus=2 container, never a user desktop. Two animated
// terminals exercise changed text pixels on separate displays and profiles of
// load; this is an encoder budget check, not a browser end-to-end latency claim.
func TestDesktopStreamTwoEncoderBudget(t *testing.T) {
	if os.Getenv("TOFI_REAL_STREAM_TEST") != "1" {
		t.Skip("requires isolated two-vCPU Linux image")
	}
	for _, name := range []string{"Xvfb", "xdpyinfo", "xterm", "ffmpeg"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var encoders []*exec.Cmd
	var logs []*limitedBuffer
	for index := 0; index < 2; index++ {
		display := fmt.Sprintf(":%d", 400+os.Getpid()%10000+index)
		env := append(cleanEnv(root, root), "DISPLAY="+display)
		log := &limitedBuffer{limit: 8192}
		xvfb, err := startDesktopProcess("Xvfb", []string{display, "-screen", "0", "1280x800x24", "-nolisten", "tcp", "-noreset"}, env, log)
		if err != nil {
			t.Fatal(err)
		}
		defer stopProcess(xvfb)
		if err := waitForDisplay(ctx, display, root, log); err != nil {
			t.Fatal(err)
		}
		terminal, err := startDesktopProcess("xterm", []string{"-geometry", "165x48+0+0", "-fa", "Liberation Mono", "-fs", "11", "-e", "bash", "--noprofile", "--norc", "-c", `n=0; while :; do printf '%04d desktop latency fixture: ABCDEFGHIJKLMNOPQRSTUVWXYZ 0123456789 abcdefghijklmnopqrstuvwxyz 0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ\n' "$n"; n=$((n+1)); sleep .05; done`}, env, log)
		if err != nil {
			t.Fatal(err)
		}
		defer stopProcess(terminal)
		args := append([]string{"-progress", "pipe:2", "-nostats"}, desktopStreamArgs(display)...)
		encoder := exec.CommandContext(ctx, "ffmpeg", args...)
		encoder.Env = env
		encoder.Stdout = io.Discard
		progress := &limitedBuffer{limit: 64 << 10}
		encoder.Stderr = progress
		if err := encoder.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if encoder.ProcessState == nil {
				_ = encoder.Process.Kill()
				_ = encoder.Wait()
			}
		}()
		encoders = append(encoders, encoder)
		logs = append(logs, progress)
	}
	started := time.Now()
	time.Sleep(10 * time.Second)
	for _, encoder := range encoders {
		_ = encoder.Process.Signal(os.Interrupt)
	}
	totalCPU := 0.0
	for index, encoder := range encoders {
		_ = encoder.Wait()
		if encoder.ProcessState == nil {
			t.Fatal("encoder did not report resource usage")
		}
		usage := encoder.ProcessState.SysUsage().(*syscall.Rusage)
		cpu := encoder.ProcessState.UserTime() + encoder.ProcessState.SystemTime()
		ratio := cpu.Seconds() / time.Since(started).Seconds()
		totalCPU += ratio
		frames := 0
		for _, line := range strings.Split(logs[index].String(), "\n") {
			if strings.HasPrefix(line, "frame=") {
				if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "frame="))); err == nil {
					frames = n
				}
			}
		}
		t.Logf("encoder %d: frames=%d cpu=%.1f%% of one core maxRSS=%.1fMiB", index, frames, ratio*100, float64(usage.Maxrss)/1024)
		if frames < 180 {
			t.Fatalf("encoder %d cannot sustain interactive cadence: frames=%d log=%s", index, frames, logs[index].String())
		}
		if usage.Maxrss > 180*1024 {
			t.Fatalf("encoder %d exceeded 180MiB budget", index)
		}
	}
	t.Logf("two encoders together: %.1f%% of two-vCPU capacity", totalCPU*50)
	if totalCPU > 1.25 {
		t.Fatalf("encoders consume too much of shared two-vCPU budget: %.1f cores", totalCPU)
	}
}
