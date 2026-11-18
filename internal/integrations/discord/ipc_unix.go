//go:build !windows

package discord

import (
	"net"
	"time"
)

func dialIPC(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}

// pipePaths is empty off Windows: Discord listens on a Unix socket.
func pipePaths() []string { return nil }
