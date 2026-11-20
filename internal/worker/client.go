package worker

import (
	"bufio"
	"bytes"
	"context"
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

// Manager supervises the persistent Python worker process and provides typed HTTP calls.
type Manager struct {
	lifecycleMu sync.Mutex // serializes Start / Restart / Stop
	stateMu     sync.RWMutex

	workerScript []byte
	scriptPath   string
	pythonPath   string
	projectRoot  string
	cmd          *exec.Cmd
	baseURL      string
	starting     bool
	lastErr      string
	httpClient   *http.Client
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

func (m *Manager) ensureScriptPath() (string, error) {
	if m.scriptPath != "" {
		if _, err := os.Stat(m.scriptPath); err == nil {
			return m.scriptPath, nil
		}
	}
	dir, err := os.MkdirTemp("", "code-cad-studio-worker-*")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "worker.py")
	if err := os.WriteFile(p, m.workerScript, 0o700); err != nil {
		return "", err
	}
	m.scriptPath = p
	return p, nil
}

// Start launches the persistent Python worker subprocess and waits for READY <port>.
func (m *Manager) Start(pythonPath, projectRoot string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	return m.startLocked(pythonPath, projectRoot)
}

func (m *Manager) startLocked(pythonPath, projectRoot string) error {
	m.stopLocked()

	m.stateMu.Lock()
	m.pythonPath = pythonPath
	m.projectRoot = projectRoot
	m.starting = true
	m.lastErr = ""
	m.stateMu.Unlock()

	defer func() {
		m.stateMu.Lock()
		m.starting = false
		m.stateMu.Unlock()
	}()

	scriptPath, err := m.ensureScriptPath()
	if err != nil {
		m.setErr(err.Error())
		return err
	}

	args := []string{"-u", scriptPath, "--port", "0"}
	if projectRoot != "" {
		args = append(args, "--project-root", projectRoot)
	}

	cmd := exec.Command(pythonPath, args...)
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

	// Forward stderr logs
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[python] %s", sc.Text())
		}
	}()

	readyCh := make(chan int, 1)
	errCh := make(chan error, 1)

	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "READY ") {
				portStr := strings.TrimPrefix(line, "READY ")
				port, perr := strconv.Atoi(strings.TrimSpace(portStr))
				if perr != nil {
					errCh <- fmt.Errorf("invalid READY port %q: %w", portStr, perr)
					return
				}
				readyCh <- port
				// Drain remaining stdout
				for sc.Scan() {
					log.Printf("[python:out] %s", sc.Text())
				}
				return
			}
			log.Printf("[python:out] %s", line)
		}
		if err := sc.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- errors.New("python worker exited before READY signal")
		}
	}()

	select {
	case port := <-readyCh:
		m.stateMu.Lock()
		m.cmd = cmd
		m.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
		m.stateMu.Unlock()
		log.Printf("Python worker started (%s) on http://127.0.0.1:%d", pythonPath, port)
		return nil
	case err := <-errCh:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		m.setErr(err.Error())
		return err
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		msg := "timed out waiting for python worker startup (30s)"
		m.setErr(msg)
		return errors.New(msg)
	}
}

func (m *Manager) setErr(msg string) {
	m.stateMu.Lock()
	m.lastErr = msg
	m.stateMu.Unlock()
}

// Restart kills and restarts the Python worker (e.g. on Python interpreter change).
func (m *Manager) Restart(pythonPath, projectRoot string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.stateMu.RLock()
	curPy := m.pythonPath
	curRoot := m.projectRoot
	m.stateMu.RUnlock()

	if pythonPath == "" {
		pythonPath = curPy
	}
	if projectRoot == "" {
		projectRoot = curRoot
	}
	return m.startLocked(pythonPath, projectRoot)
}

// ConfigureOrRestart updates the project root on the warm worker when the Python interpreter
// is unchanged, avoiding a ~4s cold build123d re-import. Falls back to Restart otherwise.
func (m *Manager) ConfigureOrRestart(ctx context.Context, pythonPath, projectRoot string) error {
	m.stateMu.RLock()
	running := m.baseURL != ""
	curPy := m.pythonPath
	m.stateMu.RUnlock()

	if running && curPy == pythonPath && projectRoot != "" {
		resp, err := m.Configure(ctx, projectRoot)
		if err == nil && resp.OK {
			m.stateMu.Lock()
			m.projectRoot = projectRoot
			m.lastErr = ""
			m.stateMu.Unlock()
			return nil
		}
	}
	return m.Restart(pythonPath, projectRoot)
}

// Stop terminates the Python worker cleanly.
func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stopLocked()
}

func (m *Manager) stopLocked() {
	m.stateMu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.baseURL = ""
	m.stateMu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// Status returns whether the worker is running and any startup error without blocking on startup.
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

func (m *Manager) getBaseURL() (string, error) {
	m.stateMu.RLock()
	base := m.baseURL
	lastErr := m.lastErr
	m.stateMu.RUnlock()

	if base == "" {
		if lastErr != "" {
			return "", errors.New(lastErr)
		}
		return "", errors.New("python worker is not running")
	}
	return base, nil
}

func (m *Manager) doJSON(ctx context.Context, method, path string, reqBody any, dst any) error {
	base, err := m.getBaseURL()
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

func (m *Manager) Configure(ctx context.Context, projectRoot string) (*ModelsResponse, error) {
	var out ModelsResponse
	err := m.doJSON(ctx, http.MethodPost, "/configure", map[string]string{
		"project_root": projectRoot,
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

func (m *Manager) Render(ctx context.Context, modelID string, params map[string]any) (*RenderResponse, error) {
	var out RenderResponse
	p := "/models/" + url.PathEscape(modelID) + "/render"
	if err := m.doJSON(ctx, http.MethodPost, p, map[string]any{"params": params}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (m *Manager) ExportName(ctx context.Context, modelID string, params map[string]any) (string, error) {
	var out ExportNameResponse
	p := "/models/" + url.PathEscape(modelID) + "/export-name"
	if err := m.doJSON(ctx, http.MethodPost, p, map[string]any{"params": params}, &out); err != nil {
		return "", err
	}
	if !out.OK || strings.TrimSpace(out.ExportFilename) == "" {
		return modelID, nil
	}
	return out.ExportFilename, nil
}

func (m *Manager) ExportToPath(ctx context.Context, modelID string, params map[string]any, format, outputPath string) (*ExportResponse, error) {
	var out ExportResponse
	p := "/models/" + url.PathEscape(modelID) + "/export"
	err := m.doJSON(ctx, http.MethodPost, p, map[string]any{
		"params":      params,
		"format":      format,
		"output_path": outputPath,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (m *Manager) ExportBytes(ctx context.Context, modelID string, params map[string]any, format string) ([]byte, error) {
	base, err := m.getBaseURL()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(map[string]any{
		"params": params,
		"format": format,
	})
	if err != nil {
		return nil, err
	}
	p := "/models/" + url.PathEscape(modelID) + "/export"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+p, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
