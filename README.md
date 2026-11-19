# Darius DB (Lite Version 0.1.1)

A fast, lightweight, portable open-source database environment manager for Windows — built to make local database development simple, clean, and zero-bloat.

Manage **MariaDB**, **MySQL**, **PostgreSQL**, **MS SQL Server**, and **Redis** from a single ~13 MB native Windows app with a system tray icon, built-in multi-mode terminal, native launcher, clean uninstaller, and zero runtime bloat.

- **Repository:** [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version)
- 📖 **Full Step-by-Step Guide:** [**GUIDE.md**](GUIDE.md)

---

## Features

- **100% Portable or Standard `.exe` Install** — Run directly out of any folder (`bin/`, `data/`, `etc/`, `logs/`, `settings.json`) or install using the standalone Windows installer **`DariusDB-Setup-0.1.1.exe`**.
- **One-Click Start/Stop & Native System Tray** — Start All / Stop All, per-engine controls, live PID & port-conflict detection, single-instance wake, close-to-tray, and right-click tray menu.
- **Portable Engine Downloader & Version Switcher** — Download official portable builds (MariaDB, MySQL, PostgreSQL, Redis) or import any local `.zip`, keep multiple versions side-by-side, and switch active versions in one click without touching `data/`.
- **Built-in Lightweight Database Browser**
  - **SQL (MariaDB / MySQL / PostgreSQL / MS SQL Server):** browse databases & tables, quick-create database + user/login with privileges, drop databases, run SQL / T-SQL (`Ctrl+Enter`), copy connection strings.
  - **Redis:** switch logical DBs (`db0`–`db15`), scan & filter keys, inspect values (with automatic JSON formatting & TTL), delete keys, and run interactive Redis CLI commands.
- **Config Editor & Live Log Viewer** — Edit `my.ini`, `postgresql.conf`, and `redis.conf` inside the app (or Notepad) with *Save & Restart*, plus auto-refreshing logs.
- **Native Launcher & Clean Uninstaller**
  - `DariusDB-Launcher.exe` — verifies WebView2, sets `DARIUSDB_HOME`, supports `--minimized`, `--terminal`, `--start-all`, `--stop-all`, `--version`.
  - `DariusDB-Uninstall.exe` — registers in Windows *Installed Apps*, gracefully stops services, asks whether to keep your `data/` databases safe, removes shortcuts & registry keys, and self-cleans.
- **Developer CLI (`darius.exe`) for Visual Studio, VS Code & Terminals** — Automatically added to your Windows `PATH` (`C:\DariusDB`). Run `darius status`, `darius start all`, `darius env mssql MyApp`, `darius sql mssql "SELECT @@VERSION"`, `darius createdb`, `darius versions`, and `darius install` from any IDE or terminal.
- **Pre-configured Terminal** — Opens a prompt with active `mariadb`/`mysql`, `psql`, `sqlcmd`, and `redis-cli` binaries already on `PATH` and port env vars set.

---

## Quick Start

### Option 1: Install on Windows (Recommended)
1. Double-click **`installer\DariusDB-Setup-0.1.1.exe`** (or **`dist\DariusDB-Setup-0.1.1.exe`**).
2. Choose **`C:\DariusDB`** (or pick any custom folder) and select whether to start on Windows boot.
3. Launch **Darius DB (Lite Version 0.1.1)** from the Desktop or Start Menu.
4. Open **Versions** to download MariaDB, MySQL, PostgreSQL, or Redis (or start **MS SQL Server** immediately), then click **Start All**.

### Option 2: Portable (No Install)
1. Extract `DariusDB-0.1.1-portable-windows-amd64.zip` (or copy `DariusDB.exe`, `DariusDB-Launcher.exe`, and `darius.exe`) into any folder, e.g. `C:\DariusDB\`.
2. Double-click `DariusDB-Launcher.exe` (or `DariusDB.exe`).

---

## Portable Folder Structure

```text
C:\DariusDB\
├── DariusDB.exe
├── DariusDB-Launcher.exe
├── DariusDB-Uninstall.exe
├── darius.exe
├── settings.json
├── GUIDE.md
├── README.md
├── LICENSE
├── bin/
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
│   └── redis/
├── etc/
│   ├── mariadb/my.ini
│   ├── mysql/my.ini
│   ├── mssql/mssql.conf
│   └── redis/redis.conf
├── logs/
│   ├── mariadb.log
│   ├── mysql.log
│   ├── postgresql.log
│   ├── mssql.log
│   └── redis.log
└── tmp/
```

---

## Supported Database Engines & Catalog Versions

| Engine | Default Port | Included Versions |
|---|---|---|
| **MariaDB** | `3306` | `13.0.2` (Latest Stable), `12.3.4` (LTS), `11.8.10` (LTS), `11.4.14` (LTS), `10.11.20` (LTS) |
| **MySQL** | `3307` | `9.7.2` (Latest), `8.4.11` (LTS), `8.0.46` (LTS) |
| **PostgreSQL** | `5432` | `18.7` (Latest), `17.12`, `16.16`, `15.20`, `14.25` |
| **MS SQL Server** | `1433` / LocalDB | `17.0.1000.7` / `17.0.4025.3` (**SQL Server 2025 — 17.x**), `16.0.1000.6` (SQL Server 2022 — 16.x) |
| **Redis** | `6379` | `8.10.3` (Latest), `8.8.3`, `7.4.11` (LTS), `6.2.24`, `5.0.14.1` |

---

## Building from Source & Packaging the Installer

### Prerequisites
- Windows 10/11 (x64) with Microsoft Edge WebView2 (pre-installed on Windows 10/11)
- [Go 1.24+](https://go.dev/dl/)
- [Wails CLI v2](https://wails.io/): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- *(Optional, for `DariusDB-Setup-0.1.1.exe`)* [Inno Setup 6](https://jrsoftware.org/isinfo.php): `winget install JRSoftware.InnoSetup`

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
| **Application** | **Darius DB (Lite Version 0.1.1)** |
| **Creator / Author** | **Darius Cumpio** |
| **Built By** | Darius Cumpio |
| **Created Date** | November 19, 2026 |
| **Year** | 2026 |
| **Version** | `Lite Version 0.1.1` |
| **Repository** | [https://github.com/dcquijano/Darius-DB-Lite-Version](https://github.com/dcquijano/Darius-DB-Lite-Version) |
| **License** | [MIT License](LICENSE) — Copyright © 2026 Darius Cumpio |
