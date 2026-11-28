package core

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
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
		cfg.Timeout = 4 * time.Second
		cfg.ReadTimeout = 120 * time.Second
		cfg.WriteTimeout = 30 * time.Second
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
			RawQuery: fmt.Sprintf("sslmode=%s&connect_timeout=4", pgSSL),
		}
		return "postgres", u.String(), nil
	default:
		return "", "", fmt.Errorf("%s is not a SQL engine", engine)
	}
}

func isRemoteHost(s EngineSettings) bool {
	host := strings.TrimSpace(s.Host)
	return host != "" && !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost")
}

// openAndPingSQL opens and verifies a SQL connection pool, retrying once on remote TCP
// dial timeouts so Dual-WAN load-balanced routers recover automatically when one WAN stalls.
func openAndPingSQL(engine string, s EngineSettings, dbName string, maxOpen, maxIdle int, idleTime, lifetime time.Duration) (*sql.DB, string, error) {
	return openAndPingSQLCtx(context.Background(), engine, s, dbName, maxOpen, maxIdle, idleTime, lifetime)
}

func openAndPingSQLCtx(ctx context.Context, engine string, s EngineSettings, dbName string, maxOpen, maxIdle int, idleTime, lifetime time.Duration) (*sql.DB, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	driverName, dsn, err := sqlDriverAndDSN(engine, s, dbName)
	if err != nil {
		return nil, "", err
	}
	attempts := 1
	if isRemoteHost(s) {
		attempts = 3
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return nil, dsn, err
		}
		db, err := sql.Open(driverName, dsn)
		if err != nil {
			return nil, dsn, err
		}
		db.SetMaxOpenConns(maxOpen)
		if maxIdle > 0 {
			db.SetMaxIdleConns(maxIdle)
		}
		if idleTime > 0 {
			db.SetConnMaxIdleTime(idleTime)
		}
		if lifetime > 0 {
			db.SetConnMaxLifetime(lifetime)
		}
		if err := db.PingContext(ctx); err == nil {
			return db, dsn, nil
		} else {
			_ = db.Close()
			if ctx.Err() != nil {
				return nil, dsn, ctx.Err()
			}
			lastErr = err
			if !strings.Contains(strings.ToLower(err.Error()), "timeout") {
				break
			}
		}
	}
	return nil, dsn, fmt.Errorf("cannot connect to %s on port %d: %v", engine, s.Port, lastErr)
}

func openSQL(engine string, s EngineSettings, dbName string) (*sql.DB, error) {
	db, _, err := openAndPingSQLCtx(context.Background(), engine, s, dbName, 1, 0, 0, 0)
	return db, err
}

func openSQLCtx(ctx context.Context, engine string, s EngineSettings, dbName string) (*sql.DB, error) {
	db, _, err := openAndPingSQLCtx(ctx, engine, s, dbName, 1, 0, 0, 0)
	return db, err
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
	return pooledSQLCtx(context.Background(), engine, s, dbName)
}

func pooledSQLCtx(ctx context.Context, engine string, s EngineSettings, dbName string) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, dsn, err := sqlDriverAndDSN(engine, s, dbName)
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

	maxOpen, maxIdle := 4, 2
	idleTime, lifetime := 2*time.Minute, 10*time.Minute
	if isRemoteHost(s) {
		// Remote shared hosts (e.g. cPanel) and NAT routers drop idle TCP/TLS sockets aggressively
		maxIdle = 1
		idleTime = 20 * time.Second
		lifetime = 2 * time.Minute
	}
	db, _, err := openAndPingSQLCtx(ctx, engine, s, dbName, maxOpen, maxIdle, idleTime, lifetime)
	if err != nil {
		return nil, err
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

// evictSQLPool removes and closes a single cached *sql.DB entry matching (engine, s, dbName)
// so the next query opens a fresh connection instead of reusing a dropped socket.
func evictSQLPool(engine string, s EngineSettings, dbName string) {
	_, dsn, err := sqlDriverAndDSN(engine, s, dbName)
	if err != nil {
		return
	}
	key := engine + "|" + dsn
	sqlPoolMu.Lock()
	entry, ok := sqlPools[key]
	if ok {
		delete(sqlPools, key)
	}
	sqlPoolMu.Unlock()
	if ok && entry.db != nil {
		_ = entry.db.Close()
	}
}

// isTransientConnErr returns true when an error indicates a dropped/stale network or pooled SQL
// socket (such as go-sql-driver/mysql "invalid connection") where closing the stale pool and
// redialling can succeed cleanly. Never treats explicit cancellations as transient.
func isTransientConnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSyncCancelled) {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, mysql.ErrInvalidConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "context canceled") || strings.Contains(msg, "cancelled by user") {
		return false
	}
	for _, needle := range []string{
		"invalid connection",
		"bad connection",
		"unexpected eof",
		"connection reset",
		"broken pipe",
		"wsarecv",
		"wsasend",
		"use of closed network connection",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
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
	for attempt := 0; attempt < 2; attempt++ {
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
			if attempt == 0 && isTransientConnErr(err) {
				evictSQLPool(engine, s, s.DefaultDB)
				continue
			}
			if s.DefaultDB != "" {
				return []string{s.DefaultDB}, nil
			}
			return nil, err
		}
		out := []string{}
		seenDefault := false
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				if attempt == 0 && isTransientConnErr(err) {
					evictSQLPool(engine, s, s.DefaultDB)
					out = nil
					break
				}
				return nil, err
			}
			if s.DefaultDB != "" && strings.EqualFold(n, s.DefaultDB) {
				seenDefault = true
			}
			out = append(out, n)
		}
		rErr := rows.Err()
		rows.Close()
		if out == nil && attempt == 0 {
			continue
		}
		if rErr != nil {
			if attempt == 0 && isTransientConnErr(rErr) {
				evictSQLPool(engine, s, s.DefaultDB)
				continue
			}
			if s.DefaultDB != "" {
				return []string{s.DefaultDB}, nil
			}
			return nil, rErr
		}
		if s.DefaultDB != "" && !seenDefault {
			out = append([]string{s.DefaultDB}, out...)
		}
		return out, nil
	}
	return nil, errors.New("failed to list databases after reconnect")
}

func listTablesWithDB(engine string, db *sql.DB, dbName string) ([]TableInfo, error) {
	return listTablesWithDBCtx(context.Background(), engine, db, dbName)
}

func listTablesWithDBCtx(ctx context.Context, engine string, db *sql.DB, dbName string) ([]TableInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows *sql.Rows
	var err error
	if isMy(engine) {
		rows, err = db.QueryContext(ctx, "SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME", dbName)
	} else {
		rows, err = db.QueryContext(ctx, `SELECT table_schema, table_name, table_type FROM information_schema.tables
			WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema <> 'public', 1, 2`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TableInfo{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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

func ListTables(engine string, s EngineSettings, dbName string) ([]TableInfo, error) {
	return ListTablesCtx(context.Background(), engine, s, dbName)
}

func ListTablesCtx(ctx context.Context, engine string, s EngineSettings, dbName string) ([]TableInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if engine == "mssql" {
		return mssqlListTablesCtx(ctx, s, dbName)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		db, err := pooledSQLCtx(ctx, engine, s, dbName)
		if err != nil {
			return nil, err
		}
		out, err := listTablesWithDBCtx(ctx, engine, db, dbName)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if ctx.Err() == nil && attempt == 0 && isTransientConnErr(err) {
			evictSQLPool(engine, s, dbName)
			continue
		}
		return nil, err
	}
	return nil, lastErr
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

func RunQueryAllCtx(ctx context.Context, engine string, s EngineSettings, dbName, q string) (*QueryResult, error) {
	return RunQueryWithLimitCtx(ctx, engine, s, dbName, q, maxShowRows)
}

func RunQueryWithLimit(engine string, s EngineSettings, dbName, q string, limit int) (*QueryResult, error) {
	return RunQueryWithLimitCtx(context.Background(), engine, s, dbName, q, limit)
}

func RunQueryWithLimitCtx(ctx context.Context, engine string, s EngineSettings, dbName, q string, limit int) (*QueryResult, error) {
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
	if engine == "mssql" {
		return mssqlRunQueryWithLimitCtx(ctx, s, dbName, q, limit)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := runQueryOnceWithPoolCtx(ctx, engine, s, dbName, q, limit)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if ctx.Err() == nil && attempt == 0 && isTransientConnErr(err) {
			evictSQLPool(engine, s, dbName)
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func runQueryOnceWithPoolCtx(ctx context.Context, engine string, s EngineSettings, dbName, q string, limit int) (*QueryResult, error) {
	db, err := pooledSQLCtx(ctx, engine, s, dbName)
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
		r, err := db.ExecContext(ctx, q)
		if err != nil {
			return nil, err
		}
		res.Affected, _ = r.RowsAffected()
		res.DurationMs = float64(time.Since(start).Microseconds()) / 1000
		return res, nil
	}

	rows, err := db.QueryContext(ctx, q)
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		cols, err := describeTableOnceWithPool(engine, s, dbName, schema, table)
		if err == nil {
			return cols, nil
		}
		lastErr = err
		if attempt == 0 && isTransientConnErr(err) {
			evictSQLPool(engine, s, dbName)
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func describeTableOnceWithPool(engine string, s EngineSettings, dbName, schema, table string) ([]ColumnInfo, error) {
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

type DiagramTable struct {
	Name    string       `json:"name"`
	Schema  string       `json:"schema"`
	Kind    string       `json:"kind"`
	Columns []ColumnInfo `json:"columns"`
}

type ForeignKeyInfo struct {
	ConstraintName string `json:"constraintName"`
	FromTable      string `json:"fromTable"`
	FromColumn     string `json:"fromColumn"`
	ToTable        string `json:"toTable"`
	ToColumn       string `json:"toColumn"`
	UpdateRule     string `json:"updateRule,omitempty"`
	DeleteRule     string `json:"deleteRule,omitempty"`
}

type DatabaseDiagramData struct {
	Engine      string           `json:"engine"`
	Database    string           `json:"database"`
	Tables      []DiagramTable   `json:"tables"`
	ForeignKeys []ForeignKeyInfo `json:"foreignKeys"`
}

type TableColumnDetail struct {
	Name            string  `json:"name"`
	DataType        string  `json:"dataType"`
	Nullable        bool    `json:"nullable"`
	DefaultVal      *string `json:"defaultVal,omitempty"`
	IsPrimaryKey    bool    `json:"isPrimaryKey"`
	IsAutoIncrement bool    `json:"isAutoIncrement"`
	IsUnique        bool    `json:"isUnique,omitempty"`
	IsForeignKey    bool    `json:"isForeignKey,omitempty"`
	Extra           string  `json:"extra,omitempty"`
	Comment         string  `json:"comment,omitempty"`
	Collation       string  `json:"collation,omitempty"`
}

type TableIndexInfo struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsUnique  bool     `json:"isUnique"`
	IsPrimary bool     `json:"isPrimary"`
	IndexType string   `json:"indexType,omitempty"`
}

type TableProperties struct {
	StorageEngine string `json:"storageEngine,omitempty"`
	Collation     string `json:"collation,omitempty"`
	RowCount      int64  `json:"rowCount"`
	DataBytes     int64  `json:"dataBytes"`
	IndexBytes    int64  `json:"indexBytes"`
	AutoIncrement string `json:"autoIncrement,omitempty"`
	CreatedAt     string `json:"createdAt,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	Comment       string `json:"comment,omitempty"`
	Owner         string `json:"owner,omitempty"`
}

type TableDetails struct {
	Engine              string              `json:"engine"`
	Database            string              `json:"database"`
	Schema              string              `json:"schema"`
	Table               string              `json:"table"`
	Kind                string              `json:"kind"`
	Columns             []TableColumnDetail `json:"columns"`
	PrimaryKey          []string            `json:"primaryKey"`
	OutgoingForeignKeys []ForeignKeyInfo    `json:"outgoingForeignKeys"`
	IncomingForeignKeys []ForeignKeyInfo    `json:"incomingForeignKeys"`
	Indexes             []TableIndexInfo    `json:"indexes"`
	Properties          TableProperties     `json:"properties"`
	CreateDDL           string              `json:"createDdl"`
}

// GetTableDetails returns complete structure metadata for a single table or view:
// columns, primary key, outgoing & incoming foreign keys, indexes, storage properties, and DDL.
func GetTableDetails(engine string, s EngineSettings, dbName, tableName string) (*TableDetails, error) {
	dbName = strings.TrimSpace(dbName)
	schema, table := splitSchemaTable(engine, dbName, tableName)
	if table == "" {
		return nil, errors.New("table name is required")
	}
	if engine == "mssql" {
		return mssqlGetTableDetails(s, dbName, schema, table)
	}
	if !isMy(engine) && engine != "postgresql" {
		return nil, fmt.Errorf("table details are not supported for %s", engine)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		out, err := tableDetailsOnceWithPool(engine, s, dbName, schema, table)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt == 0 && isTransientConnErr(err) {
			evictSQLPool(engine, s, dbName)
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func tableDetailsOnceWithPool(engine string, s EngineSettings, dbName, schema, table string) (*TableDetails, error) {
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return nil, err
	}

	out := &TableDetails{
		Engine:              engine,
		Database:            dbName,
		Schema:              schema,
		Table:               table,
		Kind:                "BASE TABLE",
		Columns:             []TableColumnDetail{},
		PrimaryKey:          []string{},
		OutgoingForeignKeys: []ForeignKeyInfo{},
		IncomingForeignKeys: []ForeignKeyInfo{},
		Indexes:             []TableIndexInfo{},
	}

	if isMy(engine) {
		// 1. Columns
		cRows, err := db.Query(`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY,
				COALESCE(EXTRA, ''), COALESCE(COLUMN_COMMENT, ''), COALESCE(COLLATION_NAME, '')
			FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
			ORDER BY ORDINAL_POSITION`, schema, table)
		if err != nil {
			return nil, err
		}
		for cRows.Next() {
			var col TableColumnDetail
			var isNull, colKey string
			var def sql.NullString
			if err := cRows.Scan(&col.Name, &col.DataType, &isNull, &def, &colKey, &col.Extra, &col.Comment, &col.Collation); err != nil {
				cRows.Close()
				return nil, err
			}
			if def.Valid {
				v := def.String
				col.DefaultVal = &v
			}
			col.Nullable = strings.EqualFold(isNull, "YES")
			col.IsPrimaryKey = strings.EqualFold(colKey, "PRI")
			col.IsUnique = strings.EqualFold(colKey, "UNI")
			col.IsAutoIncrement = strings.Contains(strings.ToLower(col.Extra), "auto_increment")
			out.Columns = append(out.Columns, col)
		}
		if err := cRows.Err(); err != nil {
			cRows.Close()
			return nil, err
		}
		cRows.Close()

		// 2. Outgoing foreign keys (this table -> referenced parent tables)
		outFkRows, err := db.Query(`SELECT k.CONSTRAINT_NAME, k.TABLE_NAME, k.COLUMN_NAME,
				k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
				COALESCE(r.UPDATE_RULE, ''), COALESCE(r.DELETE_RULE, '')
			FROM information_schema.KEY_COLUMN_USAGE k
			LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
				ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
			WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
			ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, schema, table)
		if err == nil {
			for outFkRows.Next() {
				var fk ForeignKeyInfo
				if err := outFkRows.Scan(&fk.ConstraintName, &fk.FromTable, &fk.FromColumn, &fk.ToTable, &fk.ToColumn, &fk.UpdateRule, &fk.DeleteRule); err == nil {
					out.OutgoingForeignKeys = append(out.OutgoingForeignKeys, fk)
				}
			}
			outFkRows.Close()
		}

		// 3. Incoming foreign keys (child tables -> this table)
		inFkRows, err := db.Query(`SELECT k.CONSTRAINT_NAME, k.TABLE_NAME, k.COLUMN_NAME,
				k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
				COALESCE(r.UPDATE_RULE, ''), COALESCE(r.DELETE_RULE, '')
			FROM information_schema.KEY_COLUMN_USAGE k
			LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
				ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
			WHERE k.REFERENCED_TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME = ?
			ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, schema, table)
		if err == nil {
			for inFkRows.Next() {
				var fk ForeignKeyInfo
				if err := inFkRows.Scan(&fk.ConstraintName, &fk.FromTable, &fk.FromColumn, &fk.ToTable, &fk.ToColumn, &fk.UpdateRule, &fk.DeleteRule); err == nil {
					out.IncomingForeignKeys = append(out.IncomingForeignKeys, fk)
				}
			}
			inFkRows.Close()
		}

		// 4. Indexes
		idxRows, err := db.Query(`SELECT INDEX_NAME, COALESCE(COLUMN_NAME, ''), NON_UNIQUE, COALESCE(INDEX_TYPE, '')
			FROM information_schema.STATISTICS
			WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
			ORDER BY CASE WHEN INDEX_NAME = 'PRIMARY' THEN 0 ELSE 1 END, INDEX_NAME, SEQ_IN_INDEX`, schema, table)
		if err == nil {
			idxByName := map[string]int{}
			for idxRows.Next() {
				var idxName, colName, idxType string
				var nonUnique int
				if err := idxRows.Scan(&idxName, &colName, &nonUnique, &idxType); err != nil {
					continue
				}
				pos, ok := idxByName[idxName]
				if !ok {
					pos = len(out.Indexes)
					idxByName[idxName] = pos
					out.Indexes = append(out.Indexes, TableIndexInfo{
						Name:      idxName,
						Columns:   []string{},
						IsUnique:  nonUnique == 0,
						IsPrimary: strings.EqualFold(idxName, "PRIMARY"),
						IndexType: idxType,
					})
				}
				if colName != "" {
					out.Indexes[pos].Columns = append(out.Indexes[pos].Columns, colName)
				}
			}
			idxRows.Close()
		}

		// 5. Table properties
		var tblType, engName, coll, autoInc, createdAt, updatedAt, comment string
		var rowCnt, dataLen, idxLen int64
		if err := db.QueryRow(`SELECT COALESCE(TABLE_TYPE, 'BASE TABLE'), COALESCE(ENGINE, ''), COALESCE(TABLE_COLLATION, ''),
				COALESCE(TABLE_ROWS, 0), COALESCE(DATA_LENGTH, 0), COALESCE(INDEX_LENGTH, 0),
				COALESCE(CAST(AUTO_INCREMENT AS CHAR), ''),
				COALESCE(DATE_FORMAT(CREATE_TIME, '%Y-%m-%d %H:%i:%s'), ''),
				COALESCE(DATE_FORMAT(UPDATE_TIME, '%Y-%m-%d %H:%i:%s'), ''),
				COALESCE(TABLE_COMMENT, '')
			FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, schema, table).
			Scan(&tblType, &engName, &coll, &rowCnt, &dataLen, &idxLen, &autoInc, &createdAt, &updatedAt, &comment); err == nil {
			if tblType != "" {
				out.Kind = tblType
			}
			out.Properties = TableProperties{
				StorageEngine: engName,
				Collation:     coll,
				RowCount:      rowCnt,
				DataBytes:     dataLen,
				IndexBytes:    idxLen,
				AutoIncrement: autoInc,
				CreatedAt:     createdAt,
				UpdatedAt:     updatedAt,
				Comment:       comment,
			}
		}

		// 6. Native SHOW CREATE TABLE / VIEW DDL (supports both 2-col table and 4-col view responses)
		showSQL := "SHOW CREATE TABLE " + quoteIdent(engine, schema) + "." + quoteIdent(engine, table)
		if ddlRows, derr := db.Query(showSQL); derr == nil {
			if cols, cerr := ddlRows.Columns(); cerr == nil && len(cols) >= 2 && ddlRows.Next() {
				raw := make([]sql.RawBytes, len(cols))
				ptrs := make([]interface{}, len(cols))
				for i := range raw {
					ptrs[i] = &raw[i]
				}
				if ddlRows.Scan(ptrs...) == nil {
					out.CreateDDL = string(raw[1])
				}
			}
			ddlRows.Close()
		}

		finalizeTableDetails(out)
		return out, nil
	}

	// PostgreSQL
	// 1. Columns
	cRows, err := db.Query(`SELECT a.attname,
			format_type(a.atttypid, a.atttypmod),
			CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END,
			pg_get_expr(d.adbin, d.adrelid),
			CASE WHEN EXISTS (
				SELECT 1 FROM pg_index i
				WHERE i.indrelid = a.attrelid AND i.indisprimary AND a.attnum = ANY(i.indkey)
			) THEN 'PRI' ELSE '' END,
			CASE WHEN a.attidentity = 'a' THEN 'GENERATED ALWAYS AS IDENTITY'
				 WHEN a.attidentity = 'd' THEN 'GENERATED BY DEFAULT AS IDENTITY'
				 WHEN COALESCE(pg_get_expr(d.adbin, d.adrelid), '') LIKE 'nextval(%' THEN 'serial / nextval'
				 ELSE '' END,
			COALESCE(col_description(a.attrelid, a.attnum), ''),
			COALESCE(coll.collname, '')
		FROM pg_attribute a
		JOIN pg_class cl ON cl.oid = a.attrelid
		JOIN pg_namespace ns ON ns.oid = cl.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		LEFT JOIN pg_collation coll ON coll.oid = a.attcollation AND a.attcollation <> 0
		WHERE ns.nspname = $1 AND cl.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, err
	}
	for cRows.Next() {
		var col TableColumnDetail
		var isNull, colKey string
		var def sql.NullString
		if err := cRows.Scan(&col.Name, &col.DataType, &isNull, &def, &colKey, &col.Extra, &col.Comment, &col.Collation); err != nil {
			cRows.Close()
			return nil, err
		}
		if def.Valid {
			v := def.String
			col.DefaultVal = &v
		}
		col.Nullable = strings.EqualFold(isNull, "YES")
		col.IsPrimaryKey = strings.EqualFold(colKey, "PRI")
		col.IsAutoIncrement = col.Extra != ""
		out.Columns = append(out.Columns, col)
	}
	if err := cRows.Err(); err != nil {
		cRows.Close()
		return nil, err
	}
	cRows.Close()

	// 2. Outgoing foreign keys
	pgFKBase := `SELECT c.conname,
			CASE WHEN n1.nspname = 'public' THEN c1.relname ELSE n1.nspname || '.' || c1.relname END,
			a1.attname,
			CASE WHEN n2.nspname = 'public' THEN c2.relname ELSE n2.nspname || '.' || c2.relname END,
			a2.attname,
			CASE c.confupdtype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END,
			CASE c.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END
		FROM pg_constraint c
		JOIN pg_class c1 ON c1.oid = c.conrelid
		JOIN pg_namespace n1 ON n1.oid = c1.relnamespace
		JOIN pg_class c2 ON c2.oid = c.confrelid
		JOIN pg_namespace n2 ON n2.oid = c2.relnamespace
		CROSS JOIN LATERAL unnest(c.conkey, c.confkey) WITH ORDINALITY AS u(att1, att2, ord)
		JOIN pg_attribute a1 ON a1.attrelid = c1.oid AND a1.attnum = u.att1
		JOIN pg_attribute a2 ON a2.attrelid = c2.oid AND a2.attnum = u.att2
		WHERE c.contype = 'f'`

	if outRows, oerr := db.Query(pgFKBase+` AND n1.nspname = $1 AND c1.relname = $2 ORDER BY c.conname, u.ord`, schema, table); oerr == nil {
		for outRows.Next() {
			var fk ForeignKeyInfo
			if outRows.Scan(&fk.ConstraintName, &fk.FromTable, &fk.FromColumn, &fk.ToTable, &fk.ToColumn, &fk.UpdateRule, &fk.DeleteRule) == nil {
				out.OutgoingForeignKeys = append(out.OutgoingForeignKeys, fk)
			}
		}
		outRows.Close()
	}

	// 3. Incoming foreign keys
	if inRows, ierr := db.Query(pgFKBase+` AND n2.nspname = $1 AND c2.relname = $2 ORDER BY 2, c.conname, u.ord`, schema, table); ierr == nil {
		for inRows.Next() {
			var fk ForeignKeyInfo
			if inRows.Scan(&fk.ConstraintName, &fk.FromTable, &fk.FromColumn, &fk.ToTable, &fk.ToColumn, &fk.UpdateRule, &fk.DeleteRule) == nil {
				out.IncomingForeignKeys = append(out.IncomingForeignKeys, fk)
			}
		}
		inRows.Close()
	}

	// 4. Indexes
	if idxRows, ierr := db.Query(`SELECT ic.relname,
			ix.indisunique,
			ix.indisprimary,
			am.amname,
			COALESCE((
				SELECT string_agg(pg_get_indexdef(ix.indexrelid, k, true), E'\x1f' ORDER BY k)
				FROM generate_series(1, CASE WHEN ix.indnkeyatts > 0 THEN ix.indnkeyatts ELSE ix.indnatts END) AS k
			), '')
		FROM pg_index ix
		JOIN pg_class t ON t.oid = ix.indrelid
		JOIN pg_namespace ns ON ns.oid = t.relnamespace
		JOIN pg_class ic ON ic.oid = ix.indexrelid
		JOIN pg_am am ON am.oid = ic.relam
		WHERE ns.nspname = $1 AND t.relname = $2
		ORDER BY ix.indisprimary DESC, ix.indisunique DESC, ic.relname`, schema, table); ierr == nil {
		for idxRows.Next() {
			var idx TableIndexInfo
			var colsRaw string
			if idxRows.Scan(&idx.Name, &idx.IsUnique, &idx.IsPrimary, &idx.IndexType, &colsRaw) == nil {
				if colsRaw != "" {
					idx.Columns = strings.Split(colsRaw, "\x1f")
				} else {
					idx.Columns = []string{}
				}
				out.Indexes = append(out.Indexes, idx)
			}
		}
		idxRows.Close()
	}

	// 5. Table properties & optional view definition
	var kind, owner, comment, viewDef string
	var rowCnt, dataBytes, idxBytes int64
	if err := db.QueryRow(`SELECT CASE WHEN c.relkind IN ('v', 'm') THEN 'VIEW' ELSE 'BASE TABLE' END,
			COALESCE(pg_get_userbyid(c.relowner), ''),
			COALESCE(NULLIF(c.reltuples, -1)::bigint, 0),
			CASE WHEN c.relkind = 'v' THEN 0 ELSE COALESCE(pg_table_size(c.oid), 0) END,
			CASE WHEN c.relkind = 'v' THEN 0 ELSE COALESCE(pg_indexes_size(c.oid), 0) END,
			COALESCE(obj_description(c.oid, 'pg_class'), ''),
			CASE WHEN c.relkind IN ('v', 'm') THEN COALESCE(pg_get_viewdef(c.oid, true), '') ELSE '' END
		FROM pg_class c
		JOIN pg_namespace ns ON ns.oid = c.relnamespace
		WHERE ns.nspname = $1 AND c.relname = $2`, schema, table).
		Scan(&kind, &owner, &rowCnt, &dataBytes, &idxBytes, &comment, &viewDef); err == nil {
		if kind != "" {
			out.Kind = kind
		}
		out.Properties = TableProperties{
			StorageEngine: "PostgreSQL",
			Owner:         owner,
			RowCount:      rowCnt,
			DataBytes:     dataBytes,
			IndexBytes:    idxBytes,
			Comment:       comment,
		}
		if viewDef != "" {
			out.CreateDDL = fmt.Sprintf("CREATE OR REPLACE VIEW %s AS\n%s", qualifiedTable(engine, schema, table), strings.TrimSpace(viewDef))
		}
	}

	finalizeTableDetails(out)
	return out, nil
}

// finalizeTableDetails normalizes column key flags (PK, FK, Unique), ordered PrimaryKey slice,
// and generates a synthetic CREATE TABLE DDL when the engine does not supply native DDL.
func finalizeTableDetails(d *TableDetails) {
	if d == nil {
		return
	}
	fkColSet := map[string]bool{}
	for _, fk := range d.OutgoingForeignKeys {
		fkColSet[strings.ToLower(fk.FromColumn)] = true
	}
	uniqSingleCol := map[string]bool{}
	pkOrdered := []string{}
	for _, idx := range d.Indexes {
		if idx.IsPrimary && len(idx.Columns) > 0 && len(pkOrdered) == 0 {
			pkOrdered = append(pkOrdered, idx.Columns...)
		}
		if idx.IsUnique && !idx.IsPrimary && len(idx.Columns) == 1 {
			uniqSingleCol[strings.ToLower(strings.Trim(idx.Columns[0], `"`))] = true
		}
	}
	pkSet := map[string]bool{}
	for _, c := range pkOrdered {
		pkSet[strings.ToLower(strings.Trim(c, `"`))] = true
	}
	for i := range d.Columns {
		low := strings.ToLower(d.Columns[i].Name)
		if fkColSet[low] {
			d.Columns[i].IsForeignKey = true
		}
		if uniqSingleCol[low] {
			d.Columns[i].IsUnique = true
		}
		if pkSet[low] {
			d.Columns[i].IsPrimaryKey = true
		} else if d.Columns[i].IsPrimaryKey && len(pkOrdered) == 0 {
			pkOrdered = append(pkOrdered, d.Columns[i].Name)
		}
	}
	d.PrimaryKey = pkOrdered
	if strings.TrimSpace(d.CreateDDL) == "" {
		d.CreateDDL = buildSyntheticCreateTableDDL(d)
	}
}

func buildSyntheticCreateTableDDL(d *TableDetails) string {
	if d == nil || len(d.Columns) == 0 {
		return ""
	}
	qCol := func(c string) string {
		if d.Engine == "mssql" {
			return mssqlBracket(c)
		}
		return quoteIdent(d.Engine, c)
	}
	qTbl := qualifiedTable(d.Engine, d.Schema, d.Table)
	var lines []string
	for _, col := range d.Columns {
		var parts []string
		parts = append(parts, "  "+qCol(col.Name), col.DataType)
		if col.IsAutoIncrement && d.Engine == "mssql" {
			parts = append(parts, "IDENTITY(1,1)")
		}
		if !col.Nullable {
			parts = append(parts, "NOT NULL")
		}
		if col.DefaultVal != nil && strings.TrimSpace(*col.DefaultVal) != "" {
			parts = append(parts, "DEFAULT "+*col.DefaultVal)
		}
		lines = append(lines, strings.Join(parts, " "))
	}
	if len(d.PrimaryKey) > 0 {
		pkCols := make([]string, len(d.PrimaryKey))
		for i, c := range d.PrimaryKey {
			pkCols[i] = qCol(strings.Trim(c, `"`))
		}
		lines = append(lines, fmt.Sprintf("  PRIMARY KEY (%s)", strings.Join(pkCols, ", ")))
	}

	// Group multi-column outgoing foreign keys by constraint name
	type fkGroup struct {
		name       string
		fromCols   []string
		toTable    string
		toCols     []string
		updateRule string
		deleteRule string
	}
	var fkGroups []*fkGroup
	fkByName := map[string]*fkGroup{}
	for _, fk := range d.OutgoingForeignKeys {
		g, ok := fkByName[fk.ConstraintName]
		if !ok {
			g = &fkGroup{
				name:       fk.ConstraintName,
				toTable:    fk.ToTable,
				updateRule: fk.UpdateRule,
				deleteRule: fk.DeleteRule,
			}
			fkByName[fk.ConstraintName] = g
			fkGroups = append(fkGroups, g)
		}
		g.fromCols = append(g.fromCols, qCol(fk.FromColumn))
		g.toCols = append(g.toCols, qCol(fk.ToColumn))
	}
	for _, g := range fkGroups {
		refSchema, refTbl := splitSchemaTable(d.Engine, d.Database, g.toTable)
		refQual := qualifiedTable(d.Engine, refSchema, refTbl)
		clause := fmt.Sprintf("  CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
			qCol(g.name), strings.Join(g.fromCols, ", "), refQual, strings.Join(g.toCols, ", "))
		if u := strings.TrimSpace(g.updateRule); u != "" && !strings.EqualFold(u, "NO ACTION") && !strings.EqualFold(u, "NO_ACTION") {
			clause += " ON UPDATE " + strings.ReplaceAll(u, "_", " ")
		}
		if del := strings.TrimSpace(g.deleteRule); del != "" && !strings.EqualFold(del, "NO ACTION") && !strings.EqualFold(del, "NO_ACTION") {
			clause += " ON DELETE " + strings.ReplaceAll(del, "_", " ")
		}
		lines = append(lines, clause)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n%s\n);", qTbl, strings.Join(lines, ",\n"))
	for _, idx := range d.Indexes {
		if idx.IsPrimary || len(idx.Columns) == 0 {
			continue
		}
		uniq := ""
		if idx.IsUnique {
			uniq = "UNIQUE "
		}
		cols := make([]string, len(idx.Columns))
		for i, c := range idx.Columns {
			cols[i] = qCol(strings.Trim(c, `"`))
		}
		fmt.Fprintf(&b, "\nCREATE %sINDEX %s ON %s (%s);", uniq, qCol(idx.Name), qTbl, strings.Join(cols, ", "))
	}
	return b.String()
}

// GetDatabaseDiagram returns all tables, columns, and foreign key relationships for dbName
// in a minimal batch of metadata queries suitable for rendering an interactive ER diagram.
func GetDatabaseDiagram(engine string, s EngineSettings, dbName string) (*DatabaseDiagramData, error) {
	dbName = strings.TrimSpace(dbName)
	if dbName == "" {
		return nil, errors.New("database name is required")
	}
	if engine == "mssql" {
		return mssqlDatabaseDiagram(s, dbName)
	}
	if !isMy(engine) && engine != "postgresql" {
		return nil, fmt.Errorf("database diagram is not supported for %s", engine)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		out, err := databaseDiagramOnceWithPool(engine, s, dbName)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt == 0 && isTransientConnErr(err) {
			evictSQLPool(engine, s, dbName)
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func databaseDiagramOnceWithPool(engine string, s EngineSettings, dbName string) (*DatabaseDiagramData, error) {
	db, err := pooledSQL(engine, s, dbName)
	if err != nil {
		return nil, err
	}

	out := &DatabaseDiagramData{
		Engine:      engine,
		Database:    dbName,
		Tables:      []DiagramTable{},
		ForeignKeys: []ForeignKeyInfo{},
	}
	tableByKey := map[string]int{}

	addTable := func(schema, name, kind string) int {
		display := name
		if engine == "postgresql" && schema != "" && schema != "public" {
			display = schema + "." + name
		}
		key := schema + "." + name
		if idx, ok := tableByKey[key]; ok {
			return idx
		}
		idx := len(out.Tables)
		tableByKey[key] = idx
		out.Tables = append(out.Tables, DiagramTable{
			Name:    display,
			Schema:  schema,
			Kind:    kind,
			Columns: []ColumnInfo{},
		})
		return idx
	}

	if isMy(engine) {
		tRows, err := db.Query(`SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, dbName)
		if err != nil {
			return nil, err
		}
		for tRows.Next() {
			var sch, tbl, kind string
			if err := tRows.Scan(&sch, &tbl, &kind); err != nil {
				tRows.Close()
				return nil, err
			}
			addTable(sch, tbl, kind)
		}
		if err := tRows.Err(); err != nil {
			tRows.Close()
			return nil, err
		}
		tRows.Close()

		cRows, err := db.Query(`SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY, EXTRA
			FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = ?
			ORDER BY TABLE_NAME, ORDINAL_POSITION`, dbName)
		if err != nil {
			return nil, err
		}
		for cRows.Next() {
			var sch, tbl, col, dataType, isNull, colKey, extra string
			var def sql.NullString
			if err := cRows.Scan(&sch, &tbl, &col, &dataType, &isNull, &def, &colKey, &extra); err != nil {
				cRows.Close()
				return nil, err
			}
			idx := addTable(sch, tbl, "BASE TABLE")
			var defPtr *string
			if def.Valid {
				v := def.String
				defPtr = &v
			}
			out.Tables[idx].Columns = append(out.Tables[idx].Columns, ColumnInfo{
				Name:            col,
				DataType:        dataType,
				Nullable:        strings.EqualFold(isNull, "YES"),
				DefaultVal:      defPtr,
				IsPrimaryKey:    strings.EqualFold(colKey, "PRI"),
				IsAutoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment"),
			})
		}
		if err := cRows.Err(); err != nil {
			cRows.Close()
			return nil, err
		}
		cRows.Close()

		fkRows, err := db.Query(`SELECT k.CONSTRAINT_NAME, k.TABLE_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
				COALESCE(r.UPDATE_RULE, ''), COALESCE(r.DELETE_RULE, '')
			FROM information_schema.KEY_COLUMN_USAGE k
			LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
				ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
			WHERE k.TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
			ORDER BY k.TABLE_NAME, k.ORDINAL_POSITION`, dbName)
		if err != nil {
			return nil, err
		}
		for fkRows.Next() {
			var fk ForeignKeyInfo
			if err := fkRows.Scan(&fk.ConstraintName, &fk.FromTable, &fk.FromColumn, &fk.ToTable, &fk.ToColumn, &fk.UpdateRule, &fk.DeleteRule); err != nil {
				fkRows.Close()
				return nil, err
			}
			out.ForeignKeys = append(out.ForeignKeys, fk)
		}
		if err := fkRows.Err(); err != nil {
			fkRows.Close()
			return nil, err
		}
		fkRows.Close()
		return out, nil
	}

	// PostgreSQL: load tables/views + columns, then FK constraints
	pgRows, err := db.Query(`SELECT ns.nspname, cl.relname,
			CASE WHEN cl.relkind IN ('v', 'm') THEN 'VIEW' ELSE 'BASE TABLE' END,
			COALESCE(a.attname, ''),
			COALESCE(format_type(a.atttypid, a.atttypmod), ''),
			CASE WHEN a.attname IS NULL THEN 'YES' WHEN a.attnotnull THEN 'NO' ELSE 'YES' END,
			pg_get_expr(d.adbin, d.adrelid),
			CASE WHEN a.attname IS NOT NULL AND EXISTS (
				SELECT 1 FROM pg_index i
				WHERE i.indrelid = a.attrelid AND i.indisprimary AND a.attnum = ANY(i.indkey)
			) THEN 'PRI' ELSE '' END,
			CASE WHEN a.attname IS NOT NULL AND (a.attidentity IN ('a', 'd') OR COALESCE(pg_get_expr(d.adbin, d.adrelid), '') LIKE 'nextval(%') THEN 'auto_increment' ELSE '' END
		FROM pg_class cl
		JOIN pg_namespace ns ON ns.oid = cl.relnamespace
		LEFT JOIN pg_attribute a ON a.attrelid = cl.oid AND a.attnum > 0 AND NOT a.attisdropped
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE cl.relkind IN ('r', 'p', 'v', 'm')
		  AND ns.nspname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY ns.nspname <> 'public', ns.nspname, cl.relname, a.attnum`)
	if err != nil {
		return nil, err
	}
	for pgRows.Next() {
		var sch, tbl, kind, col, dataType, isNull, colKey, extra string
		var def sql.NullString
		if err := pgRows.Scan(&sch, &tbl, &kind, &col, &dataType, &isNull, &def, &colKey, &extra); err != nil {
			pgRows.Close()
			return nil, err
		}
		idx := addTable(sch, tbl, kind)
		if col == "" {
			continue
		}
		var defPtr *string
		if def.Valid {
			v := def.String
			defPtr = &v
		}
		out.Tables[idx].Columns = append(out.Tables[idx].Columns, ColumnInfo{
			Name:            col,
			DataType:        dataType,
			Nullable:        strings.EqualFold(isNull, "YES"),
			DefaultVal:      defPtr,
			IsPrimaryKey:    strings.EqualFold(colKey, "PRI"),
			IsAutoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment"),
		})
	}
	if err := pgRows.Err(); err != nil {
		pgRows.Close()
		return nil, err
	}
	pgRows.Close()

	fkRows, err := db.Query(`SELECT c.conname,
			CASE WHEN n1.nspname = 'public' THEN c1.relname ELSE n1.nspname || '.' || c1.relname END,
			a1.attname,
			CASE WHEN n2.nspname = 'public' THEN c2.relname ELSE n2.nspname || '.' || c2.relname END,
			a2.attname,
			CASE c.confupdtype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END,
			CASE c.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END
		FROM pg_constraint c
		JOIN pg_class c1 ON c1.oid = c.conrelid
		JOIN pg_namespace n1 ON n1.oid = c1.relnamespace
		JOIN pg_class c2 ON c2.oid = c.confrelid
		JOIN pg_namespace n2 ON n2.oid = c2.relnamespace
		CROSS JOIN LATERAL unnest(c.conkey, c.confkey) WITH ORDINALITY AS u(att1, att2, ord)
		JOIN pg_attribute a1 ON a1.attrelid = c1.oid AND a1.attnum = u.att1
		JOIN pg_attribute a2 ON a2.attrelid = c2.oid AND a2.attnum = u.att2
		WHERE c.contype = 'f' AND n1.nspname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY 2, c.conname, u.ord`)
	if err == nil {
		for fkRows.Next() {
			var fk ForeignKeyInfo
			if err := fkRows.Scan(&fk.ConstraintName, &fk.FromTable, &fk.FromColumn, &fk.ToTable, &fk.ToColumn, &fk.UpdateRule, &fk.DeleteRule); err != nil {
				fkRows.Close()
				return nil, err
			}
			out.ForeignKeys = append(out.ForeignKeys, fk)
		}
		fkRows.Close()
	}
	return out, nil
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
