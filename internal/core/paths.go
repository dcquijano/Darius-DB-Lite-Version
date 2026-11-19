package core

import (
	"os"
	"path/filepath"
)

// Paths describes the portable folder layout (everything lives next to the exe):
//
//	DariusDB.exe
//	bin/<engine>/<engine>-<version>/   downloaded engine binaries
//	data/<engine>-<major>/             database data folders
//	etc/<engine>/                      config files (my.ini, redis.conf)
//	logs/                              service logs
//	tmp/                               downloads in progress
//	settings.json
type Paths struct {
	Root string `json:"root"`
	Bin  string `json:"bin"`
	Data string `json:"data"`
	Www  string `json:"www"`
	Etc  string `json:"etc"`
	Logs string `json:"logs"`
	Tmp  string `json:"tmp"`
	Run  string `json:"run"`
}

// ResolveRoot returns the portable root folder.
// DARIUSDB_HOME overrides it; otherwise the folder of the executable is used.
func ResolveRoot() string {
	if h := os.Getenv("DARIUSDB_HOME"); h != "" {
		if abs, err := filepath.Abs(h); err == nil {
			return abs
		}
		return h
	}
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func NewPaths(root string) *Paths {
	p := &Paths{
		Root: root,
		Bin:  filepath.Join(root, "bin"),
		Data: filepath.Join(root, "data"),
		Www:  filepath.Join(root, "www"),
		Etc:  filepath.Join(root, "etc"),
		Logs: filepath.Join(root, "logs"),
		Tmp:  filepath.Join(root, "tmp"),
	}
	p.Run = filepath.Join(p.Tmp, "run")
	return p
}

func (p *Paths) Ensure() error {
	for _, d := range []string{p.Bin, p.Data, p.Www, p.Etc, p.Logs, p.Tmp, p.Run} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, e := range EngineIDs {
		if err := os.MkdirAll(filepath.Join(p.Bin, e), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (p *Paths) EngineBin(engine string) string { return filepath.Join(p.Bin, engine) }
func (p *Paths) EngineEtc(engine string) string { return filepath.Join(p.Etc, engine) }
func (p *Paths) LogFile(engine string) string   { return filepath.Join(p.Logs, engine+".log") }
func (p *Paths) PidFile(engine string) string   { return filepath.Join(p.Run, engine+".pid") }

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func dirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err != nil || len(entries) == 0
}
