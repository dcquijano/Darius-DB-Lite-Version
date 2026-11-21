//go:build windows

package core

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const mssqlInstance = "MSSQLLocalDB"

type mssqlEngine struct{}

func (m *mssqlEngine) ID() string   { return "mssql" }
func (m *mssqlEngine) Name() string { return "MS SQL Server" }
func (m *mssqlEngine) IsSQL() bool  { return true }

func (m *mssqlEngine) Detect(dir string) bool {
	for _, name := range []string{"sqlcmd.exe", "SQLCMD.EXE", "SqlLocalDB.exe", "sqlservr.exe"} {
		if fileExists(filepath.Join(dir, name)) || fileExists(filepath.Join(dir, "bin", name)) {
			return true
		}
	}
	return false
}

func (m *mssqlEngine) ClientBinDir(inst Installation) string {
	if fileExists(filepath.Join(inst.Dir, "bin", "sqlcmd.exe")) {
		return filepath.Join(inst.Dir, "bin")
	}
	return inst.Dir
}

func (m *mssqlEngine) DataDir(p *Paths, _ Installation) string {
	return filepath.Join(p.Data, "mssql")
}

func (m *mssqlEngine) ConfigFile(p *Paths, _ Installation) string {
	return filepath.Join(p.EngineEtc("mssql"), "mssql.conf")
}

const mssqlConf = `# Darius DB (Lite Version 0.1.4) - MS SQL Server configuration
# Default Server Name uses the Windows PC User name (Windows Authentication)
instance = MSSQLLocalDB
server = (localdb)\MSSQLLocalDB
tcp_port = 1433
trusted_connection = true
query_timeout = 30
`

// MSSQLServerName returns the Windows PC User name (or computer hostname) used as the MS SQL Server Name.
func MSSQLServerName() string {
	if u := strings.TrimSpace(os.Getenv("USERNAME")); u != "" {
		return u
	}
	if c := strings.TrimSpace(os.Getenv("COMPUTERNAME")); c != "" {
		return c
	}
	if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
		return strings.TrimSpace(h)
	}
	return "localhost"
}

func (m *mssqlEngine) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	cfg := m.ConfigFile(p, inst)
	if b, err := os.ReadFile(cfg); err == nil && (strings.Contains(string(b), "default_user") || strings.Contains(string(b), "default_password")) {
		_ = os.WriteFile(cfg, []byte(strings.ReplaceAll(mssqlConf, "\n", "\r\n")), 0o644)
	}
	if err := writeIfMissing(cfg, mssqlConf); err != nil {
		return err
	}
	if err := os.MkdirAll(m.DataDir(p, inst), 0o755); err != nil {
		return err
	}
	localDB := findSqlLocalDB()
	if localDB == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, localDB, "info", mssqlInstance)
	hideWindow(cmd)
	out, _ := cmd.CombinedOutput()
	if strings.Contains(strings.ToLower(string(out)), "not created") {
		logf("Creating SQL Server LocalDB instance: " + mssqlInstance)
		return runQuiet(ctx, logf, localDB, "create", mssqlInstance)
	}
	return nil
}

func (m *mssqlEngine) Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	localDB := findSqlLocalDB()
	if localDB == "" {
		return nil, errors.New("SqlLocalDB.exe not found on this PC — install SQL Server Express LocalDB or connect to a running TCP SQL Server on port " + strconv.Itoa(s.Port))
	}
	cmd := exec.Command(localDB, "start", mssqlInstance)
	cmd.Dir = p.Root
	return &StartSpec{Cmd: cmd, Detached: true}, nil
}

func (m *mssqlEngine) Stop(p *Paths, inst Installation, s EngineSettings) error {
	localDB := findSqlLocalDB()
	if localDB == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return runQuiet(ctx, nil, localDB, "stop", mssqlInstance)
}

func (m *mssqlEngine) PidOf(p *Paths, inst Installation) int {
	if localDB := findSqlLocalDB(); localDB != "" {
		cmd := exec.Command(localDB, "info", mssqlInstance)
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err == nil {
			low := strings.ToLower(string(out))
			if strings.Contains(low, "state:") && strings.Contains(low, "running") {
				if pid := findLocalDBPID(); pid > 0 {
					return pid
				}
			}
		}
	}
	return 0
}

var dottedVerRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+){0,2}`)

var (
	sysMSSQLMu      sync.Mutex
	sysMSSQLCached  Installation
	sysMSSQLFound   bool
	sysMSSQLChecked time.Time

	sysLocalDBMu      sync.Mutex
	sysLocalDBCached  string
	sysLocalDBChecked time.Time

	sysSqlcmdMu      sync.Mutex
	sysSqlcmdCached  string
	sysSqlcmdChecked time.Time

	mssqlTargetMu      sync.Mutex
	mssqlTargetCached  string
	mssqlTargetChecked time.Time
)

const (
	sysDetectCacheTTL   = 60 * time.Second
	mssqlTargetCacheTTL = 30 * time.Second
)

// InvalidateMSSQLTargetCache clears the cached local MS SQL server target/pipe.
func InvalidateMSSQLTargetCache() {
	mssqlTargetMu.Lock()
	mssqlTargetCached = ""
	mssqlTargetChecked = time.Time{}
	mssqlTargetMu.Unlock()
}

// InvalidateSystemDetectCache clears cached system-wide engine detection results (MSSQL / PHP).
func InvalidateSystemDetectCache() {
	InvalidateMSSQLTargetCache()
	sysMSSQLMu.Lock()
	sysMSSQLChecked = time.Time{}
	sysMSSQLMu.Unlock()

	sysLocalDBMu.Lock()
	sysLocalDBChecked = time.Time{}
	sysLocalDBMu.Unlock()

	sysSqlcmdMu.Lock()
	sysSqlcmdChecked = time.Time{}
	sysSqlcmdMu.Unlock()

	sysPHPMu.Lock()
	sysPHPChecked = time.Time{}
	sysPHPMu.Unlock()
}

// detectSystemMSSQL checks if SQL Server 2025 (17.x) LocalDB or sqlcmd is installed on Windows.
func detectSystemMSSQL() (Installation, bool) {
	sysMSSQLMu.Lock()
	if !sysMSSQLChecked.IsZero() && time.Since(sysMSSQLChecked) < sysDetectCacheTTL {
		inst, ok := sysMSSQLCached, sysMSSQLFound
		sysMSSQLMu.Unlock()
		return inst, ok
	}
	sysMSSQLMu.Unlock()

	var inst Installation
	var ok bool
	if localDB := findSqlLocalDB(); localDB != "" {
		ver := "17.0.1000.7"
		cmd := exec.Command(localDB, "versions")
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err == nil {
			if m := dottedVerRe.FindString(string(out)); m != "" {
				ver = m
			}
		}
		inst = Installation{
			Engine:  "mssql",
			Name:    "mssql-2025-" + ver,
			Version: ver,
			Dir:     filepath.Dir(localDB),
		}
		ok = true
	} else if sc := findSystemSqlcmd(); sc != "" {
		inst = Installation{
			Engine:  "mssql",
			Name:    "mssql-2025-17.0.1000.7",
			Version: "17.0.1000.7",
			Dir:     filepath.Dir(sc),
		}
		ok = true
	}

	sysMSSQLMu.Lock()
	sysMSSQLCached, sysMSSQLFound, sysMSSQLChecked = inst, ok, time.Now()
	sysMSSQLMu.Unlock()
	return inst, ok
}

func findSqlLocalDB() string {
	sysLocalDBMu.Lock()
	if !sysLocalDBChecked.IsZero() && time.Since(sysLocalDBChecked) < sysDetectCacheTTL {
		p := sysLocalDBCached
		sysLocalDBMu.Unlock()
		return p
	}
	sysLocalDBMu.Unlock()

	found := ""
	if p, err := exec.LookPath("SqlLocalDB.exe"); err == nil {
		found = p
	} else {
		for _, ver := range []string{"170", "160", "150", "140", "130"} {
			p := filepath.Join(`C:\Program Files\Microsoft SQL Server`, ver, `Tools\Binn\SqlLocalDB.exe`)
			if fileExists(p) {
				found = p
				break
			}
		}
	}

	sysLocalDBMu.Lock()
	sysLocalDBCached, sysLocalDBChecked = found, time.Now()
	sysLocalDBMu.Unlock()
	return found
}

func findSystemSqlcmd() string {
	sysSqlcmdMu.Lock()
	if !sysSqlcmdChecked.IsZero() && time.Since(sysSqlcmdChecked) < sysDetectCacheTTL {
		p := sysSqlcmdCached
		sysSqlcmdMu.Unlock()
		return p
	}
	sysSqlcmdMu.Unlock()

	found := ""
	if p, err := exec.LookPath("sqlcmd.exe"); err == nil {
		found = p
	} else {
		for _, ver := range []string{"180", "170", "160", "150"} {
			for _, base := range []string{
				filepath.Join(`C:\Program Files\Microsoft SQL Server\Client SDK\ODBC`, ver, `Tools\Binn\SQLCMD.EXE`),
				filepath.Join(`C:\Program Files\Microsoft SQL Server`, ver, `Tools\Binn\SQLCMD.EXE`),
			} {
				if fileExists(base) {
					found = base
					break
				}
			}
			if found != "" {
				break
			}
		}
	}

	sysSqlcmdMu.Lock()
	sysSqlcmdCached, sysSqlcmdChecked = found, time.Now()
	sysSqlcmdMu.Unlock()
	return found
}

func findSqlcmd() string {
	if p := findSystemSqlcmd(); p != "" {
		return p
	}
	root := ResolveRoot()
	entries, _ := os.ReadDir(filepath.Join(root, "bin", "mssql"))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, rel := range []string{"sqlcmd.exe", filepath.Join("bin", "sqlcmd.exe")} {
			cand := filepath.Join(root, "bin", "mssql", e.Name(), rel)
			if fileExists(cand) {
				return cand
			}
		}
	}
	// Auto-provision portable sqlcmd.exe on demand if missing
	if p := autoProvisionSqlcmd(root); p != "" {
		return p
	}
	return ""
}

func autoProvisionSqlcmd(root string) string {
	destDir := filepath.Join(root, "bin", "mssql", "mssql-17.0.1000.7")
	destExe := filepath.Join(destDir, "sqlcmd.exe")
	if fileExists(destExe) {
		return destExe
	}
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("GET", "https://github.com/microsoft/go-sqlcmd/releases/download/v1.10.0/sqlcmd-windows-amd64.zip", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.4")
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return ""
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return ""
	}
	_ = os.MkdirAll(destDir, 0o755)
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), "sqlcmd.exe") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			out, err := os.Create(destExe)
			if err != nil {
				rc.Close()
				continue
			}
			_, _ = io.Copy(out, rc)
			out.Close()
			rc.Close()
			if fileExists(destExe) {
				return destExe
			}
		}
	}
	return ""
}

type localDBProcCandidate struct {
	pid   int
	path  string
	alive bool
}

func selectBestLocalDBPID(cands []localDBProcCandidate) int {
	bestLocalDB := 0
	bestAccessible := 0
	bestAlive := 0
	for _, c := range cands {
		if !c.alive || c.pid <= 0 {
			continue
		}
		if c.pid > bestAlive {
			bestAlive = c.pid
		}
		if c.path != "" {
			if strings.Contains(strings.ToLower(c.path), "localdb") && c.pid > bestLocalDB {
				bestLocalDB = c.pid
			}
			if c.pid > bestAccessible {
				bestAccessible = c.pid
			}
		}
	}
	if bestLocalDB > 0 {
		return bestLocalDB
	}
	if bestAccessible > 0 {
		return bestAccessible
	}
	return bestAlive
}

func findLocalDBPID() int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0
	}
	var cands []localDBProcCandidate
	for {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(name, "sqlservr.exe") {
			pid := int(pe.ProcessID)
			cands = append(cands, localDBProcCandidate{
				pid:   pid,
				path:  processPath(pid),
				alive: processAlive(pid),
			})
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return selectBestLocalDBPID(cands)
}

func findExistingSqlcmd() string {
	if p := findSystemSqlcmd(); p != "" {
		return p
	}
	root := ResolveRoot()
	entries, _ := os.ReadDir(filepath.Join(root, "bin", "mssql"))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, rel := range []string{"sqlcmd.exe", filepath.Join("bin", "sqlcmd.exe")} {
			cand := filepath.Join(root, "bin", "mssql", e.Name(), rel)
			if fileExists(cand) {
				return cand
			}
		}
	}
	return ""
}

func findProcessPID(exeName string) int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0
	}
	for {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(name, exeName) {
			return int(pe.ProcessID)
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return 0
}

// ---------------------------------------------------------------- MS SQL Browser & T-SQL Runner

func isRemoteMSSQLHost(s EngineSettings) bool {
	h := strings.TrimSpace(s.Host)
	return h != "" && !strings.EqualFold(h, "127.0.0.1") && !strings.EqualFold(h, "localhost")
}

func mssqlServerTarget(s EngineSettings) string {
	if isRemoteMSSQLHost(s) {
		h := strings.TrimSpace(s.Host)
		if strings.Contains(h, ",") || strings.Contains(h, `\`) {
			return h
		}
		if s.Port > 0 {
			return fmt.Sprintf("%s,%d", h, s.Port)
		}
		return h
	}
	mssqlTargetMu.Lock()
	if mssqlTargetCached != "" && time.Since(mssqlTargetChecked) < mssqlTargetCacheTTL {
		cached := mssqlTargetCached
		mssqlTargetMu.Unlock()
		return cached
	}
	mssqlTargetMu.Unlock()

	var resolved string
	if s.Port > 0 && portInUse(s.Port) {
		resolved = fmt.Sprintf("127.0.0.1,%d", s.Port)
	} else if localDB := findSqlLocalDB(); localDB != "" {
		if pipe := resolveLocalDBPipe(localDB); pipe != "" {
			resolved = pipe
		} else {
			resolved = `(localdb)\` + mssqlInstance
		}
	} else {
		resolved = fmt.Sprintf("127.0.0.1,%d", s.Port)
	}

	mssqlTargetMu.Lock()
	mssqlTargetCached = resolved
	mssqlTargetChecked = time.Now()
	mssqlTargetMu.Unlock()
	return resolved
}

func resolveLocalDBPipe(localDB string) string {
	readPipe := func() (pipe string, running bool, exists bool) {
		cmd := exec.Command(localDB, "info", mssqlInstance)
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) == 0 {
			return "", false, false
		}
		text := string(out)
		low := strings.ToLower(text)
		if strings.Contains(low, "not exist") || strings.Contains(low, "not created") || strings.Contains(low, "doesn't exist") {
			return "", false, false
		}
		exists = true
		running = strings.Contains(low, "state:") && strings.Contains(low, "running")
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "instance pipe name:") {
				p := strings.TrimSpace(line[len("instance pipe name:"):])
				if p != "" {
					pipe = p
				}
			}
		}
		return pipe, running, exists
	}

	pipe, running, exists := readPipe()
	if pipe != "" && running {
		return pipe
	}
	if !exists {
		c := exec.Command(localDB, "create", mssqlInstance)
		hideWindow(c)
		_ = c.Run()
	}
	st := exec.Command(localDB, "start", mssqlInstance)
	hideWindow(st)
	_ = st.Run()

	pipe, _, _ = readPipe()
	return pipe
}

func mssqlAuthArgs(target string, s EngineSettings) []string {
	low := strings.ToLower(target)
	// LocalDB instances authenticate the current Windows PC user natively via -E
	if !isRemoteMSSQLHost(s) && (strings.HasPrefix(low, "(localdb)") || strings.Contains(low, "localdb#")) {
		return []string{"-E"}
	}
	if s.User != "" || s.Password != "" {
		args := []string{"-U", s.User, "-P", s.Password}
		if isRemoteMSSQLHost(s) {
			args = append(args, "-C") // TrustServerCertificate for MonsterASP / SmarterASP / Azure / Cloud SQL
		}
		return args
	}
	return []string{"-E"}
}

var affectedRe = regexp.MustCompile(`^\((\d+) rows? affected\)$`)

func mssqlRunQuery(s EngineSettings, dbName, q string) (*QueryResult, error) {
	return mssqlRunQueryWithLimitCtx(context.Background(), s, dbName, q, maxRows)
}

func mssqlRunQueryCtx(ctx context.Context, s EngineSettings, dbName, q string) (*QueryResult, error) {
	return mssqlRunQueryWithLimitCtx(ctx, s, dbName, q, maxRows)
}

func mssqlRunQueryWithLimit(s EngineSettings, dbName, q string, limit int) (*QueryResult, error) {
	return mssqlRunQueryWithLimitCtx(context.Background(), s, dbName, q, limit)
}

func mssqlRunQueryWithLimitCtx(ctx context.Context, s EngineSettings, dbName, q string, limit int) (*QueryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("query is empty")
	}
	if limit <= 0 {
		limit = maxShowRows
	}
	sqlcmd := findSqlcmd()
	if sqlcmd == "" {
		return nil, errors.New("sqlcmd.exe not found — download Portable sqlcmd in the Versions tab")
	}
	if dbName == "" {
		if s.DefaultDB != "" {
			dbName = s.DefaultDB
		} else {
			dbName = "master"
		}
	}

	tmpFile, err := os.CreateTemp("", "dariusdb-mssql-*.sql")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	_, _ = tmpFile.WriteString(q + "\r\n")
	tmpFile.Close()

	const sep = "\t"
	runWithTarget := func(target string) ([]byte, error, []string) {
		baseArgs := []string{
			"-S", target,
			"-d", dbName,
			"-I",
			"-W",
			"-s", sep,
			"-b",
			"-i", tmpPath,
		}
		authArgs := mssqlAuthArgs(target, s)
		args := append(append([]string{}, baseArgs...), authArgs...)
		qCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(qCtx, sqlcmd, args...)
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return out, ctx.Err(), authArgs
		}
		// Automatic fallback to Windows Auth (-E) only on local instances
		if !isRemoteMSSQLHost(s) && err != nil && len(authArgs) > 1 && strings.Contains(strings.ToLower(string(out)), "login failed") {
			retryCmd := exec.CommandContext(qCtx, sqlcmd, append(append([]string{}, baseArgs...), "-E")...)
			hideWindow(retryCmd)
			out, err = retryCmd.CombinedOutput()
		}
		return out, err, authArgs
	}

	start := time.Now()
	target := mssqlServerTarget(s)
	out, err, _ := runWithTarget(target)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !isRemoteMSSQLHost(s) && err != nil {
		lowOut := strings.ToLower(string(out))
		if strings.Contains(lowOut, "named pipes") || strings.Contains(lowOut, "could not open a connection") || strings.Contains(lowOut, "server was not found") || strings.Contains(lowOut, "login timeout") {
			InvalidateMSSQLTargetCache()
			if freshTarget := mssqlServerTarget(s); freshTarget != "" && freshTarget != target {
				out, err, _ = runWithTarget(freshTarget)
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
		}
	}
	dur := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		return nil, errors.New(formatMSSQLError(strings.ReplaceAll(string(out), "\r\n", "\n"), err, dbName, s))
	}

	return parseMSSQLOutput(out, limit, dur), nil
}

// parseMSSQLOutput parses tab-separated sqlcmd stdout/stderr into a QueryResult without duplicating the full buffer.
func parseMSSQLOutput(out []byte, limit int, durMs float64) *QueryResult {
	const sep = "\t"
	if limit <= 0 {
		limit = maxShowRows
	}
	capHint := 64
	if limit < capHint {
		capHint = limit
	}
	res := &QueryResult{
		Columns:    []string{},
		Rows:       make([][]*string, 0, capHint),
		DurationMs: durMs,
	}

	type lineSpan struct{ start, end int }
	var spans []lineSpan
	for pos := 0; pos <= len(out); {
		nl := bytes.IndexByte(out[pos:], '\n')
		end := len(out)
		if nl >= 0 {
			end = pos + nl
		}
		lineEnd := end
		if lineEnd > pos && out[lineEnd-1] == '\r' {
			lineEnd--
		}
		spans = append(spans, lineSpan{pos, lineEnd})
		if nl < 0 {
			break
		}
		pos = end + 1
	}

	lineAt := func(idx int) string {
		sp := spans[idx]
		return string(out[sp.start:sp.end])
	}

	headerIdx := -1
	for i := 0; i+1 < len(spans); i++ {
		if len(bytes.TrimSpace(out[spans[i].start:spans[i].end])) == 0 {
			continue
		}
		if isDashRow(lineAt(i+1), sep) {
			headerIdx = i
			break
		}
	}

	if headerIdx >= 0 {
		res.IsQuery = true
		res.Columns = strings.Split(lineAt(headerIdx), sep)
		colCount := len(res.Columns)
		for i := headerIdx + 2; i < len(spans); i++ {
			sp := spans[i]
			if len(bytes.TrimSpace(out[sp.start:sp.end])) == 0 {
				continue
			}
			line := string(out[sp.start:sp.end])
			trimmed := strings.TrimSpace(line)
			if m := affectedRe.FindStringSubmatch(trimmed); m != nil {
				res.Affected, _ = strconv.ParseInt(m[1], 10, 64)
				break
			}
			if len(res.Rows) >= limit {
				res.Truncated = true
				break
			}
			parts := strings.Split(line, sep)
			row := make([]*string, colCount)
			for c := 0; c < colCount && c < len(parts); c++ {
				val := parts[c]
				if val == "NULL" {
					row[c] = nil
				} else {
					v := val
					row[c] = &v
				}
			}
			res.Rows = append(res.Rows, row)
		}
		if res.Affected == 0 {
			res.Affected = int64(len(res.Rows))
		}
		return res
	}

	for i := range spans {
		sp := spans[i]
		trimmed := strings.TrimSpace(string(out[sp.start:sp.end]))
		if m := affectedRe.FindStringSubmatch(trimmed); m != nil {
			res.Affected, _ = strconv.ParseInt(m[1], 10, 64)
		}
	}
	return res
}

func isDashRow(line, sep string) bool {
	if line == "" {
		return false
	}
	for _, p := range strings.Split(line, sep) {
		if p == "" || strings.Trim(p, "-") != "" {
			return false
		}
	}
	return true
}

func mssqlBracket(ident string) string {
	return "[" + strings.ReplaceAll(ident, "]", "]]") + "]"
}

func formatMSSQLError(raw string, err error, dbName string, s EngineSettings) string {
	msg := strings.TrimSpace(raw)
	if msg == "" && err != nil {
		msg = err.Error()
	}
	low := strings.ToLower(msg)
	if strings.Contains(low, "cannot open database") && strings.Contains(low, "requested by the login") {
		targetLabel := dbName
		if targetLabel == "" {
			targetLabel = s.DefaultDB
		}
		if !isRemoteMSSQLHost(s) {
			return fmt.Sprintf("Cannot open local MS SQL database %q — it does not exist or is offline on LocalDB. Select an existing local DB or Pull it from Cloud in Sync first. (%s)", targetLabel, msg)
		}
		return fmt.Sprintf("Cannot open remote MS SQL database %q on %s — verify the database name and SQL login credentials. (%s)", targetLabel, fallbackStr(strings.TrimSpace(s.Host), "server"), msg)
	}
	return msg
}

func mssqlListDatabases(s EngineSettings) ([]string, error) {
	queryDB := "master"
	q := "SET NOCOUNT ON; SELECT name FROM sys.databases WHERE state_desc = 'ONLINE' ORDER BY name;"
	if isRemoteMSSQLHost(s) {
		if s.DefaultDB != "" {
			queryDB = s.DefaultDB
		}
		q = "SET NOCOUNT ON; SELECT name FROM sys.databases WHERE state_desc = 'ONLINE' AND HAS_DBACCESS(name) = 1 ORDER BY name;"
	}
	res, err := mssqlRunQuery(s, queryDB, q)
	if err != nil {
		if isRemoteMSSQLHost(s) && s.DefaultDB != "" {
			return []string{s.DefaultDB}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(res.Rows))
	seenDefault := false
	for _, r := range res.Rows {
		if len(r) > 0 && r[0] != nil {
			if s.DefaultDB != "" && strings.EqualFold(*r[0], s.DefaultDB) {
				seenDefault = true
			}
			out = append(out, *r[0])
		}
	}
	if s.DefaultDB != "" && !seenDefault {
		out = append([]string{s.DefaultDB}, out...)
	}
	return out, nil
}

func mssqlListTables(s EngineSettings, dbName string) ([]TableInfo, error) {
	res, err := mssqlRunQuery(s, dbName, `SET NOCOUNT ON;
SELECT s.name, o.name, CASE o.type WHEN 'V' THEN 'VIEW' ELSE 'BASE TABLE' END
FROM sys.objects o
INNER JOIN sys.schemas s ON o.schema_id = s.schema_id
WHERE o.type IN ('U', 'V') AND o.is_ms_shipped = 0
ORDER BY s.name, o.name;`)
	if err != nil {
		return nil, err
	}
	out := make([]TableInfo, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) < 3 || r[0] == nil || r[1] == nil || r[2] == nil {
			continue
		}
		schema, name, kind := *r[0], *r[1], *r[2]
		display := name
		if !strings.EqualFold(schema, "dbo") {
			display = schema + "." + name
		}
		out = append(out, TableInfo{
			Schema:    schema,
			Name:      display,
			Kind:      kind,
			Select:    fmt.Sprintf("SELECT TOP 100 * FROM %s.%s;", mssqlBracket(schema), mssqlBracket(name)),
			SelectAll: fmt.Sprintf("SELECT * FROM %s.%s;", mssqlBracket(schema), mssqlBracket(name)),
		})
	}
	return out, nil
}

func mssqlCreateDatabase(s EngineSettings, name, user, password string) error {
	if !nameRe.MatchString(name) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	if user != "" && !nameRe.MatchString(user) {
		return errors.New("user name may only contain letters, digits, _ $ -")
	}
	if _, err := mssqlRunQuery(s, "master", "CREATE DATABASE "+mssqlBracket(name)+";"); err != nil {
		return err
	}
	if user == "" {
		return nil
	}
	pwLit := "'" + strings.ReplaceAll(password, "'", "''") + "'"
	loginSQL := fmt.Sprintf("IF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = N'%s') CREATE LOGIN %s WITH PASSWORD = N%s, CHECK_POLICY = OFF;",
		strings.ReplaceAll(user, "'", "''"), mssqlBracket(user), pwLit)
	if _, err := mssqlRunQuery(s, "master", loginSQL); err != nil {
		return err
	}
	userSQL := fmt.Sprintf("CREATE USER %s FOR LOGIN %s; ALTER ROLE db_owner ADD MEMBER %s;",
		mssqlBracket(user), mssqlBracket(user), mssqlBracket(user))
	_, err := mssqlRunQuery(s, name, userSQL)
	return err
}

func mssqlDropDatabase(s EngineSettings, name string) error {
	if systemDBs[strings.ToLower(name)] {
		return fmt.Errorf("%q is a system database and cannot be dropped here", name)
	}
	q := fmt.Sprintf("ALTER DATABASE %s SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE %s;",
		mssqlBracket(name), mssqlBracket(name))
	_, err := mssqlRunQuery(s, "master", q)
	return err
}

func mssqlLit(rv RowValue) string {
	if rv.IsNull {
		return "NULL"
	}
	return "N'" + strings.ReplaceAll(rv.Value, "'", "''") + "'"
}

func mssqlWhereClause(keys map[string]RowValue) string {
	colNames := sortedKeys(keys)
	var parts []string
	for _, col := range colNames {
		rv := keys[col]
		if rv.IsNull {
			parts = append(parts, mssqlBracket(col)+" IS NULL")
		} else {
			parts = append(parts, mssqlBracket(col)+" = "+mssqlLit(rv))
		}
	}
	return strings.Join(parts, " AND ")
}

func mssqlDescribeTable(s EngineSettings, dbName, schema, table string) ([]ColumnInfo, error) {
	escObj := strings.ReplaceAll("["+strings.ReplaceAll(schema, "]", "]]")+"].["+strings.ReplaceAll(table, "]", "]]")+"]", "'", "''")
	q := fmt.Sprintf(`SET NOCOUNT ON;
DECLARE @obj INT = OBJECT_ID(N'%s');
SELECT c.name,
       t.name + CASE
         WHEN t.name IN ('varchar','char','varbinary','binary')
           THEN '(' + CASE WHEN c.max_length = -1 THEN 'MAX' ELSE CAST(c.max_length AS VARCHAR(16)) END + ')'
         WHEN t.name IN ('nvarchar','nchar')
           THEN '(' + CASE WHEN c.max_length = -1 THEN 'MAX' ELSE CAST(c.max_length / 2 AS VARCHAR(16)) END + ')'
         ELSE ''
       END,
       CASE WHEN c.is_nullable = 1 THEN 'YES' ELSE 'NO' END,
       dc.definition,
       CASE WHEN EXISTS (
         SELECT 1
         FROM sys.index_columns ic
         INNER JOIN sys.indexes ix ON ix.object_id = ic.object_id AND ix.index_id = ic.index_id
         WHERE ic.object_id = c.object_id AND ic.column_id = c.column_id AND ix.is_primary_key = 1
       ) THEN '1' ELSE '0' END,
       CASE WHEN c.is_identity = 1 THEN '1' ELSE '0' END
FROM sys.columns c
INNER JOIN sys.types t ON c.user_type_id = t.user_type_id
LEFT JOIN sys.default_constraints dc ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
WHERE c.object_id = @obj
ORDER BY c.column_id;`, escObj)

	res, err := mssqlRunQuery(s, dbName, q)
	if err != nil {
		return nil, err
	}
	cols := make([]ColumnInfo, 0, len(res.Rows))
	for _, r := range res.Rows {
		name := valAt(r, 0)
		if name == "" {
			continue
		}
		var defPtr *string
		if len(r) > 3 && r[3] != nil {
			v := *r[3]
			defPtr = &v
		}
		cols = append(cols, ColumnInfo{
			Name:            name,
			DataType:        valAt(r, 1),
			Nullable:        strings.EqualFold(valAt(r, 2), "YES"),
			DefaultVal:      defPtr,
			IsPrimaryKey:    valAt(r, 4) == "1",
			IsAutoIncrement: valAt(r, 5) == "1",
		})
	}
	return cols, nil
}

func mssqlInsertRow(s EngineSettings, dbName, schema, table string, values map[string]RowValue) error {
	colNames := sortedKeys(values)
	var cols []string
	var vals []string
	for _, c := range colNames {
		cols = append(cols, mssqlBracket(c))
		vals = append(vals, mssqlLit(values[c]))
	}
	q := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s);",
		mssqlBracket(schema), mssqlBracket(table), strings.Join(cols, ", "), strings.Join(vals, ", "))
	_, err := mssqlRunQuery(s, dbName, q)
	return err
}

func mssqlUpdateRow(s EngineSettings, dbName, schema, table string, keys, updates map[string]RowValue, hasPK bool) error {
	updNames := sortedKeys(updates)
	var setParts []string
	for _, c := range updNames {
		setParts = append(setParts, mssqlBracket(c)+" = "+mssqlLit(updates[c]))
	}
	prefix := "UPDATE"
	if !hasPK {
		prefix = "UPDATE TOP (1)"
	}
	q := fmt.Sprintf("%s %s.%s SET %s WHERE %s;",
		prefix, mssqlBracket(schema), mssqlBracket(table), strings.Join(setParts, ", "), mssqlWhereClause(keys))
	res, err := mssqlRunQuery(s, dbName, q)
	if err != nil {
		return err
	}
	if res.Affected == 0 {
		return errors.New("no matching row was updated")
	}
	return nil
}

func mssqlDeleteRow(s EngineSettings, dbName, schema, table string, keys map[string]RowValue, hasPK bool) error {
	prefix := "DELETE"
	if !hasPK {
		prefix = "DELETE TOP (1)"
	}
	q := fmt.Sprintf("%s FROM %s.%s WHERE %s;",
		prefix, mssqlBracket(schema), mssqlBracket(table), mssqlWhereClause(keys))
	res, err := mssqlRunQuery(s, dbName, q)
	if err != nil {
		return err
	}
	if res.Affected == 0 {
		return errors.New("no matching row was deleted")
	}
	return nil
}
