//go:build !windows

package engine

import (
	"os"
	"path/filepath"
)

func replaceSessionFile(temp, target string) error {
	if err := os.Rename(temp, target); err != nil {
		return err
	}
	// Best effort on filesystems that support directory syncing. The rename has
	// committed, so a directory-sync failure must not roll back in-memory state.
	if directory, err := os.Open(filepath.Dir(target)); err == nil {
		directory.Sync()
		directory.Close()
	}
	return nil
}
