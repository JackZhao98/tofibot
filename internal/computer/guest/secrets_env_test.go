//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedConfiguredEnvironment(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tofi-env")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "API_KEY"), []byte("fixture\nvalue"), 0600)
	os.WriteFile(filepath.Join(dir, "BASH_ENV"), []byte("/tmp/evil"), 0600)
	os.WriteFile(filepath.Join(dir, "HOME"), []byte("/tmp/evil"), 0600)
	os.Symlink(filepath.Join(dir, "API_KEY"), filepath.Join(dir, "LINK_KEY"))
	for _, bot := range []string{"a", "b"} {
		env := strings.Join(cleanEnv(root, filepath.Join(root, "bots", bot)), "\x00")
		if !strings.Contains(env, "API_KEY=fixture\nvalue") {
			t.Fatal("shared env missing")
		}
		if strings.Contains(env, "/tmp/evil") || strings.Contains(env, "LINK_KEY=") {
			t.Fatal("unsafe env accepted")
		}
	}
}
