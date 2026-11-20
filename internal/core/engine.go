package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var EngineIDs = []string{"apache", "php", "mariadb", "mysql", "postgresql", "mssql", "redis", "mailpit"}

// Installation is one engine version found in bin/<engine>/<folder>.
type Installation struct {
	Engine  string `json:"engine"`
	Name    string `json:"name"`    // folder name, used as the identifier
	Version string `json:"version"` // parsed from folder name
	Dir     string `json:"dir"`
}

// StartSpec describes how to launch a server.
type StartSpec struct {
	Cmd *exec.Cmd
	// Detached means the launched command exits after spawning the real server
	// (pg_ctl). The server PID must then be read with PidOf.
	Detached bool
}

type Engine interface {
	ID() string
	Name() string
	IsSQL() bool
	// Detect reports whether dir contains a usable installation.
	Detect(dir string) bool
	// ClientBinDir is the folder added to PATH in the terminal.
	ClientBinDir(inst Installation) string
	DataDir(p *Paths, inst Installation) string
	// ConfigFile returns the main editable config file.
	ConfigFile(p *Paths, inst Installation) string
	// Prepare creates config files and initializes the data folder if needed.
	Prepare(ctx context.Context, p *Paths, inst Installation, s EngineSettings, log func(string)) error
	Start(p *Paths, inst Installation, s EngineSettings) (*StartSpec, error)
	// Stop asks the server to shut down gracefully. Caller kills on timeout.
	Stop(p *Paths, inst Installation, s EngineSettings) error
	// PidOf returns the server PID for detached engines (0 if unknown).
	PidOf(p *Paths, inst Installation) int
}

var engines = map[string]Engine{
	"apache":     &apacheEngine{},
	"php":        &phpEngine{},
	"mariadb":    &mysqlFamily{id: "mariadb", name: "MariaDB"},
	"mysql":      &mysqlFamily{id: "mysql", name: "MySQL"},
	"postgresql": &postgres{},
	"mssql":      &mssqlEngine{},
	"redis":      &redis{},
	"mailpit":    &mailpitEngine{},
}

func GetEngine(id string) (Engine, error) {
	e, ok := engines[id]
	if !ok {
		return nil, fmt.Errorf("unknown engine %q", id)
	}
	return e, nil
}

var versionRe = regexp.MustCompile(`(\d+(?:\.\d+){0,3})`)

func parseVersion(name string) string {
	if m := versionRe.FindString(name); m != "" {
		return m
	}
	return name
}

// majorMinor returns "11.4" for "11.4.14", and "16" when parts==1.
func versionPrefix(v string, parts int) string {
	s := strings.Split(v, ".")
	if len(s) > parts {
		s = s[:parts]
	}
	return strings.Join(s, ".")
}

// compareVersions compares dotted versions numerically.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ListInstallations scans bin/<engine> for installed versions (newest first).
func ListInstallations(p *Paths, engineID string) []Installation {
	eng, err := GetEngine(engineID)
	if err != nil {
		return nil
	}
	base := p.EngineBin(engineID)
	entries, _ := os.ReadDir(base)
	var out []Installation
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		if !eng.Detect(dir) {
			continue
		}
		out = append(out, Installation{Engine: engineID, Name: e.Name(), Version: parseVersion(e.Name()), Dir: dir})
	}
	// For MS SQL Server, also detect Windows-installed SQL Server LocalDB / sqlcmd tools
	if engineID == "mssql" {
		if sysInst, ok := detectSystemMSSQL(); ok && !isVersionHidden(p, engineID, sysInst.Name) {
			out = append(out, sysInst)
		}
	}
	// For Apache Web Server, also include the built-in zero-download portable server
	if engineID == "apache" {
		builtin := detectBuiltinApache(p)
		if !isVersionHidden(p, engineID, builtin.Name) {
			out = append(out, builtin)
		}
	}
	// For PHP, also detect Windows-installed PHP if present
	if engineID == "php" && len(out) == 0 {
		if sysPhp, ok := detectSystemPHP(); ok && !isVersionHidden(p, engineID, sysPhp.Name) {
			out = append(out, sysPhp)
		}
	}
	// For Mailpit, also include the built-in zero-download local SMTP + Web Inbox server
	if engineID == "mailpit" {
		builtin := detectBuiltinMailpit(p)
		if !isVersionHidden(p, engineID, builtin.Name) {
			out = append(out, builtin)
		}
	}
	sort.Slice(out, func(i, j int) bool { return compareVersions(out[i].Version, out[j].Version) > 0 })
	return out
}

func isVersionHidden(p *Paths, engineID, name string) bool {
	if p == nil || p.Root == "" {
		return false
	}
	for _, h := range readHiddenVersions(p.Root, engineID) {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

func FindInstallation(p *Paths, engineID, name string) (Installation, bool) {
	for _, in := range ListInstallations(p, engineID) {
		if in.Name == name {
			return in, true
		}
	}
	return Installation{}, false
}

// runQuiet runs a helper command hidden, appending its output to the log.
func runQuiet(ctx context.Context, logf func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if s := strings.TrimSpace(string(out)); s != "" && logf != nil {
		logf(s)
	}
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 600 {
			tail = "..." + tail[len(tail)-600:]
		}
		return fmt.Errorf("%s failed: %v\n%s", filepath.Base(name), err, tail)
	}
	return nil
}

func writeIfMissing(path, content string) error {
	if fileExists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.ReplaceAll(content, "\n", "\r\n")), 0o644)
}

func waitFor(timeout time.Duration, fn func() bool) bool {
	start := time.Now()
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		interval := 40 * time.Millisecond
		if time.Since(start) >= time.Second {
			interval = 125 * time.Millisecond
		}
		time.Sleep(interval)
	}
	return fn()
}
