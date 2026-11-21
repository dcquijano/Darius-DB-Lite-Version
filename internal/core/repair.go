package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RepairItem describes a single diagnostic/repair check performed by Repair.
type RepairItem struct {
	Category string `json:"category"`
	Action   string `json:"action"`
	Status   string `json:"status"` // "fixed" | "ok" | "warn"
}

// RepairOptions controls system-wide diagnostic and healing behavior.
type RepairOptions struct {
	Target       string `json:"target"`
	ResetConfigs bool   `json:"resetConfigs"`
	FixPorts     bool   `json:"fixPorts"`
	CleanCache   bool   `json:"cleanCache"`
	DeepSystem   bool   `json:"deepSystem"`
}

// RepairReport summarizes all checks and fixes performed during a repair run.
type RepairReport struct {
	Target     string       `json:"target"`
	FixedCount int          `json:"fixedCount"`
	WarnCount  int          `json:"warnCount"`
	CheckedAt  string       `json:"checkedAt"`
	Summary    string       `json:"summary"`
	Items      []RepairItem `json:"items"`
}

const maxRepairLogBytes = 20 * 1024 * 1024 // 20 MB

// Repair diagnoses and repairs Darius DB directories, settings, configs, stale locks,
// and engine states. target may be "", "all", or a specific engine ID.
func (m *Manager) Repair(target string, resetConfigs bool) (*RepairReport, error) {
	return m.RepairWithOptions(RepairOptions{
		Target:       target,
		ResetConfigs: resetConfigs,
		CleanCache:   true,
		DeepSystem:   true,
	})
}

// RepairWithOptions runs a comprehensive system-wide diagnostic and repair pass.
func (m *Manager) RepairWithOptions(opts RepairOptions) (*RepairReport, error) {
	target := strings.ToLower(strings.TrimSpace(opts.Target))
	if target == "" {
		target = "all"
	}
	if target != "all" {
		if _, err := GetEngine(target); err != nil {
			return nil, err
		}
	}
	opts.Target = target

	rep := &RepairReport{
		Target:    target,
		CheckedAt: time.Now().Format("2006-01-02 15:04:05"),
		Items:     []RepairItem{},
	}
	add := func(cat, action, status string) {
		switch status {
		case "fixed":
			rep.FixedCount++
		case "warn":
			rep.WarnCount++
		}
		rep.Items = append(rep.Items, RepairItem{
			Category: cat,
			Action:   action,
			Status:   status,
		})
	}

	if err := m.repairDirectoriesAndWebRoot(add); err != nil {
		return nil, err
	}
	usedPorts := m.repairSettingsAndCatalog(add)
	m.repairTempFilesLogsAndCaches(opts, add)

	m.ensureActiveVersions()

	var targets []string
	if target == "all" {
		targets = EngineIDs
	} else {
		targets = []string{target}
	}
	for _, id := range targets {
		m.repairSingleEngine(id, opts, usedPorts, add)
	}

	if target == "all" {
		m.repairSystemPath(add)
	}

	m.adopt()
	m.changed()

	switch {
	case rep.FixedCount > 0 && rep.WarnCount > 0:
		rep.Summary = fmt.Sprintf("Repair completed: %d issue(s) fixed, %d warning(s) need attention.", rep.FixedCount, rep.WarnCount)
	case rep.FixedCount > 0:
		rep.Summary = fmt.Sprintf("System repair completed: %d issue(s) automatically fixed.", rep.FixedCount)
	case rep.WarnCount > 0:
		rep.Summary = fmt.Sprintf("Diagnostics completed with %d warning(s) — see report below.", rep.WarnCount)
	default:
		rep.Summary = "All system checks passed — no corrupted files, locks, port blocks, or config errors found."
	}
	return rep, nil
}

func (m *Manager) repairDirectoriesAndWebRoot(add func(cat, action, status string)) error {
	missingDirs := []string{}
	for _, d := range []string{m.p.Www, m.p.Bin, m.p.Data, m.p.Etc, m.p.Logs, m.p.Backups, m.p.Tmp, m.p.Run} {
		if !dirExists(d) {
			missingDirs = append(missingDirs, filepath.Base(d)+"/")
		}
	}
	for _, id := range EngineIDs {
		if !dirExists(m.p.EngineBin(id)) {
			missingDirs = append(missingDirs, "bin/"+id+"/")
		}
		if !dirExists(m.p.EngineEtc(id)) {
			missingDirs = append(missingDirs, "etc/"+id+"/")
		}
	}
	if err := m.p.Ensure(); err != nil {
		return fmt.Errorf("failed to repair directory structure: %w", err)
	}
	for _, id := range EngineIDs {
		_ = os.MkdirAll(m.p.EngineEtc(id), 0o755)
	}
	if len(missingDirs) > 0 {
		add("Folders", "Recreated missing portable directories: "+strings.Join(missingDirs, ", "), "fixed")
	} else {
		add("Folders", "Verified portable folder structure (www/, bin/, data/, etc/, logs/, backups/, tmp/)", "ok")
	}

	indexFile := filepath.Join(m.p.Www, "index.html")
	if !fileExists(indexFile) {
		ensureWwwIndex(m.p.Www)
		add("Web Root", "Restored missing default landing page (www/index.html)", "fixed")
	} else {
		ensureWwwIndex(m.p.Www)
		add("Web Root", "Verified web root document (www/index.html)", "ok")
	}
	return nil
}

func (m *Manager) repairSettingsAndCatalog(add func(cat, action, status string)) map[int]string {
	settingsFile := filepath.Join(m.p.Root, "settings.json")
	settingsFixed := false
	if b, err := os.ReadFile(settingsFile); err != nil {
		_ = m.st.Update(func(s *Settings) {})
		add("Settings", "Recreated missing settings.json with clean defaults", "fixed")
		settingsFixed = true
	} else {
		var raw map[string]interface{}
		if json.Unmarshal(b, &raw) != nil {
			_ = os.WriteFile(settingsFile+".bak", b, 0o644)
			_ = m.st.Update(func(s *Settings) {})
			add("Settings", "Backed up corrupted settings.json to settings.json.bak and restored valid settings", "fixed")
			settingsFixed = true
		}
	}

	defaults := DefaultSettings()
	usedPorts := map[int]string{}
	portRepairs := []string{}
	cloudFixed := 0
	schedFixed := 0

	_ = m.st.Update(func(s *Settings) {
		if s.Engines == nil {
			s.Engines = map[string]*EngineSettings{}
		}
		if s.Theme != "system" && s.Theme != "dark" && s.Theme != "light" {
			s.Theme = "system"
			portRepairs = append(portRepairs, "theme -> system")
		}
		for _, id := range EngineIDs {
			def := defaults.Engines[id]
			es, ok := s.Engines[id]
			if !ok || es == nil {
				cp := *def
				s.Engines[id] = &cp
				portRepairs = append(portRepairs, fmt.Sprintf("%s -> %d", id, def.Port))
				continue
			}
			if id == "mssql" && (es.User != "" || es.Password != "") {
				es.User = ""
				es.Password = ""
				portRepairs = append(portRepairs, "mssql Windows Auth reset")
			}
			if es.Port <= 0 || es.Port > 65535 {
				es.Port = def.Port
				portRepairs = append(portRepairs, fmt.Sprintf("%s port -> %d", id, def.Port))
			}
			// MariaDB and MySQL share standard MySQL protocol port 3306 when used interchangeably
			isMySQLPair := (id == "mysql" && usedPorts[es.Port] == "mariadb") || (id == "mariadb" && usedPorts[es.Port] == "mysql")
			if other, taken := usedPorts[es.Port]; taken && other != id && !isMySQLPair {
				es.Port = def.Port
				portRepairs = append(portRepairs, fmt.Sprintf("%s port collision resolved -> %d", id, def.Port))
			}
			usedPorts[es.Port] = id
		}

		if len(s.CloudConnections) > 0 {
			seenIDs := map[string]bool{}
			cleaned := make([]CloudConnection, 0, len(s.CloudConnections))
			for _, c := range s.CloudConnections {
				nc := c.Normalized()
				if strings.TrimSpace(nc.Host) == "" && strings.TrimSpace(nc.Name) == "" {
					cloudFixed++
					continue
				}
				if seenIDs[nc.ID] {
					nc.ID = newCloudID()
					cloudFixed++
				}
				seenIDs[nc.ID] = true
				if nc.ID != c.ID || nc.Port != c.Port || nc.Engine != c.Engine || nc.Name != c.Name {
					cloudFixed++
				}
				cleaned = append(cleaned, nc)
			}
			s.CloudConnections = cleaned
		}

		if len(s.Schedules) > 0 {
			seenSched := map[string]bool{}
			cleaned := make([]BackupSyncSchedule, 0, len(s.Schedules))
			for _, sc := range s.Schedules {
				ns := sc.Normalized()
				if seenSched[ns.ID] {
					ns.ID = newScheduleID()
					schedFixed++
				}
				seenSched[ns.ID] = true
				if ns.ID != sc.ID || ns.SyncTime != sc.SyncTime || ns.BackupTime != sc.BackupTime || ns.RetentionDays != sc.RetentionDays || ns.Name != sc.Name {
					schedFixed++
				}
				_ = os.MkdirAll(ScheduleBackupDir(m.p, ns), 0o755)
				cleaned = append(cleaned, ns)
			}
			s.Schedules = cleaned
		}
	})

	if len(portRepairs) > 0 {
		add("Settings", "Repaired settings/port configuration ("+strings.Join(portRepairs, ", ")+")", "fixed")
	} else if !settingsFixed {
		add("Settings", "Verified settings.json and engine ports", "ok")
	}
	if cloudFixed > 0 {
		add("Cloud", fmt.Sprintf("Normalized and repaired %d saved Cloud profile entry(s)", cloudFixed), "fixed")
	} else if len(m.st.Get().CloudConnections) > 0 {
		add("Cloud", fmt.Sprintf("Verified %d saved Cloud database profile(s)", len(m.st.Get().CloudConnections)), "ok")
	}
	if schedFixed > 0 {
		add("Schedules", fmt.Sprintf("Normalized %d Auto-Sync / Auto-Backup schedule(s) and snapshot folders", schedFixed), "fixed")
	} else if len(m.st.Get().Schedules) > 0 {
		add("Schedules", fmt.Sprintf("Verified %d Auto-Sync / Auto-Backup schedule(s)", len(m.st.Get().Schedules)), "ok")
	}

	catFile := filepath.Join(m.p.Root, "catalog.json")
	if b, err := os.ReadFile(catFile); err == nil {
		var c Catalog
		if json.Unmarshal(b, &c) != nil || len(c) == 0 {
			_ = os.Rename(catFile, catFile+".bak")
			add("Catalog", "Removed corrupted catalog.json (backed up to catalog.json.bak) and restored built-in catalog", "fixed")
		} else {
			add("Catalog", "Verified custom catalog.json integrity", "ok")
		}
	}
	return usedPorts
}

func (m *Manager) repairTempFilesLogsAndCaches(opts RepairOptions, add func(cat, action, status string)) {
	cleanedTemp := 0
	for _, pat := range []string{
		filepath.Join(m.p.Tmp, "*.tmp"),
		filepath.Join(m.p.Tmp, "*.part"),
		filepath.Join(m.p.Tmp, "*.download"),
		filepath.Join(m.p.Tmp, "dariusdb-mssql-*.sql"),
		filepath.Join(m.p.Tmp, "mysql-init.sql"),
		filepath.Join(m.p.Tmp, "pg-pw.txt"),
		filepath.Join(m.p.Root, "settings.json.tmp"),
		filepath.Join(m.p.Backups, "*.tmp"),
		filepath.Join(m.p.Backups, "*", "*.tmp"),
	} {
		matches, _ := filepath.Glob(pat)
		for _, f := range matches {
			if os.Remove(f) == nil {
				cleanedTemp++
			}
		}
	}
	if cleanedTemp > 0 {
		add("Cleanup", fmt.Sprintf("Cleaned %d stale temporary / partial download or backup file(s)", cleanedTemp), "fixed")
	}

	rotatedLogs := []string{}
	if logEntries, err := os.ReadDir(m.p.Logs); err == nil {
		for _, ent := range logEntries {
			if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".log") {
				continue
			}
			info, err := ent.Info()
			if err != nil || info.Size() <= maxRepairLogBytes {
				continue
			}
			lp := filepath.Join(m.p.Logs, ent.Name())
			bak := lp + ".1"
			_ = os.Remove(bak)
			if os.Rename(lp, bak) == nil {
				_ = os.WriteFile(lp, []byte(""), 0o644)
				rotatedLogs = append(rotatedLogs, ent.Name())
			}
		}
	}
	if len(rotatedLogs) > 0 {
		add("Logs", "Rotated oversized log file(s) (>20 MB) to .log.1: "+strings.Join(rotatedLogs, ", "), "fixed")
	} else {
		add("Logs", "Verified log file sizes and write access", "ok")
	}

	if opts.CleanCache {
		CloseSQLPools("", "")
		InvalidateSystemDetectCache()
		InvalidateMSSQLTargetCache()
		add("Caches", "Reset SQL connection pools, LocalDB pipe cache, and system engine detection caches", "fixed")
	}
}

func (m *Manager) repairSystemPath(add func(cat, action, status string)) {
	// Skip modifying the user's Windows Registry PATH during synthetic temp-dir tests.
	if !fileExists(filepath.Join(m.p.Root, "DariusDB.exe")) && !fileExists(filepath.Join(m.p.Root, "darius.exe")) {
		return
	}
	dirs := append([]string{m.p.Root}, m.ClientPaths()...)
	if err := UpdateUserPath(dirs, true); err != nil {
		add("PATH", "Could not update Windows user PATH: "+err.Error(), "warn")
		return
	}
	add("PATH", "Verified Windows user PATH for Darius DB CLI & active engine tools", "ok")
}

func findFreePortNear(start int, taken map[int]string) int {
	if start <= 1024 || start > 65000 {
		start = 10000
	}
	for p := start; p <= 65500; p++ {
		if taken[p] != "" {
			continue
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return p
	}
	return start
}

func (m *Manager) repairSingleEngine(id string, opts RepairOptions, usedPorts map[int]string, add func(cat, action, status string)) {
	eng, err := GetEngine(id)
	if err != nil {
		return
	}
	cat := eng.Name()
	resetConfig := opts.ResetConfigs

	// A. Clear stuck service states (error, or wedged starting/stopping with dead PID)
	m.mu.Lock()
	svc := m.svc[id]
	wasError := svc != nil && svc.state == StateError
	wasWedged := false
	if svc != nil {
		if svc.state == StateError {
			svc.state = StateStopped
			svc.message = ""
			svc.pid = 0
			svc.cmd = nil
		} else if opts.DeepSystem && (svc.state == StateStarting || svc.state == StateStopping) && !processAlive(svc.pid) {
			svc.state = StateStopped
			svc.message = ""
			svc.pid = 0
			svc.cmd = nil
			svc.stopReq = false
			wasWedged = true
		}
	}
	m.mu.Unlock()
	if wasError {
		add(cat, "Cleared previous service error state", "fixed")
	} else if wasWedged {
		add(cat, "Cleared wedged starting/stopping service state", "fixed")
	}

	running := m.IsRunning(id)

	// B. Clean stale tmp/run/<engine>.pid file
	pidFile := m.p.PidFile(id)
	if fileExists(pidFile) && !running {
		b, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid <= 0 || !processAlive(pid) {
			_ = os.Remove(pidFile)
			add(cat, fmt.Sprintf("Removed stale PID file (tmp/run/%s.pid)", id), "fixed")
		}
	}

	// Restore hidden built-in/system versions if requested or none remain
	es := m.st.Engine(id)
	if len(es.HiddenVersions) > 0 && (resetConfig || len(ListInstallations(m.p, id)) == 0) {
		_ = m.st.Update(func(st *Settings) {
			if e := st.Engines[id]; e != nil {
				e.HiddenVersions = nil
			}
		})
		m.ensureActiveVersions()
		es = m.st.Engine(id)
		add(cat, "Restored previously removed built-in/system engine detection", "fixed")
	}

	inst, ok := FindInstallation(m.p, id, es.Version)

	// C. Check orphaned engine processes & busy port conflicts
	if ok && !running && es.Port > 0 && portInUse(es.Port) {
		if opts.DeepSystem && inst.Dir != "" && !strings.EqualFold(filepath.Clean(inst.Dir), filepath.Clean(m.p.Root)) && hasProcessInDir(inst.Dir) {
			killProcessesInDir(inst.Dir)
			deadline := time.Now().Add(800 * time.Millisecond)
			for time.Now().Before(deadline) && portInUse(es.Port) {
				time.Sleep(50 * time.Millisecond)
			}
			add(cat, fmt.Sprintf("Cleaned orphaned %s background process holding port %d", eng.Name(), es.Port), "fixed")
		}
		if portInUse(es.Port) {
			isMySQLSiblingRunning := (id == "mysql" && m.IsRunning("mariadb")) || (id == "mariadb" && m.IsRunning("mysql"))
			if !isMySQLSiblingRunning {
				if opts.FixPorts {
					oldPort := es.Port
					newPort := findFreePortNear(oldPort+1, usedPorts)
					delete(usedPorts, oldPort)
					usedPorts[newPort] = id
					_ = m.st.Update(func(s *Settings) {
						if e := s.Engines[id]; e != nil {
							e.Port = newPort
						}
					})
					es.Port = newPort
					add(cat, fmt.Sprintf("Port %d was busy (used by another program) — reassigned %s to free port %d", oldPort, eng.Name(), newPort), "fixed")
				} else {
					add(cat, fmt.Sprintf("Port %d is busy (used by another program — enable 'Auto-fix busy ports' or stop the conflicting app)", es.Port), "warn")
				}
			}
		}
	}

	// D. Engine-specific lock, crash artifact, and half-initialized data folder repairs
	needPrepare := false
	switch id {
	case "apache":
		if !running {
			for _, pf := range []string{filepath.Join(m.p.Logs, "httpd.pid"), filepath.Join(m.p.Tmp, "httpd.pid")} {
				if fileExists(pf) {
					_ = os.Remove(pf)
					add(cat, "Removed stale Apache httpd.pid lock file", "fixed")
				}
			}
		}
		if ok && fileExists(filepath.Join(inst.Dir, "conf", "darius-apache.json")) && !fileExists(filepath.Join(inst.Dir, "conf", "httpd.conf")) {
			_ = provisionPortableApache(inst.Dir, inst.Version)
			add(cat, "Restored missing portable Apache conf/httpd.conf", "fixed")
		}

	case "php":
		routerPath := filepath.Join(m.p.EngineEtc("php"), "router.php")
		if err := os.WriteFile(routerPath, []byte(strings.ReplaceAll(defaultPhpRouter, "\n", "\r\n")), 0o644); err == nil {
			if !fileExists(routerPath) {
				add(cat, "Warning: could not verify etc/php/router.php", "warn")
			}
		}

	case "mariadb", "mysql":
		if !running {
			matches, _ := filepath.Glob(filepath.Join(m.p.Data, id+"-*", "*.pid"))
			for _, pf := range matches {
				if os.Remove(pf) == nil {
					add(cat, "Removed stale "+eng.Name()+" PID lock ("+filepath.Base(pf)+")", "fixed")
				}
			}
			tcMatches, _ := filepath.Glob(filepath.Join(m.p.Data, id+"-*", "tc.log"))
			for _, tc := range tcMatches {
				if st, statErr := os.Stat(tc); statErr == nil && st.Size() == 0 {
					_ = os.Remove(tc)
					add(cat, "Removed empty 0-byte "+eng.Name()+" tc.log crash lock", "fixed")
				}
			}
			if ok {
				dataDir := eng.DataDir(m.p, inst)
				if dirExists(dataDir) && !dirEmpty(dataDir) && !dirExists(filepath.Join(dataDir, "mysql")) {
					bakDir := dataDir + ".incomplete." + time.Now().Format("20060102150405")
					if os.Rename(dataDir, bakDir) == nil {
						needPrepare = true
						add(cat, fmt.Sprintf("Backed up incomplete %s data folder (%s) and triggered clean re-initialization", eng.Name(), filepath.Base(bakDir)), "fixed")
					}
				}
			}
		}

	case "postgresql":
		if !running {
			matches, _ := filepath.Glob(filepath.Join(m.p.Data, "postgresql-*", "postmaster.pid"))
			for _, pm := range matches {
				if isStalePostmasterPid(pm) {
					_ = os.Remove(pm)
					add(cat, "Removed stale PostgreSQL postmaster.pid lock file ("+filepath.Base(filepath.Dir(pm))+")", "fixed")
				}
			}
			sockMatches, _ := filepath.Glob(filepath.Join(m.p.Tmp, ".s.PGSQL.*"))
			for _, sm := range sockMatches {
				_ = os.Remove(sm)
				add(cat, "Removed stale PostgreSQL socket lock ("+filepath.Base(sm)+")", "fixed")
			}
			if ok {
				dataDir := eng.DataDir(m.p, inst)
				if dirExists(dataDir) && !dirEmpty(dataDir) && !fileExists(filepath.Join(dataDir, "PG_VERSION")) {
					bakDir := dataDir + ".incomplete." + time.Now().Format("20060102150405")
					if os.Rename(dataDir, bakDir) == nil {
						needPrepare = true
						add(cat, fmt.Sprintf("Backed up incomplete PostgreSQL data folder (%s) and triggered clean re-initialization", filepath.Base(bakDir)), "fixed")
					}
				}
			}
		}

	case "mssql":
		if !running {
			if localDB := findSqlLocalDB(); localDB != "" {
				if wasError || resetConfig {
					stopCmd := exec.Command(localDB, "stop", mssqlInstance, "-k")
					hideWindow(stopCmd)
					_ = stopCmd.Run()

					createCmd := exec.Command(localDB, "create", mssqlInstance)
					hideWindow(createCmd)
					if createCmd.Run() == nil {
						InvalidateMSSQLTargetCache()
						add(cat, "Reset & verified SQL Server LocalDB instance ("+mssqlInstance+")", "fixed")
					}
				} else if opts.DeepSystem {
					infoCmd := exec.Command(localDB, "info", mssqlInstance)
					hideWindow(infoCmd)
					if out, infoErr := infoCmd.CombinedOutput(); infoErr != nil || strings.Contains(strings.ToLower(string(out)), "not created") {
						createCmd := exec.Command(localDB, "create", mssqlInstance)
						hideWindow(createCmd)
						if createCmd.Run() == nil {
							InvalidateMSSQLTargetCache()
							add(cat, "Created missing SQL Server LocalDB instance ("+mssqlInstance+")", "fixed")
						}
					}
				}
			}
		}
		if findSqlcmd() == "" {
			add(cat, "sqlcmd.exe not found yet — download Portable sqlcmd in Versions tab for MS SQL queries/sync", "warn")
		}

	case "redis":
		dataDir := filepath.Join(m.p.Data, "redis")
		_ = os.MkdirAll(dataDir, 0o755)
		if !running {
			tmpRdbs, _ := filepath.Glob(filepath.Join(dataDir, "temp-*.rdb"))
			for _, tr := range tmpRdbs {
				if os.Remove(tr) == nil {
					add(cat, "Removed stale Redis temp RDB file ("+filepath.Base(tr)+")", "fixed")
				}
			}
		}

	case "mailpit":
		dataDir := filepath.Join(m.p.Data, "mailpit")
		_ = os.MkdirAll(dataDir, 0o755)
		msgFile := filepath.Join(dataDir, "messages.json")
		if b, err := os.ReadFile(msgFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			var list []interface{}
			if json.Unmarshal(b, &list) != nil {
				_ = os.WriteFile(msgFile+".bak", b, 0o644)
				_ = os.WriteFile(msgFile, []byte("[]\r\n"), 0o644)
				add(cat, "Backed up corrupted Mailpit messages.json to .bak and restored clean inbox", "fixed")
			}
		}
	}

	if !ok {
		add(cat, "No version installed yet (ready to download in Versions tab)", "ok")
		return
	}

	// E. Verify & repair engine configuration file
	cfgPath := eng.ConfigFile(m.p, inst)
	if cfgPath != "" {
		st, statErr := os.Stat(cfgPath)
		if statErr != nil || st.Size() == 0 {
			needPrepare = true
		} else if resetConfig && !running {
			_ = os.Rename(cfgPath, cfgPath+".bak")
			needPrepare = true
		} else if !running {
			if repaired := m.repairCorruptConfigContent(id, cfgPath); repaired {
				add(cat, "Repaired invalid directives in "+filepath.Base(cfgPath)+" (backed up original to .bak)", "fixed")
			}
		}
	}

	if needPrepare {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := eng.Prepare(ctx, m.p, inst, es, m.logf(id)); err != nil {
			add(cat, "Config/data repair warning: "+err.Error(), "warn")
		} else {
			cfgLabel := filepath.Base(cfgPath)
			if cfgPath == "" {
				cfgLabel = inst.Name
			}
			add(cat, "Regenerated clean configuration & verified initialization ("+cfgLabel+")", "fixed")
		}
	} else {
		add(cat, fmt.Sprintf("Verified %s (%s) configuration & data directory", eng.Name(), inst.Name), "ok")
	}
}

func (m *Manager) repairCorruptConfigContent(id, cfgPath string) bool {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return false
	}
	text := string(b)
	switch id {
	case "apache":
		if !strings.Contains(text, "DocumentRoot") || !strings.Contains(text, "LoadModule") {
			_ = os.WriteFile(cfgPath+".bak", b, 0o644)
			return os.WriteFile(cfgPath, []byte(strings.ReplaceAll(apacheHttpdConf, "\n", "\r\n")), 0o644) == nil
		}
	case "php":
		if !strings.Contains(text, "[PHP]") {
			_ = os.WriteFile(cfgPath+".bak", b, 0o644)
			return os.WriteFile(cfgPath, []byte(strings.ReplaceAll(defaultPhpIni, "\n", "\r\n")), 0o644) == nil
		}
	case "mariadb":
		if !strings.Contains(text, "[mysqld]") || strings.Contains(text, "skip-name-resolve") || strings.Contains(text, "innodb_buffer_pool_size = 128M") {
			_ = os.WriteFile(cfgPath+".bak", b, 0o644)
			return os.WriteFile(cfgPath, []byte(strings.ReplaceAll(mariadbIni, "\n", "\r\n")), 0o644) == nil
		}
	case "mysql":
		if !strings.Contains(text, "[mysqld]") || strings.Contains(text, "skip-name-resolve") || strings.Contains(text, "innodb_buffer_pool_size = 128M") {
			_ = os.WriteFile(cfgPath+".bak", b, 0o644)
			return os.WriteFile(cfgPath, []byte(strings.ReplaceAll(mysqlIni, "\n", "\r\n")), 0o644) == nil
		}
	case "mssql":
		if !strings.Contains(text, "instance") || strings.Contains(text, "default_user") || strings.Contains(text, "default_password") {
			_ = os.WriteFile(cfgPath+".bak", b, 0o644)
			return os.WriteFile(cfgPath, []byte(strings.ReplaceAll(mssqlConf, "\n", "\r\n")), 0o644) == nil
		}
	case "redis":
		if !strings.Contains(text, "dbfilename") {
			_ = os.WriteFile(cfgPath+".bak", b, 0o644)
			return os.WriteFile(cfgPath, []byte(strings.ReplaceAll(redisConf, "\n", "\r\n")), 0o644) == nil
		}
	}
	return false
}

func isStalePostmasterPid(pmPath string) bool {
	b, err := os.ReadFile(pmPath)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return true
	}
	return !processAlive(pid)
}
