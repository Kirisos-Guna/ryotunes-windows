//go:build !windows

package webplayer

import (
	"errors"
	"os/exec"
)

// The Web Player engine is used by the Windows port only; Linux builds keep
// upstream's librespot playback.
func findBrowser() (string, error) {
	return "", errors.New("the Spotify Web Player engine is only used on Windows")
}

func detachBrowser(*exec.Cmd) {}

func showBrowser(int, bool) bool { return false }

func browserLaunchArgs() []string { return nil }

func closeBrowser(int) {}

func closeLeftovers(string) {}
