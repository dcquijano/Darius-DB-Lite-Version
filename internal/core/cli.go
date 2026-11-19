package core

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	CLIAppTitle   = "Darius DB (Lite Version 0.1.2)"
	CLIAppVersion = "0.1.2"
	CLIAppAuthor  = "Darius Cumpio"
	CLIAppCreated = "November 19, 2026"
	CLIAppRepo    = "https://github.com/dcquijano/Darius-DB-Lite-Version"
)

// RunCLI executes a CLI subcommand and returns an process exit code.
func RunCLI(root string, args []string) int {
	paths := NewPaths(root)
	_ = paths.Ensure()
	st := LoadSettings(root)
	mgr := NewManager(paths, st, nil)
	term := NewTerminal(paths, st, mgr, nil)

	if len(args) == 0 {
		printCLIHelp()
		return 0
	}

	cmd := strings.ToLower(strings.TrimLeft(args[0], "-"))
	rest := args[1:]

	switch cmd {
	case "help", "h", "?":
		printCLIHelp()
		return 0

	case "version", "v":
		fmt.Printf("%s\nCreated by: %s\nCreated:    %s\nLicense:    MIT Open Source\nRepository: %s\nHome:       %s\n",
			CLIAppTitle, CLIAppAuthor, CLIAppCreated, CLIAppRepo, root)
		return 0

	case "status", "ps", "services":
		res, _ := term.Exec("status")
		fmt.Println(res.Output)
		return 0

	case "start", "stop", "restart":
		target := "all"
		if len(rest) > 0 {
			target = strings.ToLower(rest[0])
		}
		if cmd == "restart" && target == "all" {
			_ = mgr.StopAll()
			if err := mgr.StartAll(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 1
			}
			res, _ := term.Exec("status")
			fmt.Println(res.Output)
			return 0
		}
		res, err := term.Exec(cmd + " " + target)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Println(res.Output)
		if strings.HasPrefix(res.Output, "Error:") {
			return 1
		}
		return 0

	case "uri", "url", "conn":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: darius uri <mariadb|mysql|postgresql|mssql|redis> [database]")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		db := ""
		if len(rest) > 1 {
			db = rest[1]
		}
		uri, err := buildConnectionURI(eng, st.Engine(eng), db)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Println(uri)
		return 0

	case "env", "vs", "vscode", "config-snippet":
		eng := "mariadb"
		if len(rest) > 0 {
			eng = normalizeEngineArg(rest[0])
		}
		db := "my_app"
		if len(rest) > 1 {
			db = rest[1]
		}
		s, err := buildIDECredentialsSnippet(eng, st.Engine(eng), db)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Println(s)
		return 0

	case "dbs", "databases":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: darius dbs <mariadb|mysql|postgresql|mssql|redis>")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		if eng == "redis" {
			dbs, err := RedisDatabases(st.Engine("redis"))
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 1
			}
			rows := [][]string{}
			for _, d := range dbs {
				rows = append(rows, []string{fmt.Sprintf("db%d", d.Index), strconv.Itoa(d.Keys)})
			}
			fmt.Println(renderASCIITable([]string{"DATABASE", "KEYS"}, rows))
			return 0
		}
		dbs, err := ListDatabases(eng, st.Engine(eng))
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		rows := make([][]string, len(dbs))
		for i, d := range dbs {
			rows[i] = []string{d}
		}
		fmt.Println(renderASCIITable([]string{"DATABASE (" + eng + ")"}, rows))
		return 0

	case "tables":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: darius tables <mariadb|mysql|postgresql|mssql> <database>")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		db := rest[1]
		tables, err := ListTables(eng, st.Engine(eng), db)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		rows := make([][]string, len(tables))
		for i, t := range tables {
			rows[i] = []string{t.Schema, t.Name, t.Kind}
		}
		fmt.Println(renderASCIITable([]string{"SCHEMA", "TABLE / VIEW", "TYPE"}, rows))
		return 0

	case "createdb":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: darius createdb <mariadb|mysql|postgresql|mssql> <dbname> [username] [password]")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		db := rest[1]
		user, pass := "", ""
		if len(rest) > 2 {
			user = rest[2]
		}
		if len(rest) > 3 {
			pass = rest[3]
		}
		if err := CreateDatabase(eng, st.Engine(eng), db, user, pass); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		if user != "" {
			fmt.Printf("Created database %q and user %q on %s.\n", db, user, eng)
		} else {
			fmt.Printf("Created database %q on %s.\n", db, eng)
		}
		return 0

	case "dropdb":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: darius dropdb <mariadb|mysql|postgresql|mssql> <dbname>")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		db := rest[1]
		if err := DropDatabase(eng, st.Engine(eng), db); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Printf("Dropped database %q on %s.\n", db, eng)
		return 0

	case "sql", "query", "exec":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, `Usage: darius sql <mariadb|mysql|postgresql|mssql|redis> "<query>" [-d <database>]`)
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		db := ""
		var qParts []string
		for i := 1; i < len(rest); i++ {
			if (rest[i] == "-d" || rest[i] == "--db") && i+1 < len(rest) {
				db = rest[i+1]
				i++
				continue
			}
			qParts = append(qParts, rest[i])
		}
		query := strings.Join(qParts, " ")
		if eng == "redis" {
			dbIdx, _ := strconv.Atoi(db)
			out, err := RedisCommand(st.Engine("redis"), dbIdx, query)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 1
			}
			fmt.Println(out)
			return 0
		}
		res, err := RunQuery(eng, st.Engine(eng), db, query)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Println(formatSQLResult(res))
		return 0

	case "update", "check-updates", "refresh":
		fmt.Println("Checking official upstream releases for newest database versions...")
		cat, err := RefreshCatalog(paths, st.Get().CatalogURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		var rows [][]string
		for _, id := range EngineIDs {
			if entries := cat[id]; len(entries) > 0 {
				rows = append(rows, []string{id, entries[0].Version, entries[0].Note, entries[0].URL})
			}
		}
		fmt.Println(renderASCIITable([]string{"ENGINE", "NEWEST VERSION", "CHANNEL", "DOWNLOAD URL"}, rows))
		return 0

	case "versions", "list", "catalog":
		cat := LoadCatalog(paths)
		filter := ""
		if len(rest) > 0 {
			filter = normalizeEngineArg(rest[0])
		}
		var rows [][]string
		for _, id := range EngineIDs {
			if filter != "" && id != filter {
				continue
			}
			es := st.Engine(id)
			insts := ListInstallations(paths, id)
			instMap := map[string]bool{}
			for _, in := range insts {
				instMap[in.Version] = true
				status := "installed"
				if in.Name == es.Version {
					status = "ACTIVE"
				}
				rows = append(rows, []string{id, in.Version, in.Name, status})
			}
			for _, ce := range cat[id] {
				if !instMap[ce.Version] {
					note := "available to download"
					if ce.Note != "" {
						note += " (" + ce.Note + ")"
					}
					rows = append(rows, []string{id, ce.Version, id + "-" + ce.Version, note})
				}
			}
		}
		fmt.Println(renderASCIITable([]string{"ENGINE", "VERSION", "NAME", "STATUS"}, rows))
		return 0

	case "install", "download":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: darius install <mariadb|mysql|postgresql|mssql|redis> [version]")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		cat := LoadCatalog(paths)
		entries := cat[eng]
		if len(entries) == 0 {
			fmt.Fprintf(os.Stderr, "No catalog entries found for %s\n", eng)
			return 1
		}
		chosen := entries[0]
		if len(rest) > 1 {
			ver := rest[1]
			found := false
			for _, e := range entries {
				if e.Version == ver || eng+"-"+e.Version == ver {
					chosen = e
					found = true
					break
				}
			}
			if !found {
				fmt.Fprintf(os.Stderr, "Version %q not found in %s catalog.\n", ver, eng)
				return 1
			}
		}
		done := make(chan Progress, 1)
		dl := NewDownloader(paths, func(p Progress) {
			switch p.Phase {
			case "downloading", "extracting":
				if p.Total > 0 {
					pct := int(float64(p.Received) / float64(p.Total) * 100)
					fmt.Fprintf(os.Stdout, "\r%-12s %s-%s: %3d%% (%d / %d MB)   ",
						p.Phase, p.Engine, p.Version, pct, p.Received/(1024*1024), p.Total/(1024*1024))
				}
			case "done", "error", "cancelled":
				fmt.Println()
				done <- p
			}
		}, func(engine, name string) {
			mgr.OnInstalled(engine, name)
		})
		fmt.Printf("Installing %s %s from %s...\n", eng, chosen.Version, chosen.URL)
		if err := dl.Install(eng, chosen.Version, chosen.URL); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		res := <-done
		if res.Phase != "done" {
			fmt.Fprintln(os.Stderr, "Error:", res.Message)
			return 1
		}
		fmt.Println("Successfully installed:", res.Message)
		return 0

	case "use", "switch":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: darius use <engine> <version-or-folder-name>")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		name := rest[1]
		if !strings.HasPrefix(name, eng+"-") {
			if _, ok := FindInstallation(paths, eng, eng+"-"+name); ok {
				name = eng + "-" + name
			}
		}
		if err := mgr.SetActive(eng, name); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Printf("Active %s version set to %s.\n", eng, name)
		return 0

	case "path":
		action := "show"
		if len(rest) > 0 {
			action = strings.ToLower(strings.TrimLeft(rest[0], "-"))
		}
		switch action {
		case "add", "install", "register":
			dirs := append([]string{root}, mgr.ClientPaths()...)
			if err := UpdateUserPath(dirs, true); err != nil {
				fmt.Fprintln(os.Stderr, "Error updating Windows PATH:", err)
				return 1
			}
			fmt.Println("Added Darius DB CLI and active database tools to your Windows User PATH:")
			for _, d := range dirs {
				fmt.Println("  +", d)
			}
			fmt.Println("\nRestart your Visual Studio / VS Code terminal to use 'darius', 'mariadb', 'mysql', 'psql', 'sqlcmd', and 'redis-cli' anywhere.")
			return 0
		case "remove", "uninstall":
			dirs := append([]string{root}, mgr.ClientPaths()...)
			if err := UpdateUserPath(dirs, false); err != nil {
				fmt.Fprintln(os.Stderr, "Error updating Windows PATH:", err)
				return 1
			}
			fmt.Println("Removed Darius DB directories from Windows User PATH.")
			return 0
		default:
			fmt.Println("Darius DB root & active client bin directories:")
			fmt.Println("  ", root)
			for _, d := range mgr.ClientPaths() {
				fmt.Println("  ", d)
			}
			fmt.Println("\nRun 'darius path --add' to register these in your Windows PATH for Visual Studio & VS Code.")
			return 0
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q. Run 'darius help' for usage.\n", args[0])
		return 1
	}
}

func normalizeEngineArg(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "httpd", "web":
		return "apache"
	case "postgres", "pg", "psql":
		return "postgresql"
	case "sqlserver", "sqlcmd", "ms":
		return "mssql"
	case "maria":
		return "mariadb"
	default:
		return s
	}
}

func buildConnectionURI(engine string, es EngineSettings, dbName string) (string, error) {
	switch engine {
	case "apache":
		return fmt.Sprintf("http://localhost:%d/", es.Port), nil
	case "mariadb", "mysql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		return fmt.Sprintf("mysql://%s@127.0.0.1:%d/%s", auth, es.Port, dbName), nil
	case "postgresql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		if dbName == "" {
			dbName = "postgres"
		}
		return fmt.Sprintf("postgresql://%s@127.0.0.1:%d/%s", auth, es.Port, dbName), nil
	case "mssql":
		if dbName == "" {
			dbName = "master"
		}
		if es.User != "" && es.Password != "" {
			return fmt.Sprintf("Server=127.0.0.1,%d;Database=%s;User Id=%s;Password=%s;TrustServerCertificate=True;", es.Port, dbName, es.User, es.Password), nil
		}
		return fmt.Sprintf("Server=(localdb)\\MSSQLLocalDB;Database=%s;Trusted_Connection=True;", dbName), nil
	case "redis":
		if es.Password != "" {
			return fmt.Sprintf("redis://:%s@127.0.0.1:%d/0", url.QueryEscape(es.Password), es.Port), nil
		}
		return fmt.Sprintf("redis://127.0.0.1:%d/0", es.Port), nil
	default:
		return "", fmt.Errorf("unknown engine %q (choose mariadb, mysql, postgresql, mssql, or redis)", engine)
	}
}

// IDESnippet returns ready-to-paste Visual Studio appsettings.json and VS Code .env blocks.
func IDESnippet(engine string, es EngineSettings, dbName string) (string, error) {
	return buildIDECredentialsSnippet(engine, es, dbName)
}

func buildIDECredentialsSnippet(engine string, es EngineSettings, dbName string) (string, error) {
	uri, err := buildConnectionURI(engine, es, dbName)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "=== %s Connection Configs for Visual Studio, VS Code & CLI ===\n\n", strings.ToUpper(engine))

	switch engine {
	case "apache":
		fmt.Fprintf(&b, "[Local Web Server URL]\n%s\n\n", uri)
		fmt.Fprintf(&b, "[VS Code / .env]\nAPP_URL=%q\nPORT=%d\n", strings.TrimRight(uri, "/"), es.Port)

	case "mssql":
		dotnet := uri
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"DefaultConnection\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env (Prisma / Node / Python)]\n")
		if es.User != "" && es.Password != "" {
			fmt.Fprintf(&b, "DATABASE_URL=\"sqlserver://127.0.0.1:%d;database=%s;user=%s;password=%s;trustServerCertificate=true\"\n\n", es.Port, dbName, es.User, es.Password)
		} else {
			fmt.Fprintf(&b, "DATABASE_URL=\"sqlserver://localhost:%d;database=%s;integratedSecurity=true;trustServerCertificate=true\"\n\n", es.Port, dbName)
		}
		fmt.Fprintf(&b, "[Terminal / Package Manager Console]\n")
		fmt.Fprintf(&b, "darius sql mssql \"SELECT @@VERSION\" -d %s\n", dbName)

	case "mariadb", "mysql":
		dotnet := fmt.Sprintf("Server=127.0.0.1;Port=%d;Database=%s;Uid=%s;Pwd=%s;", es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"DefaultConnection\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env (Node / Prisma / Python / Laravel)]\n")
		fmt.Fprintf(&b, "DATABASE_URL=%q\n", uri)
		fmt.Fprintf(&b, "DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=%s\nDB_USERNAME=%s\nDB_PASSWORD=%s\n\n",
			es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Terminal / CLI]\n")
		fmt.Fprintf(&b, "darius sql %s \"SHOW TABLES;\" -d %s\n", engine, dbName)

	case "postgresql":
		dotnet := fmt.Sprintf("Host=127.0.0.1;Port=%d;Database=%s;Username=%s;Password=%s;", es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json (Npgsql)]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"DefaultConnection\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env (Node / Prisma / Python / Laravel)]\n")
		fmt.Fprintf(&b, "DATABASE_URL=%q\n", uri)
		fmt.Fprintf(&b, "DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=%s\nDB_USERNAME=%s\nDB_PASSWORD=%s\n\n",
			es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Terminal / CLI]\n")
		fmt.Fprintf(&b, "darius sql postgresql \"SELECT version();\" -d %s\n", dbName)

	case "redis":
		dotnet := fmt.Sprintf("127.0.0.1:%d", es.Port)
		if es.Password != "" {
			dotnet += ",password=" + es.Password
		}
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json (StackExchange.Redis)]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"Redis\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env]\n")
		fmt.Fprintf(&b, "REDIS_URL=%q\nREDIS_HOST=127.0.0.1\nREDIS_PORT=%d\n\n", uri, es.Port)
		fmt.Fprintf(&b, "[Terminal / CLI]\n")
		fmt.Fprintf(&b, "darius sql redis \"PING\"\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// UpdateUserPath adds or removes dirs from HKCU\Environment\Path and broadcasts WM_SETTINGCHANGE.
func UpdateUserPath(dirs []string, add bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	cur, _, _ := k.GetStringValue("Path")
	parts := strings.Split(cur, ";")

	norm := func(p string) string {
		return strings.ToLower(strings.TrimRight(filepath.Clean(strings.TrimSpace(p)), `\/`))
	}
	targetSet := map[string]string{}
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d != "" {
			targetSet[norm(d)] = filepath.Clean(d)
		}
	}

	var kept []string
	present := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		np := norm(p)
		if _, isTarget := targetSet[np]; isTarget {
			if !add {
				continue
			}
			present[np] = true
		}
		kept = append(kept, p)
	}
	if add {
		var toPrepend []string
		for _, d := range dirs {
			np := norm(d)
			if np != "" && !present[np] {
				toPrepend = append(toPrepend, filepath.Clean(d))
				present[np] = true
			}
		}
		kept = append(toPrepend, kept...)
	}

	newPath := strings.Join(kept, ";")
	if err := k.SetExpandStringValue("Path", newPath); err != nil {
		return err
	}
	broadcastEnvChange()
	return nil
}

func broadcastEnvChange() {
	procSendMessageTimeoutW := user32Sys.NewProc("SendMessageTimeoutW")
	envPtr, _ := windows.UTF16PtrFromString("Environment")
	const (
		hwndBroadcast   = 0xFFFF
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	procSendMessageTimeoutW.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(envPtr)),
		smtoAbortIfHung,
		1000,
		0,
	)
}

func printCLIHelp() {
	fmt.Printf(`%s — Developer CLI for Visual Studio, VS Code & Terminals
Created by %s (MIT License)

Usage:
  darius status                                  Show all engines, states, versions, ports & PIDs
  darius start [all|mariadb|mysql|postgresql|mssql|redis]
  darius stop  [all|mariadb|mysql|postgresql|mssql|redis]
  darius restart <engine>                        Restart a database engine
  darius uri <engine> [database]                 Print connection URI / ADO.NET connection string
  darius env <engine> [database]                 Print Visual Studio (appsettings.json) & VS Code (.env) configs
  darius dbs <engine>                            List all databases on a running engine
  darius tables <engine> <database>              List all tables & views in a database
  darius createdb <engine> <db> [user] [pass]    Create a database (and optional user/login)
  darius dropdb <engine> <db>                    Drop a database
  darius sql <engine> "<query>" [-d <database>]  Run SQL / T-SQL / Redis command & print ASCII table
  darius versions [engine]                       List installed & downloadable engine versions
  darius install <engine> [version]              Download & install a portable engine version
  darius use <engine> <version>                  Switch active version of an engine
  darius path [--add|--remove]                   Register Darius DB CLI & DB tools in Windows PATH
  darius gui                                     Open or focus the Darius DB desktop GUI
  darius version                                 Show version & credits
  darius help                                    Show this help message
`, CLIAppTitle, CLIAppAuthor)
}
