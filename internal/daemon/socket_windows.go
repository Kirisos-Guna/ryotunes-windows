//go:build windows

package daemon

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// removeStaleSocket deletes a socket left by a daemon that did not exit
// cleanly. Windows stores AF_UNIX sockets as reparse points that neither
// os.Lstat nor os.Remove can open (ERROR_CANT_ACCESS_FILE), so this calls
// DeleteFileW directly. The caller holds the instance lock and the path is in
// the private runtime directory, so whatever is there belongs to a dead daemon.
func removeStaleSocket(socketPath string) error {
	name, err := windows.UTF16PtrFromString(socketPath)
	if err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	err = windows.DeleteFile(name)
	if err == nil || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return nil
	}
	return fmt.Errorf("remove stale socket: %w", err)
}
