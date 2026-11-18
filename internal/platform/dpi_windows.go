//go:build windows

package platform

import "golang.org/x/sys/windows"

var procSetProcessDpiAwarenessContext = windows.NewLazySystemDLL("user32.dll").NewProc("SetProcessDpiAwarenessContext")

// dpiAwarenessPerMonitorV2 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
const dpiAwarenessPerMonitorV2 = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4

// init declares per-monitor (v2) DPI awareness for the whole process, as a
// manifest would. Gio only sets it on its own window thread, which left the
// rest of the process DPI unaware. Code that other programs inject into
// every app (for example Windhawk's window-animation mods) then measured
// the Ryotunes window in scaled coordinates and drew its opening animation
// at twice the window's position: on a 200% display the window seemed to
// open from the bottom-right before snapping into place.
func init() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		_, _, _ = procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2)
	}
}
