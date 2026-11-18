//go:build windows

package spawn

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach starts the program in its own process group with no console, so it
// outlives ryotunesd and is not hit by the daemon's console control events.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}
