package mcprunner

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

// The accounts' cgroups live under the root of a private cgroup namespace; in the
// host's namespace they live under the container's own cgroup, and a read-only
// tree is never remounted there (that would open the host's tree).
func TestCgroupBaseAndRemountSelection(t *testing.T) {
	for _, tc := range []struct {
		own, base string
		remount   bool
	}{
		{"/", "/sys/fs/cgroup", true},
		{"", "/sys/fs/cgroup", true},
		{"/system.slice/docker-abc.scope", "/sys/fs/cgroup/system.slice/docker-abc.scope", false},
		{"/docker/abc", "/sys/fs/cgroup/docker/abc", false},
	} {
		if got := cgroupBase("/sys/fs/cgroup", tc.own); got != tc.base {
			t.Errorf("own %q: base %q, want %q", tc.own, got, tc.base)
		}
		err := checkCgroupRemount(tc.own)
		if (err == nil) != tc.remount {
			t.Errorf("own %q: remount error %v, allowed %v", tc.own, err, tc.remount)
		}
		if err != nil && !strings.Contains(err.Error(), "cgroup: private") {
			t.Errorf("own %q: the error does not name the fix: %v", tc.own, err)
		}
	}
}

func TestCgroupRemountFailureNamesTheFix(t *testing.T) {
	err := cgroupRemountFailure(syscall.EACCES)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("the cause is lost: %v", err)
	}
	for _, want := range []string{"cannot be remounted", "tofi-runner", "AppArmor", "tofi restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	// Required mode stops the Runner with that message; auto only logs it.
	if got := unavailable(SandboxRequired, "per-account cgroups", err); got == nil || !strings.Contains(got.Error(), "tofi-runner") {
		t.Fatalf("required: %v", got)
	}
}
