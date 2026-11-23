<#
.SYNOPSIS
  Darius DB (Lite Version 0.1.5) Repair All wrapper script for Windows.
  Launches DariusDB-Repair.exe or falls back to darius.exe repair all --fix-ports.
#>
param(
    [string]$InstallDir = "",
    [string]$Target = "all",
    [switch]$Silent,
    [switch]$ResetConfig,
    [switch]$NoFixPorts,
    [switch]$NoCache,
    [switch]$NoLaunch
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

$exe = Join-Path $PSScriptRoot "DariusDB-Repair.exe"
if (-not (Test-Path -LiteralPath $exe)) {
    $installedRepair = Join-Path $InstallDir "DariusDB-Repair.exe"
    if (Test-Path -LiteralPath $installedRepair) {
        $exe = $installedRepair
    }
}

if (Test-Path -LiteralPath $exe) {
    $argsList = @("--dir=$InstallDir", "--target=$Target")
    if ($Silent)      { $argsList += "--silent" }
    if ($ResetConfig) { $argsList += "--reset-config" }
    if ($NoFixPorts)  { $argsList += "--no-fix-ports" }
    if ($NoCache)     { $argsList += "--no-cache" }
    if ($NoLaunch)    { $argsList += "--no-launch" }
    Start-Process -FilePath $exe -ArgumentList $argsList -Wait
    exit $LASTEXITCODE
}

$cliExe = Join-Path $InstallDir "darius.exe"
if (-not (Test-Path -LiteralPath $cliExe)) {
    Write-Host "ERROR: Neither DariusDB-Repair.exe nor darius.exe was found for $InstallDir." -ForegroundColor Red
    exit 1
}

foreach ($sub in @("www", "bin", "data", "etc", "logs", "backups", "tmp")) {
    New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir $sub) | Out-Null
}

$cliArgs = @("repair", $Target)
if (-not $NoFixPorts) { $cliArgs += "--fix-ports" }
if ($ResetConfig)     { $cliArgs += "--reset-config" }
if ($NoCache)         { $cliArgs += "--no-cache" }

& $cliExe @cliArgs
exit $LASTEXITCODE
