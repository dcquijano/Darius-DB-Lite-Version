//go:build windows

package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
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

	// Handle DB URIs whose password contains unescaped '@', '#', or '%' (common in cPanel / MonsterASP passwords).
	var parsedUser, parsedPass string
	hasCustomUserInfo := false
	parseTarget := raw
	if schemeSep := strings.Index(raw, "://"); schemeSep > 0 {
		schemePart := raw[:schemeSep]
		restPart := raw[schemeSep+3:]
		if lastAt := strings.LastIndexByte(restPart, '@'); lastAt > 0 {
			userInfo := restPart[:lastAt]
			hostPart := restPart[lastAt+1:]
			if colon := strings.IndexByte(userInfo, ':'); colon >= 0 {
				parsedUser = userInfo[:colon]
				parsedPass = userInfo[colon+1:]
			} else {
				parsedUser = userInfo
			}
			// PathUnescape preserves literal '+' (unlike QueryUnescape) and decodes %7B -> '{', etc.
			if uDec, uErr := url.PathUnescape(parsedUser); uErr == nil {
				parsedUser = uDec
			}
			if pDec, pErr := url.PathUnescape(parsedPass); pErr == nil {
				parsedPass = pDec
			}
			hasCustomUserInfo = true
			parseTarget = schemePart + "://" + hostPart
		}
	}

	u, err := url.Parse(parseTarget)
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
	var panelURL string
	if (engine == "mysql" || engine == "mariadb") && (port == 2082 || port == 2083 || port == 2086 || port == 2087) {
		schemePanel := "https"
		if port == 2082 || port == 2086 {
			schemePanel = "http"
		}
		panelURL = fmt.Sprintf("%s://%s:%d", schemePanel, u.Hostname(), port)
		port = 3306
	}
	var user, pass string
	if hasCustomUserInfo {
		user, pass = parsedUser, parsedPass
	} else if u.User != nil {
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
		PanelURL: panelURL,
	}.Normalized()
	return &c, nil
}

// TestCloudConnection opens a live connection to the configured Cloud/cPanel/MonsterASP database,
// measures round-trip latency, fetches server version info, and lists accessible databases.
func TestCloudConnection(c CloudConnection) (*CloudTestResult, error) {
	return TestCloudConnectionWithProgress(c, nil)
}

// TestCloudConnectionWithProgress runs TestCloudConnection while reporting real-time
// stage and percentage updates to emit.
func TestCloudConnectionWithProgress(c CloudConnection, emit func(CloudSyncProgress)) (*CloudTestResult, error) {
	report := func(pct int, state, stage, detail string) {
		if emit != nil {
			emit(CloudSyncProgress{
				Percent: pct,
				State:   state,
				Stage:   stage,
				Detail:  detail,
			})
		}
	}

	report(10, "running", "Validating connection parameters...", fmt.Sprintf("Checking %s host & port settings", strings.ToUpper(strings.TrimSpace(c.Engine))))
	nc := c.Normalized()
	if nc.Host == "" {
		err := errors.New("host / server address is required")
		report(100, "error", "Validation failed", err.Error())
		return nil, err
	}

	// Detect common cPanel web port mistakes (cPanel web UI uses 2082/2083, WHM uses 2086/2087)
	if nc.Port == 2082 || nc.Port == 2083 || nc.Port == 2086 || nc.Port == 2087 {
		if nc.Engine == "mysql" || nc.Engine == "mariadb" {
			msg := fmt.Sprintf("Port %d is the cPanel web login port, not the MySQL database port. In cPanel, MySQL listens on port 3306. Change Port to 3306.", nc.Port)
			report(100, "error", fmt.Sprintf("Port %d is cPanel web login", nc.Port), msg)
			return &CloudTestResult{OK: false, LatencyMs: 0, Message: msg}, errors.New(msg)
		}
		if nc.Engine == "mssql" {
			msg := fmt.Sprintf("Port %d is the cPanel web login port (HTTP/HTTPS), not an MS SQL Server port. MS SQL Server uses port 1433 by default. Change Port to 1433 (or the SQL Server's actual TCP port).", nc.Port)
			report(100, "error", fmt.Sprintf("Port %d is cPanel web login — not MS SQL", nc.Port), msg)
			return &CloudTestResult{OK: false, LatencyMs: 0, Message: msg}, errors.New(msg)
		}
	}

	es := nc.ToEngineSettings()
	start := time.Now()
	addr := net.JoinHostPort(nc.Host, strconv.Itoa(nc.Port))

	report(25, "running", fmt.Sprintf("Testing TCP reachability (%s)...", addr), "Checking firewall / network route to remote server")
	tcpConn, dialErr := net.DialTimeout("tcp", addr, 6*time.Second)
	if dialErr != nil {
		lat := time.Since(start).Milliseconds()
		msg := dialErr.Error()
		if strings.Contains(strings.ToLower(msg), "timeout") {
			msg = fmt.Sprintf("cannot connect to %s on port %d: %s — Server or firewall did not respond. If using cPanel/hosting, check if cPHulk/CSF firewall temporarily rate-limited your IP after failed logins (wait 5–15 min) or if outbound port %d is blocked by your ISP/router.", nc.Engine, nc.Port, msg, nc.Port)
		}
		report(100, "error", fmt.Sprintf("TCP connection failed (%d ms)", lat), msg)
		return &CloudTestResult{OK: false, LatencyMs: lat, Message: msg}, errors.New(msg)
	}
	_ = tcpConn.Close()

	report(50, "running", fmt.Sprintf("Authenticating (%s, TLS: %s)...", strings.ToUpper(nc.Engine), nc.SSLMode), fmt.Sprintf("TCP reachable in %d ms — verifying credentials", time.Since(start).Milliseconds()))

	if nc.Engine == "redis" {
		conn, err := dialRedis(es, 0)
		if err != nil {
			lat := time.Since(start).Milliseconds()
			report(100, "error", fmt.Sprintf("Redis authentication failed (%d ms)", lat), err.Error())
			return &CloudTestResult{OK: false, LatencyMs: lat, Message: err.Error()}, err
		}
		defer conn.Close()
		report(75, "running", "Sending Redis PING...", "Verifying live session")
		pong, err := conn.Do("PING")
		lat := time.Since(start).Milliseconds()
		if err != nil {
			report(100, "error", fmt.Sprintf("Redis PING failed (%d ms)", lat), err.Error())
			return &CloudTestResult{OK: false, LatencyMs: lat, Message: err.Error()}, err
		}
		report(90, "running", "Reading Redis server version...", "Fetching server metadata")
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
		msg := fmt.Sprintf("Connected to %s:%d in %d ms (%s)", nc.Host, nc.Port, lat, ver)
		report(100, "done", fmt.Sprintf("Connected in %d ms", lat), msg)
		return &CloudTestResult{
			OK:         true,
			LatencyMs:  lat,
			ServerInfo: ver,
			Message:    msg,
		}, nil
	}

	report(60, "running", "Authenticating & listing accessible databases...", fmt.Sprintf("Opening %s database session on %s", strings.ToUpper(nc.Engine), addr))
	dbs, err := ListDatabases(nc.Engine, es)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		errStr := err.Error()
		lowErr := strings.ToLower(errStr)
		if strings.Contains(errStr, "1045") || strings.Contains(lowErr, "access denied") {
			clientIP := ""
			if parts := strings.Split(errStr, "@'"); len(parts) > 1 {
				clientIP = strings.Split(parts[1], "'")[0]
			}
			hint := "In cPanel → 'Remote MySQL®', add '%' wildcard (or your public IP) to allowed hosts. Also verify username and password have ALL PRIVILEGES in cPanel → 'MySQL® Databases'."
			if clientIP != "" {
				hint = fmt.Sprintf("In cPanel → 'Remote MySQL®', add '%%' wildcard (or client IP '%s') to allowed hosts. Also verify user and password have ALL PRIVILEGES in cPanel → 'MySQL® Databases'.", clientIP)
			}
			errStr = fmt.Sprintf("%s (%s)", errStr, hint)
		} else if strings.Contains(lowErr, "timeout") {
			errStr = fmt.Sprintf("%s — Server or firewall timed out on port %d. If using cPanel/hosting, cPHulk or CSF firewall may have temporarily blocked your IP after repeated failed logins (wait 5–15 min) or your ISP/router is blocking port %d.", errStr, nc.Port, nc.Port)
		}
		report(100, "error", fmt.Sprintf("Database handshake failed (%d ms)", lat), errStr)
		return &CloudTestResult{OK: false, LatencyMs: lat, Message: errStr}, errors.New(errStr)
	}
	targetDB := nc.Database
	if targetDB == "" && len(dbs) > 0 {
		targetDB = dbs[0]
	}
	report(85, "running", "Querying remote server version...", fmt.Sprintf("Found %d database(s) — reading server version", len(dbs)))
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
	report(100, "done", fmt.Sprintf("Connected in %d ms", lat), msg)
	return &CloudTestResult{
		OK:         true,
		LatencyMs:  lat,
		ServerInfo: ver,
		Databases:  dbs,
		Message:    msg,
	}, nil
}

type CloudSyncProgress struct {
	Percent int    `json:"percent"`
	State   string `json:"state"` // running | done | error
	Stage   string `json:"stage"`
	Detail  string `json:"detail"`
}

// ErrSyncCancelled is returned when a running database sync is cancelled by the user.
var ErrSyncCancelled = errors.New("database sync cancelled by user")

// CloudSyncDatabase exports srcDB from srcEngine/srcCfg into a temporary SQL script
// and imports it into dstDB on dstEngine/dstCfg (supporting Local -> Cloud and Cloud -> Local).
func CloudSyncDatabase(p *Paths, srcEngine string, srcCfg EngineSettings, srcDB, dstEngine string, dstCfg EngineSettings, dstDB string) (string, error) {
	return CloudSyncDatabaseWithContext(context.Background(), p, srcEngine, srcCfg, srcDB, dstEngine, dstCfg, dstDB, nil)
}

// CloudSyncDatabaseWithProgress runs CloudSyncDatabase while emitting stage & percentage progress updates.
func CloudSyncDatabaseWithProgress(p *Paths, srcEngine string, srcCfg EngineSettings, srcDB, dstEngine string, dstCfg EngineSettings, dstDB string, onProgress func(CloudSyncProgress)) (string, error) {
	return CloudSyncDatabaseWithContext(context.Background(), p, srcEngine, srcCfg, srcDB, dstEngine, dstCfg, dstDB, onProgress)
}

// CloudSyncDatabaseWithContext runs CloudSyncDatabaseWithProgress and aborts promptly when ctx is cancelled.
func CloudSyncDatabaseWithContext(ctx context.Context, p *Paths, srcEngine string, srcCfg EngineSettings, srcDB, dstEngine string, dstCfg EngineSettings, dstDB string, onProgress func(CloudSyncProgress)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	lastPct := 0
	report := func(pct int, state, stage, detail string) {
		if pct > lastPct {
			lastPct = pct
		} else if state == "running" {
			pct = lastPct
		}
		if onProgress != nil {
			onProgress(CloudSyncProgress{Percent: pct, State: state, Stage: stage, Detail: detail})
		}
	}
	isCancelled := func(err error) bool {
		return errors.Is(err, context.Canceled) || errors.Is(err, ErrSyncCancelled) || ctx.Err() != nil
	}
	abortCancelled := func() (string, error) {
		report(lastPct, "cancelled", "Sync Cancelled", "Database sync was cancelled by user.")
		return "", ErrSyncCancelled
	}

	if ctx.Err() != nil {
		return abortCancelled()
	}

	srcDB = strings.TrimSpace(srcDB)
	dstDB = strings.TrimSpace(dstDB)
	if srcDB == "" {
		err := errors.New("source database is required")
		report(0, "error", "Sync Failed", err.Error())
		return "", err
	}
	if dstDB == "" {
		dstDB = srcDB
	}
	if srcEngine == "redis" || dstEngine == "redis" {
		err := errors.New("one-click SQL sync is supported for MySQL, MariaDB, PostgreSQL, and MS SQL Server databases")
		report(0, "error", "Sync Failed", err.Error())
		return "", err
	}

	tmpDir := filepath.Join(os.TempDir(), "DariusDB-CloudSync")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		report(0, "error", "Sync Failed", err.Error())
		return "", err
	}
	dumpPath := filepath.Join(tmpDir, fmt.Sprintf("sync_%s_%d.sql", srcDB, time.Now().Unix()))
	defer os.Remove(dumpPath)

	// Early port sanity check for mssql cloud source/destination using cPanel web ports.
	// go-sqlcmd TDS parser receives HTTP bytes from these ports and reports "invalid packet size".
	for _, side := range []struct {
		eng  string
		cfg  EngineSettings
		name string
	}{
		{srcEngine, srcCfg, "source"},
		{dstEngine, dstCfg, "destination"},
	} {
		if side.eng == "mssql" && (side.cfg.Host != "" &&
			!strings.EqualFold(side.cfg.Host, "127.0.0.1") &&
			!strings.EqualFold(side.cfg.Host, "localhost")) &&
			(side.cfg.Port == 2082 || side.cfg.Port == 2083 || side.cfg.Port == 2086 || side.cfg.Port == 2087) {
			outErr := fmt.Errorf("sync failed: port %d on the %s MS SQL profile is a cPanel web login port (HTTP/HTTPS), not an MS SQL Server port. MS SQL Server listens on port 1433 by default. Edit the Cloud Profile and change Port to 1433.", side.cfg.Port, side.name)
			report(0, "error", "Sync Failed — Wrong Port", outErr.Error())
			return "", outErr
		}
	}

	report(5, "running", "Connecting to source...", fmt.Sprintf("Inspecting base tables in %s (%s)...", srcDB, strings.ToUpper(srcEngine)))
	tables, err := ListTables(srcEngine, srcCfg, srcDB)
	if ctx.Err() != nil {
		return abortCancelled()
	}
	if err != nil {
		errMsg := err.Error()
		// Enrich "invalid packet size" errors with a human-readable explanation
		if strings.Contains(strings.ToLower(errMsg), "invalid packet size") {
			errMsg = fmt.Sprintf("%s — This usually means the port on your MS SQL Cloud Profile is not an actual MS SQL Server port (MS SQL uses port 1433). If using cPanel, check that port 1433 is open and your host supports remote SQL Server connections. Edit the Cloud Profile and verify the Port.", errMsg)
		}
		outErr := fmt.Errorf("failed reading source tables: %s", errMsg)
		report(lastPct, "error", "Sync Failed", outErr.Error())
		return "", outErr
	}
	tblNames := make([]string, 0, len(tables))
	for _, t := range tables {
		if !strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			tblNames = append(tblNames, t.Name)
		}
	}
	if len(tblNames) == 0 {
		outErr := fmt.Errorf("source database %q has no base tables to sync", srcDB)
		report(lastPct, "error", "Sync Failed", outErr.Error())
		return "", outErr
	}

	report(15, "running", "Exporting source tables...", fmt.Sprintf("Found %d base table(s) in %q — exporting schema & data...", len(tblNames), srcDB))

	expDone := make(chan struct{})
	expStart := time.Now()
	go func() {
		tick := time.NewTicker(350 * time.Millisecond)
		defer tick.Stop()
		steps := 0
		for {
			select {
			case <-expDone:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				steps++
				var sz int64
				if st, serr := os.Stat(dumpPath); serr == nil {
					sz = st.Size()
				}
				bump := steps * 2
				if sz > 0 {
					bump += int(sz / (64 * 1024))
				}
				if bump > 36 {
					bump = 36
				}
				elapsed := int(time.Since(expStart).Seconds())
				report(16+bump, "running", "Exporting source tables...", fmt.Sprintf("Exporting %d table(s) from %q (%s written · %ds)...", len(tblNames), srcDB, formatByteSize(sz), elapsed))
			}
		}
	}()

	err = ExportDatabaseSelectionWithContext(ctx, p, srcEngine, srcCfg, srcDB, tblNames, "sql", true, true, dumpPath, func(bp BackupProgress) {
		if ctx.Err() != nil {
			return
		}
		if bp.State == "running" {
			pct := 16 + (bp.Percent * 38 / 100)
			if pct > 54 {
				pct = 54
			}
			report(pct, "running", bp.Stage, bp.Detail)
		}
	})
	close(expDone)
	if isCancelled(err) {
		return abortCancelled()
	}
	if err != nil {
		outErr := fmt.Errorf("export failed: %w", err)
		report(lastPct, "error", "Export Failed", outErr.Error())
		return "", outErr
	}

	var dumpSize int64
	if b, err := os.ReadFile(dumpPath); err == nil {
		text := string(b)
		if srcDB != dstDB && srcEngine == "mssql" {
			text = strings.Replace(text, fmt.Sprintf("USE %s;\r\nGO\r\n", mssqlBracket(srcDB)), "", 1)
		}
		if dstEngine == "mssql" {
			dstIdentity := fetchMSSQLTargetIdentityMapCtx(ctx, dstCfg, dstDB)
			if ctx.Err() != nil {
				return abortCancelled()
			}
			text = reconcileMSSQLIdentityInserts(text, dstIdentity)
			text = wrapMSSQLSyncRefresh(text, tables, dstDB)
		}
		_ = os.WriteFile(dumpPath, []byte(text), 0o644)
		dumpSize = int64(len(text))
	} else if st, serr := os.Stat(dumpPath); serr == nil {
		dumpSize = st.Size()
	}

	if ctx.Err() != nil {
		return abortCancelled()
	}
	report(55, "running", "Uploading & importing...", fmt.Sprintf("Exported %s (%d table(s)). Importing into %q (%s)...", formatByteSize(dumpSize), len(tblNames), dstDB, strings.ToUpper(dstEngine)))

	impDone := make(chan struct{})
	impStart := time.Now()
	go func() {
		tick := time.NewTicker(400 * time.Millisecond)
		defer tick.Stop()
		steps := 0
		for {
			select {
			case <-impDone:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				steps++
				bump := steps * 2
				if bump > 38 {
					bump = 38
				}
				elapsed := int(time.Since(impStart).Seconds())
				report(56+bump, "running", "Importing into target...", fmt.Sprintf("Applying %s across %d table(s) to %q (%ds)...", formatByteSize(dumpSize), len(tblNames), dstDB, elapsed))
			}
		}
	}()

	restored, err := RestoreDatabaseWithContext(ctx, p, dstEngine, dstCfg, dumpPath, dstDB, func(done, total int64) {
		if ctx.Err() != nil {
			return
		}
		if total > 0 {
			pct := 56 + int(done*42/total)
			if pct > 98 {
				pct = 98
			}
			report(pct, "running", "Importing into target...", fmt.Sprintf("Importing %s / %s into %q...", formatByteSize(done), formatByteSize(total), dstDB))
		}
	})
	close(impDone)
	if isCancelled(err) {
		return abortCancelled()
	}
	if err != nil {
		outErr := fmt.Errorf("import into %q failed: %w", dstDB, err)
		report(lastPct, "error", "Import Failed", outErr.Error())
		return "", outErr
	}
	msg := fmt.Sprintf("Synced %d table(s) from %q to %q", len(tblNames), srcDB, restored)
	report(100, "done", "Sync Complete!", msg)
	return msg, nil
}

func formatByteSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024.0)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024.0*1024.0))
}

// wrapMSSQLSyncRefresh disables constraints, clears existing synced tables in an FK-safe
// multi-pass loop before importing, and re-enables constraints afterward so repeated
// Local <-> Cloud syncs never collide on primary keys or foreign keys.
func wrapMSSQLSyncRefresh(sqlText string, tables []TableInfo, dstDB string) string {
	var idents []string
	for _, t := range tables {
		if strings.Contains(strings.ToUpper(t.Kind), "VIEW") {
			continue
		}
		tbl := t.Name
		if idx := strings.IndexByte(tbl, '.'); idx >= 0 {
			tbl = tbl[idx+1:]
		}
		sch := fallbackStr(t.Schema, "dbo")
		idents = append(idents, mssqlBracket(sch)+"."+mssqlBracket(tbl))
	}
	if len(idents) == 0 {
		return sqlText
	}
	var pre strings.Builder
	pre.WriteString("SET NOCOUNT ON;\r\n")
	if dstDB != "" {
		fmt.Fprintf(&pre, "USE %s;\r\nGO\r\n", mssqlBracket(dstDB))
	}
	for _, id := range idents {
		fmt.Fprintf(&pre, "IF OBJECT_ID(N'%s', N'U') IS NOT NULL BEGIN TRY ALTER TABLE %s NOCHECK CONSTRAINT ALL; DISABLE TRIGGER ALL ON %s; END TRY BEGIN CATCH END CATCH;\r\n", id, id, id)
	}
	pre.WriteString("GO\r\n")
	maxPasses := len(idents)
	if maxPasses > 8 {
		maxPasses = 8
	}
	for pass := 0; pass < maxPasses; pass++ {
		for i := len(idents) - 1; i >= 0; i-- {
			id := idents[i]
			fmt.Fprintf(&pre, "IF OBJECT_ID(N'%s', N'U') IS NOT NULL BEGIN TRY DELETE FROM %s; END TRY BEGIN CATCH END CATCH;\r\n", id, id)
		}
	}
	pre.WriteString("GO\r\n")

	var post strings.Builder
	post.WriteString("\r\nGO\r\n")
	for _, id := range idents {
		fmt.Fprintf(&post, "IF OBJECT_ID(N'%s', N'U') IS NOT NULL BEGIN TRY ENABLE TRIGGER ALL ON %s; ALTER TABLE %s WITH CHECK CHECK CONSTRAINT ALL; END TRY BEGIN CATCH END CATCH;\r\n", id, id, id)
	}
	post.WriteString("GO\r\n")
	return pre.String() + sqlText + post.String()
}

func fetchMSSQLTargetIdentityMap(s EngineSettings, dbName string) map[string]bool {
	return fetchMSSQLTargetIdentityMapCtx(context.Background(), s, dbName)
}

func fetchMSSQLTargetIdentityMapCtx(ctx context.Context, s EngineSettings, dbName string) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(dbName) == "" {
		return out
	}
	q := "SET NOCOUNT ON; SELECT QUOTENAME(s.name) + N'.' + QUOTENAME(o.name), ISNULL(OBJECTPROPERTY(o.object_id, N'TableHasIdentity'), 0) " +
		"FROM sys.objects o JOIN sys.schemas s ON o.schema_id = s.schema_id WHERE o.type = N'U';"
	res, err := mssqlRunQueryWithLimitCtx(ctx, s, dbName, q, maxShowRows)
	if err != nil {
		return out
	}
	for _, r := range res.Rows {
		name := strings.ToLower(strings.TrimSpace(valAt(r, 0)))
		if name != "" {
			out[name] = valAt(r, 1) == "1"
		}
	}
	return out
}

// reconcileMSSQLIdentityInserts matches SET IDENTITY_INSERT statements against the target DB's
// actual existing tables: if a target table already exists WITHOUT an identity column (Msg 8106),
// SET IDENTITY_INSERT is omitted; if a target table exists WITH an identity column and the script
// didn't wrap INSERTs in SET IDENTITY_INSERT ON/OFF (Msg 544), it adds the wrapper automatically.
func reconcileMSSQLIdentityInserts(sqlText string, dstHasIdentity map[string]bool) string {
	if len(dstHasIdentity) == 0 {
		return sqlText
	}
	eol := "\r\n"
	if !strings.Contains(sqlText, "\r\n") {
		eol = "\n"
	}
	lines := strings.Split(strings.ReplaceAll(sqlText, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	activeInjected := ""
	explicitOn := ""

	closeInjected := func() {
		if activeInjected != "" {
			out = append(out, fmt.Sprintf("SET IDENTITY_INSERT %s OFF;", activeInjected))
			activeInjected = ""
		}
	}

	for _, line := range lines {
		tr := strings.TrimSpace(line)
		low := strings.ToLower(tr)

		if strings.HasPrefix(low, "set identity_insert ") && (strings.HasSuffix(low, " on;") || strings.HasSuffix(low, " off;")) {
			isOn := strings.HasSuffix(low, " on;")
			rest := strings.TrimSpace(tr[len("SET IDENTITY_INSERT "):])
			if isOn {
				rest = strings.TrimSpace(rest[:len(rest)-3]) // strip ON;
			} else {
				rest = strings.TrimSpace(rest[:len(rest)-4]) // strip OFF;
			}
			key := strings.ToLower(rest)
			if existsHasID, exists := dstHasIdentity[key]; exists && !existsHasID {
				// Target table exists WITHOUT identity -> drop SET IDENTITY_INSERT to avoid Msg 8106
				continue
			}
			if isOn {
				closeInjected()
				explicitOn = key
			} else if explicitOn == key {
				explicitOn = ""
			}
			out = append(out, line)
			continue
		}

		if strings.HasPrefix(low, "insert into ") {
			after := strings.TrimSpace(tr[len("INSERT INTO "):])
			endIdx := strings.IndexAny(after, " (")
			if endIdx > 0 {
				ident := after[:endIdx]
				key := strings.ToLower(ident)
				if existsHasID, exists := dstHasIdentity[key]; exists && existsHasID && explicitOn != key {
					if activeInjected != "" && !strings.EqualFold(activeInjected, ident) {
						closeInjected()
					}
					if activeInjected == "" {
						out = append(out, fmt.Sprintf("SET IDENTITY_INSERT %s ON;", ident))
						activeInjected = ident
					}
					out = append(out, line)
					continue
				}
			}
		}

		closeInjected()
		out = append(out, line)
	}
	closeInjected()
	return strings.Join(out, eol)
}
