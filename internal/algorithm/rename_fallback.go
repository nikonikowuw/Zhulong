//go:build !linux

package algorithm

import (
	"errors"
	"os"
)

func renameDirectoryNoReplace(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(src, dst)
}
