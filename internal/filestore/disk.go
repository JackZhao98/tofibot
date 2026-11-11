package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// Disk keeps one flat directory: key "<id>.upload" is the file of that name.
// A key with a path separator is refused, so a hostile row cannot leave the root.
type Disk struct{ Root string }

func (d Disk) Kind() string { return "disk" }

func (d Disk) clean(key string) error {
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, "/\\") {
		return ErrNotFound
	}
	return nil
}

func diskErr(err error) error {
	// Both kinds stay matchable: callers that test os.IsNotExist keep working.
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w (%w)", ErrNotFound, err)
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w (%w)", ErrExists, err)
	}
	return err
}

func (d Disk) Put(_ context.Context, key string, src io.ReadSeeker, _ ObjectMeta) error {
	if err := d.clean(key); err != nil {
		return err
	}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err = root.Lstat(key); err == nil {
		return ErrExists
	}
	tmp := "." + key + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, src)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = root.Rename(tmp, key)
	}
	if err != nil {
		_ = root.Remove(tmp)
	}
	return err
}

func (d Disk) Open(_ context.Context, key string) (io.ReadSeekCloser, ObjectInfo, error) {
	if err := d.clean(key); err != nil {
		return nil, ObjectInfo{}, err
	}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	defer root.Close()
	f, err := root.Open(key)
	if err != nil {
		return nil, ObjectInfo{}, diskErr(err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, ObjectInfo{}, err
	}
	return f, ObjectInfo{Key: key, Size: info.Size()}, nil
}

func (d Disk) Stat(_ context.Context, key string) (ObjectInfo, error) {
	if err := d.clean(key); err != nil {
		return ObjectInfo{}, err
	}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer root.Close()
	info, err := root.Lstat(key)
	if err != nil {
		return ObjectInfo{}, diskErr(err)
	}
	return ObjectInfo{Key: key, Size: info.Size()}, nil
}

func (d Disk) Delete(_ context.Context, key string) error {
	if err := d.clean(key); err != nil {
		return nil
	}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (d Disk) List(_ context.Context, prefix string, fn func(ObjectInfo) error) error {
	entries, err := os.ReadDir(d.Root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if err = fn(ObjectInfo{Key: e.Name(), Size: info.Size()}); err != nil {
			return err
		}
	}
	return nil
}
