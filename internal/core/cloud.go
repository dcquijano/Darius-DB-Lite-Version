//go:build windows

package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type CloudConnection struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"` // cpanel | monsterasp | supabase | neon | aiven | tidb | rds | azure | rediscloud | custom
	Engine        string `json:"engine"`   // mysql | mariadb | postgresql | mssql | redis
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Database      string `json:"database"`
	User          string `json:"user"`
	Password      string `json:"password"`
	SSLMode       string `json:"sslMode"` // disable | preferred | require | skip-verify
	PanelURL      string `json:"panelUrl,omitempty"`
	Notes         string `json:"notes,omitempty"`
	LastConnected string `json:"lastConnected,omitempty"`
	LastLatencyMs int64  `json:"lastLatencyMs,omitempty"`
}

type CloudTestResult struct {
	OK         bool     `json:"ok"`
	LatencyMs  int64    `json:"latencyMs"`
	ServerInfo string   `json:"serverInfo,omitempty"`
	Databases  []string `json:"databases,omitempty"`
	Message    string   `json:"message"`
}

func CloudDefaultPort(engine string) int {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "mysql", "mariadb":
		return 3306
	case "postgresql", "postgres", "pgsql":
		return 5432
	case "mssql", "sqlserver":
		return 1433
	case "redis":
		return 6379
	default:
		return 3306
	}
}

func normalizeCloudEngine(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgres", "pgsql", "postgresql":
		return "postgresql"
	case "mariadb":
		return "mariadb"
	case "mssql", "sqlserver", "tsql":
		return "mssql"
	case "redis", "rediss":
		return "redis"
	default:
		return "mysql"
	}
}

func newCloudID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("cloud-%d", time.Now().UnixNano())
	}
	return "cloud-" + hex.EncodeToString(b[:])
}

func (c CloudConnection) Normalized() CloudConnection {
	c.ID = strings.TrimSpace(c.ID)
	if c.ID == "" {
		c.ID = newCloudID()
	}
	c.Engine = normalizeCloudEngine(c.Engine)
	c.Host = strings.TrimSpace(c.Host)
	c.Database = strings.TrimSpace(c.Database)
	c.User = strings.TrimSpace(c.User)
	c.Name = strings.TrimSpace(c.Name)
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	if c.Provider == "" {
		c.Provider = inferCloudProvider(c.Host, c.Engine)
	}
	if c.Port <= 0 {
		c.Port = CloudDefaultPort(c.Engine)
	}
	c.SSLMode = strings.ToLower(strings.TrimSpace(c.SSLMode))
	if c.SSLMode == "" {
		switch c.Engine {
		case "postgresql":
			c.SSLMode = "require"
		case "mssql":
			c.SSLMode = "skip-verify"
		default:
			c.SSLMode = "preferred"
		}
	}
	if c.Name == "" {
		if c.Host != "" {
			c.Name = c.Host
		} else {
			c.Name = strings.ToUpper(c.Engine) + " Cloud"
		}
	}
	return c
}

func inferCloudProvider(host, engine string) string {
	low := strings.ToLower(host)
	switch {
	case strings.Contains(low, "databaseasp") || strings.Contains(low, "monsterasp") || strings.Contains(low, "smarterasp") || strings.Contains(low, "site4now") || strings.Contains(low, "somee"):
		return "monsterasp"
	case strings.Contains(low, "supabase"):
		return "supabase"
	case strings.Contains(low, "neon.tech"):
		return "neon"
	case strings.Contains(low, "tidbcloud"):
		return "tidb"
	case strings.Contains(low, "aivencloud"):
		return "aiven"
	case strings.Contains(low, "psdb.cloud") || strings.Contains(low, "planetscale"):
		return "planetscale"
	case strings.Contains(low, "rds.amazonaws.com"):
		return "rds"
	case strings.Contains(low, "database.windows.net") || strings.Contains(low, "azure.com"):
		return "azure"
	case strings.Contains(low, "upstash") || strings.Contains(low, "redislabs") || strings.Contains(low, "redis.com"):
		return "rediscloud"
	case engine == "mssql":
		return "monsterasp"
	case engine == "mysql" || engine == "mariadb":
		return "cpanel"
	default:
		return "cloud"
	}
}

func (c CloudConnection) ToEngineSettings() EngineSettings {
	nc := c.Normalized()
	return EngineSettings{
		Enabled:   true,
		Host:      nc.Host,
		Port:      nc.Port,
		User:      nc.User,
		Password:  nc.Password,
		SSLMode:   nc.SSLMode,
		DefaultDB: nc.Database,
	}
}

// ParseCloudURI parses standard DB URIs (mysql://, postgresql://, sqlserver://, rediss://)
// as well as ADO.NET / MonsterASP / SmarterASP connection strings (Server=...;Database=...;User Id=...;Password=...;).
func ParseCloudURI(raw string) (*CloudConnection, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("paste a connection URI or ADO.NET connection string")
	}

	if !strings.Contains(raw, "://") && strings.Contains(raw, "=") && strings.Contains(raw, ";") {
		kv := map[string]string{}
		for _, part := range strings.Split(raw, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			eq := strings.IndexByte(part, '=')
			if eq <= 0 {
				continue
			}
			k := strings.ToLower(strings.TrimSpace(part[:eq]))
			v := strings.TrimSpace(part[eq+1:])
			kv[k] = v
		}
		srv := kv["server"]
		if srv == "" {
			srv = kv["data source"]
		}
		if srv == "" {
			srv = kv["host"]
		}
		if srv == "" {
			return nil, errors.New("could not find Server= or Host= in connection string")
		}
		engine := "mssql"
		if kv["host"] != "" || (kv["uid"] != "" && kv["port"] == "3306") {
			engine = "mysql"
		}
		if kv["username"] != "" && kv["port"] == "5432" {
			engine = "postgresql"
		}
		host := srv
		port := CloudDefaultPort(engine)
		if pStr := kv["port"]; pStr != "" {
			if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
				port = p
			}
		} else if idx := strings.LastIndex(srv, ","); idx > 0 {
			host = strings.TrimSpace(srv[:idx])
			if p, err := strconv.Atoi(strings.TrimSpace(srv[idx+1:])); err == nil && p > 0 {
				port = p
			}
		} else if idx := strings.LastIndex(srv, ":"); idx > 0 {
			host = strings.TrimSpace(srv[:idx])
			if p, err := strconv.Atoi(strings.TrimSpace(srv[idx+1:])); err == nil && p > 0 {
				port = p
			}
		}
		user := kv["user id"]
		if user == "" {
			user = kv["uid"]
		}
		if user == "" {
			user = kv["user"]
		}
		if user == "" {
			user = kv["username"]
		}
		pass := kv["password"]
		if pass == "" {
			pass = kv["pwd"]
		}
		db := kv["database"]
		if db == "" {
			db = kv["initial catalog"]
		}
		sslMode := "skip-verify"
		if sm := strings.ToLower(kv["ssl mode"]); sm != "" {
			sslMode = sm
		}
		c := CloudConnection{
			Name:     fallbackStr(db, host),
			Engine:   engine,
			Host:     host,
			Port:     port,
			Database: db,
			User:     user,
			Password: pass,
			SSLMode:  sslMode,
		}.Normalized()
		return &c, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URI: %w", err)
	}
	if u.Hostname() == "" {
		return nil, errors.New("URI is missing host name")
	}
	scheme := strings.ToLower(u.Scheme)
	engine := normalizeCloudEngine(scheme)
	port := CloudDefaultPort(engine)
	if pStr := u.Port(); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			port = p
		}
	}
	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	db := strings.TrimPrefix(u.Path, "/")
	q := u.Query()
	if db == "" {
		db = q.Get("database")
	}
	sslMode := strings.ToLower(q.Get("sslmode"))
	if sslMode == "" {
		sslMode = strings.ToLower(q.Get("ssl-mode"))
	}
	if sslMode == "" && scheme == "rediss" {
		sslMode = "require"
	}
	c := CloudConnection{
		Name:     fallbackStr(db, u.Hostname()),
		Engine:   engine,
		Host:     u.Hostname(),
		Port:     port,
		Database: db,
		User:     user,
		Password: pass,
		SSLMode:  sslMode,
	}.Normalized()
	return &c, nil
}

// TestCloudConnection opens a live connection to the configured Cloud/cPanel/MonsterASP database,
// measures round-trip latency, fetches server version info, and lists accessible databases.
func TestCloudConnection(c CloudConnection) (*CloudTestResult, error) {
	nc := c.Normalized()
	if nc.Host == "" {
		return nil, errors.New("host / server address is required")
	}
	es := nc.ToEngineSettings()
	start := time.Now()

	if nc.Engine == "redis" {
		conn, err := dialRedis(es, 0)
		if err != nil {
			return &CloudTestResult{OK: false, LatencyMs: time.Since(start).Milliseconds(), Message: err.Error()}, err
		}
		defer conn.Close()
		pong, err := conn.Do("PING")
		lat := time.Since(start).Milliseconds()
		if err != nil {
			return &CloudTestResult{OK: false, LatencyMs: lat, Message: err.Error()}, err
		}
		ver := ""
		if info, ierr := conn.Do("INFO", "server"); ierr == nil {
			for _, line := range strings.Split(fmt.Sprint(info), "\n") {
				if strings.HasPrefix(line, "redis_version:") {
					ver = "Redis " + strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
					break
				}
			}
		}
		if ver == "" {
			ver = fmt.Sprintf("Redis (%v)", pong)
		}
		return &CloudTestResult{
			OK:         true,
			LatencyMs:  lat,
			ServerInfo: ver,
			Message:    fmt.Sprintf("Connected to %s:%d in %d ms (%s)", nc.Host, nc.Port, lat, ver),
		}, nil
	}

	dbs, err := ListDatabases(nc.Engine, es)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return &CloudTestResult{OK: false, LatencyMs: lat, Message: err.Error()}, err
	}
	targetDB := nc.Database
	if targetDB == "" && len(dbs) > 0 {
		targetDB = dbs[0]
	}
	ver := ""
	verQ := "SELECT VERSION()"
	if nc.Engine == "mssql" {
		verQ = "SET NOCOUNT ON; SELECT @@VERSION"
	}
	if res, qerr := RunQuery(nc.Engine, es, targetDB, verQ); qerr == nil && len(res.Rows) > 0 && len(res.Rows[0]) > 0 && res.Rows[0][0] != nil {
		ver = strings.Split(strings.TrimSpace(*res.Rows[0][0]), "\n")[0]
		if len(ver) > 80 {
			ver = ver[:80] + "..."
		}
	}
	msg := fmt.Sprintf("Connected to %s:%d in %d ms (%d DB accessible)", nc.Host, nc.Port, lat, len(dbs))
	if ver != "" {
		msg = fmt.Sprintf("Connected in %d ms · %s", lat, ver)
	}
	return &CloudTestResult{
		OK:         true,
		LatencyMs:  lat,
		ServerInfo: ver,
		Databases:  dbs,
		Message:    msg,
	}, nil
}

// CloudSyncDatabase exports srcDB from srcEngine/srcCfg into a temporary SQL script
// and imports it into dstDB on dstEngine/dstCfg (supporting Local -> Cloud and Cloud -> Local).
func CloudSyncDatabase(p *Paths, srcEngine string, srcCfg EngineSettings, srcDB, dstEngine string, dstCfg EngineSettings, dstDB string) (string, error) {
	srcDB = strings.TrimSpace(srcDB)
	dstDB = strings.TrimSpace(dstDB)
	if srcDB == "" {
		return "", errors.New("source database is required")
	}
	if dstDB == "" {
		dstDB = srcDB
	}
	if srcEngine == "redis" || dstEngine == "redis" {
		return "", errors.New("one-click SQL sync is supported for MySQL, MariaDB, PostgreSQL, and MS SQL Server databases")
	}

	tmpDir := filepath.Join(os.TempDir(), "DariusDB-CloudSync")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	dumpPath := filepath.Join(tmpDir, fmt.Sprintf("sync_%s_%d.sql", srcDB, time.Now().Unix()))
	defer os.Remove(dumpPath)

	tables, err := ListTables(srcEngine, srcCfg, srcDB)
	if err != nil {
		return "", fmt.Errorf("failed reading source tables: %w", err)
	}
	tblNames := make([]string, 0, len(tables))
	for _, t := range tables {
		if !strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			tblNames = append(tblNames, t.Name)
		}
	}
	if len(tblNames) == 0 {
		return "", fmt.Errorf("source database %q has no base tables to sync", srcDB)
	}
	if err := ExportDatabaseSelection(p, srcEngine, srcCfg, srcDB, tblNames, "sql", true, true, dumpPath); err != nil {
		return "", fmt.Errorf("export failed: %w", err)
	}
	if b, err := os.ReadFile(dumpPath); err == nil {
		text := string(b)
		if srcDB != dstDB && srcEngine == "mssql" {
			text = strings.Replace(text, fmt.Sprintf("USE %s;\r\nGO\r\n", mssqlBracket(srcDB)), "", 1)
			_ = os.WriteFile(dumpPath, []byte(text), 0o644)
		}
	}

	restored, err := RestoreDatabase(p, dstEngine, dstCfg, dumpPath, dstDB)
	if err != nil {
		return "", fmt.Errorf("import into %q failed: %w", dstDB, err)
	}
	return fmt.Sprintf("Synced %d table(s) from %q to %q", len(tblNames), srcDB, restored), nil
}
