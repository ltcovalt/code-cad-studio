package setup

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/config"
)

const (
	DefaultRepoURL    = "https://github.com/ltcovalt/code-cad"
	DefaultArchiveURL = "https://github.com/ltcovalt/code-cad/archive/refs/heads/main.zip"
)

// DetectedProject represents an existing build123d project folder discovered on disk.
type DetectedProject struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	ModelCount int    `json:"model_count"`
	HasVenv    bool   `json:"has_venv"`
}

// ProjectDirInfo summarizes the current state of a candidate project directory.
type ProjectDirInfo struct {
	Path            string `json:"path"`
	Exists          bool   `json:"exists"`
	IsDir           bool   `json:"is_dir"`
	IsEmpty         bool   `json:"is_empty"`
	HasModels       bool   `json:"has_models"`
	ModelCount      int    `json:"model_count"`
	HasVenv         bool   `json:"has_venv"`
	VenvPython      string `json:"venv_python,omitempty"`
	HasRequirements bool   `json:"has_requirements"`
}

// DefaultsResult holds OS-aware onboarding defaults and any auto-detected projects.
type DefaultsResult struct {
	DefaultDownloadDir string            `json:"default_download_dir"`
	StudioVenvDir      string            `json:"studio_venv_dir"`
	DetectedProjects   []DetectedProject `json:"detected_projects"`
	DefaultRepoURL     string            `json:"default_repo_url"`
	DefaultArchiveURL  string            `json:"default_archive_url"`
	OS                 string            `json:"os"`
}

var alwaysSkipDirs = map[string]struct{}{
	".git":          {},
	".venv":         {},
	"venv":          {},
	"__pycache__":   {},
	"node_modules":  {},
	".mypy_cache":   {},
	".pytest_cache": {},
	".ruff_cache":   {},
	"build":         {},
	"dist":          {},
}

var rootOnlySkipDirs = map[string]struct{}{
	"exports":    {},
	"components": {},
	"utils":      {},
	"tests":      {},
	"test":       {},
	"docs":       {},
}

// ExpandPath expands a leading "~" to the user's home directory and cleans the path.
func ExpandPath(p string) string {
	return config.ExpandPath(p)
}

// FindProjectVenvPython returns the Python executable inside <projectRoot>/.venv or venv if present.
func FindProjectVenvPython(projectRoot string) string {
	return config.FindDirVenvPython(projectRoot)
}

// FindStudioVenvPython returns the Python executable inside Studio's shared managed venv if present.
func FindStudioVenvPython() string {
	return config.StudioVenvPython()
}

// VenvPythonPath returns the expected Python executable path inside a venv directory.
func VenvPythonPath(venvDir string) string {
	return config.VenvPythonExecutable(venvDir)
}

// InspectProjectDir inspects a folder path without running Python so UI feedback is instant.
func InspectProjectDir(dir string) ProjectDirInfo {
	clean := ExpandPath(dir)
	info := ProjectDirInfo{Path: clean}
	if clean == "" {
		return info
	}

	st, err := os.Stat(clean)
	if err != nil {
		return info
	}
	info.Exists = true
	info.IsDir = st.IsDir()
	if !st.IsDir() {
		return info
	}

	entries, err := os.ReadDir(clean)
	if err != nil {
		return info
	}
	info.IsEmpty = len(entries) == 0

	if venvPy := FindProjectVenvPython(clean); venvPy != "" {
		info.HasVenv = true
		info.VenvPython = venvPy
	}

	if reqSt, err := os.Stat(filepath.Join(clean, "requirements.txt")); err == nil && !reqSt.IsDir() {
		info.HasRequirements = true
	}

	info.ModelCount = countLikelyModels(clean)
	info.HasModels = info.ModelCount > 0
	return info
}

func countLikelyModels(root string) int {
	count := 0
	cleanRoot := filepath.Clean(root)
	_ = filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == cleanRoot {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, skip := alwaysSkipDirs[name]; skip {
				return filepath.SkipDir
			}
			if filepath.Dir(path) == cleanRoot {
				if _, skip := rootOnlySkipDirs[name]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}

		name := d.Name()
		if !strings.HasSuffix(name, ".py") {
			return nil
		}
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, "test_") || name == "setup.py" {
			return nil
		}
		count++
		return nil
	})
	return count
}

// DefaultLibrariesBaseDir returns the consolidated managed parent directory for
// Studio-downloaded and scaffolded model libraries: <Documents>/code-cad-studio
// when ~/Documents exists, or <home>/code-cad-studio otherwise.
func DefaultLibrariesBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	baseDir := home
	docsDir := filepath.Join(home, "Documents")
	if st, err := os.Stat(docsDir); err == nil && st.IsDir() {
		baseDir = docsDir
	}
	return filepath.Join(baseDir, config.AppDirName)
}

// Defaults computes OS-aware suggested download/venv locations and scans
// common directories for existing build123d repositories.
func Defaults() DefaultsResult {
	home, _ := os.UserHomeDir()
	studioVenv, _ := config.StudioVenvDir()
	res := DefaultsResult{
		StudioVenvDir:     studioVenv,
		DetectedProjects:  []DetectedProject{},
		DefaultRepoURL:    DefaultRepoURL,
		DefaultArchiveURL: DefaultArchiveURL,
		OS:                runtime.GOOS,
	}

	if home == "" {
		return res
	}

	baseDir := DefaultLibrariesBaseDir()

	// Scan managed libraries directory and common directories for existing projects.
	searchCandidates := []string{}
	if baseDir != "" {
		if entries, err := os.ReadDir(baseDir); err == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					searchCandidates = append(searchCandidates, filepath.Join(baseDir, entry.Name()))
				}
			}
		}
	}
	searchCandidates = append(searchCandidates,
		filepath.Join(home, "Documents", "code-cad"),
		filepath.Join(home, "code-cad"),
		filepath.Join(home, "repos", "code-cad"),
		filepath.Join(home, "Repos", "code-cad"),
		filepath.Join(home, "Projects", "code-cad"),
		filepath.Join(home, "projects", "code-cad"),
		filepath.Join(home, "src", "code-cad"),
	)

	seen := map[string]struct{}{}
	for _, cand := range searchCandidates {
		info := InspectProjectDir(cand)
		if !info.Exists || !info.IsDir || !info.HasModels {
			continue
		}
		displayPath := info.Path
		if resolved, err := filepath.EvalSymlinks(info.Path); err == nil && resolved != "" {
			displayPath = resolved
		}
		key := filepath.Clean(displayPath)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		res.DetectedProjects = append(res.DetectedProjects, DetectedProject{
			Path:       displayPath,
			Name:       filepath.Base(displayPath),
			ModelCount: info.ModelCount,
			HasVenv:    info.HasVenv,
		})
	}

	if baseDir != "" {
		downloadDir := filepath.Join(baseDir, "code-cad")
		if info := InspectProjectDir(downloadDir); info.Exists && !info.IsEmpty {
			downloadDir = filepath.Join(baseDir, "code-cad-starter")
		}
		res.DefaultDownloadDir = downloadDir
	}
	return res
}
