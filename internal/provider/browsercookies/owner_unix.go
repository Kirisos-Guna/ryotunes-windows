//go:build !windows

package browsercookies

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports a directory mode 0700 owned by this user.
func ownedByCurrentUser(info os.FileInfo) bool {
	if info.Mode().Perm() != 0o700 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
