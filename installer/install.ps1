<#
.SYNOPSIS
  Darius DB Installer for Windows (no admin required, portable-friendly).
  Created by Darius Cumpio - November 19, 2026 - v0.1.0 (MIT License)
#>
param(
    [string]$InstallDir = "C:\DariusDB",
    [switch]$Silent,
    [switch]$AutoStart,
    [switch]$NoDesktop,
    [switch]$NoLaunch
)

$ErrorActionPreference = "Stop"

$AppName    = "Darius DB"
$FullTitle  = "Darius DB (Lite Version 0.1.0)"
$AppVersion = "0.1.0"
$Author     = "Darius Cumpio"
$ScriptDir  = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot   = Split-Path -Parent $ScriptDir

function Find-SourceFile([string]$Name) {
    $candidates = @(
        (Join-Path $ScriptDir $Name),
        (Join-Path $RepoRoot "build\bin\$Name"),
        (Join-Path $RepoRoot $Name)
    )
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c -PathType Leaf) { return $c }
    }
    return $null
}

Write-Host ""
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host "  $FullTitle - Windows Installer" -ForegroundColor Cyan
Write-Host "  Created by $Author (MIT License)" -ForegroundColor DarkCyan
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host ""

$mainExe = Find-SourceFile "DariusDB.exe"
if (-not $mainExe) {
    Write-Host "ERROR: DariusDB.exe was not found." -ForegroundColor Red
    Write-Host "Build it first with: .\installer\build-all.ps1" -ForegroundColor Yellow
    exit 1
}
$launcherExe  = Find-SourceFile "DariusDB-Launcher.exe"
$uninstallExe = Find-SourceFile "DariusDB-Uninstall.exe"

if (-not $Silent) {
    $custom = Read-Host "Install folder [$InstallDir]"
    if ($custom.Trim() -ne "") { $InstallDir = $custom.Trim() }

    if (-not $AutoStart) {
        $ans = Read-Host "Start Darius DB automatically when Windows boots? [y/N]"
        if ($ans -match '^[Yy]') { $AutoStart = $true }
    }
}

Write-Host "Installing to: $InstallDir" -ForegroundColor Green
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# If Darius DB is already running from this folder (upgrade/reinstall), stop it first so files aren't locked
$existingLauncher = Join-Path $InstallDir "DariusDB-Launcher.exe"
$existingMain     = Join-Path $InstallDir "DariusDB.exe"
$runningProcs = Get-Process -Name "DariusDB","DariusDB-Launcher" -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -and ($_.Path -ieq $existingMain -or $_.Path -ieq $existingLauncher) }
if ($runningProcs) {
    Write-Host "Stopping running Darius DB in $InstallDir before updating..." -ForegroundColor Yellow
    if (Test-Path -LiteralPath $existingLauncher) {
        Start-Process -FilePath $existingLauncher -ArgumentList "--stop-all" -Wait -ErrorAction SilentlyContinue
    }
    $runningProcs | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 600
}

# Copy executables & documentation
Copy-Item -Force $mainExe (Join-Path $InstallDir "DariusDB.exe")
if ($launcherExe)  { Copy-Item -Force $launcherExe  (Join-Path $InstallDir "DariusDB-Launcher.exe") }
if ($uninstallExe) { Copy-Item -Force $uninstallExe (Join-Path $InstallDir "DariusDB-Uninstall.exe") }

foreach ($doc in @("LICENSE", "README.md", "GUIDE.md")) {
    $src = Find-SourceFile $doc
    if ($src) { Copy-Item -Force $src (Join-Path $InstallDir $doc) }
}

# Create portable folder structure
foreach ($sub in @("bin", "data", "etc", "logs", "tmp")) {
    New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir $sub) | Out-Null
}

# Helper to create Windows .lnk shortcuts
$wsh = New-Object -ComObject WScript.Shell
function New-Shortcut([string]$LinkPath, [string]$TargetPath, [string]$Arguments, [string]$IconPath, [string]$Desc) {
    $sc = $wsh.CreateShortcut($LinkPath)
    $sc.TargetPath       = $TargetPath
    $sc.Arguments        = $Arguments
    $sc.WorkingDirectory = $InstallDir
    $sc.IconLocation     = $IconPath
    $sc.Description      = $Desc
    $sc.Save()
}

$installedMain      = Join-Path $InstallDir "DariusDB.exe"
$installedLauncher  = if (Test-Path (Join-Path $InstallDir "DariusDB-Launcher.exe")) { Join-Path $InstallDir "DariusDB-Launcher.exe" } else { $installedMain }
$installedUninstall = Join-Path $InstallDir "DariusDB-Uninstall.exe"
$iconRef            = "$installedMain,0"

# Start Menu shortcuts
$startMenuDir = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\$AppName"
New-Item -ItemType Directory -Force -Path $startMenuDir | Out-Null
New-Shortcut (Join-Path $startMenuDir "$AppName.lnk") $installedLauncher "" $iconRef "Launch $AppName"
if (Test-Path (Join-Path $InstallDir "DariusDB-Launcher.exe")) {
    New-Shortcut (Join-Path $startMenuDir "$AppName Terminal.lnk") $installedLauncher "--terminal" $iconRef "Open $AppName Terminal with DB tools in PATH"
}
if (Test-Path $installedUninstall) {
    New-Shortcut (Join-Path $startMenuDir "Uninstall $AppName.lnk") $installedUninstall "" $iconRef "Uninstall $AppName"
}

# Desktop shortcut
if (-not $NoDesktop) {
    $desktop = [Environment]::GetFolderPath("Desktop")
    if ($desktop -and (Test-Path $desktop)) {
        New-Shortcut (Join-Path $desktop "$AppName.lnk") $installedLauncher "" $iconRef "Launch $AppName"
    }
}

# Register in Windows "Installed Apps" (HKCU - no admin required)
$uninstKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB"
New-Item -Path $uninstKey -Force | Out-Null
$sizeKB = [int]((Get-ChildItem $InstallDir -File | Measure-Object Length -Sum).Sum / 1KB)
Set-ItemProperty -Path $uninstKey -Name "DisplayName"          -Value $FullTitle
Set-ItemProperty -Path $uninstKey -Name "DisplayVersion"       -Value $AppVersion
Set-ItemProperty -Path $uninstKey -Name "Publisher"            -Value $Author
Set-ItemProperty -Path $uninstKey -Name "InstallLocation"      -Value $InstallDir
Set-ItemProperty -Path $uninstKey -Name "DisplayIcon"          -Value $iconRef
Set-ItemProperty -Path $uninstKey -Name "InstallDate"          -Value (Get-Date -Format "yyyyMMdd")
Set-ItemProperty -Path $uninstKey -Name "EstimatedSize"        -Value $sizeKB -Type DWord
Set-ItemProperty -Path $uninstKey -Name "NoModify"             -Value 1 -Type DWord
Set-ItemProperty -Path $uninstKey -Name "NoRepair"             -Value 1 -Type DWord
if (Test-Path $installedUninstall) {
    Set-ItemProperty -Path $uninstKey -Name "UninstallString"      -Value "`"$installedUninstall`""
    Set-ItemProperty -Path $uninstKey -Name "QuietUninstallString" -Value "`"$installedUninstall`" --silent --keep-data"
}

# Optional Run on Windows Boot
if ($AutoStart) {
    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    New-Item -Path $runKey -Force | Out-Null
    Set-ItemProperty -Path $runKey -Name "DariusDB" -Value "`"$installedMain`" --minimized"
}

Write-Host ""
Write-Host "Installation complete!" -ForegroundColor Green
Write-Host "  Folder:     $InstallDir"
Write-Host "  Start Menu: $startMenuDir"
Write-Host "  Uninstall:  Windows Settings > Apps > Installed Apps > $AppName"
Write-Host ""

if (-not $Silent -and -not $NoLaunch) {
    $launch = Read-Host "Launch $AppName now? [Y/n]"
    if ($launch -notmatch '^[Nn]') {
        Start-Process -FilePath $installedLauncher -WorkingDirectory $InstallDir
    }
}
