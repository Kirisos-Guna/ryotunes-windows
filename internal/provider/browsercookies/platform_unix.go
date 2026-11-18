//go:build !windows

package browsercookies

func platformRoots() ([]string, bool) { return nil, false }

func platformDecrypt(Store, []byte, string) (string, bool) { return "", false }

func platformDefaultBrowser() (string, bool) { return "", false }
