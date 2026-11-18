//go:build windows

package platform

import (
	"os"
	"path/filepath"
)

func init() {
	local := os.Getenv("LOCALAPPDATA")
	roaming := os.Getenv("APPDATA")
	if home, err := os.UserHomeDir(); err == nil {
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		if roaming == "" {
			roaming = filepath.Join(home, "AppData", "Roaming")
		}
	}
	if local == "" || roaming == "" {
		return
	}
	defaults := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(roaming, "Ryotunes", "config"),
		"XDG_DATA_HOME":   filepath.Join(local, "Ryotunes", "data"),
		"XDG_STATE_HOME":  filepath.Join(local, "Ryotunes", "state"),
		"XDG_CACHE_HOME":  filepath.Join(local, "Ryotunes", "cache"),
		// The socket lives under %TEMP%: it is per-user and ephemeral, and
		// some filesystem filters reject AF_UNIX connects elsewhere under
		// %LOCALAPPDATA% with WSAEINVAL.
		"XDG_RUNTIME_DIR": filepath.Join(os.TempDir(), "Ryotunes"),
	}
	for key, value := range defaults {
		if current := os.Getenv(key); current == "" || !filepath.IsAbs(current) {
			_ = os.Setenv(key, value)
		}
	}
	_ = os.MkdirAll(defaults["XDG_RUNTIME_DIR"], 0o700)
	prependExecutableDir()
}

// prependExecutableDir puts the install folder (and its tools\ subfolder)
// first on PATH, so tools shipped next to ryotunes.exe — yt-dlp, ffmpeg,
// ffprobe — are found before any older copy elsewhere. libmpv-2.dll needs no
// help: Windows always searches the executable's folder for DLLs.
func prependExecutableDir() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(self)
	path := os.Getenv("PATH")
	for _, extra := range []string{filepath.Join(dir, "tools"), dir} {
		if info, err := os.Stat(extra); err == nil && info.IsDir() {
			path = extra + string(os.PathListSeparator) + path
		}
	}
	_ = os.Setenv("PATH", path)
}
