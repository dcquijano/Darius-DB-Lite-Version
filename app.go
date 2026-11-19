package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"dariusdb/internal/core"
	"dariusdb/internal/tray"
)

const (
	AppName    = "Darius DB (Lite Version 0.1.2)"
	AppVersion = "0.1.2"
	AppAuthor  = "Darius Cumpio"
	AppBuiltBy = "Darius Cumpio"
	AppCreated = "November 19, 2026"
	AppYear    = 2026
	AppLicense = "MIT"
	AppRepo    = "https://github.com/dcquijano/Darius-DB-Lite-Version"
)

type AppInfo struct {
	Name    string     `json:"name"`
	Version string     `json:"version"`
	Author  string     `json:"author"`
	BuiltBy string     `json:"builtBy"`
	Created string     `json:"created"`
	Year    int        `json:"year"`
	License string     `json:"license"`
	Repo    string     `json:"repo"`
	Root    string     `json:"root"`
	Paths   core.Paths `json:"paths"`
}

type ConfigFileResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
	Hint    string `json:"hint,omitempty"`
}

type App struct {
	ctx      context.Context
	paths    *core.Paths
	settings *core.SettingsStore
	mgr      *core.Manager
	dl       *core.Downloader
	term     *core.Terminal
	tr       *tray.Tray

	mu       sync.Mutex
	quitting bool
}

func NewApp(paths *core.Paths, st *core.SettingsStore) *App {
	a := &App{
		paths:    paths,
		settings: st,
	}
	a.mgr = core.NewManager(paths, st, a.emitStatus)
	a.dl = core.NewDownloader(paths, a.emitProgress, func(engine, name string) {
		a.mgr.OnInstalled(engine, name)
	})
	a.term = core.NewTerminal(paths, st, a.mgr, nil)
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	tr, err := tray.Start(tray.Config{
		Tag:       core.InstanceTag(a.paths.Root),
		Tooltip:   a.trayTooltip(),
		IconPNG:   tray.DefaultIconPNG,
		OnShow:    a.ShowWindow,
		BuildMenu: a.buildTrayMenu,
	})
	if err == nil {
		a.tr = tr
	}

	if a.settings.Get().AutoStartServices {
		go func() { _ = a.mgr.StartAll() }()
	}
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	a.mu.Lock()
	q := a.quitting
	a.mu.Unlock()
	if !q && a.settings.Get().CloseToTray && a.tr != nil {
		runtime.WindowHide(ctx)
		return true
	}
	return false
}

func (a *App) shutdown(ctx context.Context) {
	if a.settings.Get().StopOnExit {
		_ = a.mgr.StopAll()
	}
	if a.tr != nil {
		a.tr.Close()
	}
}

func (a *App) emitStatus() {
	st := a.mgr.Status()
	if a.tr != nil {
		a.tr.SetTooltip(a.trayTooltipFrom(st))
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status", st)
	}
}

func (a *App) emitProgress(p core.Progress) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "download", p)
	}
}

func (a *App) trayTooltip() string {
	return a.trayTooltipFrom(a.mgr.Status())
}

func (a *App) trayTooltipFrom(st []core.ServiceStatus) string {
	var running []string
	for _, s := range st {
		if s.State == core.StateRunning {
			running = append(running, s.Name)
		}
	}
	if len(running) == 0 {
		return "Darius DB (Lite Version 0.1.2) — All services stopped"
	}
	return fmt.Sprintf("Darius DB (Lite Version 0.1.2) — Running: %s", strings.Join(running, ", "))
}

func (a *App) buildTrayMenu() []tray.MenuItem {
	st := a.mgr.Status()
	items := []tray.MenuItem{
		{Label: "Open Darius DB (Lite Version 0.1.2)", Default: true, Action: a.ShowWindow},
		{Separator: true},
		{Label: "Start All", Action: func() { _ = a.mgr.StartAll() }},
		{Label: "Stop All", Action: func() { _ = a.mgr.StopAll() }},
		{Separator: true},
	}
	for _, s := range st {
		s := s
		switch s.State {
		case core.StateNotInstalled:
			items = append(items, tray.MenuItem{
				Label:    fmt.Sprintf("○ %s (not installed)", s.Name),
				Disabled: true,
			})
		case core.StateRunning:
			items = append(items, tray.MenuItem{
				Label:  fmt.Sprintf("● %s (:%d) — Stop", s.Name, s.Port),
				Action: func() { _ = a.mgr.Stop(s.Engine) },
			})
		case core.StateStarting, core.StateStopping:
			items = append(items, tray.MenuItem{
				Label:    fmt.Sprintf("◐ %s — %s...", s.Name, s.State),
				Disabled: true,
			})
		default:
			items = append(items, tray.MenuItem{
				Label:  fmt.Sprintf("○ %s (:%d) — Start", s.Name, s.Port),
				Action: func() { _ = a.mgr.Start(s.Engine) },
			})
		}
	}
	items = append(items,
		tray.MenuItem{Separator: true},
		tray.MenuItem{Label: "Open Built-in Terminal", Action: func() {
			a.ShowWindow()
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "navigate", "terminal")
			}
		}},
		tray.MenuItem{Label: "Open Data Folder", Action: func() { _ = a.OpenFolder("data") }},
		tray.MenuItem{Separator: true},
		tray.MenuItem{Label: "Quit Darius DB (Lite Version 0.1.2)", Action: a.QuitApp},
	)
	return items
}

// ---------------------------------------------------------------- Window & Info

func (a *App) Info() AppInfo {
	return AppInfo{
		Name:    AppName,
		Version: AppVersion,
		Author:  AppAuthor,
		BuiltBy: AppBuiltBy,
		Created: AppCreated,
		Year:    AppYear,
		License: AppLicense,
		Repo:    AppRepo,
		Root:    a.paths.Root,
		Paths:   *a.paths,
	}
}

func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

func (a *App) HideToTray() {
	if a.ctx != nil && a.tr != nil {
		runtime.WindowHide(a.ctx)
	}
}

func (a *App) QuitApp() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

// ---------------------------------------------------------------- Services

func (a *App) Status() []core.ServiceStatus       { return a.mgr.Status() }
func (a *App) Start(engine string) error          { return a.mgr.Start(engine) }
func (a *App) Stop(engine string) error           { return a.mgr.Stop(engine) }
func (a *App) Restart(engine string) error        { return a.mgr.Restart(engine) }
func (a *App) StartAll() error                    { return a.mgr.StartAll() }
func (a *App) StopAll() error                     { return a.mgr.StopAll() }
func (a *App) SetActive(engine, name string) error { return a.mgr.SetActive(engine, name) }
func (a *App) Uninstall(engine, name string) error { return a.mgr.Uninstall(engine, name) }

// ---------------------------------------------------------------- Catalog & Downloads

func (a *App) Catalog() core.Catalog { return core.LoadCatalog(a.paths) }

func (a *App) RefreshCatalog() (core.Catalog, error) {
	return core.RefreshCatalog(a.paths, a.settings.Get().CatalogURL)
}

func (a *App) AddCatalogEntry(engine, version, url, note string) (core.Catalog, error) {
	return core.AddCustomCatalogEntry(a.paths, engine, version, url, note)
}

func (a *App) Install(engine, version, url string) error {
	return a.dl.Install(engine, version, url)
}

func (a *App) InstallFromZip(engine string) (string, error) {
	if a.ctx == nil {
		return "", errors.New("window not ready")
	}
	eng, err := core.GetEngine(engine)
	if err != nil {
		return "", err
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select " + eng.Name() + " portable ZIP archive",
		Filters: []runtime.FileFilter{
			{DisplayName: "ZIP Archives (*.zip)", Pattern: "*.zip"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	return a.dl.InstallZip(engine, path)
}

func (a *App) CancelInstall(engine, version string) { a.dl.Cancel(engine, version) }
func (a *App) ActiveDownloads() []string            { return a.dl.Active() }

// ---------------------------------------------------------------- Config & Logs

func (a *App) configPathFor(engine string) (string, error) {
	eng, err := core.GetEngine(engine)
	if err != nil {
		return "", err
	}
	if err := core.EnsureConfig(a.paths, engine); err != nil {
		return "", err
	}
	es := a.settings.Engine(engine)
	inst, ok := core.FindInstallation(a.paths, engine, es.Version)
	if !ok {
		// For MariaDB / MySQL / Redis, config file doesn't depend on installation
		if engine != "postgresql" {
			return eng.ConfigFile(a.paths, core.Installation{}), nil
		}
		return "", fmt.Errorf("%s is not installed yet", eng.Name())
	}
	return eng.ConfigFile(a.paths, inst), nil
}

func (a *App) ReadConfig(engine string) (*ConfigFileResult, error) {
	path, err := a.configPathFor(engine)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &ConfigFileResult{
			Path:   path,
			Exists: false,
			Hint:   "PostgreSQL creates postgresql.conf inside its data folder on first start. Start PostgreSQL once to edit it.",
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return &ConfigFileResult{
		Path:    path,
		Content: strings.ReplaceAll(string(b), "\r\n", "\n"),
		Exists:  true,
	}, nil
}

func (a *App) SaveConfig(engine, content string) error {
	path, err := a.configPathFor(engine)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\n", "\r\n")
	return os.WriteFile(path, []byte(normalized), 0o644)
}

func (a *App) OpenConfigExternal(engine string) error {
	path, err := a.configPathFor(engine)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("config file does not exist yet: %s", path)
	}
	return core.OpenInNotepad(path)
}

func (a *App) ReadLog(engine string) string  { return a.mgr.ReadLog(engine, 64*1024) }
func (a *App) ClearLog(engine string) error  { return a.mgr.ClearLog(engine) }

// ---------------------------------------------------------------- Built-in Terminal

func (a *App) TerminalState() core.TerminalState { return a.term.State() }
func (a *App) TerminalBanner() string            { return a.term.Banner() }
func (a *App) TerminalSetMode(mode string) (*core.TerminalExecResult, error) {
	return a.term.SetMode(mode)
}
func (a *App) TerminalExec(line string) (*core.TerminalExecResult, error) {
	return a.term.Exec(line)
}
func (a *App) TerminalCancel() { a.term.Cancel() }

// ---------------------------------------------------------------- Folders, Terminal & Helpers

func (a *App) OpenFolder(kind string) error {
	switch kind {
	case "root":
		return core.OpenFolder(a.paths.Root)
	case "www":
		return core.OpenFolder(a.paths.Www)
	case "data":
		return core.OpenFolder(a.paths.Data)
	case "bin":
		return core.OpenFolder(a.paths.Bin)
	case "etc":
		return core.OpenFolder(a.paths.Etc)
	case "logs":
		return core.OpenFolder(a.paths.Logs)
	default:
		if strings.HasPrefix(kind, "data:") {
			id := strings.TrimPrefix(kind, "data:")
			eng, err := core.GetEngine(id)
			if err != nil {
				return err
			}
			if inst, ok := core.FindInstallation(a.paths, id, a.settings.Engine(id).Version); ok {
				return core.OpenFolder(eng.DataDir(a.paths, inst))
			}
			return core.OpenFolder(a.paths.Data)
		}
		return fmt.Errorf("unknown folder %q", kind)
	}
}

func (a *App) OpenTerminal() error {
	s := a.settings.Get()
	env := map[string]string{
		"DARIUSDB_HOME": a.paths.Root,
	}
	banner := []string{
		"Darius DB Terminal (portable database tools are in PATH)",
	}

	if pg := s.Engines["postgresql"]; pg != nil {
		env["PGHOST"] = "127.0.0.1"
		env["PGPORT"] = strconv.Itoa(pg.Port)
		if pg.User != "" {
			env["PGUSER"] = pg.User
		}
	}
	if m := s.Engines["mariadb"]; m != nil && m.Version != "" {
		env["MYSQL_HOST"] = "127.0.0.1"
		env["MYSQL_TCP_PORT"] = strconv.Itoa(m.Port)
	} else if m := s.Engines["mysql"]; m != nil && m.Version != "" {
		env["MYSQL_HOST"] = "127.0.0.1"
		env["MYSQL_TCP_PORT"] = strconv.Itoa(m.Port)
	}

	for _, st := range a.mgr.Status() {
		if st.Active == "" {
			continue
		}
		cli := ""
		switch st.Engine {
		case "apache":
			cli = fmt.Sprintf("http://localhost:%d/  (www/)", st.Port)
		case "mariadb":
			cli = fmt.Sprintf("mariadb -u %s -P %d", fallback(s.Engines["mariadb"].User, "root"), st.Port)
		case "mysql":
			cli = fmt.Sprintf("mysql -u %s -P %d", fallback(s.Engines["mysql"].User, "root"), st.Port)
		case "postgresql":
			cli = fmt.Sprintf("psql -U %s -p %d", fallback(s.Engines["postgresql"].User, "postgres"), st.Port)
		case "mssql":
			cli = "sqlcmd -S (localdb)\\MSSQLLocalDB -E"
		case "redis":
			cli = fmt.Sprintf("redis-cli -p %d", st.Port)
		}
		banner = append(banner, fmt.Sprintf("  %-11s %-18s port %-5d  (%s)", st.Name, st.Active, st.Port, cli))
	}
	if len(banner) == 1 {
		banner = append(banner, "  No engines installed yet. Install one from the Versions tab.")
	}
	return core.OpenTerminal(a.paths.Root, a.mgr.ClientPaths(), env, banner)
}

func (a *App) ConnectionString(engine, dbName string) string {
	es := a.settings.Engine(engine)
	switch engine {
	case "apache":
		return fmt.Sprintf("http://localhost:%d/", es.Port)
	case "mariadb", "mysql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		return fmt.Sprintf("mysql://%s@127.0.0.1:%d/%s", auth, es.Port, dbName)
	case "postgresql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		if dbName == "" {
			dbName = "postgres"
		}
		return fmt.Sprintf("postgresql://%s@127.0.0.1:%d/%s", auth, es.Port, dbName)
	case "mssql":
		if dbName == "" {
			dbName = "master"
		}
		if es.User != "" && es.Password != "" {
			return fmt.Sprintf("Server=127.0.0.1,%d;Database=%s;User Id=%s;Password=%s;TrustServerCertificate=True;", es.Port, dbName, es.User, es.Password)
		}
		return fmt.Sprintf("Server=(localdb)\\MSSQLLocalDB;Database=%s;Trusted_Connection=True;", dbName)
	case "redis":
		if es.Password != "" {
			return fmt.Sprintf("redis://:%s@127.0.0.1:%d/0", url.QueryEscape(es.Password), es.Port)
		}
		return fmt.Sprintf("redis://127.0.0.1:%d/0", es.Port)
	default:
		return ""
	}
}

func (a *App) IDESnippet(engine, dbName string) (string, error) {
	return core.IDESnippet(engine, a.settings.Engine(engine), dbName)
}

func (a *App) RegisterCLIInPath() error {
	dirs := append([]string{a.paths.Root}, a.mgr.ClientPaths()...)
	return core.UpdateUserPath(dirs, true)
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ---------------------------------------------------------------- Settings

func (a *App) GetSettings() core.Settings { return a.settings.Get() }

func (a *App) SaveSettings(s core.Settings) error {
	for id, es := range s.Engines {
		if es.Port < 1 || es.Port > 65535 {
			return fmt.Errorf("invalid port for %s: %d (must be 1–65535)", id, es.Port)
		}
	}
	prev := a.settings.Get()
	// Preserve active version selections in case the UI didn't touch them
	for id, es := range s.Engines {
		if es.Version == "" && prev.Engines[id] != nil {
			es.Version = prev.Engines[id].Version
		}
	}
	if err := a.settings.Replace(s); err != nil {
		return err
	}
	if s.RunOnBoot != prev.RunOnBoot {
		if err := core.SetRunOnBoot(s.RunOnBoot); err != nil {
			return fmt.Errorf("settings saved, but failed to update Windows startup: %v", err)
		}
	}
	a.emitStatus()
	return nil
}

// ---------------------------------------------------------------- Database Browser

func (a *App) ListDatabases(engine string) ([]string, error) {
	return core.ListDatabases(engine, a.settings.Engine(engine))
}

func (a *App) ListTables(engine, dbName string) ([]core.TableInfo, error) {
	return core.ListTables(engine, a.settings.Engine(engine), dbName)
}

func (a *App) RunQuery(engine, dbName, query string) (*core.QueryResult, error) {
	return core.RunQuery(engine, a.settings.Engine(engine), dbName, query)
}

func (a *App) CreateDatabase(engine, name, user, password string) error {
	return core.CreateDatabase(engine, a.settings.Engine(engine), name, user, password)
}

func (a *App) DropDatabase(engine, name string) error {
	return core.DropDatabase(engine, a.settings.Engine(engine), name)
}

func (a *App) RedisDatabases() ([]core.RedisDB, error) {
	return core.RedisDatabases(a.settings.Engine("redis"))
}

func (a *App) RedisScan(db int, pattern, cursor string) (*core.RedisScanResult, error) {
	return core.RedisScan(a.settings.Engine("redis"), db, pattern, cursor)
}

func (a *App) RedisGet(db int, key string) (*core.RedisValue, error) {
	return core.RedisGet(a.settings.Engine("redis"), db, key)
}

func (a *App) RedisCommand(db int, line string) (string, error) {
	return core.RedisCommand(a.settings.Engine("redis"), db, line)
}

func (a *App) RedisDelete(db int, key string) error {
	return core.RedisDelete(a.settings.Engine("redis"), db, key)
}
