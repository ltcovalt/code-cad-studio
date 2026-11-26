package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/watcher"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

const (
	defaultSSEKeepalive = 15 * time.Second
	maxSSELogQueue      = 64
	maxSSEControlQueue  = 4096
)

// DesktopDialogs abstracts native OS file/folder dialogs so internal/server
// has zero dependency on Wails and can run identically as a standalone web server.
type DesktopDialogs interface {
	ChooseDirectory(title, startDir string) (string, error)
	ChooseFile(title, startDir string) (string, error)
	SaveFile(title, defaultDir, defaultFilename, filterName, filterPattern string) (string, error)
}

// DesktopWindow abstracts native frameless window controls so internal/server
// has zero dependency on Wails and can run identically as a standalone web server.
type DesktopWindow interface {
	Minimise()
	ToggleMaximise() bool
	IsMaximised() bool
	Close()
}

type sseMessage struct {
	event string
	data  string
	isLog bool
}

type sseClient struct {
	mu        sync.Mutex
	queue     []sseMessage
	logCount  int
	ctrlCount int
	notify    chan struct{}
	closed    bool
}

// Server holds the standard net/http router and coordinates the worker, config, presets, and SSE hub.
type Server struct {
	mux      *http.ServeMux
	cfgStore *config.Store
	worker   *worker.Manager
	watcher  *watcher.Watcher
	dialogs  DesktopDialogs

	windowMu sync.RWMutex
	window   DesktopWindow

	demoFS   fs.FS
	paramsPy []byte

	desktopRunner func(name string, args ...string) error

	sseMu               sync.Mutex
	sseClients          map[*sseClient]struct{}
	sseKeepaliveTimeout time.Duration

	setupMu     sync.Mutex
	setupCancel context.CancelFunc

	shuttingDown atomic.Bool
}

// SetWindow attaches native desktop window controls (optional; nil in standalone web mode).
func (s *Server) SetWindow(win DesktopWindow) {
	s.windowMu.Lock()
	s.window = win
	s.windowMu.Unlock()
}

func (s *Server) getWindow() DesktopWindow {
	s.windowMu.RLock()
	win := s.window
	s.windowMu.RUnlock()
	return win
}

// SetScaffoldAssets configures the embedded offline demo project filesystem and canonical params.py bytes.
func (s *Server) SetScaffoldAssets(demoFS fs.FS, paramsPy []byte) {
	s.demoFS = demoFS
	s.paramsPy = paramsPy
}

func defaultDesktopRunner(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

// New creates a new HTTP server with all JSON API and SSE routes registered.
func New(cfgStore *config.Store, workerMgr *worker.Manager, dialogs DesktopDialogs) *Server {
	s := &Server{
		mux:                 http.NewServeMux(),
		cfgStore:            cfgStore,
		worker:              workerMgr,
		dialogs:             dialogs,
		desktopRunner:       defaultDesktopRunner,
		sseClients:          make(map[*sseClient]struct{}),
		sseKeepaliveTimeout: defaultSSEKeepalive,
	}

	if workerMgr != nil {
		workerMgr.SetOnUnexpectedExit(func(msg string) {
			s.broadcastSSE("worker-error", map[string]string{"error": msg})
		})
	}

	s.watcher = watcher.New(s.handlePyFileChanged, s.handlePresetFileChanged)
	s.registerRoutes()
	return s
}

func librarySpecs(cfg config.Config) []worker.LibrarySpec {
	specs := make([]worker.LibrarySpec, 0, len(cfg.Libraries))
	for _, lib := range cfg.Libraries {
		specs = append(specs, worker.LibrarySpec{
			ID:   lib.ID,
			Name: lib.Name,
			Path: lib.Path,
		})
	}
	return specs
}

// StartServices boots the Python worker and library watcher using persisted config.
func (s *Server) StartServices() {
	cfg := s.cfgStore.Get()
	if cfg.NeedsOnboarding() {
		return
	}
	pyPath := cfg.ResolvePython()
	if err := s.worker.EnsureStarted(pyPath, librarySpecs(cfg)); err != nil {
		log.Printf("Worker startup warning: %v", err)
	}
	if roots := cfg.LibraryRoots(); len(roots) > 0 {
		if err := s.watcher.Watch(roots); err != nil {
			log.Printf("Watcher startup warning: %v", err)
		}
	}
}

// Shutdown stops any in-flight setup task, the file watcher, and the Python worker cleanly.
func (s *Server) Shutdown() {
	s.shuttingDown.Store(true)
	s.cancelSetupTask()
	if s.watcher != nil {
		s.watcher.Stop()
	}
	if s.worker != nil {
		s.worker.Stop()
	}
}

// Handler returns the standard http.Handler for standalone web mode.
func (s *Server) Handler() http.Handler {
	return s.securityMiddleware(s.mux, false)
}

// DesktopMiddleware adapts the Go ServeMux for Wails3 desktop AssetOptions.Middleware.
// Any matched /api/* or /events route is handled by Go; everything else
// falls through to Wails' static/dev asset server, permitting internal Wails hosts/origins.
func (s *Server) DesktopMiddleware(next http.Handler) http.Handler {
	return s.middleware(next, true)
}

// ServerMiddleware adapts the Go ServeMux for Wails3 headless server AssetOptions.Middleware.
// Any matched /api/* or /events route is handled by Go; everything else
// falls through to Wails' static/dev asset server, rejecting internal Wails hosts/origins.
func (s *Server) ServerMiddleware(next http.Handler) http.Handler {
	return s.middleware(next, false)
}

func (s *Server) middleware(next http.Handler, allowWailsInternal bool) http.Handler {
	combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := s.mux.Handler(r)
		if pattern != "" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/events" {
			s.mux.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
	return s.securityMiddleware(combined, allowWailsInternal)
}

func (s *Server) registerRoutes() {
	// Config & Health
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("POST /api/config", s.handleSaveConfig)
	s.mux.HandleFunc("POST /api/config/theme", s.handleSaveTheme)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	// Onboarding & Environment Setup
	s.mux.HandleFunc("GET /api/setup/defaults", s.handleSetupDefaults)
	s.mux.HandleFunc("POST /api/setup/inspect-dir", s.handleSetupInspectDir)
	s.mux.HandleFunc("POST /api/setup/probe", s.handleSetupProbe)
	s.mux.HandleFunc("POST /api/setup/download-project", s.handleSetupDownloadProject)
	s.mux.HandleFunc("POST /api/setup/install-env", s.handleSetupInstallEnv)
	s.mux.HandleFunc("POST /api/setup/cancel", s.handleSetupCancel)
	s.mux.HandleFunc("POST /api/setup/complete", s.handleSetupComplete)

	// Native OS Dialogs
	s.mux.HandleFunc("POST /api/dialogs/project", s.handleDialogProject)
	s.mux.HandleFunc("POST /api/dialogs/python", s.handleDialogPython)
	s.mux.HandleFunc("POST /api/dialogs/export-dir", s.handleDialogExportDir)

	// Native Frameless Window Controls
	s.mux.HandleFunc("POST /api/window/minimize", s.handleWindowMinimize)
	s.mux.HandleFunc("POST /api/window/toggle-maximize", s.handleWindowToggleMaximize)
	s.mux.HandleFunc("GET /api/window/state", s.handleWindowState)
	s.mux.HandleFunc("POST /api/window/close", s.handleWindowClose)

	// Render Cancellation, Scaffolding & Desktop Actions
	s.mux.HandleFunc("POST /api/render/cancel", s.handleCancelRender)
	s.mux.HandleFunc("POST /api/models/scaffold", s.handleScaffoldModel)
	s.mux.HandleFunc("POST /api/desktop/open-editor", s.handleDesktopOpenEditor)
	s.mux.HandleFunc("POST /api/desktop/reveal-file", s.handleDesktopRevealFile)
	s.mux.HandleFunc("POST /api/desktop/open-slicer", s.handleDesktopOpenSlicer)

	// Models, Rendering, Exporting & Presets
	s.mux.HandleFunc("GET /api/models", s.handleListModels)
	s.mux.HandleFunc("GET /api/models/{id}", s.handleGetModel)
	s.mux.HandleFunc("POST /api/models/{id}/render", s.handleRenderModel)
	s.mux.HandleFunc("POST /api/models/{id}/export", s.handleExportModel)
	s.mux.HandleFunc("GET /api/models/{id}/presets", s.handleGetPresets)
	s.mux.HandleFunc("POST /api/models/{id}/presets", s.handleSavePreset)
	s.mux.HandleFunc("PATCH /api/models/{id}/presets/{key}", s.handleRenamePreset)
	s.mux.HandleFunc("PUT /api/models/{id}/presets/{key}/verification", s.handleSetPresetVerification)
	s.mux.HandleFunc("DELETE /api/models/{id}/presets/{key}", s.handleDeletePreset)
	s.mux.HandleFunc("POST /api/models/{id}/presets/default", s.handleSetDefaultPreset)
	s.mux.HandleFunc("POST /api/models/{id}/update-defaults", s.handleUpdateModelDefaults)
	s.mux.HandleFunc("POST /api/models/{id}/rename", s.handleRenameModel)

	// SSE for hot-reload
	s.mux.HandleFunc("GET /events", s.handleSSE)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("json encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, traceback ...string) {
	payload := map[string]any{
		"ok":    false,
		"code":  code,
		"error": message,
	}
	if len(traceback) > 0 && strings.TrimSpace(traceback[0]) != "" {
		payload["traceback"] = traceback[0]
	}
	writeJSON(w, status, payload)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmptyEOF bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer r.Body.Close()

	dec := json.NewDecoder(r.Body)
	err := dec.Decode(dst)
	if err == nil {
		return true
	}
	if allowEmptyEOF && errors.Is(err, io.EOF) {
		return true
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Request body exceeds 2 MiB limit")
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON body")
	return false
}

func (s *Server) broadcastSSE(eventName string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := sseMessage{
		event: eventName,
		data:  string(raw),
		isLog: eventName == "setup-log",
	}

	s.sseMu.Lock()
	clients := make([]*sseClient, 0, len(s.sseClients))
	for c := range s.sseClients {
		clients = append(clients, c)
	}
	s.sseMu.Unlock()

	for _, c := range clients {
		c.mu.Lock()
		if !c.closed {
			appended := false
			if msg.isLog {
				if c.logCount < maxSSELogQueue {
					c.queue = append(c.queue, msg)
					c.logCount++
					appended = true
				}
			} else {
				if c.ctrlCount < maxSSEControlQueue {
					c.queue = append(c.queue, msg)
					c.ctrlCount++
					appended = true
				}
			}
			if appended {
				select {
				case c.notify <- struct{}{}:
				default:
				}
			}
		}
		c.mu.Unlock()
	}
}

func (s *Server) handlePyFileChanged(path string) {
	if s.shuttingDown.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Fast warm reload (~20ms) keeps build123d/OCP loaded in memory.
	if err := s.worker.Reload(ctx); err == nil {
		log.Printf("Python file changed (%s) -> warm reloaded worker", path)
		s.broadcastSSE("file-changed", map[string]string{"path": path})
		return
	}

	if s.shuttingDown.Load() {
		return
	}
	log.Printf("Python file changed (%s) -> restarting worker", path)
	cfg := s.cfgStore.Get()
	if err := s.worker.Restart(cfg.ResolvePython(), librarySpecs(cfg)); err != nil {
		if s.shuttingDown.Load() {
			return
		}
		log.Printf("Worker restart failed: %v", err)
		s.broadcastSSE("worker-error", map[string]string{"error": err.Error()})
		return
	}
	s.broadcastSSE("file-changed", map[string]string{"path": path})
}

func (s *Server) handlePresetFileChanged(path string) {
	if s.shuttingDown.Load() {
		return
	}
	log.Printf("Presets file changed (%s)", path)
	s.broadcastSSE("presets-changed", map[string]string{"path": path})
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming unsupported")
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	client := &sseClient{
		notify: make(chan struct{}, 1),
	}
	s.sseMu.Lock()
	s.sseClients[client] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, client)
		s.sseMu.Unlock()

		client.mu.Lock()
		client.closed = true
		client.queue = nil
		client.logCount = 0
		client.ctrlCount = 0
		client.mu.Unlock()
	}()

	seq := int64(1)
	if _, err := fmt.Fprintf(w, "id: %d\nevent: connected\ndata: {}\n\n", seq); err != nil {
		return
	}
	flusher.Flush()

	interval := s.sseKeepaliveTimeout
	if interval <= 0 {
		interval = defaultSSEKeepalive
	}
	keepalive := time.NewTicker(interval)
	defer keepalive.Stop()

	drain := func() bool {
		for {
			client.mu.Lock()
			if len(client.queue) == 0 {
				client.mu.Unlock()
				return true
			}
			batch := client.queue
			client.queue = nil
			client.logCount = 0
			client.ctrlCount = 0
			client.mu.Unlock()

			for _, msg := range batch {
				seq++
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, msg.event, msg.data); err != nil {
					return false
				}
			}
			flusher.Flush()
		}
	}

	ctx := r.Context()
	for {
		if !drain() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-client.notify:
			// drained at top of loop
		case <-keepalive.C:
			if !drain() {
				return
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) ensureWorkerRunning(ctx context.Context) error {
	running, _ := s.worker.Status()
	if running {
		return nil
	}
	cfg := s.cfgStore.Get()
	return s.worker.EnsureStarted(cfg.ResolvePython(), librarySpecs(cfg))
}
