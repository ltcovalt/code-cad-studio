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

	"github.com/ltcovalt/code-cad-studio/internal/config"
)

// PythonCandidate describes a discovered Python interpreter and its CAD readiness.
type PythonCandidate struct {
	Path             string `json:"path"`
	Source           string `json:"source"` // configured, studio_venv, library_venv, path, conda, pyenv, system
	SourceDetail     string `json:"source_detail,omitempty"`
	Version          string `json:"version"`
	VersionOK        bool   `json:"version_ok"`
	HasBuild123d     bool   `json:"has_build123d"`
	Build123dVersion string `json:"build123d_version,omitempty"`
	HasOCPTessellate bool   `json:"has_ocp_tessellate"`
	SysPrefix        string `json:"sys_prefix,omitempty"`
	Error            string `json:"error,omitempty"`
}

// ProbeResult summarizes Python, uv, and shared Studio venv readiness across libraries.
type ProbeResult struct {
	Candidates           []PythonCandidate `json:"candidates"`
	Recommended          *PythonCandidate  `json:"recommended,omitempty"`
	HasUV                bool              `json:"has_uv"`
	UVPath               string            `json:"uv_path,omitempty"`
	ManagedPythonVersion string            `json:"managed_python_version"`
	OS                   string            `json:"os"`
	StudioVenvDir        string            `json:"studio_venv_dir"`
	RequirementsFiles    []string          `json:"requirements_files"`
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
}))`

type probeJSON struct {
	Executable       string `json:"executable"`
	SysPrefix        string `json:"sys_prefix"`
	Version          string `json:"version"`
	VersionOK        bool   `json:"version_ok"`
	HasBuild123d     bool   `json:"has_build123d"`
	Build123dVersion string `json:"build123d_version"`
	HasOCPTessellate bool   `json:"has_ocp_tessellate"`
}

type rawCandidate struct {
	path   string
	source string
	detail string
}

// FindUV locates the `uv` executable on PATH, in Studio's managed tools directory,
// or in standard per-OS installation directories.
func FindUV() (string, bool) {
	home, _ := os.UserHomeDir()
	if home == "" && runtime.GOOS == "windows" {
		home = strings.TrimSpace(os.Getenv("USERPROFILE"))
	}
	studioUV, _ := config.StudioUVPath()
	return findUVForOS(
		runtime.GOOS,
		home,
		strings.TrimSpace(os.Getenv("LOCALAPPDATA")),
		strings.TrimSpace(os.Getenv("ProgramFiles")),
		studioUV,
		exec.LookPath,
	)
}

func findUVForOS(
	goos string,
	home string,
	localAppData string,
	programFiles string,
	studioUV string,
	lookPath func(string) (string, error),
) (string, bool) {
	if lookPath != nil {
		if p, err := lookPath("uv"); err == nil && strings.TrimSpace(p) != "" {
			return p, true
		}
	}

	var candidates []string
	if studioUV != "" {
		candidates = append(candidates, studioUV)
	}
	if goos == "windows" {
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "uv.exe"),
				filepath.Join(home, ".cargo", "bin", "uv.exe"),
			)
		}
		if localAppData != "" {
			candidates = append(candidates,
				filepath.Join(localAppData, "uv", "uv.exe"),
				filepath.Join(localAppData, "Programs", "uv", "uv.exe"),
			)
		}
		if programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "uv", "uv.exe"))
		}
	} else {
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "uv"),
				filepath.Join(home, ".cargo", "bin", "uv"),
			)
		}
		candidates = append(candidates,
			"/opt/homebrew/bin/uv",
			"/usr/local/bin/uv",
			"/usr/bin/uv",
		)
	}

	for _, c := range candidates {
		if c == "" {
			continue
		}
		if isExecutableFile(c, goos) {
			return c, true
		}
	}
	return "", false
}

func isExecutableFile(p, goos string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
}

// isWindowsStoreStub returns true for Windows Store App Execution Alias python*.exe stubs.
func isWindowsStoreStub(p string) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"))
	return strings.Contains(norm, "/microsoft/windowsapps/")
}

// FindRequirementsFiles returns deduplicated requirements.txt files present in libraryRoots.
func FindRequirementsFiles(libraryRoots []string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, root := range libraryRoots {
		clean := ExpandPath(root)
		if clean == "" {
			continue
		}
		reqFile := filepath.Join(clean, "requirements.txt")
		key := reqFile
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		if st, err := os.Stat(reqFile); err == nil && !st.IsDir() {
			seen[key] = struct{}{}
			out = append(out, reqFile)
		}
	}
	return out
}

// Probe inspects library roots and discovers candidate Python interpreters plus uv availability.
func Probe(ctx context.Context, libraryRoots []string, explicitPython string) ProbeResult {
	studioVenv, _ := config.StudioVenvDir()
	res := ProbeResult{
		Candidates:           []PythonCandidate{},
		ManagedPythonVersion: ManagedPythonVersion,
		OS:                   runtime.GOOS,
		StudioVenvDir:        studioVenv,
		RequirementsFiles:    FindRequirementsFiles(libraryRoots),
	}

	if uvPath, ok := FindUV(); ok {
		res.HasUV = true
		res.UVPath = uvPath
	}

	raw := collectCandidatePaths(ctx, libraryRoots, explicitPython)
	if len(raw) == 0 {
		return res
	}

	probed := make([]PythonCandidate, len(raw))
	var wg sync.WaitGroup
	for i, rc := range raw {
		wg.Add(1)
		go func(idx int, c rawCandidate) {
			defer wg.Done()
			probed[idx] = ProbeExecutable(ctx, c.path, c.source, c.detail)
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

	rankCandidates(candidates)
	res.Candidates = candidates
	res.Recommended = pickRecommended(candidates)
	return res
}

// ProbeExecutable runs a fast metadata probe against a single Python executable.
func ProbeExecutable(ctx context.Context, pythonPath, source, sourceDetail string) PythonCandidate {
	cleanPath := ExpandPath(pythonPath)
	cand := PythonCandidate{
		Path:         cleanPath,
		Source:       source,
		SourceDetail: sourceDetail,
	}
	if cleanPath == "" {
		cand.Error = "empty python path"
		return cand
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, cleanPath, "-c", probeSnippet)
	configureCmd(cmd)
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
	cand.SysPrefix = parsed.SysPrefix
	return cand
}

var versionedPythonRe = regexp.MustCompile(`^python3\.(\d+)$`)

// pythonMinorPriority ranks Python versions by build123d/OCP compatibility
// (managed target 3.14 > 3.13 > 3.12 > 3.11 > 3.10 > newer untargeted minors).
func pythonMinorPriority(major, minor int) int {
	if major < 3 {
		return -200
	}
	if major > 3 {
		return 10
	}
	switch {
	case minor == 14:
		return 100
	case minor == 13:
		return 95
	case minor == 12:
		return 90
	case minor == 11:
		return 85
	case minor == 10:
		return 80
	case minor >= 15:
		return 40
	default:
		return -100
	}
}

func parseMajorMinor(version string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

// findVersionedPythons scans directories for python3.<minor> binaries
// and returns them ordered by tested compatibility priority (3.12 > 3.13 > 3.11 > 3.10 > 3.14 > 3.15+).
func findVersionedPythons(dirs []string) []string {
	type entry struct {
		path  string
		minor int
		prio  int
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
			found = append(found, entry{
				path:  full,
				minor: minor,
				prio:  pythonMinorPriority(3, minor),
			})
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].prio != found[j].prio {
			return found[i].prio > found[j].prio
		}
		return found[i].minor > found[j].minor
	})
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.path)
	}
	return out
}

func parsePyLauncherOutput(out string) []string {
	var paths []string
	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		// py.exe -0p lines look like: " -V:3.12 *        C:\Python312\python.exe"
		if idx := strings.Index(line, "\t"); idx >= 0 {
			line = strings.TrimSpace(line[idx:])
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// Path may contain spaces; look for token starting with drive letter or UNC
		exePath := ""
		for i, f := range fields {
			if len(f) >= 3 && f[1] == ':' && (f[2] == '\\' || f[2] == '/') {
				exePath = strings.Join(fields[i:], " ")
				break
			}
		}
		if exePath == "" {
			last := fields[len(fields)-1]
			if strings.HasSuffix(strings.ToLower(last), ".exe") {
				exePath = last
			}
		}
		if exePath != "" {
			paths = append(paths, strings.Trim(exePath, `"`))
		}
	}
	return paths
}

func discoverPyLauncherPaths(ctx context.Context) []string {
	pyExe, err := exec.LookPath("py.exe")
	if err != nil || pyExe == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, pyExe, "-0p")
	configureCmd(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parsePyLauncherOutput(string(out))
}

func collectCandidatePaths(ctx context.Context, libraryRoots []string, explicitPython string) []rawCandidate {
	var out []rawCandidate
	seenPath := map[string]struct{}{}

	add := func(p, source, detail string) {
		p = ExpandPath(p)
		if p == "" {
			return
		}
		if source != "configured" && isWindowsStoreStub(p) {
			return
		}
		if !filepath.IsAbs(p) {
			resolved, err := exec.LookPath(p)
			if err != nil {
				if source == "configured" {
					out = append(out, rawCandidate{path: p, source: source, detail: detail})
				}
				return
			}
			p = resolved
			if source != "configured" && isWindowsStoreStub(p) {
				return
			}
		} else {
			st, err := os.Stat(p)
			if err != nil || st.IsDir() {
				if source == "configured" {
					out = append(out, rawCandidate{path: p, source: source, detail: detail})
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
		out = append(out, rawCandidate{path: p, source: source, detail: detail})
	}

	// 1. Explicitly configured interpreter
	if strings.TrimSpace(explicitPython) != "" {
		add(explicitPython, "configured", "")
	}

	// 2. Shared Studio-managed virtual environment
	if studioPy := FindStudioVenvPython(); studioPy != "" {
		add(studioPy, "studio_venv", "")
	}

	// 3. Existing virtual environments inside configured libraries
	for _, root := range libraryRoots {
		cleanRoot := ExpandPath(root)
		if cleanRoot == "" {
			continue
		}
		if venvPy := FindProjectVenvPython(cleanRoot); venvPy != "" {
			add(venvPy, "library_venv", filepath.Base(cleanRoot))
		}
	}

	// 4. PATH lookups (default python3/python plus any versioned python3.X binaries)
	pathNames := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		pathNames = []string{"python", "python3"}
	}
	for _, name := range pathNames {
		if p, err := exec.LookPath(name); err == nil {
			add(p, "path", "")
		}
	}
	if runtime.GOOS != "windows" {
		for _, p := range findVersionedPythons(filepath.SplitList(os.Getenv("PATH"))) {
			add(p, "path", "")
		}
	}

	// 5. Conda, pyenv, uv-managed, and standard OS install locations
	home, _ := os.UserHomeDir()
	if condaPrefix := strings.TrimSpace(os.Getenv("CONDA_PREFIX")); condaPrefix != "" {
		if runtime.GOOS == "windows" {
			add(filepath.Join(condaPrefix, "python.exe"), "conda", "")
		} else {
			add(filepath.Join(condaPrefix, "bin", "python"), "conda", "")
		}
	}

	if runtime.GOOS == "windows" {
		for _, p := range discoverPyLauncherPaths(ctx) {
			add(p, "system", "")
		}
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			uvMatches, _ := filepath.Glob(filepath.Join(appData, "uv", "python", "*", "python.exe"))
			sort.Sort(sort.Reverse(sort.StringSlice(uvMatches)))
			for _, m := range uvMatches {
				add(m, "system", "")
			}
		}
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			uvMatches, _ := filepath.Glob(filepath.Join(localAppData, "uv", "python", "*", "python.exe"))
			sort.Sort(sort.Reverse(sort.StringSlice(uvMatches)))
			for _, m := range uvMatches {
				add(m, "system", "")
			}
			matches, _ := filepath.Glob(filepath.Join(localAppData, "Programs", "Python", "Python3*", "python.exe"))
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			for _, m := range matches {
				add(m, "system", "")
			}
		}
		if home != "" {
			for _, dir := range []string{"miniconda3", "anaconda3", "miniforge3", "mambaforge"} {
				add(filepath.Join(home, dir, "python.exe"), "conda", "")
			}
		}
	} else {
		if home != "" {
			add(filepath.Join(home, ".pyenv", "shims", "python3"), "pyenv", "")
			pyenvMatches, _ := filepath.Glob(filepath.Join(home, ".pyenv", "versions", "*", "bin", "python3"))
			sort.Sort(sort.Reverse(sort.StringSlice(pyenvMatches)))
			for _, m := range pyenvMatches {
				add(m, "pyenv", "")
			}
			uvMatches, _ := filepath.Glob(filepath.Join(home, ".local", "share", "uv", "python", "*", "bin", "python3"))
			sort.Sort(sort.Reverse(sort.StringSlice(uvMatches)))
			for _, m := range uvMatches {
				add(m, "system", "")
			}
			for _, dir := range []string{"miniconda3", "anaconda3", "miniforge3", "mambaforge"} {
				add(filepath.Join(home, dir, "bin", "python"), "conda", "")
			}
		}
		sysDirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}
		for _, dir := range sysDirs {
			add(filepath.Join(dir, "python3"), "system", "")
		}
		for _, p := range findVersionedPythons(sysDirs) {
			add(p, "system", "")
		}
	}

	return out
}

func sourcePriority(source string) int {
	switch source {
	case "configured":
		return 0
	case "studio_venv":
		return 1
	case "library_venv":
		return 2
	case "path":
		return 3
	case "conda":
		return 4
	case "pyenv":
		return 5
	default:
		return 6
	}
}

func candidateScore(c PythonCandidate) int {
	if c.Error != "" {
		return -1000
	}
	score := 0
	if c.VersionOK {
		score += 100
		if maj, min, ok := parseMajorMinor(c.Version); ok {
			score += pythonMinorPriority(maj, min) / 2
		}
	}
	if c.VersionOK && c.HasBuild123d {
		score += 500
		if c.HasOCPTessellate {
			score += 20
		}
	}
	score -= sourcePriority(c.Source) * 5
	return score
}

func rankCandidates(candidates []PythonCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		si := candidateScore(candidates[i])
		sj := candidateScore(candidates[j])
		if si != sj {
			return si > sj
		}
		return sourcePriority(candidates[i].Source) < sourcePriority(candidates[j].Source)
	})
}

func pickRecommended(candidates []PythonCandidate) *PythonCandidate {
	for i := range candidates {
		c := candidates[i]
		if c.Error == "" && c.VersionOK && c.HasBuild123d {
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
