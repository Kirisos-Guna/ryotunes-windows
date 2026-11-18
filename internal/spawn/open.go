package spawn

import (
	"context"
	"os/exec"
	"runtime"
)

// Open returns a command that opens a URL, file or folder with the user's
// default handler, detached like Command: xdg-open on Linux, the Windows
// shell's URL handler on Windows.
func Open(ctx context.Context, target string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return Command(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", target)
	}
	return Command(ctx, "xdg-open", target)
}
