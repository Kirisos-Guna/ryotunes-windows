# Windows port

This repository is an unofficial Windows port of Ryotunes 2, the Go rewrite
that lives on the `hyperion` branch of
[ryoku-dev/ryotunes](https://github.com/ryoku-dev/ryotunes). The application
code is upstream's; the changes below only make it build and run on Windows.
Linux builds keep their upstream behaviour: every Windows path sits behind a
`_windows.go` file or a `runtime.GOOS` check.

## Build

Requirements: Go (`winget install GoLang.Go`) and, for the installer,
Inno Setup 6 (`winget install JRSoftware.InnoSetup`). No C compiler is needed.

```powershell
powershell -ExecutionPolicy Bypass -File release\windows\build-windows.ps1
```

This compiles the three programs with `CGO_ENABLED=0`, downloads the pinned
`libmpv-2.dll` and `yt-dlp.exe` (checked by SHA-256), and writes the
per-user installer `dist\Ryotunes-Setup-<version>-x64.exe`
(`release\windows\ryotunes.iss`).

For a quick development build:

```powershell
$env:CGO_ENABLED = "0"
go build -o dist\ryotunes.exe  -ldflags "-H windowsgui" .\cmd\ryotunes
go build -o dist\ryotunesd.exe -ldflags "-H windowsgui" .\cmd\ryotunesd
```

Put `libmpv-2.dll` next to `ryotunesd.exe` and `yt-dlp.exe` on PATH or in a
`tools\` folder beside it.

## What changed

| Area | Linux upstream | Windows port |
|---|---|---|
| DPI awareness | Gio sets per-monitor DPI awareness on its window thread | Also declared process-wide at startup (`internal/platform/dpi_windows.go`), so DLLs other programs inject (e.g. Windhawk window-animation mods) see real coordinates and animate the window in place |
| Directories | XDG Base Directory variables | `internal/platform` maps them to `%APPDATA%\Ryotunes`, `%LOCALAPPDATA%\Ryotunes` and `%TEMP%\Ryotunes` before any package reads them |
| Daemon socket | Unix socket, systemd socket activation | The same AF_UNIX socket (Windows 10 1803+) under `%TEMP%`; the client probes it and starts `ryotunesd.exe` from its own folder |
| Stale sockets | `Lstat` + mode check | Windows stores AF_UNIX sockets as reparse points that `os.Lstat`/`os.Remove` cannot open, so `DeleteFileW` removes them under the instance lock |
| Socket and directory permissions | `umask`, `chmod 0600`, directory `fsync` | No-ops; the per-user profile ACL protects the files and NTFS journals renames |
| Client singleton lock | `flock(2)` | `gofrs/flock` (LockFileEx) |
| Child processes | `Setsid` / `Setpgid`, `kill(-pgid)`, `setpriority` | `CREATE_NEW_PROCESS_GROUP`, `taskkill /T`, `BELOW_NORMAL_PRIORITY_CLASS` |
| Opening folders and URLs | `xdg-open` | `url.dll,FileProtocolHandler` |
| Discord Rich Presence | `$XDG_RUNTIME_DIR/discord-ipc-N` | `\\.\pipe\discord-ipc-N` |
| Browser cookie import | Linux profile paths, `peanuts`/keyring AES-CBC | Windows profile paths, DPAPI-unwrapped AES-GCM `v10` keys, default browser from the registry |
| Spotify decoders | go-librespot FLAC/MP3/Vorbis via CGO | `third_party/go-librespot` is vendored with `!cgo` stubs; Ryotunes plays Spotify audio through libmpv, so they are never used |
| Self update | pacman / xbps / dnf via pkexec | Downloads this repository's `Ryotunes-Setup-<version>-x64.exe`, verifies its SHA-256 and runs it silently (`internal/update/windows.go`) |
| Window maximize / fullscreen | Gio defaults | `third_party/gioui` is vendored with Windows fixes: maximize fills exactly the work area (no ghost caption buttons when the work area is inset), fullscreen covers the whole monitor, and the window opens centered inside the work area (a saved size that would not fit opens at 80% of it, `internal/platform/window_windows.go`) |

## Not available on Windows

- MPRIS, media keys and the tray icon use D-Bus. The daemon logs that they are
  unavailable and carries on.
- The spectrum visualiser reads PipeWire through `pw-record`/`cava`.
- Ryoku desktop theming (matugen, Hyprland window rules).

## Tests

`go vet ./...` is clean on Windows. Several upstream test packages still fail
on Windows. They put sockets in long `t.TempDir()` paths, which go over the
AF_UNIX path limit; use file names Windows does not allow; or need libmpv on
the test machine. They are not application failures. Porting them is open
work.

## Upstream Linux CI

The upstream GitHub workflows (Arch, Fedora and Void packaging) are kept in
`release/linux-ci/` for reference. Only `.github/workflows/windows.yml` runs
here.

## Staying up to date with upstream

`.github/workflows/upstream-sync.yml` runs every day (and on demand from the
Actions tab). It compares `release/windows/UPSTREAM_COMMIT` with the head of
upstream's `hyperion` branch. When upstream has moved, it:

1. applies the upstream diff on top of this port (`git apply --3way`),
2. runs `go vet` and `go build` on Windows,
3. builds the installer,
4. commits to `main`, tags the next version and publishes a release.

The version follows upstream's `VERSION` when that is newer than the last tag,
and otherwise bumps the patch number. If the changes conflict or the build
fails, the workflow publishes nothing and opens an issue. GitHub emails you
about it. Fix the port, set `UPSTREAM_COMMIT` to the upstream commit you
merged, and push.

Installed copies find new releases on their own. About 20 seconds after
Ryotunes opens, it checks this repository's releases and shows a notice when
one is newer. **Settings › About › Install** downloads the new
`Ryotunes-Setup-<version>-x64.exe`, checks its SHA-256, runs it silently and
reopens Ryotunes. This works for copies installed with the Setup program;
portable copies only link to the release page.
