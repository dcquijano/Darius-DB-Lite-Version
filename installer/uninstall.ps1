<#
.SYNOPSIS
  Darius DB (Lite Version 0.1.3) Uninstall & Maintenance Wizard wrapper.
  Created by Darius Cumpio - November 19, 2026 - v0.1.3
#>
param(
    [string]$InstallDir = "",
    [switch]$Silent,
    [switch]$Repair,
    [switch]$RemoveData
)

$ErrorActionPreference = "Stop"

if (-not $InstallDir) {
    $reg = Get-ItemProperty -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB" -ErrorAction SilentlyContinue
    if ($reg -and $reg.InstallLocation) {
        $InstallDir = $reg.InstallLocation
    } else {
        $InstallDir = "C:\DariusDB"
    }
}

$exe = Join-Path $InstallDir "DariusDB-Uninstall.exe"
if (-not (Test-Path -LiteralPath $exe)) {
    $localUninst = Join-Path $PSScriptRoot "DariusDB-Uninstall.exe"
    if (Test-Path -LiteralPath $localUninst) {
        $exe = $localUninst
    }
}

if (Test-Path -LiteralPath $exe) {
    $argsList = @("--dir=$InstallDir")
    if ($Silent)     { $argsList += "--silent" }
    if ($Repair)     { $argsList += "--repair" }
    if ($RemoveData) { $argsList += "--remove-data" } else { $argsList += "--keep-data" }
    Start-Process -FilePath $exe -ArgumentList $argsList -Wait
    exit 0
}

Write-Host "DariusDB-Uninstall.exe not found in $InstallDir; performing script cleanup..." -ForegroundColor Yellow

# Remove registry entries & shortcuts
Remove-ItemProperty -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run" -Name "DariusDB" -ErrorAction SilentlyContinue
Remove-Item -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path (Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\Darius DB") -Recurse -Force -ErrorAction SilentlyContinue
$desktop = [Environment]::GetFolderPath("Desktop")
if ($desktop) { Remove-Item -Path (Join-Path $desktop "Darius DB.lnk") -Force -ErrorAction SilentlyContinue }

if (Test-Path $InstallDir) {
    $targets = @("DariusDB.exe", "DariusDB-Launcher.exe", "DariusDB-Uninstall.exe", "bin", "tmp", "logs", "LICENSE", "README.md", "GUIDE.md", "catalog.json")
    if ($RemoveData) { $targets += @("data", "etc", "settings.json") }
    foreach ($t in $targets) {
        Remove-Item -Path (Join-Path $InstallDir $t) -Recurse -Force -ErrorAction SilentlyContinue
    }
    if ($RemoveData -and ((Get-ChildItem $InstallDir -Force | Measure-Object).Count -eq 0)) {
        Remove-Item -Path $InstallDir -Force -ErrorAction SilentlyContinue
    }
}

Write-Host "Darius DB uninstalled." -ForegroundColor Green
