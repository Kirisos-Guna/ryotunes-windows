//go:build windows

package platform

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// openCommand uses url.dll's FileProtocolHandler, which opens URLs in the
// default browser and folders in Explorer without `cmd /c start` quoting
// pitfalls.
func openCommand(target string) *exec.Cmd {
	command := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	return command
}

// backgroundProcess detaches a child from the caller's console and process
// group so it keeps running after the caller exits.
func backgroundProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}
