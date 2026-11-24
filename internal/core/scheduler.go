//go:build windows

package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	AutoNameLocalDB  bool   `json:"autoNameLocalDb,omitempty"`
	AutoNameFormat   string `json:"autoNameFormat,omitempty"` // date_day_year_ts | month_day_year_ts | date_year_ts | date_day_year
	AutoSync             bool   `json:"autoSync"`
	SyncDirection        string `json:"syncDirection"` // pull | pull_folder | push (default pull: Cloud -> Local DB)
	SyncFolder           string `json:"syncFolder,omitempty"`
	SyncTime             string `json:"syncTime"` // HH:MM 24h local time (default 02:00)
	AutoBackup           bool   `json:"autoBackup"`
	AutoNameBackup       bool   `json:"autoNameBackup,omitempty"`
	AutoNameBackupFormat string `json:"autoNameBackupFormat,omitempty"` // date_day_year_ts | month_day_year_ts | date_year_ts | date_day_year
	BackupTime           string `json:"backupTime"`                     // HH:MM 24h local time (default 02:00)
	BackupSource     string `json:"backupSource"`     // cloud | cloud_folder | local | both (default cloud if CloudID set else local)
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
	Name           string  `json:"name"`
	Path           string  `json:"path"`
	Size           int64   `json:"size"`
	SizeText       string  `json:"sizeText"`
	CreatedAt      string  `json:"createdAt"`
	CreatedDisplay string  `json:"createdDisplay,omitempty"` // e.g. Mon, Nov 23, 2026 · 01:31:08 PM
	AgeDays        float64 `json:"ageDays"`
	ExpiresIn      string  `json:"expiresIn"`
	PurgeAt        string  `json:"purgeAt,omitempty"`    // e.g. Mon, Nov 30, 2026 · 01:31:08 PM
	PurgeStamp     string  `json:"purgeStamp,omitempty"` // e.g. 2026_11_30_Mon_133108
	Expired        bool    `json:"expired"`
}

func formatPurgeDateGuide(t time.Time) string {
	return t.Format("Mon, Jan 02, 2006 · 03:04:05 PM")
}

// ScheduleRunProgress describes live 0-100% progress during a manual or scheduled
// Auto-Sync / Auto-Backup execution.
type ScheduleRunProgress struct {
	ScheduleID string `json:"scheduleId"`
	Name       string `json:"name"`
	Trigger    string `json:"trigger"` // scheduled | manual
	Mode       string `json:"mode"`    // sync | backup | both
	Percent    int    `json:"percent"` // 0..100 monotonic
	State      string `json:"state"`   // running | done | error
	Stage      string `json:"stage"`
	Detail     string `json:"detail"`
}

// SyncLogEntry records a persistent traceable audit entry for every scheduled,
// manual, or modal database sync, backup, and retention purge.
type SyncLogEntry struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	ScheduleID string `json:"scheduleId,omitempty"`
	Name       string `json:"name"`
	Trigger    string `json:"trigger"`             // scheduled | manual | sync-modal
	Action     string `json:"action"`              // sync | backup | purge
	Direction  string `json:"direction,omitempty"` // pull | push
	Source     string `json:"source,omitempty"`
	Target     string `json:"target,omitempty"`
	Status     string `json:"status"` // started | ok | error | cancelled
	DurationMs int64  `json:"durationMs,omitempty"`
	Detail     string `json:"detail"`
}

var syncLogMu sync.Mutex

const maxSyncLogEntries = 500

func syncLogJSONLPath(p *Paths) string {
	if p == nil || p.Logs == "" {
		return ""
	}
	return filepath.Join(p.Logs, "sync-history.jsonl")
}

func syncLogTextPath(p *Paths) string {
	if p == nil || p.Logs == "" {
		return ""
	}
	return filepath.Join(p.Logs, "scheduler.log")
}

// AppendSyncLog persists a structured trace entry to logs/sync-history.jsonl and a
// human-readable line to logs/scheduler.log (bounded to maxSyncLogEntries).
func AppendSyncLog(p *Paths, entry SyncLogEntry) SyncLogEntry {
	if entry.ID == "" {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		entry.ID = "log-" + hex.EncodeToString(b)
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format("2006-01-02 15:04:05")
	}
	if p == nil || p.Logs == "" {
		return entry
	}
	syncLogMu.Lock()
	defer syncLogMu.Unlock()

	_ = os.MkdirAll(p.Logs, 0o755)

	// 1. Append human-readable trace line to logs/scheduler.log
	if f, err := os.OpenFile(syncLogTextPath(p), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		route := ""
		if entry.Source != "" || entry.Target != "" {
			route = fmt.Sprintf(" | %s -> %s", entry.Source, entry.Target)
		}
		dur := ""
		if entry.DurationMs > 0 {
			dur = fmt.Sprintf(" | %.1fs", float64(entry.DurationMs)/1000.0)
		}
		_, _ = fmt.Fprintf(f, "[%s] [%s] [%s] [%s] %s%s%s | %s\n",
			entry.Timestamp,
			strings.ToUpper(fallbackStr(entry.Trigger, "manual")),
			strings.ToUpper(fallbackStr(entry.Action, "sync")),
			strings.ToUpper(fallbackStr(entry.Status, "ok")),
			fallbackStr(entry.Name, "Sync"),
			route,
			dur,
			entry.Detail,
		)
		f.Close()
	}

	// 2. Append structured JSONL entry to logs/sync-history.jsonl
	jlPath := syncLogJSONLPath(p)
	if b, err := json.Marshal(entry); err == nil {
		if f, err := os.OpenFile(jlPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_, _ = f.Write(b)
			_, _ = f.WriteString("\n")
			f.Close()
		}
	}
	// Bound JSONL file size so history never grows unbounded
	if raw, err := os.ReadFile(jlPath); err == nil {
		lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
		if len(lines) > maxSyncLogEntries+100 {
			kept := lines[len(lines)-maxSyncLogEntries:]
			_ = os.WriteFile(jlPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
		}
	}
	return entry
}

// ReadSyncLogs returns recent sync/backup trace entries newest-first, optionally
// filtered by scheduleID.
func ReadSyncLogs(p *Paths, scheduleID string, limit int) ([]SyncLogEntry, error) {
	if limit <= 0 || limit > maxSyncLogEntries {
		limit = 200
	}
	if p == nil || p.Logs == "" {
		return []SyncLogEntry{}, nil
	}
	syncLogMu.Lock()
	raw, err := os.ReadFile(syncLogJSONLPath(p))
	syncLogMu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			return []SyncLogEntry{}, nil
		}
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	out := make([]SyncLogEntry, 0, min(len(lines), limit))
	filterID := strings.TrimSpace(scheduleID)
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var e SyncLogEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if filterID != "" && e.ScheduleID != filterID {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ClearSyncLogs clears both logs/sync-history.jsonl and logs/scheduler.log.
func ClearSyncLogs(p *Paths) error {
	if p == nil || p.Logs == "" {
		return nil
	}
	syncLogMu.Lock()
	defer syncLogMu.Unlock()
	_ = os.Remove(syncLogJSONLPath(p))
	_ = os.WriteFile(syncLogTextPath(p), []byte{}, 0o644)
	return nil
}

// ClearScheduleStatusFields clears the displayed LastSync/LastBackup status banner fields
// while keeping LastSyncDate/LastBackupDate intact so today's automatic slot stays respected.
func ClearScheduleStatusFields(cur *BackupSyncSchedule, mode string) error {
	if cur == nil {
		return errors.New("schedule is nil")
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "sync":
		cur.LastSyncAt = ""
		cur.LastSyncStatus = ""
	case "backup":
		cur.LastBackupAt = ""
		cur.LastBackupStatus = ""
	case "both", "":
		cur.LastSyncAt = ""
		cur.LastSyncStatus = ""
		cur.LastBackupAt = ""
		cur.LastBackupStatus = ""
	default:
		return fmt.Errorf("unsupported clear mode %q", mode)
	}
	return nil
}

// ResetScheduleDateForEditedTime computes the updated LastSyncDate / LastBackupDate
// when a schedule's target HH:MM time or enabled flag is changed:
//   - If the new time is still ahead of `now` today, returns "" so it runs today on schedule.
//   - If the new time has already passed today, returns today's date to prevent a retroactive run.
//   - If the time and enabled state did not change, preserves curLastDate.
func ResetScheduleDateForEditedTime(now time.Time, oldHHMM, newHHMM, curLastDate string, wasActive, nowActive bool) string {
	if !nowActive {
		return curLastDate
	}
	if normalizeHHMM(oldHHMM) == normalizeHHMM(newHHMM) && wasActive {
		return curLastDate
	}
	norm := normalizeHHMM(newHHMM)
	parts := strings.Split(norm, ":")
	hh, _ := strconv.Atoi(parts[0])
	mm, _ := strconv.Atoi(parts[1])
	target := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if now.Before(target) {
		return ""
	}
	return now.Format("2006-01-02")
}

func mapProgressRange(innerPct, startPct, endPct int) int {
	if innerPct < 0 {
		innerPct = 0
	}
	if innerPct > 100 {
		innerPct = 100
	}
	if endPct <= startPct {
		return startPct
	}
	return startPct + (innerPct*(endPct-startPct))/100
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

var autoPullDBCleanRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// NormalizeAutoNameFormat returns a supported Pull Cloud -> Local auto-naming format.
func NormalizeAutoNameFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "month_day_year_ts":
		return "month_day_year_ts"
	case "date_year_ts":
		return "date_year_ts"
	case "date_day_year":
		return "date_day_year"
	default:
		return "date_day_year_ts"
	}
}

// FormatAutoPullLocalDBName builds a SQL-safe Local Database name (<= 63 chars,
// matching nameRe across MySQL, MariaDB, PostgreSQL, and MS SQL) from a base name
// and local date/day/year/timestamp, e.g. "alegria_2026_11_22_Sun_132136".
func FormatAutoPullLocalDBName(base, format string, now time.Time) string {
	clean := autoPullDBCleanRe.ReplaceAllString(strings.TrimSpace(base), "_")
	clean = strings.Trim(clean, "_-")
	if clean == "" {
		clean = "pulled_db"
	}
	if clean[0] >= '0' && clean[0] <= '9' {
		clean = "db_" + clean
	}
	var suffix string
	switch NormalizeAutoNameFormat(format) {
	case "month_day_year_ts":
		suffix = now.Format("_Jan_02_2006_Mon_150405")
	case "date_year_ts":
		suffix = now.Format("_2006_01_02_150405")
	case "date_day_year":
		suffix = now.Format("_2006_01_02_Mon")
	default:
		suffix = now.Format("_2006_01_02_Mon_150405")
	}
	maxBase := 63 - len(suffix)
	if maxBase < 1 {
		maxBase = 1
	}
	if len(clean) > maxBase {
		clean = strings.TrimRight(clean[:maxBase], "_-")
		if clean == "" {
			clean = "db"
		}
	}
	return clean + suffix
}

// FormatAutoBackupZipName builds a dated .zip snapshot filename using the same
// Date/Day/Year/Timestamp formats as Auto-Sync pull naming, e.g.
// "alegria_2026_11_23_Mon_020000.zip" (with optional "_cloud"/"_local" suffix).
func FormatAutoBackupZipName(base, suffix, format string, now time.Time) string {
	clean := autoPullDBCleanRe.ReplaceAllString(strings.TrimSpace(base), "_")
	clean = strings.Trim(clean, "_-")
	if clean == "" {
		clean = "database"
	}
	var stamp string
	switch NormalizeAutoNameFormat(format) {
	case "month_day_year_ts":
		stamp = now.Format("_Jan_02_2006_Mon_150405")
	case "date_year_ts":
		stamp = now.Format("_2006_01_02_150405")
	case "date_day_year":
		stamp = now.Format("_2006_01_02_Mon")
	default:
		stamp = now.Format("_2006_01_02_Mon_150405")
	}
	return clean + suffix + stamp + ".zip"
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
	if out.AutoNameLocalDB || strings.TrimSpace(out.AutoNameFormat) != "" {
		out.AutoNameFormat = NormalizeAutoNameFormat(out.AutoNameFormat)
	}
	if out.AutoNameBackup || strings.TrimSpace(out.AutoNameBackupFormat) != "" {
		out.AutoNameBackupFormat = NormalizeAutoNameFormat(out.AutoNameBackupFormat)
	}
	out.SyncDirection = strings.ToLower(strings.TrimSpace(out.SyncDirection))
	if out.SyncDirection != "push" && out.SyncDirection != "pull_folder" {
		out.SyncDirection = "pull"
	}
	out.SyncFolder = strings.TrimSpace(out.SyncFolder)
	out.SyncTime = normalizeHHMM(out.SyncTime)
	out.BackupTime = normalizeHHMM(out.BackupTime)
	out.BackupSource = strings.ToLower(strings.TrimSpace(out.BackupSource))
	if out.BackupSource != "local" && out.BackupSource != "cloud" && out.BackupSource != "both" && out.BackupSource != "cloud_folder" {
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

// ScheduleSyncDir returns the base folder where Pull Cloud -> Folder runs save
// generated dated subfolders (defaults to backups/<schedule>/syncs if SyncFolder is blank).
func ScheduleSyncDir(p *Paths, sched BackupSyncSchedule) string {
	if d := strings.TrimSpace(sched.SyncFolder); d != "" {
		return d
	}
	return filepath.Join(ScheduleBackupDir(p, sched), "syncs")
}

// SchedulePurgeDirs returns all deduplicated directories managed by sched for snapshot
// listing and retention purging (BackupDir plus SyncFolder when pull_folder is active).
func SchedulePurgeDirs(p *Paths, sched BackupSyncSchedule) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		clean := filepath.Clean(d)
		key := strings.ToLower(clean)
		if !seen[key] {
			seen[key] = true
			dirs = append(dirs, clean)
		}
	}
	add(ScheduleBackupDir(p, sched))
	if sched.SyncDirection == "pull_folder" || strings.TrimSpace(sched.SyncFolder) != "" {
		add(ScheduleSyncDir(p, sched))
	}
	return dirs
}

var (
	snapshotTimestampRe       = regexp.MustCompile(`_(\d{8})_(\d{6})\.(zip|sql|bak)$`)
	snapshotDateDayYearTsRe   = regexp.MustCompile(`_(\d{4}_\d{2}_\d{2})_[a-z]{3}_(\d{6})\.(zip|sql|bak)$`)
	snapshotMonthDayYearTsRe  = regexp.MustCompile(`_([a-z]{3}_\d{2}_\d{4})_[a-z]{3}_(\d{6})\.(zip|sql|bak)$`)
	snapshotDateYearTsRe      = regexp.MustCompile(`_(\d{4}_\d{2}_\d{2})_(\d{6})\.(zip|sql|bak)$`)
	snapshotDateDayYearOnlyRe = regexp.MustCompile(`_(\d{4}_\d{2}_\d{2})_[a-z]{3}\.(zip|sql|bak)$`)

	folderTimestampRe       = regexp.MustCompile(`_(\d{8})_(\d{6})$`)
	folderDateDayYearTsRe   = regexp.MustCompile(`_(\d{4}_\d{2}_\d{2})_[a-z]{3}_(\d{6})$`)
	folderMonthDayYearTsRe  = regexp.MustCompile(`_([a-z]{3}_\d{2}_\d{4})_[a-z]{3}_(\d{6})$`)
	folderDateYearTsRe      = regexp.MustCompile(`_(\d{4}_\d{2}_\d{2})_(\d{6})$`)
	folderDateDayYearOnlyRe = regexp.MustCompile(`_(\d{4}_\d{2}_\d{2})_[a-z]{3}$`)
)

func parseSnapshotFolderTime(name string) (time.Time, bool) {
	low := strings.ToLower(strings.TrimSpace(name))
	if m := folderDateDayYearTsRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("2006_01_02_150405", m[1]+"_"+m[2], time.Local); err == nil {
			return t, true
		}
	}
	if m := folderMonthDayYearTsRe.FindStringSubmatch(low); m != nil {
		monCap := strings.ToUpper(m[1][:1]) + m[1][1:]
		if t, err := time.ParseInLocation("Jan_02_2006_150405", monCap+"_"+m[2], time.Local); err == nil {
			return t, true
		}
	}
	if m := folderDateYearTsRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("2006_01_02_150405", m[1]+"_"+m[2], time.Local); err == nil {
			return t, true
		}
	}
	if m := folderDateDayYearOnlyRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("2006_01_02", m[1], time.Local); err == nil {
			return t, true
		}
	}
	if m := folderTimestampRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("20060102_150405", m[1]+"_"+m[2], time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// isGeneratedSnapshotFolder verifies that a directory strictly matches a dated Darius DB
// snapshot folder name AND contains only .zip/.sql/.bak archive files (no nested dirs or
// unrelated user files) before it can be listed or deleted by retention purge.
func isGeneratedSnapshotFolder(fullPath, name string) (bool, time.Time, int64) {
	ts, ok := parseSnapshotFolderTime(name)
	if !ok {
		return false, time.Time{}, 0
	}
	sub, err := os.ReadDir(fullPath)
	if err != nil || len(sub) == 0 {
		return false, time.Time{}, 0
	}
	var totalSize int64
	for _, f := range sub {
		if f.IsDir() || !isSnapshotArchive(f.Name()) {
			return false, time.Time{}, 0
		}
		if fi, ierr := f.Info(); ierr == nil {
			totalSize += fi.Size()
		}
	}
	return true, ts, totalSize
}

func parseSnapshotTime(name string, modTime time.Time) time.Time {
	low := strings.ToLower(name)
	if m := snapshotTimestampRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("20060102_150405", m[1]+"_"+m[2], time.Local); err == nil {
			return t
		}
	}
	if m := snapshotDateDayYearTsRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("2006_01_02_150405", m[1]+"_"+m[2], time.Local); err == nil {
			return t
		}
	}
	if m := snapshotMonthDayYearTsRe.FindStringSubmatch(low); m != nil {
		monCap := strings.ToUpper(m[1][:1]) + m[1][1:]
		if t, err := time.ParseInLocation("Jan_02_2006_150405", monCap+"_"+m[2], time.Local); err == nil {
			return t
		}
	}
	if m := snapshotDateYearTsRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("2006_01_02_150405", m[1]+"_"+m[2], time.Local); err == nil {
			return t
		}
	}
	if m := snapshotDateDayYearOnlyRe.FindStringSubmatch(low); m != nil {
		if t, err := time.ParseInLocation("2006_01_02", m[1], time.Local); err == nil {
			return t
		}
	}
	return modTime
}

func isSnapshotArchive(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".zip" || ext == ".sql" || ext == ".bak"
}

// PurgeOldSnapshots deletes backup snapshot archives AND generated dated snapshot folders
// in dir whose age meets or exceeds retentionDays (default 7 days / 1 week).
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
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if ok, snapTime, _ := isGeneratedSnapshotFolder(full, e.Name()); ok && !snapTime.After(cutoff) {
				if rerr := os.RemoveAll(full); rerr == nil {
					purged = append(purged, e.Name()+"/")
				}
			}
			continue
		}
		if !isSnapshotArchive(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		snapTime := parseSnapshotTime(e.Name(), info.ModTime())
		if !snapTime.After(cutoff) {
			if rerr := os.Remove(full); rerr == nil {
				purged = append(purged, e.Name())
			}
		}
	}
	return len(purged), purged, nil
}

// PurgeScheduleAllDirs purges expired snapshot archives and generated dated folders
// across every directory managed by sched (BackupDir and pull_folder SyncFolder).
func PurgeScheduleAllDirs(p *Paths, sched BackupSyncSchedule, now time.Time) (int, []string, error) {
	total := 0
	var allNames []string
	var firstErr error
	for _, d := range SchedulePurgeDirs(p, sched) {
		n, names, err := PurgeOldSnapshots(d, sched.RetentionDays, now)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		total += n
		allNames = append(allNames, names...)
	}
	return total, allNames, firstErr
}

// ListSnapshots returns all stored snapshot archives and generated dated folders for sched
// ordered newest-first with age & retention purge guide info.
func ListSnapshots(p *Paths, sched BackupSyncSchedule, now time.Time) ([]BackupSnapshotInfo, error) {
	ret := sched.RetentionDays
	if ret <= 0 {
		ret = 7
	}
	type item struct {
		t    time.Time
		info BackupSnapshotInfo
	}
	var list []item
	seenPath := map[string]bool{}

	appendEntry := func(displayName, fullPath string, size int64, sizeText string, snapTime time.Time) {
		key := strings.ToLower(filepath.Clean(fullPath))
		if seenPath[key] {
			return
		}
		seenPath[key] = true
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
				Name:           displayName,
				Path:           fullPath,
				Size:           size,
				SizeText:       sizeText,
				CreatedAt:      snapTime.Format("2006-01-02 15:04"),
				CreatedDisplay: formatPurgeDateGuide(snapTime),
				AgeDays:        ageDays,
				ExpiresIn:      expText,
				PurgeAt:        formatPurgeDateGuide(expiresAt),
				PurgeStamp:     expiresAt.Format("2006_01_02_Mon_150405"),
				Expired:        expired,
			},
		})
	}

	for _, dir := range SchedulePurgeDirs(p, sched) {
		if !dirExists(dir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				if ok, snapTime, folderSize := isGeneratedSnapshotFolder(full, e.Name()); ok {
					appendEntry(e.Name()+"/", full, folderSize, formatByteSize(folderSize)+" · 📁 Folder", snapTime)
				}
				continue
			}
			if !isSnapshotArchive(e.Name()) {
				continue
			}
			st, err := e.Info()
			if err != nil {
				continue
			}
			snapTime := parseSnapshotTime(e.Name(), st.ModTime())
			appendEntry(e.Name(), full, st.Size(), formatByteSize(st.Size()), snapTime)
		}
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

// EnsureLocalEngineReady verifies a local database engine is running (auto-starting it via mgr
// if installed and stopped, or falling back between wire-compatible MySQL/MariaDB on the same port).
func EnsureLocalEngineReady(p *Paths, st *SettingsStore, mgr *Manager, engine string, es EngineSettings) (string, EngineSettings, error) {
	host := strings.TrimSpace(es.Host)
	if host != "" && !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost") {
		return engine, es, nil
	}
	if engine == "mssql" {
		if mgr != nil && !mgr.IsRunning("mssql") {
			_ = mgr.Start("mssql")
		}
		return engine, es, nil
	}
	alt := ""
	if engine == "mysql" {
		alt = "mariadb"
	} else if engine == "mariadb" {
		alt = "mysql"
	}

	if mgr != nil {
		if mgr.IsRunning(engine) {
			return engine, es, nil
		}
		if alt != "" && mgr.IsRunning(alt) && st != nil {
			altEs := st.Engine(alt)
			if altEs.Port == es.Port || altEs.Port == 3306 {
				return alt, altEs, nil
			}
		}
		if p != nil {
			if _, ok := FindInstallation(p, engine, es.Version); ok {
				if err := mgr.Start(engine); err == nil {
					return engine, st.Engine(engine), nil
				} else if alt == "" {
					return engine, es, err
				}
			}
			if alt != "" && st != nil {
				altEs := st.Engine(alt)
				if _, ok := FindInstallation(p, alt, altEs.Version); ok {
					if err := mgr.Start(alt); err == nil {
						return alt, st.Engine(alt), nil
					}
				}
			}
		}
	}

	if es.Port > 0 && portInUse(es.Port) {
		return engine, es, nil
	}
	return engine, es, fmt.Errorf("local %s is not running on port %d — start %s (or MariaDB/MySQL) on the Dashboard first, or download it in Versions",
		strings.ToUpper(engine), es.Port, strings.ToUpper(engine))
}

// Scheduler runs enabled daily Auto-Sync (Pull Cloud -> Local / Push) and daily Auto-Backup
// + 1-week snapshot purge tasks in the background.
type Scheduler struct {
	p                  *Paths
	st                 *SettingsStore
	mgr                *Manager
	onChange           func()
	onProgress         func(CloudSyncProgress)
	onScheduleProgress func(ScheduleRunProgress)
	onSyncLog          func(SyncLogEntry)

	stopOnce   sync.Once
	stopCh     chan struct{}
	runMu      sync.Mutex
	cancelMu   sync.Mutex
	runCancels map[string]context.CancelFunc
}

func NewScheduler(p *Paths, st *SettingsStore, mgr *Manager, onChange func(), onProgress func(CloudSyncProgress)) *Scheduler {
	return &Scheduler{
		p:          p,
		st:         st,
		mgr:        mgr,
		onChange:   onChange,
		onProgress: onProgress,
		stopCh:     make(chan struct{}),
		runCancels: make(map[string]context.CancelFunc),
	}
}

// CancelScheduleRun aborts an active sync/backup run for schedule ID (or all active runs if id == "").
func (sc *Scheduler) CancelScheduleRun(id string) bool {
	sc.cancelMu.Lock()
	var cancels []context.CancelFunc
	for key, fn := range sc.runCancels {
		if fn != nil && (id == "" || key == id || strings.HasPrefix(key, id+":")) {
			cancels = append(cancels, fn)
		}
	}
	sc.cancelMu.Unlock()
	for _, fn := range cancels {
		fn()
	}
	return len(cancels) > 0
}

// SetScheduleProgressCallback registers a listener for unified 0-100% schedule run progress.
func (sc *Scheduler) SetScheduleProgressCallback(fn func(ScheduleRunProgress)) {
	sc.onScheduleProgress = fn
}

// SetSyncLogCallback registers a listener invoked whenever a new trace log entry is recorded.
func (sc *Scheduler) SetSyncLogCallback(fn func(SyncLogEntry)) {
	sc.onSyncLog = fn
}

func (sc *Scheduler) recordLog(entry SyncLogEntry) {
	saved := AppendSyncLog(sc.p, entry)
	if sc.onSyncLog != nil {
		sc.onSyncLog(saved)
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
		sc.appendLog("Scheduler started (checking due daily sync/backup schedules every 15s)")
		sc.RunDueSchedules(time.Now())
		tick := time.NewTicker(15 * time.Second)
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
		// Always enforce snapshot retention (for both .zip archives and generated dated folders)
		if sched.AutoBackup || (sched.AutoSync && sched.SyncDirection == "pull_folder") {
			purgeDirs := SchedulePurgeDirs(sc.p, sched)
			if purgedN, purgedNames, _ := PurgeScheduleAllDirs(sc.p, sched, now); purgedN > 0 {
				cutoff := now.Add(-time.Duration(sched.RetentionDays) * 24 * time.Hour)
				sc.recordLog(SyncLogEntry{
					ScheduleID: sched.ID,
					Name:       sched.Name,
					Trigger:    "scheduled",
					Action:     "purge",
					Target:     strings.Join(purgeDirs, "; "),
					Status:     "ok",
					Detail:     fmt.Sprintf("Purged %d expired snapshot(s) (>= %d days, cutoff %s): %s", purgedN, sched.RetentionDays, formatPurgeDateGuide(cutoff), strings.Join(purgedNames, ", ")),
				})
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
		_, _ = sc.executeLocked(sched.ID, doSync, doBackup, now, "scheduled")
	}
}

// RunScheduleNow executes a schedule immediately by ID ("both", "sync", or "backup").
// Manual runs are logged and update LastSyncAt/LastBackupAt without consuming today's
// upcoming scheduled time slot (LastSyncDate/LastBackupDate).
func (sc *Scheduler) RunScheduleNow(id, mode string) (string, error) {
	sc.runMu.Lock()
	defer sc.runMu.Unlock()
	doSync := mode == "both" || mode == "sync" || mode == ""
	doBackup := mode == "both" || mode == "backup" || mode == ""
	return sc.executeLocked(id, doSync, doBackup, time.Now(), "manual")
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

func (sc *Scheduler) executeLocked(id string, doSync, doBackup bool, now time.Time, trigger string) (string, error) {
	if trigger != "scheduled" {
		trigger = "manual"
	}
	trigTag := "Manual"
	if trigger == "scheduled" {
		trigTag = "Auto"
	}
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
	effectiveLocalDB := sched.LocalDB

	runSync := doSync && sched.AutoSync
	runBackup := doBackup && sched.AutoBackup
	runMode := "both"
	if runSync && !runBackup {
		runMode = "sync"
	} else if !runSync && runBackup {
		runMode = "backup"
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	cancelKey := id + ":" + runMode
	sc.cancelMu.Lock()
	if sc.runCancels == nil {
		sc.runCancels = make(map[string]context.CancelFunc)
	}
	if prev := sc.runCancels[cancelKey]; prev != nil {
		prev()
	}
	sc.runCancels[cancelKey] = cancelRun
	sc.cancelMu.Unlock()
	defer func() {
		cancelRun()
		sc.cancelMu.Lock()
		delete(sc.runCancels, cancelKey)
		sc.cancelMu.Unlock()
	}()
	isCancelledErr := func(err error) bool {
		return errors.Is(err, context.Canceled) || errors.Is(err, ErrSyncCancelled) || runCtx.Err() != nil
	}

	lastPct := 0
	emitProg := func(pct int, state, stage, detail string) {
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		if state == "running" {
			if pct > lastPct {
				lastPct = pct
			} else {
				pct = lastPct
			}
		} else if pct > lastPct {
			lastPct = pct
		}
		if sc.onScheduleProgress != nil {
			sc.onScheduleProgress(ScheduleRunProgress{
				ScheduleID: sched.ID,
				Name:       sched.Name,
				Trigger:    trigger,
				Mode:       runMode,
				Percent:    pct,
				State:      state,
				Stage:      stage,
				Detail:     detail,
			})
		}
	}

	emitProg(2, "running", "Starting schedule...", fmt.Sprintf("Preparing %s (%s)...", sched.Name, trigTag))

	// 1) Auto-Sync (default Pull Cloud -> Local)
	if runSync {
		syncStartedAt := time.Now()
		syncStart, syncEnd := 4, 96
		syncPrefix := ""
		if runBackup {
			syncStart, syncEnd = 4, 52
			syncPrefix = "[1/2 Sync] "
		}
		if cloudConn == nil {
			err := errors.New("cloud profile not found for auto-sync")
			firstErr = err
			_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
				cur.LastSyncAt, cur.LastSyncStatus = humanNow, fmt.Sprintf("err [%s]: %s", trigTag, err.Error())
			})
			sc.recordLog(SyncLogEntry{
				ScheduleID: sched.ID,
				Name:       sched.Name,
				Trigger:    trigger,
				Action:     "sync",
				Direction:  sched.SyncDirection,
				Status:     "error",
				DurationMs: time.Since(syncStartedAt).Milliseconds(),
				Detail:     err.Error(),
			})
			emitProg(syncEnd, "running", syncPrefix+"Sync failed", err.Error())
		} else if sched.SyncDirection == "pull_folder" {
			cloudDB := fallbackStr(sched.CloudDB, cloudConn.Database)
			baseName := fallbackStr(sched.LocalDB, cloudDB)
			baseDir := ScheduleSyncDir(sc.p, sched)
			var folderName, zipFileName string
			if sched.AutoNameLocalDB {
				folderName = FormatAutoPullLocalDBName(baseName, sched.AutoNameFormat, now)
				zipFileName = folderName + ".zip"
			} else {
				safe := cleanDBNameRe.ReplaceAllString(strings.TrimSpace(baseName), "_")
				safe = strings.Trim(safe, "_-")
				if safe == "" {
					safe = "database"
				}
				folderName = safe
				zipFileName = fmt.Sprintf("%s_%s.zip", safe, stamp)
			}
			runDir := filepath.Join(baseDir, folderName)
			outZip := filepath.Join(runDir, zipFileName)
			srcDesc := fmt.Sprintf("Cloud:%s/%s", cloudConn.Name, cloudDB)
			dstDesc := runDir
			sc.recordLog(SyncLogEntry{
				ScheduleID: sched.ID,
				Name:       sched.Name,
				Trigger:    trigger,
				Action:     "sync",
				Direction:  "pull_folder",
				Source:     srcDesc,
				Target:     dstDesc,
				Status:     "started",
				Detail:     fmt.Sprintf("Started %s pull-to-folder export (no local DB · scheduled @ %s)", strings.ToLower(trigTag), sched.SyncTime),
			})
			emitProg(syncStart, "running", syncPrefix+"Creating output folder...", fmt.Sprintf("Preparing %s...", runDir))
			var msg string
			var syncErr error
			if mkErr := os.MkdirAll(runDir, 0o755); mkErr != nil {
				syncErr = mkErr
			} else {
				syncErr = ExportDatabaseFullZipWithContext(runCtx, sc.p, cloudEng, cloudEs, cloudDB, nil, true, true, outZip, func(bp BackupProgress) {
					if bp.State == "running" {
						mapped := mapProgressRange(bp.Percent, syncStart+2, syncEnd)
						emitProg(mapped, "running", syncPrefix+fallbackStr(bp.Stage, "Exporting Cloud DB to folder..."), bp.Detail)
					}
				})
				if syncErr == nil {
					msg = fmt.Sprintf("Saved %s into folder %s", zipFileName, runDir)
				}
			}
			durMs := time.Since(syncStartedAt).Milliseconds()
			if syncErr != nil {
				firstErr = syncErr
				cancelled := isCancelledErr(syncErr)
				stTag := "err"
				logStatus := "error"
				if cancelled {
					_ = os.Remove(outZip)
					stTag = "cancelled"
					logStatus = "cancelled"
				}
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastSyncAt, cur.LastSyncStatus = humanNow, fmt.Sprintf("%s [%s]: %s", stTag, trigTag, syncErr.Error())
				})
				sc.recordLog(SyncLogEntry{
					ScheduleID: sched.ID,
					Name:       sched.Name,
					Trigger:    trigger,
					Action:     "sync",
					Direction:  "pull_folder",
					Source:     srcDesc,
					Target:     dstDesc,
					Status:     logStatus,
					DurationMs: durMs,
					Detail:     syncErr.Error(),
				})
			} else {
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastSyncAt = humanNow
					cur.LastSyncStatus = fmt.Sprintf("ok [%s] · %s", trigTag, msg)
					if trigger == "scheduled" {
						cur.LastSyncDate = today
					}
				})
				sc.recordLog(SyncLogEntry{
					ScheduleID: sched.ID,
					Name:       sched.Name,
					Trigger:    trigger,
					Action:     "sync",
					Direction:  "pull_folder",
					Source:     srcDesc,
					Target:     dstDesc,
					Status:     "ok",
					DurationMs: durMs,
					Detail:     msg,
				})
				parts = append(parts, msg)
				emitProg(syncEnd, "running", syncPrefix+"Folder export complete", msg)
			}
		} else {
			cloudDB := fallbackStr(sched.CloudDB, cloudConn.Database)
			localDB := fallbackStr(sched.LocalDB, cloudDB)
			if sched.SyncDirection != "push" && sched.AutoNameLocalDB {
				localDB = FormatAutoPullLocalDBName(localDB, sched.AutoNameFormat, now)
				effectiveLocalDB = localDB
			}
			srcDesc := fmt.Sprintf("Cloud:%s/%s", cloudConn.Name, cloudDB)
			dstDesc := fmt.Sprintf("Local:%s/%s", strings.ToUpper(localEng), localDB)
			if sched.SyncDirection == "push" {
				srcDesc, dstDesc = dstDesc, srcDesc
			}
			sc.recordLog(SyncLogEntry{
				ScheduleID: sched.ID,
				Name:       sched.Name,
				Trigger:    trigger,
				Action:     "sync",
				Direction:  sched.SyncDirection,
				Source:     srcDesc,
				Target:     dstDesc,
				Status:     "started",
				Detail:     fmt.Sprintf("Started %s %s sync (scheduled @ %s)", strings.ToLower(trigTag), sched.SyncDirection, sched.SyncTime),
			})
			emitProg(syncStart, "running", syncPrefix+"Checking local service...", fmt.Sprintf("Ensuring local %s is ready for %s...", strings.ToUpper(localEng), localDB))
			syncProgressCB := func(p CloudSyncProgress) {
				if p.State == "running" {
					mapped := mapProgressRange(p.Percent, syncStart+2, syncEnd)
					emitProg(mapped, "running", syncPrefix+fallbackStr(p.Stage, "Syncing database..."), p.Detail)
				}
			}
			var msg string
			var syncErr error
			if effEng, effEs, rErr := EnsureLocalEngineReady(sc.p, sc.st, sc.mgr, localEng, localEs); rErr != nil {
				syncErr = rErr
			} else if runCtx.Err() != nil {
				syncErr = ErrSyncCancelled
			} else if sched.SyncDirection == "push" {
				msg, syncErr = CloudSyncDatabaseWithContext(runCtx, sc.p, effEng, effEs, localDB, cloudEng, cloudEs, cloudDB, syncProgressCB)
			} else {
				msg, syncErr = CloudSyncDatabaseWithContext(runCtx, sc.p, cloudEng, cloudEs, cloudDB, effEng, effEs, localDB, syncProgressCB)
			}
			durMs := time.Since(syncStartedAt).Milliseconds()
			if syncErr != nil {
				firstErr = syncErr
				cancelled := isCancelledErr(syncErr)
				stTag := "err"
				logStatus := "error"
				if cancelled {
					stTag = "cancelled"
					logStatus = "cancelled"
				}
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastSyncAt, cur.LastSyncStatus = humanNow, fmt.Sprintf("%s [%s]: %s", stTag, trigTag, syncErr.Error())
				})
				sc.recordLog(SyncLogEntry{
					ScheduleID: sched.ID,
					Name:       sched.Name,
					Trigger:    trigger,
					Action:     "sync",
					Direction:  sched.SyncDirection,
					Source:     srcDesc,
					Target:     dstDesc,
					Status:     logStatus,
					DurationMs: durMs,
					Detail:     syncErr.Error(),
				})
			} else {
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastSyncAt = humanNow
					cur.LastSyncStatus = fmt.Sprintf("ok [%s] · %s", trigTag, msg)
					// Only automatic scheduled runs consume today's scheduled SyncTime slot
					if trigger == "scheduled" {
						cur.LastSyncDate = today
					}
				})
				sc.recordLog(SyncLogEntry{
					ScheduleID: sched.ID,
					Name:       sched.Name,
					Trigger:    trigger,
					Action:     "sync",
					Direction:  sched.SyncDirection,
					Source:     srcDesc,
					Target:     dstDesc,
					Status:     "ok",
					DurationMs: durMs,
					Detail:     msg,
				})
				parts = append(parts, msg)
				emitProg(syncEnd, "running", syncPrefix+"Sync complete", msg)
			}
		}
	}

	// 2) Auto-Backup (.zip snapshot) + 1-week retention purge
	if runBackup {
		bkpStartedAt := time.Now()
		bkpStart, bkpEnd := 4, 95
		bkpPrefix := ""
		if runSync {
			bkpStart, bkpEnd = 54, 95
			bkpPrefix = "[2/2 Backup] "
		}
		emitProg(bkpStart, "running", bkpPrefix+"Preparing .zip snapshot...", fmt.Sprintf("Creating backup archive for %s...", sched.Name))
		dir := ScheduleBackupDir(sc.p, sched)
		sc.recordLog(SyncLogEntry{
			ScheduleID: sched.ID,
			Name:       sched.Name,
			Trigger:    trigger,
			Action:     "backup",
			Source:     sched.BackupSource,
			Target:     dir,
			Status:     "started",
			Detail:     fmt.Sprintf("Started %s .zip snapshot backup (scheduled @ %s)", strings.ToLower(trigTag), sched.BackupTime),
		})
		if err := os.MkdirAll(dir, 0o755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
				cur.LastBackupAt, cur.LastBackupStatus = humanNow, fmt.Sprintf("err [%s]: %s", trigTag, err.Error())
			})
			sc.recordLog(SyncLogEntry{
				ScheduleID: sched.ID,
				Name:       sched.Name,
				Trigger:    trigger,
				Action:     "backup",
				Source:     sched.BackupSource,
				Target:     dir,
				Status:     "error",
				DurationMs: time.Since(bkpStartedAt).Milliseconds(),
				Detail:     err.Error(),
			})
		} else {
			var snapFiles []string
			var bErr error
			runZip := func(eng string, es EngineSettings, db string, suffix string, rangeStart, rangeEnd int) {
				if bErr != nil || db == "" {
					return
				}
				var zipName string
				if sched.AutoNameBackup {
					zipName = FormatAutoBackupZipName(db, suffix, sched.AutoNameBackupFormat, now)
				} else {
					safe := cleanDBNameRe.ReplaceAllString(db, "_")
					if safe == "" {
						safe = "database"
					}
					zipName = fmt.Sprintf("%s%s_%s.zip", safe, suffix, stamp)
				}
				outZip := filepath.Join(dir, zipName)
				bErr = ExportDatabaseFullZipWithContext(runCtx, sc.p, eng, es, db, nil, true, true, outZip, func(bp BackupProgress) {
					if bp.State == "running" {
						mapped := mapProgressRange(bp.Percent, rangeStart, rangeEnd)
						emitProg(mapped, "running", bkpPrefix+fallbackStr(bp.Stage, "Exporting .zip snapshot..."), bp.Detail)
					}
				})
				if bErr != nil && isCancelledErr(bErr) {
					_ = os.Remove(outZip)
				}
				if bErr == nil {
					snapFiles = append(snapFiles, filepath.Base(outZip))
				}
			}

			switch sched.BackupSource {
			case "both":
				mid := bkpStart + (bkpEnd-bkpStart)/2
				hasCloud := cloudConn != nil
				hasLocal := effectiveLocalDB != ""
				cloudEnd := bkpEnd
				localStart := bkpStart
				if hasCloud && hasLocal {
					cloudEnd = mid
					localStart = mid
				}
				if hasCloud {
					runZip(cloudEng, cloudEs, fallbackStr(sched.CloudDB, cloudConn.Database), "_cloud", bkpStart, cloudEnd)
				}
				if hasLocal {
					if effEng, effEs, rErr := EnsureLocalEngineReady(sc.p, sc.st, sc.mgr, localEng, localEs); rErr != nil {
						if bErr == nil {
							bErr = rErr
						}
					} else {
						runZip(effEng, effEs, effectiveLocalDB, "_local", localStart, bkpEnd)
					}
				}
			case "cloud_folder":
				if cloudConn != nil {
					cloudDB := fallbackStr(sched.CloudDB, cloudConn.Database)
					baseName := fallbackStr(sched.LocalDB, cloudDB)
					var folderName string
					if sched.AutoNameBackup {
						folderName = FormatAutoPullLocalDBName(baseName, sched.AutoNameBackupFormat, now)
					} else {
						safe := cleanDBNameRe.ReplaceAllString(strings.TrimSpace(baseName), "_")
						safe = strings.Trim(safe, "_-")
						if safe == "" {
							safe = "database"
						}
						folderName = fmt.Sprintf("%s_%s", safe, stamp)
					}
					runDir := filepath.Join(dir, folderName)
					outZip := filepath.Join(runDir, folderName+".zip")
					if mkErr := os.MkdirAll(runDir, 0o755); mkErr != nil {
						bErr = mkErr
					} else {
						bErr = ExportDatabaseFullZipWithContext(runCtx, sc.p, cloudEng, cloudEs, cloudDB, nil, true, true, outZip, func(bp BackupProgress) {
							if bp.State == "running" {
								mapped := mapProgressRange(bp.Percent, bkpStart, bkpEnd)
								emitProg(mapped, "running", bkpPrefix+fallbackStr(bp.Stage, "Exporting Cloud DB to folder..."), bp.Detail)
							}
						})
						if bErr != nil && isCancelledErr(bErr) {
							_ = os.RemoveAll(runDir)
						}
						if bErr == nil {
							snapFiles = append(snapFiles, folderName+"/"+folderName+".zip")
						}
					}
				} else {
					bErr = errors.New("cloud profile not found for scheduled folder backup")
				}
			case "cloud":
				if cloudConn != nil {
					runZip(cloudEng, cloudEs, fallbackStr(sched.CloudDB, cloudConn.Database), "", bkpStart, bkpEnd)
				} else if effectiveLocalDB != "" {
					if effEng, effEs, rErr := EnsureLocalEngineReady(sc.p, sc.st, sc.mgr, localEng, localEs); rErr != nil {
						bErr = rErr
					} else {
						runZip(effEng, effEs, effectiveLocalDB, "", bkpStart, bkpEnd)
					}
				} else {
					bErr = errors.New("cloud profile not found for scheduled backup")
				}
			default: // local
				if effectiveLocalDB != "" {
					if effEng, effEs, rErr := EnsureLocalEngineReady(sc.p, sc.st, sc.mgr, localEng, localEs); rErr != nil {
						bErr = rErr
					} else {
						runZip(effEng, effEs, effectiveLocalDB, "", bkpStart, bkpEnd)
					}
				} else {
					bErr = errors.New("local database name is required for backup")
				}
			}

			emitProg(97, "running", bkpPrefix+"Checking snapshot retention...", fmt.Sprintf("Purging snapshots older than %d day(s)...", sched.RetentionDays))
			purgedN, _, _ := PurgeScheduleAllDirs(sc.p, sched, now)
			durMs := time.Since(bkpStartedAt).Milliseconds()
			if bErr != nil {
				if firstErr == nil {
					firstErr = bErr
				}
				cancelled := isCancelledErr(bErr)
				stTag := "err"
				logStatus := "error"
				if cancelled {
					stTag = "cancelled"
					logStatus = "cancelled"
				}
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastBackupAt, cur.LastBackupStatus, cur.LastPurgedCount = humanNow, fmt.Sprintf("%s [%s]: %s", stTag, trigTag, bErr.Error()), purgedN
				})
				sc.recordLog(SyncLogEntry{
					ScheduleID: sched.ID,
					Name:       sched.Name,
					Trigger:    trigger,
					Action:     "backup",
					Source:     sched.BackupSource,
					Target:     dir,
					Status:     logStatus,
					DurationMs: durMs,
					Detail:     bErr.Error(),
				})
			} else {
				statusMsg := fmt.Sprintf("ok [%s] · %s", trigTag, strings.Join(snapFiles, ", "))
				if purgedN > 0 {
					statusMsg += fmt.Sprintf(" (purged %d >%dd)", purgedN, sched.RetentionDays)
				}
				_ = sc.updateScheduleStatus(id, func(cur *BackupSyncSchedule) {
					cur.LastBackupAt = humanNow
					cur.LastBackupStatus = statusMsg
					cur.LastPurgedCount = purgedN
					// Only automatic scheduled runs consume today's scheduled BackupTime slot
					if trigger == "scheduled" {
						cur.LastBackupDate = today
					}
				})
				sc.recordLog(SyncLogEntry{
					ScheduleID: sched.ID,
					Name:       sched.Name,
					Trigger:    trigger,
					Action:     "backup",
					Source:     sched.BackupSource,
					Target:     dir,
					Status:     "ok",
					DurationMs: durMs,
					Detail:     statusMsg,
				})
				parts = append(parts, "Backup saved: "+strings.Join(snapFiles, ", "))
				if purgedN > 0 {
					parts = append(parts, fmt.Sprintf("Purged %d expired snapshot(s) (>= %d days)", purgedN, sched.RetentionDays))
				}
			}
		}
	}

	summary := strings.Join(parts, " · ")
	if firstErr != nil {
		detail := firstErr.Error()
		if summary != "" {
			detail = summary + " · Error: " + detail
		}
		if isCancelledErr(firstErr) {
			emitProg(lastPct, "cancelled", "Cancelled", detail)
		} else {
			emitProg(100, "error", "Schedule finished with error", detail)
		}
		return summary, firstErr
	}
	if len(parts) == 0 {
		emitProg(100, "done", "No enabled action ran", "Enable Auto-Sync or Auto-Backup in this schedule.")
		return "No enabled action ran", nil
	}
	emitProg(100, "done", "Schedule complete!", summary)
	return summary, nil
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
