package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func setTestConfigEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg-data"))
	t.Setenv("AppData", filepath.Join(root, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "AppData", "Local"))
	return root
}

func TestNormalizeLibrariesDisambiguatesSameNamedFolders(t *testing.T) {
	base := t.TempDir()
	pathA := filepath.Join(base, "repos", "code-cad")
	pathB := filepath.Join(base, "Documents", "code-cad")

	libs := NormalizeLibraries([]Library{
		{Path: pathA},
		{Path: pathB},
		{Path: pathA + string(os.PathSeparator)}, // duplicate path must be dropped
		{Path: "   "},                            // empty path must be dropped
	})

	if len(libs) != 2 {
		t.Fatalf("expected 2 unique libraries, got %d: %+v", len(libs), libs)
	}
	if libs[0].ID == libs[1].ID {
		t.Fatalf("expected distinct IDs for same-named folders, got %+v", libs)
	}
	if libs[0].Name != "code-cad (repos)" || libs[1].Name != "code-cad (Documents)" {
		t.Fatalf("expected parent-disambiguated names, got %+v", libs)
	}

	// Preserving existing IDs even when user renames both libraries to the same display label
	renamed := NormalizeLibraries([]Library{
		{ID: libs[0].ID, Name: "Shared Name", Path: libs[0].Path},
		{ID: libs[1].ID, Name: "Shared Name", Path: libs[1].Path},
	})
	if len(renamed) != 2 || renamed[0].ID != libs[0].ID || renamed[1].ID != libs[1].ID {
		t.Fatalf("expected existing IDs preserved across renames, got %+v", renamed)
	}
	if renamed[0].Name != "Shared Name" || renamed[1].Name != "Shared Name" {
		t.Fatalf("expected explicit user names kept verbatim, got %+v", renamed)
	}
}

func TestResolvePythonAndExportDirPriority(t *testing.T) {
	setTestConfigEnv(t)

	libA := filepath.Join(t.TempDir(), "lib_a")
	libB := filepath.Join(t.TempDir(), "lib_b")
	if err := os.MkdirAll(libA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libB, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Libraries: NormalizeLibraries([]Library{
			{ID: "lib-a", Name: "Lib A", Path: libA},
			{ID: "lib-b", Name: "Lib B", Path: libB},
		}),
	}

	// Per-library export dir resolution
	if got := cfg.ResolveExportDir("lib-b"); got != filepath.Join(libB, "exports") {
		t.Fatalf("ResolveExportDir(lib-b) = %q, want %q", got, filepath.Join(libB, "exports"))
	}
	if got := cfg.ResolveExportDir(""); got != filepath.Join(libA, "exports") {
		t.Fatalf("ResolveExportDir('') = %q, want %q", got, filepath.Join(libA, "exports"))
	}

	// Global export_dir overrides per-library default
	globalExports := filepath.Join(t.TempDir(), "global-exports")
	cfg.ExportDir = globalExports
	if got := cfg.ResolveExportDir("lib-b"); got != globalExports {
		t.Fatalf("ResolveExportDir with override = %q, want %q", got, globalExports)
	}

	// Library .venv picked up when Studio venv and PythonPath are absent
	libBVenvPy := VenvPythonExecutable(filepath.Join(libB, ".venv"))
	if err := os.MkdirAll(filepath.Dir(libBVenvPy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libBVenvPy, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := cfg.ResolvePython(); got != libBVenvPy {
		t.Fatalf("ResolvePython (library venv) = %q, want %q", got, libBVenvPy)
	}

	// Studio-managed venv takes priority over library .venv when present
	studioVenvDir, err := StudioVenvDir()
	if err != nil {
		t.Fatal(err)
	}
	studioPy := VenvPythonExecutable(studioVenvDir)
	if err := os.MkdirAll(filepath.Dir(studioPy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(studioPy, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := cfg.ResolvePython(); got != studioPy {
		t.Fatalf("ResolvePython (studio venv) = %q, want %q", got, studioPy)
	}

	// Explicit PythonPath overrides everything
	customPy := filepath.Join(t.TempDir(), "custom-python")
	cfg.PythonPath = customPy
	if got := cfg.ResolvePython(); got != customPy {
		t.Fatalf("ResolvePython (explicit) = %q, want %q", got, customPy)
	}
}

func TestStudioVenvDirOSPaths(t *testing.T) {
	home := filepath.Join(string(os.PathSeparator), "Users", "maker")
	homeFn := func() (string, error) { return home, nil }

	darwinPath, err := studioVenvDirForOS("darwin", func(string) string { return "" }, homeFn)
	if err != nil {
		t.Fatalf("darwin error: %v", err)
	}
	wantDarwin := filepath.Join(home, "Library", "Caches", AppDirName, "venv")
	if darwinPath != wantDarwin {
		t.Fatalf("darwin path = %q, want %q", darwinPath, wantDarwin)
	}
	if strings.Contains(darwinPath, " ") {
		t.Fatalf("darwin venv path must be space-free, got %q", darwinPath)
	}

	linuxXDG, err := studioVenvDirForOS("linux", func(k string) string {
		if k == "XDG_DATA_HOME" {
			return "/var/tmp/xdg-data"
		}
		return ""
	}, homeFn)
	if err != nil {
		t.Fatalf("linux XDG error: %v", err)
	}
	if linuxXDG != filepath.Join("/var/tmp/xdg-data", AppDirName, "venv") {
		t.Fatalf("linux XDG path = %q", linuxXDG)
	}

	linuxFallback, err := studioVenvDirForOS("linux", func(string) string { return "" }, homeFn)
	if err != nil {
		t.Fatalf("linux fallback error: %v", err)
	}
	if linuxFallback != filepath.Join(home, ".local", "share", AppDirName, "venv") {
		t.Fatalf("linux fallback path = %q", linuxFallback)
	}

	winEnv, err := studioVenvDirForOS("windows", func(k string) string {
		if k == "LOCALAPPDATA" {
			return filepath.Join(home, "AppData", "Local")
		}
		return ""
	}, homeFn)
	if err != nil {
		t.Fatalf("windows env error: %v", err)
	}
	if winEnv != filepath.Join(home, "AppData", "Local", AppDirName, "venv") {
		t.Fatalf("windows env path = %q", winEnv)
	}

	winFallback, err := studioVenvDirForOS("windows", func(string) string { return "" }, homeFn)
	if err != nil {
		t.Fatalf("windows fallback error: %v", err)
	}
	if winFallback != filepath.Join(home, "AppData", "Local", AppDirName, "venv") {
		t.Fatalf("windows fallback path = %q", winFallback)
	}
}

func TestConfigV1DefaultsOnboardingAndPersistence(t *testing.T) {
	setTestConfigEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if store.LoadError() != nil {
		t.Fatalf("unexpected LoadError on fresh store: %v", store.LoadError())
	}

	cfg := store.Get()
	if cfg.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", cfg.SchemaVersion, SchemaVersion)
	}
	if cfg.ExportQuality != "standard" {
		t.Fatalf("ExportQuality = %q, want standard", cfg.ExportQuality)
	}
	if cfg.Theme != "dark" {
		t.Fatalf("Theme = %q, want dark", cfg.Theme)
	}
	if !cfg.NeedsOnboarding() {
		t.Fatalf("expected fresh config to require onboarding")
	}

	updated, err := store.Update(func(c *Config) {
		c.OnboardingSkipped = true
		c.EditorCommand = "code"
		c.SlicerCommand = "orca-slicer"
		c.ExportQuality = "fine"
		c.DefaultPresets = map[string]string{"code-cad/lid": "pyrex_round_4_cup"}
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updated.NeedsOnboarding() {
		t.Fatalf("expected NeedsOnboarding() == false when OnboardingSkipped is true")
	}
	if updated.SchemaVersion != 1 || updated.ExportQuality != "fine" || updated.EditorCommand != "code" || updated.SlicerCommand != "orca-slicer" {
		t.Fatalf("unexpected updated config: %+v", updated)
	}

	// Mutating returned DefaultPresets must not affect Store internal state
	updated.DefaultPresets["code-cad/lid"] = "mutated"
	reloaded, err := NewStore()
	if err != nil {
		t.Fatalf("reload NewStore failed: %v", err)
	}
	got := reloaded.Get()
	if got.DefaultPresets["code-cad/lid"] != "pyrex_round_4_cup" {
		t.Fatalf("expected persisted DefaultPresets intact, got %+v", got.DefaultPresets)
	}
	if !got.OnboardingSkipped || got.EditorCommand != "code" || got.SlicerCommand != "orca-slicer" || got.ExportQuality != "fine" {
		t.Fatalf("unexpected reloaded v1 fields: %+v", got)
	}

	// Verify config.json on disk contains schema_version: 1
	cfgDir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(cfgDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if disk["schema_version"] != float64(1) {
		t.Fatalf("disk schema_version = %v, want 1", disk["schema_version"])
	}
}

func TestCorruptConfigBackedUpAndNotSilentlyWiped(t *testing.T) {
	setTestConfigEnv(t)
	cfgDir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.json")
	corruptPayload := []byte("{not valid json: [")
	if err := os.WriteFile(cfgPath, corruptPayload, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore should succeed with recoverable load error, got %v", err)
	}
	if store.LoadError() == nil {
		t.Fatalf("expected non-nil LoadError for corrupt config.json")
	}
	backupPath := store.CorruptBackupPath()
	if backupPath == "" || !strings.HasPrefix(filepath.Base(backupPath), "config.json.corrupt.") {
		t.Fatalf("unexpected CorruptBackupPath: %q", backupPath)
	}
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("failed to read backup file: %v", err)
	}
	if string(backupData) != string(corruptPayload) {
		t.Fatalf("backup payload mismatch: got %q, want %q", backupData, corruptPayload)
	}
	// Original config.json must remain untouched by NewStore()
	origData, err := os.ReadFile(cfgPath)
	if err != nil || string(origData) != string(corruptPayload) {
		t.Fatalf("NewStore must not overwrite corrupt config.json, got %q err=%v", origData, err)
	}

	// Subsequent Update saves valid config while backup retains original corrupt data
	if _, err := store.Update(func(c *Config) {
		c.Theme = "light"
	}); err != nil {
		t.Fatalf("Update after corrupt load failed: %v", err)
	}
	if store.LoadError() != nil {
		t.Fatalf("expected LoadError cleared after successful Update, got %v", store.LoadError())
	}
	backupAfter, err := os.ReadFile(backupPath)
	if err != nil || string(backupAfter) != string(corruptPayload) {
		t.Fatalf("corrupt backup must survive subsequent Update")
	}

	// Unsupported future schema_version must also trigger backup & LoadError
	if err := os.WriteFile(cfgPath, []byte(`{"schema_version": 99, "theme": "dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store2, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if store2.LoadError() == nil {
		t.Fatalf("expected LoadError for unsupported schema_version 99")
	}
	if store2.CorruptBackupPath() == "" {
		t.Fatalf("expected backup path for unsupported schema_version")
	}
}

func TestStoreConcurrentUpdateAndGetNoDataRace(t *testing.T) {
	setTestConfigEnv(t)
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	const iters = 25
	var wg sync.WaitGroup
	errCh := make(chan error, workers*iters)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				modelKey := fmt.Sprintf("lib/model_%d", id)
				presetVal := fmt.Sprintf("preset_%d_%d", id, i)
				out, err := store.Update(func(c *Config) {
					if c.DefaultPresets == nil {
						c.DefaultPresets = map[string]string{}
					}
					c.DefaultPresets[modelKey] = presetVal
				})
				if err != nil {
					errCh <- err
					return
				}
				// Mutate returned map immediately to expose any shared-map race
				out.DefaultPresets["caller_mutation"] = "bad"
				snap := store.Get()
				if _, leaked := snap.DefaultPresets["caller_mutation"]; leaked {
					errCh <- errors.New("returned DefaultPresets aliased internal Store map")
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	final := store.Get()
	for w := 0; w < workers; w++ {
		key := fmt.Sprintf("lib/model_%d", w)
		want := fmt.Sprintf("preset_%d_%d", w, iters-1)
		if final.DefaultPresets[key] != want {
			t.Fatalf("DefaultPresets[%q] = %q, want %q", key, final.DefaultPresets[key], want)
		}
	}
}
