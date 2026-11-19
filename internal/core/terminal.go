package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TerminalState struct {
	Mode   string `json:"mode"`   // shell | mariadb | mysql | postgresql | redis
	Cwd    string `json:"cwd"`
	Target string `json:"target"` // active SQL database name or Redis db index
	Prompt string `json:"prompt"`
	Busy   bool   `json:"busy"`
}

type TerminalExecResult struct {
	State  TerminalState `json:"state"`
	Output string        `json:"output"`
	Clear  bool          `json:"clear,omitempty"`
}

type Terminal struct {
	p    *Paths
	st   *SettingsStore
	mgr  *Manager
	emit func(chunk string)

	mu     sync.Mutex
	mode   string
	cwd    string
	sqlDB  map[string]string
	redDB  int
	cancel context.CancelFunc
}

func NewTerminal(p *Paths, st *SettingsStore, mgr *Manager, emit func(chunk string)) *Terminal {
	return &Terminal{
		p:     p,
		st:    st,
		mgr:   mgr,
		emit:  emit,
		mode:  "shell",
		cwd:   p.Root,
		sqlDB: map[string]string{"mariadb": "", "mysql": "", "postgresql": "postgres", "mssql": "master"},
	}
}

func (t *Terminal) State() TerminalState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stateLocked()
}

func (t *Terminal) stateLocked() TerminalState {
	st := TerminalState{
		Mode: t.mode,
		Cwd:  t.cwd,
		Busy: t.cancel != nil,
	}
	switch t.mode {
	case "mariadb", "mysql":
		db := t.sqlDB[t.mode]
		st.Target = db
		if db == "" {
			db = "none"
		}
		st.Prompt = fmt.Sprintf("%s [%s]>", t.mode, db)
	case "postgresql":
		db := t.sqlDB["postgresql"]
		if db == "" {
			db = "postgres"
		}
		st.Target = db
		st.Prompt = fmt.Sprintf("%s=#", db)
	case "mssql":
		db := t.sqlDB["mssql"]
		if db == "" {
			db = "master"
		}
		st.Target = db
		st.Prompt = fmt.Sprintf("mssql [%s]>", db)
	case "redis":
		es := t.st.Engine("redis")
		st.Target = strconv.Itoa(t.redDB)
		if t.redDB > 0 {
			st.Prompt = fmt.Sprintf("127.0.0.1:%d[%d]>", es.Port, t.redDB)
		} else {
			st.Prompt = fmt.Sprintf("127.0.0.1:%d>", es.Port)
		}
	default:
		st.Prompt = t.cwd + ">"
	}
	return st
}

// Banner returns the welcome message shown when the built-in terminal opens.
func (t *Terminal) Banner() string {
	var b strings.Builder
	b.WriteString("Darius DB (Lite Version 0.1.2) Built-in Terminal (Created by Darius Cumpio)\n")
	b.WriteString("Type 'help' for built-in commands, 'status' for services, or switch modes above.\n")
	b.WriteString("--------------------------------------------------------------------------------\n")
	statuses := t.mgr.Status()
	any := false
	for _, s := range statuses {
		if s.Active == "" {
			continue
		}
		any = true
		fmt.Fprintf(&b, "  %-11s %-20s :%-5d  (%s)\n", s.Name, s.Active, s.Port, s.State)
	}
	if !any {
		b.WriteString("  No database engines installed yet. Open 'Versions' to download one.\n")
	}
	return b.String()
}

// SetMode switches the active terminal mode ("shell", "mariadb", "mysql", "postgresql", "mssql", "redis").
func (t *Terminal) SetMode(mode string) (*TerminalExecResult, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "shell", "mariadb", "mysql", "postgresql", "mssql", "redis":
	default:
		return nil, fmt.Errorf("unknown terminal mode %q", mode)
	}

	t.mu.Lock()
	t.mode = mode
	state := t.stateLocked()
	t.mu.Unlock()

	msg := ""
	switch mode {
	case "shell":
		msg = "Switched to Windows Shell mode (portable DB tools in PATH)."
	case "mariadb", "mysql":
		es := t.st.Engine(mode)
		if !t.mgr.IsRunning(mode) {
			msg = fmt.Sprintf("Switched to %s SQL shell (port %d). Note: %s is currently stopped — type 'start %s' or start it from Dashboard.", mode, es.Port, mode, mode)
		} else {
			msg = fmt.Sprintf("Connected to %s on 127.0.0.1:%d as %s. Type SQL queries, 'USE <db>', or 'exit' to return to shell.", mode, es.Port, es.User)
		}
	case "postgresql":
		es := t.st.Engine("postgresql")
		if !t.mgr.IsRunning("postgresql") {
			msg = fmt.Sprintf("Switched to PostgreSQL (psql) shell (port %d). Note: PostgreSQL is currently stopped — type 'start postgresql' or start it from Dashboard.", es.Port)
		} else {
			msg = fmt.Sprintf("Connected to PostgreSQL on 127.0.0.1:%d/%s as %s. Supports SQL, \\l, \\c <db>, \\dt, \\du, or \\q.", es.Port, state.Target, es.User)
		}
	case "mssql":
		es := t.st.Engine("mssql")
		if !t.mgr.IsRunning("mssql") {
			msg = "Switched to MS SQL Server (sqlcmd) shell. Note: MS SQL Server is currently stopped — type 'start mssql' or start it from Dashboard."
		} else {
			msg = fmt.Sprintf("Connected to MS SQL Server (%s, db: %s). Supports T-SQL, USE <db>, sp_databases, sp_tables, or exit.", mssqlServerTarget(es), state.Target)
		}
	case "redis":
		es := t.st.Engine("redis")
		if !t.mgr.IsRunning("redis") {
			msg = fmt.Sprintf("Switched to Redis CLI (port %d). Note: Redis is currently stopped — type 'start redis' or start it from Dashboard.", es.Port)
		} else {
			msg = fmt.Sprintf("Connected to Redis on 127.0.0.1:%d (db%d). Type Redis commands (PING, SET, GET, KEYS *) or 'exit'.", es.Port, t.redDB)
		}
	}
	return &TerminalExecResult{State: state, Output: msg}, nil
}

// Cancel terminates any currently running external shell command.
func (t *Terminal) Cancel() {
	t.mu.Lock()
	c := t.cancel
	t.mu.Unlock()
	if c != nil {
		c()
	}
}

// Exec runs a command line in the current terminal mode.
func (t *Terminal) Exec(rawLine string) (*TerminalExecResult, error) {
	line := strings.TrimSpace(rawLine)
	t.mu.Lock()
	mode := t.mode
	state := t.stateLocked()
	t.mu.Unlock()

	if line == "" {
		return &TerminalExecResult{State: state}, nil
	}

	low := strings.ToLower(line)
	if low == "cls" || low == "clear" {
		return &TerminalExecResult{State: state, Clear: true}, nil
	}

	// Universal service control shortcuts work in any mode
	if res, ok := t.tryServiceCommand(line); ok {
		return res, nil
	}

	switch mode {
	case "mariadb", "mysql":
		return t.execMySQLMode(mode, line)
	case "postgresql":
		return t.execPostgresMode(line)
	case "mssql":
		return t.execMSSQLMode(line)
	case "redis":
		return t.execRedisMode(line)
	default:
		return t.execShellMode(line)
	}
}

func (t *Terminal) tryServiceCommand(line string) (*TerminalExecResult, bool) {
	fields := strings.Fields(strings.TrimRight(line, ";"))
	if len(fields) == 0 {
		return nil, false
	}
	verb := strings.ToLower(fields[0])
	if (verb == "status" || verb == "services") && len(fields) == 1 {
		return &TerminalExecResult{State: t.State(), Output: t.formatStatusTable()}, true
	}
	if (verb == "start" || verb == "stop" || verb == "restart") && len(fields) == 2 {
		target := strings.ToLower(fields[1])
		switch target {
		case "all":
			var err error
			if verb == "start" {
				err = t.mgr.StartAll()
			} else if verb == "stop" {
				err = t.mgr.StopAll()
			} else {
				return nil, false
			}
			if err != nil {
				return &TerminalExecResult{State: t.State(), Output: "Error: " + err.Error()}, true
			}
			return &TerminalExecResult{State: t.State(), Output: t.formatStatusTable()}, true
		case "apache", "httpd", "web", "mariadb", "mysql", "postgresql", "postgres", "mssql", "sqlserver", "redis":
			if target == "postgres" {
				target = "postgresql"
			} else if target == "sqlserver" {
				target = "mssql"
			} else if target == "httpd" || target == "web" {
				target = "apache"
			}
			var err error
			switch verb {
			case "start":
				err = t.mgr.Start(target)
			case "stop":
				err = t.mgr.Stop(target)
			case "restart":
				err = t.mgr.Restart(target)
			}
			if err != nil {
				return &TerminalExecResult{State: t.State(), Output: "Error: " + err.Error()}, true
			}
			return &TerminalExecResult{State: t.State(), Output: fmt.Sprintf("%s: %s completed.\n%s", target, verb, t.formatStatusTable())}, true
		}
	}
	return nil, false
}

func (t *Terminal) formatStatusTable() string {
	rows := [][]string{}
	for _, s := range t.mgr.Status() {
		ver := s.Active
		if ver == "" {
			ver = "(not installed)"
		}
		pid := "-"
		if s.Pid > 0 {
			pid = strconv.Itoa(s.Pid)
		}
		rows = append(rows, []string{s.Name, s.State, ver, strconv.Itoa(s.Port), pid})
	}
	return renderASCIITable([]string{"ENGINE", "STATE", "ACTIVE VERSION", "PORT", "PID"}, rows)
}

// ---------------------------------------------------------------- Shell mode

func (t *Terminal) execShellMode(line string) (*TerminalExecResult, error) {
	fields := strings.Fields(line)
	cmd0 := strings.ToLower(strings.TrimSuffix(filepath.Base(fields[0]), ".exe"))

	// Built-in help
	if cmd0 == "help" && len(fields) == 1 {
		help := strings.Join([]string{
			"Darius DB Terminal Commands:",
			"  status                      Show all database services, ports, and PIDs",
			"  start <engine|all>          Start mariadb, mysql, postgresql, redis, or all",
			"  stop <engine|all>           Stop mariadb, mysql, postgresql, redis, or all",
			"  restart <engine>            Restart a database service",
			"  mariadb | mysql             Enter interactive MariaDB / MySQL SQL shell",
			"  psql                        Enter interactive PostgreSQL SQL shell",
			"  redis-cli                   Enter interactive Redis CLI shell",
			"  cd <dir>                    Change working directory",
			"  path                        Show portable database bin directories in PATH",
			"  cls / clear                 Clear terminal output",
			"  <any Windows/CLI command>   Runs in cmd.exe with portable DB tools in PATH",
			"                              (e.g. dir, mysqldump, pg_dump, mariadb --version)",
		}, "\n")
		return &TerminalExecResult{State: t.State(), Output: help}, nil
	}

	if cmd0 == "pwd" && len(fields) == 1 {
		return &TerminalExecResult{State: t.State(), Output: t.State().Cwd}, nil
	}

	if cmd0 == "path" && len(fields) == 1 {
		cp := t.mgr.ClientPaths()
		if len(cp) == 0 {
			return &TerminalExecResult{State: t.State(), Output: "No active engine bin/ directories (install an engine in Versions)."}, nil
		}
		return &TerminalExecResult{State: t.State(), Output: "Active DB client folders in PATH:\n  " + strings.Join(cp, "\n  ")}, nil
	}

	// Built-in cd
	if cmd0 == "cd" || cmd0 == "chdir" {
		arg := strings.TrimSpace(line[len(fields[0]):])
		if strings.HasPrefix(strings.ToLower(arg), "/d ") {
			arg = strings.TrimSpace(arg[3:])
		}
		arg = strings.Trim(arg, `"`)
		if arg == "" {
			return &TerminalExecResult{State: t.State(), Output: t.State().Cwd}, nil
		}
		t.mu.Lock()
		target := arg
		if !filepath.IsAbs(target) {
			target = filepath.Join(t.cwd, target)
		}
		target = filepath.Clean(target)
		st, err := os.Stat(target)
		if err != nil || !st.IsDir() {
			state := t.stateLocked()
			t.mu.Unlock()
			return &TerminalExecResult{State: state, Output: fmt.Sprintf("The system cannot find the path specified: %s", target)}, nil
		}
		t.cwd = target
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{State: state}, nil
	}

	// Intercept interactive REPL invocations so they don't hang waiting for a Win32 console handle
	if isInteractiveDBInvoke(cmd0, fields[1:]) {
		switch cmd0 {
		case "mariadb":
			return t.SetMode("mariadb")
		case "mysql":
			if !t.mgr.IsRunning("mysql") && t.mgr.IsRunning("mariadb") {
				return t.SetMode("mariadb")
			}
			return t.SetMode("mysql")
		case "psql":
			return t.SetMode("postgresql")
		case "sqlcmd", "mssql":
			return t.SetMode("mssql")
		case "redis-cli":
			return t.SetMode("redis")
		}
	}

	// Run external command via cmd.exe with portable DB tools in PATH
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.mu.Lock()
	t.cancel = cancel
	cwd := t.cwd
	t.mu.Unlock()
	defer func() {
		cancel()
		t.mu.Lock()
		t.cancel = nil
		t.mu.Unlock()
	}()

	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd := exec.CommandContext(ctx, cmdExe, "/D", "/S", "/C", line)
	cmd.Dir = cwd
	cmd.Env = t.buildEnv()
	hideWindow(cmd)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return &TerminalExecResult{State: t.State(), Output: "Failed to start command: " + err.Error()}, nil
	}

	var out strings.Builder
	doneRead := make(chan struct{})
	go func() {
		defer close(doneRead)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			text := sc.Text() + "\n"
			out.WriteString(text)
			if t.emit != nil {
				t.emit(text)
			}
		}
	}()

	werr := cmd.Wait()
	pw.Close()
	<-doneRead
	pr.Close()

	resText := strings.TrimRight(out.String(), "\r\n")
	if ctx.Err() == context.Canceled {
		resText += "\n^C (cancelled)"
	} else if ctx.Err() == context.DeadlineExceeded {
		resText += "\n(command timed out)"
	} else if werr != nil && resText == "" {
		resText = werr.Error()
	}
	return &TerminalExecResult{State: t.State(), Output: resText}, nil
}

// isInteractiveDBInvoke reports whether cmd0 + args would enter an interactive
// REPL prompt rather than executing a one-shot action (-e, -c, -V, --version, --help).
func isInteractiveDBInvoke(cmd0 string, args []string) bool {
	switch cmd0 {
	case "mariadb", "mysql":
		for _, a := range args {
			if a == "-e" || strings.HasPrefix(a, "--execute") || a == "-V" || a == "--version" || a == "--help" || a == "-?" {
				return false
			}
		}
		return true
	case "psql":
		for _, a := range args {
			if a == "-c" || strings.HasPrefix(a, "--command") || a == "-V" || a == "--version" || a == "--help" || a == "-?" || a == "-l" || a == "--list" || a == "-f" {
				return false
			}
		}
		return true
	case "sqlcmd", "mssql":
		for _, a := range args {
			if a == "-Q" || a == "-q" || a == "-i" || a == "-?" || a == "--version" || a == "--help" {
				return false
			}
		}
		return true
	case "redis-cli":
		// redis-cli is interactive only when no command words are passed
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "-p" || a == "-h" || a == "-a" || a == "-n" {
				i++ // skip flag value
				continue
			}
			return false
		}
		return true
	}
	return false
}

func (t *Terminal) buildEnv() []string {
	s := t.st.Get()
	overrides := map[string]string{
		"DARIUSDB_HOME": t.p.Root,
	}
	if pg := s.Engines["postgresql"]; pg != nil {
		overrides["PGHOST"] = "127.0.0.1"
		overrides["PGPORT"] = strconv.Itoa(pg.Port)
		if pg.User != "" {
			overrides["PGUSER"] = pg.User
		}
		if pg.Password != "" {
			overrides["PGPASSWORD"] = pg.Password
		}
	}
	var my *EngineSettings
	if m := s.Engines["mariadb"]; m != nil && m.Version != "" {
		my = m
	} else if m := s.Engines["mysql"]; m != nil && m.Version != "" {
		my = m
	}
	if my != nil {
		overrides["MYSQL_HOST"] = "127.0.0.1"
		overrides["MYSQL_TCP_PORT"] = strconv.Itoa(my.Port)
		if my.Password != "" {
			overrides["MYSQL_PWD"] = my.Password
		}
	}

	prefix := t.mgr.ClientPaths()
	newPath := strings.Join(append(prefix, os.Getenv("PATH")), ";")
	base := os.Environ()
	out := make([]string, 0, len(base)+len(overrides)+1)
	for _, kv := range base {
		k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if k == "PATH" {
			continue
		}
		if _, ok := overrides[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PATH="+newPath)
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// ---------------------------------------------------------------- MariaDB / MySQL interactive shell

func (t *Terminal) execMySQLMode(engine, line string) (*TerminalExecResult, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(line, ";"))
	low := strings.ToLower(trimmed)
	if low == "exit" || low == "quit" || low == `\q` {
		return t.SetMode("shell")
	}
	if low == "status" || low == `\s` {
		es := t.st.Engine(engine)
		t.mu.Lock()
		db := t.sqlDB[engine]
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{
			State:  state,
			Output: fmt.Sprintf("%s  127.0.0.1:%d  user: %s  current database: %s", engine, es.Port, es.User, db),
		}, nil
	}
	if strings.HasPrefix(low, "use ") || strings.HasPrefix(low, `\u `) {
		dbName := strings.Trim(strings.TrimSpace(trimmed[3:]), "`\"' ")
		// Verify connection to the database
		es := t.st.Engine(engine)
		db, err := openSQL(engine, es, dbName)
		if err != nil {
			return &TerminalExecResult{State: t.State(), Output: "ERROR: " + err.Error()}, nil
		}
		db.Close()
		t.mu.Lock()
		t.sqlDB[engine] = dbName
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{State: state, Output: "Database changed to " + dbName}, nil
	}

	t.mu.Lock()
	activeDB := t.sqlDB[engine]
	t.mu.Unlock()

	res, err := RunQuery(engine, t.st.Engine(engine), activeDB, line)
	if err != nil {
		return &TerminalExecResult{State: t.State(), Output: "ERROR: " + err.Error()}, nil
	}
	return &TerminalExecResult{State: t.State(), Output: formatSQLResult(res)}, nil
}

// ---------------------------------------------------------------- PostgreSQL (psql) interactive shell

func (t *Terminal) execPostgresMode(line string) (*TerminalExecResult, error) {
	trimmed := strings.TrimSpace(line)
	low := strings.ToLower(strings.TrimRight(trimmed, ";"))
	switch {
	case low == "exit" || low == "quit" || low == `\q`:
		return t.SetMode("shell")
	case low == `\?` || low == "help":
		out := strings.Join([]string{
			"psql meta-commands supported in Darius DB Terminal:",
			"  \\l                 List all databases",
			"  \\c <dbname>        Switch to database <dbname>",
			"  \\dt                List tables in current database",
			"  \\du                List roles / users",
			"  \\conninfo          Show current connection info",
			"  \\q or exit         Return to Windows shell mode",
			"  <SQL query>        Execute SQL query against current database",
		}, "\n")
		return &TerminalExecResult{State: t.State(), Output: out}, nil
	case low == `\conninfo`:
		es := t.st.Engine("postgresql")
		t.mu.Lock()
		db := t.sqlDB["postgresql"]
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{
			State:  state,
			Output: fmt.Sprintf(`You are connected to database "%s" as user "%s" on host "127.0.0.1" at port "%d".`, db, es.User, es.Port),
		}, nil
	case low == `\l` || low == `\list`:
		line = "SELECT datname AS \"Name\", pg_catalog.pg_get_userbyid(datdba) AS \"Owner\", pg_catalog.pg_encoding_to_char(encoding) AS \"Encoding\" FROM pg_catalog.pg_database WHERE NOT datistemplate ORDER BY 1;"
	case low == `\dt`:
		line = "SELECT table_schema AS \"Schema\", table_name AS \"Name\", table_type AS \"Type\" FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY 1, 2;"
	case low == `\du`:
		line = "SELECT rolname AS \"Role name\", rolsuper AS \"Superuser\", rolcreatedb AS \"Create DB\", rolcanlogin AS \"Can login\" FROM pg_catalog.pg_roles ORDER BY 1;"
	case strings.HasPrefix(low, `\c `) || strings.HasPrefix(low, `\connect `):
		parts := strings.Fields(trimmed)
		if len(parts) < 2 {
			return &TerminalExecResult{State: t.State(), Output: "Usage: \\c <database>"}, nil
		}
		dbName := strings.Trim(parts[1], `"'`)
		es := t.st.Engine("postgresql")
		db, err := openSQL("postgresql", es, dbName)
		if err != nil {
			return &TerminalExecResult{State: t.State(), Output: "FATAL: " + err.Error()}, nil
		}
		db.Close()
		t.mu.Lock()
		t.sqlDB["postgresql"] = dbName
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{State: state, Output: fmt.Sprintf(`You are now connected to database "%s" as user "%s".`, dbName, es.User)}, nil
	}

	t.mu.Lock()
	activeDB := t.sqlDB["postgresql"]
	t.mu.Unlock()

	res, err := RunQuery("postgresql", t.st.Engine("postgresql"), activeDB, line)
	if err != nil {
		return &TerminalExecResult{State: t.State(), Output: "ERROR: " + err.Error()}, nil
	}
	return &TerminalExecResult{State: t.State(), Output: formatSQLResult(res)}, nil
}

// ---------------------------------------------------------------- MS SQL Server (sqlcmd) interactive shell

func (t *Terminal) execMSSQLMode(line string) (*TerminalExecResult, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(line, ";"))
	low := strings.ToLower(trimmed)
	switch {
	case low == "exit" || low == "quit" || low == `\q`:
		return t.SetMode("shell")
	case low == "status" || low == `\conninfo`:
		es := t.st.Engine("mssql")
		t.mu.Lock()
		db := t.sqlDB["mssql"]
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{
			State:  state,
			Output: fmt.Sprintf("MS SQL Server  %s  current database: %s", mssqlServerTarget(es), db),
		}, nil
	case low == "show databases" || low == "sp_databases":
		line = "SELECT name AS [Database], database_id AS [ID], create_date AS [Created] FROM sys.databases ORDER BY database_id;"
	case low == "show tables" || low == "sp_tables":
		line = "SELECT TABLE_SCHEMA AS [Schema], TABLE_NAME AS [Table], TABLE_TYPE AS [Type] FROM INFORMATION_SCHEMA.TABLES ORDER BY 1, 2;"
	case strings.HasPrefix(low, "use "):
		dbName := strings.Trim(strings.TrimSpace(trimmed[3:]), "[]\"' ")
		es := t.st.Engine("mssql")
		if _, err := mssqlRunQuery(es, dbName, "SELECT 1;"); err != nil {
			return &TerminalExecResult{State: t.State(), Output: "ERROR: " + err.Error()}, nil
		}
		t.mu.Lock()
		t.sqlDB["mssql"] = dbName
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{State: state, Output: fmt.Sprintf("Changed database context to '%s'.", dbName)}, nil
	}

	t.mu.Lock()
	activeDB := t.sqlDB["mssql"]
	t.mu.Unlock()

	res, err := mssqlRunQuery(t.st.Engine("mssql"), activeDB, line)
	if err != nil {
		return &TerminalExecResult{State: t.State(), Output: "ERROR: " + err.Error()}, nil
	}
	return &TerminalExecResult{State: t.State(), Output: formatSQLResult(res)}, nil
}

// ---------------------------------------------------------------- Redis CLI interactive shell

func (t *Terminal) execRedisMode(line string) (*TerminalExecResult, error) {
	fields, err := splitArgs(line)
	if err != nil {
		return &TerminalExecResult{State: t.State(), Output: "(error) " + err.Error()}, nil
	}
	if len(fields) == 0 {
		return &TerminalExecResult{State: t.State()}, nil
	}
	verb := strings.ToLower(fields[0])
	if verb == "exit" || verb == "quit" {
		return t.SetMode("shell")
	}
	if verb == "select" && len(fields) == 2 {
		idx, err := strconv.Atoi(fields[1])
		if err != nil || idx < 0 {
			return &TerminalExecResult{State: t.State(), Output: "(error) ERR invalid DB index"}, nil
		}
		if _, err := RedisCommand(t.st.Engine("redis"), idx, "PING"); err != nil {
			return &TerminalExecResult{State: t.State(), Output: "(error) " + err.Error()}, nil
		}
		t.mu.Lock()
		t.redDB = idx
		state := t.stateLocked()
		t.mu.Unlock()
		return &TerminalExecResult{State: state, Output: "OK"}, nil
	}

	t.mu.Lock()
	dbIdx := t.redDB
	t.mu.Unlock()

	out, err := RedisCommand(t.st.Engine("redis"), dbIdx, line)
	if err != nil {
		return &TerminalExecResult{State: t.State(), Output: "(error) " + err.Error()}, nil
	}
	return &TerminalExecResult{State: t.State(), Output: out}, nil
}

// ---------------------------------------------------------------- ASCII table formatter

func formatSQLResult(res *QueryResult) string {
	if !res.IsQuery {
		return fmt.Sprintf("Query OK, %d row(s) affected (%.1f ms)", res.Affected, res.DurationMs)
	}
	if len(res.Columns) == 0 {
		return fmt.Sprintf("Empty set (%.1f ms)", res.DurationMs)
	}
	rows := make([][]string, len(res.Rows))
	for i, r := range res.Rows {
		row := make([]string, len(res.Columns))
		for j, cell := range r {
			if cell == nil {
				row[j] = "NULL"
			} else {
				s := strings.ReplaceAll(*cell, "\r\n", " ")
				s = strings.ReplaceAll(s, "\n", " ")
				if len(s) > 80 {
					s = s[:77] + "..."
				}
				row[j] = s
			}
		}
		rows[i] = row
	}
	tbl := renderASCIITable(res.Columns, rows)
	suffix := fmt.Sprintf("\n%d row(s) in set (%.1f ms)", len(res.Rows), res.DurationMs)
	if res.Truncated {
		suffix += " (capped at 1,000 rows)"
	}
	return tbl + suffix
}

func renderASCIITable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i := 0; i < len(r) && i < len(widths); i++ {
			if len(r[i]) > widths[i] {
				widths[i] = len(r[i])
			}
		}
	}
	var sep strings.Builder
	sep.WriteByte('+')
	for _, w := range widths {
		sep.WriteString(strings.Repeat("-", w+2))
		sep.WriteByte('+')
	}
	line := sep.String()

	var b strings.Builder
	b.WriteString(line + "\n|")
	for i, h := range headers {
		fmt.Fprintf(&b, " %-*s |", widths[i], h)
	}
	b.WriteString("\n" + line)
	for _, r := range rows {
		b.WriteString("\n|")
		for i, w := range widths {
			val := ""
			if i < len(r) {
				val = r[i]
			}
			fmt.Fprintf(&b, " %-*s |", w, val)
		}
	}
	b.WriteString("\n" + line)
	return b.String()
}
