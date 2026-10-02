package extensions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentMCPMetadataIndexRestartAndFingerprint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "metadata")
	index, err := NewPersistentMCPMetadataIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []MCPMetadataTool{{RemoteName: "search", Description: "Find records"}}
	if err := index.Put("crm", "config-a", want); err != nil {
		t.Fatal(err)
	}
	want[0].Description = "mutated after Put"
	restarted, err := NewPersistentMCPMetadataIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := restarted.Get("crm", "config-a")
	if !ok || len(got) != 1 || got[0].Description != "Find records" {
		t.Fatalf("restart read = %#v, %v", got, ok)
	}
	got[0].RemoteName = "mutated after Get"
	got, ok = restarted.Get("crm", "config-a")
	if !ok || got[0].RemoteName != "search" {
		t.Fatalf("Get did not return copy: %#v", got)
	}
	if _, ok := restarted.Get("crm", "config-b"); ok {
		t.Fatal("fingerprint mismatch was a hit")
	}
}

func TestPersistentMCPMetadataIndexPrivateAtomicFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private-index")
	index, err := NewPersistentMCPMetadataIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Put("server", "fp", []MCPMetadataTool{{RemoteName: "tool", Description: "desc"}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one server file, got %d", len(entries))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("file mode = %04o, want 0600", got)
	}
	if info.Size() > maxMetadataIndexFileBytes {
		t.Fatalf("file too large: %d", info.Size())
	}
}

func TestPersistentMCPMetadataIndexCorruptFileIsMiss(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "metadata")
	index, err := NewPersistentMCPMetadataIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Put("bad", "fp", []MCPMetadataTool{{RemoteName: "tool"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index.serverPath("bad"), []byte(`{"version":`), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentMCPMetadataIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.Get("bad", "fp"); ok {
		t.Fatal("corrupt file was a hit")
	}
}

func TestPersistentMCPMetadataIndexBoundsAndAbsolutePath(t *testing.T) {
	if _, err := NewPersistentMCPMetadataIndex("relative"); err == nil {
		t.Fatal("expected relative path error")
	}
	index, err := NewPersistentMCPMetadataIndex(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Put("", "fp", nil); err == nil {
		t.Fatal("expected empty server rejection")
	}
	if err := index.Put("s", "fp", []MCPMetadataTool{{RemoteName: "tool", Description: string(make([]byte, maxMetadataIndexDescription+1))}}); err == nil {
		t.Fatal("expected description bound")
	}
}
