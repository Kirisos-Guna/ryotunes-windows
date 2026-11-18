//go:build windows

package discord

import (
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// pipePaths lists the named pipes Discord (and Vesktop) listen on.
func pipePaths() []string {
	paths := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		paths = append(paths, fmt.Sprintf(`\.\pipe\discord-ipc-%d`, i))
	}
	return paths
}

func dialIPC(path string, timeout time.Duration) (net.Conn, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			return &pipeConn{File: os.NewFile(uintptr(handle), path), path: path}, nil
		}
		if err != windows.ERROR_PIPE_BUSY || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pipeConn adapts a synchronous named-pipe handle to net.Conn. Deadlines are
// best effort: a deadline closes the pipe when it expires, which unblocks any
// pending read or write the same way a socket timeout would.
type pipeConn struct {
	*os.File
	path  string
	timer *time.Timer
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.path) }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.path) }

func (c *pipeConn) SetDeadline(t time.Time) error {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if !t.IsZero() {
		c.timer = time.AfterFunc(time.Until(t), func() { _ = c.File.Close() })
	}
	return nil
}

func (c *pipeConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *pipeConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

func (c *pipeConn) Close() error {
	if c.timer != nil {
		c.timer.Stop()
	}
	return c.File.Close()
}
