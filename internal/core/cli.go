package core

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	CLIAppTitle        = "Darius DB (Lite Version 0.1.5)"
	CLIAppVersion      = "0.1.5"
	CLIAppAuthor       = "Darius Cumpio"
	CLIAppCreated      = "November 19, 2026"
	CLIAppRepo         = "https://github.com/dcquijano/Darius-DB-Lite-Version"
	CLIAppInstallerURL = "https://drive.google.com/drive/folders/1FVLCHqOfxmP9PbSx70jy_BfH2Yn1E_UG?usp=sharing"
)

// CLIAppBuild identifies the build timestamp (overridden via -ldflags at build time).
var CLIAppBuild = "202611261347"

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
		fmt.Printf("%s (build %s)\nCreated by: %s\nCreated:    %s\nLicense:    MIT Open Source\nRepository: %s\nInstaller:  %s\nHome:       %s\n",
			CLIAppTitle, CLIAppBuild, CLIAppAuthor, CLIAppCreated, CLIAppRepo, CLIAppInstallerURL, root)
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

	case "restore", "import":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: darius restore <mssql|mariadb|mysql|postgresql> <file.bak|file.sql> [target-db]")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		file := rest[1]
		targetDB := ""
		if len(rest) > 2 {
			targetDB = rest[2]
		}
		dbName, err := RestoreDatabase(paths, eng, st.Engine(eng), file, targetDB)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Printf("Successfully restored %q into database %q on %s.\n", filepath.Base(file), dbName, eng)
		return 0

	case "backup", "export", "dump":
		if len(rest) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: darius backup <mssql|mariadb|mysql|postgresql> <dbname> <output.bak|output.sql>")
			return 1
		}
		eng := normalizeEngineArg(rest[0])
		dbName := rest[1]
		outFile := rest[2]
		if err := BackupDatabase(paths, eng, st.Engine(eng), dbName, outFile); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Printf("Successfully backed up %q (%s) to %s.\n", dbName, eng, outFile)
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

	case "repair", "fix", "doctor":
		target := "all"
		resetCfg := false
		fixPorts := false
		cleanCache := true
		for _, a := range rest {
			low := strings.ToLower(a)
			switch low {
			case "--reset-config", "--reset", "-r":
				resetCfg = true
			case "--fix-ports", "--ports", "-p":
				fixPorts = true
			case "--deep", "--system":
				fixPorts = true
				cleanCache = true
			case "--clean-cache", "--cache":
				cleanCache = true
			case "--no-cache":
				cleanCache = false
			default:
				target = normalizeEngineArg(a)
			}
		}
		rep, err := mgr.RepairWithOptions(RepairOptions{
			Target:       target,
			ResetConfigs: resetCfg,
			FixPorts:     fixPorts,
			CleanCache:   cleanCache,
			DeepSystem:   true,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		dirs := append([]string{root}, mgr.ClientPaths()...)
		_ = UpdateUserPath(dirs, true)
		fmt.Printf("%s — Self-Repair & Diagnostics (%s)\n\n", CLIAppTitle, rep.CheckedAt)
		rows := [][]string{}
		for _, it := range rep.Items {
			badge := "OK"
			if it.Status == "fixed" {
				badge = "FIXED"
			} else if it.Status == "warn" {
				badge = "WARN"
			}
			rows = append(rows, []string{badge, it.Category, it.Action})
		}
		fmt.Println(renderASCIITable([]string{"STATUS", "COMPONENT", "DIAGNOSTIC / ACTION"}, rows))
		fmt.Println("\n" + rep.Summary)
		return 0

	case "system-update", "self-update":
		tok := st.Get().GitHubToken
		autoApply := false
		var targetArg string
		for _, a := range rest {
			low := strings.ToLower(strings.TrimSpace(a))
			switch {
			case low == "--apply" || low == "--install" || low == "--auto":
				autoApply = true
			case strings.HasPrefix(low, "--token="):
				tok = strings.TrimSpace(a[8:])
			default:
				if targetArg == "" {
					targetArg = strings.TrimSpace(a)
				}
			}
		}
		if targetArg != "" {
			var msg string
			var err error
			if strings.HasPrefix(strings.ToLower(targetArg), "http://") || strings.HasPrefix(strings.ToLower(targetArg), "https://") {
				msg, err = ApplySystemUpdateFromURL(paths, targetArg, tok)
			} else {
				msg, err = ApplySystemUpdateFromFile(paths, targetArg)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 1
			}
			fmt.Println(msg)
			return 0
		}
		up, err := SyncFromGitHub(paths, CLIAppVersion, tok)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Printf("%s — System Version Updater (Live GitHub Sync)\n", CLIAppTitle)
		fmt.Printf("Installed Version: Lite Version %s\n", up.CurrentVersion)
		fmt.Printf("Latest Version:    Lite Version %s\n", up.LatestVersion)
		if up.LatestCommitSHA != "" {
			fmt.Printf("Latest GitHub:     [%s] %s (%s)\n", up.LatestCommitSHA, up.LatestCommitMsg, up.LatestCommitDate)
		}
		if !up.FetchOK {
			fmt.Printf("Status:            FETCH FAILED — %s\n", up.FetchError)
			fmt.Printf("Tip:               Run 'darius system-update --token=<YOUR_GITHUB_PAT>' or make the GitHub repo Public.\n")
			return 1
		}
		if up.HasUpdate {
			fmt.Printf("Status:            UPDATE AVAILABLE (v%s)\n", up.LatestVersion)
			if autoApply {
				msg, err := ApplySystemUpdateWithFallback(paths, up.DirectSetupURL, up.FallbackSetupURL, tok)
				if err != nil {
					fmt.Fprintln(os.Stderr, "Error applying update:", err)
					return 1
				}
				fmt.Println(msg)
				return 0
			}
			fmt.Printf("Install Update:    darius system-update --apply\n")
		} else {
			fmt.Printf("Status:            UP TO DATE & SYNCED WITH GITHUB (%s)\n", up.FetchSource)
		}
		if up.DirectSetupURL != "" {
			fmt.Printf("Direct Setup URL:  %s\n", up.DirectSetupURL)
		}
		fmt.Printf("Installer Folder:  %s\n\n", up.InstallerURL)
		fmt.Printf("What's New (Fetched from GitHub):\n")
		for _, item := range up.Changelog {
			fmt.Printf("  • %s\n", item)
		}
		return 0

	case "schedule", "schedules", "auto-backup", "auto-sync":
		sub := "list"
		if len(rest) > 0 {
			sub = strings.ToLower(strings.TrimLeft(rest[0], "-"))
		}
		sc := NewScheduler(paths, st, mgr, nil, nil)
		switch sub {
		case "run":
			if len(rest) > 1 && strings.ToLower(rest[1]) != "due" {
				targetID := rest[1]
				for _, item := range st.Get().Schedules {
					s := item.Normalized()
					if !s.Enabled {
						continue
					}
					if strings.EqualFold(targetID, "all") || strings.EqualFold(targetID, s.ID) || strings.EqualFold(targetID, s.Name) {
						msg, err := sc.RunScheduleNow(s.ID, "both")
						if err != nil {
							fmt.Fprintf(os.Stderr, "[%s] Error: %v\n", s.Name, err)
							return 1
						}
						fmt.Printf("[%s] %s\n", s.Name, msg)
					}
				}
				return 0
			}
			sc.RunDueSchedules(time.Now())
			fmt.Println("Checked and executed all due Auto-Sync / Auto-Backup schedules.")
			return 0
		case "purge":
			total := 0
			for _, item := range st.Get().Schedules {
				s := item.Normalized()
				n, _, _ := PurgeScheduleAllDirs(paths, s, time.Now())
				total += n
			}
			fmt.Printf("Purged %d expired backup snapshot(s).\n", total)
			return 0
		default:
			scheds := st.Get().Schedules
			if len(scheds) == 0 {
				fmt.Println("No Auto-Sync / Auto-Backup schedules configured.")
				return 0
			}
			for _, raw := range scheds {
				s := raw.Normalized()
				fmt.Printf("- [%s] %s | sync=%v (%s @ %s) | backup=%v (@ %s, keep %dd) | lastSync=%s | lastBackup=%s\n",
					s.ID, s.Name, s.AutoSync, s.SyncDirection, s.SyncTime, s.AutoBackup, s.BackupTime, s.RetentionDays, s.LastSyncAt, s.LastBackupAt)
			}
			return 0
		}

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
	case "mail", "smtp":
		return "mailpit"
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
	host := fallbackStr(strings.TrimSpace(es.Host), "127.0.0.1")
	isRemote := !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost")
	switch engine {
	case "apache", "php":
		return fmt.Sprintf("http://localhost:%d/", es.Port), nil
	case "mailpit":
		return fmt.Sprintf("smtp://127.0.0.1:1025 (Web Inbox: http://localhost:%d/)", es.Port), nil
	case "mariadb", "mysql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		if dbName == "" && es.DefaultDB != "" {
			dbName = es.DefaultDB
		}
		return fmt.Sprintf("mysql://%s@%s:%d/%s", auth, host, es.Port, dbName), nil
	case "postgresql":
		auth := url.User(es.User).String()
		if es.Password != "" {
			auth = url.UserPassword(es.User, es.Password).String()
		}
		if dbName == "" {
			if es.DefaultDB != "" {
				dbName = es.DefaultDB
			} else {
				dbName = "postgres"
			}
		}
		q := ""
		if es.SSLMode != "" && es.SSLMode != "none" && es.SSLMode != "disable" {
			q = "?sslmode=" + url.QueryEscape(es.SSLMode)
		}
		return fmt.Sprintf("postgresql://%s@%s:%d/%s%s", auth, host, es.Port, dbName, q), nil
	case "mssql":
		if dbName == "" {
			if es.DefaultDB != "" {
				dbName = es.DefaultDB
			} else {
				dbName = "master"
			}
		}
		if isRemote {
			srv := host
			if es.Port > 0 {
				srv = fmt.Sprintf("%s,%d", host, es.Port)
			}
			return fmt.Sprintf("Server=%s;Database=%s;User Id=%s;Password=%s;Encrypt=True;TrustServerCertificate=True;", srv, dbName, es.User, es.Password), nil
		}
		srv := MSSQLServerName()
		return fmt.Sprintf("Server=%s;Database=%s;Trusted_Connection=True;TrustServerCertificate=True;", srv, dbName), nil
	case "redis":
		scheme := "redis"
		if es.SSLMode == "require" || es.SSLMode == "skip-verify" {
			scheme = "rediss"
		}
		if es.Password != "" {
			if es.User != "" {
				return fmt.Sprintf("%s://%s:%s@%s:%d/0", scheme, url.QueryEscape(es.User), url.QueryEscape(es.Password), host, es.Port), nil
			}
			return fmt.Sprintf("%s://:%s@%s:%d/0", scheme, url.QueryEscape(es.Password), host, es.Port), nil
		}
		return fmt.Sprintf("%s://%s:%d/0", scheme, host, es.Port), nil
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
	host := fallbackStr(strings.TrimSpace(es.Host), "127.0.0.1")
	isRemote := !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost")
	var b strings.Builder
	fmt.Fprintf(&b, "=== %s Connection Configs for Visual Studio, VS Code & CLI ===\n\n", strings.ToUpper(engine))

	switch engine {
	case "apache":
		fmt.Fprintf(&b, "[Local Web Server URL]\n%s\n\n", uri)
		fmt.Fprintf(&b, "[VS Code / .env]\nAPP_URL=%q\nPORT=%d\n", strings.TrimRight(uri, "/"), es.Port)

	case "mssql":
		srv := MSSQLServerName()
		if isRemote {
			srv = host
		}
		dotnet := uri
		fmt.Fprintf(&b, "[MS SQL Server Connection Info]\n")
		if isRemote {
			fmt.Fprintf(&b, "Server Name: %s\nAuth Mode:   SQL Server Authentication\nPort:        %d\nUser:        %s\n\n", srv, es.Port, es.User)
		} else {
			fmt.Fprintf(&b, "Server Name: %s\nAuth Mode:   Windows Authentication (Trusted_Connection)\nPort:        %d\n\n", srv, es.Port)
		}
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"DefaultConnection\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env (Prisma / Node / Python)]\n")
		if isRemote {
			fmt.Fprintf(&b, "DATABASE_URL=\"sqlserver://%s:%d;database=%s;user=%s;password=%s;encrypt=true;trustServerCertificate=true\"\n\n", srv, es.Port, dbName, es.User, es.Password)
		} else {
			fmt.Fprintf(&b, "DATABASE_URL=\"sqlserver://%s:%d;database=%s;integratedSecurity=true;trustServerCertificate=true\"\n\n", srv, es.Port, dbName)
		}
		fmt.Fprintf(&b, "[Terminal / Package Manager Console]\n")
		fmt.Fprintf(&b, "darius sql mssql \"SELECT @@VERSION\" -d %s\n", dbName)

	case "mariadb", "mysql":
		dotnet := fmt.Sprintf("Server=%s;Port=%d;Database=%s;Uid=%s;Pwd=%s;", host, es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"DefaultConnection\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env (Node / Prisma / Python / Laravel)]\n")
		fmt.Fprintf(&b, "DATABASE_URL=%q\n", uri)
		fmt.Fprintf(&b, "DB_CONNECTION=mysql\nDB_HOST=%s\nDB_PORT=%d\nDB_DATABASE=%s\nDB_USERNAME=%s\nDB_PASSWORD=%s\n\n",
			host, es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Terminal / CLI]\n")
		fmt.Fprintf(&b, "darius sql %s \"SHOW TABLES;\" -d %s\n", engine, dbName)

	case "postgresql":
		dotnet := fmt.Sprintf("Host=%s;Port=%d;Database=%s;Username=%s;Password=%s;", host, es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json (Npgsql)]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"DefaultConnection\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env (Node / Prisma / Python / Laravel)]\n")
		fmt.Fprintf(&b, "DATABASE_URL=%q\n", uri)
		fmt.Fprintf(&b, "DB_CONNECTION=pgsql\nDB_HOST=%s\nDB_PORT=%d\nDB_DATABASE=%s\nDB_USERNAME=%s\nDB_PASSWORD=%s\n\n",
			host, es.Port, dbName, es.User, es.Password)
		fmt.Fprintf(&b, "[Terminal / CLI]\n")
		fmt.Fprintf(&b, "darius sql postgresql \"SELECT version();\" -d %s\n", dbName)

	case "redis":
		dotnet := fmt.Sprintf("%s:%d", host, es.Port)
		if es.Password != "" {
			dotnet += ",password=" + es.Password
		}
		fmt.Fprintf(&b, "[Visual Studio / .NET appsettings.json (StackExchange.Redis)]\n")
		fmt.Fprintf(&b, "{\n  \"ConnectionStrings\": {\n    \"Redis\": %q\n  }\n}\n\n", dotnet)
		fmt.Fprintf(&b, "[VS Code / .env]\n")
		fmt.Fprintf(&b, "REDIS_URL=%q\nREDIS_HOST=%s\nREDIS_PORT=%d\n\n", uri, host, es.Port)
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
  darius restore <engine> <file.bak|.sql> [db]   Restore a .bak backup or import a .sql script
  darius backup <engine> <db> <out.bak|.sql>     Back up a database to a .bak or .sql file
  darius sql <engine> "<query>" [-d <database>]  Run SQL / T-SQL / Redis command & print ASCII table
  darius versions [engine]                       List installed & downloadable engine versions
  darius install <engine> [version]              Download & install a portable engine version
  darius use <engine> <version>                  Switch active version of an engine
  darius repair [all|<engine>] [--reset-config] [--fix-ports]  Full system repair (folders, configs, locks, ports, caches & PATH)
  darius system-update [--apply] [--token=PAT]   Check GitHub & install Darius DB system updates
  darius path [--add|--remove]                   Register Darius DB CLI & DB tools in Windows PATH
  darius gui                                     Open or focus the Darius DB desktop GUI
  darius version                                 Show version & credits
  darius help                                    Show this help message
`, CLIAppTitle, CLIAppAuthor)
}
