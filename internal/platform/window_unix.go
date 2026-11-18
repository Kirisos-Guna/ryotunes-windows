//go:build !windows

package platform

// FitWindow returns the size (in dp) to open the main window at. Off Windows
// the window manager places and sizes windows, so the saved size is kept.
func FitWindow(width, height int) (int, int) { return width, height }
