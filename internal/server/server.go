package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/watcher"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

// DesktopDialogs abstracts native OS file/folder dialogs so internal/server
// has zero dependency on Wails and can run identically as a standalone web server.
type DesktopDialogs interface {
	ChooseDirectory(title, startDir string) (string, error)
	ChooseFile(title, startDir string) (string, error)
	SaveFile(title, defaultFilename, filterName, filterPattern string) (string, error)
}

// DesktopWindow abstracts native frameless window controls so internal/server
// has zero dependency on Wails and can run identically as a standalone web server.
type DesktopWindow interface {
	Minimise()
	ToggleMaximise() bool
	IsMaximised() bool
	Close()
}

// Server holds the standard net/http router and coordinates the worker, config, presets, and SSE hub.
type Server struct {
	mux      *http.ServeMux
	cfgStore *config.Store
	worker   *worker.Manager
	watcher  *watcher.Watcher
	dialogs  DesktopDialogs
	window   DesktopWindow

	sseMu      sync.Mutex
	sseClients map[chan string]struct{}
}

// SetWindow attaches native desktop window controls (optional; nil in standalone web mode).
func (s *Server) SetWindow(win DesktopWindow) {
	s.window = win
}

// New creates a new HTTP server with all JSON API and SSE routes registered.
func New(cfgStore *config.Store, workerMgr *worker.Manager, dialogs DesktopDialogs) *Server {
	s := &Server{
		mux:        http.NewServeMux(),
		cfgStore:   cfgStore,
		worker:     workerMgr,
		dialogs:    dialogs,
		sseClients: make(map[chan string]struct{}),
	}

	s.watcher = watcher.New(s.handlePyFileChanged, s.handlePresetFileChanged)
	s.registerRoutes()
	return s
}

// StartServices boots the Python worker and project watcher using persisted config.
func (s *Server) StartServices() {
	cfg := s.cfgStore.Get()
	pyPath := cfg.ResolvePython()
	if err := s.worker.EnsureStarted(pyPath, cfg.ProjectRoot); err != nil {
		log.Printf("Worker startup warning: %v", err)
	}
	if cfg.ProjectRoot != "" {
		if err := s.watcher.Watch(cfg.ProjectRoot); err != nil {
			log.Printf("Watcher startup warning: %v", err)
		}
	}
}

// Shutdown stops the file watcher and Python worker cleanly.
func (s *Server) Shutdown() {
	if s.watcher != nil {
		s.watcher.Stop()
	}
	if s.worker != nil {
		s.worker.Stop()
	}
}

// Handler returns the standard http.Handler for standalone web mode.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Middleware adapts the Go ServeMux for Wails3 AssetOptions.Middleware.
// Any matched /api/* or /events route is handled by Go; everything else
// falls through to Wails' static/dev asset server.
func (s *Server) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := s.mux.Handler(r)
		if pattern != "" {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			s.mux.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerRoutes() {
	// Config & Health
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("POST /api/config", s.handleSaveConfig)
	s.mux.HandleFunc("POST /api/config/theme", s.handleSaveTheme)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	// Native OS Dialogs
	s.mux.HandleFunc("POST /api/dialogs/project", s.handleDialogProject)
	s.mux.HandleFunc("POST /api/dialogs/python", s.handleDialogPython)

	// Native Frameless Window Controls
	s.mux.HandleFunc("POST /api/window/minimize", s.handleWindowMinimize)
	s.mux.HandleFunc("POST /api/window/toggle-maximize", s.handleWindowToggleMaximize)
	s.mux.HandleFunc("GET /api/window/state", s.handleWindowState)
	s.mux.HandleFunc("POST /api/window/close", s.handleWindowClose)

	// Models, Rendering, Exporting & Presets
	s.mux.HandleFunc("GET /api/models", s.handleListModels)
	s.mux.HandleFunc("GET /api/models/{id}", s.handleGetModel)
	s.mux.HandleFunc("POST /api/models/{id}/render", s.handleRenderModel)
	s.mux.HandleFunc("POST /api/models/{id}/export", s.handleExportModel)
	s.mux.HandleFunc("GET /api/models/{id}/presets", s.handleGetPresets)
	s.mux.HandleFunc("POST /api/models/{id}/presets", s.handleSavePreset)
	s.mux.HandleFunc("PATCH /api/models/{id}/presets/{key}", s.handleRenamePreset)
	s.mux.HandleFunc("DELETE /api/models/{id}/presets/{key}", s.handleDeletePreset)
	s.mux.HandleFunc("POST /api/models/{id}/presets/default", s.handleSetDefaultPreset)
	s.mux.HandleFunc("POST /api/models/{id}/update-defaults", s.handleUpdateModelDefaults)

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

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	return dec.Decode(dst)
}

func (s *Server) broadcastSSE(eventName string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, string(data))
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for ch := range s.sseClients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Server) handlePyFileChanged(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Fast warm reload (~20ms) keeps build123d/OCP loaded in memory.
	if err := s.worker.Reload(ctx); err == nil {
		log.Printf("Python file changed (%s) -> warm reloaded worker", path)
		s.broadcastSSE("file-changed", map[string]string{"path": path})
		return
	}

	log.Printf("Python file changed (%s) -> restarting worker", path)
	cfg := s.cfgStore.Get()
	if err := s.worker.Restart(cfg.ResolvePython(), cfg.ProjectRoot); err != nil {
		log.Printf("Worker restart failed: %v", err)
		s.broadcastSSE("worker-error", map[string]string{"error": err.Error()})
		return
	}
	s.broadcastSSE("file-changed", map[string]string{"path": path})
}

func (s *Server) handlePresetFileChanged(path string) {
	log.Printf("Presets file changed (%s)", path)
	s.broadcastSSE("presets-changed", map[string]string{"path": path})
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 16)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, ch)
		s.sseMu.Unlock()
	}()

	// Initial ping
	_, _ = fmt.Fprint(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			_, _ = fmt.Fprint(w, msg)
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
	return s.worker.EnsureStarted(cfg.ResolvePython(), cfg.ProjectRoot)
}
