package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type EngineSettings struct {
	Enabled        bool     `json:"enabled"`        // included in "Start All"
	Version        string   `json:"version"`        // active installation (folder name in bin/<engine>)
	Port           int      `json:"port"`
	User           string   `json:"user"`           // admin user used by the built-in browser
	Password       string   `json:"password"`       // admin password used by the built-in browser
	HiddenVersions []string `json:"hiddenVersions,omitempty"` // system/built-in installations removed by user
}

type Settings struct {
	Engines           map[string]*EngineSettings `json:"engines"`
	AutoStartServices bool                       `json:"autoStartServices"`
	StartMinimized    bool                       `json:"startMinimized"`
	RunOnBoot         bool                       `json:"runOnBoot"`
	StopOnExit        bool                       `json:"stopOnExit"`
	CloseToTray       bool                       `json:"closeToTray"`
	CatalogURL        string                     `json:"catalogUrl"`
	Theme             string                     `json:"theme"` // system | dark | light
}

func DefaultSettings() *Settings {
	return &Settings{
		Engines: map[string]*EngineSettings{
			"apache":     {Enabled: true, Port: 8080},
			"php":        {Enabled: true, Port: 9000},
			"mariadb":    {Enabled: true, Port: 3306, User: "root"},
			"mysql":      {Enabled: false, Port: 3307, User: "root"},
			"postgresql": {Enabled: true, Port: 5432, User: "postgres"},
			"mssql":      {Enabled: true, Port: 1433, User: "dbdarius", Password: "admin"},
			"redis":      {Enabled: true, Port: 6379},
			"mailpit":    {Enabled: true, Port: 8025},
		},
		StopOnExit:  true,
		CloseToTray: true,
		CatalogURL:  "",
		Theme:       "system",
	}
}

type SettingsStore struct {
	mu   sync.RWMutex
	path string
	s    *Settings
}

func LoadSettings(root string) *SettingsStore {
	st := &SettingsStore{path: filepath.Join(root, "settings.json"), s: DefaultSettings()}
	if b, err := os.ReadFile(st.path); err == nil {
		loaded := DefaultSettings()
		if json.Unmarshal(b, loaded) == nil {
			// merge so new engines/fields get defaults
			def := DefaultSettings()
			for id, es := range def.Engines {
				if loaded.Engines[id] == nil {
					loaded.Engines[id] = es
				}
			}
			// Populate default MSSQL credentials (dbdarius / admin) if previously left blank
			if ms := loaded.Engines["mssql"]; ms != nil && ms.User == "" && ms.Password == "" {
				ms.User = "dbdarius"
				ms.Password = "admin"
			}
			st.s = loaded
			_ = st.saveLocked()
		}
	}
	return st
}

// Get returns a deep copy of the settings.
func (st *SettingsStore) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s.clone()
}

func (st *SettingsStore) Engine(id string) EngineSettings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if e := st.s.Engines[id]; e != nil {
		return *e
	}
	return EngineSettings{}
}

func (st *SettingsStore) Update(fn func(s *Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(st.s)
	return st.saveLocked()
}

func (st *SettingsStore) Replace(s Settings) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	c := s.clone()
	def := DefaultSettings()
	for id, es := range def.Engines {
		if c.Engines[id] == nil {
			c.Engines[id] = es
		}
	}
	st.s = &c
	return st.saveLocked()
}

func (st *SettingsStore) saveLocked() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

func (s *Settings) clone() Settings {
	c := *s
	c.Engines = make(map[string]*EngineSettings, len(s.Engines))
	for k, v := range s.Engines {
		e := *v
		if len(v.HiddenVersions) > 0 {
			e.HiddenVersions = append([]string(nil), v.HiddenVersions...)
		}
		c.Engines[k] = &e
	}
	return c
}
