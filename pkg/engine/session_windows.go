//go:build windows

package engine

import "golang.org/x/sys/windows"

// The temporary file is on the same volume as the target. Never remove the old
// session first: replacement must leave either the old or the new complete JSON.
func replaceSessionFile(temp, target string) error {
	from, err := windows.UTF16PtrFromString(temp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
