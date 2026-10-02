//go:build linux

package guest

import (
	"os/exec"
	"testing"
)

func TestIsTerminatedDoesNotMaskSignalAborted(t *testing.T) {
	cmd := exec.Command("sh", "-c", "kill -ABRT $$")
	if err := cmd.Run(); err == nil {
		t.Fatal("abort fixture exited successfully")
	} else if isTerminated(err) || !isAborted(err) {
		t.Fatalf("SIGABRT classification=%v aborted=%v: %v", isTerminated(err), isAborted(err), err)
	}
}
