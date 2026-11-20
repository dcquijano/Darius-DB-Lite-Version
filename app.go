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
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"dariusdb/internal/core"
	"dariusdb/internal/tray"
)

const (
	AppName         = "Darius DB (Lite Version 0.1.3)"
	AppVersion      = "0.1.3"
	AppAuthor       = "Darius Cumpio"
	AppBuiltBy      = "Darius Cumpio"
	AppCreated      = "November 19, 2026"
	AppYear         = 2026
	AppLicense      = "MIT"
	AppRepo         = "https://github.com/dcquijano/Darius-DB-Lite-Version"
	AppInstallerURL = "https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing"
)

type AppInfo struct {
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Author       string     `json:"author"`
	BuiltBy      string     `json:"builtBy"`
	Created      string     `json:"created"`
	Year         int        `json:"year"`
	License      string     `json:"license"`
	Repo         string     `json:"repo"`
	InstallerURL string     `json:"installerUrl"`
	Root         string     `json:"root"`
	Paths        core.Paths `json:"paths"`
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

	mu          sync.Mutex
	quitting    bool
	forceShow   bool
	justUpdated bool
	skipAdopt   bool
}

func NewApp(paths *core.Paths, st *core.SettingsStore) *App {
	return NewAppWithOptions(paths, st, false)
}

func NewAppWithOptions(paths *core.Paths, st *core.SettingsStore, skipAdopt bool) *App {
	a := &App{
		paths:     paths,
		settings:  st,
		skipAdopt: skipAdopt,
	}
	a.mgr = core.NewManagerWithOptions(paths, st, a.emitStatus, skipAdopt)
	a.dl = core.NewDownloader(paths, a.emitProgress, func(engine, name string) {
		a.mgr.OnInstalled(engine, name)
	})
	a.term = core.NewTerminal(paths, st, a.mgr, nil)
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	go core.ApplyWindowIconFromPNG(AppName, tray.DefaultIconPNG)

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

	if a.settings.Get().AutoStartServices && !a.skipAdopt && !a.justUpdated {
		go func() { _ = a.mgr.StartAll() }()
	}
}

func (a *App) domReady(ctx context.Context) {
	if a.forceShow || a.justUpdated {
		runtime.WindowUnminimise(ctx)
		runtime.WindowShow(ctx)
		go core.ForceShowWindow(AppName)
	}
	if a.justUpdated {
		runtime.EventsEmit(ctx, "app-updated", core.CLIAppBuild)
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
		return "Darius DB (Lite Version 0.1.3) — All services stopped"
	}
	return fmt.Sprintf("Darius DB (Lite Version 0.1.3) — Running: %s", strings.Join(running, ", "))
}

func (a *App) buildTrayMenu() []tray.MenuItem {
	st := a.mgr.Status()
	items := []tray.MenuItem{
		{Label: "Open Darius DB (Lite Version 0.1.3)", Default: true, Action: a.ShowWindow},
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
		tray.MenuItem{Label: "Quit Darius DB (Lite Version 0.1.3)", Action: a.QuitApp},
	)
	return items
}

// ---------------------------------------------------------------- Window & Info

func (a *App) Info() AppInfo {
	return AppInfo{
		Name:         AppName,
		Version:      AppVersion,
		Author:       AppAuthor,
		BuiltBy:      AppBuiltBy,
		Created:      AppCreated,
		Year:         AppYear,
		License:      AppLicense,
		Repo:         AppRepo,
		InstallerURL: AppInstallerURL,
		Root:         a.paths.Root,
		Paths:        *a.paths,
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

func (a *App) Status() []core.ServiceStatus        { return a.mgr.Status() }
func (a *App) Start(engine string) error           { return a.mgr.Start(engine) }
func (a *App) Stop(engine string) error            { return a.mgr.Stop(engine) }
func (a *App) Restart(engine string) error         { return a.mgr.Restart(engine) }
func (a *App) StartAll() error                     { return a.mgr.StartAll() }
func (a *App) StopAll() error                      { return a.mgr.StopAll() }
func (a *App) SetActive(engine, name string) error { return a.mgr.SetActive(engine, name) }
func (a *App) Uninstall(engine, name string) error { return a.mgr.Uninstall(engine, name) }

// ---------------------------------------------------------------- Catalog & Downloads

func (a *App) Catalog() core.Catalog { return core.LoadCatalog(a.paths) }

func (a *App) RefreshCatalog() (core.Catalog, error) {
	s := a.settings.Get()
	return core.RefreshCatalogWithToken(a.paths, s.CatalogURL, s.GitHubToken)
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

func (a *App) ReadLog(engine string) string { return a.mgr.ReadLog(engine, 64*1024) }
func (a *App) ClearLog(engine string) error { return a.mgr.ClearLog(engine) }

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
		case "php":
			cli = fmt.Sprintf("php -v  |  http://localhost:%d/", st.Port)
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
		case "mailpit":
			cli = fmt.Sprintf("http://localhost:%d/  (SMTP :1025)", st.Port)
		}
		banner = append(banner, fmt.Sprintf("  %-11s %-18s port %-5d  (%s)", st.Name, st.Active, st.Port, cli))
	}
	if len(banner) == 1 {
		banner = append(banner, "  No engines installed yet. Install one from the Versions tab.")
	}
	return core.OpenTerminal(a.paths.Root, a.mgr.ClientPaths(), env, banner)
}

func (a *App) resolveTarget(engine string) (string, core.EngineSettings, *core.CloudConnection) {
	if strings.HasPrefix(engine, "cloud:") {
		id := strings.TrimPrefix(engine, "cloud:")
		for _, cc := range a.settings.Get().CloudConnections {
			if cc.ID == id {
				nc := cc.Normalized()
				return nc.Engine, nc.ToEngineSettings(), &nc
			}
		}
	}
	return engine, a.settings.Engine(engine), nil
}

func (a *App) ConnectionString(engine, dbName string) string {
	realEng, es, cloud := a.resolveTarget(engine)
	if cloud != nil {
		if uri, err := core.IDESnippet(realEng, es, fallback(dbName, cloud.Database)); err == nil && uri != "" {
			if raw, rerr := a.rawCloudURI(realEng, es, fallback(dbName, cloud.Database)); rerr == nil {
				return raw
			}
		}
	}
	switch realEng {
	case "apache", "php":
		return fmt.Sprintf("http://localhost:%d/", es.Port)
	case "mailpit":
		return fmt.Sprintf("smtp://127.0.0.1:1025 (Web Inbox: http://localhost:%d/)", es.Port)
	case "mariadb", "mysql":
		host := fallback(strings.TrimSpace(es.Host), "127.0.0.1")
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		return fmt.Sprintf("mysql://%s@%s:%d/%s", auth, host, es.Port, dbName)
	case "postgresql":
		host := fallback(strings.TrimSpace(es.Host), "127.0.0.1")
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		if dbName == "" {
			dbName = fallback(es.DefaultDB, "postgres")
		}
		return fmt.Sprintf("postgresql://%s@%s:%d/%s", auth, host, es.Port, dbName)
	case "mssql":
		if dbName == "" {
			dbName = fallback(es.DefaultDB, "master")
		}
		if cloud != nil && cloud.Host != "" {
			srv := cloud.Host
			if es.Port > 0 {
				srv = fmt.Sprintf("%s,%d", cloud.Host, es.Port)
			}
			return fmt.Sprintf("Server=%s;Database=%s;User Id=%s;Password=%s;Encrypt=True;TrustServerCertificate=True;", srv, dbName, es.User, es.Password)
		}
		srv := core.MSSQLServerName()
		return fmt.Sprintf("Server=%s;Database=%s;Trusted_Connection=True;TrustServerCertificate=True;", srv, dbName)
	case "redis":
		host := fallback(strings.TrimSpace(es.Host), "127.0.0.1")
		if es.Password != "" {
			return fmt.Sprintf("redis://:%s@%s:%d/0", url.QueryEscape(es.Password), host, es.Port)
		}
		return fmt.Sprintf("redis://%s:%d/0", host, es.Port)
	default:
		return ""
	}
}

func (a *App) rawCloudURI(realEng string, es core.EngineSettings, dbName string) (string, error) {
	// Used internally to pick clean URI formatting for cloud targets
	return a.ConnectionStringDirect(realEng, es, dbName), nil
}

func (a *App) ConnectionStringDirect(realEng string, es core.EngineSettings, dbName string) string {
	host := fallback(strings.TrimSpace(es.Host), "127.0.0.1")
	switch realEng {
	case "mariadb", "mysql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		return fmt.Sprintf("mysql://%s@%s:%d/%s", auth, host, es.Port, dbName)
	case "postgresql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		if dbName == "" {
			dbName = fallback(es.DefaultDB, "postgres")
		}
		q := ""
		if es.SSLMode != "" && es.SSLMode != "none" && es.SSLMode != "disable" {
			q = "?sslmode=" + url.QueryEscape(es.SSLMode)
		}
		return fmt.Sprintf("postgresql://%s@%s:%d/%s%s", auth, host, es.Port, dbName, q)
	case "mssql":
		if dbName == "" {
			dbName = fallback(es.DefaultDB, "master")
		}
		srv := host
		if es.Port > 0 {
			srv = fmt.Sprintf("%s,%d", host, es.Port)
		}
		return fmt.Sprintf("Server=%s;Database=%s;User Id=%s;Password=%s;Encrypt=True;TrustServerCertificate=True;", srv, dbName, es.User, es.Password)
	case "redis":
		scheme := "redis"
		if es.SSLMode == "require" || es.SSLMode == "skip-verify" {
			scheme = "rediss"
		}
		if es.Password != "" {
			return fmt.Sprintf("%s://:%s@%s:%d/0", scheme, url.QueryEscape(es.Password), host, es.Port)
		}
		return fmt.Sprintf("%s://%s:%d/0", scheme, host, es.Port)
	default:
		return ""
	}
}

func (a *App) IDESnippet(engine, dbName string) (string, error) {
	realEng, es, cloud := a.resolveTarget(engine)
	if cloud != nil && dbName == "" {
		dbName = cloud.Database
	}
	return core.IDESnippet(realEng, es, dbName)
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
	// Preserve active version selections and saved cloud connections in case the UI didn't touch them
	for id, es := range s.Engines {
		if es.Version == "" && prev.Engines[id] != nil {
			es.Version = prev.Engines[id].Version
		}
	}
	if s.CloudConnections == nil {
		s.CloudConnections = prev.CloudConnections
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

// ---------------------------------------------------------------- Database Browser (Local & Cloud)

func (a *App) ListDatabases(engine string) ([]string, error) {
	realEng, es, _ := a.resolveTarget(engine)
	return core.ListDatabases(realEng, es)
}

func (a *App) ListTables(engine, dbName string) ([]core.TableInfo, error) {
	realEng, es, _ := a.resolveTarget(engine)
	return core.ListTables(realEng, es, dbName)
}

func (a *App) RunQuery(engine, dbName, query string) (*core.QueryResult, error) {
	realEng, es, _ := a.resolveTarget(engine)
	return core.RunQuery(realEng, es, dbName, query)
}

func (a *App) RunQueryAll(engine, dbName, query string) (*core.QueryResult, error) {
	realEng, es, _ := a.resolveTarget(engine)
	return core.RunQueryAll(realEng, es, dbName, query)
}

func (a *App) DescribeTable(engine, dbName, tableName string) ([]core.ColumnInfo, error) {
	realEng, es, _ := a.resolveTarget(engine)
	return core.DescribeTable(realEng, es, dbName, tableName)
}

func (a *App) InsertRow(engine, dbName, tableName string, values map[string]core.RowValue) error {
	realEng, es, _ := a.resolveTarget(engine)
	return core.InsertRow(realEng, es, dbName, tableName, values)
}

func (a *App) UpdateRow(engine, dbName, tableName string, keys, updates map[string]core.RowValue, hasPK bool) error {
	realEng, es, _ := a.resolveTarget(engine)
	return core.UpdateRow(realEng, es, dbName, tableName, keys, updates, hasPK)
}

func (a *App) DeleteRow(engine, dbName, tableName string, keys map[string]core.RowValue, hasPK bool) error {
	realEng, es, _ := a.resolveTarget(engine)
	return core.DeleteRow(realEng, es, dbName, tableName, keys, hasPK)
}

func (a *App) CreateTable(engine, dbName, tableName string, cols []core.CreateTableColumn) error {
	realEng, es, _ := a.resolveTarget(engine)
	return core.CreateTable(realEng, es, dbName, tableName, cols)
}

func (a *App) DropTable(engine, dbName, tableName string) error {
	realEng, es, _ := a.resolveTarget(engine)
	return core.DropTable(realEng, es, dbName, tableName)
}

func (a *App) CreateDatabase(engine, name, user, password string) error {
	realEng, es, _ := a.resolveTarget(engine)
	return core.CreateDatabase(realEng, es, name, user, password)
}

func (a *App) DropDatabase(engine, name string) error {
	realEng, es, _ := a.resolveTarget(engine)
	return core.DropDatabase(realEng, es, name)
}

type RestoreFileChoice struct {
	Path        string `json:"path"`
	SuggestedDB string `json:"suggestedDb"`
}

func (a *App) PickRestoreFile(engine string) (*RestoreFileChoice, error) {
	if a.ctx == nil {
		return nil, errors.New("window not ready")
	}
	filters := []runtime.FileFilter{
		{DisplayName: "Database Imports & Backups (*.sql;*.csv;*.bak;*.backup;*.dump)", Pattern: "*.sql;*.csv;*.bak;*.backup;*.dump"},
		{DisplayName: "SQL Script (*.sql)", Pattern: "*.sql"},
		{DisplayName: "CSV Data File (*.csv)", Pattern: "*.csv"},
		{DisplayName: "MS SQL Server Backup (*.bak)", Pattern: "*.bak"},
		{DisplayName: "All Files (*.*)", Pattern: "*.*"},
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Select .sql, .csv, or .bak File to Import / Restore",
		Filters: filters,
	})
	if err != nil || path == "" {
		return nil, err
	}
	return &RestoreFileChoice{
		Path:        path,
		SuggestedDB: core.DeriveDBNameFromFile(path),
	}, nil
}

type ImportResult struct {
	Database string `json:"database"`
	Table    string `json:"table,omitempty"`
	Rows     int    `json:"rows,omitempty"`
	Message  string `json:"message"`
}

func (a *App) RestoreDatabase(engine, filePath, targetDB string) (string, error) {
	realEng, es, _ := a.resolveTarget(engine)
	return core.RestoreDatabase(a.paths, realEng, es, filePath, targetDB)
}

func (a *App) ImportDatabaseFile(engine, filePath, targetDB, targetTable string) (*ImportResult, error) {
	realEng, es, _ := a.resolveTarget(engine)
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filePath)))
	if ext == ".csv" {
		dbName, count, err := core.ImportCSV(realEng, es, filePath, targetDB, targetTable)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(targetTable) == "" {
			targetTable = core.DeriveDBNameFromFile(filePath)
		}
		return &ImportResult{
			Database: dbName,
			Table:    targetTable,
			Rows:     count,
			Message:  fmt.Sprintf("Imported %d CSV row(s) into %s.%s", count, dbName, targetTable),
		}, nil
	}
	dbName, err := core.RestoreDatabase(a.paths, realEng, es, filePath, targetDB)
	if err != nil {
		return nil, err
	}
	return &ImportResult{
		Database: dbName,
		Message:  fmt.Sprintf("Successfully imported %q into database %q", filepath.Base(filePath), dbName),
	}, nil
}

func (a *App) BackupDatabaseDialog(engine, dbName, format string) (string, error) {
	return a.ExportDatabaseDialog(engine, dbName, nil, format, true, true)
}

func (a *App) ExportDatabaseDialog(engine, dbName string, tables []string, format string, includeSchema, includeData bool) (string, error) {
	if a.ctx == nil {
		return "", errors.New("window not ready")
	}
	if strings.TrimSpace(dbName) == "" {
		return "", errors.New("select a database to export first")
	}
	ext := strings.ToLower(strings.TrimPrefix(format, "."))
	if ext == "" {
		if engine == "mssql" && len(tables) == 0 {
			ext = "bak"
		} else {
			ext = "sql"
		}
	}
	suffix := ""
	if len(tables) == 1 {
		suffix = "_" + strings.ReplaceAll(tables[0], ".", "_")
	} else if len(tables) > 1 {
		suffix = fmt.Sprintf("_%dtables", len(tables))
	}
	defaultName := fmt.Sprintf("%s%s_%s.%s", dbName, suffix, time.Now().Format("20060102"), ext)
	var filters []runtime.FileFilter
	switch ext {
	case "csv":
		filters = []runtime.FileFilter{
			{DisplayName: "CSV Spreadsheet (*.csv)", Pattern: "*.csv"},
			{DisplayName: "SQL Script (*.sql)", Pattern: "*.sql"},
		}
	case "bak":
		filters = []runtime.FileFilter{
			{DisplayName: "MS SQL Server Backup (*.bak)", Pattern: "*.bak"},
			{DisplayName: "SQL Script (*.sql)", Pattern: "*.sql"},
		}
	default:
		filters = []runtime.FileFilter{
			{DisplayName: "SQL Script (*.sql)", Pattern: "*.sql"},
			{DisplayName: "CSV Spreadsheet (*.csv)", Pattern: "*.csv"},
			{DisplayName: "MS SQL Server Backup (*.bak)", Pattern: "*.bak"},
		}
	}
	outPath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           fmt.Sprintf("Export %s (%s)", dbName, engine),
		DefaultFilename: defaultName,
		Filters:         filters,
	})
	if err != nil || outPath == "" {
		return "", err
	}
	if ext == "bak" && len(tables) == 0 {
		realEng, es, _ := a.resolveTarget(engine)
		if err := core.BackupDatabase(a.paths, realEng, es, dbName, outPath); err != nil {
			return "", err
		}
		return outPath, nil
	}
	realEng, es, _ := a.resolveTarget(engine)
	if err := core.ExportDatabaseSelection(a.paths, realEng, es, dbName, tables, ext, includeSchema, includeData, outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

// ---------------------------------------------------------------- Cloud Database Profiles & Sync

func (a *App) CloudProfiles() []core.CloudConnection {
	list := a.settings.Get().CloudConnections
	if list == nil {
		return []core.CloudConnection{}
	}
	return list
}

func (a *App) SaveCloudProfile(conn core.CloudConnection) ([]core.CloudConnection, error) {
	nc := conn.Normalized()
	if nc.Host == "" {
		return nil, errors.New("host / server address is required")
	}
	err := a.settings.Update(func(s *core.Settings) {
		for i, cur := range s.CloudConnections {
			if cur.ID == nc.ID {
				if nc.LastConnected == "" {
					nc.LastConnected = cur.LastConnected
					nc.LastLatencyMs = cur.LastLatencyMs
				}
				s.CloudConnections[i] = nc
				return
			}
		}
		s.CloudConnections = append(s.CloudConnections, nc)
	})
	if err != nil {
		return nil, err
	}
	core.CloseSQLPools(nc.Engine, "")
	return a.CloudProfiles(), nil
}

func (a *App) DeleteCloudProfile(id string) ([]core.CloudConnection, error) {
	err := a.settings.Update(func(s *core.Settings) {
		kept := make([]core.CloudConnection, 0, len(s.CloudConnections))
		for _, cur := range s.CloudConnections {
			if cur.ID != id {
				kept = append(kept, cur)
			}
		}
		s.CloudConnections = kept
	})
	if err != nil {
		return nil, err
	}
	return a.CloudProfiles(), nil
}

func (a *App) ParseCloudURI(rawURI string) (*core.CloudConnection, error) {
	return core.ParseCloudURI(rawURI)
}

func (a *App) TestCloudProfile(conn core.CloudConnection) (*core.CloudTestResult, error) {
	res, err := core.TestCloudConnection(conn)
	if err == nil && res != nil && res.OK && conn.ID != "" {
		_ = a.settings.Update(func(s *core.Settings) {
			for i, cur := range s.CloudConnections {
				if cur.ID == conn.ID {
					s.CloudConnections[i].LastConnected = time.Now().Format("2006-01-02 15:04")
					s.CloudConnections[i].LastLatencyMs = res.LatencyMs
				}
			}
		})
	}
	return res, err
}

func (a *App) CloudSyncDatabase(srcTarget, srcDB, dstTarget, dstDB string) (string, error) {
	srcEng, srcEs, _ := a.resolveTarget(srcTarget)
	dstEng, dstEs, _ := a.resolveTarget(dstTarget)
	return core.CloudSyncDatabase(a.paths, srcEng, srcEs, srcDB, dstEng, dstEs, dstDB)
}

func (a *App) CloudRedisDatabases(target string) ([]core.RedisDB, error) {
	_, es, _ := a.resolveTarget(target)
	return core.RedisDatabases(es)
}

func (a *App) CloudRedisScan(target string, db int, pattern, cursor string) (*core.RedisScanResult, error) {
	_, es, _ := a.resolveTarget(target)
	return core.RedisScan(es, db, pattern, cursor)
}

func (a *App) CloudRedisGet(target string, db int, key string) (*core.RedisValue, error) {
	_, es, _ := a.resolveTarget(target)
	return core.RedisGet(es, db, key)
}

func (a *App) CloudRedisCommand(target string, db int, line string) (string, error) {
	_, es, _ := a.resolveTarget(target)
	return core.RedisCommand(es, db, line)
}

func (a *App) CloudRedisDelete(target string, db int, key string) error {
	_, es, _ := a.resolveTarget(target)
	return core.RedisDelete(es, db, key)
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

func (a *App) Repair(target string, resetConfigs bool) (*core.RepairReport, error) {
	return a.mgr.Repair(target, resetConfigs)
}

func (a *App) DeployWebsiteDialog() (string, error) {
	if a.ctx == nil {
		return "", errors.New("window not ready")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Website .zip Archive or index.html to Host in www/",
		Filters: []runtime.FileFilter{
			{DisplayName: "Website Package (*.zip;*.html;*.htm)", Pattern: "*.zip;*.html;*.htm"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	siteName, _, err := core.DeployWebsitePath(a.paths.Www, path, "")
	if err != nil {
		return "", err
	}
	port := a.settings.Engine("apache").Port
	if port <= 0 {
		port = 8080
	}
	return fmt.Sprintf("http://localhost:%d/%s/", port, siteName), nil
}

// ---------------------------------------------------------------- System Version Updater

func (a *App) CheckSystemUpdate() (*core.SystemUpdateInfo, error) {
	return core.CheckSystemUpdate(AppVersion, a.settings.Get().GitHubToken)
}

func (a *App) SyncFromGitHub() (*core.SystemUpdateInfo, error) {
	return core.SyncFromGitHub(a.paths, AppVersion, a.settings.Get().GitHubToken)
}

func (a *App) SaveGitHubToken(token string) error {
	return a.settings.Update(func(s *core.Settings) {
		s.GitHubToken = strings.TrimSpace(token)
	})
}

func (a *App) HasGitHubToken() bool {
	return strings.TrimSpace(a.settings.Get().GitHubToken) != ""
}

func (a *App) LaunchUpdaterTool() error {
	return core.LaunchStandaloneUpdater(a.paths)
}

func (a *App) ApplySystemUpdateAuto(primaryURL, fallbackURL string) (string, error) {
	_ = a.settings.Update(func(s *core.Settings) { s.AutoStartServices = false })
	_ = a.mgr.StopAll()
	msg, err := core.ApplySystemUpdateWithFallback(a.paths, primaryURL, fallbackURL, a.settings.Get().GitHubToken)
	if err != nil {
		return "", err
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		a.QuitApp()
	}()
	return msg, nil
}

func (a *App) ApplySystemUpdateFromURL(downloadURL string) (string, error) {
	_ = a.settings.Update(func(s *core.Settings) { s.AutoStartServices = false })
	_ = a.mgr.StopAll()
	msg, err := core.ApplySystemUpdateFromURL(a.paths, downloadURL, a.settings.Get().GitHubToken)
	if err != nil {
		return "", err
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		a.QuitApp()
	}()
	return msg, nil
}

func (a *App) ApplySystemUpdateFromFileDialog() (string, error) {
	if a.ctx == nil {
		return "", errors.New("window not ready")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select DariusDB-Setup-*.exe Installer or DariusDB-*-portable.zip to Update System",
		Filters: []runtime.FileFilter{
			{DisplayName: "Darius DB System Update (*.exe;*.zip)", Pattern: "*.exe;*.zip"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	_ = a.settings.Update(func(s *core.Settings) { s.AutoStartServices = false })
	_ = a.mgr.StopAll()
	msg, err := core.ApplySystemUpdateFromFile(a.paths, path)
	if err != nil {
		return "", err
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		a.QuitApp()
	}()
	return msg, nil
}
