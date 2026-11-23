package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

const AppDirName = "code-cad-studio"

// Library represents a configured build123d project / model library folder.
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// Config holds persisted user settings for Code CAD Studio.
type Config struct {
	Libraries           []Library         `json:"libraries"`
	PythonPath          string            `json:"python_path"`
	ExportDir           string            `json:"export_dir,omitempty"`
	Theme               string            `json:"theme"`
	PresetSaveScope     string            `json:"preset_save_scope,omitempty"` // "user" or "project"
	DefaultPresets      map[string]string `json:"default_presets,omitempty"`   // modelID -> presetKey
	OnboardingCompleted bool              `json:"onboarding_completed,omitempty"`
}

// Store manages thread-safe loading and saving of Config.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

var nonLibrarySlug = regexp.MustCompile(`[^a-z0-9]+`)

// ExpandPath expands a leading "~" to the user's home directory and cleans the path.
func ExpandPath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if trimmed == "~" {
				trimmed = home
			} else {
				trimmed = filepath.Join(home, trimmed[2:])
			}
		}
	}
	return filepath.Clean(trimmed)
}

func slugifyLibraryID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = nonLibrarySlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "library"
	}
	return s
}

func pathDedupeKey(p string) string {
	clean := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

// NormalizeLibraries cleans paths, drops empty/duplicate paths, assigns sensible display names
// (disambiguating colliding folder basenames when names were auto-derived), and ensures stable
// unique IDs for every library.
func NormalizeLibraries(libs []Library) []Library {
	type pending struct {
		id         string
		name       string
		path       string
		autoNamed  bool
	}

	seenPaths := make(map[string]struct{}, len(libs))
	items := make([]pending, 0, len(libs))
	for _, lib := range libs {
		p := ExpandPath(lib.Path)
		if p == "" {
			continue
		}
		pk := pathDedupeKey(p)
		if _, dup := seenPaths[pk]; dup {
			continue
		}
		seenPaths[pk] = struct{}{}

		name := strings.TrimSpace(lib.Name)
		autoNamed := false
		if name == "" {
			base := filepath.Base(p)
			if base == "" || base == "." || base == string(os.PathSeparator) {
				base = "Library"
			}
			name = base
			autoNamed = true
		}

		items = append(items, pending{
			id:        strings.TrimSpace(lib.ID),
			name:      name,
			path:      p,
			autoNamed: autoNamed,
		})
	}

	// If multiple libraries share the same case-insensitive name, disambiguate any
	// auto-named entries using their parent directory name (e.g. "code-cad (Documents)").
	nameCounts := make(map[string]int, len(items))
	for _, it := range items {
		nameCounts[strings.ToLower(it.name)]++
	}
	for i := range items {
		if items[i].autoNamed && nameCounts[strings.ToLower(items[i].name)] > 1 {
			parent := filepath.Base(filepath.Dir(items[i].path))
			if parent != "" && parent != "." && parent != string(os.PathSeparator) {
				items[i].name = fmt.Sprintf("%s (%s)", items[i].name, parent)
			}
		}
	}

	usedIDs := make(map[string]struct{}, len(items))
	out := make([]Library, 0, len(items))

	// First reserve existing valid unique IDs so renames/reorders never churn preset keys.
	reserved := make([]string, len(items))
	for i, it := range items {
		if it.id != "" {
			cand := slugifyLibraryID(it.id)
			if _, exists := usedIDs[cand]; !exists {
				usedIDs[cand] = struct{}{}
				reserved[i] = cand
			}
		}
	}

	for i, it := range items {
		id := reserved[i]
		if id == "" {
			baseSlug := slugifyLibraryID(it.name)
			if baseSlug == "library" {
				baseSlug = slugifyLibraryID(filepath.Base(it.path))
			}
			id = baseSlug
			for n := 2; ; n++ {
				if _, exists := usedIDs[id]; !exists {
					break
				}
				id = fmt.Sprintf("%s-%d", baseSlug, n)
			}
			usedIDs[id] = struct{}{}
		}
		out = append(out, Library{
			ID:   id,
			Name: it.name,
			Path: it.path,
		})
	}

	return out
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

// StudioVenvDir returns the path to Studio's shared managed virtual environment (<ConfigDir>/venv).
func StudioVenvDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "venv"), nil
}

// VenvPythonExecutable returns the expected Python executable inside a venv directory.
func VenvPythonExecutable(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(venvDir, "bin", "python")
}

// StudioVenvPython returns the Python executable inside Studio's shared managed venv if present.
func StudioVenvPython() string {
	venvDir, err := StudioVenvDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(venvDir, "bin", "python"),
		filepath.Join(venvDir, "bin", "python3"),
	}
	if runtime.GOOS == "windows" {
		candidates = []string{
			filepath.Join(venvDir, "Scripts", "python.exe"),
		}
	}
	for _, cand := range candidates {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

// FindDirVenvPython returns the Python executable inside <dir>/.venv or <dir>/venv if present.
func FindDirVenvPython(dir string) string {
	root := ExpandPath(dir)
	if root == "" {
		return ""
	}
	candidates := []string{
		filepath.Join(root, ".venv", "bin", "python"),
		filepath.Join(root, ".venv", "bin", "python3"),
		filepath.Join(root, "venv", "bin", "python"),
		filepath.Join(root, "venv", "bin", "python3"),
	}
	if runtime.GOOS == "windows" {
		candidates = []string{
			filepath.Join(root, ".venv", "Scripts", "python.exe"),
			filepath.Join(root, "venv", "Scripts", "python.exe"),
		}
	}
	for _, cand := range candidates {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
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
			Libraries: []Library{},
			Theme:     "dark",
		},
	}

	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &s.cfg)
	}
	s.cfg.Libraries = NormalizeLibraries(s.cfg.Libraries)
	return s, nil
}

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg
	cfg.Libraries = make([]Library, len(s.cfg.Libraries))
	copy(cfg.Libraries, s.cfg.Libraries)
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
	s.cfg.Libraries = NormalizeLibraries(s.cfg.Libraries)
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return s.cfg, err
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return s.cfg, err
	}
	out := s.cfg
	out.Libraries = make([]Library, len(s.cfg.Libraries))
	copy(out.Libraries, s.cfg.Libraries)
	return out, nil
}

// FindLibrary returns the configured library with the given ID, if present.
func (c Config) FindLibrary(id string) (Library, bool) {
	for _, lib := range c.Libraries {
		if lib.ID == id {
			return lib, true
		}
	}
	return Library{}, false
}

// LibraryRoots returns all configured library root paths in order.
func (c Config) LibraryRoots() []string {
	roots := make([]string, 0, len(c.Libraries))
	for _, lib := range c.Libraries {
		if lib.Path != "" {
			roots = append(roots, lib.Path)
		}
	}
	return roots
}

// ResolvePython determines the shared Python executable to use:
// 1. Explicit PythonPath if set
// 2. Shared Studio-managed venv (<ConfigDir>/venv) if present
// 3. First configured library containing a .venv or venv Python executable
// 4. Fallback to "python3" (or "python" on Windows)
func (c Config) ResolvePython() string {
	if py := strings.TrimSpace(c.PythonPath); py != "" {
		return ExpandPath(py)
	}
	if studioPy := StudioVenvPython(); studioPy != "" {
		return studioPy
	}
	for _, lib := range c.Libraries {
		if libPy := FindDirVenvPython(lib.Path); libPy != "" {
			return libPy
		}
	}
	if runtime.GOOS == "windows" {
		return "python"
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return "python3"
	}
	if _, err := exec.LookPath("python"); err == nil {
		return "python"
	}
	return "python3"
}

// NeedsOnboarding returns true when the user has neither completed onboarding
// nor configured any model libraries yet.
func (c Config) NeedsOnboarding() bool {
	return !c.OnboardingCompleted && len(c.Libraries) == 0
}

// ResolveExportDir determines the default directory for model exports:
// 1. Explicit global ExportDir if set
// 2. <library.Path>/exports for the specified libraryID (when found)
// 3. <Libraries[0].Path>/exports if at least one library is configured
// 4. Empty string otherwise
func (c Config) ResolveExportDir(libraryID string) string {
	if dir := strings.TrimSpace(c.ExportDir); dir != "" {
		return ExpandPath(dir)
	}
	if libraryID != "" {
		if lib, ok := c.FindLibrary(libraryID); ok && lib.Path != "" {
			return filepath.Join(lib.Path, "exports")
		}
	}
	if len(c.Libraries) > 0 && c.Libraries[0].Path != "" {
		return filepath.Join(c.Libraries[0].Path, "exports")
	}
	return ""
}
