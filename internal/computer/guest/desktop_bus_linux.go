//go:build linux

package guest

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// One session D-Bus per desktop (decision 2026-10-30). Before this every GUI
// app ran in its own dbus-run-session, so no two apps shared a bus and there
// was no AT-SPI registry a tool could ask about them. Now the desktop starts
// one dbus-daemon at <runtime dir>/bus and every graphical process gets its
// address plus the switches that make GTK and Qt publish their accessibility
// tree (the AT-SPI bus and registry start on first use through the bus's own
// service activation). The classic screenshot and xdotool tools do not care.

// sessionBusPath is the socket of the desktop's session bus. The runtime dir is
// recreated at every desktop start, so the socket never outlives its daemon.
func sessionBusPath(runtimeDir string) string { return filepath.Join(runtimeDir, "bus") }

// sessionBusAddress is the D-Bus address of the desktop's session bus.
func sessionBusAddress(runtimeDir string) string { return "unix:path=" + sessionBusPath(runtimeDir) }

// desktopA11yEnv is what a graphical process needs to join the desktop bus and
// publish its accessibility tree.
func desktopA11yEnv(runtimeDir string) []string {
	return []string{
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"DBUS_SESSION_BUS_ADDRESS=" + sessionBusAddress(runtimeDir),
		"GTK_A11Y=atspi",
		"NO_AT_BRIDGE=0",
		"GNOME_ACCESSIBILITY=1",
		"QT_ACCESSIBILITY=1",
		"QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1",
	}
}

// withDesktopA11yEnv returns env with the desktop bus and accessibility switches
// set, replacing earlier values of the same names.
func withDesktopA11yEnv(env []string, runtimeDir string) []string {
	for _, item := range desktopA11yEnv(runtimeDir) {
		name, value, _ := strings.Cut(item, "=")
		env = replaceEnv(env, name, value)
	}
	return env
}

// desktopA11yShellExports is the same set as shell lines for a launcher script.
func desktopA11yShellExports(runtimeDir string) string {
	var b strings.Builder
	for _, item := range desktopA11yEnv(runtimeDir) {
		name, value, _ := strings.Cut(item, "=")
		b.WriteString("export " + name + "=" + shellQuote(value) + "\n")
	}
	return b.String()
}

// startSessionBus starts the desktop's dbus-daemon on the fixed socket and waits
// until it accepts connections. Its output goes to the desktop log.
func startSessionBus(ctx context.Context, runtimeDir string, env []string, log *limitedBuffer) (*exec.Cmd, error) {
	cmd, err := startDesktopProcess("dbus-daemon", []string{"--session", "--nofork", "--nopidfile", "--address=" + sessionBusAddress(runtimeDir)}, env, log)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if conn, err := net.DialTimeout("unix", sessionBusPath(runtimeDir), 200*time.Millisecond); err == nil {
			_ = conn.Close()
			return cmd, nil
		}
		if time.Now().After(deadline) {
			_ = stopProcess(cmd)
			return nil, errors.New("session bus did not come up")
		}
		select {
		case <-ctx.Done():
			_ = stopProcess(cmd)
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
