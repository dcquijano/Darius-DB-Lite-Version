//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dariusdb/internal/core"
)

type setupEngineSpec struct {
	ID            string
	Label         string
	Version       string
	URL           string
	DefaultSizeMB int
	NeedsVCRedist bool
	NeedsLocalDB  bool
	Recommended   bool
	OfflinePath   string
	OfflineIsDir  bool
	OfflineSizeMB int
}

type setupPrereqInfo struct {
	Status      core.PrereqStatus
	OfflinePath string
}

type setupEnvSnapshot struct {
	Prereqs             [3]setupPrereqInfo // 0: WebView2, 1: VC++ x64, 2: SQL LocalDB
	Engines             []setupEngineSpec
	OfflineDirs         []string
	HasOfflinePack      bool
	OfflineRoot         string
	OfflineEngineCount  int
	OfflinePrereqCount  int
}

var setupEngineOrder = []struct {
	id            string
	label         string
	defaultSizeMB int
	needsVCRedist bool
	needsLocalDB  bool
	recommended   bool
}{
	{id: "mariadb", label: "MariaDB", defaultSizeMB: 75, recommended: true},
	{id: "mysql", label: "MySQL", defaultSizeMB: 250, needsVCRedist: true},
	{id: "postgresql", label: "PostgreSQL", defaultSizeMB: 230, needsVCRedist: true},
	{id: "mssql", label: "MS SQL CLI", defaultSizeMB: 15, needsLocalDB: true},
	{id: "redis", label: "Redis", defaultSizeMB: 12},
	{id: "apache", label: "Apache Web", defaultSizeMB: 12, needsVCRedist: true, recommended: true},
	{id: "php", label: "PHP", defaultSizeMB: 32, needsVCRedist: true, recommended: true},
	{id: "mailpit", label: "Mailpit", defaultSizeMB: 10},
}

func discoverOfflineDirs(explicitDir, selfExe, installDir string) []string {
	var raw []string
	cleanInstall := ""
	if strings.TrimSpace(installDir) != "" {
		cleanInstall = strings.ToLower(filepath.Clean(installDir))
	}
	if strings.TrimSpace(explicitDir) != "" {
		raw = append(raw, explicitDir)
	}
	if strings.TrimSpace(selfExe) != "" {
		selfDir := filepath.Dir(selfExe)
		parentDir := filepath.Dir(selfDir)
		if cleanInstall == "" || !strings.EqualFold(filepath.Clean(selfDir), cleanInstall) {
			raw = append(raw, selfDir)
		}
		raw = append(raw,
			filepath.Join(selfDir, "offline"),
			filepath.Join(parentDir, "offline"),
			filepath.Join(parentDir, "build", "offline"),
		)
	}
	if strings.TrimSpace(installDir) != "" {
		// Only check <installDir>\offline so <installDir>\bin\<engine> is never mistaken for an offline source.
		raw = append(raw, filepath.Join(installDir, "offline"))
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range raw {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		clean := filepath.Clean(d)
		key := strings.ToLower(clean)
		if seen[key] {
			continue
		}
		seen[key] = true
		if st, err := os.Stat(clean); err == nil && st.IsDir() {
			out = append(out, clean)
		}
	}
	return out
}

func probeSetupEnvironment(explicitOfflineDir, selfExe, installDir string) setupEnvSnapshot {
	offlineDirs := discoverOfflineDirs(explicitOfflineDir, selfExe, installDir)
	snap := setupEnvSnapshot{
		OfflineDirs: offlineDirs,
	}

	prereqs := core.CheckAllPrerequisites()
	for i := 0; i < len(prereqs) && i < len(snap.Prereqs); i++ {
		offPath := core.FindOfflinePrereq(prereqs[i].ID, offlineDirs)
		snap.Prereqs[i] = setupPrereqInfo{
			Status:      prereqs[i],
			OfflinePath: offPath,
		}
		if offPath != "" {
			snap.OfflinePrereqCount++
			if !snap.HasOfflinePack {
				snap.HasOfflinePack = true
				snap.OfflineRoot = filepath.Dir(offPath)
			}
		}
	}

	var paths *core.Paths
	if strings.TrimSpace(installDir) != "" {
		paths = core.NewPaths(installDir)
	}
	cat := core.LoadCatalog(paths)
	for _, meta := range setupEngineOrder {
		spec := setupEngineSpec{
			ID:            meta.id,
			Label:         meta.label,
			DefaultSizeMB: meta.defaultSizeMB,
			NeedsVCRedist: meta.needsVCRedist,
			NeedsLocalDB:  meta.needsLocalDB,
			Recommended:   meta.recommended,
		}
		if entries := cat[meta.id]; len(entries) > 0 {
			spec.Version = entries[0].Version
			spec.URL = entries[0].URL
		}
		if dirPath, ver, ok := core.FindOfflineEngineDir(meta.id, spec.Version, offlineDirs); ok {
			spec.OfflinePath = dirPath
			spec.OfflineIsDir = true
			spec.OfflineSizeMB = calcPathSizeMB(dirPath, meta.defaultSizeMB)
			if ver != "" {
				spec.Version = ver
			}
			snap.OfflineEngineCount++
			if !snap.HasOfflinePack {
				snap.HasOfflinePack = true
				snap.OfflineRoot = filepath.Dir(filepath.Dir(dirPath))
			}
		} else if zipPath, ver, ok := core.FindOfflineEngineArchive(meta.id, spec.Version, offlineDirs); ok {
			spec.OfflinePath = zipPath
			spec.OfflineIsDir = false
			spec.OfflineSizeMB = calcPathSizeMB(zipPath, meta.defaultSizeMB)
			if ver != "" {
				spec.Version = ver
			}
			snap.OfflineEngineCount++
			if !snap.HasOfflinePack {
				snap.HasOfflinePack = true
				snap.OfflineRoot = filepath.Dir(zipPath)
			}
		}
		snap.Engines = append(snap.Engines, spec)
	}
	return snap
}

func calcPathSizeMB(path string, fallbackMB int) int {
	st, err := os.Stat(path)
	if err != nil {
		return fallbackMB
	}
	if !st.IsDir() {
		mb := int((st.Size() + 512*1024) / (1024 * 1024))
		if mb < 1 {
			mb = 1
		}
		return mb
	}
	return fallbackMB
}

func formatPrereqCheckboxLabel(info setupPrereqInfo) string {
	var name, usedBy string
	approxMB := 0
	switch info.Status.ID {
	case core.PrereqIDWebView2:
		name = "Microsoft Edge WebView2 Runtime"
		usedBy = "Required for GUI"
		approxMB = 2
	case core.PrereqIDVCRedistX64:
		name = "Visual C++ 2015–2026 Redistributable (x64)"
		usedBy = "For MySQL / PostgreSQL / Apache / PHP"
		approxMB = 25
	case core.PrereqIDMSSQLLocalDB:
		name = "SQL Server Express LocalDB"
		usedBy = "For local MS SQL Server"
		approxMB = 55
	default:
		name = info.Status.Name
		usedBy = "Required runtime"
	}
	if info.Status.Installed {
		return fmt.Sprintf("%s — Installed ✓", name)
	}
	if info.OfflinePath != "" {
		return fmt.Sprintf("%s — Missing ⚠ [Offline Ready — %s]", name, usedBy)
	}
	return fmt.Sprintf("%s — Missing ⚠ (~%d MB download — %s)", name, approxMB, usedBy)
}

func formatEngineCheckboxLabel(spec setupEngineSpec) string {
	ver := spec.Version
	if spec.OfflinePath != "" {
		mb := spec.OfflineSizeMB
		if mb <= 0 {
			mb = spec.DefaultSizeMB
		}
		if ver != "" {
			return fmt.Sprintf("%s %s [Offline %d MB]", spec.Label, ver, mb)
		}
		return fmt.Sprintf("%s [Offline %d MB]", spec.Label, mb)
	}
	if ver != "" {
		return fmt.Sprintf("%s %s (~%d MB)", spec.Label, ver, spec.DefaultSizeMB)
	}
	return fmt.Sprintf("%s (~%d MB)", spec.Label, spec.DefaultSizeMB)
}

func prereqApproxDownloadMB(id string) int {
	switch id {
	case core.PrereqIDWebView2:
		return 2
	case core.PrereqIDVCRedistX64:
		return 25
	case core.PrereqIDMSSQLLocalDB:
		return 55
	default:
		return 10
	}
}

func parsePrereqSelectionArg(raw string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, tok := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "", "none":
		case "all":
			add(core.PrereqIDWebView2)
			add(core.PrereqIDVCRedistX64)
			add(core.PrereqIDMSSQLLocalDB)
		case "webview2", "wv2", "edge":
			add(core.PrereqIDWebView2)
		case "vcredist", "vcredist_x64", "vc", "vc++", "vcpp":
			add(core.PrereqIDVCRedistX64)
		case "localdb", "mssql_localdb", "sqllocaldb":
			add(core.PrereqIDMSSQLLocalDB)
		}
	}
	return out
}

func parseEngineSelectionArg(raw string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, tok := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "", "none", "lite":
		case "all", "full":
			for _, m := range setupEngineOrder {
				add(m.id)
			}
		case "recommended", "web", "webstack":
			for _, m := range setupEngineOrder {
				if m.recommended {
					add(m.id)
				}
			}
		case "apache", "httpd":
			add("apache")
		case "php":
			add("php")
		case "mariadb", "maria":
			add("mariadb")
		case "mysql":
			add("mysql")
		case "postgresql", "postgres", "pg":
			add("postgresql")
		case "mssql", "sqlserver", "sqlcmd":
			add("mssql")
		case "redis":
			add("redis")
		case "mailpit", "mail":
			add("mailpit")
		}
	}
	return out
}

func installSelectedComponents(
	ctx context.Context,
	opts installOptions,
	basePct, endPct int,
	step func(pct int, msg string),
	status func(pct int, msg string),
) (installedPrereqs []string, installedEngines []string, warnings []string) {
	total := len(opts.prereqs) + len(opts.engines)
	if total == 0 {
		return nil, nil, nil
	}
	if step == nil {
		step = func(int, string) {}
	}
	if status == nil {
		status = func(int, string) {}
	}

	paths := core.NewPaths(opts.dir)
	_ = paths.Ensure()
	st := core.LoadSettings(opts.dir)
	cat := core.LoadCatalog(paths)
	selfExe, _ := os.Executable()
	offlineDirs := discoverOfflineDirs(opts.offlineDir, selfExe, opts.dir)

	span := endPct - basePct
	if span < total {
		span = total
	}
	doneIdx := 0
	sliceFor := func(idx int) (int, int) {
		lo := basePct + (span*idx)/total
		hi := basePct + (span*(idx+1))/total
		if hi <= lo {
			hi = lo + 1
		}
		if hi > endPct {
			hi = endPct
		}
		return lo, hi
	}

	// 1. Install selected Windows prerequisites first so engines (and Repair) see VC++/LocalDB immediately.
	for _, pid := range opts.prereqs {
		lo, hi := sliceFor(doneIdx)
		doneIdx++

		label := prereqDisplayLabel(pid)
		step(lo, "Installing prerequisite: "+label)
		err := core.InstallPrerequisiteWithOffline(ctx, paths, pid, offlineDirs, opts.offlineOnly, func(p core.Progress) {
			if p.Phase == "downloading" && p.Total > 0 {
				subPct := lo + int(float64(hi-lo)*0.7*float64(p.Received)/float64(p.Total))
				status(subPct, fmt.Sprintf("Downloading %s (%d / %d MB)...", label, p.Received/(1024*1024), p.Total/(1024*1024)))
			} else if p.Phase == "installing" {
				subPct := lo + int(float64(hi-lo)*0.8)
				status(subPct, p.Message)
			}
		})
		if err != nil {
			w := fmt.Sprintf("%s: %v", label, err)
			warnings = append(warnings, w)
			step(hi, "WARNING: "+w)
			continue
		}
		installedPrereqs = append(installedPrereqs, label)
		step(hi, label+" ready")
	}

	// 2. Install selected database & web engines (offline pack first, then online catalog download).
	setActiveVersion := func(engine, folderName string) {
		_ = st.Update(func(s *core.Settings) {
			if es := s.Engines[engine]; es != nil {
				es.Version = folderName
			}
		})
	}

	for _, engID := range opts.engines {
		lo, hi := sliceFor(doneIdx)
		doneIdx++

		entries := cat[engID]
		ver := ""
		dlURL := ""
		if len(entries) > 0 {
			ver = entries[0].Version
			dlURL = entries[0].URL
		}

		// Skip gracefully if this exact version is already installed in bin/<engine>.
		if ver != "" && dirExistsLocal(filepath.Join(paths.EngineBin(engID), engID+"-"+ver)) {
			step(hi, fmt.Sprintf("%s %s already installed (kept)", strings.ToUpper(engID), ver))
			installedEngines = append(installedEngines, fmt.Sprintf("%s %s", engID, ver))
			continue
		}

		// Prefer pre-extracted offline directory, then offline zip archive, then online download.
		if offDir, offVer, ok := core.FindOfflineEngineDir(engID, ver, offlineDirs); ok {
			if offVer != "" {
				ver = offVer
			}
			step(lo, fmt.Sprintf("Installing %s %s from offline bundle...", engID, ver))
			name, err := core.InstallOfflineEngineDir(paths, engID, ver, offDir, setActiveVersion)
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "already installed") {
					step(hi, fmt.Sprintf("%s %s already installed (kept)", engID, ver))
					installedEngines = append(installedEngines, fmt.Sprintf("%s %s", engID, ver))
				} else {
					w := fmt.Sprintf("%s %s (offline dir): %v", engID, ver, err)
					warnings = append(warnings, w)
					step(hi, "WARNING: "+w)
				}
				continue
			}
			installedEngines = append(installedEngines, name)
			step(hi, name+" installed (offline)")
			continue
		}

		dl := core.NewDownloader(paths, func(p core.Progress) {
			switch p.Phase {
			case "downloading":
				if p.Total > 0 {
					subPct := lo + int(float64(hi-lo)*0.7*float64(p.Received)/float64(p.Total))
					status(subPct, fmt.Sprintf("Downloading %s %s (%d / %d MB)...", p.Engine, p.Version, p.Received/(1024*1024), p.Total/(1024*1024)))
				} else {
					status(lo, fmt.Sprintf("Downloading %s %s...", p.Engine, p.Version))
				}
			case "extracting":
				subPct := lo + int(float64(hi-lo)*0.85)
				if p.Total > 0 {
					subPct = lo + int(float64(hi-lo)*(0.7+0.25*float64(p.Received)/float64(p.Total)))
				}
				status(subPct, fmt.Sprintf("Extracting %s %s...", p.Engine, p.Version))
			}
		}, setActiveVersion)

		if offZip, offVer, ok := core.FindOfflineEngineArchive(engID, ver, offlineDirs); ok {
			if offVer != "" {
				ver = offVer
			}
			step(lo, fmt.Sprintf("Extracting %s %s from offline archive (%s)...", engID, ver, filepath.Base(offZip)))
			actualVer, err := dl.InstallZipSync(ctx, engID, ver, offZip)
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "already installed") {
					step(hi, fmt.Sprintf("%s %s already installed (kept)", engID, ver))
					installedEngines = append(installedEngines, fmt.Sprintf("%s %s", engID, ver))
				} else {
					w := fmt.Sprintf("%s %s (offline zip): %v", engID, ver, err)
					warnings = append(warnings, w)
					step(hi, "WARNING: "+w)
				}
				continue
			}
			installedEngines = append(installedEngines, engID+"-"+actualVer)
			step(hi, fmt.Sprintf("%s-%s installed (offline)", engID, actualVer))
			continue
		}

		if opts.offlineOnly {
			w := fmt.Sprintf("%s %s: archive not found in offline bundle (offline-only mode)", engID, ver)
			warnings = append(warnings, w)
			step(hi, "WARNING: "+w)
			continue
		}
		if ver == "" || dlURL == "" {
			w := fmt.Sprintf("%s: no download entry found in catalog", engID)
			warnings = append(warnings, w)
			step(hi, "WARNING: "+w)
			continue
		}

		step(lo, fmt.Sprintf("Downloading & installing %s %s...", engID, ver))
		if err := dl.InstallSync(ctx, engID, ver, dlURL); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already installed") {
				step(hi, fmt.Sprintf("%s %s already installed (kept)", engID, ver))
				installedEngines = append(installedEngines, fmt.Sprintf("%s %s", engID, ver))
			} else {
				w := fmt.Sprintf("%s %s: %v", engID, ver, err)
				warnings = append(warnings, w)
				step(hi, "WARNING: "+w)
			}
			continue
		}
		installedEngines = append(installedEngines, engID+"-"+ver)
		step(hi, fmt.Sprintf("%s-%s installed", engID, ver))
	}

	return installedPrereqs, installedEngines, warnings
}

func prereqDisplayLabel(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case core.PrereqIDWebView2:
		return "WebView2 Runtime"
	case core.PrereqIDVCRedistX64:
		return "VC++ x64 Redistributable"
	case core.PrereqIDMSSQLLocalDB:
		return "SQL Server LocalDB"
	default:
		return id
	}
}

func dirExistsLocal(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
