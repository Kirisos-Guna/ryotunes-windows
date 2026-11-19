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
| YouTube / SoundCloud sign-in | Imports the session from the browsers' cookie stores (`peanuts`/keyring AES-CBC) | Never reads other browsers' cookies (Windows Security flags DPAPI cookie decryption as credential theft). The listener signs in in a window of Ryotunes' own browser profile, and Ryotunes' extension hands over the session through the browser's cookie API (`internal/provider/webcookies`, `internal/webplayer/browser.go`) |
| Spotify decoders | go-librespot FLAC/MP3/Vorbis via CGO | `third_party/go-librespot` is vendored with `!cgo` stubs; Ryotunes plays Spotify audio through libmpv, so they are never used |
| Self update | pacman / xbps / dnf via pkexec | Downloads this repository's `Ryotunes-Setup-<version>-x64.exe`, verifies its SHA-256 and runs it silently (`internal/update/windows.go`) |
| Window maximize / fullscreen | Gio defaults | `third_party/gioui` is vendored with Windows fixes: maximize fills exactly the work area (no ghost caption buttons when the work area is inset), fullscreen covers the whole monitor, and the window opens centered inside the work area (a saved size that would not fit opens at 80% of it, `internal/platform/window_windows.go`) |

## Spotify playback

Spotify no longer gives librespot clients the keys to decrypt its audio
("failed retrieving aes key with code 1"), and Ryotunes does not circumvent
Spotify's DRM. On Windows, Spotify tracks play in **Spotify's own Web
Player** instead, the way Myspoti does:

- `internal/webplayer` starts Brave (else Microsoft Edge or Chrome) with its
  own profile in `%LOCALAPPDATA%\Ryotunes\spotify-player` and opens
  open.spotify.com. The browser's licensed Widevine module decrypts the audio
  (Ryotunes opts its own Brave profile in to Widevine, which Brave keeps off
  by default), and plays count as in a browser.
- The browser is not automated. Spotify refuses Widevine licences (HTTP 403)
  to a page that reports automation (`navigator.webdriver`, set by remote
  debugging) and skips to other songs. So, as Myspoti's Android host injects
  its bridge into a WebView, Ryotunes loads a small extension of its own
  (`internal/webplayer/extension`) into the player profile: it injects
  Myspoti's `player-bridge.js` into the page and relays its messages over a
  local WebSocket that needs a per-launch token.
- The window stays shown but parked beyond the edge of every monitor as a tool
  window (off the taskbar and Alt+Tab): Chromium does not load media in a
  hidden window.
- The first time, playing a Spotify track opens the window on Spotify's
  sign-in page; it parks itself again once you have signed in.
- `internal/player/web.go` routes `webplayer:spotify:track:<id>` streams to it
  and turns the page's state into the same events mpv sends, so the queue,
  pause, seek, volume and end-of-track work unchanged. mpv's equalizer and
  effects do not apply to Spotify tracks.

There are no ads on a Premium account. A free account hears Spotify's own ads,
as in the Web Player, and Spotify may play another song than the one picked;
the player follows what Spotify plays. Ryotunes adds no ad blocking of its own.

The Spotify Home feed is the account's own (`home` query, run in the Web
Player page, as Myspoti does; its hash comes from Myspoti's operation table).

## Myspoti extensions

`internal/extensions` runs extensions written for Myspoti's extension API in a
JavaScript sandbox (goja). Install one by copying its folder (with
`manifest.json` and `main.js`) into

    %APPDATA%\Ryotunes\config\ryotunes\extensions
yotunesextensions

and restarting Ryotunes (Quit, then open it again). Supported today: the
`lyrics` hook, asked before Ryotunes' built-in lyrics sources, with
`myspoti.http` (the manifest's `network` hosts enforced, https only, no local
addresses, 120 requests a minute), `query`, `settings.get` (manifest defaults,
or `settings.json` in the folder), `storage`, `log`/`console` and `util`.
Other hooks load and are ignored. `internal/extensions/testdata` holds
Myspoti's sample LRCLIB extension, run unchanged by the tests.

## Visualizer and scrolling

- The visualizer captures the default output device with WASAPI loopback
  (`internal/client/spectrum/loopback_windows.go`) instead of cava and
  pw-record, and computes the 64 spectrum bands itself (`analyzer.go`), so all
  four styles work, for mpv and the Spotify Web Player alike.
- The vendored Gio spreads classic mouse-wheel notches over a few frames
  (`third_party/gioui/app/smoothscroll_windows.go`); touchpads are unchanged.

## Not available on Windows

- MPRIS, media keys and the tray icon use D-Bus. The daemon logs that they are
  unavailable and carries on.
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
