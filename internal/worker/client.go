package worker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const workerStartupTimeout = 30 * time.Second

// Manager supervises the persistent Python worker process and provides typed HTTP calls.
type Manager struct {
	lifecycleMu sync.Mutex // serializes Start / Restart / CancelAndRestart / Stop
	stateMu     sync.RWMutex

	workerScript     []byte
	scriptDir        string
	scriptPath       string
	pythonPath       string
	libraries        []LibrarySpec
	cmd              *exec.Cmd
	proc             *procHandle
	doneCh           chan struct{}
	baseURL          string
	authToken        string
	workerGen        uint64
	starting         bool
	lastErr          string
	onUnexpectedExit func(string)
	httpClient       *http.Client
}

// NewManager creates a new Python worker manager using the embedded worker.py script bytes.
func NewManager(workerScript []byte) *Manager {
	return &Manager{
		workerScript: workerScript,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				Proxy: nil,
			},
		},
	}
}

// SetOnUnexpectedExit registers an optional callback invoked when the worker crashes mid-session.
func (m *Manager) SetOnUnexpectedExit(fn func(string)) {
	m.stateMu.Lock()
	m.onUnexpectedExit = fn
	m.stateMu.Unlock()
}

func cloneLibraries(libs []LibrarySpec) []LibrarySpec {
	out := make([]LibrarySpec, len(libs))
	copy(out, libs)
	return out
}

func generateWorkerToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func envWithWorkerToken(token string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(entry, "CODE_CAD_WORKER_TOKEN=") {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, "CODE_CAD_WORKER_TOKEN="+token)
	return out
}

func (m *Manager) ensureScriptPath() (string, error) {
	if m.scriptPath != "" {
		if _, err := os.Stat(m.scriptPath); err == nil {
			return m.scriptPath, nil
		}
	}
	m.cleanupScriptDirLocked()

	dir, err := os.MkdirTemp("", "code-cad-studio-worker-*")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "worker.py")
	if err := os.WriteFile(p, m.workerScript, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	m.scriptDir = dir
	m.scriptPath = p
	return p, nil
}

func (m *Manager) cleanupScriptDirLocked() {
	if m.scriptDir != "" {
		_ = os.RemoveAll(m.scriptDir)
		m.scriptDir = ""
	}
	m.scriptPath = ""
}

// Start launches the persistent Python worker subprocess and waits for READY <port>.
func (m *Manager) Start(pythonPath string, libraries []LibrarySpec) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	return m.startLocked(context.Background(), pythonPath, libraries)
}

// EnsureStarted guarantees a single running worker without killing an in-flight
// startup. Concurrent callers wait for an active Start to finish and reuse it.
func (m *Manager) EnsureStarted(pythonPath string, libraries []LibrarySpec) error {
	m.stateMu.RLock()
	if m.baseURL != "" {
		m.stateMu.RUnlock()
		return nil
	}
	m.stateMu.RUnlock()

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.stateMu.RLock()
	alreadyRunning := m.baseURL != ""
	m.stateMu.RUnlock()
	if alreadyRunning {
		return nil
	}
	return m.startLocked(context.Background(), pythonPath, libraries)
}

func formatLogSnippet(line string) string {
	const maxLogBytes = 512
	if len(line) <= maxLogBytes {
		return line
	}
	return fmt.Sprintf("%s... (%d bytes)", line[:maxLogBytes], len(line))
}

func (m *Manager) startLocked(ctx context.Context, pythonPath string, libraries []LibrarySpec) error {
	m.stopLocked()

	libsCopy := cloneLibraries(libraries)
	m.stateMu.Lock()
	m.pythonPath = pythonPath
	m.libraries = libsCopy
	m.workerGen++
	m.starting = true
	m.lastErr = ""
	m.stateMu.Unlock()

	defer func() {
		m.stateMu.Lock()
		m.starting = false
		m.stateMu.Unlock()
	}()

	if err := ctx.Err(); err != nil {
		m.setErr(err.Error())
		return err
	}

	scriptPath, err := m.ensureScriptPath()
	if err != nil {
		m.setErr(err.Error())
		return err
	}

	token, err := generateWorkerToken()
	if err != nil {
		m.setErr(err.Error())
		return err
	}

	args := []string{"-u", scriptPath, "--port", "0"}
	if len(libsCopy) > 0 {
		if b, merr := json.Marshal(libsCopy); merr == nil {
			args = append(args, "--libraries-json", string(b))
		}
	}

	cmd := exec.Command(pythonPath, args...)
	configureCmd(cmd)
	cmd.Env = envWithWorkerToken(token)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.setErr(err.Error())
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.setErr(err.Error())
		return err
	}

	if err := cmd.Start(); err != nil {
		msg := fmt.Sprintf("failed to start python (%s): %v", pythonPath, err)
		m.setErr(msg)
		return errors.New(msg)
	}

	proc := attachProcess(cmd)
	doneCh := make(chan struct{})
	var ioWG sync.WaitGroup
	ioWG.Add(2)

	// Drain stderr with unbounded bufio.Reader so long lines (>128 KB) never deadlock.
	go func() {
		defer ioWG.Done()
		br := bufio.NewReaderSize(stderr, 64*1024)
		for {
			line, rerr := br.ReadString('\n')
			if len(line) > 0 {
				trimmed := strings.TrimRight(line, "\r\n")
				if trimmed != "" {
					log.Printf("[python] %s", formatLogSnippet(trimmed))
				}
			}
			if rerr != nil {
				break
			}
		}
	}()

	readyCh := make(chan int, 1)
	errCh := make(chan error, 1)

	// Drain stdout with unbounded bufio.Reader and capture READY <port>.
	go func() {
		defer ioWG.Done()
		br := bufio.NewReaderSize(stdout, 64*1024)
		signaled := false
		for {
			line, rerr := br.ReadString('\n')
			if len(line) > 0 {
				trimmed := strings.TrimSpace(line)
				if !signaled && strings.HasPrefix(trimmed, "READY ") {
					portStr := strings.TrimPrefix(trimmed, "READY ")
					port, perr := strconv.Atoi(strings.TrimSpace(portStr))
					if perr != nil {
						signaled = true
						errCh <- fmt.Errorf("invalid READY port %q: %w", portStr, perr)
					} else {
						signaled = true
						readyCh <- port
					}
				} else if trimmed != "" {
					log.Printf("[python:out] %s", formatLogSnippet(trimmed))
				}
			}
			if rerr != nil {
				if !signaled {
					if errors.Is(rerr, io.EOF) {
						errCh <- errors.New("python worker exited before READY signal")
					} else {
						errCh <- rerr
					}
				}
				break
			}
		}
	}()

	// Single waiter goroutine reaps the child once pipe readers finish.
	go func() {
		ioWG.Wait()
		waitErr := cmd.Wait()
		proc.close()
		close(doneCh)
		m.onProcessExited(cmd, waitErr)
	}()

	timer := time.NewTimer(workerStartupTimeout)
	defer timer.Stop()

	select {
	case port := <-readyCh:
		m.stateMu.Lock()
		select {
		case <-doneCh:
			m.stateMu.Unlock()
			err := errors.New("python worker exited immediately after READY signal")
			m.setErr(err.Error())
			return err
		default:
		}
		m.cmd = cmd
		m.proc = proc
		m.doneCh = doneCh
		m.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
		m.authToken = token
		m.stateMu.Unlock()
		log.Printf("Python worker started (%s) on http://127.0.0.1:%d", pythonPath, port)
		return nil

	case err := <-errCh:
		proc.kill()
		<-doneCh
		m.setErr(err.Error())
		return err

	case <-ctx.Done():
		proc.kill()
		<-doneCh
		m.setErr(ctx.Err().Error())
		return ctx.Err()

	case <-timer.C:
		proc.kill()
		<-doneCh
		msg := "timed out waiting for python worker startup (30s)"
		m.setErr(msg)
		return errors.New(msg)
	}
}

func (m *Manager) onProcessExited(cmd *exec.Cmd, waitErr error) {
	var cb func(string)
	var errMsg string

	m.stateMu.Lock()
	if m.cmd == cmd {
		m.cmd = nil
		m.proc = nil
		m.doneCh = nil
		m.baseURL = ""
		m.authToken = ""
		m.workerGen++
		if waitErr != nil {
			errMsg = fmt.Sprintf("python worker exited unexpectedly: %v", waitErr)
		} else {
			errMsg = "python worker exited unexpectedly"
		}
		m.lastErr = errMsg
		cb = m.onUnexpectedExit
	}
	m.stateMu.Unlock()

	if cb != nil && errMsg != "" {
		cb(errMsg)
	}
}

func (m *Manager) setErr(msg string) {
	m.stateMu.Lock()
	m.lastErr = msg
	m.stateMu.Unlock()
}

// Restart kills and restarts the Python worker (e.g. on Python interpreter change).
func (m *Manager) Restart(pythonPath string, libraries []LibrarySpec) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.stateMu.RLock()
	curPy := m.pythonPath
	curLibs := cloneLibraries(m.libraries)
	m.stateMu.RUnlock()

	if pythonPath == "" {
		pythonPath = curPy
	}
	if libraries == nil {
		libraries = curLibs
	}
	return m.startLocked(context.Background(), pythonPath, libraries)
}

// CancelAndRestart immediately kills the active worker process tree (unblocking any wedged
// build/render request) and spawns a fresh worker with the last configured interpreter & libraries.
func (m *Manager) CancelAndRestart(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	m.stateMu.RLock()
	curPy := m.pythonPath
	curLibs := cloneLibraries(m.libraries)
	m.stateMu.RUnlock()

	if strings.TrimSpace(curPy) == "" {
		return errors.New("python interpreter not configured")
	}

	m.stopLocked()
	return m.startLocked(ctx, curPy, curLibs)
}

// ConfigureOrRestart updates libraries on the warm worker when the Python interpreter
// is unchanged, avoiding a ~4s cold build123d re-import. Falls back to Restart otherwise.
func (m *Manager) ConfigureOrRestart(ctx context.Context, pythonPath string, libraries []LibrarySpec) error {
	libsCopy := cloneLibraries(libraries)

	m.stateMu.Lock()
	running := m.baseURL != ""
	curPy := m.pythonPath
	genBefore := m.workerGen
	if running && curPy == pythonPath {
		m.libraries = libsCopy
	}
	m.stateMu.Unlock()

	if running && curPy == pythonPath {
		resp, err := m.Configure(ctx, libsCopy)
		if err == nil && resp.OK {
			m.stateMu.Lock()
			sameWorker := m.workerGen == genBefore && m.baseURL != ""
			if sameWorker {
				m.libraries = libsCopy
				m.lastErr = ""
			}
			m.stateMu.Unlock()
			if sameWorker {
				return nil
			}
		}
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.stateMu.RLock()
	stillRunningSamePy := m.baseURL != "" && m.pythonPath == pythonPath
	m.stateMu.RUnlock()
	if stillRunningSamePy {
		resp, err := m.Configure(ctx, libsCopy)
		if err == nil && resp.OK {
			m.stateMu.Lock()
			m.libraries = libsCopy
			m.lastErr = ""
			m.stateMu.Unlock()
			return nil
		}
	}

	return m.startLocked(context.Background(), pythonPath, libsCopy)
}

// Stop terminates the Python worker cleanly and removes temporary worker script files.
func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stopLocked()
	m.cleanupScriptDirLocked()
}

// Close terminates the Python worker and cleans up temporary files.
func (m *Manager) Close() {
	m.Stop()
}

func (m *Manager) stopLocked() {
	m.stateMu.Lock()
	cmd := m.cmd
	proc := m.proc
	doneCh := m.doneCh
	m.cmd = nil
	m.proc = nil
	m.doneCh = nil
	m.baseURL = ""
	m.authToken = ""
	m.workerGen++
	m.stateMu.Unlock()

	if proc != nil {
		proc.kill()
	} else if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if doneCh != nil {
		<-doneCh
	}
}

// Status returns whether the worker is running and any startup/crash error without blocking on startup.
func (m *Manager) Status() (bool, string) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.baseURL != "", m.lastErr
}

// Starting returns true while the Python worker subprocess is booting.
func (m *Manager) Starting() bool {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.starting
}

func (m *Manager) getConnection() (string, string, error) {
	m.stateMu.RLock()
	base := m.baseURL
	token := m.authToken
	lastErr := m.lastErr
	m.stateMu.RUnlock()

	if base == "" {
		if lastErr != "" {
			return "", "", errors.New(lastErr)
		}
		return "", "", errors.New("python worker is not running")
	}
	return base, token, nil
}

func (m *Manager) doJSON(ctx context.Context, method, path string, reqBody any, dst any) error {
	base, token, err := m.getConnection()
	if err != nil {
		return err
	}

	var body io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("worker HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (m *Manager) Health(ctx context.Context) (*HealthResponse, error) {
	var out HealthResponse
	if err := m.doJSON(ctx, http.MethodGet, "/health", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (m *Manager) Configure(ctx context.Context, libraries []LibrarySpec) (*ModelsResponse, error) {
	if libraries == nil {
		libraries = []LibrarySpec{}
	}
	var out ModelsResponse
	err := m.doJSON(ctx, http.MethodPost, "/configure", map[string]any{
		"libraries": libraries,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Reload evicts project modules and clears caches inside the running worker without restarting Python.
func (m *Manager) Reload(ctx context.Context) error {
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := m.doJSON(ctx, http.MethodPost, "/reload", map[string]any{}, &out); err != nil {
		return err
	}
	if !out.OK {
		return errors.New(out.Error)
	}
	return nil
}

func (m *Manager) ListModels(ctx context.Context) (*ModelsResponse, error) {
	var out ModelsResponse
	if err := m.doJSON(ctx, http.MethodGet, "/models", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (m *Manager) GetSchema(ctx context.Context, modelID string) (*SchemaResponse, error) {
	var out SchemaResponse
	p := "/models/" + url.PathEscape(modelID) + "/schema"
	if err := m.doJSON(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RenderRaw forwards a render request to the Python worker and returns the raw HTTP status code
// and JSON response body without unmarshaling or re-marshaling large geometry buffers.
func (m *Manager) RenderRaw(ctx context.Context, modelID string, params map[string]any) (int, []byte, error) {
	base, token, err := m.getConnection()
	if err != nil {
		return 0, nil, err
	}
	if params == nil {
		params = map[string]any{}
	}
	bodyBytes, err := json.Marshal(map[string]any{"params": params})
	if err != nil {
		return 0, nil, err
	}
	p := "/models/" + url.PathEscape(modelID) + "/render"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+p, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func (m *Manager) Render(ctx context.Context, modelID string, params map[string]any) (*RenderResponse, error) {
	var out RenderResponse
	p := "/models/" + url.PathEscape(modelID) + "/render"
	if err := m.doJSON(ctx, http.MethodPost, p, map[string]any{"params": params}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func fallbackStemFromID(modelID string) string {
	s := strings.TrimSpace(modelID)
	if idx := strings.Index(s, "::"); idx >= 0 {
		s = s[idx+2:]
	}
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	if idx := strings.LastIndex(s, "__"); idx >= 0 {
		s = s[idx+2:]
	}
	s = strings.TrimSpace(s)

	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	clean := strings.Trim(b.String(), "._ ")
	if clean == "" {
		return "model"
	}
	return clean
}

func (m *Manager) ExportName(ctx context.Context, modelID string, params map[string]any) (string, string, error) {
	var out ExportNameResponse
	p := "/models/" + url.PathEscape(modelID) + "/export-name"
	if err := m.doJSON(ctx, http.MethodPost, p, map[string]any{"params": params}, &out); err != nil {
		return fallbackStemFromID(modelID), "", err
	}
	stem := strings.TrimSpace(out.ExportFilename)
	if !out.OK || stem == "" {
		stem = fallbackStemFromID(modelID)
	}
	return stem, strings.TrimSpace(out.LibraryID), nil
}

func (m *Manager) UpdateDefaults(ctx context.Context, modelID string, params map[string]any, defaultPreset *DefaultPresetUpdate, dryRun bool, expectedSHA256 string) (*UpdateDefaultsResponse, error) {
	var out UpdateDefaultsResponse
	p := "/models/" + url.PathEscape(modelID) + "/update-defaults"
	payload := map[string]any{
		"params":          params,
		"dry_run":         dryRun,
		"expected_sha256": expectedSHA256,
	}
	if defaultPreset != nil {
		payload["default_preset"] = defaultPreset
	}
	err := m.doJSON(ctx, http.MethodPost, p, payload, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (m *Manager) RenameModel(ctx context.Context, modelID string, name, fileStem *string) (*RenameModelResponse, error) {
	var out RenameModelResponse
	p := "/models/" + url.PathEscape(modelID) + "/rename"
	payload := map[string]any{}
	if name != nil {
		payload["name"] = *name
	}
	if fileStem != nil {
		payload["file_stem"] = *fileStem
	}
	if err := m.doJSON(ctx, http.MethodPost, p, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func buildExportPayload(params map[string]any, opts ExportOptions) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	payload := map[string]any{
		"params": params,
		"format": opts.Format,
	}
	if opts.OutputPath != "" {
		payload["output_path"] = opts.OutputPath
	}
	if q := strings.TrimSpace(opts.Quality); q != "" {
		payload["quality"] = q
	}
	if opts.LinearDeflection != nil {
		payload["linear_deflection"] = *opts.LinearDeflection
	}
	if opts.AngularDeflection != nil {
		payload["angular_deflection"] = *opts.AngularDeflection
	}
	return payload
}

// ExportToPathWithOptions exports a model to disk using configurable tessellation quality options.
func (m *Manager) ExportToPathWithOptions(ctx context.Context, modelID string, params map[string]any, opts ExportOptions) (*ExportResponse, error) {
	var out ExportResponse
	p := "/models/" + url.PathEscape(modelID) + "/export"
	if err := m.doJSON(ctx, http.MethodPost, p, buildExportPayload(params, opts), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (m *Manager) ExportToPath(ctx context.Context, modelID string, params map[string]any, format, outputPath string) (*ExportResponse, error) {
	return m.ExportToPathWithOptions(ctx, modelID, params, ExportOptions{
		Format:     format,
		OutputPath: outputPath,
		Quality:    "standard",
	})
}

// ExportBytesWithOptions exports a model and returns raw bytes using configurable tessellation quality options.
func (m *Manager) ExportBytesWithOptions(ctx context.Context, modelID string, params map[string]any, opts ExportOptions) ([]byte, error) {
	base, token, err := m.getConnection()
	if err != nil {
		return nil, err
	}
	opts.OutputPath = ""
	b, err := json.Marshal(buildExportPayload(params, opts))
	if err != nil {
		return nil, err
	}
	p := "/models/" + url.PathEscape(modelID) + "/export"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+p, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("export failed (HTTP %d): %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

func (m *Manager) ExportBytes(ctx context.Context, modelID string, params map[string]any, format string) ([]byte, error) {
	return m.ExportBytesWithOptions(ctx, modelID, params, ExportOptions{
		Format:  format,
		Quality: "standard",
	})
}
