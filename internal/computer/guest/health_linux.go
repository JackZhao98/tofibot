//go:build linux

package guest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) healthStatus() (int, map[string]any) {
	result := map[string]any{
		"service": "tofi-guest", "vsock_port": VsockPort, "workspace": s.root,
		"capabilities": []string{"shell.exec", "files.list", "files.read", "files.write", "timezone.get", "timezone.set", "terminal.open", "terminal.list", "terminal.read", "terminal.write", "terminal.resize", "terminal.close", "desktop.start", "desktop.stop", "desktop.hold", "desktop.release", "desktop.capture", "desktop.click", "desktop.type", "desktop.key", "desktop.scroll", "browser.navigate", "browser.snapshot", "browser.action"},
	}
	f, err := os.CreateTemp(s.root, ".health-*")
	if err != nil {
		result["error"] = fmt.Sprintf("workspace is not writable: %v", err)
		return 503, result
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version := exec.CommandContext(ctx, "google-chrome-stable", "--version")
	version.Env = cleanEnv(s.root, filepath.Join(s.root, "home"))
	out, err := version.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Google Chrome") {
		result["error"] = fmt.Sprintf("google-chrome-stable is not ready: %s", strings.TrimSpace(string(out)))
		return 503, result
	}
	result["browser_version"] = strings.TrimSpace(string(out))
	return 200, result
}
