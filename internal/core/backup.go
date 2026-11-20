//go:build windows

package core

import (
	"context"
	"encoding/csv"
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

	switch engine {
	case "mssql":
		if ext == ".bak" {
			return targetDB, mssqlRestoreBak(p, s, filePath, targetDB)
		}
		return targetDB, mssqlImportSQL(s, filePath, targetDB)

	case "mariadb", "mysql":
		if ext == ".bak" {
			return "", fmt.Errorf(".bak is a Microsoft SQL Server backup format — switch to the MS SQL Server tab to restore %s", filepath.Base(filePath))
		}
		return targetDB, mysqlImportSQL(p, engine, s, filePath, targetDB)

	case "postgresql":
		if ext == ".bak" {
			return "", fmt.Errorf(".bak is a Microsoft SQL Server backup format — switch to the MS SQL Server tab to restore %s", filepath.Base(filePath))
		}
		return targetDB, postgresRestore(p, s, filePath, targetDB, ext)

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
	sqlcmd := findSqlcmd()
	if sqlcmd == "" {
		return errors.New("sqlcmd.exe not found")
	}
	if targetDB != "" && !systemDBs[strings.ToLower(targetDB)] {
		createQ := fmt.Sprintf("IF DB_ID(N'%s') IS NULL CREATE DATABASE %s;",
			strings.ReplaceAll(targetDB, "'", "''"), mssqlBracket(targetDB))
		_, _ = mssqlRunQuery(s, "master", createQ)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqlcmd, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
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
	fmt.Fprintf(&b, "-- Darius DB (Lite Version 0.1.3) MS SQL Server SQL Dump\r\n-- Database: %s\r\n-- Date: %s\r\n\r\n",
		dbName, time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "IF DB_ID(N'%s') IS NULL CREATE DATABASE %s;\r\nGO\r\nUSE %s;\r\nGO\r\n\r\n",
		strings.ReplaceAll(dbName, "'", "''"), mssqlBracket(dbName), mssqlBracket(dbName))

	for _, t := range tables {
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		tableName := t.Name
		if idx := strings.IndexByte(tableName, '.'); idx >= 0 {
			tableName = tableName[idx+1:]
		}
		fullIdent := mssqlBracket(t.Schema) + "." + mssqlBracket(tableName)

		// Column definitions
		colQ := fmt.Sprintf(
			"SET NOCOUNT ON; SELECT COLUMN_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, IS_NULLABLE "+
				"FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = N'%s' AND TABLE_NAME = N'%s' ORDER BY ORDINAL_POSITION;",
			strings.ReplaceAll(t.Schema, "'", "''"), strings.ReplaceAll(tableName, "'", "''"),
		)
		colRes, err := mssqlRunQuery(s, dbName, colQ)
		if err == nil && len(colRes.Rows) > 0 {
			fmt.Fprintf(&b, "IF OBJECT_ID(N'%s', N'U') IS NULL\r\nCREATE TABLE %s (\r\n", fullIdent, fullIdent)
			for i, cr := range colRes.Rows {
				cName, dType, maxLen, isNull := valAt(cr, 0), valAt(cr, 1), valAt(cr, 2), valAt(cr, 3)
				typeSpec := dType
				switch strings.ToLower(dType) {
				case "varchar", "nvarchar", "char", "nchar", "varbinary":
					if maxLen == "-1" {
						typeSpec += "(MAX)"
					} else if maxLen != "" && maxLen != "NULL" {
						typeSpec += "(" + maxLen + ")"
					}
				}
				nullSpec := "NULL"
				if strings.EqualFold(isNull, "NO") {
					nullSpec = "NOT NULL"
				}
				comma := ","
				if i == len(colRes.Rows)-1 {
					comma = ""
				}
				fmt.Fprintf(&b, "  %s %s %s%s\r\n", mssqlBracket(cName), typeSpec, nullSpec, comma)
			}
			b.WriteString(");\r\nGO\r\n")
		}

		// Table data rows
		dataRes, err := mssqlRunQuery(s, dbName, "SELECT * FROM "+fullIdent+";")
		if err == nil && len(dataRes.Rows) > 0 && len(dataRes.Columns) > 0 {
			var colList []string
			for _, c := range dataRes.Columns {
				colList = append(colList, mssqlBracket(c))
			}
			colsJoined := strings.Join(colList, ", ")
			for _, row := range dataRes.Rows {
				var vals []string
				for _, cell := range row {
					if cell == nil {
						vals = append(vals, "NULL")
					} else {
						vals = append(vals, "N'"+strings.ReplaceAll(*cell, "'", "''")+"'")
					}
				}
				fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s);\r\n", fullIdent, colsJoined, strings.Join(vals, ", "))
			}
			b.WriteString("GO\r\n\r\n")
		}
	}
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

func valAt(row []*string, idx int) string {
	if idx < len(row) && row[idx] != nil {
		return *row[idx]
	}
	return ""
}

// ---------------------------------------------------------------- MariaDB / MySQL (.sql)

func mysqlImportSQL(p *Paths, engine string, s EngineSettings, sqlPath, targetDB string) error {
	if !nameRe.MatchString(targetDB) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	db, err := openSQL(engine, s, "")
	if err != nil {
		return err
	}
	_, err = db.Exec("CREATE DATABASE IF NOT EXISTS " + quoteIdent(engine, targetDB) + " CHARACTER SET utf8mb4")
	db.Close()
	if err != nil {
		return err
	}

	// Prefer native client binary if installed for large dump streaming
	if inst, ok := FindInstallation(p, engine, s.Version); ok {
		cli := filepath.Join(inst.Dir, "bin", "mariadb.exe")
		if !fileExists(cli) {
			cli = filepath.Join(inst.Dir, "bin", "mysql.exe")
		}
		if fileExists(cli) {
			f, err := os.Open(sqlPath)
			if err != nil {
				return err
			}
			defer f.Close()
			args := []string{
				"-h", "127.0.0.1",
				"-P", fmt.Sprint(s.Port),
				"-u", fallbackStr(s.User, "root"),
				"--default-character-set=utf8mb4",
				targetDB,
			}
			if s.Password != "" {
				args = append([]string{"-p" + s.Password}, args...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, cli, args...)
			hideWindow(cmd)
			cmd.Stdin = f
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return nil
		}
	}

	// Fallback: execute via Go SQL driver (MultiStatements=true)
	b, err := os.ReadFile(sqlPath)
	if err != nil {
		return err
	}
	targetConn, err := openSQL(engine, s, targetDB)
	if err != nil {
		return err
	}
	defer targetConn.Close()
	_, err = targetConn.Exec(string(b))
	return err
}

func mysqlBackupSQL(p *Paths, engine string, s EngineSettings, dbName, outPath string) error {
	if inst, ok := FindInstallation(p, engine, s.Version); ok {
		dump := filepath.Join(inst.Dir, "bin", "mariadb-dump.exe")
		if !fileExists(dump) {
			dump = filepath.Join(inst.Dir, "bin", "mysqldump.exe")
		}
		if fileExists(dump) {
			args := []string{
				"-h", "127.0.0.1",
				"-P", fmt.Sprint(s.Port),
				"-u", fallbackStr(s.User, "root"),
				"--routines",
				"--events",
				"--single-transaction",
				"--result-file=" + outPath,
				dbName,
			}
			if s.Password != "" {
				args = append([]string{"-p" + s.Password}, args...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, dump, args...)
			hideWindow(cmd)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return nil
		}
	}

	// Fallback pure-Go SQL dump
	db, err := openSQL(engine, s, dbName)
	if err != nil {
		return err
	}
	defer db.Close()

	tables, err := ListTables(engine, s, dbName)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "-- Darius DB (Lite Version 0.1.3) SQL Dump\n-- Engine: %s  Database: %s\n\nSET FOREIGN_KEY_CHECKS=0;\n\n", engine, dbName)
	for _, t := range tables {
		var tbl, createSQL string
		if err := db.QueryRow("SHOW CREATE TABLE "+quoteIdent(engine, t.Name)).Scan(&tbl, &createSQL); err == nil {
			fmt.Fprintf(&b, "DROP TABLE IF EXISTS %s;\n%s;\n\n", quoteIdent(engine, t.Name), createSQL)
		}
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		res, err := RunQuery(engine, s, dbName, "SELECT * FROM "+quoteIdent(engine, t.Name))
		if err == nil && len(res.Rows) > 0 {
			for _, row := range res.Rows {
				var vals []string
				for _, cell := range row {
					if cell == nil {
						vals = append(vals, "NULL")
					} else {
						vals = append(vals, quoteStr(engine, *cell))
					}
				}
				fmt.Fprintf(&b, "INSERT INTO %s VALUES (%s);\n", quoteIdent(engine, t.Name), strings.Join(vals, ", "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("SET FOREIGN_KEY_CHECKS=1;\n")
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

// ---------------------------------------------------------------- PostgreSQL (.sql & .backup/.dump)

func postgresRestore(p *Paths, s EngineSettings, filePath, targetDB, ext string) error {
	if !nameRe.MatchString(targetDB) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	// Create targetDB if it does not exist yet
	adminDB, err := openSQL("postgresql", s, "postgres")
	if err != nil {
		return err
	}
	var exists bool
	_ = adminDB.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", targetDB).Scan(&exists)
	if !exists {
		_, err = adminDB.Exec("CREATE DATABASE " + quoteIdent("postgresql", targetDB) + " ENCODING 'UTF8'")
	}
	adminDB.Close()
	if err != nil {
		return err
	}

	if inst, ok := FindInstallation(p, "postgresql", s.Version); ok {
		user := fallbackStr(s.User, "postgres")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		if ext == ".backup" || ext == ".dump" {
			pgRestore := filepath.Join(inst.Dir, "bin", "pg_restore.exe")
			if fileExists(pgRestore) {
				cmd := exec.CommandContext(ctx, pgRestore,
					"-h", "127.0.0.1", "-p", fmt.Sprint(s.Port), "-U", user,
					"-d", targetDB, "--clean", "--if-exists", "--no-owner", filePath,
				)
				hideWindow(cmd)
				if s.Password != "" {
					cmd.Env = append(os.Environ(), "PGPASSWORD="+s.Password)
				}
				out, err := cmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("%s", strings.TrimSpace(string(out)))
				}
				return nil
			}
		}

		psql := filepath.Join(inst.Dir, "bin", "psql.exe")
		if fileExists(psql) {
			cmd := exec.CommandContext(ctx, psql,
				"-h", "127.0.0.1", "-p", fmt.Sprint(s.Port), "-U", user,
				"-d", targetDB, "-v", "ON_ERROR_STOP=1", "-f", filePath,
			)
			hideWindow(cmd)
			if s.Password != "" {
				cmd.Env = append(os.Environ(), "PGPASSWORD="+s.Password)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return nil
		}
	}

	// Fallback: execute SQL directly
	b, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	conn, err := openSQL("postgresql", s, targetDB)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Exec(string(b))
	return err
}

func postgresBackupSQL(p *Paths, s EngineSettings, dbName, outPath string) error {
	if inst, ok := FindInstallation(p, "postgresql", s.Version); ok {
		pgDump := filepath.Join(inst.Dir, "bin", "pg_dump.exe")
		if fileExists(pgDump) {
			user := fallbackStr(s.User, "postgres")
			args := []string{"-h", "127.0.0.1", "-p", fmt.Sprint(s.Port), "-U", user, "-f", outPath}
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
			if s.Password != "" {
				cmd.Env = append(os.Environ(), "PGPASSWORD="+s.Password)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return nil
		}
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

// ExportDatabaseSelection exports the entire database, a single table, or selected tables
// in .sql (with optional schema/data toggles) or .csv (single table) format.
func ExportDatabaseSelection(p *Paths, engine string, s EngineSettings, dbName string, tables []string, format string, includeSchema, includeData bool, outPath string) error {
	dbName = strings.TrimSpace(dbName)
	outPath = strings.TrimSpace(outPath)
	if dbName == "" {
		return errors.New("select a database to export first")
	}
	if outPath == "" {
		return errors.New("no output file selected")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}

	ext := strings.ToLower(strings.TrimPrefix(format, "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(outPath), "."))
	}
	if ext == "csv" {
		if len(tables) != 1 {
			return errors.New("CSV export requires exactly 1 selected table")
		}
		return exportTableCSV(engine, s, dbName, tables[0], outPath)
	}

	if !includeSchema && !includeData {
		return errors.New("select at least Structure (CREATE TABLE) or Data (INSERT) to export")
	}

	if len(tables) == 0 && includeSchema && includeData && ext != "csv" {
		return BackupDatabase(p, engine, s, dbName, outPath)
	}

	switch engine {
	case "mariadb", "mysql":
		return mysqlExportTablesSQL(p, engine, s, dbName, tables, includeSchema, includeData, outPath)
	case "postgresql":
		return postgresExportTablesSQL(p, s, dbName, tables, includeSchema, includeData, outPath)
	case "mssql":
		return mssqlExportTablesSQL(s, dbName, tables, includeSchema, includeData, outPath)
	default:
		return fmt.Errorf("export is not supported for %s", engine)
	}
}

func exportTableCSV(engine string, s EngineSettings, dbName, tableName, outPath string) error {
	schema, table := splitSchemaTable(engine, dbName, tableName)
	q := "SELECT * FROM " + qualifiedTable(engine, schema, table) + ";"
	res, err := RunQueryAll(engine, s, dbName, q)
	if err != nil {
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

func mysqlExportTablesSQL(p *Paths, engine string, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	if inst, ok := FindInstallation(p, engine, s.Version); ok {
		dump := filepath.Join(inst.Dir, "bin", "mariadb-dump.exe")
		if !fileExists(dump) {
			dump = filepath.Join(inst.Dir, "bin", "mysqldump.exe")
		}
		if fileExists(dump) {
			args := []string{
				"-h", "127.0.0.1",
				"-P", fmt.Sprint(s.Port),
				"-u", fallbackStr(s.User, "root"),
				"--single-transaction",
			}
			if len(tables) == 0 {
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
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, dump, args...)
			hideWindow(cmd)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return nil
		}
	}

	// Fallback pure-Go selective SQL dump
	db, err := openSQL(engine, s, dbName)
	if err != nil {
		return err
	}
	defer db.Close()

	allTables, err := ListTables(engine, s, dbName)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, t := range tables {
		selected[strings.ToLower(strings.TrimSpace(t))] = true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "-- Darius DB (Lite Version 0.1.3) SQL Export\n-- Engine: %s  Database: %s\n\nSET FOREIGN_KEY_CHECKS=0;\n\n", engine, dbName)
	for _, t := range allTables {
		if len(selected) > 0 && !selected[strings.ToLower(t.Name)] {
			continue
		}
		if includeSchema {
			var tbl, createSQL string
			if err := db.QueryRow("SHOW CREATE TABLE "+quoteIdent(engine, t.Name)).Scan(&tbl, &createSQL); err == nil {
				fmt.Fprintf(&b, "DROP TABLE IF EXISTS %s;\n%s;\n\n", quoteIdent(engine, t.Name), createSQL)
			}
		}
		if !includeData || strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		res, err := RunQueryAll(engine, s, dbName, "SELECT * FROM "+quoteIdent(engine, t.Name))
		if err == nil && len(res.Rows) > 0 {
			for _, row := range res.Rows {
				var vals []string
				for _, cell := range row {
					if cell == nil {
						vals = append(vals, "NULL")
					} else {
						vals = append(vals, quoteStr(engine, *cell))
					}
				}
				fmt.Fprintf(&b, "INSERT INTO %s VALUES (%s);\n", quoteIdent(engine, t.Name), strings.Join(vals, ", "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("SET FOREIGN_KEY_CHECKS=1;\n")
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

func postgresExportTablesSQL(p *Paths, s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	if inst, ok := FindInstallation(p, "postgresql", s.Version); ok {
		pgDump := filepath.Join(inst.Dir, "bin", "pg_dump.exe")
		if fileExists(pgDump) {
			user := fallbackStr(s.User, "postgres")
			args := []string{"-h", "127.0.0.1", "-p", fmt.Sprint(s.Port), "-U", user, "-f", outPath, "--no-owner"}
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

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, pgDump, args...)
			hideWindow(cmd)
			if s.Password != "" {
				cmd.Env = append(os.Environ(), "PGPASSWORD="+s.Password)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	return errors.New("pg_dump.exe not found — install a PostgreSQL version in Versions")
}

func mssqlExportTablesSQL(s EngineSettings, dbName string, tables []string, includeSchema, includeData bool, outPath string) error {
	allTables, err := mssqlListTables(s, dbName)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, t := range tables {
		selected[strings.ToLower(strings.TrimSpace(t))] = true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "-- Darius DB (Lite Version 0.1.3) MS SQL Server SQL Export\r\n-- Database: %s\r\n-- Date: %s\r\n\r\n",
		dbName, time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "USE %s;\r\nGO\r\n\r\n", mssqlBracket(dbName))

	for _, t := range allTables {
		if len(selected) > 0 && !selected[strings.ToLower(t.Name)] {
			continue
		}
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		tableName := t.Name
		if idx := strings.IndexByte(tableName, '.'); idx >= 0 {
			tableName = tableName[idx+1:]
		}
		fullIdent := mssqlBracket(t.Schema) + "." + mssqlBracket(tableName)

		if includeSchema {
			colQ := fmt.Sprintf(
				"SET NOCOUNT ON; SELECT COLUMN_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, IS_NULLABLE "+
					"FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = N'%s' AND TABLE_NAME = N'%s' ORDER BY ORDINAL_POSITION;",
				strings.ReplaceAll(t.Schema, "'", "''"), strings.ReplaceAll(tableName, "'", "''"),
			)
			colRes, err := mssqlRunQuery(s, dbName, colQ)
			if err == nil && len(colRes.Rows) > 0 {
				fmt.Fprintf(&b, "IF OBJECT_ID(N'%s', N'U') IS NULL\r\nCREATE TABLE %s (\r\n", fullIdent, fullIdent)
				for i, cr := range colRes.Rows {
					cName, dType, maxLen, isNull := valAt(cr, 0), valAt(cr, 1), valAt(cr, 2), valAt(cr, 3)
					typeSpec := dType
					switch strings.ToLower(dType) {
					case "varchar", "nvarchar", "char", "nchar", "varbinary":
						if maxLen == "-1" {
							typeSpec += "(MAX)"
						} else if maxLen != "" && maxLen != "NULL" {
							typeSpec += "(" + maxLen + ")"
						}
					}
					nullSpec := "NULL"
					if strings.EqualFold(isNull, "NO") {
						nullSpec = "NOT NULL"
					}
					comma := ","
					if i == len(colRes.Rows)-1 {
						comma = ""
					}
					fmt.Fprintf(&b, "  %s %s %s%s\r\n", mssqlBracket(cName), typeSpec, nullSpec, comma)
				}
				b.WriteString(");\r\nGO\r\n")
			}
		}

		if includeData {
			dataRes, err := mssqlRunQueryWithLimit(s, dbName, "SELECT * FROM "+fullIdent+";", maxShowRows)
			if err == nil && len(dataRes.Rows) > 0 && len(dataRes.Columns) > 0 {
				var colList []string
				for _, c := range dataRes.Columns {
					colList = append(colList, mssqlBracket(c))
				}
				colsJoined := strings.Join(colList, ", ")
				for _, row := range dataRes.Rows {
					var vals []string
					for _, cell := range row {
						if cell == nil {
							vals = append(vals, "NULL")
						} else {
							vals = append(vals, "N'"+strings.ReplaceAll(*cell, "'", "''")+"'")
						}
					}
					fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s);\r\n", fullIdent, colsJoined, strings.Join(vals, ", "))
				}
				b.WriteString("GO\r\n\r\n")
			}
		}
	}
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}
