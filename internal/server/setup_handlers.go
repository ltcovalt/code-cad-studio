package server

import (
	"context"
	"errors"
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
	if !decodeJSON(w, r, &req, true) {
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
		LibraryRoots []string `json:"library_roots"`
		PythonPath   string   `json:"python_path"`
	}
	if !decodeJSON(w, r, &req, true) {
		return
	}

	res := setup.Probe(r.Context(), req.LibraryRoots, req.PythonPath)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSetupDownloadProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetDir      string `json:"target_dir"`
		ArchiveURL     string `json:"archive_url"`
		AllowOverwrite bool   `json:"allow_overwrite"`
		ExpectedSHA256 string `json:"expected_sha256"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}

	ctx, cleanup, err := s.beginSetupTask(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "setup_conflict", err.Error())
		return
	}
	defer cleanup()

	res, err := setup.DownloadStarterProject(ctx, setup.DownloadOptions{
		ArchiveURL:     req.ArchiveURL,
		TargetDir:      req.TargetDir,
		AllowOverwrite: req.AllowOverwrite,
		ExpectedSHA256: req.ExpectedSHA256,
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
		writeError(w, http.StatusUnprocessableEntity, "download_failed", err.Error())
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
		LibraryRoots []string `json:"library_roots"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}

	ctx, cleanup, err := s.beginSetupTask(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "setup_conflict", err.Error())
		return
	}
	defer cleanup()

	res, err := setup.InstallEnvironment(ctx, setup.InstallOptions{
		LibraryRoots: req.LibraryRoots,
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
		writeError(w, http.StatusUnprocessableEntity, "install_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"venv_dir":    res.VenvDir,
		"python_path": res.PythonPath,
		"candidate":   res.Candidate,
		"uv_path":     res.UVPath,
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
		Libraries  []config.Library `json:"libraries"`
		ExportDir  string           `json:"export_dir"`
		PythonPath string           `json:"python_path"`
	}
	if !decodeJSON(w, r, &req, true) {
		return
	}

	libraries := config.NormalizeLibraries(req.Libraries)
	exportDir := setup.ExpandPath(req.ExportDir)
	pythonPath := setup.ExpandPath(req.PythonPath)

	// Keep python_path empty when it matches Studio's auto-detected shared venv.
	// Explicit choices (such as a specific library .venv or conda env) are persisted so
	// reordering or removing libraries later never silently swaps the interpreter.
	if pythonPath != "" {
		if studioPy := setup.FindStudioVenvPython(); studioPy != "" && strings.EqualFold(filepath.Clean(pythonPath), filepath.Clean(studioPy)) {
			pythonPath = ""
		}
	}

	cfg, err := s.cfgStore.Update(func(c *config.Config) {
		c.Libraries = libraries
		c.ExportDir = exportDir
		c.PythonPath = pythonPath
		c.OnboardingCompleted = true
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_save_error", err.Error())
		return
	}

	var workerErr error
	if roots := cfg.LibraryRoots(); len(roots) > 0 {
		workerErr = s.worker.ConfigureOrRestart(r.Context(), cfg.ResolvePython(), librarySpecs(cfg))
		if workerErr != nil {
			s.broadcastSSE("worker-error", map[string]string{"error": workerErr.Error()})
		}
		_ = s.watcher.Watch(roots)
	} else {
		s.watcher.Stop()
	}

	health, healthErr := s.worker.Health(r.Context())
	writeJSON(w, http.StatusOK, s.buildConfigResponse(cfg, health, workerErr, healthErr))
}
