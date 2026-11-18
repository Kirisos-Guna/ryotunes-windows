Ryotunes for Windows
====================

An unofficial Windows port of Ryotunes 2 (the Go rewrite), built from the
upstream "hyperion" branch of https://github.com/ryoku-dev/ryotunes.

Start
-----
Open Ryotunes from the Start menu. It starts the background player
(ryotunesd.exe) itself. The installer puts the program in
%LOCALAPPDATA%\Programs\Ryotunes; remove it from Settings > Apps.

Closing the window keeps the music playing in ryotunesd.exe. Choose Quit in
the app to stop playback and exit the player. With nothing playing and no
window open, ryotunesd.exe exits on its own after five minutes.

What is in this folder
----------------------
  ryotunes.exe       the app window
  ryotunesd.exe      the background player (playback, queue, downloads)
  ryotunes-cli.exe   command-line control, e.g.  ryotunes-cli toggle_pause
  libmpv-2.dll       audio engine (mpv)
  tools\yt-dlp.exe   YouTube stream resolver and downloader

Optional: FFmpeg
----------------
Downloads (Downloads page) and local-library duration scanning need
ffmpeg.exe and ffprobe.exe. Install them with

    winget install Gyan.FFmpeg

or drop both executables into the tools\ folder of the install directory.

Where your data lives
---------------------
  Settings       %APPDATA%\Ryotunes\config
  Library, logs  %LOCALAPPDATA%\Ryotunes\data and ...\state
  Audio cache    %LOCALAPPDATA%\Ryotunes\cache
  Socket, locks  %TEMP%\Ryotunes

Sign-in notes
-------------
YouTube sign-in can import cookies from a browser profile. Firefox (and
LibreWolf, Waterfox, Zen, Floorp) works best. Recent Chrome, Edge and Brave
builds protect cookies with "app-bound encryption", which other programs
cannot read; sign in with a Firefox-family browser if import finds nothing.

Linux-only features that are off on Windows
-------------------------------------------
  - MPRIS / media keys and the system tray (they use D-Bus)
  - The PipeWire spectrum visualiser
  - Ryoku desktop theming and in-app self-update (the app links to the
    release page instead)

Licence
-------
Ryotunes is GPL-3.0-or-later. See LICENSE, NOTICE, THIRD_PARTY_NOTICES and
BUNDLED-LICENSES.txt.
