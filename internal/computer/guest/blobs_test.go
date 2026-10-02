//go:build linux || darwin

package guest

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestGuestBlobAtomicDedupOwnershipAndReclamation(t *testing.T) {
	root := t.TempDir()
	s, err := New(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	request := func(method, id string, data []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(method, "/v1/blobs/"+id, bytes.NewReader(data)))
		return w
	}
	first, second := uuid.NewString(), uuid.NewString()
	data := []byte("synthetic account file")
	for _, id := range []string{first, second} {
		if w := request("PUT", id, data); w.Code != 201 {
			t.Fatalf("put %d %s", w.Code, w.Body.String())
		}
	}
	folder := filepath.Join(root, "shared/.tofi/blobs")
	a, _ := os.Stat(filepath.Join(folder, first))
	b, _ := os.Stat(filepath.Join(folder, second))
	if !os.SameFile(a, b) {
		t.Fatal("identical bytes consumed two physical objects")
	}
	if w := request("GET", first, nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("blob read lost bytes")
	}
	if w := request("PUT", first, []byte("different")); w.Code != 409 {
		t.Fatal("existing alias overwritten")
	}
	if w := request("DELETE", first, nil); w.Code != 204 {
		t.Fatal("alias delete")
	}
	if w := request("GET", second, nil); w.Code != 200 {
		t.Fatal("another live alias lost")
	}
	request("DELETE", second, nil)
	objects, err := os.ReadDir(filepath.Join(folder, "objects"))
	if err != nil || len(objects) != 0 {
		t.Fatalf("final reference retained quota: %v %v", objects, err)
	}
	link := uuid.NewString()
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(folder, link))
	if w := request("GET", link, nil); w.Code == 200 {
		t.Fatal("read followed symlink outside blob root")
	}
	if w := request("PUT", "../escape", data); w.Code == 201 {
		t.Fatal("path injection")
	}
	if w := request("PUT", uuid.NewString(), bytes.Repeat([]byte{'x'}, int(MaxBlobBytes+1))); w.Code != 413 {
		t.Fatal("oversized file accepted")
	}
}
