package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTestZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestInspectProjectDirAndDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// Fresh home with Documents folder -> suggests ~/Documents/code-cad as default download dir
	docsDir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir Documents: %v", err)
	}

	def := Defaults()
	wantDownload := filepath.Join(docsDir, "code-cad")
	if def.DefaultDownloadDir != wantDownload {
		t.Fatalf("DefaultDownloadDir = %q, want %q", def.DefaultDownloadDir, wantDownload)
	}
	if def.StudioVenvDir == "" {
		t.Fatalf("expected non-empty StudioVenvDir")
	}
	if len(def.DetectedProjects) != 0 {
		t.Fatalf("expected 0 detected projects, got %d", len(def.DetectedProjects))
	}

	// Create an existing ~/code-cad with flat + nested models, utils (must be skipped), requirements.txt, and .venv
	existing := filepath.Join(home, "code-cad")
	if err := os.MkdirAll(filepath.Join(existing, "models", "widget"), 0o755); err != nil {
		t.Fatalf("mkdir models/widget: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, "models", "widget", "widget.py"), []byte("# model\n"), 0o644); err != nil {
		t.Fatalf("write widget.py: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(existing, "models", "gridfinity", "bin"), 0o755); err != nil {
		t.Fatalf("mkdir models/gridfinity/bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, "models", "gridfinity", "bin", "bin.py"), []byte("# nested model\n"), 0o644); err != nil {
		t.Fatalf("write bin.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, "root_model.py"), []byte("# root model\n"), 0o644); err != nil {
		t.Fatalf("write root_model.py: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(existing, "utils"), 0o755); err != nil {
		t.Fatalf("mkdir utils: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, "utils", "params.py"), []byte("# helper\n"), 0o644); err != nil {
		t.Fatalf("write params.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, "requirements.txt"), []byte("build123d\n"), 0o644); err != nil {
		t.Fatalf("write requirements.txt: %v", err)
	}
	venvPy := VenvPythonPath(filepath.Join(existing, ".venv"))
	if err := os.MkdirAll(filepath.Dir(venvPy), 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	if err := os.WriteFile(venvPy, []byte(""), 0o755); err != nil {
		t.Fatalf("write venv python: %v", err)
	}

	info := InspectProjectDir(existing)
	if !info.Exists || !info.IsDir || info.IsEmpty {
		t.Fatalf("unexpected info existence flags: %+v", info)
	}
	if !info.HasModels || info.ModelCount != 3 {
		t.Fatalf("expected 3 models (flat + nested + root, excluding utils), got %+v", info)
	}
	if !info.HasVenv || info.VenvPython != venvPy {
		t.Fatalf("expected venv python %q, got %+v", venvPy, info)
	}
	if !info.HasRequirements {
		t.Fatalf("expected HasRequirements=true")
	}

	// Defaults should now detect ~/code-cad
	def2 := Defaults()
	if len(def2.DetectedProjects) != 1 || def2.DetectedProjects[0].Path != existing || def2.DetectedProjects[0].ModelCount != 3 {
		t.Fatalf("expected detected project %q with 3 models, got %+v", existing, def2.DetectedProjects)
	}
}

func TestDownloadStarterProject(t *testing.T) {
	zipBytes := buildTestZip(t, map[string]string{
		"code-cad-main/README.md":             "# code-cad\n",
		"code-cad-main/models/box/box.py":     "from build123d import Box\n",
		"code-cad-main/utils/params.py":       "pass\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "my-code-cad")
	var stages []string
	res, err := DownloadStarterProject(context.Background(), DownloadOptions{
		ArchiveURL: srv.URL,
		TargetDir:  target,
	}, func(p DownloadProgress) {
		stages = append(stages, p.Stage)
	})
	if err != nil {
		t.Fatalf("DownloadStarterProject: %v", err)
	}
	if !res.ProjectInfo.HasModels || res.ProjectInfo.ModelCount != 1 {
		t.Fatalf("expected 1 model after extraction, got %+v", res.ProjectInfo)
	}
	if _, err := os.Stat(filepath.Join(target, "models", "box", "box.py")); err != nil {
		t.Fatalf("expected root-stripped models/box/box.py: %v", err)
	}
	if len(stages) == 0 || stages[len(stages)-1] != "done" {
		t.Fatalf("expected final progress stage 'done', got %v", stages)
	}

	// Re-downloading into non-empty target without AllowMerge must fail cleanly
	if _, err := DownloadStarterProject(context.Background(), DownloadOptions{
		ArchiveURL: srv.URL,
		TargetDir:  target,
	}, nil); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected non-empty directory error, got %v", err)
	}
}

func TestDownloadStarterProjectOverwriteExistingFolder(t *testing.T) {
	zipBytes := buildTestZip(t, map[string]string{
		"code-cad-main/README.md":           "# starter readme\n",
		"code-cad-main/.gitignore":          "*.stl\n",
		"code-cad-main/models/box/box.py":   "# starter box\n",
		"code-cad-main/models/clip/clip.py": "# starter clip\n",
		"code-cad-main/utils/params.py":     "# starter params\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "existing-project")
	if err := os.MkdirAll(filepath.Join(target, "models", "box"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(target, "models", "custom"), 0o755); err != nil {
		t.Fatalf("mkdir custom: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "models", "box", "box.py"), []byte("# old box\n"), 0o644); err != nil {
		t.Fatalf("write existing box: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "models", "custom", "custom.py"), []byte("# my custom\n"), 0o644); err != nil {
		t.Fatalf("write custom model: %v", err)
	}

	res, err := DownloadStarterProject(context.Background(), DownloadOptions{
		ArchiveURL:     srv.URL,
		TargetDir:      target,
		AllowOverwrite: true,
	}, nil)
	if err != nil {
		t.Fatalf("DownloadStarterProject overwrite: %v", err)
	}
	if !res.Merged || res.FilesAdded != 2 || res.FilesOverwritten != 1 {
		t.Fatalf("unexpected overwrite counts: %+v", res)
	}

	// Matching starter file is overwritten
	gotBox, err := os.ReadFile(filepath.Join(target, "models", "box", "box.py"))
	if err != nil || string(gotBox) != "# starter box\n" {
		t.Fatalf("expected box.py to be overwritten, got %q, err=%v", string(gotBox), err)
	}
	// Unrelated custom model stays intact
	gotCustom, err := os.ReadFile(filepath.Join(target, "models", "custom", "custom.py"))
	if err != nil || string(gotCustom) != "# my custom\n" {
		t.Fatalf("custom model should remain intact, got %q, err=%v", string(gotCustom), err)
	}
	// Missing starter model & utils are added
	if _, err := os.Stat(filepath.Join(target, "models", "clip", "clip.py")); err != nil {
		t.Fatalf("expected models/clip/clip.py: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "utils", "params.py")); err != nil {
		t.Fatalf("expected utils/params.py: %v", err)
	}
	// Top-level repo metadata is not copied into an existing folder
	if _, err := os.Stat(filepath.Join(target, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README.md should not be copied into existing folder")
	}
	if _, err := os.Stat(filepath.Join(target, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf(".gitignore should not be copied into existing folder")
	}
}

func TestDownloadStarterProjectRejectsZipSlip(t *testing.T) {
	zipBytes := buildTestZip(t, map[string]string{
		"code-cad-main/../../evil.txt": "pwned",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "safe-dir")
	if _, err := DownloadStarterProject(context.Background(), DownloadOptions{
		ArchiveURL: srv.URL,
		TargetDir:  target,
	}, nil); err == nil {
		t.Fatalf("expected zip-slip error, got nil")
	}
}

func TestCandidateRanking(t *testing.T) {
	candidates := []PythonCandidate{
		{Path: "/usr/bin/python3", Source: "path", Version: "3.12.0", VersionOK: true, HasBuild123d: false, HasVenv: true},
		{Path: "/global/python3", Source: "path", Version: "3.12.0", VersionOK: true, HasBuild123d: true, HasOCPTessellate: false},
		{Path: "/proj/.venv/bin/python", Source: "library_venv", SourceDetail: "proj", Version: "3.12.0", VersionOK: true, HasBuild123d: true, HasOCPTessellate: true},
		{Path: "/studio/venv/bin/python", Source: "studio_venv", Version: "3.12.0", VersionOK: true, HasBuild123d: true, HasOCPTessellate: true},
		{Path: "/old/python3.9", Source: "system", Version: "3.9.18", VersionOK: false, HasBuild123d: true},
	}

	rankCandidates(candidates, false)
	rec := pickRecommended(candidates, false)
	if rec == nil || rec.Path != "/studio/venv/bin/python" {
		t.Fatalf("expected studio_venv with build123d+ocp_tessellate first, got %+v", rec)
	}

	// When no candidate has build123d, pick VersionOK + HasVenv
	noB3D := []PythonCandidate{
		{Path: "/old/python3.9", Source: "path", Version: "3.9.18", VersionOK: false, HasVenv: true},
		{Path: "/usr/bin/python3", Source: "path", Version: "3.12.4", VersionOK: true, HasVenv: true},
	}
	rankCandidates(noB3D, false)
	rec2 := pickRecommended(noB3D, false)
	if rec2 == nil || rec2.Path != "/usr/bin/python3" {
		t.Fatalf("expected python 3.12.4 with venv, got %+v", rec2)
	}
}

func TestFindVersionedPythons(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"python3.9", "python3.12", "python3.14", "python3.15", "python3.14-config", "python3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got := findVersionedPythons([]string{dir})
	want := []string{
		filepath.Join(dir, "python3.15"),
		filepath.Join(dir, "python3.14"),
		filepath.Join(dir, "python3.12"),
	}
	if len(got) != len(want) {
		t.Fatalf("findVersionedPythons = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
