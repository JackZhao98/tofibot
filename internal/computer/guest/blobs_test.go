//go:build linux || darwin

package guest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestGuestStagedBlobCleanupAfterObjectAliasCrashAndDurableWrites(t *testing.T) {
	root := t.TempDir()
	s, err := New(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	data := []byte("synthetic staged content")
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	id := uuid.NewString()
	request := func(method, id, digest string, data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/blobs/"+id, bytes.NewReader(data))
		if digest != "" {
			r.Header.Set("X-Tofi-Staged-SHA256", digest)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("PUT", id, "", data); w.Code != 201 {
		t.Fatalf("durable stage: %d %s", w.Code, w.Body.String())
	}
	folder := filepath.Join(root, "shared/.tofi/blobs")
	object := filepath.Join(folder, "objects", hash)
	// Simulate a crash with the durable object present but the alias absent.
	if err = os.Remove(filepath.Join(folder, id)); err != nil {
		t.Fatal(err)
	}
	if w := request("DELETE", id, hash, nil); w.Code != 204 {
		t.Fatalf("orphan cleanup: %d %s", w.Code, w.Body.String())
	}
	if _, err = os.Stat(object); !os.IsNotExist(err) {
		t.Fatal("unlinked content retained quota")
	}
	live := uuid.NewString()
	if w := request("PUT", live, "", data); w.Code != 201 {
		t.Fatal("fixture write")
	}
	if w := request("DELETE", uuid.NewString(), hash, nil); w.Code != 204 {
		t.Fatal("idempotent stage cleanup")
	}
	if w := request("GET", live, "", nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("live alias/object was reclaimed")
	}
	if w := request("DELETE", live, strings.Repeat("0", 64), nil); w.Code != 409 {
		t.Fatal("digest mismatch deleted alias")
	}
	if w := request("DELETE", uuid.NewString(), "../../outside", nil); w.Code != 400 {
		t.Fatal("forged digest accepted")
	}
	if w := request("GET", live, hash, nil); w.Code != 400 {
		t.Fatal("staged digest allowed on read")
	}
}

func TestGuestBlobPendingWritesAreBoundToAliasAndCleanedBeforeAcknowledgement(t *testing.T) {
	root := t.TempDir()
	s, err := New(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	id, otherID := uuid.NewString(), uuid.NewString()
	data := []byte("synthetic pre-rename content")
	digest := sha256.Sum256(data)
	folder := filepath.Join(root, "shared/.tofi/blobs/objects")
	if err = os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	pending, otherPending := filepath.Join(root, "shared/.tofi/blobs", blobPendingName(id)), filepath.Join(root, "shared/.tofi/blobs", blobPendingName(otherID))
	if err = os.WriteFile(pending, data[:5], 0400); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(otherPending, []byte("synthetic other staging identity"), 0400); err != nil {
		t.Fatal(err)
	}
	request := func(method string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/blobs/"+id, bytes.NewReader(data))
		if method == "DELETE" {
			r.Header.Set("X-Tofi-Staged-SHA256", hex.EncodeToString(digest[:]))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	// O_EXCL must detect the same journal-bound pending path, rather than writing
	// a fresh random temporary that a later DELETE cannot identify.
	if w := request("PUT"); w.Code != 507 {
		t.Fatalf("PUT ignored journal-bound interrupted write: %d", w.Code)
	}
	if w := request("DELETE"); w.Code != 204 || w.Header().Get("X-Tofi-Blob-Durability") != "1" {
		t.Fatalf("cleanup did not acknowledge durably: %d", w.Code)
	}
	if _, err = os.Lstat(pending); !os.IsNotExist(err) {
		t.Fatal("pending file remains after cleanup acknowledgement")
	}
	if got, err := os.ReadFile(otherPending); err != nil || string(got) != "synthetic other staging identity" {
		t.Fatal("cleanup removed another staging identity")
	}
	if w := request("PUT"); w.Code != 201 {
		t.Fatalf("retry after cleanup: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET"); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("retry did not preserve bytes")
	}
	if w := request("DELETE"); w.Code != 204 {
		t.Fatal("completed alias cleanup")
	}
}
