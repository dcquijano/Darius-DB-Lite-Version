<#
.SYNOPSIS
  Builds Darius DB (Lite Version 0.1.3) GUI, Launcher, Uninstaller, Updater, CLI, Portable ZIP, and Standalone Setup .exe.
  Created by Darius Cumpio - November 19, 2026 - v0.1.3
#>
param(
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Version = "0.1.3"
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $RepoRoot

try {
    $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + $env:Path
    $goPath = (go env GOPATH).Trim()
    if ($goPath) { $env:Path += ';' + (Join-Path $goPath 'bin') }

    Remove-Item -Recurse -Force "dist\*", "installer\*.exe", "build\bin\*.exe" -ErrorAction SilentlyContinue

    # Ensure root and cmd/updater rsrc_windows_amd64.syso are present so every .exe embeds the Darius DB icon & manifest
    if (-not (Test-Path "rsrc_windows_amd64.syso") -and (Test-Path "cmd\installer\rsrc_windows_amd64.syso")) {
        Copy-Item -Force "cmd\installer\rsrc_windows_amd64.syso" "rsrc_windows_amd64.syso"
    }
    if (-not (Test-Path "cmd\updater\rsrc_windows_amd64.syso") -and (Test-Path "cmd\installer\rsrc_windows_amd64.syso")) {
        Copy-Item -Force "cmd\installer\rsrc_windows_amd64.syso" "cmd\updater\rsrc_windows_amd64.syso"
    }

    # Incrementally warm Go build cache with unified build tags (-tags "desktop,production")
    # in small batches so $WORK never exceeds ~12 MB
    $tags = "desktop,production"
    go build -p 1 -tags $tags runtime os io math strconv strings bytes sync
    go build -p 1 -tags $tags crypto/sha256 crypto/sha512 crypto/aes crypto/cipher crypto/ecdsa crypto/rsa
    go build -p 1 -tags $tags crypto/x509 crypto/tls
    go build -p 1 -tags $tags archive/zip encoding/json net/http
    go build -p 1 -tags $tags database/sql github.com/go-sql-driver/mysql github.com/lib/pq
    go build -p 1 -tags $tags ./internal/winui ./internal/tray ./internal/core
    go build -p 1 -tags $tags github.com/labstack/echo/v4 github.com/gabriel-vasile/mimetype
    go build -p 1 -tags $tags github.com/wailsapp/wails/v2/pkg/runtime github.com/wailsapp/wails/v2/pkg/options
    go build -p 1 -tags $tags github.com/wailsapp/wails/v2

    if (-not $SkipTests) {
        Write-Host "==> Running tests..." -ForegroundColor Cyan
        go test -p 1 -tags $tags -vet=off -count=1 ./internal/core ./internal/tray ./internal/winui
        if ($LASTEXITCODE -ne 0) { throw "go test failed" }
    }

    Write-Host "==> Building DariusDB.exe (Wails production build)..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path "build\bin" | Out-Null
    go build -p 1 -tags $tags -ldflags "-s -w -H windowsgui" -o "build\bin\DariusDB.exe" .
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB.exe failed" }

    Write-Host "==> Building DariusDB-Launcher.exe..." -ForegroundColor Cyan
    go build -p 1 -tags $tags -ldflags "-s -w -H=windowsgui" -o "build\bin\DariusDB-Launcher.exe" ./cmd/launcher
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Launcher.exe failed" }

    Write-Host "==> Building DariusDB-Uninstall.exe..." -ForegroundColor Cyan
    go build -p 1 -tags $tags -ldflags "-s -w -H=windowsgui" -o "build\bin\DariusDB-Uninstall.exe" ./cmd/uninstaller
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Uninstall.exe failed" }

    Write-Host "==> Building DariusDB-Updater.exe (Standalone System Version Updater)..." -ForegroundColor Cyan
    go build -p 1 -tags $tags -ldflags "-s -w -H=windowsgui" -o "build\bin\DariusDB-Updater.exe" ./cmd/updater
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Updater.exe failed" }

    Write-Host "==> Building darius.exe (Developer CLI for Visual Studio & VS Code)..." -ForegroundColor Cyan
    go build -p 1 -tags $tags -ldflags "-s -w" -o "build\bin\darius.exe" ./cmd/cli
    if ($LASTEXITCODE -ne 0) { throw "building darius.exe failed" }

    Write-Host "==> Building DariusDB-Setup-$Version.exe stub..." -ForegroundColor Cyan
    $stubExe = "build\bin\DariusDB-Setup-stub.exe"
    go build -p 1 -tags $tags -ldflags "-s -w -H=windowsgui" -o $stubExe ./cmd/installer
    if ($LASTEXITCODE -ne 0) { throw "building DariusDB-Setup.exe failed" }

    Write-Host "==> Packaging portable ZIP & self-contained DariusDB-Setup-$Version.exe..." -ForegroundColor Cyan
    $distDir = Join-Path $RepoRoot "dist"
    $stageDir = Join-Path $distDir "DariusDB-$Version-portable"
    $zipPath = Join-Path $distDir "DariusDB-$Version-portable-windows-amd64.zip"
    $setupExe = Join-Path $distDir "DariusDB-Setup-$Version.exe"

    Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

    Copy-Item "build\bin\DariusDB.exe"           $stageDir
    Copy-Item "build\bin\DariusDB-Launcher.exe"  $stageDir
    Copy-Item "build\bin\DariusDB-Uninstall.exe" $stageDir
    Copy-Item "build\bin\DariusDB-Updater.exe"   $stageDir
    Copy-Item "build\bin\darius.exe"             $stageDir
    Copy-Item "LICENSE", "README.md", "GUIDE.md" $stageDir

    Remove-Item -Force $zipPath -ErrorAction SilentlyContinue
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::CreateFromDirectory($stageDir, $zipPath, [System.IO.Compression.CompressionLevel]::Optimal, $false)
    Remove-Item -Recurse -Force $stageDir

    # Create self-contained single-file DariusDB-Setup-0.1.3.exe (stub + zip payload + 16-byte trailer)
    Copy-Item -Force $stubExe $setupExe
    $zipBytes = [System.IO.File]::ReadAllBytes($zipPath)
    $sizeBytes = [System.BitConverter]::GetBytes([uint64]$zipBytes.Length)
    $magic = [System.Text.Encoding]::ASCII.GetBytes("DARIUS01")
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

    # Place Installer, Launcher, Uninstaller, and Updater in installer\ for direct click access & GitHub distribution
    Copy-Item -Force $setupExe "installer\DariusDB-Setup-$Version.exe"
    Copy-Item -Force "build\bin\DariusDB-Launcher.exe" "installer\DariusDB-Launcher.exe"
    Copy-Item -Force "build\bin\DariusDB-Uninstall.exe" "installer\DariusDB-Uninstall.exe"
    Copy-Item -Force "build\bin\DariusDB-Updater.exe" "installer\DariusDB-Updater.exe"

    Write-Host ""
    Write-Host "==> Build outputs:" -ForegroundColor Green
    Get-ChildItem "build\bin\*.exe", "dist\*", "installer\*.exe" -ErrorAction SilentlyContinue |
    Select-Object Name, @{N = "SizeMB"; E = { [math]::Round($_.Length / 1MB, 2) } }, FullName |
    Format-Table -AutoSize
}
finally {
    Pop-Location
}
