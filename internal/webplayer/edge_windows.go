//go:build windows

package webplayer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// findBrowser returns Brave (the listener's choice), else Microsoft Edge
// (every Windows 10/11 has it) or Google Chrome. All three are Chromium with
// a licensed Widevine module.
func findBrowser() (string, error) {
	for _, name := range []string{"brave.exe", "msedge.exe", "chrome.exe"} {
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
		filepath.Join(os.Getenv("ProgramFiles"), `BraveSoftware\Brave-Browser\Application\brave.exe`),
		filepath.Join(os.Getenv("LOCALAPPDATA"), `BraveSoftware\Brave-Browser\Application\brave.exe`),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
	} {
		if fileExists(path) {
			return path, nil
		}
	}
	return "", errors.New("No browser for Spotify was found. Spotify playback needs Brave, Microsoft Edge or Google Chrome.")
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
	procGetClassName             = user32.NewProc("GetClassNameW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
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

// browserWindows lists pid's browser windows (class Chrome_WidgetWin_1),
// not its helper windows such as "Default IME".
func browserWindows(pid int) []uintptr {
	var found []uintptr
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if int(owner) != pid {
			return 1
		}
		class := make([]uint16, 64)
		n, _, _ := procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		if windows.UTF16ToString(class[:n]) != "Chrome_WidgetWin_1" {
			return 1
		}
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		found = append(found, hwnd)
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
		if !show && parked(hwnd, ex) {
			continue // already out of sight: touching it would only flicker
		}
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
			if parked(hwnd, ex) {
				procShowWindow.Call(hwnd, swShowNoActivate)
				continue
			}
			ex = (ex | wsExToolWindow | wsExNoActivate) &^ wsExAppWindow
			procSetWindowLongPtr.Call(hwnd, gwlExStyle, ex)
			setWindowPos(hwnd, offscreenX(), 0, windowWidth, windowHeight, swpNoZOrder|swpNoActivate)
			procShowWindow.Call(hwnd, swShowNoActivate)
		}
	}
	return len(found) > 0
}

// parked reports a window already off the taskbar and beyond the monitors.
func parked(hwnd, ex uintptr) bool {
	if ex&wsExToolWindow == 0 || ex&wsExAppWindow != 0 {
		return false
	}
	var rect struct{ Left, Top, Right, Bottom int32 }
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	return int(rect.Right) <= systemMetric(smXVirtualScreen)
}

// closeBrowser asks the browser's windows to close, as clicking × would, so
// it saves its profile (the Spotify sign-in) before exiting.
func closeBrowser(pid int) {
	kill := exec.Command("taskkill", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = kill.Run()
}

// closeLeftovers ends browsers still running on the player's profile (a
// previous Ryotunes that did not close it). Chromium allows one browser per
// profile: a new launch would only hand its window to the old one, which
// runs without the player extension.
func closeLeftovers(profile string) {
	script := "Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and $_.CommandLine.Contains('--user-data-dir=" +
		strings.ReplaceAll(profile, "'", "''") + "') } | ForEach-Object { $_.ProcessId }"
	query := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	query.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := query.Output()
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil && pid > 0 {
			kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
			kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
			_ = kill.Run()
		}
	}
}

const swRestore = 9

// raiseSignInWindows restores, centres and brings forward pid's windows
// that are not the parked player: the browser opens a new window minimised,
// as its process belongs to the hidden player, and Windows does not let it
// come forward by itself.
func raiseSignInWindows(pid int) bool {
	raised := false
	for _, hwnd := range browserWindows(pid) {
		ex, _, _ := procGetWindowLongPtr.Call(hwnd, gwlExStyle)
		if ex&wsExToolWindow != 0 {
			continue // the player
		}
		procShowWindow.Call(hwnd, swRestore)
		x := max(0, (systemMetric(smCXScreen)-windowWidth)/2)
		y := max(0, (systemMetric(smCYScreen)-windowHeight)/2)
		setWindowPos(hwnd, x, y, windowWidth, windowHeight, swpNoZOrder)
		procSetForegroundWindow.Call(hwnd)
		raised = true
	}
	return raised
}
