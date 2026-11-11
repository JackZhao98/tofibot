package filestore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/filestore"
	"github.com/JackZhao98/tofibot/internal/filestore/fakes3"
)

func s3Pair(t *testing.T) (*filestore.S3, *fakes3.Server) {
	t.Helper()
	f := fakes3.New("bkt", "AKIATESTONLY")
	t.Cleanup(f.Close)
	s, err := filestore.NewS3(filestore.S3Config{Endpoint: f.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: "AKIATESTONLY", Secret: "test-secret-not-real", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

// Both backends answer the same contract.
func TestStoreContract(t *testing.T) {
	s3, _ := s3Pair(t)
	for name, st := range map[string]filestore.Store{"disk": filestore.Disk{Root: t.TempDir()}, "s3": s3} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			data := []byte("hello object store")
			key := "k-1"
			if name == "s3" {
				key = "tofi/v1/i/accounts/a/uploads/k-1"
			}
			if err := st.Put(ctx, key, bytes.NewReader(data), filestore.ObjectMeta{Size: int64(len(data)), SHA256: "x", SafeMIME: "text/plain"}); err != nil {
				t.Fatal(err)
			}
			if err := st.Put(ctx, key, bytes.NewReader(data), filestore.ObjectMeta{Size: int64(len(data))}); !errors.Is(err, filestore.ErrExists) {
				t.Fatalf("second put = %v, want ErrExists", err)
			}
			r, info, err := st.Open(ctx, key)
			if err != nil || info.Size != int64(len(data)) {
				t.Fatal(err, info)
			}
			if _, err = r.Seek(6, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(r)
			r.Close()
			if string(got) != "object store" {
				t.Fatalf("range read %q", got)
			}
			n := 0
			if err = st.List(ctx, strings.TrimSuffix(key, "k-1"), func(filestore.ObjectInfo) error { n++; return nil }); err != nil || n != 1 {
				t.Fatal(err, n)
			}
			if err = st.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if err = st.Delete(ctx, key); err != nil {
				t.Fatalf("delete of a missing object must succeed: %v", err)
			}
			if _, err = st.Stat(ctx, key); !errors.Is(err, filestore.ErrNotFound) {
				t.Fatalf("stat after delete = %v", err)
			}
			if _, _, err = st.Open(ctx, key); !errors.Is(err, filestore.ErrNotFound) {
				t.Fatalf("open after delete = %v", err)
			}
		})
	}
}

func TestDiskRefusesPathKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "outside"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	d := filestore.Disk{Root: root}
	for _, k := range []string{"../outside", "a/b", ".."} {
		if _, _, err := d.Open(context.Background(), k); !errors.Is(err, filestore.ErrNotFound) {
			t.Fatalf("%q: %v", k, err)
		}
	}
}

func TestS3ErrorKinds(t *testing.T) {
	s, f := s3Pair(t)
	ctx := context.Background()
	f.Down = true
	if _, err := s.Stat(ctx, "k"); !errors.Is(err, filestore.ErrUnavailable) {
		t.Fatalf("503 = %v", err)
	}
	f.Down = false
	bad, _ := filestore.NewS3(filestore.S3Config{Endpoint: f.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: "WRONGKEY", Secret: "x", PathStyle: true})
	if _, err := bad.Stat(ctx, "k"); !errors.Is(err, filestore.ErrDenied) {
		t.Fatalf("bad key = %v", err)
	}
	f.Down = true
	if err := s.Put(ctx, "x", strings.NewReader("a"), filestore.ObjectMeta{Size: 1}); err == nil || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "bkt") {
		t.Fatalf("an error text names the host or bucket: %v", err)
	}
}

func TestProbeNamesEachFailure(t *testing.T) {
	s, f := s3Pair(t)
	ctx := context.Background()
	public, err := filestore.Probe(ctx, s, "tofi/v1/i/_probe/1")
	if err != nil || public {
		t.Fatalf("healthy bucket: public=%v err=%v", public, err)
	}
	if len(f.Keys()) != 0 {
		t.Fatalf("probe left objects: %v", f.Keys())
	}
	code := func() string {
		_, err := filestore.Probe(ctx, s, "tofi/v1/i/_probe/2")
		var pe *filestore.ProbeError
		if !errors.As(err, &pe) {
			return ""
		}
		return pe.Code
	}
	f.Public = true
	if public, _ = filestore.Probe(ctx, s, "tofi/v1/i/_probe/3"); !public {
		t.Fatal("a bucket that answers an unsigned read is reported public")
	}
	f.Public = false
	f.NoConditional = true
	if c := code(); c != "conditional_put_unsupported" {
		t.Fatalf("got %q", c)
	}
	f.NoConditional, f.NoRange = false, true
	if c := code(); c != "range_unsupported" {
		t.Fatalf("got %q", c)
	}
	f.NoRange, f.Down = false, true
	if c := code(); c != "endpoint_unreachable" {
		t.Fatalf("got %q", c)
	}
	f.Down = false
	bad, _ := filestore.NewS3(filestore.S3Config{Endpoint: f.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: "WRONGKEY", Secret: "x", PathStyle: true})
	if _, err = filestore.Probe(ctx, bad, "p"); err == nil || err.Error() != "credentials_rejected" {
		t.Fatalf("got %v", err)
	}
	nobucket, _ := filestore.NewS3(filestore.S3Config{Endpoint: f.URL, Region: "us-east-1", Bucket: "nope", AccessKeyID: "AKIATESTONLY", Secret: "x", PathStyle: true})
	if _, err = filestore.Probe(ctx, nobucket, "p"); err == nil || err.Error() != "bucket_not_found" {
		t.Fatalf("got %v", err)
	}
}
