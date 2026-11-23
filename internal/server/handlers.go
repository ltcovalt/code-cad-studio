package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/presets"
	"github.com/ltcovalt/code-cad-studio/internal/setup"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

type configResponse struct {
	Config            config.Config          `json:"config"`
	ResolvedPython    string                 `json:"resolved_python"`
	ResolvedExportDir string                 `json:"resolved_export_dir"`
	NeedsOnboarding   bool                   `json:"needs_onboarding"`
	HasDesktop        bool                   `json:"has_desktop"`
	WorkerRunning     bool                   `json:"worker_running"`
	WorkerStarting    bool                   `json:"worker_starting"`
	WorkerError       string                 `json:"worker_error"`
	Health            *worker.HealthResponse `json:"health,omitempty"`
	HealthError       string                 `json:"health_error,omitempty"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	if cfg.Theme == "" {
		cfg.Theme = "dark"
	}
	running, lastErr := s.worker.Status()
	writeJSON(w, http.StatusOK, configResponse{
		Config:            cfg,
		ResolvedPython:    cfg.ResolvePython(),
		ResolvedExportDir: cfg.ResolveExportDir(""),
		NeedsOnboarding:   cfg.NeedsOnboarding(),
		HasDesktop:        s.dialogs != nil,
		WorkerRunning:     running,
		WorkerStarting:    s.worker.Starting(),
		WorkerError:       lastErr,
	})
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Libraries  []config.Library `json:"libraries"`
		PythonPath string           `json:"python_path"`
		ExportDir  string           `json:"export_dir"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}

	libraries := config.NormalizeLibraries(req.Libraries)
	pythonPath := setup.ExpandPath(req.PythonPath)
	exportDir := setup.ExpandPath(req.ExportDir)

	cfg, err := s.cfgStore.Update(func(c *config.Config) {
		c.Libraries = libraries
		c.PythonPath = pythonPath
		c.ExportDir = exportDir
		if len(libraries) > 0 {
			c.OnboardingCompleted = true
		}
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	var workerErr error
	if roots := cfg.LibraryRoots(); len(roots) > 0 {
		workerErr = s.worker.ConfigureOrRestart(r.Context(), cfg.ResolvePython(), librarySpecs(cfg))
		_ = s.watcher.Watch(roots)
	} else {
		// Keep warm worker synced to empty library list if already running
		if running, _ := s.worker.Status(); running {
			_, _ = s.worker.Configure(r.Context(), []worker.LibrarySpec{})
		}
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
		ResolvedExportDir: cfg.ResolveExportDir(""),
		NeedsOnboarding:   cfg.NeedsOnboarding(),
		HasDesktop:        s.dialogs != nil,
		WorkerRunning:     running,
		WorkerStarting:    s.worker.Starting(),
		WorkerError:       lastErr,
		Health:            health,
		HealthError:       healthErrStr,
	})
}

func (s *Server) handleSaveTheme(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Theme string `json:"theme"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	theme := strings.TrimSpace(req.Theme)
	if theme == "" {
		theme = "dark"
	}
	_, _ = s.cfgStore.Update(func(c *config.Config) {
		c.Theme = theme
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	running, lastErr := s.worker.Status()
	starting := s.worker.Starting()
	if starting || !running {
		writeJSON(w, http.StatusOK, map[string]any{
			"worker_running":  running,
			"worker_starting": starting,
			"worker_error":    lastErr,
			"health":          nil,
			"health_error":    "",
		})
		return
	}

	health, healthErr := s.worker.Health(r.Context())
	healthErrStr := ""
	if healthErr != nil {
		healthErrStr = healthErr.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"worker_running":  running,
		"worker_starting": false,
		"worker_error":    lastErr,
		"health":          health,
		"health_error":    healthErrStr,
	})
}

func nearestExistingDir(p string) string {
	cur := setup.ExpandPath(p)
	for cur != "" {
		if info, err := os.Stat(cur); err == nil {
			if info.IsDir() {
				return cur
			}
			cur = filepath.Dir(cur)
			continue
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

func firstLibraryPath(cfg config.Config) string {
	if len(cfg.Libraries) > 0 {
		return cfg.Libraries[0].Path
	}
	return ""
}

func (s *Server) handleDialogProject(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "native dialogs unavailable"})
		return
	}
	var req struct {
		Current string `json:"current"`
	}
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	startDir := strings.TrimSpace(req.Current)
	if startDir == "" {
		startDir = firstLibraryPath(s.cfgStore.Get())
	}
	startDir = nearestExistingDir(startDir)
	dir, err := s.dialogs.ChooseDirectory("Select Model Library Folder", startDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      dir,
		"cancelled": dir == "",
	})
}

func (s *Server) handleDialogPython(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "native dialogs unavailable"})
		return
	}
	var req struct {
		Current string `json:"current"`
	}
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	startDir := strings.TrimSpace(req.Current)
	if startDir == "" {
		startDir = firstLibraryPath(s.cfgStore.Get())
	}
	startDir = nearestExistingDir(startDir)
	p, err := s.dialogs.ChooseFile("Select Python Interpreter", startDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      p,
		"cancelled": p == "",
	})
}

func (s *Server) handleDialogExportDir(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "native dialogs unavailable"})
		return
	}
	var req struct {
		Current string `json:"current"`
	}
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	cfg := s.cfgStore.Get()
	startDir := strings.TrimSpace(req.Current)
	if startDir == "" {
		startDir = cfg.ResolveExportDir("")
	}
	startDir = nearestExistingDir(startDir)
	dir, err := s.dialogs.ChooseDirectory("Select Default Export Folder", startDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      dir,
		"cancelled": dir == "",
	})
}

func (s *Server) handleWindowMinimize(w http.ResponseWriter, r *http.Request) {
	if s.window != nil {
		s.window.Minimise()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWindowToggleMaximize(w http.ResponseWriter, r *http.Request) {
	maximized := false
	if s.window != nil {
		maximized = s.window.ToggleMaximise()
	}
	writeJSON(w, http.StatusOK, map[string]any{"maximized": maximized})
}

func (s *Server) handleWindowState(w http.ResponseWriter, r *http.Request) {
	maximized := false
	if s.window != nil {
		maximized = s.window.IsMaximised()
	}
	writeJSON(w, http.StatusOK, map[string]any{"maximized": maximized})
}

func (s *Server) handleWindowClose(w http.ResponseWriter, r *http.Request) {
	if s.window != nil {
		s.window.Close()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	if len(cfg.Libraries) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"libraries":      []config.Library{},
			"models":         []worker.ModelInfo{},
			"library_errors": map[string]string{},
		})
		return
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             false,
			"libraries":      cfg.Libraries,
			"models":         []worker.ModelInfo{},
			"library_errors": map[string]string{},
			"error":          err.Error(),
		})
		return
	}

	resp, err := s.worker.ListModels(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             false,
			"libraries":      cfg.Libraries,
			"models":         []worker.ModelInfo{},
			"library_errors": map[string]string{},
			"error":          err.Error(),
		})
		return
	}
	if !resp.OK {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             false,
			"libraries":      cfg.Libraries,
			"models":         []worker.ModelInfo{},
			"library_errors": map[string]string{},
			"error":          resp.Error,
		})
		return
	}

	models := resp.Models
	if models == nil {
		models = []worker.ModelInfo{}
	}
	libErrors := resp.LibraryErrors
	if libErrors == nil {
		libErrors = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"libraries":      cfg.Libraries,
		"models":         models,
		"library_errors": libErrors,
	})
}

func (s *Server) handleGetModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !schema.OK {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"error":     schema.Error,
			"traceback": schema.Traceback,
		})
		return
	}

	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults, schemaDefaultPresetMeta(schema))
	fields := schema.Fields
	if fields == nil {
		fields = []worker.ParamField{}
	}
	defaults := schema.Defaults
	if defaults == nil {
		defaults = map[string]any{}
	}

	cfg := s.cfgStore.Get()
	defaultPresetKey := ""
	if cfg.DefaultPresets != nil {
		defaultPresetKey = cfg.DefaultPresets[modelID]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"model":              schema.Model,
		"description":        schema.Description,
		"fields":             fields,
		"defaults":           defaults,
		"presets":            presetList,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleRenderModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	var req struct {
		Params map[string]any `json:"params"`
	}
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	resp, err := s.worker.Render(r.Context(), modelID, req.Params)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleExportModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	var req struct {
		Params map[string]any `json:"params"`
		Format string         `json:"format"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	fmtName := strings.ToLower(strings.TrimSpace(req.Format))
	if fmtName == "" {
		fmtName = "step"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	stem, libraryID, err := s.worker.ExportName(r.Context(), modelID, req.Params)
	if err != nil || strings.TrimSpace(stem) == "" {
		stem = modelID
	}
	defaultFile := fmt.Sprintf("%s.%s", stem, fmtName)

	if s.dialogs == nil {
		raw, err := s.worker.ExportBytes(r.Context(), modelID, req.Params, fmtName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, defaultFile))
		_, _ = w.Write(raw)
		return
	}

	filterName := strings.ToUpper(fmtName) + " Files"
	filterPattern := "*." + fmtName

	exportDir := s.cfgStore.Get().ResolveExportDir(libraryID)
	if exportDir != "" {
		_ = os.MkdirAll(exportDir, 0o755)
	}

	outPath, err := s.dialogs.SaveFile("Export "+strings.ToUpper(fmtName), exportDir, defaultFile, filterName, filterPattern)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if outPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": true})
		return
	}

	resp, err := s.worker.ExportToPath(r.Context(), modelID, req.Params, fmtName, outPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        resp.OK,
		"cancelled": false,
		"path":      resp.Path,
		"size":      resp.Size,
		"error":     resp.Error,
		"traceback": resp.Traceback,
	})
}

func schemaDefaultPresetMeta(schema *worker.SchemaResponse) *presets.DefaultMeta {
	if schema == nil || schema.DefaultPreset == nil {
		return nil
	}
	return &presets.DefaultMeta{
		Label:       schema.DefaultPreset.Label,
		Description: schema.DefaultPreset.Description,
	}
}

func (s *Server) handleGetPresets(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "presets": []presets.Preset{}})
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		errMsg := "failed to load schema"
		if err != nil {
			errMsg = err.Error()
		} else if schema.Error != "" {
			errMsg = schema.Error
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": errMsg, "presets": []presets.Preset{}})
		return
	}
	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults, schemaDefaultPresetMeta(schema))
	cfg := s.cfgStore.Get()
	defaultPresetKey := ""
	if cfg.DefaultPresets != nil {
		defaultPresetKey = cfg.DefaultPresets[modelID]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"presets":            presetList,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleSavePreset(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	var req struct {
		Label        string                `json:"label"`
		Description  string                `json:"description"`
		Scope        string                `json:"scope"`
		FullSnapshot bool                  `json:"full_snapshot"`
		Params       map[string]any        `json:"params"`
		Verification *presets.Verification `json:"verification,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Preset name is required"})
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}

	var verification *presets.Verification
	if req.Verification != nil {
		status, err := presets.ParseVerificationStatus(string(req.Verification.Status))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		verification = presets.NormalizeVerification(&presets.Verification{
			Status: status,
			Note:   req.Verification.Note,
		})
	}

	scope := strings.TrimSpace(req.Scope)
	if scope != "project" {
		scope = "user"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to load model schema"})
		return
	}

	key, err := presets.Save(
		modelID,
		schema.Model.Dir,
		label,
		strings.TrimSpace(req.Description),
		scope,
		req.FullSnapshot,
		req.Params,
		schema.Defaults,
		verification,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	_, _ = s.cfgStore.Update(func(c *config.Config) {
		c.PresetSaveScope = scope
	})

	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults, schemaDefaultPresetMeta(schema))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"selected_key":      key,
		"presets":           presetList,
		"preset_save_scope": scope,
	})
}

func (s *Server) handleSetPresetVerification(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	key := strings.TrimSpace(r.PathValue("key"))
	var req struct {
		Scope  string `json:"scope"`
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}

	status, err := presets.ParseVerificationStatus(req.Status)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	scope := strings.TrimSpace(req.Scope)
	if key == "default" {
		scope = "default"
	} else if scope != "project" {
		scope = "user"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to load model schema"})
		return
	}

	err = presets.SetVerification(modelID, schema.Model.Dir, key, scope, &presets.Verification{
		Status: status,
		Note:   req.Note,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults, schemaDefaultPresetMeta(schema))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"presets": presetList,
	})
}

func (s *Server) handleRenamePreset(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	oldKey := strings.TrimSpace(r.PathValue("key"))
	var req struct {
		Scope       string `json:"scope"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope != "project" {
		scope = "user"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to load model schema"})
		return
	}

	newKey, err := presets.Rename(modelID, schema.Model.Dir, oldKey, scope, req.Label, req.Description)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	defaultPresetKey := ""
	cfg, _ := s.cfgStore.Update(func(c *config.Config) {
		if c.DefaultPresets != nil && c.DefaultPresets[modelID] == oldKey {
			c.DefaultPresets[modelID] = newKey
		}
	})
	if cfg.DefaultPresets != nil {
		defaultPresetKey = cfg.DefaultPresets[modelID]
	}

	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults, schemaDefaultPresetMeta(schema))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"key":                newKey,
		"presets":            presetList,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleDeletePreset(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	key := strings.TrimSpace(r.PathValue("key"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope != "project" {
		scope = "user"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "failed to load model schema"})
		return
	}

	if err := presets.Delete(modelID, schema.Model.Dir, key, scope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults, schemaDefaultPresetMeta(schema))
	stillExists := false
	for _, p := range presetList {
		if p.Key == key {
			stillExists = true
			break
		}
	}

	defaultPresetKey := ""
	cfg, _ := s.cfgStore.Update(func(c *config.Config) {
		if c.DefaultPresets != nil && c.DefaultPresets[modelID] == key && !stillExists {
			delete(c.DefaultPresets, modelID)
		}
	})
	if cfg.DefaultPresets != nil {
		defaultPresetKey = cfg.DefaultPresets[modelID]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"presets":            presetList,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleSetDefaultPreset(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	var req struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "default" {
		key = ""
	}

	cfg, err := s.cfgStore.Update(func(c *config.Config) {
		if c.DefaultPresets == nil {
			c.DefaultPresets = map[string]string{}
		}
		if key == "" {
			delete(c.DefaultPresets, modelID)
		} else {
			c.DefaultPresets[modelID] = key
		}
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	defaultPresetKey := ""
	if cfg.DefaultPresets != nil {
		defaultPresetKey = cfg.DefaultPresets[modelID]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleUpdateModelDefaults(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	var req struct {
		Params         map[string]any `json:"params"`
		DryRun         bool           `json:"dry_run"`
		ExpectedSHA256 string         `json:"expected_sha256"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid JSON body"})
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	resp, err := s.worker.UpdateDefaults(r.Context(), modelID, req.Params, req.DryRun, strings.TrimSpace(req.ExpectedSHA256))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !req.DryRun && resp.OK && resp.Updated > 0 && resp.File != "" {
		_ = presets.DowngradeDefaultVerification(filepath.Dir(resp.File))
	}
	if resp.Changes == nil {
		resp.Changes = []worker.DefaultChange{}
	}
	if resp.Unsupported == nil {
		resp.Unsupported = []worker.UnsupportedDefaultChange{}
	}
	writeJSON(w, http.StatusOK, resp)
}
