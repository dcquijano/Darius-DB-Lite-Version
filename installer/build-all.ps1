<#
.SYNOPSIS
  Builds Darius DB (GUI + Launcher + Uninstaller), portable ZIP, and optional Inno Setup installer.
  Created by Darius Cumpio - November 19, 2026 - v0.1.0
#>
param(
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"

$Version = "0.1.0"
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $RepoRoot

try {
    # Ensure Go and Wails are on PATH
    $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
    $goPath = (go env GOPATH).Trim()
    if ($goPath) { $env:Path += ';' + (Join-Path $goPath 'bin') }

    if (-not $SkipTests) {
        Write-Host "==> Running tests..." -ForegroundColor Cyan
        go test -count=1 ./...
        if ($LASTEXITCODE -ne 0) { throw "go test failed" }
    }

    Write-Host "==> Building DariusDB.exe (Wails production build)..." -ForegroundColor Cyan
    wails build -clean -ldflags "-s -w"
    if ($LASTEXITCODE -ne 0) { throw "wails build failed" }

    Write-Host "==> Building DariusDB-Launcher.exe..." -ForegroundColor Cyan
    go build -ldflags "-s -w -H=windowsgui" -o "build\bin\DariusDB-Launcher.exe" ./cmd/launcher
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Launcher.exe failed" }

    Write-Host "==> Building DariusDB-Uninstall.exe..." -ForegroundColor Cyan
    go build -ldflags "-s -w -H=windowsgui" -o "build\bin\DariusDB-Uninstall.exe" ./cmd/uninstaller
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Uninstall.exe failed" }

    Write-Host "==> Packaging portable ZIP..." -ForegroundColor Cyan
    $distDir = Join-Path $RepoRoot "dist"
    $stageDir = Join-Path $distDir "DariusDB-$Version-portable"
    $zipPath = Join-Path $distDir "DariusDB-$Version-portable-windows-amd64.zip"

    Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

    Copy-Item "build\bin\DariusDB.exe"           $stageDir
    Copy-Item "build\bin\DariusDB-Launcher.exe"  $stageDir
    Copy-Item "build\bin\DariusDB-Uninstall.exe" $stageDir
    Copy-Item "LICENSE", "README.md", "GUIDE.md" $stageDir
    Copy-Item "installer\install.ps1"            $stageDir
    Copy-Item "installer\Install-DariusDB.bat"   $stageDir
    Copy-Item "installer\uninstall.ps1"          $stageDir
    Copy-Item "installer\Uninstall-DariusDB.bat" $stageDir

    Remove-Item -Force $zipPath -ErrorAction SilentlyContinue
    Compress-Archive -Path "$stageDir\*" -DestinationPath $zipPath
    Remove-Item -Recurse -Force $stageDir

    # Build Inno Setup installer if ISCC.exe is available
    $iscc = @(
        (Get-Command iscc -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -ErrorAction SilentlyContinue),
        "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
        "C:\Program Files\Inno Setup 6\ISCC.exe"
    ) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1

    if ($iscc) {
        Write-Host "==> Building Inno Setup installer (DariusDB-Setup-$Version.exe)..." -ForegroundColor Cyan
        & $iscc "installer\DariusDB.iss"
    }
    else {
        Write-Host "==> Inno Setup (ISCC.exe) not installed - skipping Setup .exe (portable ZIP + Install-DariusDB.bat ready)." -ForegroundColor Yellow
        Write-Host "    To build DariusDB-Setup-$Version.exe, install Inno Setup: winget install JRSoftware.InnoSetup" -ForegroundColor DarkYellow
    }

    Write-Host ""
    Write-Host "==> Build outputs:" -ForegroundColor Green
    Get-ChildItem "build\bin\*.exe", "dist\*" -ErrorAction SilentlyContinue |
    Select-Object Name, @{N = "SizeMB"; E = { [math]::Round($_.Length / 1MB, 2) } }, FullName |
    Format-Table -AutoSize
}
finally {
    Pop-Location
}
