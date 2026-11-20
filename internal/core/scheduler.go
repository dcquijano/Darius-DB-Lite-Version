//go:build windows

package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const windowsScheduleTaskName = "DariusDB-AutoSchedule"

// BackupSyncSchedule defines a daily Auto-Sync (e.g. Pull Cloud -> Local) and/or
// daily Auto-Backup snapshot schedule (default 02:00 AM) with 7-day snapshot purge.
type BackupSyncSchedule struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	CloudID          string `json:"cloudId,omitempty"`
	CloudDB          string `json:"cloudDb,omitempty"`
	LocalEngine      string `json:"localEngine"`      // mariadb | mysql | postgresql | mssql
	LocalDB          string `json:"localDb"`
	AutoSync         bool   `json:"autoSync"`
	SyncDirection    string `json:"syncDirection"`    // pull | push (default pull: Cloud -> Local)
	SyncTime         string `json:"syncTime"`         // HH:MM 24h local time (default 02:00)
	AutoBackup       bool   `json:"autoBackup"`
	BackupTime       string `json:"backupTime"`       // HH:MM 24h local time (default 02:00)
	BackupSource     string `json:"backupSource"`     // cloud | local | both (default cloud if CloudID set else local)
	RetentionDays    int    `json:"retentionDays"`    // days before snapshot purge (default 7 = 1 week)
	BackupDir        string `json:"backupDir,omitempty"`
	CreatedAt        string `json:"createdAt,omitempty"`
	LastSyncAt       string `json:"lastSyncAt,omitempty"`
	LastSyncDate     string `json:"lastSyncDate,omitempty"`
	LastSyncStatus   string `json:"lastSyncStatus,omitempty"`
	LastBackupAt     string `json:"lastBackupAt,omitempty"`
	LastBackupDate   string `json:"lastBackupDate,omitempty"`
	LastBackupStatus string `json:"lastBackupStatus,omitempty"`
	LastPurgedCount  int    `json:"lastPurgedCount,omitempty"`
}

// BackupSnapshotInfo describes an automated snapshot file on disk and its purge lifetime.
type BackupSnapshotInfo struct {
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	Size      int64   `json:"size"`
	SizeText  string  `json:"sizeText"`
	CreatedAt string  `json:"createdAt"`
	AgeDays   float64 `json:"ageDays"`
	ExpiresIn string  `json:"expiresIn"`
	Expired   bool    `json:"expired"`
}

func newScheduleID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "sched-" + hex.EncodeToString(b)
}

func normalizeHHMM(raw string) string {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return "02:00"
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return "02:00"
	}
	return fmt.Sprintf("%02d:%02d", hh, mm)
}

func (s BackupSyncSchedule) Normalized() BackupSyncSchedule {
	out := s
	if strings.TrimSpace(out.ID) == "" {
		out.ID = newScheduleID()
	}
	out.Name = strings.TrimSpace(out.Name)
	out.CloudID = strings.TrimSpace(out.CloudID)
	out.CloudDB = strings.TrimSpace(out.CloudDB)
	out.LocalEngine = strings.ToLower(strings.TrimSpace(out.LocalEngine))
	if out.LocalEngine == "" {
		out.LocalEngine = "mssql"
	}
	out.LocalDB = strings.TrimSpace(out.LocalDB)
	out.SyncDirection = strings.ToLower(strings.TrimSpace(out.SyncDirection))
	if out.SyncDirection != "push" {
		out.SyncDirection = "pull"
	}
	out.SyncTime = normalizeHHMM(out.SyncTime)
	out.BackupTime = normalizeHHMM(out.BackupTime)
	out.BackupSource = strings.ToLower(strings.TrimSpace(out.BackupSource))
	if out.BackupSource != "local" && out.BackupSource != "cloud" && out.BackupSource != "both" {
		if out.CloudID != "" {
			out.BackupSource = "cloud"
		} else {
			out.BackupSource = "local"
		}
	}
	if out.RetentionDays <= 0 {
		out.RetentionDays = 7
	}
	out.BackupDir = strings.TrimSpace(out.BackupDir)
	if out.Name == "" {
		if out.CloudDB != "" {
			out.Name = out.CloudDB + " Daily"
		} else if out.LocalDB != "" {
			out.Name = out.LocalDB + " Daily"
		} else {
			out.Name = "Daily Sync & Backup"
		}
	}
	if out.CreatedAt == "" {
		out.CreatedAt = time.Now().Format("2006-01-02 15:04")
	}
	return out
}

// ScheduleBackupDir returns the folder where automated backup snapshots are saved.
func ScheduleBackupDir(p *Paths, sched BackupSyncSchedule) string {
	if d := strings.TrimSpace(sched.BackupDir); d != "" {
		return d
	}
	slug := cleanDBNameRe.ReplaceAllString(sched.Name, "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		slug = sched.ID
	}
	base := filepath.Join(p.Root, "backups")
	if p != nil && p.Backups != "" {
		base = p.Backups
	}
	return filepath.Join(base, slug)
}

var snapshotTimestampRe = regexp.MustCompile(`_(\d{8})_(\d{6})\.(zip|sql|bak)$`)

func parseSnapshotTime(name string, modTime time.Time) time.Time {
	if m := snapshotTimestampRe.FindStringSubmatch(strings.ToLower(name)); m != nil {
		if t, err := time.ParseInLocation("20060102_150405", m[1]+"_"+m[2], time.Local); err == nil {
			return t
		}
	}
	return modTime
}

func isSnapshotArchive(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".zip" || ext == ".sql" || ext == ".bak"
}

// PurgeOldSnapshots deletes backup snapshots in dir whose age meets or exceeds
// retentionDays (default 7 days / 1 week).
func PurgeOldSnapshots(dir string, retentionDays int, now time.Time) (int, []string, error) {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	if !dirExists(dir) {
		return 0, nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil, err
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	var purged []string
	for _, e := range entries {
		if e.IsDir() || !isSnapshotArchive(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		snapTime := parseSnapshotTime(e.Name(), info.ModTime())
		if !snapTime.After(cutoff) {
			full := filepath.Join(dir, e.Name())
			if rerr := os.Remove(full); rerr == nil {
				purged = append(purged, e.Name())
			}
		}
	}
	return len(purged), purged, nil
}

// ListSnapshots returns all stored snapshots for sched ordered newest-first with age & retention info.
func ListSnapshots(p *Paths, sched BackupSyncSchedule, now time.Time) ([]BackupSnapshotInfo, error) {
	dir := ScheduleBackupDir(p, sched)
	if !dirExists(dir) {
		return []BackupSnapshotInfo{}, nil
	}
	ret := sched.RetentionDays
	if ret <= 0 {
		ret = 7
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type item struct {
		t    time.Time
		info BackupSnapshotInfo
	}
	var list []item
	for _, e := range entries {
		if e.IsDir() || !isSnapshotArchive(e.Name()) {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		snapTime := parseSnapshotTime(e.Name(), st.ModTime())
		age := now.Sub(snapTime)
		if age < 0 {
			age = 0
		}
		ageDays := age.Hours() / 24.0
		expiresAt := snapTime.Add(time.Duration(ret) * 24 * time.Hour)
		rem := expiresAt.Sub(now)
		expired := rem <= 0
		expText := "Due for purge"
		if !expired {
			daysLeft := int(rem.Hours() / 24)
			hrsLeft := int(rem.Hours()) % 24
			if daysLeft > 0 {
				expText = fmt.Sprintf("Purges in %dd %dh", daysLeft, hrsLeft)
			} else {
				expText = fmt.Sprintf("Purges in %dh", hrsLeft)
			}
		}
		list = append(list, item{
			t: snapTime,
			info: BackupSnapshotInfo{
				Name:      e.Name(),
				Path:      filepath.Join(dir, e.Name()),
				Size:      st.Size(),
				SizeText:  formatByteSize(st.Size()),
				CreatedAt: snapTime.Format("2006-01-02 15:04"),
				AgeDays:   ageDays,
				ExpiresIn: expText,
				Expired:   expired,
			},
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].t.After(list[j].t) })
	out := make([]BackupSnapshotInfo, len(list))
	for i := range list {
		out[i] = list[i].info
	}
	return out, nil
}

// isScheduleDue checks whether a daily HH:MM task should run right now for today's date,
// preventing duplicate daily executions and avoiding retroactive triggers on the creation day
// if the schedule was created after today's target time.
func isScheduleDue(now time.Time, hhmm, lastDate, createdAt string) bool {
	today := now.Format("2006-01-02")
	if lastDate == today {
		return false
	}
	norm := normalizeHHMM(hhmm)
	parts := strings.Split(norm, ":")
	hh, _ := strconv.Atoi(parts[0])
	mm, _ := strconv.Atoi(parts[1])
	target := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if now.Before(target) {
		return false
	}
	if lastDate == "" && createdAt != "" {
		if ct, err := time.ParseInLocation("2006-01-02 15:04", createdAt, now.Location()); err == nil {
			if ct.Format("2006-01-02") == today && ct.After(target) {
				return false
			}
		}
	}
	return true
}

// Scheduler runs enabled daily Auto-Sync (Pull Cloud -> Local / Push) and daily Auto-Backup
// + 1-week snapshot purge tasks in the background.
type Scheduler struct {
	p          *Paths
	st         *SettingsStore
	onChange   func()
	onProgress func(CloudSyncProgress)

	stopOnce sync.Once
	stopCh   chan struct{}
	runMu    sync.Mutex
}

func NewScheduler(p *Paths, st *SettingsStore, onChange func(), onProgress func(CloudSyncProgress)) *Scheduler {
	return &Scheduler{
		p:          p,
		st:         st,
		onChange:   onChange,
		onProgress: onProgress,
		stopCh:     make(chan struct{}),
	}
}

func (sc *Scheduler) Start() {
	go func() {
		// Brief startup grace so local engines finish adopting/starting first
		select {
		case <-sc.stopCh:
			return
		case <-time.After(8 * time.Second):
		}
		sc.RunDueSchedules(time.Now())
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-sc.stopCh:
				return
			case now := <-tick.C:
				sc.RunDueSchedules(now)
			}
		}
	}()
}

func (sc *Scheduler) Stop() {
	sc.stopOnce.Do(func() { close(sc.stopCh) })
}

func (sc *Scheduler) appendLog(line string) {
	if sc.p == nil || sc.p.Logs == "" {
		return
	}
	_ = os.MkdirAll(sc.p.Logs, 0o755)
	f, err := os.OpenFile(filepath.Join(sc.p.Logs, "scheduler.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}

func (sc *Scheduler) resolveTargets(sched BackupSyncSchedule) (srcEng, dstEng string, srcEs, dstEs EngineSettings, cloudConn *CloudConnection, err error) {
	settings := sc.st.Get()
	if sched.CloudID != "" {
		for i := range settings.CloudConnections {
			if settings.CloudConnections[i].ID == sched.CloudID {
				c := settings.CloudConnections[i]
				cloudConn = &c
				break
			}
		}
	}
	localEs := EngineSettings{}
	if e := settings.Engines[sched.LocalEngine]; e != nil {
		localEs = *e
	}
	if cloudConn != nil {
		cloudEs := cloudConn.ToEngineSettings()
		return sched.LocalEngine, cloudConn.Engine, localEs, cloudEs, cloudConn, nil
	}
	return sched.LocalEngine, "", localEs, EngineSettings{}, nil, nil
}

// RunDueSchedules evaluates all enabled schedules for `now`, running due backups (with 7-day purge)
// and due daily syncs once per calendar day. Also purges any expired snapshots even outside backup hours.
func (sc *Scheduler) RunDueSchedules(now time.Time) {
	sc.runMu.Lock()
	defer sc.runMu.Unlock()

	schedules := sc.st.Get().Schedules
	for _, raw := range schedules {
		sched := raw.Normalized()
		if !sched.Enabled {
			continue
		}
		// Always enforce 1-week snapshot retention even if today's backup hasn't triggered yet
		if sched.AutoBackup {
			dir := ScheduleBackupDir(sc.p, sched)
			if purgedN, _, _ := PurgeOldSnapshots(dir, sched.RetentionDays, now); purgedN > 0 {
				sc.appendLog(fmt.Sprintf("%s: purged %d expired snapshot(s) (>= %d days old) from %s", sched.Name, purgedN, sched.RetentionDays, dir))
				_ = sc.updateScheduleStatus(sched.ID, func(cur *BackupSyncSchedule) {
					cur.LastPurgedCount = purgedN
				})
			}
		}
		doBackup := sched.AutoBackup && isScheduleDue(now, sched.BackupTime, sched.LastBackupDate, sched.CreatedAt)
		doSync := sched.AutoSync && isScheduleDue(now, sched.SyncTime, sched.LastSyncDate, sched.CreatedAt)
		if !doBackup && !doSync {
			continue
		}
		_, _ = sc.executeLocked(sched.ID, doSync, doBackup, now)
	}
}

// RunScheduleNow executes a schedule immediately by ID ("both", "sync", or "backup").
func (sc *Scheduler) RunScheduleNow(id, mode string) (string, error) {
	sc.runMu.Lock()
	defer sc.runMu.Unlock()
	doSync := mode == "both" || mode == "sync" || mode == ""
	doBackup := mode == "both" || mode == "backup" || mode == ""
	return sc.executeLocked(id, doSync, doBackup, time.Now())
}

func (sc *Scheduler) updateScheduleStatus(id string, fn func(*BackupSyncSchedule)) error {
	err := sc.st.Update(func(s *Settings) {
		for i := range s.Schedules {
			if s.Schedules[i].ID == id {
				fn(&s.Schedules[i])
				return
			}
		}
	})
	if err == nil && sc.onChange != nil {
		sc.onChange()
	}
	return err
}

func (sc *Scheduler) executeLocked(id string, doSync, doBackup bool, now time.Time) (string, error) {
	var sched BackupSyncSchedule
	found := false
	for _, s := range sc.st.Get().Schedules {
		if s.ID == id {
			sched = s.Normalized()
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("schedule %q not found", id)
	}

	localEng, cloudEng, localEs, cloudEs, cloudConn, err := sc.resolveTargets(sched)
	if err != nil {
		return "", err
	}

	today := now.Format("2006-01-02")
	stamp := now.Format("20060102_150405")
	humanNow := now.Format("2006-01-02 15:04")
	var parts []string
	var firstErr error

	// 1) Auto-Sync (default Pull Cloud -> Local)
	if doSync && sched.AutoSync {
		if cloudConn == nil {
			err := errors.New("cloud profile not found for auto-sync")
			firstErr = err
			_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
				cur.LastSyncAt, cur.LastSyncStatus = humanNow, "err: "+err.Error()
			})
			sc.appendLog(fmt.Sprintf("%s SYNC FAILED: %v", sched.Name, err))
		} else {
			cloudDB := fallbackStr(sched.CloudDB, cloudConn.Database)
			localDB := fallbackStr(sched.LocalDB, cloudDB)
			var msg string
			var syncErr error
			if sched.SyncDirection == "push" {
				msg, syncErr = CloudSyncDatabaseWithProgress(sc.p, localEng, localEs, localDB, cloudEng, cloudEs, cloudDB, sc.onProgress)
			} else {
				msg, syncErr = CloudSyncDatabaseWithProgress(sc.p, cloudEng, cloudEs, cloudDB, localEng, localEs, localDB, sc.onProgress)
			}
			if syncErr != nil {
				firstErr = syncErr
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastSyncAt, cur.LastSyncStatus = humanNow, "err: "+syncErr.Error()
				})
				sc.appendLog(fmt.Sprintf("%s SYNC FAILED (%s): %v", sched.Name, sched.SyncDirection, syncErr))
			} else {
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastSyncAt, cur.LastSyncDate, cur.LastSyncStatus = humanNow, today, "ok · "+msg
				})
				sc.appendLog(fmt.Sprintf("%s SYNC OK (%s): %s", sched.Name, sched.SyncDirection, msg))
				parts = append(parts, msg)
			}
		}
	}

	// 2) Auto-Backup (.zip snapshot) + 1-week retention purge
	if doBackup && sched.AutoBackup {
		dir := ScheduleBackupDir(sc.p, sched)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
				cur.LastBackupAt, cur.LastBackupStatus = humanNow, "err: "+err.Error()
			})
			sc.appendLog(fmt.Sprintf("%s BACKUP FAILED: %v", sched.Name, err))
		} else {
			var snapFiles []string
			var bErr error
			runZip := func(eng string, es EngineSettings, db string, suffix string) {
				if bErr != nil || db == "" {
					return
				}
				safe := cleanDBNameRe.ReplaceAllString(db, "_")
				if safe == "" {
					safe = "database"
				}
				outZip := filepath.Join(dir, fmt.Sprintf("%s%s_%s.zip", safe, suffix, stamp))
				bErr = ExportDatabaseFullZipWithProgress(sc.p, eng, es, db, nil, true, true, outZip, nil)
				if bErr == nil {
					snapFiles = append(snapFiles, filepath.Base(outZip))
				}
			}

			switch sched.BackupSource {
			case "both":
				if cloudConn != nil {
					runZip(cloudEng, cloudEs, fallbackStr(sched.CloudDB, cloudConn.Database), "_cloud")
				}
				if sched.LocalDB != "" {
					runZip(localEng, localEs, sched.LocalDB, "_local")
				}
			case "cloud":
				if cloudConn != nil {
					runZip(cloudEng, cloudEs, fallbackStr(sched.CloudDB, cloudConn.Database), "")
				} else if sched.LocalDB != "" {
					runZip(localEng, localEs, sched.LocalDB, "")
				} else {
					bErr = errors.New("cloud profile not found for scheduled backup")
				}
			default: // local
				if sched.LocalDB != "" {
					runZip(localEng, localEs, sched.LocalDB, "")
				} else {
					bErr = errors.New("local database name is required for backup")
				}
			}

			purgedN, _, _ := PurgeOldSnapshots(dir, sched.RetentionDays, now)
			if bErr != nil {
				if firstErr == nil {
					firstErr = bErr
				}
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastBackupAt, cur.LastBackupStatus, cur.LastPurgedCount = humanNow, "err: "+bErr.Error(), purgedN
				})
				sc.appendLog(fmt.Sprintf("%s BACKUP FAILED: %v", sched.Name, bErr))
			} else {
				statusMsg := fmt.Sprintf("ok · %s", strings.Join(snapFiles, ", "))
				if purgedN > 0 {
					statusMsg += fmt.Sprintf(" (purged %d >%dd)", purgedN, sched.RetentionDays)
				}
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastBackupAt, cur.LastBackupDate, cur.LastBackupStatus, cur.LastPurgedCount = humanNow, today, statusMsg, purgedN
				})
				sc.appendLog(fmt.Sprintf("%s BACKUP OK: %s", sched.Name, statusMsg))
				parts = append(parts, "Backup saved: "+strings.Join(snapFiles, ", "))
				if purgedN > 0 {
					parts = append(parts, fmt.Sprintf("Purged %d expired snapshot(s) (>= %d days)", purgedN, sched.RetentionDays))
				}
			}
		}
	}

	if firstErr != nil {
		return strings.Join(parts, " · "), firstErr
	}
	if len(parts) == 0 {
		return "No enabled action ran", nil
	}
	return strings.Join(parts, " · "), nil
}

// IsWindowsScheduleTaskRegistered checks if DariusDB-AutoSchedule is registered in Windows Task Scheduler.
func IsWindowsScheduleTaskRegistered() (bool, string) {
	cmd := exec.Command("schtasks.exe", "/Query", "/TN", windowsScheduleTaskName, "/FO", "LIST")
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "next run time:") {
			return true, strings.TrimSpace(line)
		}
	}
	return true, "Registered"
}

// RegisterWindowsScheduleTask creates or removes a daily Windows Task Scheduler entry
// that wakes `darius.exe schedule run` at timeHHMM (default 02:00) even if the GUI is closed.
func RegisterWindowsScheduleTask(p *Paths, timeHHMM string, enable bool) error {
	if !enable {
		cmd := exec.Command("schtasks.exe", "/Delete", "/TN", windowsScheduleTaskName, "/F")
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(out)), "cannot find") && !strings.Contains(strings.ToLower(string(out)), "does not exist") {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return errors.New(msg)
		}
		return nil
	}
	norm := normalizeHHMM(timeHHMM)
	cliExe := filepath.Join(p.Root, "darius.exe")
	if !fileExists(cliExe) {
		if exe, err := os.Executable(); err == nil {
			cliExe = exe
		}
	}
	var tr string
	if strings.EqualFold(filepath.Base(cliExe), "darius.exe") {
		tr = fmt.Sprintf(`"%s" schedule run`, cliExe)
	} else {
		tr = fmt.Sprintf(`"%s" --cli schedule run`, cliExe)
	}
	cmd := exec.Command("schtasks.exe",
		"/Create",
		"/TN", windowsScheduleTaskName,
		"/SC", "DAILY",
		"/ST", norm,
		"/TR", tr,
		"/F",
	)
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}
