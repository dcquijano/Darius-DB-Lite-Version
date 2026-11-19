# Darius DB (Lite Version 0.1.1) — Step-by-Step Installation & User Guide

> **Application:** Darius DB (Lite Version 0.1.1)  
> **Version:** `Lite Version 0.1.1`  
> **Created by:** Darius Cumpio  
> **Created Date:** November 19, 2026  
> **Year:** 2026  
> **Repository:** [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version)  
> **License:** MIT Open Source License

---

## Table of Contents

1. [System Requirements](#1-system-requirements)
2. [How to Install Darius DB (Step by Step)](#2-how-to-install-darius-db-step-by-step)
   - [Option A: Using the Standalone Windows Installer (`DariusDB-Setup-0.1.1.exe`)](#option-a-using-the-standalone-windows-installer-dariusdb-setup-011exe)
   - [Option B: 100% Portable (No Installation Required)](#option-b-100-portable-no-installation-required)
3. [First-Time Setup & Daily Usage Guide](#3-first-time-setup--daily-usage-guide)
4. [Using the Launcher (`DariusDB-Launcher.exe`)](#4-using-the-launcher-dariusdb-launcherexe)
5. [Developer CLI (`darius`) for Visual Studio, VS Code & Terminals](#5-developer-cli-darius-for-visual-studio-vs-code--terminals)
6. [How to Uninstall Darius DB](#6-how-to-uninstall-darius-db)
7. [How to Build from Source & Create Your Own Installer](#7-how-to-build-from-source--create-your-own-installer)
8. [Troubleshooting](#8-troubleshooting)
9. [Credits & Release Info](#9-credits--release-info)

---

## 1. System Requirements

- **OS:** Windows 10 or Windows 11 (64-bit / x64)
- **Runtime:** Microsoft Edge WebView2 Runtime (already pre-installed on Windows 11 and modern Windows 10; the launcher checks for it automatically)
- **Disk Space:** ~15 MB for Darius DB itself, plus whichever database engines you choose to download
- **Admin Rights:** **Not required** — Darius DB runs and installs cleanly in user space (e.g. `C:\DariusDB`)

---

## 2. How to Install Darius DB (Step by Step)

### Option A: Using the Standalone Windows Installer (`DariusDB-Setup-0.1.1.exe`)

1. Double-click **`DariusDB-Setup-0.1.1.exe`** (located in `installer\DariusDB-Setup-0.1.1.exe` or `dist\DariusDB-Setup-0.1.1.exe`).
2. In the **Welcome to Darius DB (Lite Version 0.1.1) Setup** dialog:
   - Click **Yes** to install to **`C:\DariusDB`** (recommended), or
   - Click **No** to open the Windows folder browser and pick any custom folder.
3. Choose whether to **Start Darius DB automatically when Windows signs in** (Yes / No).
4. Setup automatically:
   - Extracts `DariusDB.exe`, `DariusDB-Launcher.exe`, `DariusDB-Uninstall.exe`, `LICENSE`, `README.md`, and `GUIDE.md`.
   - Creates `bin/`, `data/`, `etc/`, `logs/`, and `tmp/`.
   - Creates Desktop & Start Menu shortcuts (**Darius DB**, **Darius DB Terminal**, **Uninstall Darius DB**).
   - Registers **Darius DB (Lite Version 0.1.1)** in **Windows Settings → Apps → Installed Apps**.
5. Click **Yes** on the completion dialog to launch **Darius DB (Lite Version 0.1.1)** immediately.

> **Silent / Automated CLI install:**
> ```powershell
> .\DariusDB-Setup-0.1.1.exe --silent --dir="C:\DariusDB" --autostart
> ```

---

### Option B: 100% Portable (No Installation Required)

1. Create any folder, e.g. `D:\Tools\DariusDB\` or on a USB flash drive.
2. Copy **`DariusDB.exe`** (and optionally `DariusDB-Launcher.exe`) into that folder.
3. Double-click **`DariusDB-Launcher.exe`** (or `DariusDB.exe` directly).
4. Darius DB automatically creates `bin/`, `data/`, `etc/`, `logs/`, `tmp/`, and `settings.json` right beside itself.
5. To move Darius DB to another PC or drive later: stop all services, then simply copy/move the whole folder!

---

## 3. First-Time Setup & Daily Usage Guide

### Step 1: Download Your Database Engines

1. Open Darius DB and click **Versions** in the left sidebar (or click **Install Engines** on the welcome banner).
2. Select an engine tab at the top: **MariaDB**, **MySQL**, **PostgreSQL**, **MS SQL Server**, or **Redis**.
3. Click **Download** next to the version you want (e.g., MariaDB `13.0.2` / `12.3.4` LTS, MySQL `9.7.2` / `8.4.11` LTS, PostgreSQL `18.7` / `17.12`, MS SQL Server 2025 `17.0.1000.7`, or Redis `8.10.3`).
   - Prefer an offline archive? Click **Install .zip** and select any official Windows portable `.zip` you already downloaded.
4. Wait for the progress bar to finish downloading and extracting into `bin/<engine>/`.

### Step 2: Start Your Databases

1. Return to the **Dashboard** tab.
2. Click **Start All** to start all enabled engines at once, or click **Start** on an individual engine card.
3. On the very first start, Darius DB automatically generates clean default config files (`etc/mariadb/my.ini`, `etc/redis/redis.conf`) and initializes the engine's `data/` folder.
4. Once running, the status dot turns green and displays the active **port** and **PID**.

### Step 3: Browse Databases, Create Users & Run SQL / Redis Commands

1. Click **Browse** on any running engine card (or open the **Database** tab).
2. **For MariaDB, MySQL, PostgreSQL, and MS SQL Server:**
   - Click **+ New** to create a database (and optionally create a dedicated user/login with password and full privileges on that database in one step!).
   - Click any database on the left to list its tables and views.
   - Click any table to preview `SELECT * FROM ... LIMIT 100;`.
   - Write any SQL query in the editor and press **`Ctrl + Enter`** to run it.
   - Click **Copy URI** to copy a ready-to-paste connection string (`mysql://...`, `postgresql://...`) for `.env` files!
3. **For Redis:**
   - Pick a logical DB (`db0`–`db15`) from the dropdown.
   - Filter keys by pattern (e.g. `session:*`) and click **Scan**.
   - Click a key to inspect its value (JSON strings are automatically formatted), TTL, and size, or delete it.
   - Use the interactive **`redis>`** command bar at the bottom (`SET hello world`, `GET hello`, `INFO server`, etc.).

### Step 4: Switch Between Multiple Engine Versions

1. Download two or more versions of an engine in **Versions** (for example, MariaDB `11.4.14` and `10.11.20`, or PostgreSQL `18.7` and `16.16`).
2. Switch the active version directly from the dropdown on the **Dashboard** card (or click **Use Version** in **Versions**).
3. If the service is running, Darius DB automatically stops the old version and starts the new one.
4. Each major version keeps its own data directory (`data/mariadb-11.4`, `data/postgresql-17`, etc.) so switching versions never corrupts your data.

### Step 5: Edit Configs & Change Ports

- **Change ports or admin credentials:** Open **Settings**, edit the Port / Admin User / Password table, and click **Save Settings**. Restart the service for the new port to take effect.
- **Edit raw config files (`my.ini`, `postgresql.conf`, `redis.conf`):** Open **Config & Logs** → **Config File**, make edits, and press **`Ctrl + S`** (or click **Save & Restart** if running). Click **Notepad** if you prefer editing externally.
- **View logs:** Switch to **Service Log** in **Config & Logs** (auto-refreshes every 2 seconds).

### Step 6: Open the Pre-Configured Terminal

Click **Terminal** in the bottom-left sidebar (or run `DariusDB-Launcher.exe --terminal`). A Command Prompt opens with:
- Active `mariadb`/`mysql`, `psql`, and `redis-cli` binaries already prepended to `PATH`.
- `PGHOST`, `PGPORT`, `PGUSER`, `MYSQL_HOST`, `MYSQL_TCP_PORT`, and `DARIUSDB_HOME` set automatically.

### Step 7: System Tray & Auto-Start on Windows Boot

- Clicking the window's **`×`** button minimizes Darius DB quietly into the **Windows System Tray** (when *Close window to tray* is enabled in Settings).
- **Left-click** the tray icon to restore the window.
- **Right-click** the tray icon for quick actions: Start All, Stop All, start/stop individual engines, Open Terminal, Open Data Folder, or Quit.
- Enable **Run on Windows boot** and **Auto-start enabled services** in **Settings** for a hands-free automatic workflow.

---

## 4. Using the Launcher (`DariusDB-Launcher.exe`)

`DariusDB-Launcher.exe` is a tiny (~2 MB) native helper placed beside `DariusDB.exe`.

| Command | Action |
|---|---|
| Double-click `DariusDB-Launcher.exe` | Verifies WebView2 Runtime is installed, sets `DARIUSDB_HOME`, and launches `DariusDB.exe` |
| `DariusDB-Launcher.exe --minimized` | Launches Darius DB hidden in the Windows system tray |
| `DariusDB-Launcher.exe --terminal` | Opens the pre-configured DB Terminal directly without opening the GUI |
| `DariusDB-Launcher.exe --start-all` | Starts all enabled installed database engines headlessly (great for scripts) |
| `DariusDB-Launcher.exe --stop-all` | Gracefully stops all running database engines |
| `DariusDB-Launcher.exe --version` | Shows version and creator credits dialog |

---

## 5. Developer CLI (`darius`) for Visual Studio, VS Code & Terminals

`darius.exe` is installed automatically into `C:\DariusDB` and registered in your Windows User `PATH` (or click **Add CLI & DB Tools to Windows PATH** in **Settings**).

You can run **`darius`** from **Visual Studio (Package Manager Console / Developer PowerShell)**, **VS Code Integrated Terminal**, **Windows Terminal**, **PowerShell**, **CMD**, or **Git Bash**:

```powershell
# Check all database engines, states, versions, ports, and PIDs
darius status

# Start / stop / restart services from your IDE terminal
darius start all
darius start mssql
darius stop redis
darius restart mariadb

# Generate ready-to-paste Visual Studio (appsettings.json) & VS Code (.env) connection blocks
darius env mssql MyStoreDB
darius env mariadb my_laravel_app
darius env postgresql my_node_app

# Print raw connection URI / ADO.NET string
darius uri mssql MyStoreDB

# List databases and tables
darius dbs mssql
darius tables mssql msdb

# Create or drop a database (with optional user & password)
darius createdb mariadb shop_db shop_user secret123
darius createdb mssql ShopDB
darius dropdb mssql ShopDB

# Execute SQL / T-SQL / Redis commands directly from your IDE terminal
darius sql mssql "SELECT name, database_id FROM sys.databases" -d master
darius sql mariadb "SHOW DATABASES;"
darius sql redis "PING"

# Manage portable engine versions from CLI
darius versions
darius install redis 8.10.3
darius use redis 8.10.3

# Register or remove Darius DB & active engine bin/ folders in Windows PATH
darius path --add
darius path --remove

# Open the Darius DB desktop window from terminal
darius gui
```

---

## 6. How to Uninstall Darius DB

### Method 1: Windows Settings (Recommended)

1. Open **Windows Settings → Apps → Installed Apps** (or *Control Panel → Programs and Features*).
2. Find **Darius DB** (Publisher: *Darius Cumpio*) and click **Uninstall**.
3. Confirm uninstall.
4. When asked **"Do you want to KEEP your database data and config files?"**:
   - Click **Yes** to preserve `data/`, `etc/`, and `settings.json` safely (recommended).
   - Click **No** to completely remove every file including databases.
5. The uninstaller automatically stops running services, removes Windows Startup & Apps registry entries, removes Desktop & Start Menu shortcuts, deletes program files, and cleans up itself.

### Method 2: Double-Click `DariusDB-Uninstall.exe`

Run `DariusDB-Uninstall.exe` directly inside your Darius DB folder (e.g. `C:\DariusDB\DariusDB-Uninstall.exe`).

> **Silent CLI uninstall:**
> ```powershell
> # Keep database files:
> .\DariusDB-Uninstall.exe --silent --keep-data
>
> # Remove everything including databases:
> .\DariusDB-Uninstall.exe --silent --remove-data
> ```

### Method 3: Portable Uninstall

If you used the portable folder without running an installer:
1. Quit Darius DB (make sure all services are stopped and *Run on Windows boot* is unchecked).
2. Delete the Darius DB folder.

---

## 7. How to Build from Source & Create Your Own Installer

### Prerequisites

1. Install **Go 1.24+**:
   ```powershell
   winget install GoLang.Go
   ```
2. Install **Wails CLI v2**:
   ```powershell
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```
3. *(Optional — only needed to compile `DariusDB-Setup-0.1.1.exe`)* Install **Inno Setup 6**:
   ```powershell
   winget install JRSoftware.InnoSetup
   ```

> **Note:** No C compiler (GCC/CGO) and no Node.js/npm build step are required!

### One-Command Build (GUI + Launcher + Uninstaller + Portable ZIP + Setup EXE)

Run the build script from the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File installer\build-all.ps1
```

This creates:
- `build\bin\DariusDB.exe` — main desktop app (~13 MB)
- `build\bin\DariusDB-Launcher.exe` — native launcher (~2–3 MB)
- `build\bin\DariusDB-Uninstall.exe` — native uninstaller (~2–3 MB)
- `dist\DariusDB-0.1.1-portable-windows-amd64.zip` — portable archive
- `dist\DariusDB-Setup-0.1.1.exe` (and `installer\DariusDB-Setup-0.1.1.exe`) — standalone single-file native Windows installer `.exe`

### Manual Inno Setup Build

If you already built `build\bin\*.exe` and want to compile just the Inno Setup `.exe` installer:

```powershell
"C:\Program Files (x86)\Inno Setup 6\ISCC.exe" installer\DariusDB.iss
```

### Automated GitHub Releases

Pushing a version tag (for example `git tag v0.1.1 && git push origin v0.1.1`) triggers [`.github/workflows/release.yml`](.github/workflows/release.yml), which automatically runs tests, builds all binaries, packages the portable ZIP, compiles `DariusDB-Setup-*.exe` with Inno Setup, and uploads both artifacts!

---

## 8. Troubleshooting

- **"Port XXXX is already used by another program"** — Another local database server, container, or Windows service is already listening on that port. Open **Settings**, assign a different port (e.g. `3307` or `5433`), save, and start again.
- **PostgreSQL `postgresql.conf` not found yet** — PostgreSQL creates `postgresql.conf` inside `data/postgresql-<major>/` during its first start (`initdb`). Start PostgreSQL once, then edit it in **Config & Logs**.
- **PowerShell says running scripts is disabled** — Run the native `.exe` installer **`DariusDB-Setup-0.1.1.exe`** instead of `.ps1` scripts.
- **Windows SmartScreen warning on unsigned open-source `.exe`** — Because Darius DB is a community open-source build without a paid code-signing certificate, Windows SmartScreen may show *"Windows protected your PC"*. Click **More info → Run anyway** (or build directly from this source code).

---

## 9. Credits & Release Info

| Field | Details |
|---|---|
| **Project Name** | Darius DB (Lite Version 0.1.1) |
| **Version** | `Lite Version 0.1.1` |
| **Creator / Author** | **Darius Cumpio** |
| **Built By** | Darius Cumpio |
| **Created Date** | November 19, 2026 |
| **Year** | 2026 |
| **Repository** | [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version) |
| **License** | MIT License (`Copyright (c) 2026 Darius Cumpio`) |
