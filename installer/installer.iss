; Bootstrap installer: downloads the latest release exe from GitHub at install time.
; Installs per user (no admin) because the app updates itself and writes its data next to the exe.
; Build: iscc /DAppVersion=1.2.3 installer\installer.iss

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#define AppName "Deadlock Counter-Build"
#define AppExe "deadlock-counter.exe"

[Setup]
AppId={{B6A4F0E5-3C1D-4F7A-9B52-6D2C8E1A7F10}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=LuGG000
AppPublisherURL=https://github.com/LuGG000/deadlock-counter-build
DefaultDirName={localappdata}\Programs\DeadlockCounter
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputBaseFilename=deadlock-counter-setup
OutputDir=Output
Compression=lzma2
SolidCompression=yes
SetupIconFile=..\assets\icon.ico
WizardStyle=modern
UninstallDisplayIcon={app}\{#AppExe}

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; Flags: unchecked

[Files]
Source: "{tmp}\{#AppExe}"; DestDir: "{app}"; Flags: external ignoreversion

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppExe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExe}"; Description: "Launch {#AppName}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Leftovers of the self-updater and re-downloadable caches. defaults.json and settings.json are kept.
Type: files; Name: "{app}\{#AppExe}.old"
Type: files; Name: "{app}\{#AppExe}.new"
Type: filesandordirs; Name: "{app}\icons"
Type: files; Name: "{app}\live_cache.json"
Type: files; Name: "{app}\tracklock_cache.json"
Type: files; Name: "{app}\skillorders_v2.json"

[Code]
const
  ExeUrl = 'https://github.com/LuGG000/deadlock-counter-build/releases/latest/download/{#AppExe}';

var
  DownloadPage: TDownloadWizardPage;

procedure InitializeWizard;
begin
  DownloadPage := CreateDownloadPage(SetupMessage(msgWizardPreparing), SetupMessage(msgPreparingDesc), nil);
end;

procedure StopRunningApp;
var
  ResultCode: Integer;
begin
  Exec('taskkill.exe', '/F /IM {#AppExe}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if CurPageID = wpReady then
  begin
    DownloadPage.Clear;
    DownloadPage.Add(ExeUrl, '{#AppExe}', '');
    DownloadPage.Show;
    try
      try
        DownloadPage.Download;
      except
        if not DownloadPage.AbortedByUser then
          SuppressibleMsgBox('The download failed. Please check your internet connection and try again.' + #13#10#13#10 + GetExceptionMessage, mbCriticalError, MB_OK, IDOK);
        Result := False;
      end;
    finally
      DownloadPage.Hide;
    end;
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  StopRunningApp;
  Result := '';
end;

function InitializeUninstall: Boolean;
begin
  StopRunningApp;
  Result := True;
end;
