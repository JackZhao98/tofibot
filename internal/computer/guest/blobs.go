package guest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

const MaxBlobBytes int64 = 20 << 20

// Opaque aliases and content objects both reside on the guest workspace disk.
// Hard links deduplicate bytes without introducing a second mounted writer.
func (s *Service) handleBlob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/blobs/")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || r.URL.RawQuery != "" {
		writeError(w, 400, "invalid blob identity")
		return
	}
	if r.Method != "PUT" && r.Method != "GET" && r.Method != "DELETE" {
		writeError(w, 405, "method not allowed")
		return
	}
	stagedDigest := r.Header.Get("X-Tofi-Staged-SHA256")
	if stagedDigest != "" {
		hash, err := hex.DecodeString(stagedDigest)
		if r.Method != "DELETE" || err != nil || len(hash) != 32 || hex.EncodeToString(hash) != stagedDigest {
			writeError(w, 400, "invalid staged digest")
			return
		}
	}
	base, err := os.OpenRoot(s.root)
	if err != nil {
		writeError(w, 503, "workspace unavailable")
		return
	}
	defer base.Close()
	if err = base.MkdirAll("shared/.tofi/blobs/objects", 0700); err != nil {
		writeError(w, 507, "workspace storage unavailable")
		return
	}
	root, err := base.OpenRoot("shared/.tofi/blobs")
	if err != nil {
		writeError(w, 503, "blob storage unavailable")
		return
	}
	defer root.Close()
	s.fileWriteMu.Lock()
	defer s.fileWriteMu.Unlock()
	switch r.Method {
	case "PUT":
		r.Body = http.MaxBytesReader(w, r.Body, MaxBlobBytes)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, 413, "blob exceeds upload limit")
			return
		}
		digest := sha256.Sum256(data)
		object := "objects/" + hex.EncodeToString(digest[:])
		if _, err = root.Lstat(id); err == nil {
			writeError(w, 409, "blob alias already exists")
			return
		} else if !os.IsNotExist(err) {
			writeError(w, 503, "blob state unavailable")
			return
		}
		if info, err := root.Lstat(object); os.IsNotExist(err) {
			temporary := "objects/." + uuid.NewString() + ".pending"
			f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
			if err != nil {
				writeError(w, 507, "workspace storage full or unavailable")
				return
			}
			_, err = f.Write(data)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				root.Remove(temporary)
				writeError(w, 507, "workspace storage full or unavailable")
				return
			}
			if err = root.Rename(temporary, object); err != nil {
				root.Remove(temporary)
				writeError(w, 507, "workspace storage unavailable")
				return
			}
			if err = syncBlobDirectory(root, "objects"); err != nil {
				writeError(w, 507, "workspace storage durability unavailable")
				return
			}
		} else if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			writeError(w, 503, "invalid content object")
			return
		}
		if err = root.Link(object, id); err != nil {
			writeError(w, 507, "workspace storage unavailable")
			return
		}
		if err = syncBlobDirectory(root, "."); err != nil {
			writeError(w, 507, "workspace storage durability unavailable")
			return
		}
		w.Header().Set("X-Tofi-Blob-Durability", "1")
		w.WriteHeader(201)
	case "GET":
		f, err := openBlob(root, id)
		if err != nil {
			writeError(w, 404, "blob not found")
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBlobBytes {
			writeError(w, 503, "invalid blob")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, id, info.ModTime(), f)
	case "DELETE":
		var object string
		var original os.FileInfo
		if f, err := openBlob(root, id); err == nil {
			original, _ = f.Stat()
			hash := sha256.New()
			_, readErr := io.Copy(hash, io.LimitReader(f, MaxBlobBytes+1))
			f.Close()
			if readErr == nil {
				object = "objects/" + hex.EncodeToString(hash.Sum(nil))
			}
		}
		if stagedDigest != "" {
			if object != "" && object != "objects/"+stagedDigest {
				writeError(w, 409, "staged content differs")
				return
			}
			object = "objects/" + stagedDigest
		}
		if err = root.Remove(id); err != nil && !os.IsNotExist(err) {
			writeError(w, 503, "blob removal unavailable")
			return
		}
		// The final alias releases the single physical object. SameFile prevents
		// hostile metadata from selecting some other live content object.
		if object != "" {
			if info, e := root.Lstat(object); e == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && blobHasOneLink(info) && (stagedDigest != "" || (original != nil && os.SameFile(original, info))) {
				if err = root.Remove(object); err != nil && !os.IsNotExist(err) {
					writeError(w, 503, "object removal unavailable")
					return
				}
			}
		}
		if err = syncBlobDirectory(root, "objects"); err == nil {
			err = syncBlobDirectory(root, ".")
		}
		if err != nil {
			writeError(w, 503, "blob removal durability unavailable")
			return
		}
		w.Header().Set("X-Tofi-Blob-Durability", "1")
		w.WriteHeader(204)
	}
}

func openBlob(root *os.Root, id string) (*os.File, error) {
	flags, err := blobReadFlags()
	if err != nil {
		return nil, err
	}
	f, err := root.OpenFile(id, os.O_RDONLY|flags, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("blob is not a regular file")
	}
	return f, nil
}

// A successful blob acknowledgement includes durable directory entries. The
// import coordinator may commit SQLite metadata immediately after this reply.
func syncBlobDirectory(root *os.Root, path string) error {
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
