//go:build windows

package core

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var cleanDBNameRe = regexp.MustCompile(`[^A-Za-z0-9_$\-]`)

func DeriveDBNameFromFile(filePath string) string {
	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	base = cleanDBNameRe.ReplaceAllString(base, "_")
	base = strings.Trim(base, "_-")
	if base == "" {
		return "restored_db"
	}
	if base[0] >= '0' && base[0] <= '9' {
		base = "db_" + base
	}
	if len(base) > 60 {
		base = base[:60]
	}
	return base
}

// RestoreDatabase restores a .bak, .sql, or .backup/.dump file into targetDB.
// Returns the target database name that was restored.
func RestoreDatabase(p *Paths, engine string, s EngineSettings, filePath, targetDB string) (string, error) {
	return RestoreDatabaseWithProgress(p, engine, s, filePath, targetDB, nil)
}

// RestoreDatabaseWithProgress behaves like RestoreDatabase while streaming byte progress for large imports.
func RestoreDatabaseWithProgress(p *Paths, engine string, s EngineSettings, filePath, targetDB string, onBytes func(done, total int64)) (string, error) {
	return RestoreDatabaseWithContext(context.Background(), p, engine, s, filePath, targetDB, onBytes)
}

// RestoreDatabaseWithContext behaves like RestoreDatabaseWithProgress and aborts promptly when ctx is cancelled.
func RestoreDatabaseWithContext(ctx context.Context, p *Paths, engine string, s EngineSettings, filePath, targetDB string, onBytes func(done, total int64)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return "", errors.New("no backup or SQL file selected")
	}
	st, err := os.Stat(filePath)
	if err != nil || st.IsDir() {
		return "", fmt.Errorf("file not found: %s", filePath)
	}

	targetDB = strings.TrimSpace(targetDB)
	if targetDB == "" {
		targetDB = DeriveDBNameFromFile(filePath)
	}

	ext := strings.ToLower(filepath.Ext(filePath))

	if ext == ".zip" {
		return targetDB, restoreDatabaseFromZip(p, engine, s, filePath, targetDB)
	}

	switch engine {
	case "mssql":
		if ext == ".bak" {
			return targetDB, mssqlRestoreBak(p, s, filePath, targetDB)
		}
		return targetDB, mssqlImportSQLCtx(ctx, s, filePath, targetDB)

	case "mariadb", "mysql":
		if ext == ".bak" {
			return "", fmt.Errorf(".bak is a Microsoft SQL Server backup format — switch to the MS SQL Server tab to restore %s", filepath.Base(filePath))
		}
		return targetDB, mysqlImportSQLWithProgressCtx(ctx, p, engine, s, filePath, targetDB, st.Size(), onBytes)

	case "postgresql":
		if ext == ".bak" {
			return "", fmt.Errorf(".bak is a Microsoft SQL Server backup format — switch to the MS SQL Server tab to restore %s", filepath.Base(filePath))
		}
		return targetDB, postgresRestoreCtx(ctx, p, s, filePath, targetDB, ext)

	default:
		return "", fmt.Errorf("restore/import is not supported for %s", engine)
	}
}

// BackupDatabase exports dbName to outPath (.bak for MS SQL Server, .sql for SQL scripts).
func BackupDatabase(p *Paths, engine string, s EngineSettings, dbName, outPath string) error {
	dbName = strings.TrimSpace(dbName)
	outPath = strings.TrimSpace(outPath)
	if dbName == "" {
		return errors.New("select a database to back up first")
	}
	if outPath == "" {
		return errors.New("no output file selected")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(outPath))

	switch engine {
	case "mssql":
		if ext == ".sql" {
			return mssqlExportSQL(s, dbName, outPath)
		}
		return mssqlBackupBak(s, dbName, outPath)

	case "mariadb", "mysql":
		return mysqlBackupSQL(p, engine, s, dbName, outPath)

	case "postgresql":
		return postgresBackupSQL(p, s, dbName, outPath)

	default:
		return fmt.Errorf("backup is not supported for %s", engine)
	}
}

// ---------------------------------------------------------------- MS SQL Server (.bak & .sql)

func mssqlRestoreBak(p *Paths, s EngineSettings, bakPath, targetDB string) error {
	if !nameRe.MatchString(targetDB) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	dataDir := filepath.Join(p.Data, "mssql")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	escapedBak := strings.ReplaceAll(bakPath, "'", "''")

	// 1. Inspect logical file list inside the .bak archive so we can build WITH MOVE clauses
	fileListQuery := fmt.Sprintf("SET NOCOUNT ON; RESTORE FILELISTONLY FROM DISK = N'%s';", escapedBak)
	res, err := mssqlRunQuery(s, "master", fileListQuery)
	if err != nil {
		return fmt.Errorf("cannot read .bak file %s: %v", filepath.Base(bakPath), err)
	}
	if len(res.Rows) == 0 {
		return fmt.Errorf("no database files found inside %s", filepath.Base(bakPath))
	}

	var moveClauses []string
	dataCount, logCount := 0, 0
	for i, row := range res.Rows {
		if len(row) == 0 || row[0] == nil {
			continue
		}
		logicalName := *row[0]
		fileType := "D"
		if len(row) > 2 && row[2] != nil {
			fileType = strings.ToUpper(strings.TrimSpace(*row[2]))
		}
		var destFile string
		switch fileType {
		case "L":
			logCount++
			suffix := "_log.ldf"
			if logCount > 1 {
				suffix = fmt.Sprintf("_log%d.ldf", logCount)
			}
			destFile = filepath.Join(dataDir, targetDB+suffix)
		default:
			dataCount++
			suffix := ".mdf"
			if dataCount > 1 {
				suffix = fmt.Sprintf("_%d.ndf", i+1)
			}
			destFile = filepath.Join(dataDir, targetDB+suffix)
		}
		moveClauses = append(moveClauses, fmt.Sprintf("MOVE N'%s' TO N'%s'",
			strings.ReplaceAll(logicalName, "'", "''"),
			strings.ReplaceAll(destFile, "'", "''"),
		))
	}

	withOpts := append(moveClauses, "REPLACE")
	restoreSQL := fmt.Sprintf(
		"IF DB_ID(N'%s') IS NOT NULL ALTER DATABASE %s SET SINGLE_USER WITH ROLLBACK IMMEDIATE;\r\n"+
			"RESTORE DATABASE %s FROM DISK = N'%s' WITH %s;\r\n"+
			"ALTER DATABASE %s SET MULTI_USER;",
		strings.ReplaceAll(targetDB, "'", "''"),
		mssqlBracket(targetDB),
		mssqlBracket(targetDB),
		escapedBak,
		strings.Join(withOpts, ", "),
		mssqlBracket(targetDB),
	)

	_, err = mssqlRunQuery(s, "master", restoreSQL)
	return err
}

func mssqlImportSQL(s EngineSettings, sqlPath, targetDB string) error {
	return mssqlImportSQLCtx(context.Background(), s, sqlPath, targetDB)
}

func mssqlImportSQLCtx(ctx context.Context, s EngineSettings, sqlPath, targetDB string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sqlcmd := findSqlcmd()
	if sqlcmd == "" {
		return errors.New("sqlcmd.exe not found")
	}
	if targetDB != "" && !systemDBs[strings.ToLower(targetDB)] {
		createQ := fmt.Sprintf("IF DB_ID(N'%s') IS NULL CREATE DATABASE %s;",
			strings.ReplaceAll(targetDB, "'", "''"), mssqlBracket(targetDB))
		if _, cerr := mssqlRunQueryCtx(ctx, s, "master", createQ); cerr != nil && !isRemoteMSSQLHost(s) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("cannot prepare local database %q: %w", targetDB, cerr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if targetDB == "" {
		targetDB = "master"
	}

	target := mssqlServerTarget(s)
	args := []string{
		"-S", target,
		"-d", targetDB,
		"-I",
		"-b",
		"-i", sqlPath,
	}
	args = append(args, mssqlAuthArgs(target, s)...)

	impCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(impCtx, sqlcmd, args...)
	cmd.WaitDelay = 2 * time.Second
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		msg := formatSqlcmdError(string(out), err)
		return errors.New(msg)
	}
	return nil
}

var sqlcmdRowCountRe = regexp.MustCompile(`(?i)^\(\d+\s+rows?\s+affected\)$`)

func formatSqlcmdError(raw string, err error) string {
	var keep []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		tr := strings.TrimSpace(line)
		if tr == "" || sqlcmdRowCountRe.MatchString(tr) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(tr), "changed database context to") {
			continue
		}
		keep = append(keep, tr)
	}
	msg := strings.TrimSpace(strings.Join(keep, "\n"))
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if len(msg) > 1200 {
		msg = msg[len(msg)-1200:]
		msg = "…" + msg
	}
	return msg
}

func mssqlBackupBak(s EngineSettings, dbName, outPath string) error {
	_ = os.Remove(outPath)
	q := fmt.Sprintf("BACKUP DATABASE %s TO DISK = N'%s' WITH FORMAT, INIT, NAME = N'%s-Full Database Backup';",
		mssqlBracket(dbName),
		strings.ReplaceAll(outPath, "'", "''"),
		strings.ReplaceAll(dbName, "'", "''"),
	)
	_, err := mssqlRunQuery(s, "master", q)
	return err
}

func mssqlExportSQL(s EngineSettings, dbName, outPath string) error {
	tables, err := mssqlListTables(s, dbName)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "-- Darius DB (Lite Version 0.1.6) MS SQL Server SQL Dump\r\n-- Database: %s\r\n-- Date: %s\r\n\r\n",
		dbName, time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "SET NOCOUNT ON;\r\nIF DB_ID(N'%s') IS NULL CREATE DATABASE %s;\r\nGO\r\nUSE %s;\r\nGO\r\n\r\n",
		strings.ReplaceAll(dbName, "'", "''"), mssqlBracket(dbName), mssqlBracket(dbName))

	for _, t := range tables {
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		_ = mssqlWriteTableSQLCtx(context.Background(), &b, s, dbName, t, true, true)
	}
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

type mssqlColMeta struct {
	name       string
	dataType   string
	maxLen     string
	isNullable string
	isIdentity bool
	isComputed bool
	precision  string
	scale      string
}

func isMSSQLBinaryType(dt string) bool {
	switch strings.ToLower(dt) {
	case "binary", "varbinary", "image":
		return true
	}
	return false
}

func isMSSQLAutoTimestampType(dt string) bool {
	switch strings.ToLower(dt) {
	case "timestamp", "rowversion":
		return true
	}
	return false
}

func isMSSQLTextType(dt string) bool {
	switch strings.ToLower(strings.TrimSpace(dt)) {
	case "varchar", "nvarchar", "char", "nchar", "text", "ntext", "xml", "sysname":
		return true
	}
	return false
}

func decodeMSSQLFieldToTSQLExpr(escaped string) string {
	if !strings.ContainsRune(escaped, '\\') {
		return "N'" + strings.ReplaceAll(escaped, "'", "''") + "'"
	}
	var parts []string
	var lit strings.Builder
	flushLit := func() {
		if lit.Len() > 0 {
			parts = append(parts, "N'"+strings.ReplaceAll(lit.String(), "'", "''")+"'")
			lit.Reset()
		}
	}
	for i := 0; i < len(escaped); i++ {
		if escaped[i] == '\\' && i+1 < len(escaped) {
			switch escaped[i+1] {
			case '\\':
				lit.WriteByte('\\')
				i++
				continue
			case 'r':
				flushLit()
				parts = append(parts, "CHAR(13)")
				i++
				continue
			case 'n':
				flushLit()
				parts = append(parts, "CHAR(10)")
				i++
				continue
			case 't':
				flushLit()
				parts = append(parts, "CHAR(9)")
				i++
				continue
			}
		}
		lit.WriteByte(escaped[i])
	}
	flushLit()
	if len(parts) == 0 {
		return "N''"
	}
	return strings.Join(parts, " + ")
}

func mssqlWriteTableSQL(b *strings.Builder, s EngineSettings, dbName string, t TableInfo, includeSchema, includeData bool) {
	_ = mssqlWriteTableSQLCtx(context.Background(), b, s, dbName, t, includeSchema, includeData)
}

func mssqlWriteTableSQLCtx(ctx context.Context, b io.Writer, s EngineSettings, dbName string, t TableInfo, includeSchema, includeData bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tableName := t.Name
	if idx := strings.IndexByte(tableName, '.'); idx >= 0 {
		tableName = tableName[idx+1:]
	}
	fullIdent := mssqlBracket(t.Schema) + "." + mssqlBracket(tableName)

	colQ := fmt.Sprintf(
		"SET NOCOUNT ON; SELECT COLUMN_NAME, DATA_TYPE, "+
			"ISNULL(CAST(CHARACTER_MAXIMUM_LENGTH AS VARCHAR(20)), ''), "+
			"ISNULL(IS_NULLABLE, 'YES'), "+
			"ISNULL(COLUMNPROPERTY(OBJECT_ID(QUOTENAME(TABLE_SCHEMA)+N'.'+QUOTENAME(TABLE_NAME)), COLUMN_NAME, 'IsIdentity'), 0), "+
			"ISNULL(COLUMNPROPERTY(OBJECT_ID(QUOTENAME(TABLE_SCHEMA)+N'.'+QUOTENAME(TABLE_NAME)), COLUMN_NAME, 'IsComputed'), 0), "+
			"ISNULL(CAST(NUMERIC_PRECISION AS VARCHAR(20)), ''), "+
			"ISNULL(CAST(NUMERIC_SCALE AS VARCHAR(20)), '') "+
			"FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = N'%s' AND TABLE_NAME = N'%s' ORDER BY ORDINAL_POSITION;",
		strings.ReplaceAll(t.Schema, "'", "''"), strings.ReplaceAll(tableName, "'", "''"),
	)
	var cols []mssqlColMeta
	colRes, err := mssqlRunQueryCtx(ctx, s, dbName, colQ)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		for _, cr := range colRes.Rows {
			cols = append(cols, mssqlColMeta{
				name:       valAt(cr, 0),
				dataType:   valAt(cr, 1),
				maxLen:     valAt(cr, 2),
				isNullable: valAt(cr, 3),
				isIdentity: valAt(cr, 4) == "1",
				isComputed: valAt(cr, 5) == "1",
				precision:  valAt(cr, 6),
				scale:      valAt(cr, 7),
			})
		}
	}

	if includeSchema && len(cols) > 0 {
		fmt.Fprintf(b, "IF OBJECT_ID(N'%s', N'U') IS NULL\r\nCREATE TABLE %s (\r\n", fullIdent, fullIdent)
		for i, c := range cols {
			typeSpec := c.dataType
			switch strings.ToLower(c.dataType) {
			case "varchar", "nvarchar", "char", "nchar", "varbinary", "binary":
				if c.maxLen == "-1" {
					typeSpec += "(MAX)"
				} else if c.maxLen != "" && c.maxLen != "NULL" {
					typeSpec += "(" + c.maxLen + ")"
				}
			case "decimal", "numeric":
				if c.precision != "" && c.precision != "NULL" {
					if c.scale != "" && c.scale != "NULL" {
						typeSpec += "(" + c.precision + "," + c.scale + ")"
					} else {
						typeSpec += "(" + c.precision + ")"
					}
				}
			}
			idSpec := ""
			if c.isIdentity {
				idSpec = " IDENTITY(1,1)"
			}
			nullSpec := "NULL"
			if strings.EqualFold(c.isNullable, "NO") {
				nullSpec = "NOT NULL"
			}
			comma := ","
			if i == len(cols)-1 {
				comma = ""
			}
			fmt.Fprintf(b, "  %s %s%s %s%s\r\n", mssqlBracket(c.name), typeSpec, idSpec, nullSpec, comma)
		}
		_, _ = io.WriteString(b, ");\r\nGO\r\n")
	}

	if includeData {
		var insertCols []mssqlColMeta
		for _, c := range cols {
			// Skip auto-generated SQL Server timestamp/rowversion and computed columns (Msg 273)
			if isMSSQLAutoTimestampType(c.dataType) || c.isComputed {
				continue
			}
			insertCols = append(insertCols, c)
		}
		var selectQ string
		if len(insertCols) > 0 {
			var selExprs []string
			for _, c := range insertCols {
				br := mssqlBracket(c.name)
				if isMSSQLBinaryType(c.dataType) {
					selExprs = append(selExprs, fmt.Sprintf("CONVERT(VARCHAR(MAX), %s, 1) AS %s", br, br))
				} else if isMSSQLTextType(c.dataType) {
					selExprs = append(selExprs, fmt.Sprintf("REPLACE(REPLACE(REPLACE(REPLACE(CAST(%s AS NVARCHAR(MAX)), N'\\', N'\\\\'), CHAR(13), N'\\r'), CHAR(10), N'\\n'), CHAR(9), N'\\t') AS %s", br, br))
				} else {
					selExprs = append(selExprs, br)
				}
			}
			selectQ = fmt.Sprintf("SELECT %s FROM %s;", strings.Join(selExprs, ", "), fullIdent)
		} else if len(cols) == 0 {
			selectQ = "SELECT * FROM " + fullIdent + ";"
		} else {
			return nil
		}

		dataRes, err := mssqlRunQueryWithLimitCtx(ctx, s, dbName, selectQ, maxShowRows)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && len(dataRes.Rows) > 0 && len(dataRes.Columns) > 0 {
			var colList []string
			hasIdentity := false
			for i, cName := range dataRes.Columns {
				colList = append(colList, mssqlBracket(cName))
				if i < len(insertCols) && insertCols[i].isIdentity {
					hasIdentity = true
				}
			}
			colsJoined := strings.Join(colList, ", ")
			if hasIdentity {
				fmt.Fprintf(b, "SET IDENTITY_INSERT %s ON;\r\n", fullIdent)
			}
			for _, row := range dataRes.Rows {
				var vals []string
				for i, cell := range row {
					if cell == nil {
						vals = append(vals, "NULL")
					} else if i < len(insertCols) && isMSSQLBinaryType(insertCols[i].dataType) && strings.HasPrefix(strings.ToLower(strings.TrimSpace(*cell)), "0x") {
						vals = append(vals, strings.TrimSpace(*cell))
					} else if i < len(insertCols) && isMSSQLTextType(insertCols[i].dataType) {
						vals = append(vals, decodeMSSQLFieldToTSQLExpr(*cell))
					} else {
						vals = append(vals, "N'"+strings.ReplaceAll(*cell, "'", "''")+"'")
					}
				}
				fmt.Fprintf(b, "INSERT INTO %s (%s) VALUES (%s);\r\n", fullIdent, colsJoined, strings.Join(vals, ", "))
			}
			if hasIdentity {
				fmt.Fprintf(b, "SET IDENTITY_INSERT %s OFF;\r\n", fullIdent)
			}
			_, _ = io.WriteString(b, "GO\r\n\r\n")
		}
	}
	return nil
}

func valAt(row []*string, idx int) string {
	if idx < len(row) && row[idx] != nil {
		return *row[idx]
	}
	return ""
}

// ---------------------------------------------------------------- MariaDB / MySQL (.sql)

type countingProgressReader struct {
	r        io.Reader
	total    int64
	read     int64
	lastEmit time.Time
	onBytes  func(done, total int64)
}

func (c *countingProgressReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.read += int64(n)
		if c.onBytes != nil && (time.Since(c.lastEmit) >= 250*time.Millisecond || err == io.EOF) {
			c.lastEmit = time.Now()
			c.onBytes(c.read, c.total)
		}
	}
	return n, err
}

func findMySQLClientTool(p *Paths, engine string, s EngineSettings, names ...string) string {
	candidates := []string{engine}
	if engine == "mysql" {
		candidates = append(candidates, "mariadb")
	} else if engine == "mariadb" {
		candidates = append(candidates, "mysql")
	}
	for _, cand := range candidates {
		ver := ""
		if cand == engine {
			ver = s.Version
		}
		if inst, ok := FindInstallation(p, cand, ver); ok {
			for _, n := range names {
				full := filepath.Join(inst.Dir, "bin", n)
				if fileExists(full) {
					return full
				}
			}
		}
	}
	return ""
}

// appendMySQLClientConnArgs adds plugin-dir (needed for caching_sha2_password with MariaDB CLI)
// and TLS mode flags matching EngineSettings.SSLMode for MySQL/MariaDB client binaries.
func appendMySQLClientConnArgs(cliPath string, s EngineSettings, args []string) []string {
	pluginDir := filepath.Join(filepath.Dir(filepath.Dir(cliPath)), "lib", "plugin")
	if dirExists(pluginDir) {
		args = append(args, "--plugin-dir="+pluginDir)
	}
	isMariaCLI := strings.HasPrefix(strings.ToLower(filepath.Base(cliPath)), "mariadb")
	ssl := strings.ToLower(strings.TrimSpace(s.SSLMode))
	if isMariaCLI {
		switch ssl {
		case "disable", "none", "false":
			args = append(args, "--skip-ssl")
		case "verify-full", "verify-ca":
			args = append(args, "--ssl")
		default:
			args = append(args, "--skip-ssl-verify-server-cert")
		}
	} else {
		switch ssl {
		case "disable", "none", "false":
			args = append(args, "--ssl-mode=DISABLED")
		case "require":
			args = append(args, "--ssl-mode=REQUIRED")
		case "verify-full":
			args = append(args, "--ssl-mode=VERIFY_IDENTITY")
		}
	}
	return args
}

func isMySQLBinaryCol(dbType string) bool {
	up := strings.ToUpper(dbType)
	return strings.Contains(up, "BLOB") || strings.Contains(up, "BINARY")
}

func mysqlImportSQL(p *Paths, engine string, s EngineSettings, sqlPath, targetDB string) error {
	var total int64
	if st, err := os.Stat(sqlPath); err == nil {
		total = st.Size()
	}
	return mysqlImportSQLWithProgress(p, engine, s, sqlPath, targetDB, total, nil)
}

func mysqlImportSQLWithProgress(p *Paths, engine string, s EngineSettings, sqlPath, targetDB string, totalBytes int64, onBytes func(done, total int64)) error {
	return mysqlImportSQLWithProgressCtx(context.Background(), p, engine, s, sqlPath, targetDB, totalBytes, onBytes)
}

func mysqlImportSQLWithProgressCtx(ctx context.Context, p *Paths, engine string, s EngineSettings, sqlPath, targetDB string, totalBytes int64, onBytes func(done, total int64)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !nameRe.MatchString(targetDB) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	host := fallbackStr(strings.TrimSpace(s.Host), "127.0.0.1")
	isRemote := !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost")
	if db, err := openSQL(engine, s, ""); err == nil {
		if !isRemote || !strings.EqualFold(targetDB, s.DefaultDB) {
			_, _ = db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+quoteIdent(engine, targetDB)+" CHARACTER SET utf8mb4")
		}
		if !isRemote {
			_, _ = db.ExecContext(ctx, "SET GLOBAL max_allowed_packet=268435456")
		}
		db.Close()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Prefer native client binary with --binary-mode=1 and --max-allowed-packet=256M for fast BLOB-safe streaming
	if cli := findMySQLClientTool(p, engine, s, "mariadb.exe", "mysql.exe"); cli != "" {
		f, err := os.Open(sqlPath)
		if err != nil {
			return err
		}
		defer f.Close()
		if totalBytes <= 0 {
			if st, serr := f.Stat(); serr == nil {
				totalBytes = st.Size()
			}
		}
		port := s.Port
		if port <= 0 {
			port = 3306
		}
		args := []string{
			"-h", host,
			"-P", fmt.Sprint(port),
			"-u", fallbackStr(s.User, "root"),
			"--binary-mode=1",
			"--max-allowed-packet=256M",
			"--default-character-set=utf8mb4",
		}
		args = appendMySQLClientConnArgs(cli, s, args)
		args = append(args, targetDB)
		if s.Password != "" {
			args = append([]string{"-p" + s.Password}, args...)
		}
		impCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(impCtx, cli, args...)
		cmd.WaitDelay = 2 * time.Second
		hideWindow(cmd)
		if onBytes != nil && totalBytes > 0 {
			cmd.Stdin = &countingProgressReader{r: f, total: totalBytes, onBytes: onBytes}
		} else {
			cmd.Stdin = f
		}
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
		return nil
	}

	// Fallback: execute via Go SQL driver (MultiStatements=true)
	b, err := os.ReadFile(sqlPath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	targetConn, err := openSQLCtx(ctx, engine, s, targetDB)
	if err != nil {
		return err
	}
	defer targetConn.Close()
	_, err = targetConn.ExecContext(ctx, string(b))
	return err
}

func mysqlBackupSQL(p *Paths, engine string, s EngineSettings, dbName, outPath string) error {
	return mysqlExportTablesSQLWithProgress(p, engine, s, dbName, nil, true, true, outPath, true, nil)
}

func mysqlWriteGoDump(engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, onTable func(idx, total int, tblName string)) error {
	return mysqlWriteGoDumpCtx(context.Background(), engine, s, dbName, tables, includeSchema, includeData, outPath, onTable)
}

func mysqlWriteGoDumpCtx(ctx context.Context, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, onTable func(idx, total int, tblName string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := openSQLCtx(ctx, engine, s, dbName)
	if err != nil {
		return err
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()

	reconnect := func() error {
		if db != nil {
			_ = db.Close()
			db = nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		ndb, oerr := openSQLCtx(ctx, engine, s, dbName)
		if oerr != nil {
			return oerr
		}
		db = ndb
		return nil
	}

	var allTables []TableInfo
	for attempt := 0; attempt < 3; attempt++ {
		allTables, err = listTablesWithDBCtx(ctx, engine, db, dbName)
		if err == nil {
			break
		}
		if attempt < 2 && isTransientConnErr(err) && ctx.Err() == nil {
			if rerr := reconnect(); rerr == nil {
				continue
			}
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, t := range tables {
		selected[strings.ToLower(strings.TrimSpace(t))] = true
	}
	var todo []TableInfo
	for _, t := range allTables {
		if len(selected) > 0 && !selected[strings.ToLower(t.Name)] {
			continue
		}
		todo = append(todo, t)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 256*1024)
	defer bw.Flush()

	fmt.Fprintf(bw, "-- Darius DB (Lite Version 0.1.6) Binary-Safe SQL Export\n-- Engine: %s  Database: %s\n\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;\nSET UNIQUE_CHECKS=0;\nSET AUTOCOMMIT=0;\n\n", engine, dbName)

	for i, t := range todo {
		if err := ctx.Err(); err != nil {
			return err
		}
		if onTable != nil {
			onTable(i+1, len(todo), t.Name)
		}
		qt := quoteIdent(engine, t.Name)

		dumpOneTable := func() (bool, error) {
			wroteStream := false
			var headerSQL string
			if includeSchema {
				var tbl, createSQL string
				if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+qt).Scan(&tbl, &createSQL); err == nil {
					headerSQL = fmt.Sprintf("DROP TABLE IF EXISTS %s;\n%s;\n\n", qt, createSQL)
				} else if ctx.Err() != nil {
					return false, ctx.Err()
				} else if isTransientConnErr(err) {
					return false, fmt.Errorf("show create %s: %w", t.Name, err)
				}
			}
			writeHeaderOnce := func() error {
				if headerSQL == "" {
					return nil
				}
				wroteStream = true
				_, werr := bw.WriteString(headerSQL)
				headerSQL = ""
				return werr
			}

			if !includeData || strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
				if err := writeHeaderOnce(); err != nil {
					return wroteStream, err
				}
				return wroteStream, nil
			}

			rows, err := db.QueryContext(ctx, "SELECT * FROM "+qt)
			if err != nil {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return false, fmt.Errorf("query %s: %w", t.Name, err)
			}
			colTypes, _ := rows.ColumnTypes()
			cols, _ := rows.Columns()
			binCols := make([]bool, len(cols))
			for cIdx, ct := range colTypes {
				if ct != nil && isMySQLBinaryCol(ct.DatabaseTypeName()) {
					binCols[cIdx] = true
				}
			}

			raw := make([]sql.RawBytes, len(cols))
			ptrs := make([]interface{}, len(cols))
			for idx := range raw {
				ptrs[idx] = &raw[idx]
			}

			var colNames []string
			for _, c := range cols {
				colNames = append(colNames, quoteIdent(engine, c))
			}
			insertHead := fmt.Sprintf("INSERT INTO %s (%s) VALUES\n", qt, strings.Join(colNames, ", "))

			var batchBuf strings.Builder
			batchRows := 0
			flushBatch := func() error {
				if batchRows == 0 {
					return nil
				}
				if werr := writeHeaderOnce(); werr != nil {
					return werr
				}
				wroteStream = true
				_, werr := bw.WriteString(insertHead)
				if werr == nil {
					_, werr = bw.WriteString(batchBuf.String())
				}
				if werr == nil {
					_, werr = bw.WriteString(";\n")
				}
				batchBuf.Reset()
				batchRows = 0
				return werr
			}

			for rows.Next() {
				if err := ctx.Err(); err != nil {
					rows.Close()
					return wroteStream, err
				}
				if err := rows.Scan(ptrs...); err != nil {
					rows.Close()
					return wroteStream, fmt.Errorf("scan %s: %w", t.Name, err)
				}
				if batchRows > 0 {
					batchBuf.WriteString(",\n")
				}
				batchBuf.WriteByte('(')
				for cIdx, b := range raw {
					if cIdx > 0 {
						batchBuf.WriteString(", ")
					}
					if b == nil {
						batchBuf.WriteString("NULL")
					} else if binCols[cIdx] {
						if len(b) == 0 {
							batchBuf.WriteString("''")
						} else {
							batchBuf.WriteString("0x")
							batchBuf.WriteString(hex.EncodeToString(b))
						}
					} else {
						batchBuf.WriteByte('\'')
						batchBuf.WriteString(sqlEscape(string(b)))
						batchBuf.WriteByte('\'')
					}
				}
				batchBuf.WriteByte(')')
				batchRows++
				if batchBuf.Len() >= 256*1024 || batchRows >= 250 {
					if err := flushBatch(); err != nil {
						rows.Close()
						return wroteStream, err
					}
				}
			}
			if rerr := rows.Err(); rerr != nil {
				rows.Close()
				return wroteStream, rerr
			}
			rows.Close()
			if err := writeHeaderOnce(); err != nil {
				return wroteStream, err
			}
			if err := flushBatch(); err != nil {
				return wroteStream, err
			}
			if _, err := bw.WriteString("\n"); err != nil {
				return wroteStream, err
			}
			return wroteStream, nil
		}

		var tableErr error
		for attempt := 0; attempt < 3; attempt++ {
			var wroteStream bool
			wroteStream, tableErr = dumpOneTable()
			if tableErr == nil {
				break
			}
			if attempt < 2 && !wroteStream && isTransientConnErr(tableErr) && ctx.Err() == nil {
				if rerr := reconnect(); rerr == nil {
					continue
				}
			}
			return tableErr
		}
	}

	_, _ = bw.WriteString("COMMIT;\nSET AUTOCOMMIT=1;\nSET UNIQUE_CHECKS=1;\nSET FOREIGN_KEY_CHECKS=1;\n")
	return bw.Flush()
}

// ---------------------------------------------------------------- PostgreSQL (.sql & .backup/.dump)

// isPostgresArchiveDump inspects file contents to detect PostgreSQL custom-format
// ("PGDMP"), tar-format ("ustar" containing toc.dat), or directory-format dumps,
// even when saved with a .sql extension.
func isPostgresArchiveDump(filePath string) bool {
	if st, err := os.Stat(filePath); err == nil && st.IsDir() {
		return fileExists(filepath.Join(filePath, "toc.dat"))
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [512]byte
	n, err := io.ReadAtLeast(f, hdr[:], 5)
	if err != nil && n < 5 {
		return false
	}
	if bytes.HasPrefix(hdr[:n], []byte("PGDMP")) {
		return true
	}
	if n >= 265 && string(hdr[257:262]) == "ustar" && bytes.Contains(hdr[:100], []byte("toc.dat")) {
		return true
	}
	return false
}

func findPostgresTool(p *Paths, s EngineSettings, toolName string) string {
	if inst, ok := FindInstallation(p, "postgresql", s.Version); ok {
		cand := filepath.Join(inst.Dir, "bin", toolName)
		if fileExists(cand) {
			return cand
		}
	}
	if p != nil {
		for _, inst := range ListInstallations(p, "postgresql") {
			cand := filepath.Join(inst.Dir, "bin", toolName)
			if fileExists(cand) {
				return cand
			}
		}
	}
	if lp, err := exec.LookPath(toolName); err == nil {
		return lp
	}
	return ""
}

func postgresRestore(p *Paths, s EngineSettings, filePath, targetDB, ext string) error {
	return postgresRestoreCtx(context.Background(), p, s, filePath, targetDB, ext)
}

func postgresRestoreCtx(ctx context.Context, p *Paths, s EngineSettings, filePath, targetDB, ext string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !nameRe.MatchString(targetDB) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	host := fallbackStr(strings.TrimSpace(s.Host), "127.0.0.1")
	isRemote := !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost")
	exists := false
	if adminDB, err := openSQL("postgresql", s, fallbackStr(s.DefaultDB, "postgres")); err == nil {
		_ = adminDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", targetDB).Scan(&exists)
		if !exists && (!isRemote || !strings.EqualFold(targetDB, s.DefaultDB)) {
			_, _ = adminDB.ExecContext(ctx, "CREATE DATABASE "+quoteIdent("postgresql", targetDB)+" ENCODING 'UTF8'")
		}
		adminDB.Close()
	} else if !isRemote {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	user := fallbackStr(s.User, "postgres")
	impCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	runPgRestore := func() error {
		pgRestore := findPostgresTool(p, s, "pg_restore.exe")
		if pgRestore == "" {
			return errors.New("this file is a PostgreSQL custom/archive dump — install PostgreSQL in Versions so pg_restore.exe is available")
		}
		args := []string{
			"-h", host, "-p", fmt.Sprint(s.Port), "-U", user,
			"-d", targetDB, "--no-owner", "--no-acl",
		}
		if exists {
			args = append(args, "--clean", "--if-exists")
		}
		args = append(args, filePath)
		cmd := exec.CommandContext(impCtx, pgRestore, args...)
		hideWindow(cmd)
		env := os.Environ()
		if s.Password != "" {
			env = append(env, "PGPASSWORD="+s.Password)
		}
		if s.SSLMode != "" && s.SSLMode != "none" {
			env = append(env, "PGSSLMODE="+s.SSLMode)
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return errors.New(msg)
		}
		return nil
	}

	if isPostgresArchiveDump(filePath) {
		return runPgRestore()
	}

	if psql := findPostgresTool(p, s, "psql.exe"); psql != "" {
		cmd := exec.CommandContext(impCtx, psql,
			"-h", host, "-p", fmt.Sprint(s.Port), "-U", user,
			"-d", targetDB, "-v", "ON_ERROR_STOP=1", "-f", filePath,
		)
		hideWindow(cmd)
		env := os.Environ()
		if s.Password != "" {
			env = append(env, "PGPASSWORD="+s.Password)
		}
		if s.SSLMode != "" && s.SSLMode != "none" {
			env = append(env, "PGSSLMODE="+s.SSLMode)
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			msg := strings.TrimSpace(string(out))
			// Fallback: if psql still identifies an archive dump, hand off to pg_restore automatically
			if strings.Contains(strings.ToLower(msg), "pg_restore") {
				return runPgRestore()
			}
			return fmt.Errorf("%s", msg)
		}
		return nil
	}

	if ext == ".backup" || ext == ".dump" {
		return runPgRestore()
	}

	// Fallback: execute SQL directly
	b, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := openSQL("postgresql", s, targetDB)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, string(b))
	return err
}

func postgresBackupSQL(p *Paths, s EngineSettings, dbName, outPath string) error {
	host := fallbackStr(strings.TrimSpace(s.Host), "127.0.0.1")
	if pgDump := findPostgresTool(p, s, "pg_dump.exe"); pgDump != "" {
		user := fallbackStr(s.User, "postgres")
		args := []string{"-h", host, "-p", fmt.Sprint(s.Port), "-U", user, "-f", outPath}
		ext := strings.ToLower(filepath.Ext(outPath))
		if ext == ".backup" || ext == ".dump" {
			args = append(args, "-F", "c")
		} else {
			args = append(args, "--clean", "--if-exists", "--no-owner")
		}
		args = append(args, dbName)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, pgDump, args...)
		hideWindow(cmd)
		env := os.Environ()
		if s.Password != "" {
			env = append(env, "PGPASSWORD="+s.Password)
		}
		if s.SSLMode != "" && s.SSLMode != "none" {
			env = append(env, "PGSSLMODE="+s.SSLMode)
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		if !strings.EqualFold(host, "127.0.0.1") && ext != ".backup" && ext != ".dump" {
			// Fallback to pure-Go table SQL export when remote Cloud PG server version is newer than local pg_dump
			return postgresExportTablesPureGo(s, dbName, nil, true, true, outPath)
		}
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	if strings.ToLower(filepath.Ext(outPath)) != ".backup" && strings.ToLower(filepath.Ext(outPath)) != ".dump" {
		return postgresExportTablesPureGo(s, dbName, nil, true, true, outPath)
	}
	return errors.New("pg_dump.exe not found — install a PostgreSQL version in Versions")
}

func fallbackStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ImportCSV imports a .csv file (first row = column headers) into targetDB.targetTable.
// Creates targetDB and/or targetTable automatically if they do not exist yet.
func ImportCSV(engine string, s EngineSettings, csvPath, targetDB, targetTable string) (string, int, error) {
	csvPath = strings.TrimSpace(csvPath)
	if csvPath == "" {
		return "", 0, errors.New("no CSV file selected")
	}
	f, err := os.Open(csvPath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return "", 0, fmt.Errorf("invalid CSV: %w", err)
	}
	if len(records) < 2 {
		return "", 0, errors.New("CSV must have a header row and at least 1 data row")
	}

	targetDB = strings.TrimSpace(targetDB)
	if targetDB == "" {
		targetDB = DeriveDBNameFromFile(csvPath)
	}
	if !nameRe.MatchString(targetDB) {
		return "", 0, errors.New("database name may only contain letters, digits, _ $ -")
	}

	targetTable = strings.TrimSpace(targetTable)
	if targetTable == "" {
		targetTable = DeriveDBNameFromFile(csvPath)
	}
	if !nameRe.MatchString(targetTable) {
		return "", 0, errors.New("table name may only contain letters, digits, _ $ -")
	}

	headers := make([]string, len(records[0]))
	seen := map[string]int{}
	for i, h := range records[0] {
		h = strings.TrimPrefix(strings.TrimSpace(h), "\xEF\xBB\xBF")
		h = cleanDBNameRe.ReplaceAllString(h, "_")
		h = strings.Trim(h, "_-")
		if h == "" {
			h = fmt.Sprintf("col_%d", i+1)
		}
		if h[0] >= '0' && h[0] <= '9' {
			h = "c_" + h
		}
		seen[h]++
		if seen[h] > 1 {
			h = fmt.Sprintf("%s_%d", h, seen[h])
		}
		headers[i] = h
	}

	// Ensure database exists
	dbs, err := ListDatabases(engine, s)
	if err != nil {
		return "", 0, err
	}
	dbExists := false
	for _, d := range dbs {
		if strings.EqualFold(d, targetDB) {
			dbExists = true
			targetDB = d
			break
		}
	}
	if !dbExists {
		if err := CreateDatabase(engine, s, targetDB, "", ""); err != nil {
			return "", 0, err
		}
	}

	// Create table if it doesn't exist yet
	existingCols, err := DescribeTable(engine, s, targetDB, targetTable)
	if err != nil || len(existingCols) == 0 {
		var colDefs []CreateTableColumn
		for _, h := range headers {
			colDefs = append(colDefs, CreateTableColumn{
				Name:     h,
				Type:     "TEXT",
				Nullable: true,
			})
		}
		if err := CreateTable(engine, s, targetDB, targetTable, colDefs); err != nil {
			return "", 0, err
		}
	}

	inserted := 0
	for _, rec := range records[1:] {
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}
		rowMap := map[string]RowValue{}
		for i, col := range headers {
			if i < len(rec) {
				rowMap[col] = RowValue{Value: rec[i]}
			} else {
				rowMap[col] = RowValue{IsNull: true}
			}
		}
		if err := InsertRow(engine, s, targetDB, targetTable, rowMap); err != nil {
			return targetDB, inserted, fmt.Errorf("row %d: %w", inserted+1, err)
		}
		inserted++
	}
	return targetDB, inserted, nil
}

// unused io import guard
var _ = io.EOF

// BackupProgress reports live stage & percentage progress during database backup/export.
type BackupProgress struct {
	Percent int    `json:"percent"`
	State   string `json:"state"` // running | done | error
	Stage   string `json:"stage"`
	Detail  string `json:"detail"`
}

// ExportDatabaseSelection exports the entire database, a single table, or selected tables
// in .sql (with optional schema/data toggles) or .csv (single table) format.
func ExportDatabaseSelection(p *Paths, engine string, s EngineSettings, dbName string, tables []string, format string, includeSchema, includeData bool, outPath string) error {
	return ExportDatabaseSelectionWithProgress(p, engine, s, dbName, tables, format, includeSchema, includeData, outPath, nil)
}

// ExportDatabaseSelectionWithProgress exports with live stage & percentage progress callbacks.
func ExportDatabaseSelectionWithProgress(p *Paths, engine string, s EngineSettings, dbName string, tables []string, format string, includeSchema, includeData bool, outPath string, onProgress func(BackupProgress)) error {
	return ExportDatabaseSelectionWithContext(context.Background(), p, engine, s, dbName, tables, format, includeSchema, includeData, outPath, onProgress)
}

// ExportDatabaseSelectionWithContext exports with live stage & percentage progress callbacks and respects ctx cancellation.
func ExportDatabaseSelectionWithContext(ctx context.Context, p *Paths, engine string, s EngineSettings, dbName string, tables []string, format string, includeSchema, includeData bool, outPath string, onProgress func(BackupProgress)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lastPct := 0
	report := func(pct int, state, stage, detail string) {
		if pct > lastPct {
			lastPct = pct
		} else if state == "running" {
			pct = lastPct
		}
		if onProgress != nil {
			onProgress(BackupProgress{Percent: pct, State: state, Stage: stage, Detail: detail})
		}
	}

	dbName = strings.TrimSpace(dbName)
	outPath = strings.TrimSpace(outPath)
	if dbName == "" {
		err := errors.New("select a database to export first")
		report(0, "error", "Backup Failed", err.Error())
		return err
	}
	if outPath == "" {
		err := errors.New("no output file selected")
		report(0, "error", "Backup Failed", err.Error())
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		report(0, "error", "Backup Failed", err.Error())
		return err
	}

	ext := strings.ToLower(strings.TrimPrefix(format, "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(outPath), "."))
	}
	if ext == "zip" || strings.EqualFold(filepath.Ext(outPath), ".zip") {
		return ExportDatabaseFullZipWithContext(ctx, p, engine, s, dbName, tables, includeSchema, includeData, outPath, onProgress)
	}

	if ext == "csv" {
		if len(tables) != 1 {
			err := errors.New("CSV export requires exactly 1 selected table")
			report(0, "error", "Backup Failed", err.Error())
			return err
		}
		report(15, "running", "Exporting CSV...", fmt.Sprintf("Exporting table %q from %q...", tables[0], dbName))
		if err := exportTableCSV(engine, s, dbName, tables[0], outPath); err != nil {
			report(lastPct, "error", "Export Failed", err.Error())
			return err
		}
		var sz int64
		if st, serr := os.Stat(outPath); serr == nil {
			sz = st.Size()
		}
		report(100, "done", "Backup Complete!", fmt.Sprintf("Saved %s (%s)", filepath.Base(outPath), formatByteSize(sz)))
		return nil
	}

	if !includeSchema && !includeData {
		err := errors.New("select at least Structure (CREATE TABLE) or Data (INSERT) to export")
		report(0, "error", "Backup Failed", err.Error())
		return err
	}

	report(8, "running", "Creating backup...", fmt.Sprintf("Exporting %q (%s) to %s...", dbName, strings.ToUpper(engine), filepath.Base(outPath)))
	done := make(chan struct{})
	started := time.Now()
	go func() {
		tick := time.NewTicker(350 * time.Millisecond)
		defer tick.Stop()
		steps := 0
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				steps++
				var sz int64
				if st, serr := os.Stat(outPath); serr == nil {
					sz = st.Size()
				}
				bump := steps * 3
				if sz > 0 {
					bump += int(sz / (64 * 1024))
				}
				if bump > 80 {
					bump = 80
				}
				elapsed := int(time.Since(started).Seconds())
				report(10+bump, "running", "Creating backup...", fmt.Sprintf("Writing %s (%s · %ds)...", filepath.Base(outPath), formatByteSize(sz), elapsed))
			}
		}
	}()

	var err error
	onTbl := func(idx, total int, tblName string) {
		if total > 0 {
			pct := 10 + (idx * 80 / total)
			if pct > 90 {
				pct = 90
			}
			report(pct, "running", fmt.Sprintf("Exporting table (%d/%d)...", idx, total), fmt.Sprintf("Exporting %s from %q...", tblName, dbName))
		}
	}
	if len(tables) == 0 && includeSchema && includeData && ext != "csv" && engine != "mssql" && engine != "mysql" && engine != "mariadb" {
		err = BackupDatabase(p, engine, s, dbName, outPath)
	} else {
		switch engine {
		case "mariadb", "mysql":
			err = mysqlExportTablesSQLWithProgressCtx(ctx, p, engine, s, dbName, tables, includeSchema, includeData, outPath, len(tables) == 0, onTbl)
		case "postgresql":
			err = postgresExportTablesSQLCtx(ctx, p, s, dbName, tables, includeSchema, includeData, outPath)
		case "mssql":
			err = mssqlExportTablesSQLWithProgressCtx(ctx, s, dbName, tables, includeSchema, includeData, outPath, onTbl)
		default:
			err = fmt.Errorf("export is not supported for %s", engine)
		}
	}
	close(done)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		report(lastPct, "error", "Backup Failed", err.Error())
		return err
	}
	var finalSz int64
	if st, serr := os.Stat(outPath); serr == nil {
		finalSz = st.Size()
	}
	report(100, "done", "Backup Complete!", fmt.Sprintf("Saved %s (%s)", filepath.Base(outPath), formatByteSize(finalSz)))
	return nil
}

func exportTableCSV(engine string, s EngineSettings, dbName, tableName, outPath string) error {
	return exportTableCSVCtx(context.Background(), engine, s, dbName, tableName, outPath)
}

func exportTableCSVCtx(ctx context.Context, engine string, s EngineSettings, dbName, tableName, outPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	schema, table := splitSchemaTable(engine, dbName, tableName)
	q := "SELECT * FROM " + qualifiedTable(engine, schema, table) + ";"
	res, err := RunQueryAllCtx(ctx, engine, s, dbName, q)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	// Write UTF-8 BOM so Excel opens UTF-8 CSV cleanly
	_, _ = f.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(f)
	if err := w.Write(res.Columns); err != nil {
		return err
	}
	for _, row := range res.Rows {
		rec := make([]string, len(res.Columns))
		for i := range res.Columns {
			if i < len(row) && row[i] != nil {
				rec[i] = *row[i]
			}
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// ExportDatabaseFullZip creates a compressed .zip backup archive containing:
// 1) <dbName>.sql (full SQL dump or selected tables),
// 2) tables/<table>.csv (UTF-8 CSV export per base table when Data is enabled), and
// 3) manifest.json metadata.
func ExportDatabaseFullZip(p *Paths, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	return ExportDatabaseFullZipWithProgress(p, engine, s, dbName, tables, includeSchema, includeData, outPath, nil)
}

// ExportDatabaseFullZipWithProgress creates a full .zip backup while reporting stage & per-table percentage progress.
func ExportDatabaseFullZipWithProgress(p *Paths, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, onProgress func(BackupProgress)) error {
	return ExportDatabaseFullZipWithContext(context.Background(), p, engine, s, dbName, tables, includeSchema, includeData, outPath, onProgress)
}

// ExportDatabaseFullZipWithContext creates a full .zip backup while reporting stage & per-table percentage progress and respecting ctx cancellation.
func ExportDatabaseFullZipWithContext(ctx context.Context, p *Paths, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, onProgress func(BackupProgress)) error {
	lastPct := 0
	report := func(pct int, state, stage, detail string) {
		if pct > lastPct {
			lastPct = pct
		} else if state == "running" {
			pct = lastPct
		}
		if onProgress != nil {
			onProgress(BackupProgress{Percent: pct, State: state, Stage: stage, Detail: detail})
		}
	}
	isCancelled := func(err error) bool {
		return errors.Is(err, context.Canceled) || errors.Is(err, ErrSyncCancelled) || (ctx != nil && ctx.Err() != nil)
	}

	if ctx != nil && ctx.Err() != nil {
		report(lastPct, "cancelled", "Backup Cancelled", ErrSyncCancelled.Error())
		return ErrSyncCancelled
	}
	if !includeSchema && !includeData {
		includeSchema = true
		includeData = true
	}
	tmpDir, err := os.MkdirTemp("", "dariusdb-fullzip-*")
	if err != nil {
		report(0, "error", "Backup Failed", err.Error())
		return err
	}
	defer os.RemoveAll(tmpDir)

	report(5, "running", "Inspecting tables...", fmt.Sprintf("Listing base tables in %q (%s)...", dbName, strings.ToUpper(engine)))
	allTables, err := ListTablesCtx(ctx, engine, s, dbName)
	if err != nil {
		if isCancelled(err) {
			report(lastPct, "cancelled", "Backup Cancelled", ErrSyncCancelled.Error())
			return ErrSyncCancelled
		}
		report(lastPct, "error", "Backup Failed", err.Error())
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		report(lastPct, "cancelled", "Backup Cancelled", ErrSyncCancelled.Error())
		return ErrSyncCancelled
	}
	selected := map[string]bool{}
	for _, t := range tables {
		selected[strings.ToLower(strings.TrimSpace(t))] = true
	}
	var exportTables []string
	for _, t := range allTables {
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		if len(selected) > 0 && !selected[strings.ToLower(t.Name)] {
			continue
		}
		exportTables = append(exportTables, t.Name)
	}

	safeDB := cleanDBNameRe.ReplaceAllString(dbName, "_")
	if safeDB == "" {
		safeDB = "database"
	}
	sqlPath := filepath.Join(tmpDir, safeDB+".sql")

	report(12, "running", "Exporting SQL dump...", fmt.Sprintf("Found %d table(s) in %q — generating %s.sql...", len(exportTables), dbName, safeDB))
	sqlDone := make(chan struct{})
	sqlStart := time.Now()
	go func() {
		tick := time.NewTicker(350 * time.Millisecond)
		defer tick.Stop()
		steps := 0
		for {
			select {
			case <-sqlDone:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				steps++
				var sz int64
				if st, serr := os.Stat(sqlPath); serr == nil {
					sz = st.Size()
				}
				bump := steps * 2
				if sz > 0 {
					bump += int(sz / (64 * 1024))
				}
				if bump > 31 {
					bump = 31
				}
				elapsed := int(time.Since(sqlStart).Seconds())
				report(13+bump, "running", "Exporting SQL dump...", fmt.Sprintf("Generating %s.sql (%s · %ds)...", safeDB, formatByteSize(sz), elapsed))
			}
		}
	}()

	err = ExportDatabaseSelectionWithContext(ctx, p, engine, s, dbName, tables, "sql", includeSchema, includeData, sqlPath, func(bp BackupProgress) {
		if bp.State == "running" {
			// Map inner SQL dump 0..100 into outer 13..45%
			pct := 13 + (bp.Percent * 32 / 100)
			if pct > 45 {
				pct = 45
			}
			report(pct, "running", bp.Stage, bp.Detail)
		}
	})
	close(sqlDone)
	if err != nil {
		if isCancelled(err) {
			report(lastPct, "cancelled", "Backup Cancelled", ErrSyncCancelled.Error())
			return ErrSyncCancelled
		}
		outErr := fmt.Errorf("SQL dump failed: %w", err)
		report(lastPct, "error", "SQL Dump Failed", outErr.Error())
		return outErr
	}
	report(45, "running", "SQL dump ready", fmt.Sprintf("Exported %s.sql — preparing table CSV spreadsheets...", safeDB))

	csvFiles := map[string]string{}
	if includeData && len(exportTables) > 0 {
		tablesDir := filepath.Join(tmpDir, "tables")
		_ = os.MkdirAll(tablesDir, 0o755)
		for i, t := range exportTables {
			if ctx != nil && ctx.Err() != nil {
				report(lastPct, "cancelled", "Backup Cancelled", ErrSyncCancelled.Error())
				return ErrSyncCancelled
			}
			pct := 46 + ((i + 1) * 39 / len(exportTables))
			if pct > 85 {
				pct = 85
			}
			safeTbl := cleanDBNameRe.ReplaceAllString(t, "_")
			if safeTbl == "" {
				safeTbl = "table"
			}
			report(pct, "running", fmt.Sprintf("Exporting table CSV (%d/%d)...", i+1, len(exportTables)), fmt.Sprintf("Exporting tables/%s.csv...", safeTbl))
			csvPath := filepath.Join(tablesDir, safeTbl+".csv")
			if err := exportTableCSVCtx(ctx, engine, s, dbName, t, csvPath); err == nil {
				csvFiles["tables/"+safeTbl+".csv"] = csvPath
			} else if isCancelled(err) {
				report(lastPct, "cancelled", "Backup Cancelled", ErrSyncCancelled.Error())
				return ErrSyncCancelled
			}
		}
	}
	if ctx != nil && ctx.Err() != nil {
		report(lastPct, "cancelled", "Backup Cancelled", ErrSyncCancelled.Error())
		return ErrSyncCancelled
	}

	report(86, "running", "Packaging .zip archive...", fmt.Sprintf("Writing manifest.json and compressing %s...", filepath.Base(outPath)))
	manifest := struct {
		App           string   `json:"app"`
		Engine        string   `json:"engine"`
		Host          string   `json:"host"`
		Database      string   `json:"database"`
		ExportedAt    string   `json:"exportedAt"`
		IncludeSchema bool     `json:"includeSchema"`
		IncludeData   bool     `json:"includeData"`
		TableCount    int      `json:"tableCount"`
		Tables        []string `json:"tables"`
	}{
		App:           "Darius DB (Lite Version 0.1.6)",
		Engine:        engine,
		Host:          fallbackStr(strings.TrimSpace(s.Host), "127.0.0.1"),
		Database:      dbName,
		ExportedAt:    time.Now().Format(time.RFC3339),
		IncludeSchema: includeSchema,
		IncludeData:   includeData,
		TableCount:    len(exportTables),
		Tables:        exportTables,
	}
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if mb, err := json.MarshalIndent(manifest, "", "  "); err == nil {
		_ = os.WriteFile(manifestPath, mb, 0o644)
	}

	zf, err := os.Create(outPath)
	if err != nil {
		report(lastPct, "error", "Archive Failed", err.Error())
		return err
	}
	zw := zip.NewWriter(zf)
	addZipFile := func(arcName, srcPath string) error {
		src, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer src.Close()
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   filepath.ToSlash(arcName),
			Method: zip.Deflate,
		})
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		return err
	}

	if err := addZipFile(safeDB+".sql", sqlPath); err != nil {
		zw.Close()
		zf.Close()
		report(lastPct, "error", "Archive Failed", err.Error())
		return err
	}
	_ = addZipFile("manifest.json", manifestPath)
	zipDone := 0
	zipTotal := len(csvFiles)
	for arcName, srcPath := range csvFiles {
		_ = addZipFile(arcName, srcPath)
		zipDone++
		if zipTotal > 0 {
			pct := 88 + (zipDone * 10 / zipTotal)
			if pct > 98 {
				pct = 98
			}
			report(pct, "running", "Compressing .zip...", fmt.Sprintf("Adding %s (%d/%d)...", arcName, zipDone, zipTotal))
		}
	}
	if err := zw.Close(); err != nil {
		zf.Close()
		report(lastPct, "error", "Archive Failed", err.Error())
		return err
	}
	if err := zf.Close(); err != nil {
		report(lastPct, "error", "Archive Failed", err.Error())
		return err
	}
	var zipSz int64
	if st, serr := os.Stat(outPath); serr == nil {
		zipSz = st.Size()
	}
	report(100, "done", "Backup Complete!", fmt.Sprintf("Saved %s (%s · %d table CSVs + SQL)", filepath.Base(outPath), formatByteSize(zipSz), len(csvFiles)))
	return nil
}

func restoreDatabaseFromZip(p *Paths, engine string, s EngineSettings, zipPath, targetDB string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("invalid .zip backup: %w", err)
	}
	defer zr.Close()

	var sqlEntry *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(f.Name), ".sql") {
			if sqlEntry == nil || (!strings.Contains(f.Name, "/") && strings.Contains(sqlEntry.Name, "/")) || f.UncompressedSize64 > sqlEntry.UncompressedSize64 {
				sqlEntry = f
			}
		}
	}
	if sqlEntry == nil {
		return errors.New("the .zip backup does not contain a .sql dump file")
	}

	tmpDir, err := os.MkdirTemp("", "dariusdb-restorezip-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	rc, err := sqlEntry.Open()
	if err != nil {
		return err
	}
	extractedSQL := filepath.Join(tmpDir, filepath.Base(sqlEntry.Name))
	out, err := os.Create(extractedSQL)
	if err != nil {
		rc.Close()
		return err
	}
	_, cerr := io.Copy(out, rc)
	out.Close()
	rc.Close()
	if cerr != nil {
		return cerr
	}
	_, err = RestoreDatabase(p, engine, s, extractedSQL, targetDB)
	return err
}

func mysqlExportTablesSQL(p *Paths, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	return mysqlExportTablesSQLWithProgress(p, engine, s, dbName, tables, includeSchema, includeData, outPath, len(tables) == 0, nil)
}

func mysqlExportTablesSQLWithProgress(p *Paths, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, includeRoutines bool, onTable func(idx, total int, tblName string)) error {
	return mysqlExportTablesSQLWithProgressCtx(context.Background(), p, engine, s, dbName, tables, includeSchema, includeData, outPath, includeRoutines, onTable)
}

func mysqlExportTablesSQLWithProgressCtx(ctx context.Context, p *Paths, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, includeRoutines bool, onTable func(idx, total int, tblName string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	host := fallbackStr(strings.TrimSpace(s.Host), "127.0.0.1")
	if dump := findMySQLClientTool(p, engine, s, "mariadb-dump.exe", "mysqldump.exe"); dump != "" {
		port := s.Port
		if port <= 0 {
			port = 3306
		}
		args := []string{
			"-h", host,
			"-P", fmt.Sprint(port),
			"-u", fallbackStr(s.User, "root"),
			"--single-transaction",
			"--hex-blob",
			"--extended-insert",
			"--quick",
			"--default-character-set=utf8mb4",
			"--max-allowed-packet=256M",
		}
		args = appendMySQLClientConnArgs(dump, s, args)
		if includeRoutines && len(tables) == 0 {
			args = append(args, "--routines", "--events")
		}
		if !includeSchema {
			args = append(args, "--no-create-info")
		}
		if !includeData {
			args = append(args, "--no-data")
		}
		args = append(args, "--result-file="+outPath, dbName)
		args = append(args, tables...)
		if s.Password != "" {
			args = append([]string{"-p" + s.Password}, args...)
		}
		expCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(expCtx, dump, args...)
		cmd.WaitDelay = 2 * time.Second
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else if strings.EqualFold(host, "127.0.0.1") {
			return fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
	}

	return mysqlWriteGoDumpCtx(ctx, engine, s, dbName, tables, includeSchema, includeData, outPath, onTable)
}

func postgresExportTablesSQL(p *Paths, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	return postgresExportTablesSQLCtx(context.Background(), p, s, dbName, tables, includeSchema, includeData, outPath)
}

func postgresExportTablesSQLCtx(ctx context.Context, p *Paths, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	host := fallbackStr(strings.TrimSpace(s.Host), "127.0.0.1")
	if pgDump := findPostgresTool(p, s, "pg_dump.exe"); pgDump != "" {
		user := fallbackStr(s.User, "postgres")
		args := []string{"-h", host, "-p", fmt.Sprint(s.Port), "-U", user, "-f", outPath, "--no-owner"}
		if includeSchema && !includeData {
			args = append(args, "--schema-only")
		} else if !includeSchema && includeData {
			args = append(args, "--data-only")
		}
		if includeSchema {
			args = append(args, "--clean", "--if-exists")
		}
		for _, tbl := range tables {
			schema, table := splitSchemaTable("postgresql", dbName, tbl)
			args = append(args, "-t", quoteIdent("postgresql", schema)+"."+quoteIdent("postgresql", table))
		}
		args = append(args, dbName)

		expCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(expCtx, pgDump, args...)
		cmd.WaitDelay = 2 * time.Second
		hideWindow(cmd)
		env := os.Environ()
		if s.Password != "" {
			env = append(env, "PGPASSWORD="+s.Password)
		}
		if s.SSLMode != "" && s.SSLMode != "none" {
			env = append(env, "PGSSLMODE="+s.SSLMode)
		}
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else if strings.EqualFold(host, "127.0.0.1") {
			return fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
	}
	return postgresExportTablesPureGoCtx(ctx, s, dbName, tables, includeSchema, includeData, outPath)
}

func postgresExportTablesPureGo(s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	return postgresExportTablesPureGoCtx(context.Background(), s, dbName, tables, includeSchema, includeData, outPath)
}

func postgresExportTablesPureGoCtx(ctx context.Context, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	allTables, err := ListTables("postgresql", s, dbName)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, t := range tables {
		selected[strings.ToLower(strings.TrimSpace(t))] = true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "-- Darius DB (Lite Version 0.1.6) PostgreSQL SQL Export\n-- Database: %s\n\n", dbName)
	for _, t := range allTables {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(selected) > 0 && !selected[strings.ToLower(t.Name)] {
			continue
		}
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		schema, tbl := splitSchemaTable("postgresql", dbName, t.Name)
		fullIdent := quoteIdent("postgresql", schema) + "." + quoteIdent("postgresql", tbl)
		if includeSchema {
			cols, err := DescribeTable("postgresql", s, dbName, t.Name)
			if err == nil && len(cols) > 0 {
				fmt.Fprintf(&b, "DROP TABLE IF EXISTS %s CASCADE;\nCREATE TABLE %s (\n", fullIdent, fullIdent)
				var pks []string
				for i, c := range cols {
					nullStr := " NOT NULL"
					if c.Nullable {
						nullStr = ""
					}
					comma := ","
					if i == len(cols)-1 && len(pks) == 0 && !c.IsPrimaryKey {
						comma = ""
					}
					fmt.Fprintf(&b, "  %s %s%s%s\n", quoteIdent("postgresql", c.Name), c.DataType, nullStr, comma)
					if c.IsPrimaryKey {
						pks = append(pks, quoteIdent("postgresql", c.Name))
					}
				}
				if len(pks) > 0 {
					fmt.Fprintf(&b, "  PRIMARY KEY (%s)\n", strings.Join(pks, ", "))
				}
				b.WriteString(");\n\n")
			}
		}
		if !includeData {
			continue
		}
		res, err := RunQueryAll("postgresql", s, dbName, "SELECT * FROM "+fullIdent+";")
		if err == nil && len(res.Rows) > 0 {
			var colIdents []string
			for _, cn := range res.Columns {
				colIdents = append(colIdents, quoteIdent("postgresql", cn))
			}
			colsJoined := strings.Join(colIdents, ", ")
			for _, row := range res.Rows {
				var vals []string
				for _, cell := range row {
					if cell == nil {
						vals = append(vals, "NULL")
					} else {
						vals = append(vals, quoteStr("postgresql", *cell))
					}
				}
				fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s);\n", fullIdent, colsJoined, strings.Join(vals, ", "))
			}
			b.WriteString("\n")
		}
	}
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

func mssqlExportTablesSQL(s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	return mssqlExportTablesSQLWithProgress(s, dbName, tables, includeSchema, includeData, outPath, nil)
}

func mssqlExportTablesSQLWithProgress(s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, onTable func(idx, total int, tblName string)) error {
	return mssqlExportTablesSQLWithProgressCtx(context.Background(), s, dbName, tables, includeSchema, includeData, outPath, onTable)
}

func mssqlExportTablesSQLWithProgressCtx(ctx context.Context, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string, onTable func(idx, total int, tblName string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	allTables, err := mssqlListTables(s, dbName)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, t := range tables {
		selected[strings.ToLower(strings.TrimSpace(t))] = true
	}

	var todo []TableInfo
	for _, t := range allTables {
		if len(selected) > 0 && !selected[strings.ToLower(t.Name)] {
			continue
		}
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		todo = append(todo, t)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 128*1024)

	fmt.Fprintf(bw, "-- Darius DB (Lite Version 0.1.6) MS SQL Server SQL Export\r\n-- Database: %s\r\n-- Date: %s\r\n\r\n",
		dbName, time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(bw, "SET NOCOUNT ON;\r\nUSE %s;\r\nGO\r\n\r\n", mssqlBracket(dbName))
	_ = bw.Flush()

	for i, t := range todo {
		if err := ctx.Err(); err != nil {
			return err
		}
		if onTable != nil {
			onTable(i+1, len(todo), t.Name)
		}
		if err := mssqlWriteTableSQLCtx(ctx, bw, s, dbName, t, includeSchema, includeData); err != nil {
			return err
		}
		_ = bw.Flush()
	}
	return bw.Flush()
}
