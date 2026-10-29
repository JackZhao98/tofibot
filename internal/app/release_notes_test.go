package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A v* tag without its release notes fails the release workflow, before anything is built or published.
func TestReleaseNotesCheckFailsATagWithoutItsFile(t *testing.T) {
	root := testRepoRoot(t)
	script := filepath.Join(root, "scripts", "check-release-notes.sh")
	run := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("v0.6.0-rc.1"); err != nil {
		t.Fatalf("the shipped notes must pass: %v\n%s", err, out)
	}
	if out, err := run("--all"); err != nil {
		t.Fatalf("--all: %v\n%s", err, out)
	}
	out, err := run("v99.0.0")
	if err == nil || !strings.Contains(out, "release-notes/v99.0.0.md is missing") {
		t.Fatalf("a tag without notes must fail: %v\n%s", err, out)
	}
	if _, err := run("v1.2"); err == nil {
		t.Fatal("a name that is not a release tag must fail")
	}

	// A file with the wrong shape fails with the reason.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(script)
	if err := os.WriteFile(filepath.Join(dir, "scripts", "check-release-notes.sh"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "release-notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"v1.0.0":      "---\nversion: v1.0.1\ndate: 2026-10-28\nchannel: stable\n---\n# T\n\n- x\n",
		"v1.0.1":      "---\nversion: v1.0.1\ndate: 28 Oct\nchannel: stable\n---\n# T\n\n- x\n",
		"v1.0.2-rc.1": "---\nversion: v1.0.2-rc.1\ndate: 2026-10-28\nchannel: stable\n---\n# T\n\n- x\n",
		"v1.0.3":      "---\nversion: v1.0.3\ndate: 2026-10-28\nchannel: stable\n---\nNo title.\n\n- x\n",
		"v1.0.4":      "---\nversion: v1.0.4\ndate: 2026-10-28\nchannel: stable\n---\n# T\n\nNo bullet.\n",
		"v1.0.5":      "no front matter\n# T\n- x\n",
	}
	for tag, text := range bad {
		if err := os.WriteFile(filepath.Join(dir, "release-notes", tag+".md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", filepath.Join(dir, "scripts", "check-release-notes.sh"), tag)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("%s should fail:\n%s", tag, out)
		}
	}
	good := "---\nversion: v1.0.6\ndate: 2026-10-28\nchannel: stable\n---\n# T\n\n- x\n"
	_ = os.WriteFile(filepath.Join(dir, "release-notes", "v1.0.6.md"), []byte(good), 0o644)
	cmd := exec.Command("bash", filepath.Join(dir, "scripts", "check-release-notes.sh"), "v1.0.6")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("a well formed file must pass: %v\n%s", err, out)
	}
}

func TestReleaseWorkflowRunsTheNotesCheckBeforeAnythingIsBuilt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testRepoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	version := strings.Index(text, "  version:")
	check := strings.Index(text, "scripts/check-release-notes.sh")
	test := strings.Index(text, "\n  test:")
	if version < 0 || check < version || check > test {
		t.Fatalf("the notes check must be a step of the version job, which every other job needs (version %d, check %d, test %d)", version, check, test)
	}
	if !strings.Contains(text, "needs: version") {
		t.Fatal("jobs after version must need it")
	}
}
