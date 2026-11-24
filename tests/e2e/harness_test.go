//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	suiteRepoRoot string
	suiteBinDir   string

	serverBuildOnce sync.Once
	serverBinPath   string
	serverBuildErr  error
)

func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: getwd failed: %v\n", err)
		os.Exit(1)
	}
	suiteRepoRoot = filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(suiteRepoRoot, "go.mod")); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: could not locate repo root from %s: %v\n", wd, err)
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp("", "ccs-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: mkdtemp failed: %v\n", err)
		os.Exit(1)
	}
	suiteBinDir = tmpDir

	code := m.Run()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}

func repoPath(parts ...string) string {
	all := append([]string{suiteRepoRoot}, parts...)
	return filepath.Join(all...)
}

func fixturePath(parts ...string) string {
	all := append([]string{suiteRepoRoot, "tests", "e2e", "fixtures"}, parts...)
	return filepath.Join(all...)
}

func ensureFrontendDist(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E_SKIP_FRONTEND_BUILD") == "1" {
		return
	}
	indexPath := repoPath("frontend", "dist", "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		return
	}
	cmd := exec.Command("npm", "--prefix", repoPath("frontend"), "run", "build")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build frontend/dist prerequisite: %v\n%s", err, string(out))
	}
}

func buildServerBinaryOnce(t *testing.T) string {
	t.Helper()
	ensureFrontendDist(t)
	serverBuildOnce.Do(func() {
		outPath := filepath.Join(suiteBinDir, "code-cad-studio-server")
		cmd := exec.Command("go", "build", "-tags", "server", "-o", outPath, ".")
		cmd.Dir = suiteRepoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			serverBuildErr = fmt.Errorf("go build -tags server failed: %w\n%s", err, string(out))
			return
		}
		serverBinPath = outPath
	})
	if serverBuildErr != nil {
		t.Fatalf("%v", serverBuildErr)
	}
	return serverBinPath
}

func resolvePython(t *testing.T) string {
	t.Helper()
	if envPy := strings.TrimSpace(os.Getenv("E2E_PYTHON_PATH")); envPy != "" {
		if _, err := os.Stat(envPy); err != nil {
			t.Fatalf("E2E_PYTHON_PATH=%q is invalid: %v", envPy, err)
		}
		return envPy
	}
	candidates := []string{
		repoPath(".venv", "bin", "python"),
		"/home/luke/repos/code-cad/.venv/bin/python",
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	for _, name := range []string{"python3.14", "python3.13", "python3.12", "python3.11", "python3.10", "python3"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Fatalf("no Python 3.10+ interpreter found on host")
	return ""
}

func cleanPythonEnv(base []string, extraPythonPath string) []string {
	out := make([]string, 0, len(base)+1)
	var existingPP string
	for _, kv := range base {
		if strings.HasPrefix(kv, "PYTHONPATH=") {
			existingPP = strings.TrimPrefix(kv, "PYTHONPATH=")
			continue
		}
		out = append(out, kv)
	}
	if extraPythonPath != "" {
		if existingPP != "" {
			out = append(out, "PYTHONPATH="+extraPythonPath+string(os.PathListSeparator)+existingPP)
		} else {
			out = append(out, "PYTHONPATH="+extraPythonPath)
		}
	}
	return out
}

var (
	realCADMu    sync.Mutex
	realCADCache = map[string]bool{}
)

func hasRealCAD(pythonPath string) bool {
	realCADMu.Lock()
	if cached, ok := realCADCache[pythonPath]; ok {
		realCADMu.Unlock()
		return cached
	}
	realCADMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, pythonPath, "-c", "import build123d, ocp_tessellate")
	cmd.Env = cleanPythonEnv(os.Environ(), "")
	ok := cmd.Run() == nil

	realCADMu.Lock()
	realCADCache[pythonPath] = ok
	realCADMu.Unlock()
	return ok
}

func requireRealCAD(t *testing.T) string {
	t.Helper()
	py := resolvePython(t)
	if !hasRealCAD(py) {
		if os.Getenv("E2E_REQUIRE_CAD") == "1" {
			t.Fatalf("E2E_REQUIRE_CAD=1 set, but build123d/ocp_tessellate unavailable in %s", py)
		}
		t.Skipf("real build123d/ocp_tessellate unavailable in %s; set E2E_PYTHON_PATH and E2E_REQUIRE_CAD=1", py)
	}
	return py
}

func requirePytest(t *testing.T, py string) {
	t.Helper()
	cmd := exec.Command(py, "-m", "pytest", "--version")
	if err := cmd.Run(); err != nil {
		if os.Getenv("E2E_REQUIRE_CAD") == "1" {
			t.Fatalf("E2E_REQUIRE_CAD=1 set, but pytest unavailable in %s: %v", py, err)
		}
		t.Skipf("pytest unavailable in %s: %v", py, err)
	}
}

func requireNPM(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		if os.Getenv("E2E_REQUIRE_CAD") == "1" {
			t.Fatalf("E2E_REQUIRE_CAD=1 set, but npm unavailable: %v", err)
		}
		t.Skipf("npm unavailable: %v", err)
	}
}

func reserveFreePort(t *testing.T) int {
	t.Helper()
	if fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0); err == nil {
		sa := &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}, Port: 0}
		if bindErr := syscall.Bind(fd, sa); bindErr == nil {
			if name, nameErr := syscall.Getsockname(fd); nameErr == nil {
				if inet4, ok := name.(*syscall.SockaddrInet4); ok && inet4.Port > 0 {
					_ = syscall.Close(fd)
					return inet4.Port
				}
			}
		}
		_ = syscall.Close(fd)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 127.0.0.1:0: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func randomHex32(t *testing.T) string {
	t.Helper()
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

func sha256HexFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == "__pycache__" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyDir(%s -> %s): %v", src, dst, err)
	}
}

func encodeModelRoute(baseURL, prefix, modelID, suffix string) string {
	escapedID := url.PathEscape(modelID)
	return strings.TrimRight(baseURL, "/") + prefix + escapedID + suffix
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ---------- Direct Python Worker Harness ----------

type workerOpts struct {
	libraries  []map[string]string
	useMockCAD bool
	token      string // optional override; random 32-byte hex if empty
}

type workerHandle struct {
	t        *testing.T
	cmd      *exec.Cmd
	pgid     int
	baseURL  string
	port     int
	token    string
	client   *http.Client
	stdout   *syncBuffer
	stderr   *syncBuffer
	exitedCh chan struct{}
	waitErr  error
}

func startWorker(t *testing.T, pythonPath string, opts workerOpts) *workerHandle {
	t.Helper()
	token := opts.token
	if token == "" {
		token = randomHex32(t)
	}

	args := []string{repoPath("worker", "worker.py")}
	if len(opts.libraries) > 0 {
		rawLibs, err := json.Marshal(opts.libraries)
		if err != nil {
			t.Fatalf("marshal worker libraries: %v", err)
		}
		args = append(args, "--libraries-json", string(rawLibs))
	}

	cmd := exec.Command(pythonPath, args...)
	cmd.Dir = repoPath("worker")
	extraPP := ""
	if opts.useMockCAD {
		extraPP = fixturePath("mock_cad")
	}
	env := cleanPythonEnv(os.Environ(), extraPP)
	env = append(env,
		"CODE_CAD_WORKER_TOKEN="+token,
		"PYTHONUNBUFFERED=1",
	)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("worker stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("worker stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	pgid := cmd.Process.Pid
	h := &workerHandle{
		t:        t,
		cmd:      cmd,
		pgid:     pgid,
		token:    token,
		client:   &http.Client{Timeout: 30 * time.Second},
		stdout:   &syncBuffer{},
		stderr:   &syncBuffer{},
		exitedCh: make(chan struct{}),
	}

	readyCh := make(chan int, 1)
	readyErrCh := make(chan error, 1)

	var ioWG sync.WaitGroup
	ioWG.Add(2)

	go func() {
		defer ioWG.Done()
		reader := bufio.NewReader(stdoutPipe)
		sentReady := false
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				_, _ = h.stdout.Write([]byte(line))
				trimmed := strings.TrimSpace(line)
				if !sentReady && strings.HasPrefix(trimmed, "READY ") {
					portStr := strings.TrimSpace(strings.TrimPrefix(trimmed, "READY "))
					if p, convErr := strconv.Atoi(portStr); convErr == nil && p > 0 {
						sentReady = true
						readyCh <- p
					}
				}
			}
			if err != nil {
				if !sentReady {
					readyErrCh <- fmt.Errorf("worker stdout closed before READY: %w", err)
				}
				return
			}
		}
	}()

	go func() {
		defer ioWG.Done()
		_, _ = io.Copy(h.stderr, stderrPipe)
	}()

	go func() {
		ioWG.Wait()
		h.waitErr = cmd.Wait()
		close(h.exitedCh)
	}()

	t.Cleanup(func() {
		h.stop()
	})

	select {
	case p := <-readyCh:
		h.port = p
		h.baseURL = fmt.Sprintf("http://127.0.0.1:%d", p)
	case rErr := <-readyErrCh:
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", rErr, h.stdout.String(), h.stderr.String())
	case <-h.exitedCh:
		t.Fatalf("worker exited before READY (%v)\nstdout:\n%s\nstderr:\n%s", h.waitErr, h.stdout.String(), h.stderr.String())
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for worker READY\nstdout:\n%s\nstderr:\n%s", h.stdout.String(), h.stderr.String())
	}

	return h
}

func (h *workerHandle) stop() {
	if h.pgid <= 0 {
		return
	}
	select {
	case <-h.exitedCh:
		h.pgid = 0
		return
	default:
	}
	_ = syscall.Kill(-h.pgid, syscall.SIGTERM)
	select {
	case <-h.exitedCh:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-h.pgid, syscall.SIGKILL)
		select {
		case <-h.exitedCh:
		case <-time.After(2 * time.Second):
		}
	}
	h.pgid = 0
}

func (h *workerHandle) doRaw(method, route string, body []byte, customToken *string, customHost string) (*http.Response, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimRight(h.baseURL, "/")+route, bodyReader)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	tok := h.token
	if customToken != nil {
		tok = *customToken
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if customHost != "" {
		req.Host = customHost
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp, raw, err
}

func (h *workerHandle) getJSON(t *testing.T, route string) (int, map[string]any) {
	t.Helper()
	resp, raw, err := h.doRaw(http.MethodGet, route, nil, nil, "")
	if err != nil {
		t.Fatalf("worker GET %s failed: %v\nstderr:\n%s", route, err, h.stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("worker GET %s invalid JSON (%d): %v\nbody: %s", route, resp.StatusCode, err, string(raw))
	}
	return resp.StatusCode, out
}

func (h *workerHandle) postJSON(t *testing.T, route string, payload any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, raw, err := h.doRaw(http.MethodPost, route, b, nil, "")
	if err != nil {
		t.Fatalf("worker POST %s failed: %v\nstderr:\n%s", route, err, h.stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("worker POST %s invalid JSON (%d): %v\nbody: %s", route, resp.StatusCode, err, string(raw))
	}
	return resp.StatusCode, out
}

// ---------- Go -tags server Harness ----------

type serverOpts struct {
	useMockCAD    bool
	pythonPath    string
	preConfigJSON []byte
	preConfigPath string // relative inside config dir if custom
}

type serverHandle struct {
	t             *testing.T
	cmd           *exec.Cmd
	pgid          int
	pid           int
	baseURL       string
	port          int
	tempRoot      string
	homeDir       string
	xdgConfigHome string
	xdgDataHome   string
	xdgCacheHome  string
	appDataDir    string
	localAppData  string
	exportsDir    string
	workspaceDir  string
	configPath    string
	client        *http.Client
	stdout        *syncBuffer
	stderr        *syncBuffer
	exitedCh      chan struct{}
	waitErr       error
	stopped       bool
	stopOnce      sync.Once
}

var urlPortPattern = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost):(\d+)`)

func startServer(t *testing.T, opts serverOpts) *serverHandle {
	t.Helper()
	bin := buildServerBinaryOnce(t)

	root := t.TempDir()
	return startServerInRoot(t, bin, root, opts)
}

func startServerInRoot(t *testing.T, bin, root string, opts serverOpts) *serverHandle {
	t.Helper()
	homeDir := filepath.Join(root, "home")
	xdgConfig := filepath.Join(root, "xdg_config")
	xdgData := filepath.Join(root, "xdg_data")
	xdgCache := filepath.Join(root, "xdg_cache")
	appData := filepath.Join(root, "appdata")
	localAppData := filepath.Join(root, "localappdata")
	exportsDir := filepath.Join(root, "exports")
	workspaceDir := filepath.Join(root, "workspace")

	for _, d := range []string{homeDir, xdgConfig, xdgData, xdgCache, appData, localAppData, exportsDir, workspaceDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	cfgDir := filepath.Join(xdgConfig, "code-cad-studio")
	cfgFile := filepath.Join(cfgDir, "config.json")
	if opts.preConfigJSON != nil {
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", cfgDir, err)
		}
		target := cfgFile
		if opts.preConfigPath != "" {
			target = filepath.Join(cfgDir, opts.preConfigPath)
		}
		if err := os.WriteFile(target, opts.preConfigJSON, 0o644); err != nil {
			t.Fatalf("write preConfigJSON: %v", err)
		}
	}

	extraPP := ""
	if opts.useMockCAD {
		extraPP = fixturePath("mock_cad")
	}

	const maxBindRetries = 3
	for attempt := 0; attempt <= maxBindRetries; attempt++ {
		port := reserveFreePort(t)
		cmd := exec.Command(bin)
		cmd.Dir = suiteRepoRoot

		env := cleanPythonEnv(os.Environ(), extraPP)
		if os.Getenv("PYTHONUSERBASE") == "" {
			if hostHome := strings.TrimSpace(os.Getenv("HOME")); hostHome != "" {
				env = append(env, "PYTHONUSERBASE="+filepath.Join(hostHome, ".local"))
			}
		}
		env = append(env,
			"HOME="+homeDir,
			"XDG_CONFIG_HOME="+xdgConfig,
			"XDG_DATA_HOME="+xdgData,
			"XDG_CACHE_HOME="+xdgCache,
			"APPDATA="+appData,
			"LOCALAPPDATA="+localAppData,
			"WAILS_SERVER_HOST=127.0.0.1",
			fmt.Sprintf("WAILS_SERVER_PORT=%d", port),
			fmt.Sprintf("PORT=%d", port),
		)
		cmd.Env = env
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		h := &serverHandle{
			t:             t,
			cmd:           cmd,
			port:          port,
			baseURL:       fmt.Sprintf("http://127.0.0.1:%d", port),
			tempRoot:      root,
			homeDir:       homeDir,
			xdgConfigHome: xdgConfig,
			xdgDataHome:   xdgData,
			xdgCacheHome:  xdgCache,
			appDataDir:    appData,
			localAppData:  localAppData,
			exportsDir:    exportsDir,
			workspaceDir:  workspaceDir,
			configPath:    cfgFile,
			client:        &http.Client{Timeout: 30 * time.Second},
			stdout:        &syncBuffer{},
			stderr:        &syncBuffer{},
			exitedCh:      make(chan struct{}),
		}

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("server stdout pipe: %v", err)
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			t.Fatalf("server stderr pipe: %v", err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatalf("start server: %v", err)
		}
		h.pid = cmd.Process.Pid
		h.pgid = cmd.Process.Pid

		detectedPortCh := make(chan int, 2)
		drainAndScan := func(r io.Reader, dst *syncBuffer) {
			br := bufio.NewReader(r)
			for {
				line, err := br.ReadString('\n')
				if line != "" {
					_, _ = dst.Write([]byte(line))
					if m := urlPortPattern.FindStringSubmatch(line); len(m) == 2 {
						if p, convErr := strconv.Atoi(m[1]); convErr == nil && p > 0 {
							select {
							case detectedPortCh <- p:
							default:
							}
						}
					}
				}
				if err != nil {
					return
				}
			}
		}

		var ioWG sync.WaitGroup
		ioWG.Add(2)
		go func() {
			defer ioWG.Done()
			drainAndScan(stdoutPipe, h.stdout)
		}()
		go func() {
			defer ioWG.Done()
			drainAndScan(stderrPipe, h.stderr)
		}()
		go func() {
			ioWG.Wait()
			h.waitErr = cmd.Wait()
			close(h.exitedCh)
		}()

		deadline := time.Now().Add(15 * time.Second)
		retrying := false
		for time.Now().Before(deadline) {
			select {
			case p := <-detectedPortCh:
				h.port = p
				h.baseURL = fmt.Sprintf("http://127.0.0.1:%d", p)
			case <-h.exitedCh:
				combined := strings.ToLower(h.stdout.String() + "\n" + h.stderr.String())
				if attempt < maxBindRetries && strings.Contains(combined, "address already in use") {
					h.stop()
					retrying = true
					break
				}
				h.stop()
				t.Fatalf("server exited prematurely (%v)\nstdout:\n%s\nstderr:\n%s", h.waitErr, h.stdout.String(), h.stderr.String())
			default:
			}
			if retrying {
				break
			}

			resp, err := h.client.Get(h.baseURL + "/api/config")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					t.Cleanup(func() {
						h.stop()
					})
					return h
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if retrying {
			continue
		}

		h.stop()
		t.Fatalf("timed out waiting for server at %s\nstdout:\n%s\nstderr:\n%s", h.baseURL, h.stdout.String(), h.stderr.String())
		return nil
	}

	t.Fatalf("exhausted %d bind retries starting server", maxBindRetries)
	return nil
}

func (h *serverHandle) stop() {
	h.stopOnce.Do(func() {
		if h.stopped || h.pgid <= 0 {
			return
		}
		h.stopped = true
		select {
		case <-h.exitedCh:
			if syscall.Kill(-h.pgid, 0) == nil {
				_ = syscall.Kill(-h.pgid, syscall.SIGKILL)
				time.Sleep(100 * time.Millisecond)
			}
			return
		default:
		}
		_ = syscall.Kill(h.pid, syscall.SIGTERM)
		select {
		case <-h.exitedCh:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-h.pgid, syscall.SIGKILL)
			select {
			case <-h.exitedCh:
			case <-time.After(2 * time.Second):
			}
		}
		// Reap any lingering child processes in the process group.
		if syscall.Kill(-h.pgid, 0) == nil {
			_ = syscall.Kill(-h.pgid, syscall.SIGKILL)
			time.Sleep(100 * time.Millisecond)
		}
	})
}

func (h *serverHandle) restart(t *testing.T, opts serverOpts) *serverHandle {
	t.Helper()
	h.stop()
	bin := buildServerBinaryOnce(t)
	return startServerInRoot(t, bin, h.tempRoot, opts)
}

func (h *serverHandle) doRaw(method, route string, body []byte, headers map[string]string, customHost string) (*http.Response, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimRight(h.baseURL, "/")+route, r)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	if customHost != "" {
		req.Host = customHost
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp, raw, err
}

func (h *serverHandle) getJSON(t *testing.T, route string) (int, map[string]any) {
	t.Helper()
	resp, raw, err := h.doRaw(http.MethodGet, route, nil, nil, "")
	if err != nil {
		t.Fatalf("GET %s failed: %v\nstderr:\n%s", route, err, h.stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("GET %s invalid JSON (%d): %v\nbody: %s", route, resp.StatusCode, err, string(raw))
	}
	return resp.StatusCode, out
}

func (h *serverHandle) postJSON(t *testing.T, route string, payload any) (int, map[string]any) {
	t.Helper()
	return h.requestJSON(t, http.MethodPost, route, payload)
}

func (h *serverHandle) requestJSON(t *testing.T, method, route string, payload any) (int, map[string]any) {
	t.Helper()
	var b []byte
	if payload != nil {
		var err error
		b, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s payload: %v", route, err)
		}
	} else {
		b = []byte("{}")
	}
	resp, raw, err := h.doRaw(method, route, b, nil, "")
	if err != nil {
		t.Fatalf("%s %s failed: %v\nstderr:\n%s", method, route, err, h.stderr.String())
	}
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s invalid JSON (%d): %v\nbody: %s", method, route, resp.StatusCode, err, string(raw))
	}
	return resp.StatusCode, out
}

func (h *serverHandle) configure(t *testing.T, libraries []map[string]string, pythonPath string) map[string]any {
	t.Helper()
	if pythonPath == "" {
		pythonPath = resolvePython(t)
	}
	code, resp := h.postJSON(t, "/api/config", map[string]any{
		"libraries":   libraries,
		"python_path": pythonPath,
		"export_dir":  h.exportsDir,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/config returned %d: %v", code, resp)
	}
	return resp
}

func (h *serverHandle) findWorkerPIDs(t *testing.T) []int {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,pgid=,args=").Output()
	if err != nil {
		t.Fatalf("ps failed: %v", err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		pgid, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		if pid == h.pid {
			continue
		}
		args := strings.Join(fields[3:], " ")
		if (ppid == h.pid || pgid == h.pgid) && (strings.Contains(args, "worker.py") || strings.Contains(args, "python")) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ---------- SSE Client Helper ----------

type sseEvent struct {
	ID      string
	Event   string
	Data    string
	Comment string
}

type sseClient struct {
	events chan sseEvent
	errs   chan error
	cancel context.CancelFunc
	resp   *http.Response
}

func (h *serverHandle) connectSSE(t *testing.T) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("new SSE request: %v", err)
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}

	sc := &sseClient{
		events: make(chan sseEvent, 128),
		errs:   make(chan error, 1),
		cancel: cancel,
		resp:   resp,
	}

	go func() {
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		var cur sseEvent
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				trimmed := strings.TrimRight(line, "\r\n")
				if trimmed == "" {
					if cur.ID != "" || cur.Event != "" || cur.Data != "" || cur.Comment != "" {
						select {
						case sc.events <- cur:
						case <-ctx.Done():
							return
						}
						cur = sseEvent{}
					}
				} else if strings.HasPrefix(trimmed, ":") {
					cmt := strings.TrimSpace(strings.TrimPrefix(trimmed, ":"))
					select {
					case sc.events <- sseEvent{Comment: cmt}:
					case <-ctx.Done():
						return
					}
				} else if strings.HasPrefix(trimmed, "id:") {
					cur.ID = strings.TrimSpace(strings.TrimPrefix(trimmed, "id:"))
				} else if strings.HasPrefix(trimmed, "event:") {
					cur.Event = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
				} else if strings.HasPrefix(trimmed, "data:") {
					d := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
					if cur.Data == "" {
						cur.Data = d
					} else {
						cur.Data += "\n" + d
					}
				}
			}
			if err != nil {
				sc.errs <- err
				return
			}
		}
	}()

	t.Cleanup(func() {
		sc.close()
	})

	return sc
}

func (sc *sseClient) close() {
	sc.cancel()
	if sc.resp != nil && sc.resp.Body != nil {
		_ = sc.resp.Body.Close()
	}
}

func (sc *sseClient) waitForEvent(t *testing.T, timeout time.Duration, pred func(sseEvent) bool) sseEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-sc.events:
			if pred(evt) {
				return evt
			}
		case err := <-sc.errs:
			t.Fatalf("SSE stream ended while waiting for event: %v", err)
		case <-deadline:
			t.Fatalf("timed out after %v waiting for SSE event", timeout)
		}
	}
}

// JSON helpers

func jsonMap(t *testing.T, v any, name string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected %s to be JSON object, got %T (%v)", name, v, v)
	}
	return m
}

func jsonSlice(t *testing.T, v any, name string) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("expected %s to be JSON array, got %T (%v)", name, v, v)
	}
	return s
}

func jsonBool(m map[string]any, key string) (bool, bool) {
	v, ok := m[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func jsonStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
