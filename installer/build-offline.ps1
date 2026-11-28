<#
.SYNOPSIS
  Builds an All-in-One Offline Bundle for Darius DB (school labs, USB deployment & air-gapped PCs).
  Stages Windows prerequisites (WebView2 x64, VC++ x64, SQL Server LocalDB) and selected engines
  from internal\core\catalog.json, then produces:
    1. dist\DariusDB-<Version>-offline-bundle-windows-amd64.zip  (Portable Offline USB Pack + Setup Wizard)
    2. dist\DariusDB-Offline-Setup-<Version>.exe                 (Single-file Inno Setup 6 Offline Installer, if ISCC.exe is installed)

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File installer\build-offline.ps1
  powershell -ExecutionPolicy Bypass -File installer\build-offline.ps1 -Preset WebStack
  powershell -ExecutionPolicy Bypass -File installer\build-offline.ps1 -Engines mariadb,php,apache -SkipLocalDB
  powershell -ExecutionPolicy Bypass -File installer\build-offline.ps1 -SkipBuild -SkipDownload
#>
param(
    [ValidateSet("Full", "WebStack", "Database", "Minimal", "Custom")]
    [string]$Preset = "Full",
    [string[]]$Engines = @(),
    [switch]$SkipPrereqs,
    [switch]$SkipWebView2,
    [switch]$SkipLocalDB,
    [switch]$SkipBuild,
    [switch]$SkipTests,
    [switch]$SkipDownload,
    [switch]$ForceDownload,
    [switch]$NoZip,
    [switch]$NoInno,
    [string]$StageDir = "",
    [string]$CatalogPath = ""
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot  = Split-Path -Parent $ScriptDir

$Version = "0.1.6"
$wailsJson = Join-Path $RepoRoot "wails.json"
if (Test-Path $wailsJson) {
    try {
        $wj = Get-Content $wailsJson -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($wj.info -and $wj.info.productVersion) {
            $Version = ([string]$wj.info.productVersion).Trim().TrimStart("v")
        }
    } catch {}
}

if (-not $StageDir) {
    $StageDir = Join-Path $RepoRoot "build\offline"
}
if (-not $CatalogPath) {
    $CatalogPath = Join-Path $RepoRoot "internal\core\catalog.json"
}

$PrereqDir = Join-Path $StageDir "prereqs"
$EngineZipDir = Join-Path $StageDir "engines"
$EngineBinDir = Join-Path $StageDir "bin"
$DistDir = Join-Path $RepoRoot "dist"

# Resolve selected engines from -Engines or -Preset
$selectedEngines = New-Object System.Collections.Generic.List[string]
if ($Engines -and $Engines.Count -gt 0) {
    foreach ($raw in $Engines) {
        foreach ($tok in ($raw -split ',')) {
            $e = $tok.Trim().ToLowerInvariant()
            if ($e -and -not $selectedEngines.Contains($e)) {
                $selectedEngines.Add($e)
            }
        }
    }
} else {
    $presetMap = @{
        "Full"     = @("mariadb", "mysql", "postgresql", "mssql", "php", "apache", "redis", "mailpit")
        "WebStack" = @("apache", "php", "mariadb", "mailpit")
        "Database" = @("mariadb", "mysql", "postgresql", "mssql", "redis")
        "Minimal"  = @()
        "Custom"   = @()
    }
    foreach ($e in $presetMap[$Preset]) {
        $selectedEngines.Add($e)
    }
}

function Test-BinaryMagic([string]$Path, [string]$ExpectedKind) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $false }
    $len = (Get-Item -LiteralPath $Path).Length
    if ($len -lt 64) { return $false }
    $fs = [System.IO.File]::OpenRead($Path)
    try {
        $buf = New-Object byte[] 4
        $read = $fs.Read($buf, 0, 4)
        if ($read -lt 2) { return $false }
        switch ($ExpectedKind) {
            "zip" { return ($buf[0] -eq 0x50 -and $buf[1] -eq 0x4B) } # PK
            "exe" { return ($buf[0] -eq 0x4D -and $buf[1] -eq 0x5A) } # MZ
            "msi" { return ($buf[0] -eq 0xD0 -and $buf[1] -eq 0xCF) } # OLE Compound
            default { return $true }
        }
    } finally {
        $fs.Dispose()
    }
}

function Invoke-SafeDownload([string[]]$Urls, [string]$DestPath, [string]$Kind, [string]$Label) {
    if (-not $ForceDownload -and (Test-BinaryMagic $DestPath $Kind)) {
        $mb = [math]::Round((Get-Item -LiteralPath $DestPath).Length / 1MB, 2)
        Write-Host "  [cached] $Label ($mb MB)" -ForegroundColor DarkGray
        return $true
    }
    $part = "$DestPath.part"
    $headers = @{ "User-Agent" = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/$Version" }
    $lastErr = $null
    foreach ($u in $Urls) {
        if (-not $u) { continue }
        Remove-Item -Force $part -ErrorAction SilentlyContinue
        Write-Host "  [downloading] $Label <- $u" -ForegroundColor Cyan
        try {
            Invoke-WebRequest -Uri $u -OutFile $part -Headers $headers -UseBasicParsing -MaximumRedirection 10
            if (-not (Test-BinaryMagic $part $Kind)) {
                Remove-Item -Force $part -ErrorAction SilentlyContinue
                $lastErr = "Downloaded response was not a valid $Kind binary (likely HTML error page)"
                continue
            }
            Move-Item -Force $part $DestPath
            $mb = [math]::Round((Get-Item -LiteralPath $DestPath).Length / 1MB, 2)
            Write-Host "  [ok] $Label ($mb MB)" -ForegroundColor Green
            return $true
        } catch {
            Remove-Item -Force $part -ErrorAction SilentlyContinue
            $lastErr = $_.Exception.Message
        }
    }
    Write-Host "  [warn] Could not download $Label ($lastErr)" -ForegroundColor Yellow
    return $false
}

function Get-EngineDownloadCandidates([string]$Engine, [string]$Ver, [string]$PrimaryUrl) {
    $list = New-Object System.Collections.Generic.List[string]
    if ($PrimaryUrl) { $list.Add($PrimaryUrl) }
    $majMin = ($Ver -split '\.')[0..([Math]::Min(1, ($Ver -split '\.').Count - 1))] -join '.'
    switch ($Engine) {
        "mysql" {
            $list.Add("https://cdn.mysql.com/archives/mysql-$majMin/mysql-$Ver-winx64.zip")
            $list.Add("https://downloads.mysql.com/archives/get/p/23/file/mysql-$Ver-winx64.zip")
        }
        "php" {
            if ($PrimaryUrl -match '/downloads/releases/' -and $PrimaryUrl -notmatch '/archives/') {
                $list.Add($PrimaryUrl.Replace('/downloads/releases/', '/downloads/releases/archives/'))
            }
        }
        "mariadb" {
            $list.Add("https://dlm.mariadb.com/rest-api/mariadb/$Ver/mariadb-$Ver-winx64.zip")
            $list.Add("https://archive.mariadb.org/mariadb-$Ver/winx64-packages/mariadb-$Ver-winx64.zip")
        }
    }
    return @($list | Select-Object -Unique)
}

function Expand-EngineArchiveNormalized([string]$ZipPath, [string]$DestDir) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $tmpDir = "$DestDir.extracting"
    Remove-Item -Recurse -Force $tmpDir, $DestDir -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
    [System.IO.Compression.ZipFile]::ExtractToDirectory($ZipPath, $tmpDir)

    # Strip single shared top-level folder (e.g. mariadb-13.1.2-winx64/, pgsql/, Apache24/)
    # or unwrap a single subfolder holding the engine binaries when a stray ReadMe.txt sits at root.
    $topItems = @(Get-ChildItem -LiteralPath $tmpDir)
    $topDirs = @($topItems | Where-Object { $_.PSIsContainer })
    $src = $tmpDir
    if ($topItems.Count -eq 1 -and $topDirs.Count -eq 1) {
        $src = $topDirs[0].FullName
    } elseif ($topDirs.Count -eq 1 -and -not (Test-Path (Join-Path $tmpDir "bin")) -and -not (Test-Path (Join-Path $tmpDir "php.exe")) -and -not (Test-Path (Join-Path $tmpDir "redis-server.exe")) -and -not (Test-Path (Join-Path $tmpDir "mailpit.exe")) -and -not (Test-Path (Join-Path $tmpDir "sqlcmd.exe"))) {
        $cand = $topDirs[0].FullName
        if ((Test-Path (Join-Path $cand "bin")) -or (Test-Path (Join-Path $cand "php.exe")) -or (Test-Path (Join-Path $cand "redis-server.exe")) -or (Test-Path (Join-Path $cand "mailpit.exe")) -or (Test-Path (Join-Path $cand "sqlcmd.exe"))) {
            $src = $cand
        }
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $DestDir) | Out-Null
    Move-Item -LiteralPath $src -Destination $DestDir
    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
}

function Find-InnoSetupCompiler() {
    $cmd = Get-Command ISCC.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $candidates = @(
        "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
        "C:\Program Files\Inno Setup 6\ISCC.exe",
        (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe")
    )
    foreach ($c in $candidates) {
        if ($c -and (Test-Path -LiteralPath $c -PathType Leaf)) { return $c }
    }
    return $null
}

Push-Location $RepoRoot
try {
    Write-Host ""
    Write-Host "============================================================" -ForegroundColor Cyan
    Write-Host "  Darius DB v$Version - All-in-One Offline Bundle Packager" -ForegroundColor Cyan
    Write-Host "  Preset: $Preset | Engines: $(if ($selectedEngines.Count -gt 0) { $selectedEngines -join ', ' } else { 'none' })" -ForegroundColor DarkCyan
    Write-Host "============================================================" -ForegroundColor Cyan
    Write-Host ""

    # 1. Ensure core binaries & DariusDB-Setup-<Version>.exe are built
    $setupExe = Join-Path $DistDir "DariusDB-Setup-$Version.exe"
    if (-not $SkipBuild) {
        Write-Host "==> Building core Darius DB binaries and Setup Wizard..." -ForegroundColor Cyan
        $buildArgs = @{ SkipVersionGen = $true }
        if ($SkipTests) { $buildArgs["SkipTests"] = $true }
        & (Join-Path $ScriptDir "build-all.ps1") @buildArgs
    } else {
        if (-not (Test-Path $setupExe)) {
            $fallbackSetup = Join-Path $ScriptDir "DariusDB-Setup-$Version.exe"
            if (Test-Path $fallbackSetup) {
                New-Item -ItemType Directory -Force -Path $DistDir | Out-Null
                Copy-Item -Force $fallbackSetup $setupExe
            }
        }
    }

    New-Item -ItemType Directory -Force -Path $PrereqDir, $EngineZipDir, $EngineBinDir, $DistDir | Out-Null

    $manifestPrereqs = New-Object System.Collections.ArrayList
    $manifestEngines = New-Object System.Collections.ArrayList

    # 2. Stage Windows Prerequisites
    $prereqSpecs = @(
        @{
            Id   = "webview2"
            Name = "Microsoft Edge WebView2 Runtime (x64 Standalone Offline)"
            File = "MicrosoftEdgeWebView2RuntimeInstallerX64.exe"
            Kind = "exe"
            Skip = ($SkipPrereqs -or $SkipWebView2)
            Urls = @("https://go.microsoft.com/fwlink/?linkid=2124701")
        },
        @{
            Id   = "vcredist_x64"
            Name = "Visual C++ 2015-2026 Redistributable (x64)"
            File = "vc_redist.x64.exe"
            Kind = "exe"
            Skip = $SkipPrereqs
            Urls = @("https://aka.ms/vc14/vc_redist.x64.exe", "https://aka.ms/vs/17/release/vc_redist.x64.exe")
        },
        @{
            Id   = "mssql_localdb"
            Name = "Microsoft SQL Server Express LocalDB"
            File = "SqlLocalDB.msi"
            Kind = "msi"
            Skip = ($SkipPrereqs -or $SkipLocalDB)
            Urls = @("https://download.microsoft.com/download/3/8/d/38de7036-2433-4207-8eae-06e247e17b25/SqlLocalDB.msi")
        }
    )

    Write-Host "==> Staging Windows Prerequisites into $PrereqDir..." -ForegroundColor Cyan
    foreach ($pr in $prereqSpecs) {
        if ($pr.Skip) { continue }
        $dest = Join-Path $PrereqDir $pr.File
        if (-not $SkipDownload) {
            $ok = Invoke-SafeDownload -Urls $pr.Urls -DestPath $dest -Kind $pr.Kind -Label $pr.Name
            if (-not $ok) { continue }
        }
        if (Test-Path -LiteralPath $dest -PathType Leaf) {
            $hash = (Get-FileHash -LiteralPath $dest -Algorithm SHA256).Hash.ToLowerInvariant()
            [void]$manifestPrereqs.Add([PSCustomObject]@{
                id     = $pr.Id
                name   = $pr.Name
                file   = $pr.File
                sizeMB = [math]::Round((Get-Item -LiteralPath $dest).Length / 1MB, 2)
                sha256 = $hash
            })
        }
    }

    # 3. Stage Engines from internal\core\catalog.json
    $catalog = $null
    if (Test-Path -LiteralPath $CatalogPath -PathType Leaf) {
        $catalog = Get-Content -LiteralPath $CatalogPath -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    if ($selectedEngines.Count -gt 0 -and $catalog) {
        Write-Host "==> Staging Offline Engines into $StageDir..." -ForegroundColor Cyan
        foreach ($eng in $selectedEngines) {
            $entries = $catalog.$eng
            if (-not $entries -or $entries.Count -eq 0) {
                Write-Host "  [skip] No catalog entry for '$eng'" -ForegroundColor Yellow
                continue
            }
            $entry = $entries[0]
            $ver = [string]$entry.version
            $url = [string]$entry.url
            $zipName = "$eng-$ver.zip"
            $zipDest = Join-Path $EngineZipDir $zipName
            $binDest = Join-Path (Join-Path $EngineBinDir $eng) "$eng-$ver"

            if (-not $SkipDownload) {
                $cands = Get-EngineDownloadCandidates -Engine $eng -Ver $ver -PrimaryUrl $url
                $ok = Invoke-SafeDownload -Urls $cands -DestPath $zipDest -Kind "zip" -Label "$eng $ver"
                if ($ok -and ($ForceDownload -or -not (Test-Path -LiteralPath $binDest -PathType Container))) {
                    Write-Host "  [extracting] $zipName -> bin\$eng\$eng-$ver" -ForegroundColor DarkCyan
                    Expand-EngineArchiveNormalized -ZipPath $zipDest -DestDir $binDest
                }
            } elseif ((Test-Path -LiteralPath $zipDest -PathType Leaf) -and -not (Test-Path -LiteralPath $binDest -PathType Container)) {
                if (Test-BinaryMagic $zipDest "zip") {
                    Expand-EngineArchiveNormalized -ZipPath $zipDest -DestDir $binDest
                }
            }

            if (Test-Path -LiteralPath $zipDest -PathType Leaf) {
                $hash = (Get-FileHash -LiteralPath $zipDest -Algorithm SHA256).Hash.ToLowerInvariant()
                [void]$manifestEngines.Add([PSCustomObject]@{
                    engine  = $eng
                    version = $ver
                    file    = $zipName
                    sizeMB  = [math]::Round((Get-Item -LiteralPath $zipDest).Length / 1MB, 2)
                    sha256  = $hash
                })
            } elseif (Test-Path -LiteralPath $binDest -PathType Container) {
                [void]$manifestEngines.Add([PSCustomObject]@{
                    engine  = $eng
                    version = $ver
                    dir     = "bin/$eng/$eng-$ver"
                })
            }
        }
    }

    # Prune any empty engine directories under build\offline\bin\ so ISPP DirExists checks stay exact
    if (Test-Path -LiteralPath $EngineBinDir -PathType Container) {
        Get-ChildItem -LiteralPath $EngineBinDir -Directory -ErrorAction SilentlyContinue | ForEach-Object {
            if ((Get-ChildItem -LiteralPath $_.FullName -Recurse -File -ErrorAction SilentlyContinue).Count -eq 0) {
                Remove-Item -Recurse -Force $_.FullName -ErrorAction SilentlyContinue
            }
        }
    }

    # 4. Write offline-manifest.json
    $manifest = [PSCustomObject]@{
        name        = "Darius DB (Lite Version $Version) Offline Bundle"
        version     = $Version
        preset      = $Preset
        generatedAt = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss zzz")
        prereqs     = @($manifestPrereqs.ToArray())
        engines     = @($manifestEngines.ToArray())
    }
    $manifestPath = Join-Path $StageDir "offline-manifest.json"
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($manifestPath, ($manifest | ConvertTo-Json -Depth 5) + "`r`n", $utf8NoBom)

    # 5. Package Portable Offline USB Pack (.zip)
    $offlineZip = Join-Path $DistDir "DariusDB-$Version-offline-bundle-windows-amd64.zip"
    if (-not $NoZip) {
        Write-Host "==> Creating Portable Offline USB Pack ZIP..." -ForegroundColor Cyan
        $packStage = Join-Path $DistDir "DariusDB-$Version-offline-pack"
        Remove-Item -Recurse -Force $packStage, $offlineZip -ErrorAction SilentlyContinue
        New-Item -ItemType Directory -Force -Path (Join-Path $packStage "offline") | Out-Null

        if (Test-Path -LiteralPath $setupExe -PathType Leaf) {
            Copy-Item -Force $setupExe (Join-Path $packStage "DariusDB-Setup-$Version.exe")
        } else {
            Write-Host "  [warn] $setupExe not found; offline ZIP will contain staged payloads without Setup .exe" -ForegroundColor Yellow
        }
        Copy-Item -Force $manifestPath (Join-Path $packStage "offline\offline-manifest.json")

        if ((Test-Path $PrereqDir) -and (Get-ChildItem -LiteralPath $PrereqDir -File -ErrorAction SilentlyContinue).Count -gt 0) {
            Copy-Item -Recurse -Force $PrereqDir (Join-Path $packStage "offline\prereqs")
        }
        if ((Test-Path $EngineZipDir) -and (Get-ChildItem -LiteralPath $EngineZipDir -File -ErrorAction SilentlyContinue).Count -gt 0) {
            Copy-Item -Recurse -Force $EngineZipDir (Join-Path $packStage "offline\engines")
        }

        $batInteractive = @(
            "@echo off",
            "title Darius DB v$Version - Offline Setup",
            "echo Launching Darius DB v$Version Offline Setup Wizard...",
            "start """" ""%~dp0DariusDB-Setup-$Version.exe"" --offline-dir=""%~dp0offline"" --offline-only",
            ""
        ) -join "`r`n"
        [System.IO.File]::WriteAllText((Join-Path $packStage "Install-Offline.bat"), $batInteractive, $utf8NoBom)

        $batSilent = @(
            "@echo off",
            "title Darius DB v$Version - Silent Lab Deployment",
            "echo Installing Darius DB v$Version silently from local offline bundle...",
            """%~dp0DariusDB-Setup-$Version.exe"" --silent --dir=""C:\DariusDB"" --offline-dir=""%~dp0offline"" --offline-only --install-missing-prereqs --engines=all",
            "if errorlevel 1 (",
            "  echo Installation failed with exit code %ERRORLEVEL%.",
            "  pause",
            "  exit /b %ERRORLEVEL%",
            ")",
            "echo Darius DB v$Version offline installation completed successfully!",
            ""
        ) -join "`r`n"
        [System.IO.File]::WriteAllText((Join-Path $packStage "Install-Offline-Silent.bat"), $batSilent, $utf8NoBom)

        $readmeLines = New-Object System.Collections.Generic.List[string]
        $readmeLines.Add("================================================================================")
        $readmeLines.Add("  DARIUS DB (LITE VERSION $Version) - ALL-IN-ONE OFFLINE BUNDLE PACK")
        $readmeLines.Add("================================================================================")
        $readmeLines.Add("")
        $readmeLines.Add("Designed for school computer labs, USB flash drives, and air-gapped Windows PCs")
        $readmeLines.Add("with NO internet access.")
        $readmeLines.Add("")
        $readmeLines.Add("HOW TO INSTALL (INTERACTIVE WIZARD):")
        $readmeLines.Add("  1. Extract this folder anywhere (or copy to a USB drive).")
        $readmeLines.Add("  2. Double-click Install-Offline.bat (or DariusDB-Setup-$Version.exe).")
        $readmeLines.Add("  3. On the Requirements & Components step, bundled items appear marked [Offline]")
        $readmeLines.Add("     and install directly from the offline\ folder with zero internet access.")
        $readmeLines.Add("")
        $readmeLines.Add("HOW TO INSTALL (SILENT LAB / IMAGING DEPLOYMENT):")
        $readmeLines.Add("  Run Install-Offline-Silent.bat or:")
        $readmeLines.Add("    DariusDB-Setup-$Version.exe --silent --dir=""C:\DariusDB"" --offline-dir=""offline"" --offline-only --install-missing-prereqs --engines=all")
        $readmeLines.Add("")
        $readmeLines.Add("INCLUDED WINDOWS PREREQUISITES:")
        if ($manifestPrereqs.Count -eq 0) {
            $readmeLines.Add("  (none staged)")
        } else {
            foreach ($p in $manifestPrereqs) { $readmeLines.Add("  - $($p.name) [$($p.file), $($p.sizeMB) MB]") }
        }
        $readmeLines.Add("")
        $readmeLines.Add("INCLUDED OFFLINE ENGINES:")
        if ($manifestEngines.Count -eq 0) {
            $readmeLines.Add("  (none staged - Lite core app only)")
        } else {
            foreach ($e in $manifestEngines) { $readmeLines.Add("  - $($e.engine) $($e.version) [$($e.file), $($e.sizeMB) MB]") }
        }
        $readmeLines.Add("")
        [System.IO.File]::WriteAllText((Join-Path $packStage "README-OFFLINE.txt"), ($readmeLines -join "`r`n") + "`r`n", $utf8NoBom)

        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [System.IO.Compression.ZipFile]::CreateFromDirectory($packStage, $offlineZip, [System.IO.Compression.CompressionLevel]::Fastest, $false)
        Remove-Item -Recurse -Force $packStage
        $zipMB = [math]::Round((Get-Item -LiteralPath $offlineZip).Length / 1MB, 2)
        Write-Host "  [ok] Created $offlineZip ($zipMB MB)" -ForegroundColor Green
    }

    # 6. Compile Single-File Inno Setup All-in-One Offline Installer (if ISCC.exe is available)
    if (-not $NoInno) {
        $iscc = Find-InnoSetupCompiler
        if ($iscc) {
            Write-Host "==> Compiling All-in-One Inno Setup Offline Installer via $iscc..." -ForegroundColor Cyan
            $issPath = Join-Path $ScriptDir "DariusDB.iss"
            & $iscc "/DOfflineBundle=1" "/DMyAppVersion=$Version" "/DOfflineDir=$StageDir" $issPath
            if ($LASTEXITCODE -ne 0) { throw "ISCC.exe failed with exit code $LASTEXITCODE" }
            $innoExe = Join-Path $DistDir "DariusDB-Offline-Setup-$Version.exe"
            if (Test-Path $innoExe) {
                $innoMB = [math]::Round((Get-Item -LiteralPath $innoExe).Length / 1MB, 2)
                Write-Host "  [ok] Created $innoExe ($innoMB MB)" -ForegroundColor Green
            }
        } else {
            Write-Host ""
            Write-Host "==> Note: Inno Setup 6 (ISCC.exe) is not installed on this PC — skipped .exe compilation." -ForegroundColor Yellow
            Write-Host "    The Portable Offline USB Pack ZIP is ready right now in dist\." -ForegroundColor Yellow
            Write-Host "    To also build the single-file DariusDB-Offline-Setup-$Version.exe:" -ForegroundColor DarkYellow
            Write-Host "      1. Install Inno Setup 6:  winget install JRSoftware.InnoSetup" -ForegroundColor DarkYellow
            Write-Host "      2. Compile with:          ISCC.exe /DOfflineBundle=1 /DMyAppVersion=$Version installer\DariusDB.iss" -ForegroundColor DarkYellow
        }
    }

    Write-Host ""
    Write-Host "==> Offline packaging outputs:" -ForegroundColor Green
    Get-ChildItem (Join-Path $DistDir "*offline*") -ErrorAction SilentlyContinue |
        Select-Object Name, @{N = "SizeMB"; E = { [math]::Round($_.Length / 1MB, 2) } }, FullName |
        Format-Table -AutoSize
}
finally {
    Pop-Location
}
