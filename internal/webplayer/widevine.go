package webplayer

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Spotify's audio is encrypted with Widevine. Chromium browsers download it
// into the profile a few minutes after a profile's first launch, so on a new
// install songs "played" without sound for those minutes. The player's
// profile is given a copy of the Widevine this computer already has (the
// listener's own Brave, Chrome or Edge), which works at once.

// widevinePlatform is the CDM's folder for this CPU.
func widevinePlatform() string {
	if runtime.GOARCH == "arm64" {
		return "win_arm64"
	}
	return "win_x64"
}

// widevineVersion returns the version of the CDM in dir (a folder holding
// manifest.json and _platform_specific), or "" when it is not a whole one.
func widevineVersion(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "_platform_specific", widevinePlatform(), "widevinecdm.dll")); err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version == "" || strings.ContainsAny(manifest.Version, `/\:`) {
		return ""
	}
	return manifest.Version
}

// hasWidevine reports a whole CDM in the profile.
func hasWidevine(profile string) bool {
	entries, err := os.ReadDir(filepath.Join(profile, "WidevineCdm"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && widevineVersion(filepath.Join(profile, "WidevineCdm", entry.Name())) != "" {
			return true
		}
	}
	return false
}

// widevineSources lists the CDM folders on this computer, from the
// listener's browsers' profiles and Chrome's installation.
func widevineSources() []string {
	var dirs []string
	add := func(pattern string) {
		matches, _ := filepath.Glob(pattern)
		dirs = append(dirs, matches...)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		for _, browser := range []string{`BraveSoftware\Brave-Browser`, `Google\Chrome`, `Microsoft\Edge`} {
			add(filepath.Join(local, browser, "User Data", "WidevineCdm", "*"))
		}
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if root != "" {
			add(filepath.Join(root, "Google", "Chrome", "Application", "*", "WidevineCdm"))
		}
	}
	return dirs
}

// seedWidevine copies the newest CDM on this computer into the profile when
// the profile has none. It reports whether the profile has one afterwards.
func seedWidevine(profile string) bool {
	if hasWidevine(profile) {
		return true
	}
	best, bestVersion := "", ""
	for _, dir := range widevineSources() {
		if version := widevineVersion(dir); version != "" && newerVersion(version, bestVersion) {
			best, bestVersion = dir, version
		}
	}
	if best == "" {
		return false
	}
	target := filepath.Join(profile, "WidevineCdm", bestVersion)
	staging := target + ".copying"
	_ = os.RemoveAll(staging)
	if err := copyTree(best, staging); err != nil {
		_ = os.RemoveAll(staging)
		return false
	}
	_ = os.RemoveAll(target)
	if err := os.Rename(staging, target); err != nil {
		_ = os.RemoveAll(staging)
		return false
	}
	return hasWidevine(profile)
}

// newerVersion compares dotted versions: a newer than b.
func newerVersion(a, b string) bool {
	if b == "" {
		return true
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
