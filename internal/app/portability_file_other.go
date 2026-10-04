//go:build !linux && !darwin

package app

import (
	"errors"
	"os"
)

func openPortableLocalFile(root *os.Root, name string) (*os.File, error) {
	return nil, errors.New("safe local attachment export is unavailable")
}
func portableSingleLink(info os.FileInfo) bool { return false }
