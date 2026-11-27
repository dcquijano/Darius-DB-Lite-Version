<#
.SYNOPSIS
  Automatically generates version.json (changelog, announcement, build ID, release date, and URLs)
  directly from git commit history and wails.json version metadata.
  Created by Darius Cumpio - MIT License
#>
param(
    [string]$Version = "",
    [string]$BuildId = "",
    [string]$RepoSlug = "",
    [string]$OutputPath = "",
    [switch]$KeepBuild,
    [switch]$SyncCliGo,
    [int]$MaxCommits = 45
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $RepoRoot

function Format-CommitSummary([string]$RawSubject) {
    $s = ($RawSubject -replace '\s+', ' ').Trim()
    if (-not $s) { return "" }
    # Remove trailing [skip ci] / [ci skip] tags
    $s = ($s -replace '\s*\[(skip ci|ci skip)\]\s*$', '').Trim()
    # Strip conventional commit prefix (feat:, fix(scope):, chore:, etc.)
    $s = ($s -replace '^(feat|fix|chore|docs|perf|refactor|style|test|build|ci)(\([^)]+\))?:\s*', '').Trim()
    if (-not $s) { return "" }
    if ($s.Length -gt 1) {
        return $s.Substring(0, 1).ToUpper() + $s.Substring(1)
    }
    return $s.ToUpper()
}

try {
    # 1. Resolve canonical version from parameter -> wails.json -> existing version.json -> fallback
    if (-not $Version -and (Test-Path "wails.json")) {
        try {
            $wj = Get-Content "wails.json" -Raw -Encoding UTF8 | ConvertFrom-Json
            if ($wj.info -and $wj.info.productVersion) {
                $Version = ([string]$wj.info.productVersion).Trim().TrimStart("v")
            }
        } catch {}
    }

    $existing = $null
    if (Test-Path "version.json") {
        try {
            $existing = Get-Content "version.json" -Raw -Encoding UTF8 | ConvertFrom-Json
        } catch {}
    }

    if (-not $Version -and $existing -and $existing.version) {
        $Version = ([string]$existing.version).Trim().TrimStart("v")
    }
    if (-not $Version) {
        $Version = "0.1.6"
    }

    # 2. Resolve Build ID
    if (-not $BuildId) {
        if ($KeepBuild -and $existing -and $existing.build) {
            $BuildId = ([string]$existing.build).Trim()
        } else {
            $BuildId = Get-Date -Format "yyyyMMddHHmm"
        }
    }

    # 3. Collect git commits (newest first, excluding automated version.json loop commits)
    $changelog = New-Object System.Collections.Generic.List[string]
    $featureHighlights = New-Object System.Collections.Generic.List[string]
    $fallbackHighlights = New-Object System.Collections.Generic.List[string]
    $seenSubjects = @{}
    $latestDate = Get-Date -Format "yyyy-MM-dd"
    $latestSha = ""

    $gitExe = Get-Command git -ErrorAction SilentlyContinue
    if ($gitExe) {
        $prevEAP = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        $rawLines = & git log -n ($MaxCommits * 2) --no-merges --pretty=format:"%h|%cs|%s" 2>$null
        $ErrorActionPreference = $prevEAP

        foreach ($line in $rawLines) {
            if (-not $line) { continue }
            $parts = $line -split '\|', 3
            if ($parts.Count -lt 3) { continue }
            $sha = $parts[0].Trim()
            $date = $parts[1].Trim()
            $subj = $parts[2].Trim()

            if (-not $subj) { continue }
            if ($subj -match '^chore:\s*auto-update version\.json' -or $subj -match '\[skip ci\]') { continue }

            $clean = Format-CommitSummary $subj
            if (-not $clean) { continue }

            $dedupeKey = $clean.ToLowerInvariant()
            if ($seenSubjects.ContainsKey($dedupeKey)) { continue }
            $seenSubjects[$dedupeKey] = $true

            if (-not $latestSha) {
                $latestSha = $sha
                if ($date) { $latestDate = $date }
            }

            $entry = "[$sha] $clean ($date)"
            $changelog.Add($entry)

            if ($fallbackHighlights.Count -lt 3) {
                $fallbackHighlights.Add($clean)
            }
            $isRebuildOnly = ($subj -match '^(chore|build|ci)(\([^)]+\))?:' -or $clean -match '^(Rebuild|Auto-update|Update upgrade example)')
            if (-not $isRebuildOnly -and $featureHighlights.Count -lt 3) {
                $featureHighlights.Add($clean)
            }

            if ($changelog.Count -ge $MaxCommits) { break }
        }
    }

    if ($changelog.Count -eq 0 -and $existing -and $existing.changelog) {
        foreach ($item in $existing.changelog) {
            $changelog.Add([string]$item)
        }
    }

    $emDash = [char]0x2014
    $highlights = if ($featureHighlights.Count -gt 0) { $featureHighlights } else { $fallbackHighlights }
    if ($highlights.Count -gt 0) {
        $summaryList = ($highlights | ForEach-Object { ($_ -replace '\s*\(build\s+\d+\)\s*$', '').Trim().TrimEnd('.') }) -join '; '
        $announcement = "Darius DB (Lite Version $Version) $emDash Latest updates: $summaryList."
    } elseif ($existing -and $existing.announcement) {
        $announcement = [string]$existing.announcement
    } else {
        $announcement = "Darius DB (Lite Version $Version) $emDash Lightweight, portable full-stack web & database manager for Windows."
    }

    $channel      = if ($existing -and $existing.channel) { [string]$existing.channel } else { "Lite Version" }
    $author       = if ($existing -and $existing.author) { [string]$existing.author } else { "Darius Cumpio" }
    $installerUrl = if ($existing -and $existing.installerUrl) { [string]$existing.installerUrl } else { "https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing" }
    if (-not $RepoSlug -and $env:DARIUSDB_RELEASES_REPO) {
        $RepoSlug = $env:DARIUSDB_RELEASES_REPO.Trim()
    }
    if ($RepoSlug) {
        $slugClean = $RepoSlug.Trim().Trim('/')
        $repoUrl = "https://github.com/$slugClean"
    } elseif ($existing -and $existing.repoUrl) {
        $repoUrl = ([string]$existing.repoUrl).Trim().TrimEnd('/')
        $slugClean = ($repoUrl -replace '^https?://github\.com/', '').Trim('/')
    } else {
        $slugClean = "dcquijano/Darius-DB-Lite-Releases"
        $repoUrl = "https://github.com/$slugClean"
    }

    $payload = [ordered]@{
        name             = "Darius DB ($channel $Version)"
        version          = $Version
        build            = $BuildId
        channel          = $channel
        releasedAt       = $latestDate
        author           = $author
        latestCommit     = $latestSha
        generatedFromGit = $true
        installerUrl     = $installerUrl
        directSetupUrl   = "$repoUrl/releases/download/v$Version/DariusDB-Setup-$Version.exe"
        directZipUrl     = "$repoUrl/releases/download/v$Version/DariusDB-$Version-portable-windows-amd64.zip"
        fallbackSetupUrl = "https://raw.githubusercontent.com/$slugClean/main/installer/DariusDB-Setup-$Version.exe"
        repoUrl          = $repoUrl
        announcement     = $announcement
        changelog        = @($changelog)
    }

    # Format clean 2-space UTF-8 JSON without PowerShell 5.1 \u0026/\u0027/\u003c/\u003e HTML escaping
    function Escape-JsonStr([string]$v) {
        $e = $v.Replace('\', '\\').Replace('"', '\"').Replace("`r", '\r').Replace("`n", '\n').Replace("`t", '\t')
        return '"' + $e + '"'
    }
    $lines = New-Object System.Collections.Generic.List[string]
    $lines.Add('{')
    $keys = @($payload.Keys)
    for ($i = 0; $i -lt $keys.Count; $i++) {
        $k = $keys[$i]
        $v = $payload[$k]
        $comma = if ($i -lt $keys.Count - 1) { ',' } else { '' }
        if ($v -is [bool]) {
            $valStr = if ($v) { 'true' } else { 'false' }
            $lines.Add("  $(Escape-JsonStr $k): $valStr$comma")
        } elseif ($v -is [System.Array]) {
            $lines.Add("  $(Escape-JsonStr $k): [")
            for ($j = 0; $j -lt $v.Count; $j++) {
                $itemComma = if ($j -lt $v.Count - 1) { ',' } else { '' }
                $lines.Add("    $(Escape-JsonStr ([string]$v[$j]))$itemComma")
            }
            $lines.Add("  ]$comma")
        } else {
            $lines.Add("  $(Escape-JsonStr $k): $(Escape-JsonStr ([string]$v))$comma")
        }
    }
    $lines.Add('}')
    $json = ($lines -join "`r`n") + "`r`n"
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    $targetPath = if ($OutputPath) { $OutputPath } else { Join-Path $RepoRoot "version.json" }
    [System.IO.File]::WriteAllText($targetPath, $json, $utf8NoBom)

    if ($SyncCliGo -and (Test-Path "internal\core\cli.go")) {
        $cliPath = Join-Path $RepoRoot "internal\core\cli.go"
        $cliText = [System.IO.File]::ReadAllText($cliPath, $utf8NoBom)
        $updatedCli = [regex]::Replace($cliText, 'var CLIAppBuild = "[^"]*"', "var CLIAppBuild = `"$BuildId`"")
        if ($updatedCli -ne $cliText) {
            [System.IO.File]::WriteAllText($cliPath, $updatedCli, $utf8NoBom)
        }
    }

    Write-Host "==> Generated version.json from git commits: v$Version (build $BuildId, $($changelog.Count) commits)" -ForegroundColor Green
}
finally {
    Pop-Location
}
