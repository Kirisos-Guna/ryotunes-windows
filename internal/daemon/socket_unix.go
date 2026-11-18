//go:build !windows

package daemon

import (
	"fmt"
	"os"
)

// removeStaleSocket deletes a socket left by a daemon that did not exit
// cleanly, refusing to delete anything that is not a socket.
func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("socket path exists and is not a socket: %s", socketPath)
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}
