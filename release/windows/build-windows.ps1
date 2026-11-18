<#
.SYNOPSIS
  Builds the Ryotunes for Windows installer.

.DESCRIPTION
  Compiles ryotunes.exe (GUI), ryotunesd.exe (background daemon) and
  ryotunes-cli.exe with CGO disabled, fetches the pinned libmpv and yt-dlp
  runtime binaries (verified by SHA-256), stages everything, and wraps it in
  dist\Ryotunes-Setup-<version>-x64.exe (Inno Setup) with a .sha256 file.

  Run from the repository root:
    powershell -ExecutionPolicy Bypass -File release\windows\build-windows.ps1

.PARAMETER Version
  Version stamped into the binaries. Defaults to the VERSION file.
#>
[CmdletBinding()]
param(
  [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
Set-Location $root

if (-not $Version) { $Version = (Get-Content VERSION -Raw).Trim() }

# A Go installed in this session is not on PATH until a new shell starts.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  $goBin = Join-Path $env:ProgramFiles "Go\bin"
  if (Test-Path (Join-Path $goBin "go.exe")) { $env:PATH = "$goBin;$env:PATH" }
  else { throw "Go is not installed. Install it with: winget install GoLang.Go" }
}

# Pinned runtime dependencies. Update the URL and hash together.
$deps = @{
  mpv = @{
    Url    = "https://github.com/shinchiro/mpv-winbuild-cmake/releases/download/20261111/mpv-dev-x86_64-20261111-git-bcde6b93cc.7z"
    File   = "mpv-dev-x86_64-20261111-git-bcde6b93cc.7z"
    Sha256 = "273c079b11ea5575822f972756f4c9a71149a5c10130353abeb8586738938452"
  }
  ytdlp = @{
    Url    = "https://github.com/yt-dlp/yt-dlp/releases/download/2026.10.26/yt-dlp.exe"
    File   = "yt-dlp.exe"
    Sha256 = "f5389d3698ecde4ef06f4e6b1ce0294fe9ac1396228c8990b8d3416d6d177a7d"
  }
}

$cache = Join-Path $root "third_party-bin\dl"
New-Item -ItemType Directory -Force $cache | Out-Null

function Get-Pinned($dep) {
  $path = Join-Path $cache $dep.File
  if (-not (Test-Path $path) -or (Get-FileHash $path -Algorithm SHA256).Hash -ne $dep.Sha256.ToUpper()) {
    Write-Host "Downloading $($dep.Url)"
    Invoke-WebRequest -Uri $dep.Url -OutFile $path -UseBasicParsing
  }
  $hash = (Get-FileHash $path -Algorithm SHA256).Hash
  if ($hash -ne $dep.Sha256.ToUpper()) {
    Remove-Item $path -Force
    throw "Checksum mismatch for $($dep.File): got $hash"
  }
  return $path
}

$mpvArchive = Get-Pinned $deps.mpv
$ytdlp = Get-Pinned $deps.ytdlp
$mpvDir = Join-Path $cache "mpv"
if (-not (Test-Path (Join-Path $mpvDir "libmpv-2.dll"))) {
  New-Item -ItemType Directory -Force $mpvDir | Out-Null
  # Windows 10+ ships bsdtar, which reads 7z archives.
  & "$env:SystemRoot\System32\tar.exe" -xf $mpvArchive -C $mpvDir
  if ($LASTEXITCODE -ne 0) { throw "Could not extract $mpvArchive" }
}

$name = "Ryotunes-$Version-windows-x64"
$stage = Join-Path $root "dist\$name"
if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force (Join-Path $stage "tools") | Out-Null

$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$ldflags = "-s -w -X github.com/ryoku-dev/ryotunes/internal/version.Version=$Version"
$builds = @(
  @{ Out = "ryotunes.exe";     Pkg = "./cmd/ryotunes";     Gui = $true },
  @{ Out = "ryotunesd.exe";    Pkg = "./cmd/ryotunesd";    Gui = $true },
  @{ Out = "ryotunes-cli.exe"; Pkg = "./cmd/ryotunes-cli"; Gui = $false }
)
foreach ($b in $builds) {
  $flags = $ldflags
  if ($b.Gui) { $flags += " -H windowsgui" }
  Write-Host "Building $($b.Out)"
  go build -trimpath -ldflags $flags -o (Join-Path $stage $b.Out) $b.Pkg
  if ($LASTEXITCODE -ne 0) { throw "go build failed for $($b.Pkg)" }
}

Copy-Item (Join-Path $mpvDir "libmpv-2.dll") $stage
Copy-Item $ytdlp (Join-Path $stage "tools")
foreach ($f in "LICENSE", "NOTICE", "THIRD_PARTY_NOTICES") { Copy-Item $f $stage }
Copy-Item "release\windows\README-WINDOWS.txt" $stage
Copy-Item "release\windows\BUNDLED-LICENSES.txt" $stage

# Wrap the staged files in a per-user installer.
$iscc = @(
  (Get-Command ISCC.exe -ErrorAction SilentlyContinue | ForEach-Object Source),
  (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"),
  (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe"),
  (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $iscc) { throw "Inno Setup 6 is not installed. Install it with: winget install JRSoftware.InnoSetup" }

$outDir = Join-Path $root "dist"
$setup = Join-Path $outDir "Ryotunes-Setup-$Version-x64.exe"
if (Test-Path $setup) { Remove-Item -Force $setup }
& $iscc /Q "/DAppVersion=$Version" "/DStageDir=$stage" "/DOutputDir=$outDir" (Join-Path $PSScriptRoot "ryotunes.iss")
if ($LASTEXITCODE -ne 0) { throw "Inno Setup failed" }

$hash = (Get-FileHash $setup -Algorithm SHA256).Hash.ToLower()
"$hash  $(Split-Path $setup -Leaf)" | Set-Content -Encoding ascii "$setup.sha256"

Write-Host "Built $setup"
Write-Host "SHA-256 $hash"
