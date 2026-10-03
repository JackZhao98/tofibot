package codexauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenReadOnlyDoesNotCreateChangePermissionsOrRefresh(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent")
	if _, err := OpenReadOnly(missing); err == nil {
		t.Fatal("accepted nonexistent auth directory")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("read-only open created state")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"access_token":"synthetic","refresh_token":"never-refresh","expires_at":1}`)
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	m, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CredentialReadOnly(context.Background()); err == nil {
		t.Fatal("accepted expired snapshot")
	}
	after, _ := os.Stat(path)
	info, _ := os.Stat(dir)
	data, _ := os.ReadFile(path)
	if info.Mode().Perm() != 0755 || !after.ModTime().Equal(before.ModTime()) || string(data) != string(raw) {
		t.Fatal("read-only accessor mutated auth state")
	}
}
