; Inno Setup 6 Script for Darius DB
; Created by Darius Cumpio - November 19, 2026 - v0.1.3 (MIT License)
;
; Build with:
;   ISCC.exe installer\DariusDB.iss
; or run:
;   powershell -ExecutionPolicy Bypass -File installer\build-all.ps1

#define MyAppName      "Darius DB (Lite Version 0.1.3)"
#define MyAppVersion   "0.1.3"
#define MyAppPublisher "Darius Cumpio"
#define MyAppExeName   "DariusDB.exe"
#define MyLauncherExe  "DariusDB-Launcher.exe"

[Setup]
AppId={{7D9A1B2E-4C8F-4E3A-9B61-D4A1E5DB0100}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppCopyright=Copyright (c) 2026 {#MyAppPublisher} (MIT License)
DefaultDirName=C:\DariusDB
DefaultGroupName={#MyAppName}
AllowNoIcons=yes
LicenseFile=..\LICENSE
OutputDir=..\dist
OutputBaseFilename=DariusDB-Setup-{#MyAppVersion}
SetupIconFile=..\build\windows\icon.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a &Desktop shortcut"; GroupDescription: "Additional shortcuts:"
Name: "startup"; Description: "Start {#MyAppName} automatically on Windows sign-in (minimized to tray)"; GroupDescription: "Startup:"; Flags: unchecked

[Dirs]
Name: "{app}\bin"
Name: "{app}\data"
Name: "{app}\etc"
Name: "{app}\logs"
Name: "{app}\tmp"

[Files]
Source: "..\build\bin\DariusDB.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\DariusDB-Launcher.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\DariusDB-Uninstall.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\DariusDB-Updater.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\darius.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\GUIDE.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyLauncherExe}"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\{#MyAppName} Terminal"; Filename: "{app}\{#MyLauncherExe}"; Parameters: "--terminal"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\Update {#MyAppName}"; Filename: "{app}\DariusDB-Updater.exe"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\User Guide"; Filename: "{app}\GUIDE.md"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyLauncherExe}"; IconFilename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "DariusDB"; ValueData: """{app}\{#MyAppExeName}"" --minimized"; Tasks: startup; Flags: uninsdeletevalue

[Run]
Filename: "{app}\{#MyLauncherExe}"; Description: "Launch {#MyAppName}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Gracefully stop any running database engines before removing files
Filename: "{app}\{#MyLauncherExe}"; Parameters: "--stop-all"; RunOnceId: "StopDariusDBServices"; Flags: runhidden

[UninstallDelete]
Type: filesandordirs; Name: "{app}\bin"
Type: filesandordirs; Name: "{app}\tmp"
Type: filesandordirs; Name: "{app}\logs"

[Code]
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: string;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    DataDir := ExpandConstant('{app}\data');
    if DirExists(DataDir) then
    begin
      if MsgBox('Do you also want to DELETE your database data and configs (data/, etc/, settings.json)?' + #13#10 + #13#10 +
                'Click NO to keep your databases safe (recommended).',
                mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
      begin
        DelTree(ExpandConstant('{app}\data'), True, True, True);
        DelTree(ExpandConstant('{app}\etc'), True, True, True);
        DeleteFile(ExpandConstant('{app}\settings.json'));
        RemoveDir(ExpandConstant('{app}'));
      end;
    end;
  end;
end;
