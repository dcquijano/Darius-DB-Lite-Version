//go:build windows

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

const mssqlConf = `# Darius DB (Lite Version 0.1.1) - MS SQL Server configuration
# Supports Microsoft SQL Server LocalDB ((localdb)\MSSQLLocalDB) and TCP (127.0.0.1,1433).
instance = MSSQLLocalDB
server = (localdb)\MSSQLLocalDB
tcp_port = 1433
auth = windows
query_timeout = 30
`

func (m *mssqlEngine) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	if err := writeIfMissing(m.ConfigFile(p, inst), mssqlConf); err != nil {
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
	return runQuiet(context.Background(), nil, localDB, "stop", mssqlInstance)
}

func (m *mssqlEngine) PidOf(p *Paths, inst Installation) int {
	if localDB := findSqlLocalDB(); localDB != "" {
		cmd := exec.Command(localDB, "info", mssqlInstance)
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err == nil {
			if strings.Contains(strings.ToLower(string(out)), "state:") &&
				strings.Contains(strings.ToLower(string(out)), "running") {
				if pid := findProcessPID("sqlservr.exe"); pid > 0 {
					return pid
				}
			}
		}
	}
	return 0
}

// detectSystemMSSQL checks if SQL Server LocalDB or sqlcmd is installed on Windows.
func detectSystemMSSQL() (Installation, bool) {
	if localDB := findSqlLocalDB(); localDB != "" {
		ver := "17.0"
		cmd := exec.Command(localDB, "versions")
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err == nil {
			if m := versionRe.FindString(string(out)); m != "" {
				ver = m
			}
		}
		return Installation{
			Engine:  "mssql",
			Name:    "mssql-localdb-" + ver,
			Version: ver,
			Dir:     filepath.Dir(localDB),
		}, true
	}
	if sc := findSystemSqlcmd(); sc != "" {
		return Installation{
			Engine:  "mssql",
			Name:    "mssql-sqlcmd",
			Version: "15.0",
			Dir:     filepath.Dir(sc),
		}, true
	}
	return Installation{}, false
}

func findSqlLocalDB() string {
	if p, err := exec.LookPath("SqlLocalDB.exe"); err == nil {
		return p
	}
	for _, ver := range []string{"170", "160", "150", "140", "130"} {
		p := filepath.Join(`C:\Program Files\Microsoft SQL Server`, ver, `Tools\Binn\SqlLocalDB.exe`)
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func findSystemSqlcmd() string {
	if p, err := exec.LookPath("sqlcmd.exe"); err == nil {
		return p
	}
	for _, ver := range []string{"180", "170", "160", "150"} {
		for _, base := range []string{
			filepath.Join(`C:\Program Files\Microsoft SQL Server\Client SDK\ODBC`, ver, `Tools\Binn\SQLCMD.EXE`),
			filepath.Join(`C:\Program Files\Microsoft SQL Server`, ver, `Tools\Binn\SQLCMD.EXE`),
		} {
			if fileExists(base) {
				return base
			}
		}
	}
	return ""
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

func mssqlServerTarget(s EngineSettings) string {
	if s.Port > 0 && portInUse(s.Port) {
		return fmt.Sprintf("127.0.0.1,%d", s.Port)
	}
	if findSqlLocalDB() != "" {
		return `(localdb)\` + mssqlInstance
	}
	return fmt.Sprintf("127.0.0.1,%d", s.Port)
}

var affectedRe = regexp.MustCompile(`^\((\d+) rows? affected\)$`)

func mssqlRunQuery(s EngineSettings, dbName, q string) (*QueryResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("query is empty")
	}
	sqlcmd := findSqlcmd()
	if sqlcmd == "" {
		return nil, errors.New("sqlcmd.exe not found — download Portable sqlcmd in the Versions tab")
	}
	if dbName == "" {
		dbName = "master"
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
	args := []string{
		"-S", mssqlServerTarget(s),
		"-d", dbName,
		"-I",
		"-W",
		"-s", sep,
		"-b",
		"-i", tmpPath,
	}
	if s.User != "" && s.Password != "" {
		args = append(args, "-U", s.User, "-P", s.Password)
	} else {
		args = append(args, "-E")
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqlcmd, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	dur := float64(time.Since(start).Microseconds()) / 1000.0
	text := strings.ReplaceAll(string(out), "\r\n", "\n")

	if err != nil {
		msg := strings.TrimSpace(text)
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}

	lines := strings.Split(text, "\n")
	res := &QueryResult{
		Columns:    []string{},
		Rows:       [][]*string{},
		DurationMs: dur,
	}

	// Find header + dash separator line if the query returned a result set
	headerIdx := -1
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if isDashRow(lines[i+1], sep) {
			headerIdx = i
			break
		}
	}

	if headerIdx >= 0 {
		res.IsQuery = true
		res.Columns = strings.Split(lines[headerIdx], sep)
		for i := headerIdx + 2; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if m := affectedRe.FindStringSubmatch(trimmed); m != nil {
				res.Affected, _ = strconv.ParseInt(m[1], 10, 64)
				break
			}
			if len(res.Rows) >= maxRows {
				res.Truncated = true
				continue
			}
			parts := strings.Split(line, sep)
			row := make([]*string, len(res.Columns))
			for c := 0; c < len(res.Columns) && c < len(parts); c++ {
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
		res.Affected = int64(len(res.Rows))
		return res, nil
	}

	for _, l := range lines {
		if m := affectedRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			res.Affected, _ = strconv.ParseInt(m[1], 10, 64)
		}
	}
	return res, nil
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

func mssqlListDatabases(s EngineSettings) ([]string, error) {
	res, err := mssqlRunQuery(s, "master", "SET NOCOUNT ON; SELECT name FROM sys.databases ORDER BY name;")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 0 && r[0] != nil {
			out = append(out, *r[0])
		}
	}
	return out, nil
}

func mssqlListTables(s EngineSettings, dbName string) ([]TableInfo, error) {
	res, err := mssqlRunQuery(s, dbName, "SET NOCOUNT ON; SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE FROM INFORMATION_SCHEMA.TABLES ORDER BY TABLE_SCHEMA, TABLE_NAME;")
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
			Schema: schema,
			Name:   display,
			Kind:   kind,
			Select: fmt.Sprintf("SELECT TOP 100 * FROM %s.%s;", mssqlBracket(schema), mssqlBracket(name)),
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
