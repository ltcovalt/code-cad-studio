package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	SuggestedProjectDir string            `json:"suggested_project_dir"`
	SuggestedExportDir  string            `json:"suggested_export_dir"`
	DefaultDownloadDir  string            `json:"default_download_dir"`
	DetectedProjects    []DetectedProject `json:"detected_projects"`
	DefaultRepoURL      string            `json:"default_repo_url"`
	DefaultArchiveURL   string            `json:"default_archive_url"`
	OS                  string            `json:"os"`
}

var nonModelDirs = map[string]struct{}{
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
	"exports":       {},
	"components":    {},
	"utils":         {},
	"tests":         {},
	"test":          {},
	".agents":       {},
	"docs":          {},
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

// FindProjectVenvPython returns the Python executable inside <projectRoot>/.venv or venv if present.
func FindProjectVenvPython(projectRoot string) string {
	root := ExpandPath(projectRoot)
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

// VenvPythonPath returns the expected Python executable path inside a venv directory.
func VenvPythonPath(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(venvDir, "bin", "python")
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

	info.ModelCount = countLikelyModels(clean, entries)
	info.HasModels = info.ModelCount > 0
	return info
}

func countLikelyModels(root string, rootEntries []os.DirEntry) int {
	count := 0

	// Check canonical models/<name>/<name>.py or models/*.py layout
	modelsDir := filepath.Join(root, "models")
	if modelEntries, err := os.ReadDir(modelsDir); err == nil {
		for _, e := range modelEntries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				continue
			}
			if _, skip := nonModelDirs[name]; skip {
				continue
			}
			if e.IsDir() {
				subPath := filepath.Join(modelsDir, name)
				if hasPythonFile(subPath) {
					count++
				}
			} else if strings.HasSuffix(name, ".py") {
				count++
			}
		}
	}

	// Also count top-level standalone .py scripts or model subdirectories
	for _, e := range rootEntries {
		name := e.Name()
		if name == "models" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if _, skip := nonModelDirs[name]; skip {
			continue
		}
		if !e.IsDir() && strings.HasSuffix(name, ".py") && name != "setup.py" {
			count++
		}
	}

	return count
}

func hasPythonFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") && !strings.HasPrefix(e.Name(), "_") {
			return true
		}
	}
	return false
}

// Defaults computes OS-aware suggested project and export directories and scans
// common locations for an existing code-cad repository.
func Defaults() DefaultsResult {
	home, _ := os.UserHomeDir()
	res := DefaultsResult{
		DetectedProjects:  []DetectedProject{},
		DefaultRepoURL:    DefaultRepoURL,
		DefaultArchiveURL: DefaultArchiveURL,
		OS:                runtime.GOOS,
	}

	if home == "" {
		return res
	}

	// Scan common directories for an existing code-cad checkout
	searchCandidates := []string{
		filepath.Join(home, "Documents", "code-cad"),
		filepath.Join(home, "code-cad"),
		filepath.Join(home, "repos", "code-cad"),
		filepath.Join(home, "Projects", "code-cad"),
		filepath.Join(home, "projects", "code-cad"),
		filepath.Join(home, "src", "code-cad"),
	}

	seen := map[string]struct{}{}
	for _, cand := range searchCandidates {
		info := InspectProjectDir(cand)
		if !info.Exists || !info.IsDir || !info.HasModels {
			continue
		}
		key := info.Path
		if resolved, err := filepath.EvalSymlinks(info.Path); err == nil {
			key = resolved
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		res.DetectedProjects = append(res.DetectedProjects, DetectedProject{
			Path:       info.Path,
			Name:       filepath.Base(info.Path),
			ModelCount: info.ModelCount,
			HasVenv:    info.HasVenv,
		})
	}

	baseDir := home
	docsDir := filepath.Join(home, "Documents")
	if st, err := os.Stat(docsDir); err == nil && st.IsDir() {
		baseDir = docsDir
	}

	downloadDir := filepath.Join(baseDir, "code-cad")
	if info := InspectProjectDir(downloadDir); info.Exists && !info.IsEmpty {
		downloadDir = filepath.Join(baseDir, "code-cad-starter")
	}
	res.DefaultDownloadDir = downloadDir

	if len(res.DetectedProjects) > 0 {
		res.SuggestedProjectDir = res.DetectedProjects[0].Path
	} else {
		res.SuggestedProjectDir = downloadDir
	}

	res.SuggestedExportDir = filepath.Join(res.SuggestedProjectDir, "exports")
	return res
}
