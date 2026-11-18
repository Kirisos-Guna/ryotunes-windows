package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenCommand returns a command that opens a folder, file or URL with the
// user's default handler: xdg-open on Linux, the shell on Windows.
func OpenCommand(target string) *exec.Cmd { return openCommand(target) }

// DaemonCommand returns a command that starts ryotunesd in the background.
// It prefers the binary next to the running executable, so a portable
// Windows install works without being on PATH.
func DaemonCommand() *exec.Cmd {
	name := "ryotunesd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := name
	if self, err := os.Executable(); err == nil {
		if sibling := filepath.Join(filepath.Dir(self), name); fileExists(sibling) {
			path = sibling
		}
	}
	command := exec.Command(path)
	backgroundProcess(command)
	return command
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// NeedsLaunchProbe reports that the client must probe the daemon socket
// before trusting it: Windows has no socket activation, so a socket file can
// outlive its daemon.
const NeedsLaunchProbe = runtime.GOOS == "windows"
