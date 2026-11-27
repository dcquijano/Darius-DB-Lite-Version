package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- MariaDB / MySQL

type mysqlFamily struct{ id, name string }

func (m *mysqlFamily) ID() string   { return m.id }
func (m *mysqlFamily) Name() string { return m.name }
func (m *mysqlFamily) IsSQL() bool  { return true }

func (m *mysqlFamily) serverExe(inst Installation) string {
	if m.id == "mariadb" {
		if p := filepath.Join(inst.Dir, "bin", "mariadbd.exe"); fileExists(p) {
			return p
		}
	}
	return filepath.Join(inst.Dir, "bin", "mysqld.exe")
}

func (m *mysqlFamily) Detect(dir string) bool {
	return fileExists(filepath.Join(dir, "bin", "mysqld.exe")) || fileExists(filepath.Join(dir, "bin", "mariadbd.exe"))
}

func (m *mysqlFamily) ClientBinDir(inst Installation) string { return filepath.Join(inst.Dir, "bin") }

func (m *mysqlFamily) DataDir(p *Paths, inst Installation) string {
	// one data folder per major.minor (e.g. data/mariadb-11.4) so patch upgrades keep data
	return filepath.Join(p.Data, m.id+"-"+versionPrefix(inst.Version, 2))
}

func (m *mysqlFamily) ConfigFile(p *Paths, inst Installation) string {
	return filepath.Join(p.EngineEtc(m.id), "my.ini")
}

const mariadbIni = `# Darius DB - MariaDB Full-Stack Configuration (UTF8MB4 + InnoDB Optimized for 20,000+ records)
# Port, basedir and datadir are passed by Darius DB on the command line.
[mysqld]
bind-address = 127.0.0.1
character-set-server = utf8mb4
collation-server = utf8mb4_unicode_ci
max_allowed_packet = 128M
innodb_buffer_pool_size = 256M
innodb_log_file_size = 64M
innodb_flush_log_at_trx_commit = 2
innodb_file_per_table = 1
max_connections = 200
slow_query_log = 1
long_query_time = 2

[client]
default-character-set = utf8mb4
`

const mysqlIni = `# Darius DB - MySQL Full-Stack Configuration (UTF8MB4 + InnoDB Optimized for 20,000+ records)
# Port, basedir and datadir are passed by Darius DB on the command line.
[mysqld]
bind-address = 127.0.0.1
character-set-server = utf8mb4
collation-server = utf8mb4_unicode_ci
max_allowed_packet = 128M
innodb_buffer_pool_size = 256M
innodb_flush_log_at_trx_commit = 2
innodb_file_per_table = 1
max_connections = 200
slow_query_log = 1
long_query_time = 2
# disable the X Protocol listener (port 33060) to keep things light
mysqlx = OFF

[client]
default-character-set = utf8mb4
`

func (m *mysqlFamily) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	cfg := m.ConfigFile(p, inst)
	tpl := mysqlIni
	if m.id == "mariadb" {
		tpl = mariadbIni
	}
	if b, err := os.ReadFile(cfg); err == nil && (strings.Contains(string(b), "skip-name-resolve") || strings.Contains(string(b), "innodb_buffer_pool_size = 128M")) {
		_ = os.WriteFile(cfg, []byte(strings.ReplaceAll(tpl, "\n", "\r\n")), 0o644)
	}
	if err := writeIfMissing(cfg, tpl); err != nil {
		return err
	}
	data := m.DataDir(p, inst)
	if dirExists(filepath.Join(data, "mysql")) {
		return nil // already initialized
	}
	if dirExists(data) && !dirEmpty(data) {
		return fmt.Errorf("data folder %s exists but is not a valid %s data folder", data, m.name)
	}
	_ = os.RemoveAll(data)
	logf(fmt.Sprintf("Initializing %s data folder: %s", m.name, data))

	if m.id == "mariadb" {
		tool := filepath.Join(inst.Dir, "bin", "mariadb-install-db.exe")
		if !fileExists(tool) {
			tool = filepath.Join(inst.Dir, "bin", "mysql_install_db.exe")
		}
		args := []string{"--datadir=" + data}
		if s.Password != "" {
			args = append(args, "--password="+s.Password)
		}
		return runQuiet(ctx, logf, tool, args...)
	}

	// MySQL
	args := []string{"--defaults-file=" + cfg, "--initialize-insecure", "--basedir=" + inst.Dir, "--datadir=" + data, "--console"}
	if s.Password != "" {
		initFile := filepath.Join(p.Tmp, "mysql-init.sql")
		sql := fmt.Sprintf("ALTER USER 'root'@'localhost' IDENTIFIED BY '%s';\n", sqlEscape(s.Password))
		if err := os.WriteFile(initFile, []byte(sql), 0o600); err != nil {
			return err
		}
		defer os.Remove(initFile)
		args = append(args, "--init-file="+initFile)
	}
	return runQuiet(ctx, logf, m.serverExe(inst), args...)
}

func (m *mysqlFamily) Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	cmd := exec.Command(m.serverExe(inst),
		"--defaults-file="+m.ConfigFile(p, inst), // must be first
		"--basedir="+inst.Dir,
		"--datadir="+m.DataDir(p, inst),
		"--port="+strconv.Itoa(s.Port),
		"--console",
	)
	cmd.Dir = inst.Dir
	return &StartSpec{Cmd: cmd}, nil
}

func (m *mysqlFamily) Stop(p *Paths, inst Installation, s EngineSettings) error {
	db, err := openSQL(m.id, s, "")
	if err == nil {
		defer db.Close()
		_, err = db.Exec("SHUTDOWN")
		if err == nil {
			return nil
		}
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "eof") || strings.Contains(low, "bad connection") ||
			strings.Contains(low, "invalid connection") || strings.Contains(low, "connection reset") ||
			strings.Contains(low, "broken pipe") {
			return nil
		}
	}
	// Fallback to mysqladmin / mariadb-admin shutdown before Manager resorts to process termination.
	adminTool := filepath.Join(inst.Dir, "bin", "mysqladmin.exe")
	if m.id == "mariadb" {
		if ma := filepath.Join(inst.Dir, "bin", "mariadb-admin.exe"); fileExists(ma) {
			adminTool = ma
		}
	}
	if fileExists(adminTool) {
		user := strings.TrimSpace(s.User)
		if user == "" {
			user = "root"
		}
		args := []string{"--protocol=tcp", "--host=127.0.0.1", "--port=" + strconv.Itoa(s.Port), "--user=" + user}
		if s.Password != "" {
			args = append(args, "--password="+s.Password)
		}
		args = append(args, "shutdown")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if runQuiet(ctx, nil, adminTool, args...) == nil {
			return nil
		}
	}
	return err
}

func (m *mysqlFamily) PidOf(p *Paths, inst Installation) int { return 0 }

// ---------------------------------------------------------------- PostgreSQL

type postgres struct{}

func (pg *postgres) ID() string   { return "postgresql" }
func (pg *postgres) Name() string { return "PostgreSQL" }
func (pg *postgres) IsSQL() bool  { return true }

func (pg *postgres) Detect(dir string) bool {
	return fileExists(filepath.Join(dir, "bin", "pg_ctl.exe")) && fileExists(filepath.Join(dir, "bin", "postgres.exe"))
}

func (pg *postgres) ClientBinDir(inst Installation) string { return filepath.Join(inst.Dir, "bin") }

func (pg *postgres) DataDir(p *Paths, inst Installation) string {
	// PostgreSQL data folders are tied to the major version
	return filepath.Join(p.Data, "postgresql-"+versionPrefix(inst.Version, 1))
}

func (pg *postgres) ConfigFile(p *Paths, inst Installation) string {
	return filepath.Join(pg.DataDir(p, inst), "postgresql.conf")
}

func (pg *postgres) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	data := pg.DataDir(p, inst)
	if fileExists(filepath.Join(data, "PG_VERSION")) {
		return ensurePostgresConfFiles(data, inst.Dir, logf)
	}
	if dirExists(data) && !dirEmpty(data) {
		return fmt.Errorf("data folder %s exists but is not a valid PostgreSQL data folder", data)
	}
	user := s.User
	if user == "" {
		user = "postgres"
	}
	logf("Initializing PostgreSQL data folder: " + data)
	args := []string{"-D", data, "-U", user, "-E", "UTF8", "--no-locale", "-A", "trust"}
	if s.Password != "" {
		pw := filepath.Join(p.Tmp, "pg-pw.txt")
		if err := os.WriteFile(pw, []byte(s.Password+"\n"), 0o600); err != nil {
			return err
		}
		defer os.Remove(pw)
		args = append(args, "--pwfile="+pw)
	}
	return runQuiet(ctx, logf, filepath.Join(inst.Dir, "bin", "initdb.exe"), args...)
}

// ensurePostgresConfFiles restores missing or empty postgresql.conf, pg_hba.conf, or
// pg_ident.conf inside an already-initialized PostgreSQL cluster directory (PG_VERSION present)
// using the installation's share/*.sample templates or safe defaults.
func ensurePostgresConfFiles(dataDir, instDir string, logf func(string)) error {
	defaults := map[string]string{
		"postgresql.conf": "# Darius DB - PostgreSQL default configuration\r\nlisten_addresses = 'localhost'\r\nmax_connections = 100\r\n",
		"pg_hba.conf":     "# TYPE  DATABASE        USER            ADDRESS                 METHOD\r\nhost    all             all             127.0.0.1/32            trust\r\nhost    all             all             ::1/128                 trust\r\n",
		"pg_ident.conf":   "# MAPNAME       SYSTEM-USERNAME         PG-USERNAME\r\n",
	}
	for _, name := range []string{"postgresql.conf", "pg_hba.conf", "pg_ident.conf"} {
		target := filepath.Join(dataDir, name)
		if st, err := os.Stat(target); err == nil && st.Size() > 0 {
			continue
		}
		if logf != nil {
			logf("Restoring missing PostgreSQL config file: " + target)
		}
		sample := filepath.Join(instDir, "share", name+".sample")
		if b, err := os.ReadFile(sample); err == nil && len(bytes.TrimSpace(b)) > 0 {
			if name == "pg_hba.conf" {
				b = append(b, []byte("\r\nhost    all             all             127.0.0.1/32            trust\r\nhost    all             all             ::1/128                 trust\r\n")...)
			}
			if err := os.WriteFile(target, b, 0o644); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(target, []byte(defaults[name]), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (pg *postgres) Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	cmd := exec.Command(filepath.Join(inst.Dir, "bin", "pg_ctl.exe"),
		"start", "-D", pg.DataDir(p, inst),
		"-l", p.LogFile("postgresql"),
		"-o", "-p "+strconv.Itoa(s.Port),
		"-w", "-t", "30",
	)
	cmd.Dir = inst.Dir
	return &StartSpec{Cmd: cmd, Detached: true}, nil
}

func (pg *postgres) Stop(p *Paths, inst Installation, s EngineSettings) error {
	pgCtl := filepath.Join(inst.Dir, "bin", "pg_ctl.exe")
	data := pg.DataDir(p, inst)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := runQuiet(ctx, nil, pgCtl, "stop", "-D", data, "-m", "fast", "-w", "-t", "4")
	cancel()
	if err == nil {
		return nil
	}
	// Fallback to immediate mode if active connections/locks prevented fast shutdown,
	// then ensure any remaining PostgreSQL worker processes in inst.Dir are terminated.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
	err2 := runQuiet(ctx2, nil, pgCtl, "stop", "-D", data, "-m", "immediate", "-w", "-t", "3")
	cancel2()
	killProcessesInDir(inst.Dir)
	if pid := pg.PidOf(p, inst); pid > 0 {
		killProcessTree(pid)
	}
	_ = os.Remove(filepath.Join(data, "postmaster.pid"))
	if err2 == nil || !portInUse(s.Port) {
		return nil
	}
	return err2
}

func (pg *postgres) PidOf(p *Paths, inst Installation) int {
	b, err := os.ReadFile(filepath.Join(pg.DataDir(p, inst), "postmaster.pid"))
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	pid, _ := strconv.Atoi(line)
	return pid
}

// ---------------------------------------------------------------- Redis

type redis struct{}

func (r *redis) ID() string   { return "redis" }
func (r *redis) Name() string { return "Redis" }
func (r *redis) IsSQL() bool  { return false }

func (r *redis) exeDir(dir string) string {
	if fileExists(filepath.Join(dir, "redis-server.exe")) {
		return dir
	}
	if fileExists(filepath.Join(dir, "bin", "redis-server.exe")) {
		return filepath.Join(dir, "bin")
	}
	return ""
}

func (r *redis) Detect(dir string) bool                  { return r.exeDir(dir) != "" }
func (r *redis) ClientBinDir(inst Installation) string   { return r.exeDir(inst.Dir) }
func (r *redis) DataDir(p *Paths, _ Installation) string { return filepath.Join(p.Data, "redis") }
func (r *redis) ConfigFile(p *Paths, _ Installation) string {
	return filepath.Join(p.EngineEtc("redis"), "redis.conf")
}

const redisConf = `# Darius DB - Redis configuration
# Port, data dir and password are passed by Darius DB on the command line.
bind 127.0.0.1
protected-mode yes
databases 16
save 900 1
save 300 10
save 60 10000
dbfilename dump.rdb
appendonly no
maxmemory 256mb
maxmemory-policy allkeys-lru
`

func (r *redis) Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, logf func(string)) error {
	if err := writeIfMissing(r.ConfigFile(p, inst), redisConf); err != nil {
		return err
	}
	return os.MkdirAll(r.DataDir(p, inst), 0o755)
}

func (r *redis) Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	// Relative, forward-slash paths keep Cygwin/MSYS2 builds happy even when
	// the root folder contains spaces.
	args := []string{"etc/redis/redis.conf", "--port", strconv.Itoa(s.Port), "--dir", "data/redis"}
	if s.Password != "" {
		args = append(args, "--requirepass", s.Password)
	}
	cmd := exec.Command(filepath.Join(r.exeDir(inst.Dir), "redis-server.exe"), args...)
	cmd.Dir = p.Root
	return &StartSpec{Cmd: cmd}, nil
}

func (r *redis) Stop(p *Paths, inst Installation, s EngineSettings) error {
	c, err := dialRedis(s, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Do("SHUTDOWN")
	if err != nil && strings.Contains(err.Error(), "EOF") {
		return nil // connection closed by shutdown = success
	}
	return err
}

func (r *redis) PidOf(p *Paths, inst Installation) int { return 0 }

// EnsureConfig writes the default config file for engines whose config lives
// in etc/ (MariaDB, MySQL, Redis). PostgreSQL's postgresql.conf is created
// inside its data folder by initdb on first start.
func EnsureConfig(p *Paths, engineID string) error {
	switch engineID {
	case "apache":
		return writeIfMissing(filepath.Join(p.EngineEtc("apache"), "httpd.conf"), apacheHttpdConf)
	case "php":
		if err := writeIfMissing(filepath.Join(p.EngineEtc("php"), "php.ini"), defaultPhpIni); err != nil {
			return err
		}
		_, err := SyncManagedPHPIni(p)
		return err
	case "mariadb":
		cfg := filepath.Join(p.EngineEtc("mariadb"), "my.ini")
		if b, err := os.ReadFile(cfg); err == nil && (strings.Contains(string(b), "innodb_buffer_pool_size = 128M") || strings.Contains(string(b), "skip-name-resolve")) {
			_ = os.WriteFile(cfg, []byte(strings.ReplaceAll(mariadbIni, "\n", "\r\n")), 0o644)
		}
		return writeIfMissing(cfg, mariadbIni)
	case "mysql":
		cfg := filepath.Join(p.EngineEtc("mysql"), "my.ini")
		if b, err := os.ReadFile(cfg); err == nil && (strings.Contains(string(b), "innodb_buffer_pool_size = 128M") || strings.Contains(string(b), "skip-name-resolve")) {
			_ = os.WriteFile(cfg, []byte(strings.ReplaceAll(mysqlIni, "\n", "\r\n")), 0o644)
		}
		return writeIfMissing(cfg, mysqlIni)
	case "mssql":
		cfg := filepath.Join(p.EngineEtc("mssql"), "mssql.conf")
		if b, err := os.ReadFile(cfg); err == nil && (strings.Contains(string(b), "default_user") || strings.Contains(string(b), "default_password")) {
			_ = os.WriteFile(cfg, []byte(strings.ReplaceAll(mssqlConf, "\n", "\r\n")), 0o644)
		}
		return writeIfMissing(cfg, mssqlConf)
	case "redis":
		return writeIfMissing(filepath.Join(p.EngineEtc("redis"), "redis.conf"), redisConf)
	case "mailpit":
		return writeIfMissing(filepath.Join(p.EngineEtc("mailpit"), "mailpit.conf"), defaultMailpitConf)
	default:
		return nil
	}
}
