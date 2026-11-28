# Darius DB (Lite Version 0.1.6)

A fast, lightweight, portable open-source local + cloud web & database environment manager for Windows — built to make full-stack development simple, clean, and zero-bloat.

Manage **Apache Web Server (+ PHP 8.2 / 8.3 / 8.4)**, **MariaDB**, **MySQL**, **PostgreSQL**, **MS SQL Server**, **Redis**, and **Mailpit** from a single **~17 MB** native Windows app with **SSMS-style Interactive ER Database Diagrams** (`PNG` / `JPEG` / `WebP` / `SVG` / `HTML` / `Mermaid`), **Chrome-style tear-off floating SQL tabs with `Tab` autocomplete**, **Cloud Auto-Sync & 2:00 AM Auto-Backup**, local `www/` website hosting, a built-in multi-mode terminal, native launcher, clean uninstaller, and zero runtime bloat.

- **Repository:** [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version)
- 📥 **Official Installer & Portable Download (Google Drive):** [**Download DariusDB-Setup-0.1.6.exe**](https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing)
- 🖼️ **System Screenshots & Examples:** [**example/**](example/)
- 🛠️ **Installer Folder Step-by-Step Guide:** [**installer/README.md**](installer/README.md)
- 📖 **Full Step-by-Step Guide:** [**GUIDE.md**](GUIDE.md)

---

## System Preview ([`example/`](example/))

### Smart Windows Setup Wizard (Requirements Check, Engine Picker & Repair Mode)
| Step 1 — Welcome & MIT License | Step 2 — Location & PATH Options | Step 3 — Requirements & Engine Picker | Maintenance / Repair Mode |
|---|---|---|---|
| ![Setup Wizard Step 1](example/setup-wizard-step1-license.png) | ![Setup Wizard Step 2](example/setup-wizard-step2-location.png) | ![Setup Wizard Step 3](example/setup-wizard-step3-requirements.png) | ![Setup Wizard Repair Mode](example/setup-wizard-repair-mode.png) |

### Services Dashboard, Auto-Run Bar & Repair Diagnostics
![Darius DB Services Dashboard](example/dashboard-overview.jpg)

| Auto-Run Toggle & Per-Service Pills | Self-Healing Repair & Diagnostics |
|---|---|
| ![Auto-Run Bar](example/dashboard-auto-run.png) | ![Repair Modal](example/repair-diagnostics.jpg) |

### Database Manager, Visual SQL Join Map, Structure & Foreign Keys Inspector, ER Diagrams & Floating Query Tabs
![Darius DB Database Manager](example/database-manager.jpg)

| Visual SQL JOIN Builder & Interactive Join Map (`INNER` / `LEFT` / `RIGHT` / `FULL` / `CROSS`) | Right-Click DB Menu (`New Database Diagram` & `Visual Join Builder & Map`) |
|---|---|
| ![Visual SQL Join Builder and Interactive Join Map](example/database-visual-join-map.jpg) | ![Right-Click Database Menu with Visual Join Builder](example/database-join-context-menu.jpg) |

| Expandable Table Tree (`▸ Columns`, `Keys & Relations`, `Indexes`) | `🔑 Structure & Keys` Inspector (Columns, Foreign Keys & Properties) | Foreign Keys, Indexes & `CREATE TABLE` DDL |
|---|---|---|
| ![Expandable Table Tree](example/database-table-tree.png) | ![Structure and Keys Inspector](example/database-structure-keys.png) | ![Foreign Keys, Indexes and DDL](example/database-structure-ddl.png) |

| Right-Click DB Menu (`New Database Diagram`) | Interactive ER Diagram & Export Dropdown |
|---|---|
| ![Right-Click Database Context Menu](example/database-context-menu.jpg) | ![Interactive ER Database Diagram](example/database-diagram-export.jpg) |

| Tear-Off Floating Query Windows (`↩ Put Back` & `✕ Close All`) | IDE-Style SQL Tab Autocomplete |
|---|---|
| ![Floating SQL Query Tabs](example/database-floating-tabs.jpg) | ![SQL Autocomplete](example/database-sql-autocomplete.jpg) |

| Show Row Details | Edit Row | Create Row |
|---|---|---|
| ![Show Row](example/database-show-row.png) | ![Edit Row](example/database-edit-row.png) | ![Create Row](example/database-create-row.png) |

### Export, Full `.zip` Backup & Multi-Format Import / `.bak` Restore
| Export (`.sql` / `.csv`) | Full `.zip` Backup | Import (`.sql` / `.csv` / `.bak`) | Restore `.bak` (MS SQL) |
|---|---|---|---|
| ![Export Database](example/database-export.png) | ![Backup ZIP](example/database-backup-zip.jpg) | ![Import Database](example/database-import.jpg) | ![Restore .bak](example/database-restore-bak.jpg) |

### Cloud Databases, Manual Sync, Daily Auto-Backup & Trace Logs
| Cloud Databases & Daily Scheduler | Add Cloud DB (URI / ADO.NET Parse & TLS) | Daily Auto-Backup & Purge Schedule | Sync & Auto-Schedule Trace Logs |
|---|---|---|---|
| ![Cloud Databases](example/cloud-databases.jpg) | ![Add Cloud Database](example/cloud-add-database.jpg) | ![Daily Auto-Backup and Purge](example/cloud-daily-auto-backup.jpg) | ![Sync Trace Logs](example/cloud-sync-trace-logs.jpg) |

| New 2:00 AM Auto-Sync Schedule | Windows 2:00 AM Scheduled Task | 7-Day Snapshot Purge & `backups/` |
|---|---|---|
| ![2:00 AM Schedule](example/cloud-auto-sync-schedule.png) | ![Win 2 AM Task](example/cloud-win-2am-task.png) | ![Backups Folder](example/cloud-backups-folder.png) |

### Versions Manager, Config & Logs, and Built-in Terminal
| Portable Engine Versions | Config & Logs Editor | Built-in Multi-Mode Terminal |
|---|---|---|
| ![Versions Manager](example/versions-manager.png) | ![Config and Logs](example/config-logs.png) | ![Built-in Terminal](example/terminal.png) |

### Settings & GitHub System Version Updater
| Settings | GitHub System Updater |
|---|---|
| ![Settings](example/settings.jpg) | ![System Updater](example/system-updater.png) |

### In-App Visual Guide, Keyboard Shortcuts (`F1`) & CLI / Ports Reference
| Visual Step-by-Step Guide & Checklist (`Alt+8`) | Quick Keyboard Shortcuts Popup (`F1` / `Ctrl+/`) |
|---|---|
| ![Step-by-Step Guide](example/guide-step-by-step.jpg) | ![Keyboard Shortcuts Modal](example/shortcuts-modal.jpg) |

| Full Keyboard Shortcuts Reference | Default Ports, Mailpit (`:8025`) & `darius` CLI Reference |
|---|---|
| ![Full Keyboard Shortcuts](example/guide-keyboard-shortcuts.jpg) | ![CLI and Ports Reference](example/guide-cli-ports.jpg) |

---

## Features

- **Refreshed v0.1.6 UI Design** — Glass sidebar & view headers, gradient accents, softer layered shadows, larger rounded cards & modals, refined tables, inputs & focus rings across every view, a polished **Light theme** (also follows *System default*), and a **Comfortable / Compact** density option (**Settings → Appearance → UI Density**) for an IDE/SSMS-style dense layout.
- **100% Portable or Standard `.exe` Install** — Run directly out of any folder (`www/`, `backups/`, `bin/`, `data/`, `etc/`, `logs/`, `tmp/`, `settings.json`) or install using the standalone Windows Setup Wizard **`DariusDB-Setup-0.1.6.exe`** (with automatic upgrade protection and built-in **Repair Mode**).
- **Unified Apache + PHP (`8.2`/`8.3`/`8.4`) + MariaDB/MySQL (`:3306`) Web Stack** — Host local websites in `www/` at `http://localhost:8080/`, drag & drop project folders / `.zip` bundles / `.html` files (or click **`Deploy .zip`**), switch PHP versions inline on the Apache card, toggle MariaDB/MySQL cleanly on port `3306`, and copy hardened PHP PDO (`utf8mb4`) snippets in one click. Managed `etc/php/php.ini` is automatically **NTFS-hardlinked** into every `bin/php/<ver>/php.ini` (`PDO`, `pdo_mysql`, `mysqli`, `mysqlnd`, `gd`, `mbstring`, `openssl`, `curl`, `intl`, `fileinfo`, `zip`, `Zend OPcache`) so CLI tools like `php artisan serve` and `php artisan tinker` work out of the box without setting global `PHPRC`.
- **Built-in Mailpit (`:8025` Web Inbox / `:1025` SMTP)** — Instant zero-download local email capture for PHP/Laravel/app testing right on the Dashboard below Redis.
- **One-Click Start/Stop, Auto-Run Presets & Native System Tray** — Start All / Stop All (`F8` / `F9`), per-service Auto-Run pills & quick presets (*MySQL*, *MySQL + Apache*, *Use Running*), live PID & port-conflict detection, single-instance wake, close-to-tray, and right-click tray menu.
- **Multi-Format Import, Export, Full `.zip` Backup & MS SQL `.bak` Restore** — Import `.sql`, `.csv` (auto-creates columns), MS SQL `.bak` (automatic `RESTORE FILELISTONLY` logical-file relocation into `data/mssql/`), or PostgreSQL `.backup`; export *Entire DB*, *Current Table*, or *Selected Tables* as `.sql` (Structure/Data toggles), `.zip`, or `.csv`; and generate full `.zip` snapshots (`SQL dump + table CSVs + manifest.json`) from GUI or CLI (`darius restore` / `darius backup`).
- **Self-Healing Repair & Diagnostics (`🔧 Repair`, `🛠️ Repair All`, `DariusDB-Repair.exe`, `darius repair`)** — Diagnoses and heals missing portable directories, `www/index.html`, corrupted `settings.json` / Mailpit `messages.json`, engine configs (`etc/*`), stale crash locks (`postmaster.pid`, `tmp/run/*.pid`), blocked ports, UI caches, Windows `PATH`, and `MSSQLLocalDB` instances while keeping databases in `data/` safe.
- **Portable Engine Downloader & Live Upstream Version Checker** — Click **↻ Check Newest Versions** to fetch live upstream builds across **Apache, PHP, MariaDB, MySQL, PostgreSQL, MS SQL Server, Redis, and Mailpit**, update in one click, or import any custom offline `.zip`.
- **Built-in Multi-Tab SQL, ER Diagram & Redis Browser**
  - **SQL (MariaDB / MySQL / PostgreSQL / MS SQL Server, Local & Cloud):** browse databases & tables with an **expandable sidebar table tree** (`▸ Columns`, `Keys & Relations`, `Indexes`), toggle **`📊 Rows` | `🔑 Structure & Keys`** to inspect columns, PKs, outgoing & incoming Foreign Keys (clickable related entities), indexes, entity properties, and `CREATE TABLE` DDL, quick-create database + user/login with privileges, drop databases/tables, inline Row CRUD (Show, Edit, Create, Delete), **IDE-style `Tab` SQL autocomplete** (`CREATE`, `SHOW`, `SELECT`, functions, tables & columns), **Chrome-style tear-off floating query windows** (`↗` pop-out, `↩ Put Back`, drag-to-reorder & `✕ Close All`), and **VS / .env** + Connection URI copy.
  - **Visual SQL JOIN Builder & Interactive Join Map (`INNER` / `LEFT` / `RIGHT` / `FULL OUTER` / `CROSS`):** right-click any database, table, query tab, or FK row → **🔗 Visual Join Builder & Map** to switch join types via interactive clickable Venn regions or cards (including automatic MySQL/MariaDB `FULL OUTER JOIN` `UNION ALL` emulation), click entity columns (`A` / `B` / `C...`) to edit `ON` keys, chain extra tables (`+ Add Join`), drag or click the `▲ ▼` splitter, filter/paginate/export joined rows as CSV, or inspect side-by-side **`🔑 Structure & Keys`**.
  - **SSMS-Style Right-Click Menu & Interactive ER Database Diagram:** right-click any database or table → **Structure, Foreign Keys & Properties**, **Copy CREATE TABLE DDL**, or **New Database Diagram** for draggable table cards, solid FK + dashed inferred `*_id` relationship lines, pan/zoom/Auto-Arrange/Fit, Table filter picker, and **`📤 Export ▾`** to **PNG, JPEG, WebP, SVG, HTML, Mermaid (`.mmd`), or Clipboard PNG**.
  - **Redis:** switch logical DBs (`db0`–`db15`), scan & filter keys, inspect values (with automatic JSON formatting & TTL), delete keys, and run interactive Redis CLI commands.
- **Cloud Databases, Live Sync & Daily 2:00 AM Auto-Backup / Purge** — Connect **cPanel / Plesk / Hostinger (MySQL)**, **MonsterASP.NET / SmarterASP (MS SQL)**, **Supabase / Neon (PostgreSQL)**, **Aiven / TiDB / PlanetScale**, **AWS RDS / Azure SQL**, and **Redis Cloud** with URI/ADO.NET Quick Auto-Fill, SSL/TLS options, separated **Manual Sync**, **Daily Auto-Sync**, and **Daily 2:00 AM Auto-Backup** modals (with live Purge Date Guide & 7-day retention purge), plus **Sync & Auto-Schedule Trace Logs (`scheduler.log`)** and optional **Windows Task Scheduler** integration.
- **Config Editor & Live Log Viewer** — Edit `httpd.conf`, `php.ini`, `my.ini`, `postgresql.conf`, `mssql.conf`, `redis.conf`, and `mailpit.conf` inside the app (or Notepad) with *Save & Restart*, plus auto-refreshing logs.
- **Native Setup, Launcher, Repair, Updater & Uninstall Wizards**
  - `DariusDB-Setup-0.1.6.exe` — self-contained 4-step Windows Setup Wizard with **live system requirements detection** (`WebView2`, `VC++ 2015–2022 x64`, `SQL Server LocalDB`), optional database/web engine pre-installer, automatic offline bundle detection (`offline\`), in-place upgrade detection (e.g. `0.1.5 → 0.1.6`), and 1-click **Repair Mode**; pair with `installer\build-offline.ps1` and `installer\DariusDB.iss` for air-gapped all-in-one offline bundles.
  - `DariusDB-Launcher.exe` — verifies WebView2, sets `DARIUSDB_HOME`, supports `--minimized`, `--terminal`, `--start-all`, `--stop-all`, `--version`.
  - `DariusDB-Repair.exe` — standalone Win32 full-system Repair All wizard.
  - `DariusDB-Updater.exe` — native Win32 System Version Updater GUI & CLI (`--check`, `--silent`, `--token=PAT`) that detects installed version, checks GitHub (`version.json` + Releases + Commits) and local installers, and upgrades in-place while keeping `data/` and `www/` untouched.
  - `DariusDB-Uninstall.exe` — native 3-step Win32 wizard with **Uninstall** and **Repair** modes, live progress bar, scrolling log, and safe database preservation (`data/`, `www/`, `etc/`).
- **Developer CLI (`darius.exe`) for Visual Studio, VS Code & Terminals** — Automatically added to your Windows `PATH` (`C:\DariusDB`). Run `darius status`, `darius start all`, `darius repair`, `darius restore`, `darius backup`, `darius env mssql MyApp`, `darius sql mssql "SELECT @@VERSION"`, `darius createdb`, `darius versions`, `darius update`, and `darius install` from any IDE or terminal.
- **Pre-configured Multi-Mode Terminal** — Switch between Windows Shell, MariaDB, MySQL, PostgreSQL (`psql`), MS SQL (`sqlcmd`), and Redis CLI inside the app.
- **Built-in Step-by-Step Guide & Keyboard Shortcuts (`Alt+8` / `F1`)** — Interactive quick-start checklist, collapsible 9-step visual manual with embedded screenshots, searchable keyboard shortcuts popup (`F1` / `Ctrl+/` / `?`), global navigation (`Alt+1`–`Alt+8`), service controls (`F8` / `F9`, `Alt+Shift+S` / `X`, `Ctrl+Shift+S` / `X` / `R` / `A` / `U`), SQL tab/query hotkeys, and one-click CLI copy reference.

---

## Quick Start

### Option 1: Install on Windows (Recommended)
1. Download **`DariusDB-Setup-0.1.6.exe`** from [**GitHub Releases**](https://github.com/dcquijano/Darius-DB-Lite-Version/releases) or the [**Official Google Drive Installer Folder**](https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing) (or run local build output `installer\DariusDB-Setup-0.1.6.exe` / `dist\DariusDB-Setup-0.1.6.exe`).
2. Double-click **`DariusDB-Setup-0.1.6.exe`**, keep **`C:\DariusDB`** (default), and complete the Setup Wizard *(existing installs offer automatic in-place upgrade or Repair Mode while keeping `data/`, `www/`, `backups/`, and `etc/` safe)*.
3. Launch **Darius DB (Lite Version 0.1.6)** from the Desktop or Start Menu.
4. Open **Versions** (`Alt+4`) to download **Apache, PHP (8.2/8.3/8.4), MariaDB, MySQL, PostgreSQL, Redis, or Mailpit** (or start built-in **Apache**, **Mailpit**, and **MS SQL Server** right away), then click **Start All** (`F8`).
5. Open **Database** (`Alt+2`) to browse tables, run multi-tab SQL with **`Tab`** autocomplete, pop out floating query tabs, or **right-click any database → New Database Diagram**!

### Option 2: Portable (No Install)
1. Download **`DariusDB-0.1.6-portable-windows-amd64.zip`** from [**GitHub Releases**](https://github.com/dcquijano/Darius-DB-Lite-Version/releases) or [**Google Drive**](https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing) (or `dist\DariusDB-0.1.6-portable-windows-amd64.zip`) and extract into any folder, e.g. `C:\DariusDB\`.
2. Double-click **`DariusDB-Launcher.exe`** (or **`DariusDB.exe`**) — all portable folders (`www/`, `backups/`, `bin/`, `data/`, `etc/`, `logs/`, `tmp/`) and `settings.json` are initialized cleanly beside the app.

---

## Portable Folder Structure

```text
C:\DariusDB\
├── DariusDB.exe
├── DariusDB-Launcher.exe
├── DariusDB-Repair.exe
├── DariusDB-Uninstall.exe
├── DariusDB-Updater.exe
├── darius.exe
├── settings.json
├── GUIDE.md
├── README.md
├── LICENSE
├── www/
│   ├── index.html
│   └── my-website/
├── backups/
│   └── <schedule-name>/
├── bin/
│   ├── apache/apache-2.4.66/
│   ├── php/php-8.4.4/          # includes php.ini hardlinked to etc/php/php.ini
│   ├── mariadb/mariadb-13.0.2/
│   ├── mysql/mysql-9.7.2/
│   ├── postgresql/postgresql-18.7/
│   ├── mssql/mssql-17.0.1000.7/
│   └── redis/redis-8.10.3/
├── data/
│   ├── mariadb-13.0/
│   ├── mysql-9.7/
│   ├── postgresql-18/
│   ├── mssql/
│   ├── redis/
│   └── mailpit/
├── etc/
│   ├── apache/httpd.conf
│   ├── php/php.ini
│   ├── mariadb/my.ini
│   ├── mysql/my.ini
│   ├── mssql/mssql.conf
│   ├── redis/redis.conf
│   └── mailpit/mailpit.conf
├── logs/
│   ├── apache.log
│   ├── mariadb.log
│   ├── mysql.log
│   ├── postgresql.log
│   ├── mssql.log
│   ├── redis.log
│   ├── mailpit.log
│   └── scheduler.log
└── tmp/
```

---

## Supported Database & Web Engines

| Engine | Default Port | Included Versions |
|---|---|---|
| **Apache Web Server** | `8080` (`www/`) | `2.4.66` (Apache 2.4 Win64 VS17/VS18) + `apache-lite-2.4` (Built-in instant web server) |
| **PHP (Integrated + CLI)** | `9000` (Inside Apache) | `8.4.x`, `8.3.x`, `8.2.x` (Thread Safe + OPcache, PDO, mysqli, Mailpit SMTP) |
| **MariaDB** | `3306` | `13.0.2` (Latest Stable), `12.3.4` (LTS), `11.8.10` (LTS), `11.4.14` (LTS), `10.11.20` (LTS) |
| **MySQL** | `3306` | `9.7.2` (Latest), `8.4.11` (LTS), `8.0.46` (LTS) |
| **PostgreSQL** | `5432` | `18.7` (Latest), `17.12`, `16.16`, `15.20`, `14.25` |
| **MS SQL Server** | `1433` / LocalDB | `17.0.1000.7` / `17.0.4025.3` (**SQL Server 2025 — 17.x**), `16.0.1000.6` (SQL Server 2022 — 16.x) |
| **Redis** | `6379` | `8.10.3` (Latest), `8.8.3`, `7.4.11` (LTS), `6.2.24`, `5.0.14.1` |
| **Mailpit** | `8025` UI / `1025` SMTP | `mailpit-lite-1.22` (Built-in zero-download) + upstream `mailpit-windows-amd64` |

---

## Building from Source & Packaging the Installer

### Prerequisites
- Windows 10/11 (x64) with Microsoft Edge WebView2 (pre-installed on Windows 10/11)
- [Go 1.24+](https://go.dev/dl/)
- [Wails CLI v2](https://wails.io/): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- *(Optional, for `DariusDB-Setup-0.1.6.exe`)* [Inno Setup 6](https://jrsoftware.org/isinfo.php): `winget install JRSoftware.InnoSetup`

No C compiler (CGO) and no Node.js build step required!

### Build Everything (App + Launcher + Uninstaller + Portable ZIP + Setup EXE)

```powershell
powershell -ExecutionPolicy Bypass -File installer\build-all.ps1
```

Or build only the main GUI app:

```powershell
go test ./...
wails build -clean -ldflags "-s -w"
```

---

## Credits & Information

| | |
|---|---|
| **Application** | **Darius DB (Lite Version 0.1.6)** |
| **Creator / Author** | **Darius Cumpio** |
| **Built By** | Darius Cumpio |
| **Created Date** | November 19, 2026 |
| **Year** | 2026 |
| **Version** | `Lite Version 0.1.6` |
| **Repository** | [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version) |
| **Installer Download** | [https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing](https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing) |
| **License** | [MIT License](LICENSE) — Copyright © 2026 Darius Cumpio |
