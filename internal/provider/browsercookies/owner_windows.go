//go:build windows

package browsercookies

import "os"

// ownedByCurrentUser: Windows has no Unix mode bits. The runtime directory
// lives under the user's %LOCALAPPDATA%, whose ACL already admits only this
// user, SYSTEM and Administrators.
func ownedByCurrentUser(info os.FileInfo) bool { return info.IsDir() }
