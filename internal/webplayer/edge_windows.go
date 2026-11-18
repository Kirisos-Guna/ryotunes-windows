//go:build windows

package webplayer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// findBrowser returns Microsoft Edge (preferred: every Windows 10/11 has it,
// with its licensed Widevine module) or Google Chrome.
func findBrowser() (string, error) {
	for _, name := range []string{"msedge.exe", "chrome.exe"} {
		for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
			key, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			path, _, err := key.GetStringValue("")
			key.Close()
			if err == nil && fileExists(path) {
				return path, nil
			}
		}
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
	} {
		if fileExists(path) {
			return path, nil
		}
	}
	return "", errors.New("Microsoft Edge was not found. Spotify playback needs Edge (or Google Chrome).")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func detachBrowser(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procGetWindowTextLength      = user32.NewProc("GetWindowTextLengthW")
	procGetWindowLongPtr         = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr         = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
)

const (
	swHide           = 0
	swShowNormal     = 1
	swShowNoActivate = 4

	wsExToolWindow = 0x00000080
	wsExAppWindow  = 0x00040000
	wsExNoActivate = 0x08000000
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010

	smCXScreen       = 0
	smCYScreen       = 1
	smXVirtualScreen = 76

	windowWidth  = 1000
	windowHeight = 760
)

// gwlExStyle is GWL_EXSTYLE (-20), sign-extended as the API expects.
var gwlExStyle = func() uintptr { index := int32(-20); return uintptr(index) }()

func systemMetric(index int) int {
	value, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(value))
}

// offscreenX is a left edge beyond every monitor.
func offscreenX() int { return systemMetric(smXVirtualScreen) - windowWidth - 3000 }

// browserLaunchArgs places the window beyond every monitor from the start.
func browserLaunchArgs() []string {
	return []string{"--window-position=" + strconv.Itoa(offscreenX()) + ",0"}
}

// browserWindows lists the top-level windows with a title owned by pid.
func browserWindows(pid int) []uintptr {
	var found []uintptr
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if int(owner) == pid {
			if length, _, _ := procGetWindowTextLength.Call(hwnd); length > 0 {
				found = append(found, hwnd)
			}
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return found
}

func setWindowPos(hwnd uintptr, x, y, width, height int, flags uintptr) {
	procSetWindowPos.Call(hwnd, 0, uintptr(int32(x)), uintptr(int32(y)), uintptr(width), uintptr(height), flags)
}

// showBrowser parks the player's window out of sight, or brings it forward
// (for signing in). Parked, the window stays shown, beyond the edge of every
// monitor, as a tool window: off the taskbar and Alt+Tab, never focused. It
// must not be hidden: Chromium does not load media in a hidden window, and
// the page would sit buffering.
func showBrowser(pid int, show bool) bool {
	found := browserWindows(pid)
	for _, hwnd := range found {
		ex, _, _ := procGetWindowLongPtr.Call(hwnd, gwlExStyle)
		procShowWindow.Call(hwnd, swHide) // the taskbar notices a style change on a re-show
		if show {
			ex = (ex &^ (wsExToolWindow | wsExNoActivate)) | wsExAppWindow
			procSetWindowLongPtr.Call(hwnd, gwlExStyle, ex)
			x := (systemMetric(smCXScreen) - windowWidth) / 2
			y := (systemMetric(smCYScreen) - windowHeight) / 2
			setWindowPos(hwnd, x, y, windowWidth, windowHeight, swpNoZOrder)
			procShowWindow.Call(hwnd, swShowNormal)
			procSetForegroundWindow.Call(hwnd)
		} else {
			ex = (ex | wsExToolWindow | wsExNoActivate) &^ wsExAppWindow
			procSetWindowLongPtr.Call(hwnd, gwlExStyle, ex)
			setWindowPos(hwnd, offscreenX(), 0, windowWidth, windowHeight, swpNoZOrder|swpNoActivate)
			procShowWindow.Call(hwnd, swShowNoActivate)
		}
	}
	return len(found) > 0
}
