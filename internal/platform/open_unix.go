//go:build !windows

package platform

import "os/exec"

func openCommand(target string) *exec.Cmd { return exec.Command("xdg-open", target) }

func backgroundProcess(*exec.Cmd) {}
