i#
.SYNOPSIS
Darius DB (Lite Version 0.1.6) System Updater script for Windows.
Upgrades an existing installation (e.g. v0.1.5 -> v0.1.6) in-place while keeping data/, www/, and etc/ safe.
Created by Darius Cumpio - November 19, 2026 - v0.1.6 (MIT License)
#>
param(
    [string]$InstallDir = "",
    [string]$SetupFile = "",
    [string]$Token = "",
    [switch]$Silent,
    [switch]$NoLaunch
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

if (-not $InstallDir) {
    $reg = Get-ItemProperty -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB" -ErrorAction SilentlyContinue
    if ($reg -and $reg.InstallLocation) {
        $InstallDir = $reg.InstallLocation
    }
    else {
        $InstallDir = "C:\DariusDB"
    }
}

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

# 1. If the native GUI Updater binary is present and not running silently with a specific file, launch it
if (-not $Silent -and -not $SetupFile) {
    foreach ($candidate in @(
            (Join-Path $ScriptDir "DariusDB-Updater.exe"),
            (Join-Path $InstallDir "DariusDB-Updater.exe")
        )) {
        if (Test-Path -LiteralPath $candidate) {
            $argsList = @("--dir=$InstallDir")
            if ($Token) { $argsList += "--token=$Token" }
            Start-Process -FilePath $candidate -ArgumentList $argsList
            exit 0
        }
    }
}

Write-Host ""
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host "  Darius DB System Version Updater" -ForegroundColor Cyan
Write-Host "  Target Folder: $InstallDir" -ForegroundColor DarkCyan
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host ""

# 2. Resolve setup executable (explicit param -> local installer folder -> download from GitHub)
if (-not $SetupFile) {
    $localSetup = Get-ChildItem -Path $ScriptDir -Filter "DariusDB-Setup-*.exe" -ErrorAction SilentlyContinue |
    Sort-Object Name -Descending | Select-Object -First 1
    if ($localSetup) {
        $SetupFile = $localSetup.FullName
        Write-Host "Found local installer: $($localSetup.Name)" -ForegroundColor Green
    }
}

if (-not $SetupFile) {
    $headers = @{ "User-Agent" = "DariusDB-Updater/0.1.6" }
    if (-not $Token -and $env:GITHUB_TOKEN) { $Token = $env:GITHUB_TOKEN }
    if ($Token) { $headers["Authorization"] = "Bearer $Token" }

    Write-Host "Checking GitHub for latest Darius DB release..." -ForegroundColor Cyan
    $repoCandidates = @("dcquijano/Darius-DB-Lite-Releases", "dcquijano/Darius-DB-Lite-Version")
    $verInfo = $null
    $activeRepo = $repoCandidates[0]
    $lastErrMsg = ""
    foreach ($slug in $repoCandidates) {
        $verUrl = if ($Token) {
            "https://api.github.com/repos/$slug/contents/version.json"
        }
        else {
            "https://raw.githubusercontent.com/$slug/main/version.json?t=$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())"
        }
        if ($Token) { $headers["Accept"] = "application/vnd.github.raw+json" }
        try {
            $verInfo = Invoke-RestMethod -Uri $verUrl -Headers $headers -ErrorAction Stop
            if ($verInfo -and $verInfo.version) {
                $activeRepo = $slug
                break
            }
        }
        catch {
            $lastErrMsg = $_.Exception.Message
        }
    }
    if (-not $verInfo) {
        Write-Host "ERROR: Could not fetch version.json from GitHub ($lastErrMsg)." -ForegroundColor Red
        Write-Host "Ensure public releases repo dcquijano/Darius-DB-Lite-Releases exists, or pass -Token <GITHUB_PAT>." -ForegroundColor Yellow
        exit 1
    }

    $dlUrls = @()
    if ($verInfo.directSetupUrl) { $dlUrls += $verInfo.directSetupUrl }
    if ($verInfo.fallbackSetupUrl) { $dlUrls += $verInfo.fallbackSetupUrl }
    if ($verInfo.version) {
        $dlUrls += "https://github.com/$activeRepo/releases/download/v$($verInfo.version)/DariusDB-Setup-$($verInfo.version).exe"
        $dlUrls += "https://raw.githubusercontent.com/$activeRepo/main/installer/DariusDB-Setup-$($verInfo.version).exe"
    }

    $stageDir = Join-Path $env:TEMP "DariusDB-Update"
    New-Item -ItemType Directory -Force -Path $stageDir | Out-Null
    $destExe = Join-Path $stageDir "DariusDB-Setup-$($verInfo.version).exe"

    $downloaded = $false
    foreach ($u in ($dlUrls | Select-Object -Unique)) {
        Write-Host "Downloading $u ..." -ForegroundColor Cyan
        try {
            $dlHeaders = @{ "User-Agent" = "DariusDB-Updater/0.1.6" }
            if ($Token) { $dlHeaders["Authorization"] = "Bearer $Token" }
            Invoke-WebRequest -Uri $u -Headers $dlHeaders -OutFile $destExe -UseBasicParsing -ErrorAction Stop
            if ((Get-Item $destExe).Length -gt 65536) {
                $SetupFile = $destExe
                $downloaded = $true
                break
            }
        }
        catch {
            Write-Host "  Could not download from $u ($($_.Exception.Message))" -ForegroundColor DarkYellow
        }
    }
    if (-not $downloaded) {
        Write-Host "ERROR: Failed to download Darius DB Setup executable from GitHub." -ForegroundColor Red
        exit 1
    }
}

Write-Host "Stopping running Darius DB processes in $InstallDir..." -ForegroundColor Yellow
$existingLauncher = Join-Path $InstallDir "DariusDB-Launcher.exe"
if (Test-Path -LiteralPath $existingLauncher) {
    Start-Process -FilePath $existingLauncher -ArgumentList "--stop-all" -Wait -ErrorAction SilentlyContinue
}
Get-Process -Name "DariusDB", "DariusDB-Launcher", "DariusDB-Updater" -ErrorAction SilentlyContinue |
Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600

Write-Host "Applying in-place upgrade from $(Split-Path -Leaf $SetupFile)..." -ForegroundColor Green
Start-Process -FilePath $SetupFile -ArgumentList "--silent", "--force", "--dir=$InstallDir" -Wait

Write-Host ""
Write-Host "Darius DB upgrade complete! Your databases (data/) and websites (www/) were kept safe." -ForegroundColor Green
if (-not $NoLaunch) {
    if (Test-Path -LiteralPath $existingLauncher) {
        Start-Process -FilePath $existingLauncher -WorkingDirectory $InstallDir
    }
    elseif (Test-Path -LiteralPath (Join-Path $InstallDir "DariusDB.exe")) {
        Start-Process -FilePath (Join-Path $InstallDir "DariusDB.exe") -WorkingDirectory $InstallDir
    }
}
