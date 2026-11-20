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
	mu           sync.Mutex
	workerScript []byte
	scriptPath   string
	pythonPath   string
	projectRoot  string
	cmd          *exec.Cmd
	baseURL      string
	httpClient   *http.Client
	lastErr      string
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(pythonPath, projectRoot)
}

func (m *Manager) startLocked(pythonPath, projectRoot string) error {
	m.stopLocked()

	m.pythonPath = pythonPath
	m.projectRoot = projectRoot
	m.lastErr = ""

	scriptPath, err := m.ensureScriptPath()
	if err != nil {
		m.lastErr = err.Error()
		return err
	}

	args := []string{"-u", scriptPath, "--port", "0"}
	if projectRoot != "" {
		args = append(args, "--project-root", projectRoot)
	}

	cmd := exec.Command(pythonPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.lastErr = err.Error()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.lastErr = err.Error()
		return err
	}

	if err := cmd.Start(); err != nil {
		m.lastErr = fmt.Sprintf("failed to start python (%s): %v", pythonPath, err)
		return errors.New(m.lastErr)
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
		m.cmd = cmd
		m.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
		log.Printf("Python worker started (%s) on %s", pythonPath, m.baseURL)
		return nil
	case err := <-errCh:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		m.lastErr = err.Error()
		return err
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		m.lastErr = "timed out waiting for python worker startup (30s)"
		return errors.New(m.lastErr)
	}
}

// Restart kills and restarts the Python worker (e.g. on .py file change or config change).
func (m *Manager) Restart(pythonPath, projectRoot string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pythonPath == "" {
		pythonPath = m.pythonPath
	}
	if projectRoot == "" {
		projectRoot = m.projectRoot
	}
	return m.startLocked(pythonPath, projectRoot)
}

// Stop terminates the Python worker cleanly.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *Manager) stopLocked() {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}
	m.cmd = nil
	m.baseURL = ""
}

// Status returns whether the worker is running and any startup error.
func (m *Manager) Status() (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.baseURL != "", m.lastErr
}

func (m *Manager) getBaseURL() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.baseURL == "" {
		if m.lastErr != "" {
			return "", errors.New(m.lastErr)
		}
		return "", errors.New("python worker is not running")
	}
	return m.baseURL, nil
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
