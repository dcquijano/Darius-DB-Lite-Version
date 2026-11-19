<#
.SYNOPSIS
  Builds Darius DB (Lite Version 0.1.2) GUI, Launcher, Uninstaller, Portable ZIP, and Standalone Setup .exe.
  Created by Darius Cumpio - November 19, 2026 - v0.1.2
#>
param(
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"

$Version  = "0.1.2"
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $RepoRoot

try {
    $env:Path = [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
    $goPath = (go env GOPATH).Trim()
    if ($goPath) { $env:Path += ';' + (Join-Path $goPath 'bin') }

    if (-not $SkipTests) {
        Write-Host "==> Running tests..." -ForegroundColor Cyan
        go test -p 1 -vet=off -count=1 ./internal/core ./internal/tray ./internal/winui
        if ($LASTEXITCODE -ne 0) { throw "go test failed" }
    }

    Write-Host "==> Building DariusDB.exe (Wails production build)..." -ForegroundColor Cyan
    wails build -skipbindings -ldflags "-s -w"
    if ($LASTEXITCODE -ne 0) { throw "wails build failed" }

    Write-Host "==> Building DariusDB-Launcher.exe..." -ForegroundColor Cyan
    go build -p 1 -ldflags "-s -w -H=windowsgui" -o "build\bin\DariusDB-Launcher.exe" ./cmd/launcher
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Launcher.exe failed" }

    Write-Host "==> Building DariusDB-Uninstall.exe..." -ForegroundColor Cyan
    go build -p 1 -ldflags "-s -w -H=windowsgui" -o "build\bin\DariusDB-Uninstall.exe" ./cmd/uninstaller
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Uninstall.exe failed" }

    Write-Host "==> Building darius.exe (Developer CLI for Visual Studio & VS Code)..." -ForegroundColor Cyan
    go build -p 1 -ldflags "-s -w" -o "build\bin\darius.exe" ./cmd/cli
    if ($LASTEXITCODE -ne 0) { throw "building darius.exe failed" }

    Write-Host "==> Building DariusDB-Setup-$Version.exe stub..." -ForegroundColor Cyan
    $stubExe = "build\bin\DariusDB-Setup-stub.exe"
    go build -p 1 -ldflags "-s -w -H=windowsgui" -o $stubExe ./cmd/installer
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Setup.exe failed" }

    Write-Host "==> Packaging portable ZIP & self-contained DariusDB-Setup-$Version.exe..." -ForegroundColor Cyan
    $distDir  = Join-Path $RepoRoot "dist"
    $stageDir = Join-Path $distDir "DariusDB-$Version-portable"
    $zipPath  = Join-Path $distDir "DariusDB-$Version-portable-windows-amd64.zip"
    $setupExe = Join-Path $distDir "DariusDB-Setup-$Version.exe"

    Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

    Copy-Item "build\bin\DariusDB.exe"           $stageDir
    Copy-Item "build\bin\DariusDB-Launcher.exe"  $stageDir
    Copy-Item "build\bin\DariusDB-Uninstall.exe" $stageDir
    Copy-Item "build\bin\darius.exe"             $stageDir
    Copy-Item "LICENSE", "README.md", "GUIDE.md" $stageDir

    Remove-Item -Force $zipPath -ErrorAction SilentlyContinue
    Compress-Archive -Path "$stageDir\*" -DestinationPath $zipPath
    Remove-Item -Recurse -Force $stageDir

    # Create self-contained single-file DariusDB-Setup-0.1.2.exe (stub + zip payload + 16-byte trailer)
    Copy-Item -Force $stubExe $setupExe
    $zipBytes  = [System.IO.File]::ReadAllBytes($zipPath)
    $sizeBytes = [System.BitConverter]::GetBytes([uint64]$zipBytes.Length)
    $magic     = [System.Text.Encoding]::ASCII.GetBytes("DARIUS01")
    $fs = [System.IO.File]::Open($setupExe, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write)
    try {
        $fs.Write($zipBytes, 0, $zipBytes.Length)
        $fs.Write($sizeBytes, 0, 8)
        $fs.Write($magic, 0, 8)
    }
    finally {
        $fs.Close()
    }
    Remove-Item -Force $stubExe -ErrorAction SilentlyContinue

    # Also place DariusDB-Setup-0.1.2.exe and DariusDB-Uninstall.exe in installer\ for direct click access
    Copy-Item -Force $setupExe "installer\DariusDB-Setup-$Version.exe"
    Copy-Item -Force "build\bin\DariusDB-Uninstall.exe" "installer\DariusDB-Uninstall.exe"

    Write-Host ""
    Write-Host "==> Build outputs:" -ForegroundColor Green
    Get-ChildItem "build\bin\*.exe", "dist\*", "installer\*.exe" -ErrorAction SilentlyContinue |
        Select-Object Name, @{N="SizeMB";E={[math]::Round($_.Length/1MB,2)}}, FullName |
        Format-Table -AutoSize
}
finally {
    Pop-Location
}
