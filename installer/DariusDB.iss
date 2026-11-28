; Inno Setup 6 Script for Darius DB
; Created by Darius Cumpio - November 19, 2026 - v0.1.6 (MIT License)
;
; Standard Lite build (fast ~20 MB core installer):
;   ISCC.exe installer\DariusDB.iss
;
; All-in-One Offline Bundle build (for school labs / air-gapped PCs with no internet):
;   powershell -ExecutionPolicy Bypass -File installer\build-offline.ps1
;   or directly after staging build\offline\:
;   ISCC.exe /DOfflineBundle=1 installer\DariusDB.iss

#ifndef MyAppVersion
  #define MyAppVersion "0.1.6"
#endif
#define MyAppName      "Darius DB (Lite Version " + MyAppVersion + ")"
#define MyAppPublisher "Darius Cumpio"
#define MyAppExeName   "DariusDB.exe"
#define MyLauncherExe  "DariusDB-Launcher.exe"
#ifndef OfflineDir
  #define OfflineDir   "..\build\offline"
#endif

[Setup]
AppId={{7D9A1B2E-4C8F-4E3A-9B61-D4A1E5DB0100}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
#ifdef OfflineBundle
AppVerName={#MyAppName} (All-in-One Offline Bundle)
OutputBaseFilename=DariusDB-Offline-Setup-{#MyAppVersion}
#else
AppVerName={#MyAppName} {#MyAppVersion}
OutputBaseFilename=DariusDB-Setup-{#MyAppVersion}
#endif
AppPublisher={#MyAppPublisher}
AppCopyright=Copyright (c) 2026 {#MyAppPublisher} (MIT License)
DefaultDirName=C:\DariusDB
DefaultGroupName={#MyAppName}
AllowNoIcons=yes
LicenseFile=..\LICENSE
OutputDir=..\dist
#if FileExists("..\build\windows\icon.ico")
SetupIconFile=..\build\windows\icon.ico
#endif
UninstallDisplayIcon={app}\{#MyAppExeName}
Compression=lzma2/ultra64
SolidCompression=yes
LZMAUseSeparateProcess=yes
WizardStyle=modern
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

#ifdef OfflineBundle
[Types]
Name: "full"; Description: "Full Offline Stack (All bundled engines + missing requirements)"
Name: "web"; Description: "Web + Database Stack (Apache, PHP, MariaDB + missing requirements)"
Name: "compact"; Description: "Fast Lite Install (Core app only — built-in Apache & Mailpit)"
Name: "custom"; Description: "Custom installation"; Flags: iscustom

[Components]
Name: "core"; Description: "Darius DB Core Application, Launcher, CLI & Repair Tools"; Types: full web compact custom; Flags: fixed

#if FileExists(OfflineDir + "\prereqs\MicrosoftEdgeWebView2RuntimeInstallerX64.exe") || FileExists(OfflineDir + "\prereqs\vc_redist.x64.exe") || FileExists(OfflineDir + "\prereqs\SqlLocalDB.msi")
Name: "prereqs"; Description: "Windows System Requirements (only installed if missing on this PC)"; Types: full web custom
#endif
#if FileExists(OfflineDir + "\prereqs\MicrosoftEdgeWebView2RuntimeInstallerX64.exe")
Name: "prereqs\webview2"; Description: "Microsoft Edge WebView2 Runtime (x64 Offline Installer)"; Types: full web custom
#endif
#if FileExists(OfflineDir + "\prereqs\vc_redist.x64.exe")
Name: "prereqs\vcredist"; Description: "Visual C++ 2015-2026 Redistributable (x64 — for MySQL, PostgreSQL, PHP, Apache)"; Types: full web custom
#endif
#if FileExists(OfflineDir + "\prereqs\SqlLocalDB.msi")
Name: "prereqs\localdb"; Description: "Microsoft SQL Server Express LocalDB (for local MS SQL Server)"; Types: full custom
#endif

#if DirExists(OfflineDir + "\bin\mariadb") || DirExists(OfflineDir + "\bin\mysql") || DirExists(OfflineDir + "\bin\postgresql") || DirExists(OfflineDir + "\bin\mssql") || DirExists(OfflineDir + "\bin\php") || DirExists(OfflineDir + "\bin\apache") || DirExists(OfflineDir + "\bin\redis") || DirExists(OfflineDir + "\bin\mailpit")
Name: "engines"; Description: "Bundled Offline Database & Web Engines"; Types: full web custom
#endif
#if DirExists(OfflineDir + "\bin\mariadb")
Name: "engines\mariadb"; Description: "MariaDB Server (Recommended SQL)"; Types: full web custom
#endif
#if DirExists(OfflineDir + "\bin\mysql")
Name: "engines\mysql"; Description: "MySQL Server (Requires VC++ x64)"; Types: full custom
#endif
#if DirExists(OfflineDir + "\bin\postgresql")
Name: "engines\postgresql"; Description: "PostgreSQL Server (Requires VC++ x64)"; Types: full custom
#endif
#if DirExists(OfflineDir + "\bin\mssql")
Name: "engines\mssql"; Description: "MS SQL Server CLI Tools (go-sqlcmd)"; Types: full custom
#endif
#if DirExists(OfflineDir + "\bin\php")
Name: "engines\php"; Description: "PHP Runtime (Requires VC++ x64)"; Types: full web custom
#endif
#if DirExists(OfflineDir + "\bin\apache")
Name: "engines\apache"; Description: "Apache HTTP Server (Requires VC++ x64)"; Types: full web custom
#endif
#if DirExists(OfflineDir + "\bin\redis")
Name: "engines\redis"; Description: "Redis In-Memory Cache Server"; Types: full custom
#endif
#if DirExists(OfflineDir + "\bin\mailpit")
Name: "engines\mailpit"; Description: "Mailpit Local SMTP & Web Inbox"; Types: full web custom
#endif
#endif

[Tasks]
Name: "desktopicon"; Description: "Create a &Desktop shortcut"; GroupDescription: "Additional shortcuts:"
Name: "addpath"; Description: "Add 'darius' CLI & installed DB tools to Windows User &PATH"; GroupDescription: "Developer Integration:"
Name: "startup"; Description: "Start {#MyAppName} automatically on Windows sign-in (minimized to tray)"; GroupDescription: "Startup:"; Flags: unchecked

[Dirs]
Name: "{app}\www"
Name: "{app}\bin"
Name: "{app}\data"
Name: "{app}\etc"
Name: "{app}\logs"
Name: "{app}\backups"
Name: "{app}\tmp"

[Files]
Source: "..\build\bin\DariusDB.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\DariusDB-Launcher.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\DariusDB-Repair.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\DariusDB-Uninstall.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\DariusDB-Updater.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\bin\darius.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\GUIDE.md"; DestDir: "{app}"; Flags: ignoreversion

#ifdef OfflineBundle
; Staged Windows prerequisites (extracted to temp folder and deleted after setup)
#if FileExists(OfflineDir + "\prereqs\MicrosoftEdgeWebView2RuntimeInstallerX64.exe")
Source: "{#OfflineDir}\prereqs\MicrosoftEdgeWebView2RuntimeInstallerX64.exe"; DestDir: "{tmp}"; Components: prereqs\webview2; Flags: deleteafterinstall; Check: NeedWebView2Install
#endif
#if FileExists(OfflineDir + "\prereqs\vc_redist.x64.exe")
Source: "{#OfflineDir}\prereqs\vc_redist.x64.exe"; DestDir: "{tmp}"; Components: prereqs\vcredist; Flags: deleteafterinstall; Check: NeedVCRedistInstall
#endif
#if FileExists(OfflineDir + "\prereqs\SqlLocalDB.msi")
Source: "{#OfflineDir}\prereqs\SqlLocalDB.msi"; DestDir: "{tmp}"; Components: prereqs\localdb; Flags: deleteafterinstall; Check: NeedSQLLocalDBInstall
#endif

; Staged offline database & web engines (extracted straight into {app}\bin\<engine>\)
#if DirExists(OfflineDir + "\bin\mariadb")
Source: "{#OfflineDir}\bin\mariadb\*"; DestDir: "{app}\bin\mariadb"; Components: engines\mariadb; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if DirExists(OfflineDir + "\bin\mysql")
Source: "{#OfflineDir}\bin\mysql\*"; DestDir: "{app}\bin\mysql"; Components: engines\mysql; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if DirExists(OfflineDir + "\bin\postgresql")
Source: "{#OfflineDir}\bin\postgresql\*"; DestDir: "{app}\bin\postgresql"; Components: engines\postgresql; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if DirExists(OfflineDir + "\bin\mssql")
Source: "{#OfflineDir}\bin\mssql\*"; DestDir: "{app}\bin\mssql"; Components: engines\mssql; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if DirExists(OfflineDir + "\bin\php")
Source: "{#OfflineDir}\bin\php\*"; DestDir: "{app}\bin\php"; Components: engines\php; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if DirExists(OfflineDir + "\bin\apache")
Source: "{#OfflineDir}\bin\apache\*"; DestDir: "{app}\bin\apache"; Components: engines\apache; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if DirExists(OfflineDir + "\bin\redis")
Source: "{#OfflineDir}\bin\redis\*"; DestDir: "{app}\bin\redis"; Components: engines\redis; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if DirExists(OfflineDir + "\bin\mailpit")
Source: "{#OfflineDir}\bin\mailpit\*"; DestDir: "{app}\bin\mailpit"; Components: engines\mailpit; Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist
#endif
#if FileExists(OfflineDir + "\offline-manifest.json")
Source: "{#OfflineDir}\offline-manifest.json"; DestDir: "{app}"; Flags: ignoreversion
#endif
#endif

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyLauncherExe}"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\{#MyAppName} Terminal"; Filename: "{app}\{#MyLauncherExe}"; Parameters: "--terminal"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\Update {#MyAppName}"; Filename: "{app}\DariusDB-Updater.exe"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\Repair All {#MyAppName}"; Filename: "{app}\DariusDB-Repair.exe"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\User Guide"; Filename: "{app}\GUIDE.md"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyLauncherExe}"; IconFilename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "DariusDB"; ValueData: """{app}\{#MyAppExeName}"" --minimized"; Tasks: startup; Flags: uninsdeletevalue

[Run]
#ifdef OfflineBundle
#if FileExists(OfflineDir + "\prereqs\MicrosoftEdgeWebView2RuntimeInstallerX64.exe")
Filename: "{tmp}\MicrosoftEdgeWebView2RuntimeInstallerX64.exe"; Parameters: "/silent /install"; StatusMsg: "Installing Microsoft Edge WebView2 Runtime (Offline)..."; Components: prereqs\webview2; Verb: "runas"; Flags: shellexec waituntilterminated; Check: NeedWebView2Install
#endif
#if FileExists(OfflineDir + "\prereqs\vc_redist.x64.exe")
Filename: "{tmp}\vc_redist.x64.exe"; Parameters: "/install /passive /norestart"; StatusMsg: "Installing Visual C++ 2015-2026 Redistributable (x64)..."; Components: prereqs\vcredist; Verb: "runas"; Flags: shellexec waituntilterminated; Check: NeedVCRedistInstall
#endif
#if FileExists(OfflineDir + "\prereqs\SqlLocalDB.msi")
Filename: "{sys}\msiexec.exe"; Parameters: "/i ""{tmp}\SqlLocalDB.msi"" /passive /norestart IACCEPTSQLLOCALDBLICENSETERMS=YES"; StatusMsg: "Installing Microsoft SQL Server Express LocalDB..."; Components: prereqs\localdb; Verb: "runas"; Flags: shellexec waituntilterminated; Check: NeedSQLLocalDBInstall
#endif
#endif
; Configure installed engines, php.ini hardlinks, default configs, and PATH cleanly
Filename: "{app}\darius.exe"; Parameters: "repair all --clean-cache"; WorkingDir: "{app}"; StatusMsg: "Configuring Darius DB engines and defaults..."; Flags: runhidden waituntilterminated
Filename: "{app}\darius.exe"; Parameters: "path --add"; WorkingDir: "{app}"; StatusMsg: "Adding 'darius' CLI & DB tools to Windows User PATH..."; Tasks: addpath; Flags: runhidden waituntilterminated
Filename: "{app}\{#MyLauncherExe}"; Description: "Launch {#MyAppName}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Gracefully stop any running database engines and clean PATH before removing files
Filename: "{app}\{#MyLauncherExe}"; Parameters: "--stop-all"; RunOnceId: "StopDariusDBServices"; Flags: runhidden waituntilterminated
Filename: "{app}\darius.exe"; Parameters: "path --remove"; RunOnceId: "CleanDariusDBPath"; Flags: runhidden waituntilterminated

[UninstallDelete]
Type: filesandordirs; Name: "{app}\bin"
Type: filesandordirs; Name: "{app}\tmp"
Type: filesandordirs; Name: "{app}\logs"

[Code]
function IsWebView2Installed(): Boolean;
var
  Ver: string;
begin
  Result := False;
  if RegQueryStringValue(HKLM, 'SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Ver) and (Ver <> '') and (Ver <> '0.0.0.0') then
  begin
    Result := True;
    Exit;
  end;
  if RegQueryStringValue(HKLM, 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Ver) and (Ver <> '') and (Ver <> '0.0.0.0') then
  begin
    Result := True;
    Exit;
  end;
  if RegQueryStringValue(HKCU, 'Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Ver) and (Ver <> '') and (Ver <> '0.0.0.0') then
    Result := True;
end;

function IsVCRedistX64Installed(): Boolean;
var
  SysDir: string;
begin
  SysDir := ExpandConstant('{sys}');
  Result := FileExists(SysDir + '\vcruntime140.dll') and
            FileExists(SysDir + '\vcruntime140_1.dll') and
            FileExists(SysDir + '\msvcp140.dll') and
            FileExists(SysDir + '\msvcp140_atomic_wait.dll');
end;

function IsSQLLocalDBInstalled(): Boolean;
var
  SubKeys: TArrayOfString;
  I: Integer;
begin
  Result := False;
  if RegGetSubkeyNames(HKLM, 'SOFTWARE\Microsoft\Microsoft SQL Server Local DB\Installed Versions', SubKeys) and (GetArrayLength(SubKeys) > 0) then
  begin
    Result := True;
    Exit;
  end;
  for I := 180 downto 110 do
  begin
    if FileExists(ExpandConstant('{commonpf64}\Microsoft SQL Server\' + IntToStr(I) + '\Tools\Binn\SqlLocalDB.exe')) then
    begin
      Result := True;
      Exit;
    end;
  end;
end;

function NeedWebView2Install(): Boolean;
begin
  Result := not IsWebView2Installed();
end;

function NeedVCRedistInstall(): Boolean;
begin
  Result := not IsVCRedistX64Installed();
end;

function NeedSQLLocalDBInstall(): Boolean;
begin
  Result := not IsSQLLocalDBInstalled();
end;

function StatusTag(Installed: Boolean): string;
begin
  if Installed then
    Result := 'Installed (will be skipped)'
  else
    Result := 'Missing';
end;

function UpdateReadyMemo(Space, NewLine, MemoUserInfoInfo, MemoDirInfo, MemoTypeInfo, MemoComponentsInfo, MemoGroupInfo, MemoTasksInfo: string): string;
var
  S: string;
begin
  S := 'Detected Windows Requirements:' + NewLine +
       Space + '• Edge WebView2 Runtime:       ' + StatusTag(IsWebView2Installed()) + NewLine +
       Space + '• Visual C++ x64 Runtime:      ' + StatusTag(IsVCRedistX64Installed()) + NewLine +
       Space + '• SQL Server Express LocalDB:  ' + StatusTag(IsSQLLocalDBInstalled()) + NewLine + NewLine;

  if MemoDirInfo <> '' then S := S + MemoDirInfo + NewLine + NewLine;
  if MemoTypeInfo <> '' then S := S + MemoTypeInfo + NewLine + NewLine;
  if MemoComponentsInfo <> '' then S := S + MemoComponentsInfo + NewLine + NewLine;
  if MemoGroupInfo <> '' then S := S + MemoGroupInfo + NewLine + NewLine;
  if MemoTasksInfo <> '' then S := S + MemoTasksInfo + NewLine + NewLine;
  Result := S;
end;

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
