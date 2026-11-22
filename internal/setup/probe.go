package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PythonCandidate describes a discovered Python interpreter and its CAD readiness.
type PythonCandidate struct {
	Path             string `json:"path"`
	Source           string `json:"source"` // configured, project_venv, path, conda, pyenv, system
	Version          string `json:"version"`
	VersionOK        bool   `json:"version_ok"`
	HasBuild123d     bool   `json:"has_build123d"`
	Build123dVersion string `json:"build123d_version,omitempty"`
	HasOCPTessellate bool   `json:"has_ocp_tessellate"`
	HasPip           bool   `json:"has_pip"`
	HasVenv          bool   `json:"has_venv"`
	SysPrefix        string `json:"sys_prefix,omitempty"`
	Error            string `json:"error,omitempty"`
}

// ProbeResult summarizes Python, uv, and project directory readiness.
type ProbeResult struct {
	Candidates  []PythonCandidate `json:"candidates"`
	Recommended *PythonCandidate  `json:"recommended,omitempty"`
	HasUV       bool              `json:"has_uv"`
	UVPath      string            `json:"uv_path,omitempty"`
	OS          string            `json:"os"`
	ProjectInfo ProjectDirInfo    `json:"project_info"`
}

const probeSnippet = `import importlib.metadata, importlib.util, json, sys
def _ver(pkg):
    try:
        return importlib.metadata.version(pkg)
    except Exception:
        return ""
print(json.dumps({
    "executable": sys.executable,
    "sys_prefix": sys.prefix,
    "version": "%d.%d.%d" % sys.version_info[:3],
    "version_ok": sys.version_info >= (3, 10),
    "has_build123d": importlib.util.find_spec("build123d") is not None,
    "build123d_version": _ver("build123d"),
    "has_ocp_tessellate": importlib.util.find_spec("ocp_tessellate") is not None,
    "has_pip": importlib.util.find_spec("pip") is not None,
    "has_venv": importlib.util.find_spec("venv") is not None,
}))`

type probeJSON struct {
	Executable       string `json:"executable"`
	SysPrefix        string `json:"sys_prefix"`
	Version          string `json:"version"`
	VersionOK        bool   `json:"version_ok"`
	HasBuild123d     bool   `json:"has_build123d"`
	Build123dVersion string `json:"build123d_version"`
	HasOCPTessellate bool   `json:"has_ocp_tessellate"`
	HasPip           bool   `json:"has_pip"`
	HasVenv          bool   `json:"has_venv"`
}

type rawCandidate struct {
	path   string
	source string
}

// Probe inspects the project directory and discovers candidate Python interpreters
// plus uv availability.
func Probe(ctx context.Context, projectRoot, explicitPython string) ProbeResult {
	res := ProbeResult{
		Candidates:  []PythonCandidate{},
		OS:          runtime.GOOS,
		ProjectInfo: InspectProjectDir(projectRoot),
	}

	if uvPath, err := exec.LookPath("uv"); err == nil && uvPath != "" {
		res.HasUV = true
		res.UVPath = uvPath
	}

	raw := collectCandidatePaths(projectRoot, explicitPython)
	if len(raw) == 0 {
		return res
	}

	probed := make([]PythonCandidate, len(raw))
	var wg sync.WaitGroup
	for i, rc := range raw {
		wg.Add(1)
		go func(idx int, c rawCandidate) {
			defer wg.Done()
			probed[idx] = ProbeExecutable(ctx, c.path, c.source)
		}(i, rc)
	}
	wg.Wait()

	// Deduplicate by (sys_prefix, version) for valid candidates so /usr/bin/python3
	// and /usr/bin/python3.12 collapse to one entry while venv symlinks remain distinct.
	seenEnv := map[string]struct{}{}
	candidates := make([]PythonCandidate, 0, len(probed))
	for _, c := range probed {
		if c.Error != "" && c.Source != "configured" {
			continue
		}
		if c.Error == "" {
			envKey := strings.ToLower(filepath.Clean(c.SysPrefix)) + "|" + c.Version
			if _, dup := seenEnv[envKey]; dup {
				continue
			}
			seenEnv[envKey] = struct{}{}
		}
		candidates = append(candidates, c)
	}

	rankCandidates(candidates, res.HasUV)
	res.Candidates = candidates
	res.Recommended = pickRecommended(candidates, res.HasUV)
	return res
}

// ProbeExecutable runs a fast metadata probe against a single Python executable.
func ProbeExecutable(ctx context.Context, pythonPath, source string) PythonCandidate {
	cleanPath := ExpandPath(pythonPath)
	cand := PythonCandidate{
		Path:   cleanPath,
		Source: source,
	}
	if cleanPath == "" {
		cand.Error = "empty python path"
		return cand
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, cleanPath, "-c", probeSnippet)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		cand.Error = msg
		return cand
	}

	var parsed probeJSON
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &parsed); err != nil {
		cand.Error = "invalid probe response: " + err.Error()
		return cand
	}

	if parsed.Executable != "" && !filepath.IsAbs(cleanPath) {
		cand.Path = parsed.Executable
	}
	cand.Version = parsed.Version
	cand.VersionOK = parsed.VersionOK
	cand.HasBuild123d = parsed.HasBuild123d
	cand.Build123dVersion = parsed.Build123dVersion
	cand.HasOCPTessellate = parsed.HasOCPTessellate
	cand.HasPip = parsed.HasPip
	cand.HasVenv = parsed.HasVenv
	cand.SysPrefix = parsed.SysPrefix
	return cand
}

var versionedPythonRe = regexp.MustCompile(`^python3\.(\d+)$`)

// findVersionedPythons scans directories for python3.<minor> binaries (e.g. python3.14, python3.15)
// and returns them ordered from newest minor version to oldest.
func findVersionedPythons(dirs []string) []string {
	type entry struct {
		path  string
		minor int
	}
	var found []entry
	seen := map[string]struct{}{}

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			m := versionedPythonRe.FindStringSubmatch(e.Name())
			if len(m) != 2 {
				continue
			}
			minor, err := strconv.Atoi(m[1])
			if err != nil || minor < 10 {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if _, dup := seen[full]; dup {
				continue
			}
			seen[full] = struct{}{}
			found = append(found, entry{path: full, minor: minor})
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		return found[i].minor > found[j].minor
	})
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.path)
	}
	return out
}

func collectCandidatePaths(projectRoot, explicitPython string) []rawCandidate {
	var out []rawCandidate
	seenPath := map[string]struct{}{}

	add := func(p, source string) {
		p = ExpandPath(p)
		if p == "" {
			return
		}
		if !filepath.IsAbs(p) {
			resolved, err := exec.LookPath(p)
			if err != nil {
				if source == "configured" {
					// Preserve invalid explicit path so we can surface an error to the user.
					out = append(out, rawCandidate{path: p, source: source})
				}
				return
			}
			p = resolved
		} else {
			st, err := os.Stat(p)
			if err != nil || st.IsDir() {
				if source == "configured" {
					out = append(out, rawCandidate{path: p, source: source})
				}
				return
			}
		}
		key := filepath.Clean(p)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, dup := seenPath[key]; dup {
			return
		}
		seenPath[key] = struct{}{}
		out = append(out, rawCandidate{path: p, source: source})
	}

	// 1. Explicitly configured interpreter
	if strings.TrimSpace(explicitPython) != "" {
		add(explicitPython, "configured")
	}

	// 2. Project virtual environment
	if venvPy := FindProjectVenvPython(projectRoot); venvPy != "" {
		add(venvPy, "project_venv")
	}

	// 3. PATH lookups (default python3/python plus any versioned python3.X binaries)
	pathNames := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		pathNames = []string{"python", "python3"}
	}
	for _, name := range pathNames {
		if p, err := exec.LookPath(name); err == nil {
			add(p, "path")
		}
	}
	if runtime.GOOS != "windows" {
		for _, p := range findVersionedPythons(filepath.SplitList(os.Getenv("PATH"))) {
			add(p, "path")
		}
	}

	// 4. Conda, pyenv, uv-managed, and standard OS install locations
	home, _ := os.UserHomeDir()
	if condaPrefix := strings.TrimSpace(os.Getenv("CONDA_PREFIX")); condaPrefix != "" {
		if runtime.GOOS == "windows" {
			add(filepath.Join(condaPrefix, "python.exe"), "conda")
		} else {
			add(filepath.Join(condaPrefix, "bin", "python"), "conda")
		}
	}

	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			matches, _ := filepath.Glob(filepath.Join(localAppData, "Programs", "Python", "Python3*", "python.exe"))
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			for _, m := range matches {
				add(m, "system")
			}
		}
		if home != "" {
			for _, dir := range []string{"miniconda3", "anaconda3", "miniforge3", "mambaforge"} {
				add(filepath.Join(home, dir, "python.exe"), "conda")
			}
		}
	} else {
		if home != "" {
			add(filepath.Join(home, ".pyenv", "shims", "python3"), "pyenv")
			pyenvMatches, _ := filepath.Glob(filepath.Join(home, ".pyenv", "versions", "*", "bin", "python3"))
			sort.Sort(sort.Reverse(sort.StringSlice(pyenvMatches)))
			for _, m := range pyenvMatches {
				add(m, "pyenv")
			}
			uvMatches, _ := filepath.Glob(filepath.Join(home, ".local", "share", "uv", "python", "*", "bin", "python3"))
			sort.Sort(sort.Reverse(sort.StringSlice(uvMatches)))
			for _, m := range uvMatches {
				add(m, "system")
			}
			for _, dir := range []string{"miniconda3", "anaconda3", "miniforge3", "mambaforge"} {
				add(filepath.Join(home, dir, "bin", "python"), "conda")
			}
		}
		sysDirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}
		for _, dir := range sysDirs {
			add(filepath.Join(dir, "python3"), "system")
		}
		for _, p := range findVersionedPythons(sysDirs) {
			add(p, "system")
		}
	}

	return out
}

func sourcePriority(source string) int {
	switch source {
	case "configured":
		return 0
	case "project_venv":
		return 1
	case "path":
		return 2
	case "conda":
		return 3
	case "pyenv":
		return 4
	default:
		return 5
	}
}

func candidateScore(c PythonCandidate, hasUV bool) int {
	if c.Error != "" {
		return -1000
	}
	score := 0
	if c.VersionOK {
		score += 100
	}
	if c.VersionOK && c.HasBuild123d {
		score += 500
		if c.HasOCPTessellate {
			score += 20
		}
	}
	if c.VersionOK && (c.HasVenv || hasUV) {
		score += 30
	}
	if c.HasPip {
		score += 10
	}
	score -= sourcePriority(c.Source) * 5
	return score
}

func rankCandidates(candidates []PythonCandidate, hasUV bool) {
	sort.SliceStable(candidates, func(i, j int) bool {
		si := candidateScore(candidates[i], hasUV)
		sj := candidateScore(candidates[j], hasUV)
		if si != sj {
			return si > sj
		}
		return sourcePriority(candidates[i].Source) < sourcePriority(candidates[j].Source)
	})
}

func pickRecommended(candidates []PythonCandidate, hasUV bool) *PythonCandidate {
	for i := range candidates {
		c := candidates[i]
		if c.Error == "" && c.VersionOK && c.HasBuild123d {
			cp := c
			return &cp
		}
	}
	for i := range candidates {
		c := candidates[i]
		if c.Error == "" && c.VersionOK && (c.HasVenv || hasUV) {
			cp := c
			return &cp
		}
	}
	for i := range candidates {
		c := candidates[i]
		if c.Error == "" && c.VersionOK {
			cp := c
			return &cp
		}
	}
	return nil
}
