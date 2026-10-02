//go:build linux

package guest

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDockShapeRunsAgainstOwnedTint2Window(t *testing.T) {
	for _, tool := range []string{"Xvfb", "fluxbox", "tint2", "xdotool", "python3", "xdpyinfo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("guest image required: " + tool)
		}
	}
	root := t.TempDir()
	env := []string{"DISPLAY=:224", "HOME=" + root, "PATH=/usr/local/bin:/usr/bin:/bin"}
	log := &limitedBuffer{limit: 64 * 1024}
	xvfb, err := startDesktopProcess("Xvfb", []string{":224", "-screen", "0", "1280x800x24", "-nolisten", "tcp"}, env, log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(xvfb)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitForDisplay(ctx, ":224", root, log); err != nil {
		t.Fatal(err)
	}
	config, err := prepareFluxboxConfig(filepath.Join(root, "bots", testBot))
	if err != nil {
		t.Fatal(err)
	}
	wm, err := startDesktopProcess("fluxbox", []string{"-display", ":224", "-rc", config, "-no-slit"}, replaceEnv(env, "HOME", filepath.Dir(filepath.Dir(config))), log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(wm)
	dock, err := startDesktopProcess("tint2", []string{"-c", filepath.Join(filepath.Dir(config), "tint2-launcher.rc")}, env, log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(dock)
	if err := shapeDesktopDock(ctx, env, dock.Process.Pid); err != nil {
		t.Fatalf("%v; tint2: %s", err, log.String())
	}
}
