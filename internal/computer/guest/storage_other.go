//go:build !linux && !darwin

package guest

import (
	"errors"
	"os"
)

func blobHasOneLink(os.FileInfo) bool { return false }

func blobReadFlags() (int, error) { return 0, errors.New("safe blob read unsupported") }

func workspaceStorage(string) (StorageStats, error) {
	return StorageStats{}, errors.New("workspace filesystem metrics unsupported")
}
