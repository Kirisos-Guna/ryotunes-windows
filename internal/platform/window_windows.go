//go:build windows

package platform

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procSystemParameters = user32.NewProc("SystemParametersInfoW")
	procGetDpiForSystem  = user32.NewProc("GetDpiForSystem")
)

const spiGetWorkArea = 0x0030

// FitWindow returns the size (in dp) to open the main window at. Windows
// does not keep a remembered window inside the screen, so a saved size that
// would not fit (or that fills the screen, as after a maximize) opens at 80%
// of the primary monitor's work area instead; Maximize still fills it.
func FitWindow(width, height int) (int, int) {
	var area windows.Rect
	if ok, _, _ := procSystemParameters.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&area)), 0); ok == 0 {
		return width, height
	}
	dpi := 96
	if procGetDpiForSystem.Find() == nil {
		if value, _, _ := procGetDpiForSystem.Call(); value != 0 {
			dpi = int(value)
		}
	}
	areaW := int(area.Right-area.Left) * 96 / dpi
	areaH := int(area.Bottom-area.Top) * 96 / dpi
	if areaW <= 0 || areaH <= 0 {
		return width, height
	}
	if width >= areaW*95/100 || height >= areaH*95/100 {
		return areaW * 80 / 100, areaH * 80 / 100
	}
	return width, height
}
