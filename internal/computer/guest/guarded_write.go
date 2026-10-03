package guest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"golang.org/x/sys/unix"
)

func writeIdentityChanged() error {
	return tooloutcome.New(tooloutcome.Validation, "write_identity_changed", "not_executed", "File target changed or could not be bound to the reviewed identity. No file content was written; inspect the target before proposing the operation again.", "verify_target").Err()
}

// Guarded writes never reopen a path for mutation. Existing files are opened
// without truncation and checked by fstat; new files use a verified directory
// descriptor and exclusive creation. Every content operation uses that same fd.
func (s *Service) guardedWriteFile(ctx context.Context, bot string, args fileArgs, identity tooloutcome.Identity) (map[string]any, error) {
	if len(args.Content) > MaxFileBytes {
		return nil, tooloutcome.InvalidArguments("content exceeds file size limit")
	}
	expected := strings.TrimSpace(args.ExpectedSHA256)
	if expected != "" {
		hash, err := hex.DecodeString(expected)
		if err != nil || len(hash) != sha256.Size {
			return nil, tooloutcome.InvalidArguments("expected_sha256 must be a 64-character hexadecimal SHA-256")
		}
	}
	key := identity.Target
	if identity.Object != "" {
		key = "object/" + identity.Object
	}
	lock := s.fileWriteLock(key)
	if err := lockFileWrite(ctx, lock); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	f, err := s.openBoundWriteFile(ctx, bot, args, identity)
	if err != nil {
		return nil, err
	}
	result, writeErr := writeBoundDescriptor(ctx, f, args)
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return result, nil
}

func (s *Service) openBoundWriteFile(ctx context.Context, bot string, args fileArgs, identity tooloutcome.Identity) (*os.File, error) {
	if identity.GuardVersion != 1 || identity.ParentObject == "" || identity.Target == "" || !within(s.root, identity.Target) || !within(s.root, identity.Parent) {
		return nil, writeIdentityChanged()
	}
	current, err := s.fileIdentity(bot, args.Path)
	if err != nil || current["target"] != identity.Target || current["parent"] != identity.Parent || current["parent_object"] != identity.ParentObject {
		return nil, writeIdentityChanged()
	}
	object, _ := current["object"].(string)
	if object != identity.Object {
		return nil, writeIdentityChanged()
	}
	if identity.Object == "" && strings.TrimSpace(args.ExpectedSHA256) != "" {
		return nil, writeIdentityChanged()
	}
	fd, err := unix.Open(identity.Parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, writeIdentityChanged()
	}
	dir := os.NewFile(uintptr(fd), identity.Parent)
	defer func() { _ = dir.Close() }()
	info, err := dir.Stat()
	if err != nil || !info.IsDir() || fileObject(info) != identity.ParentObject {
		return nil, writeIdentityChanged()
	}
	rel, err := filepath.Rel(identity.Parent, filepath.Dir(identity.Target))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, writeIdentityChanged()
	}
	if rel != "." {
		for _, part := range strings.Split(rel, string(os.PathSeparator)) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			child, err := unix.Openat(int(dir.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if errors.Is(err, unix.ENOENT) && identity.Object == "" {
				if err = unix.Mkdirat(int(dir.Fd()), part, 0770); err != nil && !errors.Is(err, unix.EEXIST) {
					return nil, writeIdentityChanged()
				}
				child, err = unix.Openat(int(dir.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
			if err != nil {
				return nil, writeIdentityChanged()
			}
			_ = dir.Close()
			dir = os.NewFile(uintptr(child), part)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if args.Append {
		flags |= unix.O_APPEND
	}
	if identity.Object == "" {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err = unix.Openat(int(dir.Fd()), filepath.Base(identity.Target), flags, 0660)
	if err != nil {
		return nil, writeIdentityChanged()
	}
	f := os.NewFile(uintptr(fd), identity.Target)
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || (identity.Object != "" && fileObject(info) != identity.Object) {
		_ = f.Close()
		if identity.Object == "" {
			return nil, fmt.Errorf("could not verify newly created file descriptor")
		}
		return nil, writeIdentityChanged()
	}
	return f, nil
}

func writeBoundDescriptor(ctx context.Context, f *os.File, args fileArgs) (map[string]any, error) {
	if expected := strings.TrimSpace(args.ExpectedSHA256); expected != "" {
		hash, err := hashBoundDescriptor(ctx, f)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(expected, hash) {
			return nil, tooloutcome.New(tooloutcome.Validation, "write_identity_changed", "not_executed", fmt.Sprintf("%s: expected %s, current %s", errFileConflict, expected, hash), "verify_target").Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !args.Append {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		if err := f.Truncate(0); err != nil {
			return nil, err
		}
	}
	n, err := f.WriteString(args.Content)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	hash, err := hashBoundDescriptor(ctx, f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": args.Path, "bytes": n, "sha256": hash, "guard_version": 1}, nil
}

func hashBoundDescriptor(ctx context.Context, f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if err != nil {
			return "", err
		}
	}
}
