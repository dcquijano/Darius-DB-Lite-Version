# Darius DB (Lite Version) — Project Agent Guide

Windows-native local + cloud web & database stack manager built with Go, Wails v2, and a vanilla HTML/CSS/JS frontend.

## Architecture Map

- **`main.go` / `app.go`**: Wails v2 desktop shell, single-instance lock (`AcquireSingleInstanceWithWait`), system tray, and IPC bindings (`window.go.main.App`).
- **`internal/core/`**: Engine managers (`apache.go`, `engines_impl.go`, `mssql.go`, `php_mailpit.go`), SQL execution & pooling (`db.go`), backup/restore/export (`backup.go`), Cloud sync & URI parsing (`cloud.go`), system repair (`repair.go`), updater & scheduler (`updater.go`, `scheduler.go`), Win32 process/pipe/registry helpers (`sys_windows.go`), CLI (`cli.go`).
- **`internal/tray/` / `internal/winui/`**: Windows tray menu and native Win32 dialogs (`DariusDB-Setup`, `Launcher`, `Updater`, `Repair`, `Uninstall`).
- **`cmd/`**: Standalone executables (`cli`, `installer`, `launcher`, `repair`, `uninstaller`, `updater`).
- **`frontend/dist/`**: Embedded SPA assets (`//go:embed all:frontend/dist` in `main.go`) — `index.html`, `app.js`, `style.css`, `shortcuts-guide.js`.
- **`installer/`**: Release build (`build-all.ps1`), Inno Setup script (`DariusDB.iss`), and PowerShell install/update/repair/uninstall scripts.

## Build & Verification Commands (Windows PowerShell)

Separate shell statements with `;` (never `&&` in Windows PowerShell 5.1):

- **JS syntax check** (after editing `frontend/dist/*.js`):
  `node --check frontend\dist\app.js`
- **Go unit tests**:
  `go test -p 1 -tags "desktop,production" -vet=off -count=1 ./internal/core ./internal/tray ./internal/winui`
- **Full release build** (runs Go tests, builds all `.exe`s, ZIP, and Setup installer):
  `powershell -ExecutionPolicy Bypass -File installer\build-all.ps1`

## Critical Project Invariants

1. **Windows Process & Pipe Safety (`engine.go`, `manager.go`, `sys_windows.go`)**
   - Never run daemon-spawning helpers (e.g., `pg_ctl.exe start/stop`, `initdb.exe`) via `exec.Cmd.CombinedOutput()` — spawned child daemons inherit OS pipe handles and deadlock `cmd.Wait()`. Always use `runCmdCapture` (temp-file capture + context tree kill) and `hideWindow(cmd)`.
   - In `Manager.Stop`, cancel `cancelStart` and terminate `startingCmd` / processes in `inst.Dir` *before* acquiring `op.Lock()`.

2. **Non-Destructive Repair (`repair.go`, `engines_impl.go`)**
   - Never force `ResetConfigs = true` merely because `opts.Target == id`. Only reset config files when `opts.ResetConfigs` is explicitly requested.
   - `postgres.Prepare` must run `ensurePostgresConfFiles` even when `PG_VERSION` already exists so missing `postgresql.conf`, `pg_hba.conf`, or `pg_ident.conf` self-heal cleanly.

3. **SQL & Cloud Connections (`db.go`, `cloud.go`, `mssql.go`, `backup.go`)**
   - Keep remote SQL dial timeout retries (`openAndPingSQL`) and `TestCloudConnectionWithProgress` TCP retries so Dual-WAN routers recover cleanly without blocking on stalled routes.
   - `ParseCloudURI` splits credentials on the **last `@`** and uses `url.PathUnescape` so passwords containing `@`, `#`, `+`, or `%XX` parse accurately, and normalizes cPanel web ports (`2082/2083/2086/2087`) on MySQL/MariaDB URIs to port `3306` + `PanelURL`.
   - Always reject cPanel/MySQL ports on MS SQL profiles (`go-sqlcmd` TDS `invalid packet size` guard), auto-create missing destination DBs on MS SQL import (`IF DB_ID(...) IS NULL CREATE DATABASE ...`), and pass `--plugin-dir` + TLS flags via `appendMySQLClientConnArgs`.

4. **Wails WebView2 Frontend (`frontend/dist/`)**
   - Wails IPC only binds to the primary WebView; never rely on `window.open()` popups for app features. Keep detachable SQL query tabs inside `#sqlFloatingWindows` sharing `renderSqlTableInto`, `attachSqlAutocomplete`, and `runSqlQuery`.
   - Never duplicate large UI components (table renderers, autocomplete menus, modal forms) — extract shared helpers.
