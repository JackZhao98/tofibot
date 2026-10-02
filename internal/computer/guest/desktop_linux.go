//go:build linux

package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (s *Service) startDesktop(ctx context.Context, botID string) (map[string]any, error) {
	if !botIDPattern.MatchString(botID) {
		return nil, errors.New("bot_id must be a canonical UUID")
	}
	// Bot directories remain the shell/file boundary. The graphical session
	// deliberately uses a workspace directory so all Bots see the same Chrome
	// window and desktop instead of racing separate processes on one profile.
	if _, err := s.botDir(botID); err != nil {
		return nil, err
	}
	botDir := filepath.Join(s.root, "shared")
	if err := os.MkdirAll(filepath.Join(botDir, "tmp"), 0770); err != nil {
		return nil, fmt.Errorf("create shared desktop workspace: %w", err)
	}
	var err error
	for _, name := range []string{"Xvfb", "fluxbox", "tint2", "google-chrome-stable", "feh", "pcmanfm", "dbus-run-session", "xterm", "setsid"} {
		if _, err := exec.LookPath(name); err != nil {
			return nil, fmt.Errorf("desktop dependency %s is unavailable", name)
		}
	}
	profile := filepath.Join(s.root, "browser", "shared")
	if err := os.MkdirAll(profile, 0700); err != nil {
		return nil, err
	}
	runtimeDir := filepath.Join("/tmp", "tofi-bots", "shared")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var d *desktop
	var startCtx context.Context
	var startCancel context.CancelFunc
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, errors.New("guest service is closed")
		}
		existing := s.sharedDesktop
		if existing != nil && existing.stopping {
			done := existing.stopDone
			s.mu.Unlock()
			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			continue
		}
		if existing != nil {
			ready := existing.ready
			display := existing.display
			s.mu.Unlock()
			if ready != nil {
				select {
				case <-ready:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			s.mu.Lock()
			if !s.bindSharedDesktopAliasLocked(botID, existing) {
				s.mu.Unlock()
				continue
			}
			startErr := existing.startErr
			display = existing.display
			s.mu.Unlock()
			if startErr != nil {
				return nil, startErr
			}
			return map[string]any{"bot_id": botID, "display": display, "profile": profile, "shared": true, "already_running": true}, nil
		}
		if s.sharedDesktop != nil {
			s.mu.Unlock()
			continue
		}
		if s.maxDesktop < 1 {
			s.mu.Unlock()
			return nil, fmt.Errorf("desktop resource limit reached (%d active sessions)", s.maxDesktop)
		}
		displayNumber := s.nextDisplay
		s.nextDisplay++
		// Reserve the slot before starting external processes. This prevents two
		// simultaneous requests from exceeding the configured desktop limit or
		// launching two Chromes for one Bot.
		d = &desktop{botID: sharedDesktopKey, botDir: botDir, shared: true, terminalMarker: "tofi-terminal-shared", display: ":" + strconv.Itoa(displayNumber), ready: make(chan struct{}), stopDone: make(chan struct{}), log: &limitedBuffer{limit: 64 * 1024}, lastActivity: time.Now()}
		startCtx, startCancel = context.WithCancel(ctx)
		d.cancel = startCancel
		s.sharedDesktop = d
		s.desktops[botID] = d
		s.mu.Unlock()
		break
	}

	if err := clearStaleChromeLocks(profile); err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, err
	}
	if err := clearBrowserSessionState(profile); err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, err
	}
	// Chrome's singleton socket/cookie/lock are runtime state. Keeping them
	// under the persistent profile makes a VM restart look like another Chrome
	// instance is still alive and can delay startup until the control timeout.
	// The directory contains no durable Bot data and is safe to recreate.
	if err := resetDesktopRuntimeDir(runtimeDir); err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, err
	}
	d.profile = profile
	d.runtimeDir = runtimeDir
	port, err := freePort()
	if err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, err
	}
	d.remotePort = port
	// Graphical helper processes share the desktop cwd and persistent user
	// temp area; Chrome's singleton/controller runtime remains disposable.
	env := append(cleanEnv(s.root, botDir), "DISPLAY="+d.display)
	env = replaceEnv(env, "TMPDIR", filepath.Join(botDir, "tmp"))
	d.xvfb, err = startDesktopProcess("Xvfb", []string{d.display, "-screen", "0", "1280x800x24", "-nolisten", "tcp", "-noreset"}, env, d.log)
	if err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, desktopStartError("start Xvfb", err, d.log)
	}
	if err := waitForDisplay(startCtx, d.display, s.root, d.log); err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, desktopStartError("wait for Xvfb", err, d.log)
	}
	fluxboxConfig, err := prepareFluxboxConfigForDesktop(botDir, filepath.Join(s.root, "home"), d.display, runtimeDir)
	if err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, desktopStartError("prepare fluxbox config", err, d.log)
	}
	if err := setDesktopWallpaper(startCtx, d.display, s.root, botID, d.log); err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, desktopStartError("set desktop wallpaper", err, d.log)
	}
	d.fluxbox, err = startDesktopProcess("fluxbox", []string{"-display", d.display, "-rc", fluxboxConfig, "-no-slit"}, replaceEnv(env, "HOME", botDir), d.log)
	if err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, desktopStartError("start fluxbox", err, d.log)
	}
	launcherPanelConfig := filepath.Join(filepath.Dir(fluxboxConfig), "tint2-launcher.rc")
	d.launcherPanel, err = startDesktopProcess("tint2", []string{"-c", launcherPanelConfig}, env, d.log)
	if err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, desktopStartError("start tint2 launcher dock", err, d.log)
	}
	if err := shapeDesktopDock(startCtx, env, d.launcherPanel.Process.Pid); err != nil {
		// A cosmetic fallback must not prevent access to the computer.
		fmt.Fprintf(d.log, "dock corner clipping unavailable: %v\n", err)
	}
	d.chromeLog = &limitedBuffer{limit: 16 * 1024, tail: true}
	if err := startChromeWithReadiness(startCtx, d, env); err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, chromeStartError("start google-chrome-stable", err)
	}
	if err := startChromeController(d, env); err != nil {
		s.abortDesktopStart(ctx, botID, d, err)
		return nil, desktopStartError("start Chrome control", err, d.log)
	}
	s.mu.Lock()
	d.cancel = nil
	s.mu.Unlock()
	startCancel()
	s.finishDesktop(botID, d, nil)
	return map[string]any{"bot_id": botID, "display": d.display, "profile": profile, "shared": true}, nil
}

// bindSharedDesktopAliasLocked is the only path that adds a logical Bot
// alias after an asynchronous shared-session start. Keeping the pointer and
// stopping state check under Service.mu prevents a stop/restart from racing a
// map write for an obsolete desktop.
func (s *Service) bindSharedDesktopAliasLocked(botID string, d *desktop) bool {
	if d == nil || s.sharedDesktop != d || d.stopping {
		return false
	}
	s.desktops[botID] = d
	return true
}

func chromeStartArgs(profile, display string, remotePort int, workarea ...int) []string {
	x, y, width, height := 0, 0, 1280, 720
	if len(workarea) == 4 && workarea[2] > 0 && workarea[3] > 0 {
		x, y, width, height = workarea[0], workarea[1], workarea[2], workarea[3]
	}
	marginX, marginY := min(32, max(8, width/40)), min(24, max(8, height/30))
	return []string{
		"--user-data-dir=" + profile,
		"--display=" + display,
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(remotePort),
		"--no-first-run", "--no-default-browser-check", "--disable-gpu",
		// Bound disposable caches while preserving profile cookies and login state.
		"--disk-cache-size=134217728", "--media-cache-size=33554432",
		"--disable-session-crashed-bubble", "--disable-restore-session-state",
		fmt.Sprintf("--window-position=%d,%d", x+marginX, y+marginY),
		fmt.Sprintf("--window-size=%d,%d", max(1, width-2*marginX), max(1, height-2*marginY)),
		"--new-window", "about:blank",
	}
}

// Query only when starting a new browser process, never during navigation or
// frame refresh. The dock publishes the WM workarea; a bounded fallback keeps
// its 64px height and 16px bottom margin clear if the WM is not ready yet.
func chromeDesktopWorkarea(ctx context.Context, env []string) []int {
	width, height := 1280, 800
	probe := func(name string, args ...string) []byte {
		probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(probeCtx, name, args...)
		cmd.Env = env
		out, _ := cmd.Output()
		return out
	}
	for _, line := range strings.Split(string(probe("xdpyinfo")), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "dimensions:" {
			var w, h int
			if _, err := fmt.Sscanf(fields[1], "%dx%d", &w, &h); err == nil && w > 0 && h > 0 {
				width, height = w, h
			}
		}
	}
	fallback := []int{0, 0, width, max(1, height-80)}
	_, raw, ok := strings.Cut(string(probe("xprop", "-root", "_NET_WORKAREA")), "=")
	if !ok {
		return fallback
	}
	fields := strings.Split(raw, ",")
	if len(fields) < 4 {
		return fallback
	}
	area := make([]int, 4)
	for i := range area {
		n, err := strconv.Atoi(strings.TrimSpace(fields[i]))
		if err != nil {
			return fallback
		}
		area[i] = n
	}
	if area[0] < 0 || area[1] < 0 || area[2] <= 0 || area[3] <= 0 || area[0]+area[2] > width || area[1]+area[3] > height {
		return fallback
	}
	// Also protect against an early, full-screen WM area before the dock strut.
	area[3] = min(area[3], max(1, height-80-area[1]))
	return area
}

func waitForDisplay(ctx context.Context, display, root string, log *limitedBuffer) error {
	if _, err := exec.LookPath("xdpyinfo"); err != nil {
		return errors.New("desktop dependency xdpyinfo is unavailable")
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		probe := exec.CommandContext(probeCtx, "xdpyinfo", "-display", display)
		probe.Env = append(cleanEnv(root, filepath.Join(root, "home")), "DISPLAY="+display)
		// xdpyinfo can emit a large extension/property dump on stdout. Keep
		// diagnostics bounded by retaining only stderr in the shared startup log.
		probe.Stdout = io.Discard
		probe.Stderr = log
		err := probe.Run()
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Xvfb did not become ready")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func clearStaleChromeLocks(profile string) error {
	return clearStaleChromeLocksFrom("/proc", profile)
}

// clearBrowserSessionState removes only Chrome's restart/session artifacts.
// The user profile itself remains in place, including Cookies, Login Data,
// Local Storage and extensions. This is required because Chrome can honor a
// user's restore_on_startup preference even when launched with about:blank.
func clearBrowserSessionState(profile string) error {
	defaultDir := filepath.Join(profile, "Default")
	sessionsDir := filepath.Join(defaultDir, "Sessions")
	info, err := os.Lstat(sessionsDir)
	if os.IsNotExist(err) {
		info = nil
	} else if err != nil {
		return fmt.Errorf("inspect Chrome session directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Chrome session path is not a directory")
	}
	if info != nil {
		entries, err := os.ReadDir(sessionsDir)
		if err != nil {
			return fmt.Errorf("read Chrome session directory: %w", err)
		}
		for _, entry := range entries {
			if err := os.Remove(filepath.Join(sessionsDir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove Chrome session artifact %s: %w", entry.Name(), err)
			}
		}
	}
	for _, name := range []string{"Current Session", "Current Tabs", "Last Session", "Last Tabs"} {
		path := filepath.Join(defaultDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Chrome session artifact %s: %w", name, err)
		}
	}
	return nil
}

func clearStaleChromeLocksFrom(procRoot, profile string) error {
	if pid, err := activeChromeForProfile(procRoot, profile); err != nil {
		return err
	} else if pid != 0 {
		return fmt.Errorf("Chrome is already running for profile %s (pid %d)", profile, pid)
	}
	for _, name := range []string{"SingletonCookie", "SingletonLock", "SingletonSocket"} {
		path := filepath.Join(profile, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect stale Chrome %s: %w", name, err)
		}
		// Chrome writes these singleton markers as symlinks. Only remove the
		// marker itself; a regular file with one of these names is preserved so
		// an unexpectedly live profile cannot cause data loss.
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale Chrome %s: %w", name, err)
		}
	}
	return nil
}

// activeChromeForProfile returns a matching Chrome PID. It deliberately
// checks /proc/<pid>/exe before inspecting argv: a shell, grep, or diagnostic
// command can contain the profile string without owning the profile.
func activeChromeForProfile(procRoot, profile string) (int, error) {
	profile, err := filepath.Abs(profile)
	if err != nil {
		return 0, fmt.Errorf("resolve Chrome profile: %w", err)
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, fmt.Errorf("read process table: %w", err)
	}
	flag := "--user-data-dir=" + filepath.Clean(profile)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		pidDir := filepath.Join(procRoot, entry.Name())
		exe, err := os.Readlink(filepath.Join(pidDir, "exe"))
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		name := strings.ToLower(filepath.Base(exe))
		if name != "chrome" && name != "google-chrome" && name != "google-chrome-stable" {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(pidDir, "cmdline"))
		if err != nil {
			continue
		}
		for _, arg := range bytes.Split(cmdline, []byte{0}) {
			if string(arg) == flag {
				return pid, nil
			}
		}
	}
	return 0, nil
}

func resetDesktopRuntimeDir(runtimeDir string) error {
	base := filepath.Dir(runtimeDir)
	info, err := os.Lstat(base)
	if os.IsNotExist(err) {
		if err := os.Mkdir(base, 0700); err != nil {
			return fmt.Errorf("create desktop runtime base: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect desktop runtime base: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("desktop runtime base is not a directory")
	}
	if err := os.RemoveAll(runtimeDir); err != nil {
		return fmt.Errorf("reset desktop runtime directory: %w", err)
	}
	if err := os.Mkdir(runtimeDir, 0700); err != nil {
		return fmt.Errorf("create desktop runtime directory: %w", err)
	}
	return nil
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

const desktopWallpaperPath = "/usr/share/backgrounds/tofi.png"

func setDesktopWallpaper(ctx context.Context, display, root, botID string, log *limitedBuffer) error {
	if _, err := os.Stat(desktopWallpaperPath); err != nil {
		return fmt.Errorf("desktop wallpaper %s is unavailable: %w", desktopWallpaperPath, err)
	}
	if _, err := exec.LookPath("feh"); err != nil {
		return errors.New("desktop dependency feh is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wallpaperCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(wallpaperCtx, "feh", "--no-fehbg", "--bg-fill", desktopWallpaperPath)
	cmd.Env = append(cleanEnv(root, filepath.Join(root, "bots", botID)), "DISPLAY="+display)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = log
	cmd.WaitDelay = 1500 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		if wallpaperCtx.Err() != nil {
			return fmt.Errorf("feh wallpaper timed out: %w", wallpaperCtx.Err())
		}
		return err
	}
	return nil
}

func (s *Service) finishDesktop(botID string, want *desktop, startErr error) {
	s.mu.Lock()
	if startErr != nil && want.cancel != nil {
		want.cancel()
		want.cancel = nil
	}
	want.startErr = startErr
	if startErr == nil {
		want.lastActivity = time.Now()
	}
	if !want.readyClosed {
		close(want.ready)
		want.readyClosed = true
	}
	s.mu.Unlock()
}

// abortDesktopStart publishes the failed start before stopping processes. A
// concurrent stop/Close can then wait for ready and share stopOnce instead of
// racing cmd.Wait with the starter goroutine.
func (s *Service) abortDesktopStart(ctx context.Context, botID string, d *desktop, startErr error) {
	s.finishDesktop(botID, d, startErr)
	cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = s.stopDesktopInstance(cleanup, d)
}

func startDesktopProcess(name string, args, env []string, output *limitedBuffer) (*exec.Cmd, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stdout = output
	cmd.Stderr = output
	// os/exec uses pipes when Stdout/Stderr are Go Writers. Chrome and
	// Fluxbox can leave grandchildren holding those pipes after the parent
	// exits; WaitDelay bounds the pipe-copy phase so shutdown never hangs.
	cmd.WaitDelay = 1500 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func prepareFluxboxConfig(botDir string) (string, error) {
	root := filepath.Dir(filepath.Dir(botDir))
	botID := filepath.Base(botDir)
	return prepareFluxboxConfigForDesktop(botDir, filepath.Join(root, "home"), "", filepath.Join("/tmp", "tofi-bots", botID))
}

func prepareFluxboxConfigForDesktop(botDir, sharedHome, display, runtimeDir string) (string, error) {
	// Fluxbox checks HOME/.fluxbox before honoring -rc. If that directory
	// is absent it copies the distribution init over the explicitly supplied
	// rc file. Give the window manager its own pre-created Bot config home.
	configDir := filepath.Join(botDir, ".fluxbox")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(configDir, "init")
	overlay := filepath.Join(configDir, "overlay")
	// Fluxbox's documented 'unset' overlay disables fbsetbg entirely. An
	// empty rootCommand does not override the distribution style's wallpaper.
	if err := os.WriteFile(overlay, []byte("background: unset\n"), 0600); err != nil {
		return "", err
	}
	filesLauncher := filepath.Join(configDir, "launch-files.sh")
	chromeLauncher := filepath.Join(configDir, "launch-chrome.sh")
	terminalLauncher := filepath.Join(configDir, "launch-terminal.sh")
	if err := writeGuestDockIcons(configDir); err != nil {
		return "", err
	}
	if err := writeDesktopLaunchers(filesLauncher, chromeLauncher, terminalLauncher, botDir, sharedHome, display, runtimeDir, configDir); err != nil {
		return "", err
	}
	menu := filepath.Join(configDir, "menu")
	menuText := "[begin] (Tofi)\n" +
		"  [exec] (Files) {" + shellQuote(filesLauncher) + "}\n" +
		"  [exec] (Chrome) {" + shellQuote(chromeLauncher) + "}\n" +
		"  [exec] (Terminal) {" + shellQuote(terminalLauncher) + "}\n" +
		"[end]\n"
	if err := os.WriteFile(menu, []byte(menuText), 0600); err != nil {
		return "", err
	}
	// Keep the guest desktop deterministic and avoid the stock init's
	// fbsetbg/xmessage wallpaper helper, which can outlive Fluxbox and hold
	// inherited output descriptors during failed starts.
	config := "session.configVersion: 13\n" + "session.styleOverlay: " + overlay + "\n" +
		"session.styleFile: /usr/share/fluxbox/styles/ubuntu-light\n" +
		"session.menuFile: " + menu + "\n" +
		"session.screen0.toolbar.visible: false\n" +
		"session.screen0.slit.enabled: false\n" +
		"session.screen0.rootCommand: true\n"
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		return "", err
	}
	if err := writeTint2Configs(configDir, filesLauncher, chromeLauncher, terminalLauncher); err != nil {
		return "", err
	}
	return path, nil
}

func writeTint2Configs(configDir, filesLauncher, chromeLauncher, terminalLauncher string) error {
	dockConfig := "rounded = 18\nborder_width = 1\nbackground_color = #f6f3ef 100\nborder_color = #ffffff 100\n" +
		"panel_items = L\n" +
		"panel_position = bottom center horizontal\n" +
		// 3 x 32px icons + 2 x 16px gaps + 20px padding on each side.
		"panel_size = 168 64\n" +
		"panel_margin = 0 16\n" +
		"panel_padding = 20 12 16\n" +
		"panel_layer = top\n" +
		"panel_background_id = 1\n" +
		"strut_policy = follow_size\n" +
		"launcher_icon_size = 32\n" +
		"launcher_padding = 0 0 16\n" +
		"launcher_background_id = 0\n" +
		"mouse_effects = 1\n" +
		"tooltip = 1\n" +
		"launcher_icon_theme = Adwaita\n" +
		"launcher_item_app = " + filesLauncher + ".desktop\n" +
		"launcher_item_app = " + chromeLauncher + ".desktop\n" +
		"launcher_item_app = " + terminalLauncher + ".desktop\n"
	return os.WriteFile(filepath.Join(configDir, "tint2-launcher.rc"), []byte(dockConfig), 0600)
}

func writeGuestDockIcons(configDir string) error {
	icons := map[string]string{
		"files.svg":    `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="#292929" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2.8 7c0-2.4 1.2-3.5 3.6-3.5H9c1.1 0 1.6.4 2.2 1.2l.5.6c.5.6 1 .9 1.9.9h4c2.6 0 3.6 1.2 3.6 3.8v6.8c0 2.5-1.1 3.6-3.7 3.6H6.5c-2.6 0-3.7-1.1-3.7-3.6V7Z"/><path d="M3.2 9h17.6"/></svg>`,
		"chrome.svg":   `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="#292929" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6.5 3.8h11.000000000000002a3.8 3.8 0 0 1 3.8 3.8v8.799999999999999a3.8 3.8 0 0 1 -3.8 3.8h-11.000000000000002a3.8 3.8 0 0 1 -3.8 -3.8v-8.799999999999999a3.8 3.8 0 0 1 3.8 -3.8Z"/><path d="M3 8.7h18"/><circle cx="6" cy="6.3" r="0.6" fill="#292929" stroke="none"/><circle cx="8.5" cy="6.3" r="0.6" fill="#292929" stroke="none"/><circle cx="11" cy="6.3" r="0.6" fill="#292929" stroke="none"/></svg>`,
		"terminal.svg": `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="#292929" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6.4 4h11.200000000000001a3.7 3.7 0 0 1 3.7 3.7v8.6a3.7 3.7 0 0 1 -3.7 3.7h-11.200000000000001a3.7 3.7 0 0 1 -3.7 -3.7v-8.6a3.7 3.7 0 0 1 3.7 -3.7Z"/><path d="m6.5 8.8 3.2 3.2-3.2 3.2M13 15.2h4.5"/></svg>`,
	}
	for name, svg := range icons {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(svg), 0600); err != nil {
			return err
		}
	}
	return nil
}

func writeDesktopLaunchers(filesPath, chromePath, terminalPath, botDir, sharedHome, display, runtimeDir, iconDir string) error {
	profileName := "tofi-" + filepath.Base(botDir)
	roleName := "tofi-files-" + filepath.Base(botDir)
	files := "#!/bin/sh\nset -eu\n" +
		"export HOME=" + shellQuote(sharedHome) + "\n" +
		"export DISPLAY=" + shellQuote(display) + "\n" +
		"export XDG_RUNTIME_DIR=" + shellQuote(runtimeDir) + "\n" +
		"if command -v xdotool >/dev/null 2>&1; then\n" +
		"  id=$(xdotool search --class pcmanfm 2>/dev/null | tail -n 1 || true)\n" +
		"  if [ -n \"$id\" ]; then xdotool windowmap \"$id\" >/dev/null 2>&1 || true; xdotool windowactivate --sync \"$id\" >/dev/null 2>&1 || true; exit 0; fi\n" +
		"fi\n" +
		"exec dbus-run-session -- pcmanfm --no-desktop --new-win --profile=" + shellQuote(profileName) +
		" --role=" + shellQuote(roleName) + " --display=" + shellQuote(display) +
		" " + shellQuote(botDir) + "\n"
	if err := writeExecutable(filesPath, []byte(files)); err != nil {
		return err
	}
	if err := writeDesktopEntry(filesPath+".desktop", "Files", filesPath, filepath.Join(iconDir, "files.svg")); err != nil {
		return err
	}
	chrome := "#!/bin/sh\nset -eu\n" +
		"exec python3 -c " + shellQuote(chromeControlClientScript) + " " + shellQuote(filepath.Join(runtimeDir, "chrome-control.sock")) + "\n"

	if err := writeExecutable(chromePath, []byte(chrome)); err != nil {
		return err
	}
	if err := writeDesktopEntry(chromePath+".desktop", "Chrome", chromePath, filepath.Join(iconDir, "chrome.svg")); err != nil {
		return err
	}
	terminal := "#!/bin/sh\nset -eu\n" +
		"export HOME=" + shellQuote(sharedHome) + "\n" +
		"export DISPLAY=" + shellQuote(display) + "\n" +
		"export TOFI_TERMINAL_ID=" + shellQuote("tofi-terminal-"+filepath.Base(botDir)) + "\n" +
		"if command -v xdotool >/dev/null 2>&1; then\n" +
		"  id=$(xdotool search --name '^Tofi Terminal$' 2>/dev/null | tail -n 1 || true)\n" +
		"  if [ -n \"$id\" ]; then xdotool windowmap \"$id\" >/dev/null 2>&1 || true; xdotool windowactivate --sync \"$id\" >/dev/null 2>&1 || true; exit 0; fi\n" +
		"fi\n" +
		"exec xterm -title " + shellQuote("Tofi Terminal") +
		" -fa " + shellQuote("Liberation Mono") + " -fs 12 -geometry 92x26" +
		" -xrm " + shellQuote("XTerm*VT100.translations: #override Ctrl Shift <Key>V: insert-selection(CLIPBOARD)\n Ctrl Shift <Key>C: copy-selection(CLIPBOARD)") +
		" -e " + shellQuote(filepath.Join(filepath.Dir(terminalPath), "terminal-shell.sh")) + "\n"
	if err := writeExecutable(terminalPath, []byte(terminal)); err != nil {
		return err
	}
	terminalShell := "#!/bin/bash\nset -eu\n" +
		"export HOME=" + shellQuote(sharedHome) + "\n" +
		"export DISPLAY=" + shellQuote(display) + "\n" +
		"export PWD=" + shellQuote(botDir) + "\n" +
		"cd " + shellQuote(botDir) + "\n" +
		"pidfile=" + shellQuote(runtimeDir) + "/terminal-shell-$$.pid\n" +
		"cleanup() { rm -f \"$pidfile\"; }\n" +
		"trap cleanup EXIT HUP INT TERM\n" +
		"pgid=$(ps -o pgid= -p \"$$\" | tr -d ' ')\n" +
		"sid=$(ps -o sid= -p \"$$\" | tr -d ' ')\n" +
		"start=$(awk '{print $22}' /proc/$$/stat)\n" +
		"printf '%s %s %s %s %s %s\\n' \"$$\" \"$pgid\" \"$sid\" \"$start\" \"$DISPLAY\" \"$TOFI_TERMINAL_ID\" > \"$pidfile\"\n" +
		"exec /bin/bash --noprofile --norc\n"
	if err := writeExecutable(filepath.Join(filepath.Dir(terminalPath), "terminal-shell.sh"), []byte(terminalShell)); err != nil {
		return err
	}
	return writeDesktopEntry(terminalPath+".desktop", "Terminal", terminalPath, filepath.Join(iconDir, "terminal.svg"))
}

func writeDesktopEntry(path, name, executable, icon string) error {
	entry := "[Desktop Entry]\nType=Application\nName=" + name + "\nExec=" + executable +
		"\nIcon=" + icon + "\nTerminal=false\nCategories=Utility;\n"
	return os.WriteFile(path, []byte(entry), 0600)
}

func writeExecutable(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0700); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func desktopStartError(prefix string, err error, log *limitedBuffer) error {
	detail := strings.TrimSpace(log.String())
	if detail != "" {
		return fmt.Errorf("%s: %w: %s", prefix, err, detail)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func chromeStartError(prefix string, err error) error {
	return fmt.Errorf("%s: %w", prefix, err)
}

func stopDesktopProcesses(ctx context.Context, d *desktop) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var first error
	if err := stopChromeProcess(ctx, d); err != nil && first == nil {
		first = fmt.Errorf("stop Chrome: %w", err)
	}
	// The panels own no persistent state; stop them before the WM/X server so
	// their launcher/taskbar children cannot remain attached to this display.
	for _, cmd := range []*exec.Cmd{d.panel, d.clockPanel, d.launcherPanel} {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if err := stopProcess(cmd); err != nil && first == nil {
			first = fmt.Errorf("stop %s: %w", filepath.Base(cmd.Path), err)
		}
	}
	// Launcher commands fork into their own session. Stop recorded xterm
	// groups after the panel exits, before tearing down the display.
	if err := stopTerminalProcesses(ctx, d); err != nil && first == nil {
		first = err
	}
	// Fluxbox can abort while releasing X11 resources after the stop signal.
	// This exception is intentionally scoped to Fluxbox; Chrome keeps the
	// strict signal policy so a crash is never reported as a clean profile
	// flush.
	if d.fluxbox != nil && d.fluxbox.Process != nil {
		if err := stopProcessAllowAbort(d.fluxbox); err != nil && first == nil {
			first = fmt.Errorf("stop %s: %w", filepath.Base(d.fluxbox.Path), err)
		}
	}
	if d.xvfb != nil && d.xvfb.Process != nil {
		if err := stopProcess(d.xvfb); err != nil && first == nil {
			first = fmt.Errorf("stop %s: %w", filepath.Base(d.xvfb.Path), err)
		}
	}
	// Close the small setsid-to-pidfile race and clean up any terminal which
	// was already attached while Xvfb was being stopped.
	if err := stopTerminalProcesses(ctx, d); err != nil && first == nil {
		first = err
	}
	return first
}

// Terminal launchers create a short-lived pid file in the Bot's disposable
// desktop runtime directory. The record contains the PTY shell PID, process
// group, session ID, start time, DISPLAY and a per-desktop marker. Kill only
// that verified session; this keeps an
// interactive shell started by this desktop from leaking after an idle stop
// without affecting another Bot's or the guest agent's shells.
type terminalRecord struct {
	pid     int
	pgid    int
	sid     int
	start   uint64
	display string
	marker  string
}

func stopTerminalProcesses(ctx context.Context, d *desktop) error {
	if d == nil || d.runtimeDir == "" {
		return nil
	}
	entries, err := os.ReadDir(d.runtimeDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect terminal processes: %w", err)
	}
	var first error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "terminal-") || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		path := filepath.Join(d.runtimeDir, entry.Name())
		data, readErr := os.ReadFile(path)
		record, parseErr := parseTerminalRecord(string(data))
		if readErr != nil || parseErr != nil || record.pid <= 1 || record.pgid <= 1 || record.sid <= 1 {
			continue
		}
		if terminalSessionOwned(record, d.display, d.terminalMarker) {
			if killErr := killTerminalSession(ctx, record); killErr != nil {
				// Keep the record when cleanup is incomplete. The next desktop
				// stop can retry after a transient process-table or signal error.
				if first == nil {
					first = fmt.Errorf("stop terminal session %d: process did not exit: %w", record.sid, killErr)
				}
				continue
			}
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) && first == nil {
				first = fmt.Errorf("remove terminal session record: %w", removeErr)
			}
		}
	}
	return first
}

func parseTerminalRecord(raw string) (terminalRecord, error) {
	fields := strings.Fields(raw)
	if len(fields) != 6 {
		return terminalRecord{}, errors.New("invalid terminal pid record")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 1 {
		return terminalRecord{}, errors.New("invalid terminal pid")
	}
	record := terminalRecord{pid: pid, pgid: pid}
	if len(fields) >= 2 {
		record.pgid, err = strconv.Atoi(fields[1])
		if err != nil || record.pgid <= 1 {
			return terminalRecord{}, errors.New("invalid terminal process group")
		}
	}
	if len(fields) >= 3 {
		record.sid, err = strconv.Atoi(fields[2])
		if err != nil || record.sid <= 1 {
			return terminalRecord{}, errors.New("invalid terminal session")
		}
	}
	start, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil || start == 0 {
		return terminalRecord{}, errors.New("invalid terminal start time")
	}
	record.start = start
	record.display = fields[4]
	record.marker = fields[5]
	if record.display == "" {
		return terminalRecord{}, errors.New("terminal display is missing")
	}
	return record, nil
}

func terminalSessionOwned(record terminalRecord, expectedDisplay, expectedMarker string) bool {
	if record.display == "" || record.display != expectedDisplay || record.marker == "" || record.marker != expectedMarker || record.pgid != record.sid {
		return false
	}
	leader, err := readProcStat(record.pid)
	if err == nil && leader.state != 'Z' {
		// A live leader must still be the Bot terminal shell and retain the
		// recorded session/group. This rejects PID reuse safely. xterm itself
		// creates a new PTY session, so the helper records this shell rather than
		// the outer xterm PID.
		return leader.start == record.start && leader.pgrp == record.pgid && leader.session == record.sid &&
			processEnvValue(record.pid, "DISPLAY") == record.display && processEnvValue(record.pid, "TOFI_TERMINAL_ID") == record.marker
	}
	// A leader may exit while a shell job remains in the same session. In that
	// case require the expected DISPLAY and per-desktop marker before using the
	// stale record to clean the session.
	for _, pid := range procPIDs() {
		stat, statErr := readProcStat(pid)
		if statErr != nil || stat.state == 'Z' || stat.session != record.sid {
			continue
		}
		if processEnvValue(pid, "DISPLAY") == record.display && processEnvValue(pid, "TOFI_TERMINAL_ID") == record.marker {
			return true
		}
	}
	return false
}

type procStat struct {
	state   byte
	ppid    int
	pgrp    int
	session int
	start   uint64
}

func readProcStat(pid int) (procStat, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return procStat{}, err
	}
	closeName := bytes.LastIndexByte(data, ')')
	if closeName < 0 || closeName+2 >= len(data) {
		return procStat{}, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data[closeName+2:]))
	if len(fields) < 20 {
		return procStat{}, errors.New("short process stat")
	}
	if fields[0] == "" {
		return procStat{}, errors.New("invalid process state")
	}
	ppid, err1 := strconv.Atoi(fields[1])
	pgrp, err2 := strconv.Atoi(fields[2])
	session, err3 := strconv.Atoi(fields[3])
	start, err4 := strconv.ParseUint(fields[19], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return procStat{}, errors.New("invalid process stat fields")
	}
	return procStat{state: fields[0][0], ppid: ppid, pgrp: pgrp, session: session, start: start}, nil
}

func procPIDs() []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	result := make([]int, 0, len(entries))
	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry.Name()); err == nil && pid > 1 {
			result = append(result, pid)
		}
	}
	return result
}

func processEnvValue(pid int, key string) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, value := range bytes.Split(data, []byte{0}) {
		if strings.HasPrefix(string(value), prefix) {
			return string(value[len(prefix):])
		}
	}
	return ""
}

func processCWD(pid int) string {
	cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
	if err != nil {
		return ""
	}
	return cwd
}

func killTerminalSession(ctx context.Context, record terminalRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, pid := range sessionPIDs(record.sid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for sessionAlive(record.sid) {
		select {
		case <-ctx.Done():
			killSession(record.sid, syscall.SIGKILL)
			return waitSessionGone(record.sid, 2*time.Second)
		case <-deadline.C:
			killSession(record.sid, syscall.SIGKILL)
			return waitSessionGone(record.sid, 2*time.Second)
		case <-ticker.C:
		}
	}
	return nil
}

func sessionPIDs(sid int) []int {
	var result []int
	for _, pid := range procPIDs() {
		stat, err := readProcStat(pid)
		if err == nil && stat.state != 'Z' && stat.session == sid {
			result = append(result, pid)
		}
	}
	return result
}

func killSession(sid int, signal syscall.Signal) {
	for _, pid := range sessionPIDs(sid) {
		_ = syscall.Kill(pid, signal)
	}
}

func sessionAlive(sid int) bool { return len(sessionPIDs(sid)) > 0 }

func waitSessionGone(sid int, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for sessionAlive(sid) {
		select {
		case <-deadline.C:
			return errors.New("process did not exit")
		case <-ticker.C:
		}
	}
	return nil
}

const chromeCloseScript = `import json,sys
import websocket
ws = websocket.create_connection(sys.argv[1], timeout=2, suppress_origin=True)
try:
    ws.send(json.dumps({"id": 1, "method": "Browser.close"}))
    try:
        ws.recv()
    except Exception:
        pass
finally:
    ws.close()
`

func closeChromeViaCDP(ctx context.Context, d *desktop) error {
	if d == nil || d.remotePort <= 0 {
		return errors.New("browser debugging endpoint is unavailable")
	}
	body, err := chromeHTTP(ctx, d, http.MethodGet, "/json/version")
	if err != nil {
		return err
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return fmt.Errorf("decode browser debugger endpoint: %w", err)
	}
	wsURL, err := url.Parse(strings.TrimSpace(version.WebSocketDebuggerURL))
	if err != nil || (wsURL.Scheme != "ws" && wsURL.Scheme != "wss") || wsURL.Hostname() != "127.0.0.1" || wsURL.Port() != strconv.Itoa(d.remotePort) {
		return errors.New("browser debugger endpoint is not local to this desktop")
	}
	cmd := exec.CommandContext(ctx, "python3", "-c", chromeCloseScript, wsURL.String())
	cmd.Env = []string{"HOME=/tmp", "PATH=/usr/local/bin:/usr/bin:/bin", "PYTHONNOUSERSITE=1"}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Browser.close: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func stopChromeProcess(ctx context.Context, d *desktop) error {
	if managed, err := stopManagedChromeIfPresent(ctx, d); managed {
		return err
	}
	return stopChromeProcessDirect(ctx, d, d.chrome)
}

// stopChromeProcessDirect is used by the managed controller after it has
// closed the control socket and taken its serialization lock.
func stopChromeProcessDirect(ctx context.Context, d *desktop, cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if ctx == nil {
		ctx = context.Background()
	}
	cdpCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	cdpErr := closeChromeViaCDP(cdpCtx, d)
	cancel()
	cdpSucceeded := cdpErr == nil
	if cdpSucceeded {
		// Browser.close may close the websocket before replying. The main
		// Chrome process is still the authority for profile flush completion.
		if err, finished := waitProcess(done, 5*time.Second); finished {
			if err == nil || isTerminated(err) {
				return nil
			}
			if errors.Is(err, exec.ErrWaitDelay) {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				return nil
			}
			return err
		}
	}
	// CDP was unavailable or did not close Chrome in time. Give the browser
	// process a signal chance before escalating to its renderer group.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if err, finished := waitProcess(done, 2*time.Second); finished {
		if err == nil || isTerminated(err) {
			return nil
		}
		if cdpSucceeded && errors.Is(err, exec.ErrWaitDelay) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			return nil
		}
		return err
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err, finished := waitProcess(done, 2*time.Second); finished {
		if err != nil {
			return err
		}
		return nil
	}
	return errors.New("Chrome process did not exit after SIGKILL")
}

func waitProcess(done <-chan error, timeout time.Duration) (error, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err, true
	case <-timer.C:
		return nil, false
	}
}

func isTerminated(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.Signaled() && (status.Signal() == syscall.SIGTERM || status.Signal() == syscall.SIGKILL)
		}
	}
	return strings.Contains(err.Error(), "signal: terminated") || strings.Contains(err.Error(), "signal: killed")
}

func stopProcessAllowAbort(cmd *exec.Cmd) error {
	return stopProcessWithPolicy(cmd, true)
}

func stopProcess(cmd *exec.Cmd) error {
	return stopProcessWithPolicy(cmd, false)
}

func stopProcessWithPolicy(cmd *exec.Cmd, allowAbort bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil && !isTerminated(err) && !(allowAbort && isAborted(err)) {
			return err
		}
		return nil
	case <-timer.C:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		// WaitDelay bounds descriptor-drain, but retain a hard outer bound in
		// case a descendant or a platform exec implementation defeats it.
		select {
		case err := <-done:
			if isTerminated(err) || (allowAbort && isAborted(err)) {
				return nil
			}
			return err
		case <-time.After(2 * time.Second):
			return errors.New("desktop process did not exit after SIGKILL")
		}
	}
}

func isAborted(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGABRT
}

func (s *Service) desktopCapture(ctx context.Context, botID string) (map[string]any, error) {
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("import"); err != nil {
		return nil, errors.New("desktop capture dependency import is unavailable")
	}
	cmd := exec.CommandContext(ctx, "import", "-window", "root", "png:-")
	cmd.Env = append(cleanEnv(s.root, filepath.Join(s.root, "bots", botID)), "DISPLAY="+d.display)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("capture desktop: %w", err)
	}
	if result, pngErr := s.captureResult(d, out); pngErr == nil {
		return result, nil
	}
	// Keep the vision payload below the control-plane limit by progressively
	// reducing the screenshot when a full 1280x800 capture is too large.
	for _, scale := range []string{"70%", "50%", "35%"} {
		resize := exec.CommandContext(ctx, "convert", "png:-", "-resize", scale, "png:-")
		resize.Stdin = bytes.NewReader(out)
		resized, resizeErr := resize.Output()
		if resizeErr != nil {
			break
		}
		if result, pngErr := s.captureResult(d, resized); pngErr == nil {
			return result, nil
		}
	}
	return nil, errors.New("screenshot exceeds 1 MiB result limit")
}

func (s *Service) captureResult(d *desktop, png []byte) (map[string]any, error) {
	result, err := asDataPNG(png)
	if err != nil {
		return nil, err
	}
	width, _ := result["width"].(int)
	height, _ := result["height"].(int)
	s.setCaptureSize(d, width, height)
	return result, nil
}

func (s *Service) desktopClick(ctx context.Context, botID string, x, y, button, screenshotW, screenshotH int) (map[string]any, error) {
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	captureW, captureH := screenshotW, screenshotH
	if captureW == 0 || captureH == 0 {
		captureW, captureH = s.captureSize(d)
	}
	if x < 0 || y < 0 || (captureW > 0 && x > captureW) || (captureH > 0 && y > captureH) {
		return nil, errors.New("desktop.click coordinates are out of range")
	}
	// The UI sends coordinates in the image's naturalWidth/naturalHeight.
	// Captures may be downscaled to fit the vision payload, while Xvfb remains
	// fixed at 1280x800, so map them back to physical display coordinates.
	if captureW > 0 && captureH > 0 {
		x, y = scaleClickCoordinates(x, y, captureW, captureH)
	}
	if button == 0 {
		button = 1
	}
	if button < 1 || button > 5 {
		return nil, errors.New("desktop.click button must be between 1 and 5")
	}
	return runXTool(ctx, d, "mousemove", "--sync", strconv.Itoa(x), strconv.Itoa(y), "click", strconv.Itoa(button))
}

func (s *Service) desktopType(ctx context.Context, botID, text string) (map[string]any, error) {
	if len(text) == 0 || len(text) > MaxDesktopInput {
		return nil, errors.New("desktop.type text must be between 1 and 64 KiB")
	}
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	return runXTool(ctx, d, "type", "--clearmodifiers", "--delay", "1", "--", text)
}

func (s *Service) desktopKey(ctx context.Context, botID, key string, modifiers []string) (map[string]any, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return nil, errors.New("desktop.key key is required")
	}
	d, err := s.currentDesktop(ctx, botID)
	if err != nil {
		return nil, err
	}
	for _, m := range modifiers {
		if strings.TrimSpace(m) == "" || strings.ContainsAny(m, " \t\r\n") {
			return nil, errors.New("desktop.key modifiers must be key names")
		}
	}
	combo := append(append([]string{}, modifiers...), key)
	return runXTool(ctx, d, "key", strings.Join(combo, "+"))
}

func runXTool(ctx context.Context, d *desktop, args ...string) (map[string]any, error) {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return nil, errors.New("desktop input dependency xdotool is unavailable")
	}
	cmd := exec.CommandContext(ctx, "xdotool", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+d.display)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("desktop input: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return map[string]any{"ok": true}, nil
}
