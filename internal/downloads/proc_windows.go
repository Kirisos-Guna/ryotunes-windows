//go:build windows

package downloads

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

func newProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}

func lowerPriority(process *os.Process) {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_INFORMATION, false, uint32(process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	_ = windows.SetPriorityClass(handle, windows.BELOW_NORMAL_PRIORITY_CLASS)
}

// terminateGroup ends yt-dlp and the ffmpeg children it started. Windows has
// no SIGTERM for console-less processes, so the first attempt asks taskkill
// to close the tree and the forced one kills it.
func terminateGroup(process *os.Process, force bool) {
	args := []string{"/T", "/PID", strconv.Itoa(process.Pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if kill.Run() != nil && force {
		_ = process.Kill()
	}
}
