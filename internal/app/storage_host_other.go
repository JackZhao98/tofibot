//go:build !linux && !darwin

package app

import "errors"

func hostDiskSpace(string) (uint64, uint64, error) {
	return 0, 0, errors.New("host disk space is not measured on this platform")
}
