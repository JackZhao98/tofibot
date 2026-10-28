package mcprunner

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Import limits. The data of a connection is small (a keyring file, a token
// file, a config); anything larger is not connection data and is refused.
const (
	importMaxFile    = 4 << 20
	importMaxTotal   = 24 << 20
	importMaxFiles   = 512
	importMaxPlugins = 32
	// ImportBodyLimit is the request body limit of POST /v1/import.
	ImportBodyLimit = 40 << 20
)

// ImportFile is one file of a plugin's data. Path is relative to the plugin
// directory.
type ImportFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	DataBase64 string `json:"data_base64"`
}

// ImportPlugin carries one plugin from an older Runner: how to install it again
// (the pinned request, with the secret environment restored) and the data that
// must not be lost.
type ImportPlugin struct {
	Request InstallRequest `json:"request"`
	Files   []ImportFile   `json:"files,omitempty"`
}

type ImportRequest struct {
	Plugins []ImportPlugin `json:"plugins"`
}

// ImportResult states, per plugin.
const (
	ImportInstalled = "installed" // installed now and every file read back with the expected digest
	ImportPresent   = "present"   // already installed here; nothing was overwritten
	ImportFailed    = "failed"    // nothing was kept
)

type ImportResult struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// Files is the number of files written and read back (installed), or compared (present).
	Files int `json:"files"`
	// Identical is true when every file already here has the digest that was sent.
	Identical bool   `json:"identical"`
	Error     string `json:"error,omitempty"`
}

// importable lists what a plugin's data may contain. Code is never imported: a
// package is installed again from its pinned request, so a modified file in an
// old installation cannot ride along.
func importablePath(kind, rel string) bool {
	if rel == "" || filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) || strings.Contains(rel, "\\") {
		return false
	}
	if filepath.Clean(rel) != rel || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return false
	}
	if strings.HasPrefix(rel, "home/") {
		return true
	}
	if kind == "builtin_gog" {
		return rel == "keyring-password" || rel == "gog-account.json"
	}
	return false
}

// Import installs plugins that came from another Runner and restores their
// data. It never overwrites a plugin that is already installed: the copy here
// may hold newer state than the one being imported.
func (r *Runner) Import(ctx context.Context, in ImportRequest) []ImportResult {
	results := make([]ImportResult, 0, len(in.Plugins))
	if len(in.Plugins) > importMaxPlugins {
		return []ImportResult{{State: ImportFailed, Error: "too many plugins"}}
	}
	var total int64
	for _, p := range in.Plugins {
		res := ImportResult{ID: p.Request.ID}
		files, n, err := decodeImportFiles(p.Request.Kind, p.Files)
		total += n
		if err == nil && total > importMaxTotal {
			err = errors.New("import is too large")
		}
		if err != nil {
			res.State, res.Error = ImportFailed, err.Error()
			results = append(results, res)
			continue
		}
		r.mu.RLock()
		existing := r.plugins[p.Request.ID]
		r.mu.RUnlock()
		if existing != nil {
			res.State = ImportPresent
			res.Identical, res.Files = r.compareImportFiles(p.Request.ID, files)
			results = append(results, res)
			continue
		}
		err = r.install(ctx, p.Request, func(pr *os.Root) error { return writeImportFiles(pr, files) })
		if err != nil {
			res.State, res.Error = ImportFailed, err.Error()
		} else {
			res.State, res.Identical, res.Files = ImportInstalled, true, len(files)
		}
		results = append(results, res)
	}
	return results
}

type decodedImportFile struct {
	path string
	data []byte
	sum  string
}

func decodeImportFiles(kind string, files []ImportFile) ([]decodedImportFile, int64, error) {
	if len(files) > importMaxFiles {
		return nil, 0, errors.New("too many files")
	}
	out := make([]decodedImportFile, 0, len(files))
	seen := map[string]bool{}
	var total int64
	for _, f := range files {
		if !importablePath(kind, f.Path) {
			return nil, 0, fmt.Errorf("file %q is not plugin data", f.Path)
		}
		if seen[f.Path] {
			return nil, 0, fmt.Errorf("file %q is listed twice", f.Path)
		}
		seen[f.Path] = true
		data, err := base64.StdEncoding.DecodeString(f.DataBase64)
		if err != nil || len(data) > importMaxFile {
			return nil, 0, fmt.Errorf("file %q is not valid", f.Path)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != strings.ToLower(f.SHA256) {
			return nil, 0, fmt.Errorf("file %q does not match its digest", f.Path)
		}
		total += int64(len(data))
		out = append(out, decodedImportFile{path: f.Path, data: data, sum: f.SHA256})
	}
	return out, total, nil
}

// writeImportFiles writes the files below the plugin directory, then reads each
// one back and compares the digest. A symlink anywhere on the way fails the
// import: the root confines every step, and a link at the final name is refused.
func writeImportFiles(pr *os.Root, files []decodedImportFile) error {
	for _, f := range files {
		rel := filepath.FromSlash(f.path)
		if err := mkdirNoSymlink(pr, filepath.Dir(rel)); err != nil {
			return err
		}
		if info, err := pr.Lstat(rel); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("file %q is a symlink", f.path)
		}
		if err := writeAtomicIn(pr, rel, f.data); err != nil {
			return err
		}
		back, err := pr.ReadFile(rel)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(back)
		if hex.EncodeToString(sum[:]) != strings.ToLower(f.sum) {
			return fmt.Errorf("file %q changed while it was written", f.path)
		}
	}
	return nil
}

// mkdirNoSymlink creates dir (relative to the plugin root) one element at a time
// and refuses to pass through anything that is not a plain directory.
func mkdirNoSymlink(pr *os.Root, dir string) error {
	if dir == "." {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := pr.Lstat(cur)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := pr.Mkdir(cur, 0700); err != nil {
				return err
			}
		case err != nil:
			return err
		case !info.IsDir():
			return errors.New("a path element is not a directory")
		}
	}
	return nil
}

func (r *Runner) compareImportFiles(id string, files []decodedImportFile) (identical bool, compared int) {
	pr, err := r.pluginRoot(id)
	if err != nil {
		return false, len(files)
	}
	defer pr.Close()
	identical = true
	for _, f := range files {
		data, err := pr.ReadFile(filepath.FromSlash(f.path))
		compared++
		if err != nil {
			identical = false
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != strings.ToLower(f.sum) {
			identical = false
		}
	}
	return identical, compared
}
