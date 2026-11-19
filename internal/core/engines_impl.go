package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

const mariadbIni = `# Darius DB - MariaDB configuration
# Port, basedir and datadir are passed by Darius DB on the command line.
[mysqld]
bind-address = 127.0.0.1
character-set-server = utf8mb4
collation-server = utf8mb4_unicode_ci
max_allowed_packet = 64M
innodb_buffer_pool_size = 128M
max_connections = 200

[client]
default-character-set = utf8mb4
`

const mysqlIni = `# Darius DB - MySQL configuration
# Port, basedir and datadir are passed by Darius DB on the command line.
[mysqld]
bind-address = 127.0.0.1
character-set-server = utf8mb4
max_allowed_packet = 64M
innodb_buffer_pool_size = 128M
max_connections = 200
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
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("SHUTDOWN")
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
		return nil
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

func (pg *postgres) Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error) {
	cmd := exec.Command(filepath.Join(inst.Dir, "bin", "pg_ctl.exe"),
		"start", "-D", pg.DataDir(p, inst),
		"-l", p.LogFile("postgresql"),
		"-o", "-p "+strconv.Itoa(s.Port),
		"-w", "-t", "60",
	)
	cmd.Dir = inst.Dir
	return &StartSpec{Cmd: cmd, Detached: true}, nil
}

func (pg *postgres) Stop(p *Paths, inst Installation, s EngineSettings) error {
	return runQuiet(context.Background(), nil, filepath.Join(inst.Dir, "bin", "pg_ctl.exe"),
		"stop", "-D", pg.DataDir(p, inst), "-m", "fast", "-w", "-t", "30")
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
		return writeIfMissing(filepath.Join(p.EngineEtc("php"), "php.ini"), defaultPhpIni)
	case "mariadb":
		return writeIfMissing(filepath.Join(p.EngineEtc("mariadb"), "my.ini"), mariadbIni)
	case "mysql":
		return writeIfMissing(filepath.Join(p.EngineEtc("mysql"), "my.ini"), mysqlIni)
	case "mssql":
		return writeIfMissing(filepath.Join(p.EngineEtc("mssql"), "mssql.conf"), mssqlConf)
	case "redis":
		return writeIfMissing(filepath.Join(p.EngineEtc("redis"), "redis.conf"), redisConf)
	case "mailpit":
		return writeIfMissing(filepath.Join(p.EngineEtc("mailpit"), "mailpit.conf"), defaultMailpitConf)
	default:
		return nil
	}
}

