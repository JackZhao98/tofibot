package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcceptanceCannotReusePersonalData(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tofi.db"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeInstance(dir, "acceptance"); err == nil {
		t.Fatal("accepted existing personal data")
	}
	personal, err := initializeInstance(dir, "personal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initializeInstance(dir, "acceptance"); err == nil {
		t.Fatal("allowed changing environment")
	}
	reopened, err := initializeInstance(dir, "personal")
	if err != nil || reopened.ID != personal.ID {
		t.Fatalf("identity changed: %v", err)
	}
}

func TestAcceptanceIdentityPersists(t *testing.T) {
	dir := t.TempDir()
	identity, err := initializeInstance(dir, "acceptance")
	if err != nil {
		t.Fatal(err)
	}
	again, err := initializeInstance(dir, "acceptance")
	if err != nil || again != identity {
		t.Fatalf("identity changed: %v", err)
	}
	if _, err := initializeInstance(dir, "personal"); err == nil {
		t.Fatal("test data accepted for personal instance")
	}
}
