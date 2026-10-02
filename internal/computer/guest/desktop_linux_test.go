//go:build linux

package guest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClearStaleChromeLocksOnlyRemovesSymlinks(t *testing.T) {
	profile := t.TempDir()
	regular := filepath.Join(profile, "SingletonCookie")
	if err := os.WriteFile(regular, []byte("cookie"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-lock-target", filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if err := clearStaleChromeLocks(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(regular); err != nil {
		t.Fatalf("regular singleton marker was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); !os.IsNotExist(err) {
		t.Fatalf("stale singleton symlink remains: %v", err)
	}
}

func TestActiveChromeForProfileRequiresChromeExeAndExactArg(t *testing.T) {
	procRoot := t.TempDir()
	profile := filepath.Join(t.TempDir(), "browser", testBot)
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	makeProc := func(pid, exeName string, cmdline []byte) {
		t.Helper()
		pidDir := filepath.Join(procRoot, pid)
		if err := os.Mkdir(pidDir, 0700); err != nil {
			t.Fatal(err)
		}
		exeTarget := filepath.Join(procRoot, exeName)
		if err := os.WriteFile(exeTarget, nil, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(exeTarget, filepath.Join(pidDir, "exe")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), cmdline, 0600); err != nil {
			t.Fatal(err)
		}
	}
	flag := []byte("--user-data-dir=" + profile + "\x00")
	// The profile string alone must not match a diagnostic/readquery process.
	makeProc("101", "readquery", append([]byte("readquery\x00"), flag...))
	makeProc("202", "chrome", append([]byte("chrome\x00"), flag...))
	pid, err := activeChromeForProfile(procRoot, profile)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 202 {
		t.Fatalf("active Chrome pid=%d, want 202", pid)
	}
}

func TestClearStaleChromeLocksRefusesActiveProfile(t *testing.T) {
	procRoot := t.TempDir()
	profile := filepath.Join(t.TempDir(), "browser", testBot)
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("stale", filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	pidDir := filepath.Join(procRoot, "303")
	if err := os.Mkdir(pidDir, 0700); err != nil {
		t.Fatal(err)
	}
	exeTarget := filepath.Join(procRoot, "google-chrome-stable")
	if err := os.WriteFile(exeTarget, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exeTarget, filepath.Join(pidDir, "exe")); err != nil {
		t.Fatal(err)
	}
	cmdline := []byte("google-chrome-stable\x00--user-data-dir=" + profile + "\x00")
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), cmdline, 0600); err != nil {
		t.Fatal(err)
	}
	if err := clearStaleChromeLocksFrom(procRoot, profile); err == nil {
		t.Fatal("active Chrome profile was not rejected")
	}
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatalf("active profile lock was removed: %v", err)
	}
}

func TestPrepareDesktopConfigProvidesPanelAndAppLaunchers(t *testing.T) {
	root := t.TempDir()
	botDir := filepath.Join(root, "bots", testBot)
	sharedHome := filepath.Join(root, "home")
	runtimeDir := filepath.Join(root, "runtime", testBot)
	config, err := prepareFluxboxConfigForDesktop(botDir, sharedHome, ":199", runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	text := string(configData)
	for _, want := range []string{
		"session.screen0.toolbar.visible: false",
		"session.menuFile:",
		"session.styleFile: /usr/share/fluxbox/styles/ubuntu-light",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Fluxbox config missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "button.files") || strings.Contains(text, "button.chrome") {
		t.Fatalf("unsupported Fluxbox toolbar button was configured: %s", text)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(config), "tint2rc")); !os.IsNotExist(err) {
		t.Fatalf("legacy full-width tint2 panel remains: %v", err)
	}
	dockConfig, err := os.ReadFile(filepath.Join(filepath.Dir(config), "tint2-launcher.rc"))
	if err != nil {
		t.Fatal(err)
	}
	dockText := string(dockConfig)
	for _, want := range []string{
		"panel_items = L",
		"panel_position = bottom center horizontal",
		"panel_size = 168 64",
		"panel_margin = 0 16",
		"strut_policy = follow_size",
		"rounded = 18",
		"background_color = #f6f3ef 100",
		"launcher_icon_size = 32",
		"launcher_background_id = 0",
		"mouse_effects = 1",
		"tooltip = 1",
		"launcher_item_app = " + filepath.Join(filepath.Dir(config), "launch-files.sh") + ".desktop",
		"launcher_item_app = " + filepath.Join(filepath.Dir(config), "launch-chrome.sh") + ".desktop",
		"launcher_item_app = " + filepath.Join(filepath.Dir(config), "launch-terminal.sh") + ".desktop",
	} {
		if !strings.Contains(dockText, want) {
			t.Fatalf("tint2 launcher dock missing %q: %s", want, dockText)
		}
	}
	for _, name := range []string{"files.svg", "chrome.svg", "terminal.svg"} {
		if info, err := os.Stat(filepath.Join(filepath.Dir(config), name)); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("dock icon %s missing or unexpected mode: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(config), "tint2-clock.rc")); !os.IsNotExist(err) {
		t.Fatalf("legacy standalone clock panel remains: %v", err)
	}
	configDir := filepath.Dir(config)
	menu, err := os.ReadFile(filepath.Join(configDir, "menu"))
	if err != nil {
		t.Fatal(err)
	}
	menuText := string(menu)
	for _, want := range []string{"[exec] (Files)", "[exec] (Chrome)", "[exec] (Terminal)", "launch-files.sh", "launch-chrome.sh", "launch-terminal.sh"} {
		if !strings.Contains(menuText, want) {
			t.Fatalf("Fluxbox menu missing %q: %s", want, menuText)
		}
	}
	files, err := os.ReadFile(filepath.Join(configDir, "launch-files.sh"))
	if err != nil {
		t.Fatal(err)
	}
	filesText := string(files)
	for _, want := range []string{
		"export HOME='" + sharedHome + "'",
		"export DISPLAY=':199'",
		"export XDG_RUNTIME_DIR='" + runtimeDir + "'",
		"dbus-run-session -- pcmanfm --no-desktop --new-win --profile='tofi-" + testBot + "'",
		"--display=':199' '" + botDir + "'",
	} {
		if !strings.Contains(filesText, want) {
			t.Fatalf("Files launcher missing %q: %s", want, filesText)
		}
	}
	chrome, err := os.ReadFile(filepath.Join(configDir, "launch-chrome.sh"))
	if err != nil {
		t.Fatal(err)
	}
	chromeText := string(chrome)
	for _, want := range []string{
		"exec python3 -c", "chrome-control.sock", "POST /v1/chrome/reopen",
	} {
		if !strings.Contains(chromeText, want) {
			t.Fatalf("Chrome launcher missing %q: %s", want, chromeText)
		}
	}
	if strings.Contains(chromeText, "exec google-chrome-stable") || strings.HasSuffix(strings.TrimSpace(chromeText), "exit 0") {
		t.Fatalf("Chrome launcher must delegate reopen to the managed controller: %s", chromeText)
	}
	for _, name := range []string{"launch-files.sh", "launch-chrome.sh"} {
		info, err := os.Stat(filepath.Join(configDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("%s mode=%o, want 0700", name, info.Mode().Perm())
		}
	}
	terminal, err := os.ReadFile(filepath.Join(configDir, "launch-terminal.sh"))
	if err != nil {
		t.Fatal(err)
	}
	terminalText := string(terminal)
	for _, want := range []string{
		"export HOME='" + sharedHome + "'",
		"export DISPLAY=':199'",
		"export TOFI_TERMINAL_ID='tofi-terminal-" + testBot + "'",
		"exec xterm -title 'Tofi Terminal' -fa 'Liberation Mono' -fs 12 -geometry 92x26",
		"-e '" + filepath.Join(filepath.Dir(config), "terminal-shell.sh") + "'",
	} {
		if !strings.Contains(terminalText, want) {
			t.Fatalf("Terminal launcher missing %q: %s", want, terminalText)
		}
	}
	shell, err := os.ReadFile(filepath.Join(configDir, "terminal-shell.sh"))
	if err != nil {
		t.Fatal(err)
	}
	shellText := string(shell)
	for _, want := range []string{
		"export HOME='" + sharedHome + "'",
		"export DISPLAY=':199'",
		"export PWD='" + botDir + "'",
		"cd '" + botDir + "'",
		"pidfile='" + runtimeDir + "'/terminal-shell-$$.pid",
		"pgid=$(ps -o pgid= -p \"$$\"",
		"sid=$(ps -o sid= -p \"$$\"",
		"start=$(awk '{print $22}' /proc/$$/stat)",
		"printf '%s %s %s %s %s %s\\n'",
		"exec /bin/bash --noprofile --norc",
	} {
		if !strings.Contains(shellText, want) {
			t.Fatalf("Terminal shell missing %q: %s", want, shellText)
		}
	}
	for _, name := range []string{"launch-files.sh.desktop", "launch-chrome.sh.desktop", "launch-terminal.sh.desktop"} {
		if _, err := os.Stat(filepath.Join(configDir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChromeStartArgsCreateFreshWindowWithoutRestoringTabs(t *testing.T) {
	args := chromeStartArgs("/workspace/browser/"+testBot, ":103", 45678)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--user-data-dir=/workspace/browser/" + testBot,
		"--disable-session-crashed-bubble",
		"--disk-cache-size=134217728",
		"--media-cache-size=33554432",
		"--disable-restore-session-state",
		"--new-window",
		"about:blank",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Chrome args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--restore-last-session") || strings.Contains(joined, "tofi-tabs.json") {
		t.Fatalf("Chrome args retained old URL/session restore path: %s", joined)
	}
}

func TestClearBrowserSessionStatePreservesProfileData(t *testing.T) {
	profile := t.TempDir()
	defaultDir := filepath.Join(profile, "Default")
	sessionsDir := filepath.Join(defaultDir, "Sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Session_1", "Tabs_1"} {
		if err := os.WriteFile(filepath.Join(sessionsDir, name), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Current Session", "Current Tabs", "Last Session", "Last Tabs"} {
		if err := os.WriteFile(filepath.Join(defaultDir, name), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Cookies", "Login Data", "Web Data"} {
		if err := os.WriteFile(filepath.Join(defaultDir, name), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := clearBrowserSessionState(profile); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("session artifacts remain: %#v", entries)
	}
	for _, name := range []string{"Current Session", "Current Tabs", "Last Session", "Last Tabs"} {
		if _, err := os.Stat(filepath.Join(defaultDir, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy session artifact %s remains: %v", name, err)
		}
	}
	for _, name := range []string{"Cookies", "Login Data", "Web Data"} {
		data, err := os.ReadFile(filepath.Join(defaultDir, name))
		if err != nil || string(data) != "preserve" {
			t.Fatalf("profile data %s changed: %q, %v", name, data, err)
		}
	}
}

func TestParseTerminalRecordRequiresProcessGroupAndSessionIdentity(t *testing.T) {
	record, err := parseTerminalRecord("101 101 101 999 :103 tofi-terminal-test\n")
	if err != nil {
		t.Fatal(err)
	}
	if record.pid != 101 || record.pgid != 101 || record.sid != 101 || record.start != 999 || record.display != ":103" || record.marker != "tofi-terminal-test" {
		t.Fatalf("record=%+v", record)
	}
	for _, raw := range []string{"", "101 101 0 999 :103 marker", "101 0 101 999 :103 marker", "101 101 101 999 :103"} {
		if _, err := parseTerminalRecord(raw); err == nil {
			t.Fatalf("record %q unexpectedly accepted", raw)
		}
	}
}

func TestTerminalSessionCleanupUsesDisplayAndKillsXtermSession(t *testing.T) {
	for _, tool := range []string{"Xvfb", "xdpyinfo", "xterm", "setsid"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("guest image required: " + tool)
		}
	}
	root := t.TempDir()
	display := ":221"
	log := &limitedBuffer{limit: 64 * 1024}
	xvfb, err := startDesktopProcess("Xvfb", []string{display, "-screen", "0", "1280x800x24", "-nolisten", "tcp"}, []string{"DISPLAY=" + display}, log)
	if err != nil {
		t.Fatalf("Xvfb failed to start: %v", err)
	}
	defer stopProcess(xvfb)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForDisplay(ctx, display, root, log); err != nil {
		t.Fatalf("display failed to become ready: %v", err)
	}
	runtimeDir := filepath.Join(root, "runtime")
	botDir := filepath.Join(root, "bots", testBot)
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(botDir, 0700); err != nil {
		t.Fatal(err)
	}
	shellHelper := filepath.Join(root, "terminal-shell.sh")
	helperText := "#!/bin/bash\nset -eu\ncd '" + botDir + "'\n" +
		"export TOFI_TERMINAL_ID='tofi-terminal-" + testBot + "'\n" +
		"pidfile='" + runtimeDir + "'/terminal-shell-$$.pid\n" +
		"pgid=$(ps -o pgid= -p \"$$\" | tr -d ' ')\n" +
		"sid=$(ps -o sid= -p \"$$\" | tr -d ' ')\n" +
		"start=$(awk '{print $22}' /proc/$$/stat)\n" +
		"printf '%s %s %s %s %s %s\\n' \"$$\" \"$pgid\" \"$sid\" \"$start\" \"$DISPLAY\" \"$TOFI_TERMINAL_ID\" > \"$pidfile\"\n" +
		"exec /bin/bash --noprofile --norc -c 'cd /tmp; trap \"\" HUP TERM; sleep 30 & wait'\n"
	if err := os.WriteFile(shellHelper, []byte(helperText), 0700); err != nil {
		t.Fatal(err)
	}
	terminal := exec.Command("setsid", "xterm", "-display", display, "-e", shellHelper)
	terminal.Env = []string{"DISPLAY=" + display, "HOME=" + root, "PATH=/usr/bin:/bin"}
	if err := terminal.Start(); err != nil {
		t.Fatalf("xterm failed to start: %v", err)
	}
	defer func() { _ = syscall.Kill(-terminal.Process.Pid, syscall.SIGKILL) }()
	var record terminalRecord
	defer func() {
		if record.sid > 1 {
			_ = killTerminalSession(context.Background(), record)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(runtimeDir, "terminal-shell-*.pid"))
		if len(files) == 1 {
			data, readErr := os.ReadFile(files[0])
			parsed, parseErr := parseTerminalRecord(string(data))
			if readErr == nil && parseErr == nil && terminalSessionOwned(parsed, display, "tofi-terminal-"+testBot) {
				record = parsed
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if record.pid == 0 {
		t.Fatal("xterm shell did not publish a valid terminal session record")
	}
	d := &desktop{botDir: botDir, terminalMarker: "tofi-terminal-" + testBot, display: display, runtimeDir: runtimeDir}
	// A record copied from another display must not be accepted for this Bot.
	d.display = ":222"
	if err := stopTerminalProcesses(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if !sessionAlive(record.sid) {
		t.Fatal("display-mismatched terminal was killed")
	}
	d.display = display
	if err := stopTerminalProcesses(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if sessionAlive(record.sid) {
		t.Fatal("terminal session survived cleanup")
	}
}

func TestTerminalSessionCleanupKeepsOtherBotAndParallelShell(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("guest image required: setsid")
	}
	root := t.TempDir()
	display := ":231"
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0700); err != nil {
		t.Fatal(err)
	}
	startRecorded := func(botID string) (*exec.Cmd, terminalRecord) {
		t.Helper()
		runtimeDir := filepath.Join(root, "runtime", botID)
		if err := os.MkdirAll(runtimeDir, 0700); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(root, botID+"-terminal.sh")
		marker := "tofi-terminal-" + botID
		text := "#!/bin/bash\nset -eu\n" +
			"export DISPLAY='" + display + "'\n" +
			"export TOFI_TERMINAL_ID='" + marker + "'\n" +
			"pidfile='" + runtimeDir + "'/terminal-shell-$$.pid\n" +
			"pgid=$(ps -o pgid= -p \"$$\" | tr -d ' ')\n" +
			"sid=$(ps -o sid= -p \"$$\" | tr -d ' ')\n" +
			"start=$(awk '{print $22}' /proc/$$/stat)\n" +
			"printf '%s %s %s %s %s %s\\n' \"$$\" \"$pgid\" \"$sid\" \"$start\" \"$DISPLAY\" \"$TOFI_TERMINAL_ID\" > \"$pidfile\"\n" +
			"exec /bin/bash --noprofile --norc -c 'sleep 30'\n"
		if err := os.WriteFile(script, []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("setsid", script)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			files, _ := filepath.Glob(filepath.Join(runtimeDir, "terminal-shell-*.pid"))
			if len(files) == 1 {
				data, readErr := os.ReadFile(files[0])
				record, parseErr := parseTerminalRecord(string(data))
				if readErr == nil && parseErr == nil && terminalSessionOwned(record, display, marker) {
					return cmd, record
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatalf("recorded shell for %s did not start", botID)
		return nil, terminalRecord{}
	}
	cmdA, recordA := startRecorded(testBot)
	cmdB, recordB := startRecorded(testBotB)
	defer func() {
		_ = syscall.Kill(-cmdA.Process.Pid, syscall.SIGKILL)
		_ = syscall.Kill(-cmdB.Process.Pid, syscall.SIGKILL)
	}()
	parallel := exec.Command("setsid", "sleep", "30")
	parallel.Env = []string{"PATH=/usr/bin:/bin"}
	if err := parallel.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-parallel.Process.Pid, syscall.SIGKILL) }()

	d := &desktop{display: display, terminalMarker: "tofi-terminal-" + testBot, runtimeDir: filepath.Join(root, "runtime", testBot)}
	if err := stopTerminalProcesses(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if sessionAlive(recordA.sid) {
		t.Fatal("own terminal session survived cleanup")
	}
	if !sessionAlive(recordB.sid) {
		t.Fatal("other Bot terminal session was killed")
	}
	if err := syscall.Kill(parallel.Process.Pid, 0); err != nil {
		t.Fatalf("parallel shell was killed: %v", err)
	}
}

func TestTerminalSessionOwnershipFallsBackFromZombieLeader(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("guest image required: setsid")
	}
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	display := ":232"
	marker := "tofi-terminal-zombie-test"
	script := filepath.Join(root, "terminal.sh")
	text := "#!/bin/bash\nset -eu\n" +
		"export DISPLAY='" + display + "'\n" +
		"export TOFI_TERMINAL_ID='" + marker + "'\n" +
		"pidfile='" + runtimeDir + "'/terminal-shell-$$.pid\n" +
		"pgid=$(ps -o pgid= -p \"$$\" | tr -d ' ')\n" +
		"sid=$(ps -o sid= -p \"$$\" | tr -d ' ')\n" +
		"start=$(awk '{print $22}' /proc/$$/stat)\n" +
		"printf '%s %s %s %s %s %s\\n' \"$$\" \"$pgid\" \"$sid\" \"$start\" \"$DISPLAY\" \"$TOFI_TERMINAL_ID\" > \"$pidfile\"\n" +
		"exec /bin/bash --noprofile --norc -c 'trap \"\" HUP TERM; sleep 30 & exit 0'\n"
	if err := os.WriteFile(script, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("setsid", script)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	var record terminalRecord
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(runtimeDir, "terminal-shell-*.pid"))
		if len(files) == 1 {
			data, readErr := os.ReadFile(files[0])
			parsed, parseErr := parseTerminalRecord(string(data))
			if readErr == nil && parseErr == nil {
				if stat, statErr := readProcStat(parsed.pid); statErr == nil && stat.state == 'Z' {
					record = parsed
					break
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if record.pid == 0 {
		t.Fatal("terminal leader did not become zombie while child remained")
	}
	if !terminalSessionOwned(record, display, marker) {
		t.Fatal("live marked child was not accepted after leader became zombie")
	}
	d := &desktop{display: display, terminalMarker: marker, runtimeDir: runtimeDir}
	if err := stopTerminalProcesses(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if sessionAlive(record.sid) {
		t.Fatal("session with zombie leader survived cleanup")
	}
}

// Exercise Fluxbox itself with a completely fresh HOME. Its first-start
// bootstrap used to overwrite our -rc file and launch fbsetbg/xmessage.
func TestFluxboxFirstStartKeepsBotConfiguration(t *testing.T) {
	for _, tool := range []string{"Xvfb", "fluxbox", "xdpyinfo", "xprop"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("guest image required: " + tool)
		}
	}
	root := t.TempDir()
	botDir := filepath.Join(root, "bots", testBot)
	config, err := prepareFluxboxConfig(botDir)
	if err != nil {
		t.Fatal(err)
	}
	env := append(cleanEnv(root, botDir), "DISPLAY=:199")
	log := &limitedBuffer{limit: 64 * 1024}
	xvfb, err := startDesktopProcess("Xvfb", []string{":199", "-screen", "0", "1280x800x24", "-nolisten", "tcp"}, env, log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(xvfb)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitForDisplay(ctx, ":199", root, log); err != nil {
		t.Fatal(err)
	}
	fluxbox, err := startDesktopProcess("fluxbox", []string{"-display", ":199", "-rc", config, "-no-slit"}, replaceEnv(env, "HOME", botDir), log)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if fluxbox != nil {
			_ = stopProcess(fluxbox)
		}
	}()
	for {
		probe := exec.CommandContext(ctx, "xprop", "-display", ":199", "-root", "_NET_SUPPORTING_WM_CHECK")
		output, _ := probe.Output()
		if strings.Contains(string(output), "window id # 0x") {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("Fluxbox not ready: %s", log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "session.styleOverlay:") || !strings.Contains(string(data), filepath.Join(botDir, ".fluxbox", "overlay")) {
		t.Fatalf("first start replaced Bot configuration: %s", data)
	}
	if strings.Contains(log.String(), "fbsetbg") || strings.Contains(log.String(), "wallpaper") {
		t.Fatalf("wallpaper helper ran: %s", log.String())
	}
	if err := stopProcess(fluxbox); err != nil {
		fluxbox = nil
		t.Fatalf("Fluxbox shutdown: %v", err)
	}
	fluxbox = nil
}

func TestChromeInitialGeometryFitsWorkarea(t *testing.T) {
	for _, area := range [][]int{{0, 0, 1280, 720}, {0, 0, 800, 520}, {30, 20, 1860, 980}} {
		args := strings.Join(chromeStartArgs("/workspace/browser/shared", ":100", 40123, area...), " ")
		if strings.Contains(args, "--start-maximized") {
			t.Fatal("initial window must not maximize")
		}
		var x, y, w, h int
		for _, arg := range strings.Fields(args) {
			if strings.HasPrefix(arg, "--window-position=") {
				fmt.Sscanf(arg, "--window-position=%d,%d", &x, &y)
			}
			if strings.HasPrefix(arg, "--window-size=") {
				fmt.Sscanf(arg, "--window-size=%d,%d", &w, &h)
			}
		}
		if x <= area[0] || y <= area[1] || x+w >= area[0]+area[2] || y+h >= area[1]+area[3] {
			t.Fatalf("window %d,%d %dx%d must have space within %v", x, y, w, h, area)
		}
	}
}
