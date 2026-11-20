package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/presets"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

func (s *Server) handleHeader(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	projectLabel := "No project selected"
	if cfg.ProjectRoot != "" {
		projectLabel = filepath.Base(cfg.ProjectRoot)
	}
	s.renderTemplate(w, "Header.html", map[string]any{
		"ProjectRoot":  cfg.ProjectRoot,
		"ProjectLabel": projectLabel,
	})
}

func (s *Server) handleThemeController(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	theme := cfg.Theme
	if theme == "" {
		theme = "dark"
	}
	s.renderTemplate(w, "ThemeController.html", map[string]any{
		"CurrentTheme": theme,
	})
}

func (s *Server) handleThemeSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	theme := strings.TrimSpace(r.FormValue("theme"))
	if theme == "" {
		theme = strings.TrimSpace(r.FormValue("theme-dropdown"))
	}
	if theme == "" {
		theme = "dark"
	}
	_, _ = s.cfgStore.Update(func(c *config.Config) {
		c.Theme = theme
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSidebar(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	s.renderTemplate(w, "Sidebar.html", map[string]any{
		"ProjectRoot": cfg.ProjectRoot,
	})
}

func (s *Server) handleModelList(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	data := map[string]any{
		"ProjectRoot": cfg.ProjectRoot,
		"Query":       query,
	}

	if cfg.ProjectRoot == "" {
		s.renderTemplate(w, "ModelList.html", data)
		return
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		data["Error"] = err.Error()
		s.renderTemplate(w, "ModelList.html", data)
		return
	}

	resp, err := s.worker.ListModels(r.Context())
	if err != nil {
		data["Error"] = err.Error()
		s.renderTemplate(w, "ModelList.html", data)
		return
	}
	if !resp.OK {
		data["Error"] = resp.Error
		s.renderTemplate(w, "ModelList.html", data)
		return
	}

	var filtered []worker.ModelInfo
	for _, m := range resp.Models {
		if query == "" ||
			strings.Contains(strings.ToLower(m.Name), query) ||
			strings.Contains(strings.ToLower(m.ID), query) ||
			strings.Contains(strings.ToLower(m.Description), query) {
			filtered = append(filtered, m)
		}
	}

	data["Models"] = filtered
	s.renderTemplate(w, "ModelList.html", data)
}

func (s *Server) handleModelForm(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	presetKey := r.URL.Query().Get("preset")
	if presetKey == "" {
		presetKey = "default"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		s.renderTemplate(w, "ModelForm.html", map[string]any{"Error": err.Error()})
		return
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil {
		s.renderTemplate(w, "ModelForm.html", map[string]any{"Error": err.Error()})
		return
	}
	if !schema.OK {
		s.renderTemplate(w, "ModelForm.html", map[string]any{
			"Error":     schema.Error,
			"Traceback": schema.Traceback,
		})
		return
	}

	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults)
	activeValues := schema.Defaults
	for _, p := range presetList {
		if p.Key == presetKey {
			activeValues = presets.MergeWithDefaults(schema.Defaults, p.Params)
			break
		}
	}

	fieldViews := BuildFieldViews(schema.Fields, activeValues)
	coreFields, nestedGroups := SplitFieldViews(fieldViews)

	// Trigger initial render on load; preserve camera only if switching preset on same model
	resetCam := r.URL.Query().Get("preserve_camera") != "1"
	renderResp := s.renderAndTrigger(w, r, modelID, activeValues, resetCam)

	s.renderTemplate(w, "ModelForm.html", map[string]any{
		"Model":          schema.Model,
		"Description":    schema.Description,
		"CoreFields":     coreFields,
		"NestedGroups":   nestedGroups,
		"Presets":        presetList,
		"SelectedPreset": presetKey,
		"HasDesktop":     s.dialogs != nil,
		"InitialStatus": map[string]any{
			"OK":               renderResp.OK,
			"BuildTimeMs":      renderResp.BuildTimeMs,
			"TessellateTimeMs": renderResp.TessellateTimeMs,
			"BBox":             renderResp.BBox,
			"Error":            renderResp.Error,
			"Traceback":        renderResp.Traceback,
		},
	})
}

func (s *Server) renderAndTrigger(w http.ResponseWriter, r *http.Request, modelID string, params map[string]any, resetCamera bool) *worker.RenderResponse {
	resp, err := s.worker.Render(r.Context(), modelID, params)
	if err != nil {
		return &worker.RenderResponse{OK: false, Error: err.Error()}
	}
	if resp.OK && len(resp.Shapes) > 0 {
		ver := s.meshVersion.Add(1)
		s.meshMu.Lock()
		s.meshes[modelID] = cachedMesh{
			modelID: modelID,
			version: ver,
			shapes:  resp.Shapes,
		}
		s.meshMu.Unlock()

		trigger, _ := json.Marshal(map[string]any{
			"meshUpdated": map[string]any{
				"modelId":     modelID,
				"version":     ver,
				"resetCamera": resetCamera,
			},
		})
		w.Header().Set("HX-Trigger", string(trigger))
	}
	return resp
}

func (s *Server) handleModelRender(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": err.Error()})
		return
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": err.Error()})
		return
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		errMsg := ""
		tb := ""
		if err != nil {
			errMsg = err.Error()
		} else {
			errMsg = schema.Error
			tb = schema.Traceback
		}
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": errMsg, "Traceback": tb})
		return
	}

	params := ParseFormParams(r.PostForm, schema.Fields)
	resetCam := r.URL.Query().Get("reset_camera") == "1"
	resp := s.renderAndTrigger(w, r, modelID, params, resetCam)

	s.renderTemplate(w, "StatusBar.html", map[string]any{
		"OK":               resp.OK,
		"BuildTimeMs":      resp.BuildTimeMs,
		"TessellateTimeMs": resp.TessellateTimeMs,
		"BBox":             resp.BBox,
		"Error":            resp.Error,
		"Traceback":        resp.Traceback,
	})
}

func (s *Server) handleGetMesh(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	s.meshMu.RLock()
	cached, ok := s.meshes[modelID]
	s.meshMu.RUnlock()

	if !ok {
		http.Error(w, "mesh not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(cached.shapes)
}

func (s *Server) handleModelExport(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": err.Error()})
		return
	}

	fmtName := strings.ToLower(strings.TrimSpace(r.FormValue("format")))
	if fmtName == "" {
		fmtName = "step"
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": "Unable to load model schema for export"})
		return
	}
	params := ParseFormParams(r.PostForm, schema.Fields)

	if s.dialogs == nil {
		// Web mode: instruct frontend to download via POST /api/models/{id}/export
		trigger, _ := json.Marshal(map[string]any{
			"downloadExport": map[string]string{
				"modelId": modelID,
				"format":  fmtName,
			},
		})
		w.Header().Set("HX-Trigger", string(trigger))
		s.renderTemplate(w, "StatusBar.html", map[string]any{
			"Message": fmt.Sprintf("Preparing %s download...", strings.ToUpper(fmtName)),
		})
		return
	}

	defaultFile := fmt.Sprintf("%s.%s", modelID, fmtName)
	filterName := strings.ToUpper(fmtName) + " Files"
	filterPattern := "*." + fmtName

	outPath, err := s.dialogs.SaveFile("Export "+strings.ToUpper(fmtName), defaultFile, filterName, filterPattern)
	if err != nil {
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": err.Error()})
		return
	}
	if outPath == "" {
		// User cancelled save dialog
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Message": "Export cancelled"})
		return
	}

	resp, err := s.worker.ExportToPath(r.Context(), modelID, params, fmtName, outPath)
	if err != nil {
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": err.Error()})
		return
	}
	if !resp.OK {
		s.renderTemplate(w, "StatusBar.html", map[string]any{"Error": resp.Error, "Traceback": resp.Traceback})
		return
	}

	s.renderTemplate(w, "StatusBar.html", map[string]any{
		"Message": fmt.Sprintf("Exported %s (%d KB)", filepath.Base(resp.Path), (resp.Size+1023)/1024),
	})
}

func (s *Server) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fmtName := strings.ToLower(strings.TrimSpace(r.FormValue("format")))
	if fmtName == "" {
		fmtName = "step"
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		http.Error(w, "failed to load schema", http.StatusInternalServerError)
		return
	}
	params := ParseFormParams(r.PostForm, schema.Fields)
	raw, err := s.worker.ExportBytes(r.Context(), modelID, params, fmtName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, modelID, fmtName))
	_, _ = w.Write(raw)
}

func (s *Server) handlePresetBar(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	selected := r.URL.Query().Get("preset")
	if selected == "" {
		selected = "default"
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		http.Error(w, "failed to load schema", http.StatusInternalServerError)
		return
	}
	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults)
	s.renderTemplate(w, "PresetBar.html", map[string]any{
		"Model":          schema.Model,
		"Presets":        presetList,
		"SelectedPreset": selected,
	})
}

func (s *Server) handleSavePreset(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	label := strings.TrimSpace(r.FormValue("preset_label"))
	desc := strings.TrimSpace(r.FormValue("preset_description"))
	scope := strings.TrimSpace(r.FormValue("preset_scope"))
	if label == "" {
		http.Error(w, "Preset name is required", http.StatusBadRequest)
		return
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		http.Error(w, "failed to load model schema", http.StatusInternalServerError)
		return
	}

	currentParams := ParseFormParams(r.PostForm, schema.Fields)
	key, err := presets.Save(modelID, schema.Model.Dir, label, desc, scope, currentParams, schema.Defaults)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	presetList := presets.List(modelID, schema.Model.Dir, schema.Defaults)
	s.renderTemplate(w, "PresetBar.html", map[string]any{
		"Model":          schema.Model,
		"Presets":        presetList,
		"SelectedPreset": key,
		"SavedMessage":   fmt.Sprintf("Saved preset %q", label),
	})
}

func (s *Server) handleSettingsModal(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	running, lastErr := s.worker.Status()

	s.renderTemplate(w, "SettingsModal.html", map[string]any{
		"Config":         cfg,
		"ResolvedPython": cfg.ResolvePython(),
		"HasDesktop":     s.dialogs != nil,
		"WorkerRunning":  running,
		"WorkerErr":      lastErr,
		"LazyHealth":     true,
	})
}

func (s *Server) handleSettingsHealth(w http.ResponseWriter, r *http.Request) {
	health, healthErr := s.worker.Health(r.Context())
	running, lastErr := s.worker.Status()

	s.renderTemplate(w, "WorkerHealth.html", map[string]any{
		"WorkerRunning": running,
		"WorkerErr":     lastErr,
		"Health":        health,
		"HealthErr":     healthErr,
	})
}

func (s *Server) handleBrowseProject(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		http.Error(w, "native dialogs unavailable", http.StatusBadRequest)
		return
	}
	cfg := s.cfgStore.Get()
	dir, err := s.dialogs.ChooseDirectory("Select build123d Project Folder", cfg.ProjectRoot)
	if err != nil || dir == "" {
		dir = r.FormValue("project_root")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<input id="settings-project-root" name="project_root" type="text" class="input input-bordered input-sm w-full font-mono text-xs" value="%s" placeholder="/path/to/code-cad" />`, htmlEscapeAttr(dir))
}

func (s *Server) handleBrowsePython(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		http.Error(w, "native dialogs unavailable", http.StatusBadRequest)
		return
	}
	cfg := s.cfgStore.Get()
	p, err := s.dialogs.ChooseFile("Select Python Interpreter", cfg.ProjectRoot)
	if err != nil || p == "" {
		p = r.FormValue("python_path")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<input id="settings-python-path" name="python_path" type="text" class="input input-bordered input-sm w-full font-mono text-xs" value="%s" placeholder="Auto-detect (.venv/bin/python or python3)" />`, htmlEscapeAttr(p))
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectRoot := strings.TrimSpace(r.FormValue("project_root"))
	pythonPath := strings.TrimSpace(r.FormValue("python_path"))

	cfg, err := s.cfgStore.Update(func(c *config.Config) {
		c.ProjectRoot = projectRoot
		c.PythonPath = pythonPath
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Restart worker & watcher with new config
	workerErr := s.worker.Restart(cfg.ResolvePython(), cfg.ProjectRoot)
	if cfg.ProjectRoot != "" {
		_ = s.watcher.Watch(cfg.ProjectRoot)
	} else {
		s.watcher.Stop()
	}

	trigger, _ := json.Marshal(map[string]any{
		"projectChanged": true,
	})
	w.Header().Set("HX-Trigger", string(trigger))

	health, healthErr := s.worker.Health(r.Context())
	running, lastErr := s.worker.Status()
	if workerErr != nil {
		lastErr = workerErr.Error()
	}

	s.renderTemplate(w, "SettingsModal.html", map[string]any{
		"Config":         cfg,
		"ResolvedPython": cfg.ResolvePython(),
		"HasDesktop":     s.dialogs != nil,
		"WorkerRunning":  running,
		"WorkerErr":      lastErr,
		"Health":         health,
		"HealthErr":      healthErr,
		"Saved":          true,
	})
}

func htmlEscapeAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
