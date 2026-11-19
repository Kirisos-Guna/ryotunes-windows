//go:build windows

package webplayer

import (
	"os"
	"testing"
)

// keepParked looks for the player's windows every second: that must not run
// out of Windows callbacks (Go allows about 2000), which crashed the daemon
// after about half an hour.
func TestBrowserWindowsCanBeListedForHours(t *testing.T) {
	for range 3000 {
		browserWindows(os.Getpid())
	}
}
