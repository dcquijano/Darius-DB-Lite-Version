package core

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

const (
	maxRows     = 1000
	maxShowRows = 250000
	maxCellLen  = 64 * 1024
)

func sqlDriverAndDSN(engine string, s EngineSettings, dbName string) (driverName, dsn string, err error) {
	host := fallbackStr(strings.TrimSpace(s.Host), "127.0.0.1")
	isRemote := !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost")
	ssl := strings.ToLower(strings.TrimSpace(s.SSLMode))

	switch engine {
	case "mariadb", "mysql":
		cfg := mysql.NewConfig()
		cfg.User = s.User
		cfg.Passwd = s.Password
		cfg.Net = "tcp"
		port := s.Port
		if port <= 0 {
			port = 3306
		}
		cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
		if dbName == "" && isRemote && s.DefaultDB != "" {
			dbName = s.DefaultDB
		}
		cfg.DBName = dbName
		cfg.Timeout = 8 * time.Second
		cfg.ReadTimeout = 120 * time.Second
		cfg.MultiStatements = true
		cfg.AllowNativePasswords = true
		cfg.AllowCleartextPasswords = true
		switch ssl {
		case "require", "true", "verify-full":
			cfg.TLSConfig = "true"
		case "skip-verify":
			cfg.TLSConfig = "skip-verify"
		case "disable", "none", "false":
			cfg.TLSConfig = "false"
		default:
			cfg.TLSConfig = "preferred"
		}
		cfg.Params = map[string]string{"charset": "utf8mb4"}
		return "mysql", cfg.FormatDSN(), nil
	case "postgresql":
		if dbName == "" {
			if s.DefaultDB != "" {
				dbName = s.DefaultDB
			} else {
				dbName = "postgres"
			}
		}
		port := s.Port
		if port <= 0 {
			port = 5432
		}
		pgSSL := "disable"
		if isRemote {
			pgSSL = "require"
		}
		switch ssl {
		case "require", "skip-verify":
			pgSSL = "require"
		case "verify-full":
			pgSSL = "verify-full"
		case "verify-ca":
			pgSSL = "verify-ca"
		case "preferred", "prefer":
			pgSSL = "prefer"
		case "disable", "none":
			pgSSL = "disable"
		}
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(s.User, s.Password),
			Host:     net.JoinHostPort(host, strconv.Itoa(port)),
			Path:     "/" + dbName,
			RawQuery: fmt.Sprintf("sslmode=%s&connect_timeout=8", pgSSL),
		}
		return "postgres", u.String(), nil
	default:
		return "", "", fmt.Errorf("%s is not a SQL engine", engine)
	}
}

func openSQL(engine string, s EngineSettings, dbName string) (*sql.DB, error) {
	driverName, dsn, err := sqlDriverAndDSN(engine, s, dbName)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot connect to %s on port %d: %v", engine, s.Port, err)
	}
	return db, nil
}

type sqlPoolEntry struct {
	engine string
	dbName string
	db     *sql.DB
}

var (
	sqlPoolMu sync.Mutex
	sqlPools  = map[string]sqlPoolEntry{}
)

// pooledSQL returns a reusable *sql.DB connection pool for interactive browser/CRUD queries.
// Callers must NOT call db.Close() on the returned pool.
func pooledSQL(engine string, s EngineSettings, dbName string) (*sql.DB, error) {
	driverName, dsn, err := sqlDriverAndDSN(engine, s, dbName)
	if err != nil {
		return nil, err
	}
	key := engine + "|" + dsn

	sqlPoolMu.Lock()
	if entry, ok := sqlPools[key]; ok && entry.db != nil {
		sqlPoolMu.Unlock()
		return entry.db, nil
	}
	sqlPoolMu.Unlock()

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(2 * time.Minute)
	db.SetConnMaxLifetime(10 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot connect to %s on port %d: %v", engine, s.Port, err)
	}

	sqlPoolMu.Lock()
	if prev, ok := sqlPools[key]; ok && prev.db != nil {
		sqlPoolMu.Unlock()
		_ = db.Close()
		return prev.db, nil
	}
	sqlPools[key] = sqlPoolEntry{engine: engine, dbName: dbName, db: db}
	sqlPoolMu.Unlock()
	return db, nil
}

// CloseSQLPools closes cached SQL connection pools for the given engine (and optional database name).
func CloseSQLPools(engine, dbName string) {
	if engine == "" || engine == "mssql" {
		InvalidateMSSQLTargetCache()
	}
	var toClose []*sql.DB
	sqlPoolMu.Lock()
	for k, entry := range sqlPools {
		if engine != "" && entry.engine != engine {
			continue
		}
		if dbName != "" && !strings.EqualFold(entry.dbName, dbName) {
			continue
		}
		if entry.db != nil {
			toClose = append(toClose, entry.db)
		}
		delete(sqlPools, k)
	}
	sqlPoolMu.Unlock()
	for _, db := range toClose {
		_ = db.Close()
	}
}

type QueryResult struct {
	Columns    []string    `json:"columns"`
	Rows       [][]*string `json:"rows"`
	IsQuery    bool        `json:"isQuery"`
	Affected   int64       `json:"affected"`
	Truncated  bool        `json:"truncated"`
	DurationMs float64     `json:"durationMs"`
}

type TableInfo struct {
	Name      string `json:"name"`
	Schema    string `json:"schema"`
	Kind      string `json:"kind"`
	Select    string `json:"select"` // ready-to-run preview query
	SelectAll string `json:"selectAll"`
}

type ColumnInfo struct {
	Name            string  `json:"name"`
	DataType        string  `json:"dataType"`
	Nullable        bool    `json:"nullable"`
	DefaultVal      *string `json:"defaultVal,omitempty"`
	IsPrimaryKey    bool    `json:"isPrimaryKey"`
	IsAutoIncrement bool    `json:"isAutoIncrement"`
}

type RowValue struct {
	Value  string `json:"value"`
	IsNull bool   `json:"isNull"`
}

type CreateTableColumn struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Nullable      bool   `json:"nullable"`
	PrimaryKey    bool   `json:"primaryKey"`
	AutoIncrement bool   `json:"autoIncrement"`
}

func isMy(engine string) bool { return engine == "mariadb" || engine == "mysql" }

func quoteIdent(engine, s string) string {
	if isMy(engine) {
		return "`" + strings.ReplaceAll(s, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteStr(engine, s string) string {
	if isMy(engine) {
		return "'" + sqlEscape(s) + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

var mysqlStrReplacer = strings.NewReplacer(
	`\`, `\\`,
	`'`, `''`,
	"\x00", `\0`,
	"\n", `\n`,
	"\r", `\r`,
	"\x1a", `\Z`,
)

// sqlEscape escapes a MySQL string literal body safely (including NUL, CR, LF, and Ctrl+Z).
func sqlEscape(s string) string {
	return mysqlStrReplacer.Replace(s)
}

func ListDatabases(engine string, s EngineSettings) ([]string, error) {
	if engine == "mssql" {
		return mssqlListDatabases(s)
	}
	db, err := pooledSQL(engine, s, s.DefaultDB)
	if err != nil {
		return nil, err
	}
	q := "SHOW DATABASES"
	if engine == "postgresql" {
		q = "SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY 1"
	}
	rows, err := db.Query(q)
	if err != nil {
		if s.DefaultDB != "" {
			return []string{s.DefaultDB}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	seenDefault := false
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if s.DefaultDB != "" && strings.EqualFold(n, s.DefaultDB) {
			seenDefault = true
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		if s.DefaultDB != "" {
			return []string{s.DefaultDB}, nil
		}
		return nil, err
	}
	if s.DefaultDB != "" && !seenDefault {
		out = append([]string{s.DefaultDB}, out...)
	}
	return out, nil
}

func ListTables(engine string, s EngineSettings, dbName string) ([]TableInfo, error) {
	if engine == "mssql" {
		return mssqlListTables(s, dbName)
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	if isMy(engine) {
		rows, err = db.Query("SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME", dbName)
	} else {
		rows, err = db.Query(`SELECT table_schema, table_name, table_type FROM information_schema.tables
			WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema <> 'public', 1, 2`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TableInfo{}
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Schema, &t.Name, &t.Kind); err != nil {
			return nil, err
		}
		from := quoteIdent(engine, t.Name)
		if engine == "postgresql" {
			from = quoteIdent(engine, t.Schema) + "." + from
			if t.Schema != "public" {
				t.Name = t.Schema + "." + t.Name
			}
		}
		t.Select = "SELECT * FROM " + from + " LIMIT 100;"
		t.SelectAll = "SELECT * FROM " + from + ";"
		out = append(out, t)
	}
	return out, rows.Err()
}

var queryWords = map[string]bool{
	"SELECT": true, "SHOW": true, "DESCRIBE": true, "DESC": true, "EXPLAIN": true, "WITH": true,
	"VALUES": true, "TABLE": true, "CALL": true, "PRAGMA": true, "HELP": true, "CHECK": true,
	"ANALYZE": true, "OPTIMIZE": true, "REPAIR": true, "CHECKSUM": true,
}

var leadingComments = regexp.MustCompile(`(?s)^(\s+|--[^\n]*\n?|#[^\n]*\n?|/\*.*?\*/)+`)

func returnsRows(q string) bool {
	q = leadingComments.ReplaceAllString(q, "")
	q = strings.TrimLeft(q, "( \t\r\n")
	end := 0
	for end < len(q) && (q[end] >= 'a' && q[end] <= 'z' || q[end] >= 'A' && q[end] <= 'Z') {
		end++
	}
	if queryWords[strings.ToUpper(q[:end])] {
		return true
	}
	return strings.Contains(strings.ToUpper(q), "RETURNING")
}

func RunQuery(engine string, s EngineSettings, dbName, q string) (*QueryResult, error) {
	return RunQueryWithLimit(engine, s, dbName, q, maxRows)
}

func RunQueryAll(engine string, s EngineSettings, dbName, q string) (*QueryResult, error) {
	return RunQueryWithLimit(engine, s, dbName, q, maxShowRows)
}

func RunQueryWithLimit(engine string, s EngineSettings, dbName, q string, limit int) (*QueryResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("query is empty")
	}
	if limit <= 0 {
		limit = maxShowRows
	}
	if engine == "mssql" {
		return mssqlRunQueryWithLimit(s, dbName, q, limit)
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	capRows := 128
	if limit < capRows {
		capRows = limit
	}
	res := &QueryResult{Columns: []string{}, Rows: make([][]*string, 0, capRows)}

	if !returnsRows(q) {
		r, err := db.Exec(q)
		if err != nil {
			return nil, err
		}
		res.Affected, _ = r.RowsAffected()
		res.DurationMs = float64(time.Since(start).Microseconds()) / 1000
		return res, nil
	}

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res.IsQuery = true
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res.Columns = cols
	raw := make([]sql.RawBytes, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if len(res.Rows) >= limit {
			res.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]*string, len(cols))
		for i, b := range raw {
			if b != nil {
				v := string(b)
				if len(v) > maxCellLen {
					v = v[:maxCellLen] + "…"
				}
				row[i] = &v
			}
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.DurationMs = float64(time.Since(start).Microseconds()) / 1000
	return res, nil
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_$\-]{0,62}$`)

var systemDBs = map[string]bool{
	"mysql": true, "information_schema": true, "performance_schema": true, "sys": true,
	"postgres": true, "template0": true, "template1": true,
	"master": true, "tempdb": true, "model": true, "msdb": true,
}

// CreateDatabase creates a database, and optionally a user with full access to it.
func CreateDatabase(engine string, s EngineSettings, name, user, password string) error {
	if engine == "mssql" {
		return mssqlCreateDatabase(s, name, user, password)
	}
	if !nameRe.MatchString(name) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	if user != "" && !nameRe.MatchString(user) {
		return errors.New("user name may only contain letters, digits, _ $ -")
	}
	db, err := pooledSQL(engine, s, "")
	if err != nil {
		return err
	}

	if isMy(engine) {
		if _, err := db.Exec("CREATE DATABASE " + quoteIdent(engine, name) + " CHARACTER SET utf8mb4"); err != nil {
			return err
		}
		if user == "" {
			return nil
		}
		for _, host := range []string{"localhost", "%"} {
			acct := quoteStr(engine, user) + "@" + quoteStr(engine, host)
			stmts := []string{
				"CREATE USER IF NOT EXISTS " + acct + " IDENTIFIED BY " + quoteStr(engine, password),
				"GRANT ALL PRIVILEGES ON " + quoteIdent(engine, name) + ".* TO " + acct,
			}
			for _, st := range stmts {
				if _, err := db.Exec(st); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// PostgreSQL
	owner := ""
	if user != "" {
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", user).Scan(&exists); err != nil {
			return err
		}
		verb := "CREATE ROLE "
		if exists {
			verb = "ALTER ROLE "
		}
		if _, err := db.Exec(verb + quoteIdent(engine, user) + " LOGIN PASSWORD " + quoteStr(engine, password)); err != nil {
			return err
		}
		owner = " OWNER " + quoteIdent(engine, user)
	}
	_, err = db.Exec("CREATE DATABASE " + quoteIdent(engine, name) + owner + " ENCODING 'UTF8'")
	return err
}

func DropDatabase(engine string, s EngineSettings, name string) error {
	if engine == "mssql" {
		return mssqlDropDatabase(s, name)
	}
	if systemDBs[strings.ToLower(name)] {
		return fmt.Errorf("%q is a system database and cannot be dropped here", name)
	}
	CloseSQLPools(engine, name)
	db, err := pooledSQL(engine, s, "")
	if err != nil {
		return err
	}
	if engine == "postgresql" {
		if _, err := db.Exec("DROP DATABASE " + quoteIdent(engine, name) + " WITH (FORCE)"); err == nil {
			return nil
		}
	}
	_, err = db.Exec("DROP DATABASE " + quoteIdent(engine, name))
	return err
}

// ---------------------------------------------------------------- Table & Row CRUD

func splitSchemaTable(engine, dbName, tableName string) (string, string) {
	tableName = strings.TrimSpace(tableName)
	if idx := strings.IndexByte(tableName, '.'); idx >= 0 {
		return strings.TrimSpace(tableName[:idx]), strings.TrimSpace(tableName[idx+1:])
	}
	switch engine {
	case "postgresql":
		return "public", tableName
	case "mssql":
		return "dbo", tableName
	default:
		return dbName, tableName
	}
}

func qualifiedTable(engine, schema, table string) string {
	switch engine {
	case "mssql":
		return mssqlBracket(schema) + "." + mssqlBracket(table)
	case "postgresql":
		return quoteIdent(engine, schema) + "." + quoteIdent(engine, table)
	default:
		return quoteIdent(engine, table)
	}
}

// DescribeTable returns ordered column metadata (types, nullability, defaults, PK, auto-increment).
func DescribeTable(engine string, s EngineSettings, dbName, tableName string) ([]ColumnInfo, error) {
	schema, table := splitSchemaTable(engine, dbName, tableName)
	if table == "" {
		return nil, errors.New("table name is required")
	}
	if engine == "mssql" {
		return mssqlDescribeTable(s, dbName, schema, table)
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return nil, err
	}

	var rows *sql.Rows
	if isMy(engine) {
		rows, err = db.Query(`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY, EXTRA
			FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
			ORDER BY ORDINAL_POSITION`, schema, table)
	} else {
		rows, err = db.Query(`SELECT a.attname,
			format_type(a.atttypid, a.atttypmod),
			CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END,
			pg_get_expr(d.adbin, d.adrelid),
			CASE WHEN EXISTS (
				SELECT 1 FROM pg_index i
				WHERE i.indrelid = a.attrelid
				  AND i.indisprimary
				  AND a.attnum = ANY(i.indkey)
			) THEN 'PRI' ELSE '' END,
			CASE WHEN a.attidentity IN ('a', 'd') OR COALESCE(pg_get_expr(d.adbin, d.adrelid), '') LIKE 'nextval(%' THEN 'auto_increment' ELSE '' END
		FROM pg_attribute a
		JOIN pg_class cl ON cl.oid = a.attrelid
		JOIN pg_namespace ns ON ns.oid = cl.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE ns.nspname = $1 AND cl.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, schema, table)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := []ColumnInfo{}
	for rows.Next() {
		var name, dataType, isNull, colKey, extra string
		var def sql.NullString
		if err := rows.Scan(&name, &dataType, &isNull, &def, &colKey, &extra); err != nil {
			return nil, err
		}
		var defPtr *string
		if def.Valid {
			v := def.String
			defPtr = &v
		}
		cols = append(cols, ColumnInfo{
			Name:            name,
			DataType:        dataType,
			Nullable:        strings.EqualFold(isNull, "YES"),
			DefaultVal:      defPtr,
			IsPrimaryKey:    strings.EqualFold(colKey, "PRI"),
			IsAutoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment"),
		})
	}
	return cols, rows.Err()
}

func ph(engine string, idx int) string {
	if engine == "postgresql" {
		return fmt.Sprintf("$%d", idx)
	}
	return "?"
}

func sortedKeys(m map[string]RowValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildWhereClause(engine string, keys map[string]RowValue, startIdx int) (string, []interface{}) {
	colNames := sortedKeys(keys)
	var parts []string
	var args []interface{}
	idx := startIdx
	for _, col := range colNames {
		rv := keys[col]
		if rv.IsNull {
			parts = append(parts, quoteIdent(engine, col)+" IS NULL")
		} else {
			parts = append(parts, quoteIdent(engine, col)+" = "+ph(engine, idx))
			args = append(args, rv.Value)
			idx++
		}
	}
	return strings.Join(parts, " AND "), args
}

// InsertRow inserts a new row into tableName using parameterized queries.
func InsertRow(engine string, s EngineSettings, dbName, tableName string, values map[string]RowValue) error {
	schema, table := splitSchemaTable(engine, dbName, tableName)
	if table == "" {
		return errors.New("table name is required")
	}
	if len(values) == 0 {
		return errors.New("no column values provided")
	}
	if engine == "mssql" {
		return mssqlInsertRow(s, dbName, schema, table, values)
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return err
	}

	colNames := sortedKeys(values)
	var cols []string
	var placeholders []string
	var args []interface{}
	idx := 1
	for _, c := range colNames {
		cols = append(cols, quoteIdent(engine, c))
		rv := values[c]
		if rv.IsNull {
			placeholders = append(placeholders, "NULL")
		} else {
			placeholders = append(placeholders, ph(engine, idx))
			args = append(args, rv.Value)
			idx++
		}
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		qualifiedTable(engine, schema, table), strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	_, err = db.Exec(q, args...)
	return err
}

// UpdateRow updates a matching row in tableName.
func UpdateRow(engine string, s EngineSettings, dbName, tableName string, keys, updates map[string]RowValue, hasPK bool) error {
	schema, table := splitSchemaTable(engine, dbName, tableName)
	if table == "" {
		return errors.New("table name is required")
	}
	if len(updates) == 0 {
		return errors.New("no changes to save")
	}
	if len(keys) == 0 {
		return errors.New("cannot identify target row (no key values)")
	}
	if engine == "mssql" {
		return mssqlUpdateRow(s, dbName, schema, table, keys, updates, hasPK)
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return err
	}

	updNames := sortedKeys(updates)
	var setParts []string
	var args []interface{}
	idx := 1
	for _, c := range updNames {
		rv := updates[c]
		if rv.IsNull {
			setParts = append(setParts, quoteIdent(engine, c)+" = NULL")
		} else {
			setParts = append(setParts, quoteIdent(engine, c)+" = "+ph(engine, idx))
			args = append(args, rv.Value)
			idx++
		}
	}

	whereSQL, whereArgs := buildWhereClause(engine, keys, idx)
	args = append(args, whereArgs...)
	qt := qualifiedTable(engine, schema, table)

	var q string
	if engine == "postgresql" && !hasPK {
		q = fmt.Sprintf("UPDATE %s SET %s WHERE ctid = (SELECT ctid FROM %s WHERE %s LIMIT 1)",
			qt, strings.Join(setParts, ", "), qt, whereSQL)
	} else {
		q = fmt.Sprintf("UPDATE %s SET %s WHERE %s", qt, strings.Join(setParts, ", "), whereSQL)
		if isMy(engine) && !hasPK {
			q += " LIMIT 1"
		}
	}
	res, err := db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return errors.New("no matching row was updated (the row may have changed or been deleted)")
	}
	return nil
}

// DeleteRow deletes a matching row from tableName.
func DeleteRow(engine string, s EngineSettings, dbName, tableName string, keys map[string]RowValue, hasPK bool) error {
	schema, table := splitSchemaTable(engine, dbName, tableName)
	if table == "" {
		return errors.New("table name is required")
	}
	if len(keys) == 0 {
		return errors.New("cannot identify target row (no key values)")
	}
	if engine == "mssql" {
		return mssqlDeleteRow(s, dbName, schema, table, keys, hasPK)
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return err
	}

	whereSQL, args := buildWhereClause(engine, keys, 1)
	qt := qualifiedTable(engine, schema, table)
	var q string
	if engine == "postgresql" && !hasPK {
		q = fmt.Sprintf("DELETE FROM %s WHERE ctid = (SELECT ctid FROM %s WHERE %s LIMIT 1)", qt, qt, whereSQL)
	} else {
		q = fmt.Sprintf("DELETE FROM %s WHERE %s", qt, whereSQL)
		if isMy(engine) && !hasPK {
			q += " LIMIT 1"
		}
	}
	res, err := db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return errors.New("no matching row was deleted")
	}
	return nil
}

var colTypeSafeRe = regexp.MustCompile(`^[A-Za-z0-9_ ()\,]+$`)

// CreateTable creates a new table in dbName from column definitions.
func CreateTable(engine string, s EngineSettings, dbName, tableName string, cols []CreateTableColumn) error {
	if !nameRe.MatchString(tableName) {
		return errors.New("table name may only contain letters, digits, _ $ -")
	}
	if len(cols) == 0 {
		return errors.New("add at least one column")
	}
	var defs []string
	var pks []string
	for _, c := range cols {
		colName := strings.TrimSpace(c.Name)
		if !nameRe.MatchString(colName) {
			return fmt.Errorf("invalid column name %q", c.Name)
		}
		typ := strings.TrimSpace(c.Type)
		if typ == "" {
			typ = "VARCHAR(255)"
		}
		if !colTypeSafeRe.MatchString(typ) {
			return fmt.Errorf("invalid column type %q", typ)
		}

		ident := quoteIdent(engine, colName)
		if engine == "mssql" {
			ident = mssqlBracket(colName)
		}

		var def string
		if c.AutoIncrement {
			switch engine {
			case "postgresql":
				if strings.Contains(strings.ToUpper(typ), "BIG") {
					def = ident + " BIGSERIAL"
				} else {
					def = ident + " SERIAL"
				}
			case "mssql":
				base := typ
				if strings.Contains(strings.ToUpper(base), "VARCHAR") {
					base = "INT"
				}
				def = ident + " " + base + " IDENTITY(1,1) NOT NULL"
			default:
				def = ident + " " + typ + " NOT NULL AUTO_INCREMENT"
			}
		} else {
			if engine == "mssql" && strings.EqualFold(typ, "BOOLEAN") {
				typ = "BIT"
			} else if engine == "mssql" && strings.EqualFold(typ, "TEXT") {
				typ = "NVARCHAR(MAX)"
			}
			nullSQL := " NOT NULL"
			if c.Nullable {
				nullSQL = " NULL"
			}
			def = ident + " " + typ + nullSQL
		}
		defs = append(defs, def)
		if c.PrimaryKey {
			pks = append(pks, ident)
		}
	}
	if len(pks) > 0 {
		defs = append(defs, "PRIMARY KEY ("+strings.Join(pks, ", ")+")")
	}

	schema, table := splitSchemaTable(engine, dbName, tableName)
	q := fmt.Sprintf("CREATE TABLE %s (\n  %s\n)", qualifiedTable(engine, schema, table), strings.Join(defs, ",\n  "))

	if engine == "mssql" {
		_, err := mssqlRunQuery(s, dbName, q+";")
		return err
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return err
	}
	_, err = db.Exec(q)
	return err
}

// DropTable drops a table from dbName.
func DropTable(engine string, s EngineSettings, dbName, tableName string) error {
	schema, table := splitSchemaTable(engine, dbName, tableName)
	if table == "" {
		return errors.New("table name is required")
	}
	q := "DROP TABLE " + qualifiedTable(engine, schema, table)
	if engine == "mssql" {
		_, err := mssqlRunQuery(s, dbName, q+";")
		return err
	}
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return err
	}
	_, err = db.Exec(q)
	return err
}

// ---------------------------------------------------------------- Redis browser

type RedisDB struct {
	Index int `json:"index"`
	Keys  int `json:"keys"`
}

type RedisKey struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type RedisScanResult struct {
	Cursor string     `json:"cursor"`
	Keys   []RedisKey `json:"keys"`
}

type RedisValue struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	TTL   int64  `json:"ttl"`
	Size  int64  `json:"size"`
	Value string `json:"value"`
}

func RedisDatabases(s EngineSettings) ([]RedisDB, error) {
	c, err := dialRedis(s, 0)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	count := 16
	if v, err := c.Do("CONFIG", "GET", "databases"); err == nil {
		if arr, ok := v.([]interface{}); ok && len(arr) == 2 {
			if n, err := strconv.Atoi(fmt.Sprint(arr[1])); err == nil && n > 0 {
				count = n
			}
		}
	}
	keys := map[int]int{}
	if v, err := c.Do("INFO", "keyspace"); err == nil {
		for _, line := range strings.Split(fmt.Sprint(v), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "db") {
				continue
			}
			parts := strings.SplitN(line[2:], ":", 2)
			idx, _ := strconv.Atoi(parts[0])
			if len(parts) == 2 {
				for _, kv := range strings.Split(parts[1], ",") {
					if strings.HasPrefix(kv, "keys=") {
						keys[idx], _ = strconv.Atoi(kv[5:])
					}
				}
			}
		}
	}
	out := make([]RedisDB, count)
	for i := range out {
		out[i] = RedisDB{Index: i, Keys: keys[i]}
	}
	return out, nil
}

func RedisScan(s EngineSettings, db int, pattern, cursor string) (*RedisScanResult, error) {
	if pattern == "" {
		pattern = "*"
	}
	if cursor == "" {
		cursor = "0"
	}
	c, err := dialRedis(s, db)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	v, err := c.Do("SCAN", cursor, "MATCH", pattern, "COUNT", "500")
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]interface{})
	if !ok || len(arr) != 2 {
		return nil, errors.New("unexpected SCAN reply")
	}
	res := &RedisScanResult{Cursor: fmt.Sprint(arr[0]), Keys: []RedisKey{}}
	names, _ := arr[1].([]interface{})
	for _, n := range names {
		name := fmt.Sprint(n)
		t, _ := c.Do("TYPE", name)
		res.Keys = append(res.Keys, RedisKey{Name: name, Type: fmt.Sprint(t)})
	}
	return res, nil
}

func RedisGet(s EngineSettings, db int, key string) (*RedisValue, error) {
	c, err := dialRedis(s, db)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	t, err := c.Do("TYPE", key)
	if err != nil {
		return nil, err
	}
	rv := &RedisValue{Key: key, Type: fmt.Sprint(t)}
	if ttl, err := c.Do("TTL", key); err == nil {
		rv.TTL, _ = ttl.(int64)
	}
	var v interface{}
	var sizeCmd []string
	switch rv.Type {
	case "string":
		v, err = c.Do("GET", key)
		sizeCmd = []string{"STRLEN", key}
	case "list":
		v, err = c.Do("LRANGE", key, "0", "499")
		sizeCmd = []string{"LLEN", key}
	case "set":
		v, err = c.Do("SRANDMEMBER", key, "500")
		sizeCmd = []string{"SCARD", key}
	case "zset":
		v, err = c.Do("ZRANGE", key, "0", "499", "WITHSCORES")
		sizeCmd = []string{"ZCARD", key}
	case "hash":
		v, err = c.Do("HGETALL", key)
		sizeCmd = []string{"HLEN", key}
	case "stream":
		v, err = c.Do("XRANGE", key, "-", "+", "COUNT", "100")
		sizeCmd = []string{"XLEN", key}
	case "none":
		return nil, fmt.Errorf("key %q does not exist", key)
	default:
		v = "(unsupported type " + rv.Type + ")"
	}
	if err != nil {
		return nil, err
	}
	if sizeCmd != nil {
		if n, err := c.Do(sizeCmd...); err == nil {
			rv.Size, _ = n.(int64)
		}
	}
	if s, ok := v.(string); ok && rv.Type == "string" {
		rv.Value = s
	} else if rv.Type == "hash" {
		arr, _ := v.([]interface{})
		var b strings.Builder
		for i := 0; i+1 < len(arr); i += 2 {
			fmt.Fprintf(&b, "%v = %v\n", arr[i], arr[i+1])
		}
		rv.Value = b.String()
	} else {
		rv.Value = formatRedis(v, "")
	}
	return rv, nil
}

func RedisCommand(s EngineSettings, db int, line string) (string, error) {
	args, err := splitArgs(line)
	if err != nil {
		return "", err
	}
	if len(args) == 0 {
		return "", errors.New("empty command")
	}
	c, err := dialRedis(s, db)
	if err != nil {
		return "", err
	}
	defer c.Close()
	v, err := c.Do(args...)
	if err != nil {
		if re, ok := err.(RedisError); ok {
			return formatRedis(re, ""), nil
		}
		return "", err
	}
	return formatRedis(v, ""), nil
}

func RedisDelete(s EngineSettings, db int, key string) error {
	c, err := dialRedis(s, db)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Do("DEL", key)
	return err
}
