package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/setup"
)

func (s *Server) beginSetupTask(parent context.Context) (context.Context, func(), error) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupCancel != nil {
		return nil, nil, errors.New("another setup task is already running")
	}
	ctx, cancel := context.WithCancel(parent)
	s.setupCancel = cancel
	cleanup := func() {
		cancel()
		s.setupMu.Lock()
		s.setupCancel = nil
		s.setupMu.Unlock()
	}
	return ctx, cleanup, nil
}

func (s *Server) cancelSetupTask() bool {
	s.setupMu.Lock()
	cancel := s.setupCancel
	s.setupMu.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	return false
}

func (s *Server) handleSetupDefaults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, setup.Defaults())
}

func (s *Server) handleSetupInspectDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	info := setup.InspectProjectDir(req.Path)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"info": info,
	})
}

func (s *Server) handleSetupProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectRoot string `json:"project_root"`
		PythonPath  string `json:"python_path"`
	}
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}

	res := setup.Probe(r.Context(), req.ProjectRoot, req.PythonPath)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSetupDownloadProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetDir      string `json:"target_dir"`
		ArchiveURL     string `json:"archive_url"`
		AllowOverwrite bool   `json:"allow_overwrite"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}

	ctx, cleanup, err := s.beginSetupTask(r.Context())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer cleanup()

	res, err := setup.DownloadStarterProject(ctx, setup.DownloadOptions{
		ArchiveURL:     req.ArchiveURL,
		TargetDir:      req.TargetDir,
		AllowOverwrite: req.AllowOverwrite,
	}, func(p setup.DownloadProgress) {
		s.broadcastSSE("setup-progress", map[string]any{
			"task":             "download",
			"stage":            p.Stage,
			"message":          p.Message,
			"bytes_downloaded": p.BytesDownloaded,
			"total_bytes":      p.TotalBytes,
			"files_extracted":  p.FilesExtracted,
			"total_files":      p.TotalFiles,
		})
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "cancelled": true, "error": "Download cancelled"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"project_info":      res.ProjectInfo,
		"files_added":       res.FilesAdded,
		"files_overwritten": res.FilesOverwritten,
		"merged":            res.Merged,
	})
}

func (s *Server) handleSetupInstallEnv(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectRoot string `json:"project_root"`
		BasePython  string `json:"base_python"`
		UseUV       *bool  `json:"use_uv"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}

	useUV := true
	if req.UseUV != nil {
		useUV = *req.UseUV
	}

	ctx, cleanup, err := s.beginSetupTask(r.Context())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer cleanup()

	res, err := setup.InstallEnvironment(ctx, setup.InstallOptions{
		ProjectRoot: req.ProjectRoot,
		BasePython:  req.BasePython,
		UseUV:       useUV,
	}, func(ev setup.InstallEvent) {
		if ev.Message != "" {
			s.broadcastSSE("setup-progress", map[string]any{
				"task":    "install",
				"stage":   ev.Stage,
				"message": ev.Message,
			})
		}
		if ev.LogLine != "" {
			s.broadcastSSE("setup-log", map[string]any{
				"task":  "install",
				"stage": ev.Stage,
				"line":  ev.LogLine,
			})
		}
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "cancelled": true, "error": "Installation cancelled"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"venv_dir":    res.VenvDir,
		"python_path": res.PythonPath,
		"candidate":   res.Candidate,
		"used_uv":     res.UsedUV,
	})
}

func (s *Server) handleSetupCancel(w http.ResponseWriter, r *http.Request) {
	cancelled := s.cancelSetupTask()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"cancelled": cancelled,
	})
}

func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectRoot string `json:"project_root"`
		ExportDir   string `json:"export_dir"`
		PythonPath  string `json:"python_path"`
	}
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}

	projectRoot := setup.ExpandPath(req.ProjectRoot)
	exportDir := setup.ExpandPath(req.ExportDir)
	pythonPath := setup.ExpandPath(req.PythonPath)

	// Keep export_dir empty when it matches the default <project_root>/exports
	// so future project switches inherit their own exports folder automatically.
	if projectRoot != "" && exportDir != "" && filepath.Clean(exportDir) == filepath.Join(projectRoot, "exports") {
		exportDir = ""
	}
	// Keep python_path empty when it matches the auto-detected project .venv python
	// so future project switches automatically pick up each project's .venv.
	if projectRoot != "" && pythonPath != "" {
		if venvPy := setup.FindProjectVenvPython(projectRoot); venvPy != "" && strings.EqualFold(filepath.Clean(pythonPath), filepath.Clean(venvPy)) {
			pythonPath = ""
		}
	}

	cfg, err := s.cfgStore.Update(func(c *config.Config) {
		c.ProjectRoot = projectRoot
		c.ExportDir = exportDir
		c.PythonPath = pythonPath
		c.OnboardingCompleted = true
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	var workerErr error
	if cfg.ProjectRoot != "" {
		workerErr = s.worker.ConfigureOrRestart(r.Context(), cfg.ResolvePython(), cfg.ProjectRoot)
		_ = s.watcher.Watch(cfg.ProjectRoot)
	} else {
		s.watcher.Stop()
	}

	health, healthErr := s.worker.Health(r.Context())
	running, lastErr := s.worker.Status()
	if workerErr != nil {
		lastErr = workerErr.Error()
	}
	healthErrStr := ""
	if healthErr != nil {
		healthErrStr = healthErr.Error()
	}

	writeJSON(w, http.StatusOK, configResponse{
		Config:            cfg,
		ResolvedPython:    cfg.ResolvePython(),
		ResolvedExportDir: cfg.ResolveExportDir(),
		NeedsOnboarding:   cfg.NeedsOnboarding(),
		HasDesktop:        s.dialogs != nil,
		WorkerRunning:     running,
		WorkerStarting:    s.worker.Starting(),
		WorkerError:       lastErr,
		Health:            health,
		HealthError:       healthErrStr,
	})
}
