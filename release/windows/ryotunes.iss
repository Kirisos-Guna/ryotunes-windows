; Inno Setup script for Ryotunes for Windows.
; Built by release\windows\build-windows.ps1, which passes:
;   /DAppVersion=<version>  /DStageDir=<folder with the staged program files>
;   /DOutputDir=<where to write the installer>

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

[Setup]
AppId={{6F1C5B8E-3A2D-4E7B-9C41-2B7D0E5A9F13}
AppName=Ryotunes
AppVersion={#AppVersion}
AppVerName=Ryotunes {#AppVersion}
AppPublisher=Ryotunes for Windows (unofficial port)
AppPublisherURL=https://github.com/Kirisos-Guna/ryotunes-windows
AppSupportURL=https://github.com/Kirisos-Guna/ryotunes-windows/issues
AppUpdatesURL=https://github.com/Kirisos-Guna/ryotunes-windows/releases
; Per-user install: no administrator prompt.
PrivilegesRequired=lowest
DefaultDirName={localappdata}\Programs\Ryotunes
DisableProgramGroupPage=yes
DefaultGroupName=Ryotunes
LicenseFile={#StageDir}\LICENSE
OutputDir={#OutputDir}
OutputBaseFilename=Ryotunes-Setup-{#AppVersion}-x64
SetupIconFile=ryotunes.ico
UninstallDisplayIcon={app}\ryotunes.ico
UninstallDisplayName=Ryotunes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
CloseApplications=force

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#StageDir}\ryotunes.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\ryotunesd.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\ryotunes-cli.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\libmpv-2.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\tools\yt-dlp.exe"; DestDir: "{app}\tools"; Flags: ignoreversion
Source: "{#StageDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\NOTICE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\THIRD_PARTY_NOTICES"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\BUNDLED-LICENSES.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\README-WINDOWS.txt"; DestDir: "{app}"; Flags: ignoreversion isreadme
Source: "ryotunes.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Ryotunes"; Filename: "{app}\ryotunes.exe"; IconFilename: "{app}\ryotunes.ico"
Name: "{group}\Uninstall Ryotunes"; Filename: "{uninstallexe}"
Name: "{autodesktop}\Ryotunes"; Filename: "{app}\ryotunes.exe"; IconFilename: "{app}\ryotunes.ico"; Tasks: desktopicon

[Run]
Filename: "{app}\ryotunes.exe"; Description: "{cm:LaunchProgram,Ryotunes}"; Flags: nowait postinstall skipifsilent
; In-app updates run Setup silently with /RELAUNCH=1: reopen Ryotunes after.
Filename: "{app}\ryotunes.exe"; Flags: nowait; Check: ShouldRelaunch

[UninstallRun]
; Stop the background player so its files can be removed.
Filename: "{app}\ryotunes-cli.exe"; Parameters: "quit"; Flags: runhidden; RunOnceId: "QuitDaemon"
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM ryotunes.exe"; Flags: runhidden; RunOnceId: "KillClient"
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM ryotunesd.exe"; Flags: runhidden; RunOnceId: "KillDaemon"

[Code]
procedure StopRunningCopies();
var
  Code: Integer;
begin
  { An upgrade must replace ryotunesd.exe, which keeps running after the
    window closes. }
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM ryotunes.exe', '', SW_HIDE, ewWaitUntilTerminated, Code);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM ryotunesd.exe', '', SW_HIDE, ewWaitUntilTerminated, Code);
end;

function ShouldRelaunch(): Boolean;
begin
  Result := ExpandConstant('{param:RELAUNCH|0}') = '1';
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  { Started by the running daemon: let it answer the update request first. }
  if ShouldRelaunch() then
    Sleep(2500);
  StopRunningCopies();
  Result := '';
end;
