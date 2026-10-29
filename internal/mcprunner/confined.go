package mcprunner

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Confined file access.
//
// The Runner process is root; the plugin code it starts is not. Everything below
// a plugin's directory is written by that plugin's account uid, so the Runner
// must treat it as hostile input: a symlink planted there must never make a
// root-side read, write, chown or delete land in another account's tree.
//
// Every root-side operation inside an account therefore goes through an
// *os.Root, which resolves each path element with openat(O_NOFOLLOW) relative
// to a directory descriptor and refuses a symlink that leaves the root. The
// descriptor is anchored at a directory the account cannot rename (the
// root-owned account directory), never at a path the account can swap.

// chownAt is the only place ownership changes. Tests replace it.
var chownAt = func(r *os.Root, name string, uid, gid int) error { return r.Lchown(name, uid, gid) }

// chownIn gives one entry (never following a final symlink) to the account's
// uid. A nil credential (single-tenant Runner, tests) leaves ownership alone.
func chownIn(cred *syscall.Credential, r *os.Root, name string) error {
	if cred == nil {
		return nil
	}
	return chownAt(r, name, int(cred.Uid), int(cred.Gid))
}

// chownTreeIn gives name and everything below it to the account's uid without
// following a symlink. A regular file with more than one link is skipped: it may
// be a hard link to a file the account does not own.
func chownTreeIn(cred *syscall.Credential, r *os.Root, name string) error {
	if cred == nil {
		return nil
	}
	info, err := r.Lstat(name)
	if err != nil {
		return err
	}
	if err := chownIn(cred, r, name); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	sub, err := r.OpenRoot(name)
	if err != nil {
		return err
	}
	defer sub.Close()
	return fs.WalkDir(sub.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				if st, ok := info.Sys().(*syscall.Stat_t); ok && uint64(st.Nlink) > 1 {
					return nil
				}
			}
		}
		return chownIn(cred, sub, path)
	})
}

// openDirRoot opens a directory as a Root. The directory itself must be a real
// directory, not a symlink.
func openDirRoot(dir string) (*os.Root, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	return os.OpenRoot(dir)
}

// openRealDir opens a sub-directory of r that must not be a symlink.
func openRealDir(r *os.Root, name string) (*os.Root, error) {
	info, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a real directory", name)
	}
	return r.OpenRoot(name)
}

// pluginsRoot opens <state>/plugins. Where the Runner serves an account the
// walk starts at the root-owned account directory and passes state/ and
// plugins/ one element at a time, so a swapped element fails instead of
// redirecting.
func (r *Runner) pluginsRoot() (*os.Root, error) {
	r.mu.RLock()
	dir := r.stateDir
	r.mu.RUnlock()
	if dir == "" {
		return nil, errors.New("runner installation is disabled")
	}
	if r.opts.ManifestDir == "" {
		top, err := openDirRoot(dir)
		if err != nil {
			return nil, err
		}
		defer top.Close()
		return openRealDir(top, "plugins")
	}
	if dir != filepath.Join(r.opts.ManifestDir, "state") {
		return nil, errors.New("runner state directory is not the account's state directory")
	}
	top, err := openDirRoot(r.opts.ManifestDir)
	if err != nil {
		return nil, err
	}
	defer top.Close()
	state, err := openRealDir(top, "state")
	if err != nil {
		return nil, err
	}
	defer state.Close()
	return openRealDir(state, "plugins")
}

// pluginRoot opens one plugin's directory.
func (r *Runner) pluginRoot(id string) (*os.Root, error) {
	plugins, err := r.pluginsRoot()
	if err != nil {
		return nil, err
	}
	defer plugins.Close()
	return openRealDir(plugins, id)
}

// pluginRoot opens the directory of the plugin the spec describes.
func (s Spec) pluginRoot() (*os.Root, error) {
	if s.open != nil {
		return s.open()
	}
	return openDirRoot(s.WorkDir)
}

// relIn turns an absolute path below dir into a path relative to it.
func relIn(dir, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if !within(dir, path) {
		return "", fmt.Errorf("%s is outside %s", path, dir)
	}
	return filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
}

// readInPlugin reads a file below the plugin directory without following a
// symlink out of it.
func (s Spec) readInPlugin(path string) ([]byte, error) {
	if filepath.IsAbs(path) && !within(s.WorkDir, path) && s.Cred == nil {
		return os.ReadFile(path) // single-tenant Runner: the operator's own manifest
	}
	rel, err := relIn(s.WorkDir, path)
	if err != nil {
		return nil, err
	}
	root, err := s.pluginRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(rel)
}

func (s Spec) lstatInPlugin(path string) (fs.FileInfo, error) {
	if filepath.IsAbs(path) && !within(s.WorkDir, path) && s.Cred == nil {
		return os.Lstat(path)
	}
	rel, err := relIn(s.WorkDir, path)
	if err != nil {
		return nil, err
	}
	root, err := s.pluginRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Lstat(rel)
}

// writeInPlugin replaces a file below the plugin directory atomically with mode
// 0600. A symlink at the final name is replaced, never followed.
func (s Spec) writeInPlugin(name string, data []byte) error {
	root, err := s.pluginRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	return writeAtomicIn(root, name, data)
}

func (s Spec) removeInPlugin(name string) error {
	root, err := s.pluginRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(name)
}

// writeAtomicIn writes name below root through a temporary file in the same
// directory, then renames it over the target.
func writeAtomicIn(root *os.Root, name string, data []byte) error {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), ".tmp-"+hex.EncodeToString(suffix))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	cleanup := func() { _ = root.Remove(tmp) }
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		cleanup()
		return err
	}
	return nil
}
