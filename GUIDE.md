# Darius DB (Lite Version 0.1.5) — Step-by-Step Installation & User Guide

> **Application:** Darius DB (Lite Version 0.1.5)  
> **Version:** `Lite Version 0.1.5`  
> **Created by:** Darius Cumpio  
> **Created Date:** November 19, 2026  
> **Year:** 2026  
> **Repository:** [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version)  
> **Official Installer Download (Google Drive):** [https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing](https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing)  
> **License:** MIT Open Source License

---

## Table of Contents

1. [System Requirements](#1-system-requirements)
2. [How to Install Darius DB (Step by Step)](#2-how-to-install-darius-db-step-by-step)
   - [Option A: Using the Standalone Windows Installer (`DariusDB-Setup-0.1.5.exe`)](#option-a-using-the-standalone-windows-installer-dariusdb-setup-015exe)
   - [Option B: 100% Portable (No Installation Required)](#option-b-100-portable-no-installation-required)
3. [First-Time Setup & Daily Usage Guide](#3-first-time-setup--daily-usage-guide)
4. [Keyboard Shortcuts Reference](#4-keyboard-shortcuts-reference)
5. [Using the Launcher (`DariusDB-Launcher.exe`)](#5-using-the-launcher-dariusdb-launcherexe)
6. [Developer CLI (`darius`) for Visual Studio, VS Code & Terminals](#6-developer-cli-darius-for-visual-studio-vs-code--terminals)
7. [How to Uninstall Darius DB](#7-how-to-uninstall-darius-db)
8. [How to Build from Source & Create Your Own Installer](#8-how-to-build-from-source--create-your-own-installer)
9. [Troubleshooting](#9-troubleshooting)
10. [Credits & Release Info](#10-credits--release-info)

---

## 1. System Requirements

- **OS:** Windows 10 or Windows 11 (64-bit / x64)
- **Runtime:** Microsoft Edge WebView2 Runtime (already pre-installed on Windows 11 and modern Windows 10; the launcher checks for it automatically)
- **Disk Space:** ~17 MB for Darius DB itself, plus whichever database engines you choose to download
- **Admin Rights:** **Not required** — Darius DB runs and installs cleanly in user space (e.g. `C:\DariusDB`)

---

## 2. How to Install Darius DB (Step by Step)

### Option A: Using the Standalone Windows Installer (`DariusDB-Setup-0.1.5.exe`)

1. Download **`DariusDB-Setup-0.1.5.exe`** from [**GitHub Releases**](https://github.com/dcquijano/Darius-DB-Lite-Version/releases) or the [**Official Google Drive Installer Folder**](https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing) (or run local build output `installer\DariusDB-Setup-0.1.5.exe` or `dist\DariusDB-Setup-0.1.5.exe`).
2. In the **Darius DB (Lite Version 0.1.5) Setup Wizard**:
   - Accept the MIT License Agreement and click **Next >**.
   - Keep the recommended destination folder **`C:\DariusDB`** (or click **Browse...** to choose another folder) and select your shortcut/PATH/startup options.
   - Click **Install**.
3. Setup automatically:
   - Extracts `DariusDB.exe`, `DariusDB-Launcher.exe`, `DariusDB-Repair.exe`, `DariusDB-Uninstall.exe`, `DariusDB-Updater.exe`, `darius.exe`, `LICENSE`, `README.md`, and `GUIDE.md`.
   - Creates `www/`, `backups/`, `bin/`, `data/`, `etc/`, `logs/`, and `tmp/`.
   - Creates Desktop & Start Menu shortcuts (**Darius DB**, **Darius DB Terminal**, **Update Darius DB**, **Repair Darius DB**, **Uninstall Darius DB**).
   - Registers **Darius DB (Lite Version 0.1.5)** in **Windows Settings → Apps → Installed Apps** and adds `darius` CLI to your Windows `PATH`.
   - **Upgrade, Double-Install Protection & Repair Mode:** If an older version (e.g. `0.1.4`) is installed, running `DariusDB-Setup-0.1.5.exe` offers a 1-click **in-place upgrade** (keeping all databases in `data/`, backups in `backups/`, and websites in `www/` untouched). If the same version is already installed, it blocks duplicate installation and offers **Repair Mode**.
4. Click **Finish** to launch **Darius DB (Lite Version 0.1.5)** immediately.

> **Silent / Automated CLI install or repair:**
> ```powershell
> .\DariusDB-Setup-0.1.5.exe --silent --dir="C:\DariusDB" --autostart
> .\DariusDB-Setup-0.1.5.exe --silent --repair
> ```

---

### Option B: 100% Portable (No Installation Required)

1. Download **`DariusDB-0.1.5-portable-windows-amd64.zip`** from [**GitHub Releases**](https://github.com/dcquijano/Darius-DB-Lite-Version/releases) or [**Google Drive**](https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing), and extract it to any folder (e.g. `D:\Tools\DariusDB\` or a USB drive).
2. Double-click **`DariusDB-Launcher.exe`** (or **`DariusDB.exe`** directly).
3. Darius DB automatically creates `www/`, `backups/`, `bin/`, `data/`, `etc/`, `logs/`, `tmp/`, and `settings.json` right beside itself.
4. To move Darius DB to another PC or drive later: stop all services (`F9`), then simply copy or move the whole folder!

---

## 3. First-Time Setup & Daily Usage Guide

> **Built-in In-App Guide:** Inside Darius DB, open **Guide & Help** in the left sidebar (or press **`Alt + 8`**) for an interactive quick-start checklist, collapsible step-by-step walkthroughs, copyable CLI commands, and a searchable shortcuts reference. Press **`F1`**, **`Ctrl + /`**, or **`?`** anytime for the quick Keyboard Shortcuts popup.

### Step 1: Download Your Database & Web Engines

1. Open Darius DB and click **Versions** in the left sidebar (or press **`Alt + 4`** / click **Install Engines** on the welcome banner).
2. Select an engine tab at the top: **Apache**, **PHP**, **MariaDB**, **MySQL**, **PostgreSQL**, **MS SQL Server**, **Redis**, or **Mailpit**.
3. Click **↻ Check Newest Versions** (**`Alt + R`**) to discover the latest upstream releases.
4. Click **Download** next to the version you want (e.g., MariaDB `13.0.2` / `12.3.4` LTS, MySQL `9.7.2` / `8.4.11` LTS, PostgreSQL `18.7` / `17.12`, MS SQL Server 2025 `17.0.1000.7`, Redis `8.10.3`, PHP `8.4`/`8.3`/`8.2`, or Mailpit).
   - Prefer an offline archive? Click **Import .zip** (**`Alt + I`**) and select any official Windows portable `.zip` you already downloaded.
5. Wait for the progress bar to finish downloading and extracting into `bin/<engine>/`.

### Step 2: Start Services, Auto-Run & Local Web Hosting (`www/`)

1. Return to the **Dashboard** tab (**`Alt + 1`**).
2. Click **Start All** (**`F8`**, **`Ctrl + Shift + S`**, or **`Alt + Shift + S`**) to start all enabled engines at once, or click **Start** on an individual engine row. Press **`F9`**, **`Ctrl + Shift + X`**, or **`Alt + Shift + X`** for **Stop All** (use **`F8`** / **`F9`** / **`Alt + Shift + X`** if Windows or Edge intercepts `Ctrl + Shift + X`).
3. Use the **Auto-Run Bar** at the top of Dashboard to choose which engines auto-start when Darius DB opens (or pick quick presets like *MySQL + Apache* or *Match Running*).
4. **Host local websites & PHP apps (`www/`):** Start **Apache** (`:8080`, integrated with PHP 8.x), place files in `www/` or click **Deploy .zip**, and open `http://localhost:8080/`. Start **Mailpit** (`:8025` Web UI / `:1025` SMTP) to inspect local outgoing emails.
5. On the very first start, Darius DB automatically generates clean default config files (`etc/*`) and initializes each engine's `data/` folder. Once running, the status dot turns green and displays the active **port** and **PID**.

### Step 3: Browse Databases, ER Diagrams, Floating SQL Tabs, Row CRUD & Redis

1. Click **Browse** on any running engine card (or press **`Alt + 2`** to open **Database**).
2. **For MariaDB, MySQL, PostgreSQL, and MS SQL Server (Local & Cloud):**
   - **Databases & Right-Click Context Menu:** Click **+ New** (**`Alt + D`**) to create a database (+ optional user/login), or **right-click any database or table** for quick actions (`🗺️ New Database Diagram`, `📝 New Query Tab`, `➕ Create Table...`, `↻ Refresh`, `📤 Export`, `🗑️ Drop`).
   - **Interactive ER Database Diagrams (`🗺️ Diagram`):** Right-click a database or click **🗺️ Diagram** in the Tables header to explore draggable table cards, solid Foreign Key + optional dashed inferred `*_id` links, Auto-Arrange, Fit View, Table picker, double-click/open table rows, and **`📤 Export ▾`** (**PNG**, **JPEG**, **WebP**, **SVG**, **HTML**, **Mermaid `.mmd`**, or **Copy PNG to Clipboard**).
   - **Multi-Tab SQL Editor, Autocomplete & Tear-Off Floating Windows:**
     - Press **`Tab`** inside the SQL editor for IDE-style autocomplete (`CREATE`, `SHOW`, `SELECT`, aggregate functions, tables, and columns).
     - Open extra query tabs with **`Alt + N`**, switch tabs with **`Alt + [`** / **`Alt + ]`**, close one tab with **`Alt + W`**, or clear all tabs with **`✕ Close All`**.
     - Drag any tab out (or click **`↗`**) into a floating query/diagram window — click **`↩ Put Back`** or drag its titlebar back onto the tab bar to re-dock!
     - Press **`Ctrl + Enter`** to run SQL or **`Ctrl + Shift + Enter`** for **Show All** (removes `LIMIT` / `TOP`).
   - **Visual Row CRUD & Pagination:** Double-click any row (or use **+ Create** via **`Alt + =`**, **Show**, **Edit**, **Delete**) for full visual Row CRUD. Filter visible rows with **`Ctrl + F`** and paginate with **`Alt + ←`** / **`Alt + →`**.
   - Click **Copy URI** (**`Alt + C`**) or **VS / .env** to copy ready-to-paste connection strings for Visual Studio and VS Code!
3. **For Redis:**
   - Pick a logical DB (`db0`–`db15`) from the dropdown.
   - Focus the pattern box (**`Ctrl + F`**), filter keys (e.g. `session:*`), and press `Enter` / click **Scan**.
   - Click a key to inspect its value (JSON strings are automatically formatted), TTL, and size, or delete it.
   - Use the interactive **`redis>`** command bar at the bottom (`SET hello world`, `GET hello`, `INFO server`, etc.).

### Step 4: Import, Export, Full `.zip` Backup & `.bak` Restore

1. **Import (`Alt + I`):** In the **Database** tab, click **📥 Import**, browse for `.sql`, `.csv` (auto-creates columns from CSV headers), MS SQL `.bak`, or PostgreSQL `.backup`, confirm the target database name (auto-created if new), and click **Import Now**.
2. **Export (`Alt + E`):** Click **📤 Export** to export the *Entire Database*, *Current Table*, or *Selected Tables* in `.sql` (toggle Structure / Data), `.zip`, or single-table `.csv`.
3. **Full `.zip` Backup (`Alt + B`):** Click **📦 Backup .zip** to generate a portable `.zip` snapshot containing the full SQL dump, per-table CSV files, and `manifest.json`.

### Step 5: Cloud Databases & Daily 2:00 AM Auto-Sync / Auto-Backup

1. Open **Cloud** (**`Alt + 3`**) and pick a **Quick Provider Preset** (cPanel/Plesk/Hostinger, MonsterASP.NET/SmarterASP, Supabase/Neon, Aiven/TiDB/PlanetScale, AWS RDS/Azure, or Redis Cloud) or click **+ Add Cloud DB** (**`Alt + N`**).
2. Paste a connection URI or ADO.NET string into **Quick Auto-Fill** and click **Parse**, then click **🔌 Test Connection** and **Save Profile**.
3. Click **Browse** on any Cloud profile to query, edit rows, import, and export live remote databases directly in the Database tab.
4. Click **Manual Sync** (**`Alt + S`**) to run a one-time Push (`Local → Cloud`) or Pull (`Cloud → Local`, including optional **✨ Auto-generate Local DB name** with Date, Day, Year & Timestamp). You can minimize active syncs into the unified floating progress bar.
5. Manage **Daily Auto-Sync** (`+ Auto-Sync`, supporting Local DB or **Generated Folder**) and **Daily 2:00 AM Auto-Backup** (`+ Auto-Backup`, supporting dated `.zip` snapshots or Generated Folders with live **Purge Date Guide** & 7-day automatic retention purge) in their own dedicated panels, inspect history in **📜 Sync Logs** (`scheduler.log`), and toggle **🪟 Win 2 AM Task** for Windows Task Scheduler wake-up.

### Step 6: Switch Between Multiple Engine Versions

1. Download two or more versions of an engine in **Versions** (for example, MariaDB `11.4.14` and `10.11.20`, or PostgreSQL `18.7` and `16.16`).
2. Switch the active version directly from the dropdown on the **Dashboard** card (or click **Use Version** in **Versions**).
3. If the service is running, Darius DB automatically stops the old version and starts the new one.
4. Each major version keeps its own data directory (`data/mariadb-11.4`, `data/postgresql-17`, etc.) so switching versions never corrupts your data.

### Step 7: Edit Configs, View Logs & Change Ports

- **Change ports or admin credentials:** Open **Settings** (**`Alt + 7`**), edit the Port / Admin User / Password table, and press **`Ctrl + S`** (or click **Save Settings**). Restart the service for the new port to take effect.
- **Edit raw config files (`httpd.conf`, `my.ini`, `postgresql.conf`, `mssql.conf`, `redis.conf`):** Open **Config & Logs** (**`Alt + 5`**) → **Config File**, make edits, and press **`Ctrl + S`** to save or **`Ctrl + Shift + Enter`** to **Save & Restart**. Click **Notepad** if you prefer editing externally.
- **View logs:** Press **`Alt + L`** (or click **Service Log**) in **Config & Logs** (auto-refreshes every 2 seconds).

### Step 8: Use the Built-in Multi-Mode Terminal

Click **Terminal** in the sidebar (or press **`Alt + 6`** / **`Ctrl + \``**, or run `DariusDB-Launcher.exe --terminal`).
- Switch modes between **Shell (CMD)**, **MariaDB**, **MySQL**, **PostgreSQL (`psql`)**, **MS SQL (`sqlcmd`)**, and **Redis CLI** via tabs or **`Alt + [`** / **`Alt + ]`**.
- Use **`↑`** / **`↓`** for command history, **`Ctrl + L`** to clear output, and **`Ctrl + C`** to cancel a running command.
- Active engine binaries and environment variables (`PGHOST`, `PGPORT`, `PGUSER`, `MYSQL_HOST`, `MYSQL_TCP_PORT`, `DARIUSDB_HOME`) are pre-configured automatically.

### Step 9: Self-Healing Repair, System Updater & System Tray

- **🔧 Repair Service (`Ctrl + Shift + R`):** Diagnoses and repairs a single engine (folders, configs, stale PID locks) while keeping `data/` safe.
- **🛠️ Repair All (`Ctrl + Shift + A`):** Full-system diagnostics across folders, `settings.json`, stale PIDs/orphans, blocked ports, SQL/UI caches, LocalDB, and Windows `PATH`.
- **⬆️ System Updater (`Ctrl + Shift + U`):** Checks GitHub releases/commits and upgrades Darius DB in-place while keeping `data/` and `www/` untouched.
- **System Tray & Auto-Start:** Clicking **`×`** (or pressing **`Ctrl + Shift + H`**) hides Darius DB quietly into the **Windows System Tray** (when *Close window to tray* is enabled in Settings). Left-click the tray icon to restore; right-click for quick actions. Enable **Run on Windows boot** and **Auto-start enabled services** in **Settings** for a hands-free workflow.

---

## 4. Keyboard Shortcuts Reference

Press **`F1`**, **`Ctrl + /`**, or **`?`** (outside text inputs) anywhere inside Darius DB to open the searchable **Keyboard Shortcuts** popup.

### Navigation (Anywhere)
| Shortcut | Action |
|---|---|
| `Alt + 1` or `Ctrl + 1` | Open **Dashboard (Services)** |
| `Alt + 2` or `Ctrl + 2` | Open **Database Browser** |
| `Alt + 3` or `Ctrl + 3` | Open **Cloud Databases** |
| `Alt + 4` or `Ctrl + 4` | Open **Versions Manager** |
| `Alt + 5` or `Ctrl + 5` | Open **Config & Logs** |
| `Alt + 6` or `Ctrl + 6` | Open **Built-in Terminal** |
| `Alt + 7` or `Ctrl + 7` | Open **Settings** |
| `Alt + 8` or `Ctrl + 8` | Open **Guide & Shortcuts** |
| `F1`, `Ctrl + /`, or `?` | Toggle **Keyboard Shortcuts Popup** |
| `Esc` | Close active popup / modal |

### Global Service & System Controls (Anywhere)
| Shortcut | Action |
|---|---|
| `F8`, `Ctrl + Shift + S`, or `Alt + Shift + S` | **Start All** enabled services |
| `F9`, `Ctrl + Shift + X`, or `Alt + Shift + X` | **Stop All** running services *(use `F9` / `Alt + Shift + X` if Windows/Edge captures `Ctrl + Shift + X`)* |
| `Ctrl + Shift + R` | Open **Repair Service** dialog |
| `Ctrl + Shift + A` | Open **Repair All (Full System)** dialog |
| `Ctrl + Shift + U` | Open **System Version Updater** |
| `Ctrl + Shift + O` | Open **Portable Root Folder** in Explorer |
| `Ctrl + Shift + H` | **Hide** window to Windows System Tray |
| `Ctrl + \`` | Jump to **Built-in Terminal** & focus prompt |
| `F5` | Refresh active view (without reloading app) |

### Database Browser (Database Tab)
| Shortcut | Action |
|---|---|
| `Ctrl + Enter` | **Run SQL Query** in active tab |
| `Ctrl + Shift + Enter` | **Show All** rows (removes `LIMIT` / `TOP`) |
| `Alt + N` | Open **New SQL Query Tab** |
| `Alt + W` | **Close** current SQL query tab |
| `Alt + [` / `Alt + ]` | Switch to **Previous / Next SQL Query Tab** |
| `Ctrl + Shift + K` | **Clear** SQL editor |
| `Ctrl + F` | Focus **Row Filter** (SQL) or **Key Pattern** (Redis) |
| `Alt + D` | Create **New Database** (+ user) |
| `Alt + T` | Create **New Table** |
| `Alt + =` | **+ Create Row** in active table |
| `Alt + I` | **Import** `.sql` / `.csv` / `.bak` |
| `Alt + E` | **Export** database or selected tables |
| `Alt + B` | Create **Full `.zip` Backup** |
| `Alt + C` | **Copy Connection URI** |
| `Alt + R` | **Refresh** databases, tables, or Redis keys |
| `Alt + ←` / `Alt + →` | **Previous / Next** results page |

### Cloud, Versions, Config & Logs, Terminal & Settings
| Scope | Shortcut | Action |
|---|---|---|
| **Cloud** | `Alt + N` / `Alt + S` / `Alt + B` | Add Cloud DB / Local ↔ Cloud Sync / New 2:00 AM Schedule |
| **Versions** | `Alt + R` / `Alt + I` / `Alt + M` | Check Newest Versions / Import `.zip` / Manual URL |
| **Config & Logs** | `Ctrl + S` / `Ctrl + Shift + Enter` | Save Config File / Save & Restart Running Service |
| **Config & Logs** | `Alt + L` / `Alt + R` | Toggle Config ↔ Service Log / Reload |
| **Terminal** | `↑` / `↓` / `Ctrl + L` / `Ctrl + C` | History / Clear Output / Cancel Running Command |
| **Terminal** | `Alt + [` / `Alt + ]` | Cycle Terminal Mode (Shell / MariaDB / MySQL / psql / sqlcmd / Redis) |
| **Settings** | `Ctrl + S` | **Save Settings** (`settings.json`) |
| **Guide** | `Ctrl + F` | Search guide steps, topics & shortcuts |

---

## 5. Using the Launcher (`DariusDB-Launcher.exe`)

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

## 6. Developer CLI (`darius`) for Visual Studio, VS Code & Terminals

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

## 7. How to Uninstall Darius DB

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

## 8. How to Build from Source & Create Your Own Installer

### Prerequisites

1. Install **Go 1.24+**:
   ```powershell
   winget install GoLang.Go
   ```
2. Install **Wails CLI v2**:
   ```powershell
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```
3. *(Optional — only needed to compile `DariusDB-Setup-0.1.5.exe`)* Install **Inno Setup 6**:
   ```powershell
   winget install JRSoftware.InnoSetup
   ```

> **Note:** No C compiler (GCC/CGO) and no Node.js/npm build step are required!

### One-Command Build (GUI + Launcher + Uninstaller + Updater + CLI + Portable ZIP + Setup EXE)

Run the build script from the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File installer\build-all.ps1
```

This creates:
- `build\bin\DariusDB.exe` — main desktop app (~17 MB)
- `build\bin\DariusDB-Launcher.exe` (and `installer\DariusDB-Launcher.exe`) — native launcher (~2–3 MB)
- `build\bin\DariusDB-Uninstall.exe` (and `installer\DariusDB-Uninstall.exe`) — native 3-step uninstaller & repair wizard (~2–3 MB)
- `build\bin\DariusDB-Updater.exe` (and `installer\DariusDB-Updater.exe`) — standalone native System Version Updater (~2–3 MB)
- `build\bin\darius.exe` — Developer CLI
- `dist\DariusDB-0.1.5-portable-windows-amd64.zip` — portable archive
- `dist\DariusDB-Setup-0.1.5.exe` (and `installer\DariusDB-Setup-0.1.5.exe`) — standalone single-file native Windows installer `.exe`

### Manual Inno Setup Build

If you already built `build\bin\*.exe` and want to compile just the Inno Setup `.exe` installer:

```powershell
"C:\Program Files (x86)\Inno Setup 6\ISCC.exe" installer\DariusDB.iss
```

### Automated GitHub Releases & In-App Updates

Pushing a version tag (for example `git tag v0.1.5 && git push origin v0.1.5`) triggers [`.github/workflows/release.yml`](.github/workflows/release.yml), which automatically runs tests, builds all binaries (including `DariusDB-Setup-*.exe`, `DariusDB-Launcher.exe`, `DariusDB-Uninstall.exe`, `DariusDB-Updater.exe`, and the portable ZIP), and publishes a **GitHub Release** with all assets attached.

> **Important for In-App & Standalone Updates:** Ensure `dcquijano/Darius-DB-Lite-Version` is **Public** on GitHub so every user's **System Updater** and `DariusDB-Updater.exe` can fetch `version.json` and download release assets automatically. If you keep the repository **Private**, paste a GitHub Personal Access Token (`Contents: Read-only`) inside **System Updater → 🔐 GitHub Token** (or in `DariusDB-Updater.exe`).

---

## 9. Troubleshooting

- **"Port XXXX is already used by another program"** — Another local database server, container, or Windows service is already listening on that port. Open **Settings**, assign a different port (e.g. `3307` or `5433`), save, and start again.
- **PostgreSQL `postgresql.conf` not found yet** — PostgreSQL creates `postgresql.conf` inside `data/postgresql-<major>/` during its first start (`initdb`). Start PostgreSQL once, then edit it in **Config & Logs**.
- **PowerShell says running scripts is disabled** — Run the native `.exe` installer **`DariusDB-Setup-0.1.5.exe`** instead of `.ps1` scripts.
- **Windows SmartScreen warning on unsigned open-source `.exe`** — Because Darius DB is a community open-source build without a paid code-signing certificate, Windows SmartScreen may show *"Windows protected your PC"*. Click **More info → Run anyway** (or build directly from this source code).

---

## 10. Credits & Release Info

| Field | Details |
|---|---|
| **Project Name** | Darius DB (Lite Version 0.1.5) |
| **Version** | `Lite Version 0.1.5` |
| **Creator / Author** | **Darius Cumpio** |
| **Built By** | Darius Cumpio |
| **Created Date** | November 19, 2026 |
| **Year** | 2026 |
| **Repository** | [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version) |
| **License** | MIT License (`Copyright (c) 2026 Darius Cumpio`) |
