//go:build !windows

package downloads

import (
	"os"
	"os/exec"
	"syscall"
)

func newProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func lowerPriority(process *os.Process) {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, process.Pid, 10)
}

// terminateGroup signals yt-dlp and the ffmpeg children it started.
func terminateGroup(process *os.Process, force bool) {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-process.Pid, signal)
}
