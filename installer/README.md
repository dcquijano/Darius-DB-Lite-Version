# Darius DB (Lite Version 0.1.6) — Installer & Setup Guide

Step-by-step instructions for installing, updating, repairing, uninstalling, and building **Darius DB** from this `installer/` folder.

> **Back to Main Docs:** [`README.md`](../README.md) · **Full User Manual:** [`GUIDE.md`](../GUIDE.md)

---

## Option 1: Step-by-Step GUI Install (Recommended)

Use **`DariusDB-Setup-0.1.6.exe`** right inside this folder (no admin rights required):

1. **Double-click `DariusDB-Setup-0.1.6.exe`**.
   - *(If Windows SmartScreen appears on an unsigned open-source build, click **More info → Run anyway**.)*
   - *(If Darius DB is already installed, Setup offers automatic **Upgrade** or **Repair Mode** while keeping `data/`, `www/`, `backups/`, and `etc/` untouched.)*
2. **Accept License & Choose Destination:** Accept the MIT License, keep the default **`C:\DariusDB`** folder (or click **Browse...** to choose another directory), and pick optional **Desktop shortcut** / **Start with Windows** checkboxes.
3. **Requirements & Optional Components (Step 3 of 4):**
   - Setup automatically detects **Edge WebView2 Runtime**, **Visual C++ 2015–2026 x64**, and **SQL Server Express LocalDB** (showing `Installed` / `Missing`).
   - Check any missing prerequisites and optional engines (**MariaDB, MySQL, PostgreSQL, MS SQL Tools, PHP, Apache, Redis, Mailpit**) to pre-install during Setup — or click **Lite (None)** to uncheck all for a fast offline/Lite install.
   - *(If an `offline\` bundle folder is placed next to Setup, bundled components automatically show `[Offline]` and install with zero internet access.)*
4. **Click Install:** Setup extracts core executables, docs, folders (`www/`, `bin/`, `data/`, `etc/`, `logs/`, `backups/`, `tmp/`), installs any selected prerequisites/engines, runs Repair All diagnostics, creates Start Menu shortcuts, and adds `darius` to Windows `PATH`.
5. **Click Finish** to launch **Darius DB (Lite Version 0.1.6)** immediately.
6. **First-Run Inside Darius DB:**
   - Open **Versions** (`Alt+4`) anytime to download additional **Apache, PHP, MariaDB, MySQL, PostgreSQL, Redis, or Mailpit** builds *(built-in Apache, Mailpit, and MS SQL Server work immediately)*.
   - Press **`F8`** (**Start All**) on **Dashboard** (`Alt+1`).
   - Open **Database** (`Alt+2`) to browse tables, run SQL with **`Tab`** autocomplete, pop out tabs, or **right-click any database → New Database Diagram**.

> **Silent CLI Install / Pre-install / Offline Lab Deployment:**
> ```powershell
> .\DariusDB-Setup-0.1.6.exe --silent --dir="C:\DariusDB" --autostart
> .\DariusDB-Setup-0.1.6.exe --silent --install-missing-prereqs --engines=recommended
> .\DariusDB-Setup-0.1.6.exe --silent --offline-dir="D:\DariusDB-Offline\offline" --offline-only --install-missing-prereqs --engines=all
> .\DariusDB-Setup-0.1.6.exe --silent --repair
> ```

---

## Option 2: All-in-One Offline Bundle (School Labs & Air-Gapped PCs)

Build a self-contained offline package containing Windows prerequisites (**WebView2 x64**, **VC++ x64**, **SQL Server LocalDB**) plus pre-staged database/web engines:

```powershell
# Build full offline bundle (produces Offline USB Pack .zip + Inno Setup .exe if ISCC.exe is installed)
powershell -ExecutionPolicy Bypass -File .\installer\build-offline.ps1

# Or build a smaller Web + Database offline stack (Apache, PHP, MariaDB, Mailpit)
powershell -ExecutionPolicy Bypass -File .\installer\build-offline.ps1 -Preset WebStack -SkipLocalDB

# Or run full release build + offline bundle in one command
powershell -ExecutionPolicy Bypass -File .\installer\build-all.ps1 -Offline

# Compile Inno Setup single-file offline installer directly (requires Inno Setup 6 ISCC.exe)
ISCC.exe /DOfflineBundle=1 .\installer\DariusDB.iss
```

Outputs in `dist\`:
- **`DariusDB-0.1.6-offline-bundle-windows-amd64.zip`**: Extract to a USB drive and double-click `Install-Offline.bat` (or `Install-Offline-Silent.bat` for unattended lab imaging).
- **`DariusDB-Offline-Setup-0.1.6.exe`**: Single-file Inno Setup 6 installer with upfront requirement detection and selectable engine components (generated automatically when Inno Setup 6 is installed).

---

## Option 3: Install via PowerShell (`install.ps1`)

Run from the repository root or inside `installer\`:

```powershell
# Interactive install (asks install folder & Windows startup option)
powershell -ExecutionPolicy Bypass -File .\installer\install.ps1

# Silent install to C:\DariusDB with auto-start enabled
powershell -ExecutionPolicy Bypass -File .\installer\install.ps1 -InstallDir "C:\DariusDB" -Silent -AutoStart

# Force reinstall/overwrite over an existing C:\DariusDB install (keeps data/ and etc/)
powershell -ExecutionPolicy Bypass -File .\installer\install.ps1 -InstallDir "C:\DariusDB" -Force -Silent
```

*(Note: `install.ps1` copies executables from `installer\` or `..\build\bin\`; run `build-all.ps1` first if `DariusDB.exe` hasn't been built yet.)*

---

## Upgrading, Repairing & Uninstalling

### Update Existing Install (`DariusDB-Updater.exe` / `update.ps1`)
Upgrades binaries in-place while preserving `data/`, `www/`, `backups/`, `etc/`, and `settings.json`:
```powershell
# Launch native GUI Updater
.\DariusDB-Updater.exe

# Or silent CLI update using local DariusDB-Setup-*.exe or GitHub Releases
powershell -ExecutionPolicy Bypass -File .\installer\update.ps1 -Silent
```

### Repair System (`DariusDB-Repair.exe` / `repair.ps1`)
Fixes missing folders, corrupted `settings.json`, engine configs (`etc/*`), stale PID locks, blocked ports, UI/SQL caches, Windows `PATH`, and `bin\php\*\php.ini` ↔ `etc\php\php.ini` hardlinks:
```powershell
# Launch native Repair All GUI Wizard
.\DariusDB-Repair.exe

# Or run CLI repair
powershell -ExecutionPolicy Bypass -File .\installer\repair.ps1 -Silent
```

### Uninstall (`DariusDB-Uninstall.exe` / `uninstall.ps1`)
```powershell
# Launch 3-step native Uninstaller (asks whether to KEEP or remove data/, etc/, settings.json)
.\DariusDB-Uninstall.exe

# Silent uninstall keeping database files safe
powershell -ExecutionPolicy Bypass -File .\installer\uninstall.ps1 -Silent
```

---

## What Each File in `installer/` Does

| File / Folder | Purpose |
|---|---|
| `DariusDB-Setup-0.1.6.exe` | **Main all-in-one Windows Setup Wizard** (double-click to install, upgrade, or repair) |
| `DariusDB-Launcher.exe` | Verifies WebView2 runtime, sets `DARIUSDB_HOME`, supports `--minimized`, `--terminal`, `--start-all`, `--stop-all` |
| `DariusDB-Updater.exe` | Standalone GUI & CLI updater (checks GitHub `version.json` / Releases / local setup) |
| `DariusDB-Repair.exe` | Standalone full-system diagnostics & self-healing Repair All wizard |
| `DariusDB-Uninstall.exe` | Standalone 3-step Uninstall & Repair wizard |
| `install.ps1` | PowerShell script installer (uses built binaries in `installer\` or `..\build\bin\`) |
| `update.ps1` | PowerShell wrapper for in-place upgrades via `DariusDB-Updater.exe` or local/GitHub Setup |
| `repair.ps1` | PowerShell wrapper for `DariusDB-Repair.exe` / `darius.exe repair` |
| `uninstall.ps1` | PowerShell wrapper for `DariusDB-Uninstall.exe` |
| `build-all.ps1` | Runs Go tests, builds all 6 `.exe` binaries, portable `.zip` (`..\dist\`), and `DariusDB-Setup-0.1.6.exe` (pass `-Offline` to also package offline bundle) |
| `build-offline.ps1` | Stages WebView2/VC++/LocalDB + engines from `catalog.json` into an Offline USB `.zip` pack and Inno Setup `DariusDB-Offline-Setup-0.1.6.exe` |
| `generate-version.ps1` | Auto-generates `..\version.json` changelog & build metadata from `git log` |
| `DariusDB.iss` | Inno Setup 6 script supporting both Lite (`ISCC.exe`) and All-in-One Offline Bundle (`/DOfflineBundle=1`) builds |
| `bin/` | Pre-staged helper/resource folder used during packaging |
