package mcprunner

import (
	"os"
	"testing"
)

// TestMain lets the test binary act as the launcher plugins start through.
func TestMain(m *testing.M) {
	if RunLauncher(os.Args) {
		return
	}
	os.Exit(m.Run())
}
