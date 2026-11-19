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
1. Copy `DariusDB.exe` (and `DariusDB-Launcher.exe`) into any folder, e.g. `C:\DariusDB\`.
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
├── bin/
│   ├── mariadb/mariadb-11.4.14/
│   ├── mysql/mysql-8.4.11/
│   ├── postgresql/postgresql-17.12/
│   └── redis/redis-8.10.3/
├── data/
│   ├── mariadb-11.4/
│   ├── mysql-8.4/
│   ├── postgresql-17/
│   └── redis/
├── etc/
│   ├── mariadb/my.ini
│   ├── mysql/my.ini
│   └── redis/redis.conf
├── logs/
└── tmp/
```

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
