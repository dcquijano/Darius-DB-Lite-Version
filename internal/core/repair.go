package core

import (
	"context"
	"encoding/json"
	"fmt"
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

// RepairReport summarizes all checks and fixes performed during a repair run.
type RepairReport struct {
	Target     string       `json:"target"`
	FixedCount int          `json:"fixedCount"`
	CheckedAt  string       `json:"checkedAt"`
	Summary    string       `json:"summary"`
	Items      []RepairItem `json:"items"`
}

// Repair diagnoses and repairs Darius DB directories, settings, configs, stale locks,
// and engine states. target may be "", "all", or a specific engine ID
// ("apache", "mariadb", "mysql", "postgresql", "mssql", "redis").
// If resetConfigs is true, existing engine config files are backed up to .bak and regenerated.
func (m *Manager) Repair(target string, resetConfigs bool) (*RepairReport, error) {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		target = "all"
	}
	if target != "all" {
		if _, err := GetEngine(target); err != nil {
			return nil, err
		}
	}

	rep := &RepairReport{
		Target:    target,
		CheckedAt: time.Now().Format("2006-01-02 15:04:05"),
		Items:     []RepairItem{},
	}
	add := func(cat, action, status string) {
		if status == "fixed" {
			rep.FixedCount++
		}
		rep.Items = append(rep.Items, RepairItem{
			Category: cat,
			Action:   action,
			Status:   status,
		})
	}

	// 1. Portable Directory Structure & Web Root
	missingDirs := []string{}
	for _, d := range []string{m.p.Www, m.p.Bin, m.p.Data, m.p.Etc, m.p.Logs, m.p.Tmp} {
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
		return nil, fmt.Errorf("failed to repair directory structure: %w", err)
	}
	for _, id := range EngineIDs {
		_ = os.MkdirAll(m.p.EngineEtc(id), 0o755)
	}
	if len(missingDirs) > 0 {
		add("Folders", "Recreated missing portable directories: "+strings.Join(missingDirs, ", "), "fixed")
	} else {
		add("Folders", "Verified portable folder structure (www/, bin/, data/, etc/, logs/, tmp/)", "ok")
	}

	indexFile := filepath.Join(m.p.Www, "index.html")
	if !fileExists(indexFile) {
		_ = writeIfMissing(indexFile, defaultWwwIndexHTML)
		add("Web Root", "Restored missing default landing page (www/index.html)", "fixed")
	} else {
		add("Web Root", "Verified web root document (www/index.html)", "ok")
	}

	// 2. Settings (settings.json) & Port Validation
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
	_ = m.st.Update(func(s *Settings) {
		if s.Engines == nil {
			s.Engines = map[string]*EngineSettings{}
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
			if es.Port <= 0 || es.Port > 65535 {
				es.Port = def.Port
				portRepairs = append(portRepairs, fmt.Sprintf("%s port -> %d", id, def.Port))
			}
			if other, taken := usedPorts[es.Port]; taken && other != id {
				es.Port = def.Port
				portRepairs = append(portRepairs, fmt.Sprintf("%s port collision resolved -> %d", id, def.Port))
			}
			usedPorts[es.Port] = id
		}
	})
	if len(portRepairs) > 0 {
		add("Settings", "Repaired engine port configuration ("+strings.Join(portRepairs, ", ")+")", "fixed")
	} else if !settingsFixed {
		add("Settings", "Verified settings.json and engine ports", "ok")
	}

	// 3. Version Catalog (catalog.json)
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

	// 4. Relink active installations
	m.ensureActiveVersions()

	// 5. Per-Engine Diagnostics & Repair
	var targets []string
	if target == "all" {
		targets = EngineIDs
	} else {
		targets = []string{target}
	}

	for _, id := range targets {
		m.repairSingleEngine(id, resetConfigs || target == id, add)
	}

	// Re-adopt any running processes and notify UI
	m.adopt()
	m.changed()

	if rep.FixedCount > 0 {
		rep.Summary = fmt.Sprintf("Repair completed: %d issue(s) automatically fixed.", rep.FixedCount)
	} else {
		rep.Summary = "All checks passed — no corrupted files, locks, or configuration errors found."
	}
	return rep, nil
}

func (m *Manager) repairSingleEngine(id string, resetConfig bool, add func(cat, action, status string)) {
	eng, err := GetEngine(id)
	if err != nil {
		return
	}
	cat := eng.Name()
	running := m.IsRunning(id)

	// A. Clear stuck error state in Manager
	m.mu.Lock()
	svc := m.svc[id]
	wasError := svc != nil && svc.state == StateError
	if wasError {
		svc.state = StateStopped
		svc.message = ""
		svc.pid = 0
		svc.cmd = nil
	}
	m.mu.Unlock()
	if wasError {
		add(cat, "Cleared previous service error state", "fixed")
	}

	// B. Clean stale tmp/<engine>.pid file
	pidFile := m.p.PidFile(id)
	if fileExists(pidFile) && !running {
		b, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid <= 0 || !processAlive(pid) {
			_ = os.Remove(pidFile)
			add(cat, fmt.Sprintf("Removed stale PID file (tmp/%s.pid)", id), "fixed")
		}
	}

	// C. Engine-specific lock & runtime repairs
	switch id {
	case "postgresql":
		if !running {
			matches, _ := filepath.Glob(filepath.Join(m.p.Data, "postgresql-*", "postmaster.pid"))
			for _, pm := range matches {
				if isStalePostmasterPid(pm) {
					_ = os.Remove(pm)
					add(cat, "Removed stale PostgreSQL postmaster.pid lock file ("+filepath.Base(filepath.Dir(pm))+")", "fixed")
				}
			}
		}

	case "mariadb", "mysql":
		if !running {
			matches, _ := filepath.Glob(filepath.Join(m.p.Data, id+"-*", "*.pid"))
			for _, pf := range matches {
				_ = os.Remove(pf)
				add(cat, "Removed stale "+eng.Name()+" PID lock ("+filepath.Base(pf)+")", "fixed")
			}
		}

	case "mssql":
		if !running {
			if localDB := findSqlLocalDB(); localDB != "" && (wasError || resetConfig) {
				stopCmd := exec.Command(localDB, "stop", mssqlInstance, "-k")
				hideWindow(stopCmd)
				_ = stopCmd.Run()

				createCmd := exec.Command(localDB, "create", mssqlInstance)
				hideWindow(createCmd)
				if err := createCmd.Run(); err == nil {
					add(cat, "Reset & verified SQL Server LocalDB instance ("+mssqlInstance+")", "fixed")
				}
			}
		}
	}

	// D. Verify active installation & configuration file
	es := m.st.Engine(id)
	inst, ok := FindInstallation(m.p, id, es.Version)
	if !ok {
		add(cat, "No version installed yet (ready to download in Versions tab)", "ok")
		return
	}

	cfgPath := eng.ConfigFile(m.p, inst)
	needPrepare := false
	if cfgPath != "" {
		st, statErr := os.Stat(cfgPath)
		if statErr != nil || st.Size() == 0 {
			needPrepare = true
		} else if resetConfig && !running {
			_ = os.Rename(cfgPath, cfgPath+".bak")
			needPrepare = true
		}
	}

	if needPrepare {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := eng.Prepare(ctx, m.p, inst, es, m.logf(id)); err != nil {
			add(cat, "Config repair warning: "+err.Error(), "warn")
		} else {
			add(cat, "Regenerated clean configuration file ("+filepath.Base(cfgPath)+")", "fixed")
		}
	} else {
		add(cat, fmt.Sprintf("Verified %s (%s) configuration & data directory", eng.Name(), inst.Name), "ok")
	}
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
