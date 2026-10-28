//go:build linux

package guest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A double or triple click is one xdotool process, so the presses reach the X
// server back to back instead of as separate tool calls.
func TestMultiClickRunsOneXdotoolCall(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	stub := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "xdotool"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	d := &desktop{display: ":99"}
	if _, err := runXTool(context.Background(), d, clickXArgs(10, 20, 1, 2)...); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 || lines[0] != "mousemove --sync 10 20 click --repeat 2 --delay 70 1" {
		t.Fatalf("calls=%q", lines)
	}
}
