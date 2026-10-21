package guest

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func importChunk(t *testing.T, s *Service, p string, offset, total int64, data []byte) (map[string]any, error) {
	t.Helper()
	return s.importFileChunk(context.Background(), testBot, importArgs{Path: p, Offset: offset, Total: total, Data: base64.StdEncoding.EncodeToString(data)})
}

func TestImportFileChunksArriveWholeAndAreIdempotent(t *testing.T) {
	s := newTestService(t)
	dst := filepath.Join(s.root, "home", "uploads", "conv-1", "invoices.zip")
	want := []byte{0, 1, 2, 0xff, 'x', 'y'}
	out, err := importChunk(t, s, dst, 0, 6, want[:4])
	if err != nil || out["status"] != "partial" {
		t.Fatalf("first=%v %v", out, err)
	}
	if _, err = os.Lstat(dst); err == nil {
		t.Fatal("partial file visible at its final name")
	}
	out, err = importChunk(t, s, dst, 4, 6, want[4:])
	if err != nil || out["status"] != "written" {
		t.Fatalf("last=%v %v", out, err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got=%v %v", got, err)
	}
	if _, err = os.Lstat(dst + ".part"); err == nil {
		t.Fatal("part file left behind")
	}
	// A file the Bot changed is never replaced by a later copy.
	if err = os.WriteFile(dst, []byte("edited"), 0660); err != nil {
		t.Fatal(err)
	}
	out, err = importChunk(t, s, dst, 0, 6, want[:4])
	if err != nil || out["status"] != "exists" {
		t.Fatalf("repeat=%v %v", out, err)
	}
	if got, _ = os.ReadFile(dst); string(got) != "edited" {
		t.Fatalf("existing file replaced: %q", got)
	}
	empty := filepath.Join(filepath.Dir(dst), "empty.txt")
	if out, err = importChunk(t, s, empty, 0, 0, nil); err != nil || out["status"] != "written" {
		t.Fatalf("empty=%v %v", out, err)
	}
}

func TestImportFileRefusesOutsideUploadsAndBadChunks(t *testing.T) {
	s := newTestService(t)
	uploads := filepath.Join(s.root, "home", "uploads", "c")
	for name, p := range map[string]string{
		"relative":  "uploads/x",
		"shared":    filepath.Join(s.root, "shared", "x"),
		"botdir":    filepath.Join(s.root, "bots", testBot, "x"),
		"escape":    uploads + "/../../x",
		"folderdir": filepath.Join(s.root, "home", "uploads"),
	} {
		if _, err := importChunk(t, s, p, 0, 1, []byte("a")); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := importChunk(t, s, filepath.Join(uploads, "big"), 0, importMaxBytes+1, nil); err == nil {
		t.Error("oversized total accepted")
	}
	if _, err := importChunk(t, s, filepath.Join(uploads, "late"), 3, 6, []byte("abc")); err == nil {
		t.Error("out-of-order chunk without a start accepted")
	}
	if _, err := importChunk(t, s, filepath.Join(uploads, "over"), 0, 2, []byte("abc")); err == nil {
		t.Error("chunk beyond total accepted")
	}
}

func TestUserTessdataHoldsShippedDataAndStaysWritable(t *testing.T) {
	system := t.TempDir()
	for _, name := range []string{"eng.traineddata", "osd.traineddata"} {
		if err := os.WriteFile(filepath.Join(system, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(system, "configs"), 0755); err != nil {
		t.Fatal(err)
	}
	old := tesseractSystemGlob
	tesseractSystemGlob = system
	defer func() { tesseractSystemGlob = old }()
	home := t.TempDir()
	dir := userTessdata(home)
	if dir != filepath.Join(home, ".local", "share", "tessdata") {
		t.Fatalf("dir=%q", dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "eng.traineddata")); err != nil || string(b) != "eng.traineddata" {
		t.Fatalf("shipped English data not reachable: %q %v", b, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "configs")); err != nil || !info.IsDir() {
		t.Fatalf("configs missing: %v", err)
	}
	// A pack the user adds, and a replacement of a shipped name, both survive.
	if err := os.WriteFile(filepath.Join(dir, "chi_sim.traineddata"), []byte("zh"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "osd.traineddata")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "osd.traineddata"), []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	if again := userTessdata(home); again != dir {
		t.Fatal("second call moved the directory")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "osd.traineddata")); string(b) != "mine" {
		t.Fatalf("user file replaced: %q", b)
	}
	found := false
	for _, e := range cleanEnv(t.TempDir(), "bot") {
		found = found || strings.HasPrefix(e, "TESSDATA_PREFIX=")
	}
	if !found {
		t.Fatal("shell environment lacks TESSDATA_PREFIX")
	}
	tesseractSystemGlob = filepath.Join(t.TempDir(), "absent")
	for _, e := range cleanEnv(t.TempDir(), "bot") {
		if strings.HasPrefix(e, "TESSDATA_PREFIX=") {
			t.Fatal("TESSDATA_PREFIX set without tesseract")
		}
	}
}
