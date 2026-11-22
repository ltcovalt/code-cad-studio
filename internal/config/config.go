package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const AppDirName = "code-cad-studio"

// Config holds persisted user settings for Code CAD Studio.
type Config struct {
	ProjectRoot     string            `json:"project_root"`
	PythonPath      string            `json:"python_path"`
	Theme           string            `json:"theme"`
	PresetSaveScope string            `json:"preset_save_scope,omitempty"` // "user" or "project"
	DefaultPresets  map[string]string `json:"default_presets,omitempty"`   // modelID -> presetKey
}

// Store manages thread-safe loading and saving of Config.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// ConfigDir returns ~/.config/code-cad-studio (or OS equivalent).
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, AppDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// NewStore loads existing config from disk or initializes defaults.
func NewStore() (*Store, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "config.json")
	s := &Store{
		path: p,
		cfg: Config{
			Theme: "dark",
		},
	}

	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &s.cfg)
	}
	return s, nil
}

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg
	if s.cfg.DefaultPresets != nil {
		cp := make(map[string]string, len(s.cfg.DefaultPresets))
		for k, v := range s.cfg.DefaultPresets {
			cp[k] = v
		}
		cfg.DefaultPresets = cp
	}
	return cfg
}

// Update atomically updates and persists configuration.
func (s *Store) Update(fn func(*Config)) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return s.cfg, err
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return s.cfg, err
	}
	return s.cfg, nil
}

// ResolvePython determines the Python executable to use:
// 1. Explicit PythonPath if set
// 2. <ProjectRoot>/.venv/bin/python (or Scripts/python.exe on Windows) if present
// 3. <ProjectRoot>/venv/bin/python if present
// 4. Fallback to "python3" (or "python" on Windows)
func (c Config) ResolvePython() string {
	if c.PythonPath != "" {
		return c.PythonPath
	}
	if c.ProjectRoot != "" {
		candidates := []string{
			filepath.Join(c.ProjectRoot, ".venv", "bin", "python"),
			filepath.Join(c.ProjectRoot, ".venv", "bin", "python3"),
			filepath.Join(c.ProjectRoot, "venv", "bin", "python"),
		}
		if runtime.GOOS == "windows" {
			candidates = []string{
				filepath.Join(c.ProjectRoot, ".venv", "Scripts", "python.exe"),
				filepath.Join(c.ProjectRoot, "venv", "Scripts", "python.exe"),
			}
		}
		for _, cand := range candidates {
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand
			}
		}
	}
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}
