package platform

import (
	"os"
	"runtime"
)

// SyncDir flushes a directory so a rename into it survives a crash. Windows
// cannot fsync a directory handle (it reports access denied); NTFS journals
// the rename itself, so there it is a no-op.
func SyncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// SecureSocket restricts a Unix socket to its owner. Windows AF_UNIX sockets
// reject chmod; their access comes from the per-user parent directory.
func SecureSocket(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, 0o600)
}
