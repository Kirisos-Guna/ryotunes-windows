//go:build !windows

package daemon

import "syscall"

func setUmask(mask int) int { return syscall.Umask(mask) }
