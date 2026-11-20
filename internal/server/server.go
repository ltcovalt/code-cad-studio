package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

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

type cachedMesh struct {
	modelID string
	version int64
	shapes  json.RawMessage
}

// Server holds the standard net/http router and coordinates the worker, config, presets, and SSE hub.
type Server struct {
	mux       *http.ServeMux
	templates *template.Template
	cfgStore  *config.Store
	worker    *worker.Manager
	watcher   *watcher.Watcher
	dialogs   DesktopDialogs

	meshMu      sync.RWMutex
	meshes      map[string]cachedMesh
	meshVersion atomic.Int64

	sseMu      sync.Mutex
	sseClients map[chan string]struct{}
}

// New creates a new HTTP server with all HTMX and JSON routes registered.
func New(templateFS embed.FS, cfgStore *config.Store, workerMgr *worker.Manager, dialogs DesktopDialogs) (*Server, error) {
	funcMap := template.FuncMap{
		"eq": func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) },
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	s := &Server{
		mux:        http.NewServeMux(),
		templates:  tmpl,
		cfgStore:   cfgStore,
		worker:     workerMgr,
		dialogs:    dialogs,
		meshes:     make(map[string]cachedMesh),
		sseClients: make(map[chan string]struct{}),
	}

	s.watcher = watcher.New(s.handlePyFileChanged, s.handlePresetFileChanged)
	s.registerRoutes()
	return s, nil
}

// StartServices boots the Python worker and project watcher using persisted config.
func (s *Server) StartServices() {
	cfg := s.cfgStore.Get()
	pyPath := cfg.ResolvePython()
	if err := s.worker.Start(pyPath, cfg.ProjectRoot); err != nil {
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
// Any matched /hx/*, /api/*, or /events route is handled by Go; everything else
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
	// HTMX partials
	s.mux.HandleFunc("GET /hx/header", s.handleHeader)
	s.mux.HandleFunc("GET /hx/theme-controller", s.handleThemeController)
	s.mux.HandleFunc("POST /hx/theme", s.handleThemeSave)
	s.mux.HandleFunc("GET /hx/sidebar", s.handleSidebar)
	s.mux.HandleFunc("GET /hx/models", s.handleModelList)
	s.mux.HandleFunc("GET /hx/models/{id}", s.handleModelForm)
	s.mux.HandleFunc("POST /hx/models/{id}/render", s.handleModelRender)
	s.mux.HandleFunc("POST /hx/models/{id}/export", s.handleModelExport)
	s.mux.HandleFunc("GET /hx/models/{id}/presets", s.handlePresetBar)
	s.mux.HandleFunc("POST /hx/models/{id}/presets", s.handleSavePreset)

	// Settings
	s.mux.HandleFunc("GET /hx/settings", s.handleSettingsModal)
	s.mux.HandleFunc("GET /hx/settings/health", s.handleSettingsHealth)
	s.mux.HandleFunc("POST /hx/settings", s.handleSettingsSave)
	s.mux.HandleFunc("POST /hx/settings/browse-project", s.handleBrowseProject)
	s.mux.HandleFunc("POST /hx/settings/browse-python", s.handleBrowsePython)

	// Data / Download APIs
	s.mux.HandleFunc("GET /api/models/{id}/mesh", s.handleGetMesh)
	s.mux.HandleFunc("POST /api/models/{id}/export", s.handleDownloadExport)

	// SSE for hot-reload
	s.mux.HandleFunc("GET /events", s.handleSSE)
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s error: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
	return s.worker.Start(cfg.ResolvePython(), cfg.ProjectRoot)
}
