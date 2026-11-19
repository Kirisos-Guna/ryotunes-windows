//go:build windows

package browsercookies

// On Windows Ryotunes reads no browser's cookie store. Reading Chromium's
// cookies there means decrypting its key with DPAPI, which Windows Security
// stops as credential theft (Behavior:Win32/WebBrowserCredAccess), and
// Chrome, Edge and Brave lock their cookies to themselves anyway. YouTube and
// SoundCloud sign in in a window of Ryotunes' own browser instead (package
// webcookies). The Windows decryption code was removed with that change.

// platformRoots names no browser folders: Discover finds no stores, and a
// remembered store no longer validates.
func platformRoots() ([]string, bool) { return nil, true }

func platformDecrypt(Store, []byte, string) (string, bool) { return "", true }

func platformDefaultBrowser() (string, bool) { return "", false }
