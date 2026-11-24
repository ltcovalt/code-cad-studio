package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setTestHomeEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
}

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

func findTestPython(t *testing.T) string {
	t.Helper()
	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "python3"}
	}
	for _, name := range candidates {
		p, err := exec.LookPath(name)
		if err != nil || p == "" || isWindowsStoreStub(p) {
			continue
		}
		cmd := exec.Command(p, "--version")
		configureCmd(cmd)
		if err := cmd.Run(); err == nil {
			return p
		}
	}
	t.Skip("python interpreter not available")
	return ""
}

func TestInspectProjectDirAndDefaults(t *testing.T) {
	home := t.TempDir()
	setTestHomeEnv(t, home)

	// Fresh home with Documents folder -> suggests ~/Documents/code-cad-studio/code-cad as default download dir
	docsDir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir Documents: %v", err)
	}

	def := Defaults()
	wantDownload := filepath.Join(docsDir, "code-cad-studio", "code-cad")
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

	// Defaults should now detect ~/code-cad with canonical resolved path
	wantExisting := existing
	if resolved, err := filepath.EvalSymlinks(existing); err == nil && resolved != "" {
		wantExisting = resolved
	}
	def2 := Defaults()
	if len(def2.DetectedProjects) != 1 || def2.DetectedProjects[0].Path != wantExisting || def2.DetectedProjects[0].ModelCount != 3 {
		t.Fatalf("expected detected project %q with 3 models, got %+v", wantExisting, def2.DetectedProjects)
	}
}

func TestDefaultLibrariesBaseDirAndDefaultsDetection(t *testing.T) {
	home := t.TempDir()
	setTestHomeEnv(t, home)

	// Without Documents folder -> falls back to <home>/code-cad-studio
	wantFallbackBase := filepath.Join(home, "code-cad-studio")
	if got := DefaultLibrariesBaseDir(); got != wantFallbackBase {
		t.Fatalf("DefaultLibrariesBaseDir() without Documents = %q, want %q", got, wantFallbackBase)
	}
	defNoDocs := Defaults()
	if defNoDocs.DefaultDownloadDir != filepath.Join(wantFallbackBase, "code-cad") {
		t.Fatalf("DefaultDownloadDir without Documents = %q, want %q", defNoDocs.DefaultDownloadDir, filepath.Join(wantFallbackBase, "code-cad"))
	}

	// With Documents folder -> <home>/Documents/code-cad-studio
	docsDir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir Documents: %v", err)
	}
	wantDocsBase := filepath.Join(docsDir, "code-cad-studio")
	if got := DefaultLibrariesBaseDir(); got != wantDocsBase {
		t.Fatalf("DefaultLibrariesBaseDir() with Documents = %q, want %q", got, wantDocsBase)
	}
	defDocs := Defaults()
	if defDocs.DefaultDownloadDir != filepath.Join(wantDocsBase, "code-cad") {
		t.Fatalf("DefaultDownloadDir = %q, want %q", defDocs.DefaultDownloadDir, filepath.Join(wantDocsBase, "code-cad"))
	}

	// Populate ~/Documents/code-cad-studio/code-cad (non-empty) -> fallback to code-cad-starter
	managedStarter := filepath.Join(wantDocsBase, "code-cad")
	if err := os.MkdirAll(filepath.Join(managedStarter, "models", "box"), 0o755); err != nil {
		t.Fatalf("mkdir managed code-cad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedStarter, "models", "box", "box.py"), []byte("# box\n"), 0o644); err != nil {
		t.Fatalf("write box.py: %v", err)
	}
	// Add a second managed subdir (demo) inside DefaultLibrariesBaseDir
	managedDemo := filepath.Join(wantDocsBase, "demo")
	if err := os.MkdirAll(filepath.Join(managedDemo, "models", "spool"), 0o755); err != nil {
		t.Fatalf("mkdir managed demo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedDemo, "models", "spool", "spool.py"), []byte("# spool\n"), 0o644); err != nil {
		t.Fatalf("write spool.py: %v", err)
	}

	// Create ~/Repos/code-cad (capital R) — Defaults() checks both repos/code-cad and Repos/code-cad;
	// on Windows case-insensitive dedup + EvalSymlinks must keep only one entry with exact on-disk casing.
	reposCheckout := filepath.Join(home, "Repos", "code-cad")
	if err := os.MkdirAll(filepath.Join(reposCheckout, "models", "bracket"), 0o755); err != nil {
		t.Fatalf("mkdir Repos/code-cad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reposCheckout, "models", "bracket", "bracket.py"), []byte("# bracket\n"), 0o644); err != nil {
		t.Fatalf("write bracket.py: %v", err)
	}

	defPopulated := Defaults()
	wantStarterFallback := filepath.Join(wantDocsBase, "code-cad-starter")
	if defPopulated.DefaultDownloadDir != wantStarterFallback {
		t.Fatalf("DefaultDownloadDir when code-cad non-empty = %q, want %q", defPopulated.DefaultDownloadDir, wantStarterFallback)
	}

	resolveOrSelf := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil && r != "" {
			return r
		}
		return p
	}
	wantPaths := map[string]bool{
		resolveOrSelf(managedStarter): true,
		resolveOrSelf(managedDemo):    true,
		resolveOrSelf(reposCheckout):  true,
	}
	if len(defPopulated.DetectedProjects) != len(wantPaths) {
		t.Fatalf("expected %d unique detected projects, got %d: %+v", len(wantPaths), len(defPopulated.DetectedProjects), defPopulated.DetectedProjects)
	}
	for _, dp := range defPopulated.DetectedProjects {
		if !wantPaths[dp.Path] {
			t.Fatalf("unexpected or non-canonical detected project path %q (want one of %v)", dp.Path, wantPaths)
		}
	}
}

func TestDownloadStarterProject(t *testing.T) {
	zipBytes := buildTestZip(t, map[string]string{
		"code-cad-main/README.md":         "# code-cad\n",
		"code-cad-main/models/box/box.py": "from build123d import Box\n",
		"code-cad-main/utils/params.py":   "pass\n",
	})
	sum := sha256.Sum256(zipBytes)
	hexSum := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "my-code-cad")
	var stages []string
	res, err := DownloadStarterProject(context.Background(), DownloadOptions{
		ArchiveURL:     srv.URL,
		TargetDir:      target,
		ExpectedSHA256: hexSum,
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

	// Re-downloading into non-empty target without AllowOverwrite must fail cleanly
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

	gotBox, err := os.ReadFile(filepath.Join(target, "models", "box", "box.py"))
	if err != nil || string(gotBox) != "# starter box\n" {
		t.Fatalf("expected box.py to be overwritten, got %q, err=%v", string(gotBox), err)
	}
	gotCustom, err := os.ReadFile(filepath.Join(target, "models", "custom", "custom.py"))
	if err != nil || string(gotCustom) != "# my custom\n" {
		t.Fatalf("custom model should remain intact, got %q, err=%v", string(gotCustom), err)
	}
	if _, err := os.Stat(filepath.Join(target, "models", "clip", "clip.py")); err != nil {
		t.Fatalf("expected models/clip/clip.py: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "utils", "params.py")); err != nil {
		t.Fatalf("expected utils/params.py: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README.md should not be copied into existing folder")
	}
	if _, err := os.Stat(filepath.Join(target, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf(".gitignore should not be copied into existing folder")
	}
}

func TestDownloadStarterProjectSecurityHardening(t *testing.T) {
	t.Run("rejects zip-slip", func(t *testing.T) {
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
	})

	t.Run("rejects non-loopback HTTP and untrusted HTTPS hosts", func(t *testing.T) {
		cases := []string{
			"http://github.com/ltcovalt/code-cad/archive/refs/heads/main.zip",
			"https://evil.example.com/archive.zip",
			"file:///etc/passwd",
		}
		for _, u := range cases {
			_, err := DownloadStarterProject(context.Background(), DownloadOptions{
				ArchiveURL: u,
				TargetDir:  filepath.Join(t.TempDir(), "dest"),
			}, nil)
			if err == nil {
				t.Fatalf("expected URL %q to be rejected", u)
			}
		}
	})

	t.Run("rejects redirect to non-loopback HTTP or untrusted host", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://github.com/evil.zip", http.StatusFound)
		}))
		defer srv.Close()

		_, err := DownloadStarterProject(context.Background(), DownloadOptions{
			ArchiveURL: srv.URL,
			TargetDir:  filepath.Join(t.TempDir(), "dest"),
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
			t.Fatalf("expected redirect rejection for non-HTTPS URL, got %v", err)
		}
	})

	t.Run("rejects SHA256 mismatch", func(t *testing.T) {
		zipBytes := buildTestZip(t, map[string]string{
			"code-cad-main/models/box/box.py": "pass\n",
		})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(zipBytes)
		}))
		defer srv.Close()

		_, err := DownloadStarterProject(context.Background(), DownloadOptions{
			ArchiveURL:     srv.URL,
			TargetDir:      filepath.Join(t.TempDir(), "dest"),
			ExpectedSHA256: strings.Repeat("0", 64),
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Fatalf("expected SHA-256 mismatch error, got %v", err)
		}
	})

	t.Run("rejects zip entry count exceeding maxZipEntries", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for i := 0; i <= maxZipEntries; i++ {
			w, err := zw.Create(fmt.Sprintf("root/models/m_%04d.py", i))
			if err != nil {
				t.Fatalf("zip create: %v", err)
			}
			_, _ = w.Write([]byte("x"))
		}
		_ = zw.Close()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(buf.Bytes())
		}))
		defer srv.Close()

		_, err := DownloadStarterProject(context.Background(), DownloadOptions{
			ArchiveURL: srv.URL,
			TargetDir:  filepath.Join(t.TempDir(), "dest"),
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "too many entries") {
			t.Fatalf("expected maxZipEntries error, got %v", err)
		}
	})

	t.Run("rejects zip entry exceeding maxSingleFileBytes", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("root/models/huge.py")
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		chunk := bytes.Repeat([]byte("A"), 1024*1024)
		for i := 0; i < 51; i++ {
			if _, err := w.Write(chunk); err != nil {
				t.Fatalf("zip write: %v", err)
			}
		}
		_ = zw.Close()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(buf.Bytes())
		}))
		defer srv.Close()

		_, err = DownloadStarterProject(context.Background(), DownloadOptions{
			ArchiveURL: srv.URL,
			TargetDir:  filepath.Join(t.TempDir(), "dest"),
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "single-file limit") {
			t.Fatalf("expected single-file limit error, got %v", err)
		}
	})

	t.Run("rejects on-disk symlink in merge mode via os.Lstat", func(t *testing.T) {
		zipBytes := buildTestZip(t, map[string]string{
			"code-cad-main/models/box/box.py": "# overwrite attempt\n",
		})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(zipBytes)
		}))
		defer srv.Close()

		root := t.TempDir()
		outside := filepath.Join(root, "outside.txt")
		if err := os.WriteFile(outside, []byte("precious\n"), 0o644); err != nil {
			t.Fatalf("write outside: %v", err)
		}

		target := filepath.Join(root, "project")
		if err := os.MkdirAll(filepath.Join(target, "models", "box"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		linkPath := filepath.Join(target, "models", "box", "box.py")
		if err := os.Symlink(outside, linkPath); err != nil {
			t.Skipf("symlink creation not supported in this environment: %v", err)
		}

		_, err := DownloadStarterProject(context.Background(), DownloadOptions{
			ArchiveURL:     srv.URL,
			TargetDir:      target,
			AllowOverwrite: true,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink rejection error, got %v", err)
		}

		gotOutside, _ := os.ReadFile(outside)
		if string(gotOutside) != "precious\n" {
			t.Fatalf("outside target file was modified through symlink: %q", string(gotOutside))
		}
	})

	t.Run("rejects symlink entry inside zip archive", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		hdr := &zip.FileHeader{
			Name:   "code-cad-main/models/evil_link",
			Method: zip.Deflate,
		}
		hdr.SetMode(os.ModeSymlink | 0o777)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("CreateHeader: %v", err)
		}
		if _, err := w.Write([]byte("/etc/passwd")); err != nil {
			t.Fatalf("write symlink target: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("zip close: %v", err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(buf.Bytes())
		}))
		defer srv.Close()

		target := filepath.Join(t.TempDir(), "dest")
		_, err = DownloadStarterProject(context.Background(), DownloadOptions{
			ArchiveURL: srv.URL,
			TargetDir:  target,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink zip entry error, got %v", err)
		}
		if _, statErr := os.Lstat(filepath.Join(target, "models", "evil_link")); !os.IsNotExist(statErr) {
			t.Fatalf("expected symlink entry not to be extracted, statErr=%v", statErr)
		}
	})
}

func TestCandidateRanking(t *testing.T) {
	candidates := []PythonCandidate{
		{Path: "/usr/bin/python3", Source: "path", Version: "3.12.0", VersionOK: true, HasBuild123d: false, HasVenv: true, HasEnsurePip: true},
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

	// Prefer tested minor 3.12 with ensurepip over 3.15 or missing-ensurepip candidate when uv is absent
	noB3D := []PythonCandidate{
		{Path: "/old/python3.9", Source: "path", Version: "3.9.18", VersionOK: false, HasVenv: true, HasEnsurePip: true},
		{Path: "/usr/bin/python3.15", Source: "path", Version: "3.15.2", VersionOK: true, HasVenv: true, HasEnsurePip: true},
		{Path: "/usr/bin/python3.12-no-ensurepip", Source: "path", Version: "3.12.4", VersionOK: true, HasVenv: true, HasEnsurePip: false},
		{Path: "/usr/bin/python3.12", Source: "path", Version: "3.12.4", VersionOK: true, HasVenv: true, HasEnsurePip: true},
	}
	rankCandidates(noB3D, false)
	rec2 := pickRecommended(noB3D, false)
	if rec2 == nil || rec2.Path != "/usr/bin/python3.12" {
		t.Fatalf("expected python 3.12.4 with venv+ensurepip, got %+v", rec2)
	}
}

func TestFindVersionedPythons(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"python3.9", "python3.10", "python3.11", "python3.12", "python3.13", "python3.14", "python3.15", "python3.14-config", "python3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got := findVersionedPythons([]string{dir})
	want := []string{
		filepath.Join(dir, "python3.12"),
		filepath.Join(dir, "python3.13"),
		filepath.Join(dir, "python3.11"),
		filepath.Join(dir, "python3.10"),
		filepath.Join(dir, "python3.14"),
		filepath.Join(dir, "python3.15"),
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

func TestFindUVStandardLocations(t *testing.T) {
	home := t.TempDir()
	localUV := filepath.Join(home, ".local", "bin", "uv")
	if err := os.MkdirAll(filepath.Dir(localUV), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(localUV, []byte("uv"), 0o755); err != nil {
		t.Fatalf("write uv: %v", err)
	}

	got, ok := findUVForOS("linux", home, "", "", func(string) (string, error) {
		return "", errors.New("not on PATH")
	})
	if !ok || got != localUV {
		t.Fatalf("findUVForOS linux = (%q, %v), want (%q, true)", got, ok, localUV)
	}

	winHome := t.TempDir()
	localAppData := filepath.Join(winHome, "AppData", "Local")
	winUV := filepath.Join(localAppData, "uv", "uv.exe")
	if err := os.MkdirAll(filepath.Dir(winUV), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(winUV, []byte("uv"), 0o755); err != nil {
		t.Fatalf("write uv.exe: %v", err)
	}
	gotWin, okWin := findUVForOS("windows", winHome, localAppData, "", func(string) (string, error) {
		return "", errors.New("not on PATH")
	})
	if !okWin || gotWin != winUV {
		t.Fatalf("findUVForOS windows = (%q, %v), want (%q, true)", gotWin, okWin, winUV)
	}
}

func TestWindowsStoreStubAndPyLauncherParsing(t *testing.T) {
	if !isWindowsStoreStub(`C:\Users\Alice\AppData\Local\Microsoft\WindowsApps\python3.exe`) {
		t.Fatalf("expected WindowsApps stub to be detected")
	}
	if !isWindowsStoreStub(`/c/Users/Alice/AppData/Local/Microsoft/WindowsApps/python.exe`) {
		t.Fatalf("expected normalized WindowsApps stub to be detected")
	}
	if isWindowsStoreStub(`C:\Python312\python.exe`) {
		t.Fatalf("did not expect standard Python312 install to be flagged as stub")
	}

	sample := strings.Join([]string{
		` -V:3.12 *        C:\Users\Alice\AppData\Local\Programs\Python\Python312\python.exe`,
		` -V:3.11          C:\Program Files\Python 3.11\python.exe`,
	}, "\n")
	parsed := parsePyLauncherOutput(sample)
	if len(parsed) != 2 ||
		parsed[0] != `C:\Users\Alice\AppData\Local\Programs\Python\Python312\python.exe` ||
		parsed[1] != `C:\Program Files\Python 3.11\python.exe` {
		t.Fatalf("parsePyLauncherOutput = %#v", parsed)
	}
}

func TestProbeExecutablePopulatesEnsurePipAndBrokenVenvIsRecreated(t *testing.T) {
	py := findTestPython(t)
	cand := ProbeExecutable(context.Background(), py, "path", "")
	if cand.Error != "" {
		t.Fatalf("ProbeExecutable(%q) failed: %s", py, cand.Error)
	}
	if cand.Version == "" || !cand.VersionOK {
		t.Fatalf("expected valid Python 3.10+ version, got %+v", cand)
	}

	// Create a broken venv directory containing a sentinel marker file and an empty/invalid python binary
	venvDir := filepath.Join(t.TempDir(), "broken_venv")
	venvPy := VenvPythonPath(venvDir)
	if err := os.MkdirAll(filepath.Dir(venvPy), 0o755); err != nil {
		t.Fatalf("mkdir venv: %v", err)
	}
	if err := os.WriteFile(venvPy, []byte(""), 0o755); err != nil {
		t.Fatalf("write broken python: %v", err)
	}
	staleMarker := filepath.Join(venvDir, "stale_marker.txt")
	if err := os.WriteFile(staleMarker, []byte("old"), 0o644); err != nil {
		t.Fatalf("write stale marker: %v", err)
	}

	// Running InstallEnvironment with a non-existent base python must still detect and remove the broken venv
	_, err := InstallEnvironment(context.Background(), InstallOptions{
		TargetVenvDir: venvDir,
		BasePython:    filepath.Join(t.TempDir(), "missing-python"),
		UseUV:         false,
	}, nil)
	if err == nil {
		t.Fatalf("expected error with missing base python")
	}
	if _, statErr := os.Stat(staleMarker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected broken venv (and stale_marker.txt) to be removed before recreation, got statErr=%v", statErr)
	}
}
