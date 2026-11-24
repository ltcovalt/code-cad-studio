package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	AppDirName    = "code-cad-studio"
	SchemaVersion = 1
)

// Library represents a configured build123d project / model library folder.
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// Config holds persisted user settings for Code CAD Studio.
type Config struct {
	SchemaVersion       int               `json:"schema_version"`
	Libraries           []Library         `json:"libraries"`
	PythonPath          string            `json:"python_path"`
	ExportDir           string            `json:"export_dir,omitempty"`
	Theme               string            `json:"theme"`
	PresetSaveScope     string            `json:"preset_save_scope,omitempty"` // "user" or "project"
	DefaultPresets      map[string]string `json:"default_presets,omitempty"`   // modelID -> presetKey
	OnboardingCompleted bool              `json:"onboarding_completed,omitempty"`
	OnboardingSkipped   bool              `json:"onboarding_skipped,omitempty"`
	EditorCommand       string            `json:"editor_command,omitempty"`
	SlicerCommand       string            `json:"slicer_command,omitempty"`
	ExportQuality       string            `json:"export_quality,omitempty"`
}

// Store manages thread-safe loading and saving of Config.
type Store struct {
	mu                sync.RWMutex
	path              string
	cfg               Config
	loadErr           error
	corruptBackupPath string
}

var nonLibrarySlug = regexp.MustCompile(`[^a-z0-9]+`)

func defaultConfig() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Libraries:     []Library{},
		Theme:         "dark",
		ExportQuality: "standard",
	}
}

func normalizeConfig(c *Config) {
	c.SchemaVersion = SchemaVersion
	c.Libraries = NormalizeLibraries(c.Libraries)
	if strings.TrimSpace(c.Theme) == "" {
		c.Theme = "dark"
	}
	if strings.TrimSpace(c.ExportQuality) == "" {
		c.ExportQuality = "standard"
	}
}

func cloneConfig(src Config) Config {
	out := src
	out.Libraries = make([]Library, len(src.Libraries))
	copy(out.Libraries, src.Libraries)
	if src.DefaultPresets != nil {
		cp := make(map[string]string, len(src.DefaultPresets))
		for k, v := range src.DefaultPresets {
			cp[k] = v
		}
		out.DefaultPresets = cp
	}
	return out
}

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
		id        string
		name      string
		path      string
		autoNamed bool
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

// StudioVenvDir returns the space-free OS-native path to Studio's shared managed virtual environment:
// - darwin: ~/Library/Caches/code-cad-studio/venv
// - windows: %LOCALAPPDATA%\code-cad-studio\venv
// - linux/other: $XDG_DATA_HOME/code-cad-studio/venv or ~/.local/share/code-cad-studio/venv
func StudioVenvDir() (string, error) {
	return studioVenvDirForOS(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func studioVenvDirForOS(goos string, getenv func(string) string, userHome func() (string, error)) (string, error) {
	switch goos {
	case "darwin":
		home, err := userHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Caches", AppDirName, "venv"), nil
	case "windows":
		if local := strings.TrimSpace(getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, AppDirName, "venv"), nil
		}
		home, err := userHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "AppData", "Local", AppDirName, "venv"), nil
	default:
		if xdg := strings.TrimSpace(getenv("XDG_DATA_HOME")); xdg != "" {
			return filepath.Join(xdg, AppDirName, "venv"), nil
		}
		home, err := userHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", AppDirName, "venv"), nil
	}
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

func parseConfigBytes(data []byte) (Config, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Config{}, errors.New("config.json is empty")
	}
	var rawObj map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawObj); err != nil {
		return Config{}, err
	}
	if rawObj == nil {
		return Config{}, errors.New("config.json must contain a JSON object")
	}
	loaded := defaultConfig()
	if err := json.Unmarshal(data, &loaded); err != nil {
		return Config{}, err
	}
	if loaded.SchemaVersion != 0 && loaded.SchemaVersion != SchemaVersion {
		return Config{}, fmt.Errorf("unsupported config schema_version %d", loaded.SchemaVersion)
	}
	normalizeConfig(&loaded)
	return loaded, nil
}

func backupCorruptConfig(origPath string, data []byte) string {
	ts := time.Now().UTC().Format("20060102T150405Z")
	base := fmt.Sprintf("%s.corrupt.%s", origPath, ts)
	candidate := base
	for i := 1; ; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			break
		}
		candidate = fmt.Sprintf("%s.%d", base, i)
	}
	if err := writeFileAtomic(candidate, data, 0o600); err != nil {
		return ""
	}
	return candidate
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := renameWithRetry(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func renameWithRetry(src, dst string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" && !os.IsPermission(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 15 * time.Millisecond)
	}
	return err
}

// NewStore loads existing config from disk or initializes defaults.
// If an existing config.json is corrupt or has an unsupported schema version,
// NewStore backs it up to config.json.corrupt.<timestamp>, preserves the original
// file on disk, records LoadError(), and initializes clean v1 defaults in memory.
func NewStore() (*Store, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "config.json")
	s := &Store{
		path: p,
		cfg:  defaultConfig(),
	}

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	loaded, parseErr := parseConfigBytes(data)
	if parseErr != nil {
		s.loadErr = parseErr
		s.corruptBackupPath = backupCorruptConfig(p, data)
		return s, nil
	}
	s.cfg = loaded
	return s, nil
}

// LoadError returns any parse/schema error encountered when loading config.json at startup.
func (s *Store) LoadError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadErr
}

// CorruptBackupPath returns the backup file path created when a corrupt config.json was detected.
func (s *Store) CorruptBackupPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.corruptBackupPath
}

// Get returns a deep copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cfg)
}

// Update atomically updates and persists configuration.
func (s *Store) Update(fn func(*Config)) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneConfig(s.cfg)
	fn(&next)
	normalizeConfig(&next)

	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return cloneConfig(s.cfg), err
	}
	if err := writeFileAtomic(s.path, append(data, '\n'), 0o644); err != nil {
		return cloneConfig(s.cfg), err
	}
	s.cfg = next
	s.loadErr = nil
	return cloneConfig(s.cfg), nil
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
// 2. Shared Studio-managed venv (StudioVenvDir()) if present
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

// NeedsOnboarding returns true when the user has neither completed nor skipped onboarding
// and has not configured any model libraries yet.
func (c Config) NeedsOnboarding() bool {
	return !c.OnboardingCompleted && !c.OnboardingSkipped && len(c.Libraries) == 0
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
