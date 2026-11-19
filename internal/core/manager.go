package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	StateNotInstalled = "not-installed"
	StateStopped      = "stopped"
	StateStarting     = "starting"
	StateRunning      = "running"
	StateStopping     = "stopping"
	StateError        = "error"
)

type ServiceStatus struct {
	Engine        string         `json:"engine"`
	Name          string         `json:"name"`
	IsSQL         bool           `json:"isSql"`
	State         string         `json:"state"`
	Message       string         `json:"message"`
	Pid           int            `json:"pid"`
	Port          int            `json:"port"`
	Enabled       bool           `json:"enabled"`
	Active        string         `json:"active"` // active installation name
	Installations []Installation `json:"installations"`
	DataDir       string         `json:"dataDir"`
	ConfigFile    string         `json:"configFile"`
	PortBusy      bool           `json:"portBusy"` // port used by another program
	ServerName    string         `json:"serverName,omitempty"`
	User          string         `json:"user,omitempty"`
}

type service struct {
	state   string
	message string
	pid     int
	inst    Installation
	cmd     *exec.Cmd     // nil for detached/adopted processes
	exited  chan struct{} // closed when cmd exits
}

type Manager struct {
	p        *Paths
	st       *SettingsStore
	onChange func()
	mu       sync.Mutex
	svc      map[string]*service
	ops      map[string]*sync.Mutex // serializes start/stop per engine
}

func NewManager(p *Paths, st *SettingsStore, onChange func()) *Manager {
	m := &Manager{p: p, st: st, onChange: onChange, svc: map[string]*service{}, ops: map[string]*sync.Mutex{}}
	for _, id := range EngineIDs {
		m.svc[id] = &service{state: StateStopped}
		m.ops[id] = &sync.Mutex{}
	}
	m.ensureActiveVersions()
	m.adopt()
	go m.monitor()
	return m
}

func (m *Manager) changed() {
	if m.onChange != nil {
		m.onChange()
	}
}

func (m *Manager) set(id string, fn func(s *service)) {
	m.mu.Lock()
	fn(m.svc[id])
	m.mu.Unlock()
	m.changed()
}

// ensureActiveVersions picks the newest installation when none is selected.
func (m *Manager) ensureActiveVersions() {
	for _, id := range EngineIDs {
		es := m.st.Engine(id)
		if _, ok := FindInstallation(m.p, id, es.Version); ok {
			continue
		}
		insts := ListInstallations(m.p, id)
		name := ""
		if len(insts) > 0 {
			name = insts[0].Name
		}
		if name != es.Version {
			_ = m.st.Update(func(s *Settings) { s.Engines[id].Version = name })
		}
	}
}

// adopt re-attaches to servers left running by a previous session.
func (m *Manager) adopt() {
	for _, id := range EngineIDs {
		eng, _ := GetEngine(id)
		es := m.st.Engine(id)
		inst, ok := FindInstallation(m.p, id, es.Version)
		if !ok {
			continue
		}
		pid := eng.PidOf(m.p, inst)
		if pid == 0 {
			if b, err := os.ReadFile(m.p.PidFile(id)); err == nil {
				pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			}
		}
		if pid > 0 && (processBelongsTo(pid, m.p.Root) || (id == "mssql" && processAlive(pid))) {
			s := m.svc[id]
			s.state, s.pid, s.inst = StateRunning, pid, inst
		} else {
			os.Remove(m.p.PidFile(id))
		}
	}
}

func (m *Manager) monitor() {
	for range time.Tick(2 * time.Second) {
		changed := false
		m.mu.Lock()
		for _, s := range m.svc {
			if s.state == StateRunning && s.cmd == nil && !processAlive(s.pid) {
				s.state, s.message, s.pid = StateStopped, "process exited", 0
				changed = true
			}
		}
		m.mu.Unlock()
		if changed {
			m.changed()
		}
	}
}

func (m *Manager) Status() []ServiceStatus {
	out := make([]ServiceStatus, 0, len(EngineIDs))
	for _, id := range EngineIDs {
		out = append(out, m.status(id))
	}
	return out
}

func (m *Manager) status(id string) ServiceStatus {
	eng, _ := GetEngine(id)
	es := m.st.Engine(id)
	insts := ListInstallations(m.p, id)
	if insts == nil {
		insts = []Installation{}
	}
	m.mu.Lock()
	s := *m.svc[id]
	m.mu.Unlock()
	st := ServiceStatus{
		Engine: id, Name: eng.Name(), IsSQL: eng.IsSQL(), State: s.state, Message: s.message,
		Pid: s.pid, Port: es.Port, Enabled: es.Enabled, Active: es.Version, Installations: insts,
		User: es.User,
	}
	if id == "mssql" {
		st.ServerName = MSSQLServerName()
		if st.User == "" {
			st.User = "dbdarius"
		}
	}
	if inst, ok := FindInstallation(m.p, id, es.Version); ok {
		st.DataDir = eng.DataDir(m.p, inst)
		st.ConfigFile = eng.ConfigFile(m.p, inst)
	} else if s.state == StateStopped {
		st.State = StateNotInstalled
	}
	if s.state == StateStopped || s.state == StateError {
		st.PortBusy = portInUse(es.Port)
	}
	return st
}

func (m *Manager) IsRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.svc[id]
	return s != nil && (s.state == StateRunning || s.state == StateStarting)
}

func (m *Manager) logf(id string) func(string) {
	return func(line string) {
		f, err := os.OpenFile(m.p.LogFile(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "[darius] %s %s\r\n", time.Now().Format("2006-01-02 15:04:05"), strings.ReplaceAll(line, "\n", "\r\n"))
	}
}

// Start launches the active version of an engine and waits until it accepts connections.
func (m *Manager) Start(id string) error {
	eng, err := GetEngine(id)
	if err != nil {
		return err
	}
	op := m.ops[id]
	op.Lock()
	defer op.Unlock()

	if m.IsRunning(id) {
		return nil
	}
	es := m.st.Engine(id)
	inst, ok := FindInstallation(m.p, id, es.Version)
	if !ok {
		return fmt.Errorf("%s is not installed - open Versions to download it", eng.Name())
	}
	if portInUse(es.Port) {
		err := fmt.Errorf("port %d is already used by another program - change the %s port in Settings", es.Port, eng.Name())
		m.set(id, func(s *service) { s.state, s.message = StateError, err.Error() })
		return err
	}

	m.set(id, func(s *service) { s.state, s.message, s.inst = StateStarting, "", inst })
	fail := func(err error) error {
		m.logf(id)("ERROR: " + err.Error())
		m.set(id, func(s *service) { s.state, s.message, s.pid, s.cmd = StateError, err.Error(), 0, nil })
		return err
	}

	logf := m.logf(id)
	logf(fmt.Sprintf("Starting %s (%s) on port %d", eng.Name(), inst.Name, es.Port))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := eng.Prepare(ctx, m.p, inst, es, logf); err != nil {
		return fail(err)
	}

	spec, err := eng.Start(m.p, inst, es)
	if err != nil {
		return fail(err)
	}
	if spec.Detached {
		cmd := spec.Cmd
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if s := strings.TrimSpace(string(out)); s != "" {
			logf(s)
		}
		if err != nil {
			tail := strings.TrimSpace(string(out))
			if len(tail) > 600 {
				tail = "..." + tail[len(tail)-600:]
			}
			if tail != "" {
				tail = "\n\n" + tail
			}
			return fail(fmt.Errorf("%s failed to start: %v%s%s", eng.Name(), err, tail, m.logTail(id)))
		}
		pid := 0
		waitFor(10*time.Second, func() bool { pid = eng.PidOf(m.p, inst); return pid > 0 && processAlive(pid) })
		if pid == 0 {
			return fail(fmt.Errorf("%s did not start%s", eng.Name(), m.logTail(id)))
		}
		os.WriteFile(m.p.PidFile(id), []byte(strconv.Itoa(pid)), 0o644)
		m.set(id, func(s *service) { s.state, s.pid, s.cmd = StateStarting, pid, nil })
	} else {
		logFile, err := os.OpenFile(m.p.LogFile(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fail(err)
		}
		cmd := spec.Cmd
		hideWindow(cmd)
		cmd.Stdout, cmd.Stderr = logFile, logFile
		if err := cmd.Start(); err != nil {
			logFile.Close()
			return fail(err)
		}
		logFile.Close() // the child keeps its own handle

		exited := make(chan struct{})
		pid := cmd.Process.Pid
		os.WriteFile(m.p.PidFile(id), []byte(strconv.Itoa(pid)), 0o644)
		m.set(id, func(s *service) { s.pid, s.cmd, s.exited = pid, cmd, exited })
		go func() {
			werr := cmd.Wait()
			close(exited)
			os.Remove(m.p.PidFile(id))
			m.set(id, func(s *service) {
				if s.cmd != cmd {
					return
				}
				if s.state == StateStopping || s.state == StateStopped {
					s.state, s.message = StateStopped, ""
				} else {
					msg := "process exited unexpectedly"
					if werr != nil {
						msg += " (" + werr.Error() + ")"
					}
					s.state, s.message = StateError, msg+m.logTail(id)
				}
				s.pid, s.cmd = 0, nil
			})
		}()
	}

	// wait for the server to accept connections
	ready := waitFor(90*time.Second, func() bool {
		if !m.IsRunning(id) {
			return true // exited, handled above
		}
		if id == "mssql" && eng.PidOf(m.p, inst) > 0 {
			return true
		}
		return portInUse(es.Port)
	})
	m.mu.Lock()
	s := m.svc[id]
	state := s.state
	m.mu.Unlock()
	if state == StateError {
		return errors.New(s.message)
	}
	if !ready {
		return fail(fmt.Errorf("%s did not open port %d in time%s", eng.Name(), es.Port, m.logTail(id)))
	}
	m.set(id, func(s *service) { s.state, s.message = StateRunning, "" })
	logf(fmt.Sprintf("%s is running (pid %d)", eng.Name(), s.pid))
	return nil
}

// Stop shuts the engine down gracefully, killing it after a timeout.
func (m *Manager) Stop(id string) error {
	eng, err := GetEngine(id)
	if err != nil {
		return err
	}
	op := m.ops[id]
	op.Lock()
	defer op.Unlock()

	m.mu.Lock()
	s := m.svc[id]
	if s.state != StateRunning && s.state != StateStarting {
		if s.state == StateError {
			s.state, s.message = StateStopped, ""
		}
		m.mu.Unlock()
		m.changed()
		return nil
	}
	s.state = StateStopping
	pid, inst, exited := s.pid, s.inst, s.exited
	hasCmd := s.cmd != nil
	m.mu.Unlock()
	m.changed()

	logf := m.logf(id)
	logf("Stopping " + eng.Name())
	waitTimeout := 20 * time.Second
	if err := eng.Stop(m.p, inst, m.st.Engine(id)); err != nil {
		logf("graceful stop failed: " + err.Error())
		waitTimeout = 2 * time.Second
	}
	gone := waitFor(waitTimeout, func() bool { return !processAlive(pid) })
	if !gone {
		logf("server did not stop in time - killing process")
		_ = killProcess(pid)
		waitFor(5*time.Second, func() bool { return !processAlive(pid) })
	}
	if hasCmd && exited != nil {
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
		}
	}
	os.Remove(m.p.PidFile(id))
	m.set(id, func(s *service) { s.state, s.message, s.pid, s.cmd = StateStopped, "", 0, nil })
	logf(eng.Name() + " stopped")
	return nil
}

func (m *Manager) Restart(id string) error {
	if err := m.Stop(id); err != nil {
		return err
	}
	return m.Start(id)
}

// StartAll starts every enabled, installed engine in parallel.
func (m *Manager) StartAll() error {
	return m.forAll(func(id string) error {
		es := m.st.Engine(id)
		if !es.Enabled {
			return nil
		}
		if _, ok := FindInstallation(m.p, id, es.Version); !ok {
			return nil
		}
		return m.Start(id)
	})
}

func (m *Manager) StopAll() error {
	return m.forAll(m.Stop)
}

func (m *Manager) forAll(fn func(string) error) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string
	for _, id := range EngineIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := fn(id); err != nil {
				mu.Lock()
				errs = append(errs, err.Error())
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

// SetActive switches the active version, restarting the service if it was running.
func (m *Manager) SetActive(id, name string) error {
	if _, ok := FindInstallation(m.p, id, name); !ok {
		return fmt.Errorf("%s is not installed", name)
	}
	wasRunning := m.IsRunning(id)
	if wasRunning {
		if err := m.Stop(id); err != nil {
			return err
		}
	}
	if err := m.st.Update(func(s *Settings) { s.Engines[id].Version = name }); err != nil {
		return err
	}
	m.changed()
	if wasRunning {
		return m.Start(id)
	}
	return nil
}

// Uninstall removes an installed version (data folders are kept).
// If the version is running, it is stopped automatically first.
// For system-wide or built-in detections outside bin/, it unlinks/removes the entry from Darius DB.
func (m *Manager) Uninstall(id, name string) error {
	inst, ok := FindInstallation(m.p, id, name)
	if !ok {
		return fmt.Errorf("%s is not installed", name)
	}
	m.mu.Lock()
	s := m.svc[id]
	busy := (s.state == StateRunning || s.state == StateStarting || s.state == StateStopping) && s.inst.Name == name
	m.mu.Unlock()
	if busy {
		_ = m.Stop(id)
	}

	cleanBin := strings.ToLower(filepath.Clean(m.p.Bin)) + string(filepath.Separator)
	cleanDir := strings.ToLower(filepath.Clean(inst.Dir))
	if !strings.HasPrefix(cleanDir, cleanBin) {
		_ = m.st.Update(func(st *Settings) {
			es := st.Engines[id]
			if es == nil {
				return
			}
			for _, h := range es.HiddenVersions {
				if strings.EqualFold(h, name) {
					return
				}
			}
			es.HiddenVersions = append(es.HiddenVersions, name)
			if strings.EqualFold(es.Version, name) {
				es.Version = ""
			}
		})
		m.ensureActiveVersions()
		m.changed()
		return nil
	}

	killProcessesInDir(inst.Dir)
	clearReadOnlyRecursive(inst.Dir)
	if err := os.RemoveAll(inst.Dir); err != nil {
		time.Sleep(300 * time.Millisecond)
		killProcessesInDir(inst.Dir)
		clearReadOnlyRecursive(inst.Dir)
		if err2 := os.RemoveAll(inst.Dir); err2 != nil {
			return err2
		}
	}
	_ = m.st.Update(func(st *Settings) {
		if es := st.Engines[id]; es != nil && strings.EqualFold(es.Version, name) {
			es.Version = ""
		}
	})
	m.ensureActiveVersions()
	m.changed()
	return nil
}

// OnInstalled is called after a download finishes.
func (m *Manager) OnInstalled(id, name string) {
	_ = m.st.Update(func(st *Settings) {
		es := st.Engines[id]
		if es == nil || len(es.HiddenVersions) == 0 {
			return
		}
		kept := make([]string, 0, len(es.HiddenVersions))
		for _, h := range es.HiddenVersions {
			if !strings.EqualFold(h, name) {
				kept = append(kept, h)
			}
		}
		es.HiddenVersions = kept
	})
	m.ensureActiveVersions()
	m.changed()
}

func (m *Manager) ReadLog(id string, max int64) string {
	f, err := os.Open(m.p.LogFile(id))
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := st.Size() - max
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	n, _ := f.ReadAt(buf, off)
	return string(buf[:n])
}

func (m *Manager) ClearLog(id string) error {
	if m.IsRunning(id) {
		return errors.New("stop the service before clearing its log")
	}
	err := os.Truncate(m.p.LogFile(id), 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Manager) logTail(id string) string {
	lines := strings.Split(strings.TrimSpace(m.ReadLog(id, 4096)), "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	t := strings.TrimSpace(strings.Join(lines, "\n"))
	if t == "" {
		return ""
	}
	return "\n\n" + t
}

// ClientPaths returns client bin folders of the active versions for the terminal.
func (m *Manager) ClientPaths() []string {
	var out []string
	for _, id := range EngineIDs {
		eng, _ := GetEngine(id)
		if inst, ok := FindInstallation(m.p, id, m.st.Engine(id).Version); ok {
			out = append(out, eng.ClientBinDir(inst))
		}
	}
	return out
}
