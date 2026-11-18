package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ryoku-dev/ryotunes/internal/spawn"
	"github.com/ryoku-dev/ryotunes/internal/version"
)

// windowsSetupName is the installer asset the Windows release workflow
// publishes, with a "<name>.sha256" checksum beside it.
func windowsSetupName(version string) string {
	return "Ryotunes-Setup-" + version + "-x64.exe"
}

// detectWindowsInstallation accepts copies installed by the Setup program,
// recognised by the Inno Setup uninstaller next to the executable. Portable
// and development builds are never overwritten.
func detectWindowsInstallation() (installation, error) {
	if runtime.GOARCH != "amd64" {
		return installation{}, errors.New("Self update supports the x64 Windows installer. Download a release for your platform instead.")
	}
	executable, err := os.Executable()
	if err != nil {
		return installation{}, fmt.Errorf("Cannot determine the running installation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(executable), "unins000.exe")); err != nil {
		return installation{}, errors.New("This copy of Ryotunes was not installed with the Setup program, so it cannot update itself. Download the installer from the release page.")
	}
	return installation{manager: managerWindows, version: version.Version}, nil
}

// installWindows runs a verified installer silently, detached from the
// daemon. The installer stops Ryotunes after a short pause (so this request
// can still answer), replaces the files and starts Ryotunes again.
func installWindows(installer string) error {
	// The caller deletes its staging directory on return, while the
	// installer is still running, so run it from a copy of its own.
	dir, err := os.MkdirTemp("", "ryotunes-setup-")
	if err != nil {
		return fmt.Errorf("Could not prepare the installer: %v", err)
	}
	target := filepath.Join(dir, filepath.Base(installer))
	if err := copyInstaller(installer, target); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("Could not prepare the installer: %v", err)
	}
	command := spawn.Command(context.Background(), target,
		"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/RELAUNCH=1")
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("Could not start the installer: %v", err)
	}
	return command.Process.Release()
}

func copyInstaller(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
