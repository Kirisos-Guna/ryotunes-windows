//go:build windows

package daemon

// setUmask is a no-op: Windows has no umask. The socket sits in the per-user
// runtime directory under %LOCALAPPDATA%, which only this user can open.
func setUmask(mask int) int { return 0 }
