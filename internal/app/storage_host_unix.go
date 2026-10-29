//go:build linux || darwin

package app

import "golang.org/x/sys/unix"

// hostDiskSpace reports the bytes a non-root process can still write and the
// size of the volume that holds path.
func hostDiskSpace(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err = unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	block := uint64(st.Bsize)
	return uint64(st.Bavail) * block, uint64(st.Blocks) * block, nil
}
