package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeLibrariesDisambiguatesSameNamedFolders(t *testing.T) {
	libs := NormalizeLibraries([]Library{
		{Path: "/tmp/repos/code-cad"},
		{Path: "/tmp/Documents/code-cad"},
		{Path: "/tmp/repos/code-cad/"}, // duplicate path must be dropped
		{Path: "   "},                  // empty path must be dropped
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

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
	cfg.ExportDir = "/tmp/global-exports"
	if got := cfg.ResolveExportDir("lib-b"); got != "/tmp/global-exports" {
		t.Fatalf("ResolveExportDir with override = %q", got)
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
	cfg.PythonPath = "/custom/python"
	if got := cfg.ResolvePython(); got != "/custom/python" {
		t.Fatalf("ResolvePython (explicit) = %q, want /custom/python", got)
	}
}
