//go:build !linux

package algorithm

import (
	"errors"
	"os"
)

func renameDirectoryNoReplace(src, dst string) error {
	_, statErr := os.Lstat(dst)
	switch {
	case statErr == nil:
		return os.ErrExist
	case !errors.Is(statErr, os.ErrNotExist):
		return statErr
	}

	return os.Rename(src, dst)
}
