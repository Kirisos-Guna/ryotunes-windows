//go:build !windows

package spawn

import (
	"os/exec"
	"syscall"
)

func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
