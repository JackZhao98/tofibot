//go:build linux || darwin

package guest

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func blobHasOneLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func blobReadFlags() (int, error) { return unix.O_NOFOLLOW | unix.O_NONBLOCK, nil }

func workspaceStorage(path string) (StorageStats, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return StorageStats{}, err
	}
	block := uint64(stat.Bsize)
	return StorageStats{TotalBytes: stat.Blocks * block, UsedBytes: (stat.Blocks - stat.Bfree) * block, FreeBytes: stat.Bfree * block, AvailableBytes: stat.Bavail * block}, nil
}
