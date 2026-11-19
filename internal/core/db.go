package core

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

const maxRows = 1000

func openSQL(engine string, s EngineSettings, dbName string) (*sql.DB, error) {
	var db *sql.DB
	var err error
	switch engine {
	case "mariadb", "mysql":
		cfg := mysql.NewConfig()
		cfg.User = s.User
		cfg.Passwd = s.Password
		cfg.Net = "tcp"
		cfg.Addr = fmt.Sprintf("127.0.0.1:%d", s.Port)
		cfg.DBName = dbName
		cfg.Timeout = 5 * time.Second
		cfg.ReadTimeout = 120 * time.Second
		cfg.MultiStatements = true
		cfg.AllowNativePasswords = true
		cfg.AllowCleartextPasswords = true
		cfg.TLSConfig = "preferred"
		cfg.Params = map[string]string{"charset": "utf8mb4"}
		db, err = sql.Open("mysql", cfg.FormatDSN())
	case "postgresql":
		if dbName == "" {
			dbName = "postgres"
		}
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(s.User, s.Password),
			Host:     fmt.Sprintf("127.0.0.1:%d", s.Port),
			Path:     "/" + dbName,
			RawQuery: "sslmode=disable&connect_timeout=5",
		}
		db, err = sql.Open("postgres", u.String())
	default:
		return nil, fmt.Errorf("%s is not a SQL engine", engine)
	}
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

type QueryResult struct {
	Columns    []string    `json:"columns"`
	Rows       [][]*string `json:"rows"`
	IsQuery    bool        `json:"isQuery"`
	Affected   int64       `json:"affected"`
	Truncated  bool        `json:"truncated"`
	DurationMs float64     `json:"durationMs"`
}

type TableInfo struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	Select string `json:"select"` // ready-to-run preview query
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

// sqlEscape escapes a MySQL string literal body.
func sqlEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "'", "''")
}

func ListDatabases(engine string, s EngineSettings) ([]string, error) {
	db, err := openSQL(engine, s, "")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	q := "SHOW DATABASES"
	if engine == "postgresql" {
		q = "SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY 1"
	}
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func ListTables(engine string, s EngineSettings, dbName string) ([]TableInfo, error) {
	db, err := openSQL(engine, s, dbName)
	if err != nil {
		return nil, err
	}
	defer db.Close()
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
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("query is empty")
	}
	db, err := openSQL(engine, s, dbName)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	start := time.Now()
	res := &QueryResult{Columns: []string{}, Rows: [][]*string{}}

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
		if len(res.Rows) >= maxRows {
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
				if len(v) > 2000 {
					v = v[:2000] + "…"
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
}

// CreateDatabase creates a database, and optionally a user with full access to it.
func CreateDatabase(engine string, s EngineSettings, name, user, password string) error {
	if !nameRe.MatchString(name) {
		return errors.New("database name may only contain letters, digits, _ $ -")
	}
	if user != "" && !nameRe.MatchString(user) {
		return errors.New("user name may only contain letters, digits, _ $ -")
	}
	db, err := openSQL(engine, s, "")
	if err != nil {
		return err
	}
	defer db.Close()

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
	if systemDBs[strings.ToLower(name)] {
		return fmt.Errorf("%q is a system database and cannot be dropped here", name)
	}
	db, err := openSQL(engine, s, "")
	if err != nil {
		return err
	}
	defer db.Close()
	if engine == "postgresql" {
		if _, err := db.Exec("DROP DATABASE " + quoteIdent(engine, name) + " WITH (FORCE)"); err == nil {
			return nil
		}
	}
	_, err = db.Exec("DROP DATABASE " + quoteIdent(engine, name))
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
