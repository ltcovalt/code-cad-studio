package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/presets"
	"github.com/ltcovalt/code-cad-studio/internal/setup"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

var (
	validLibraryIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	windowsReservedNames  = map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
		"COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
		"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}
)

type configResponse struct {
	Config            config.Config          `json:"config"`
	ResolvedPython    string                 `json:"resolved_python"`
	ResolvedExportDir string                 `json:"resolved_export_dir"`
	NeedsOnboarding   bool                   `json:"needs_onboarding"`
	HasDesktop        bool                   `json:"has_desktop"`
	OS                string                 `json:"os"`
	ConfigError       string                 `json:"config_error,omitempty"`
	CorruptBackupPath string                 `json:"corrupt_backup_path,omitempty"`
	WorkerRunning     bool                   `json:"worker_running"`
	WorkerStarting    bool                   `json:"worker_starting"`
	WorkerError       string                 `json:"worker_error"`
	Health            *worker.HealthResponse `json:"health,omitempty"`
	HealthError       string                 `json:"health_error,omitempty"`
}

func (s *Server) buildConfigResponse(cfg config.Config, health *worker.HealthResponse, workerErr, healthErr error) configResponse {
	if cfg.Theme == "" {
		cfg.Theme = config.DefaultTheme
	}
	running, lastErr := s.worker.Status()
	if workerErr != nil {
		lastErr = workerErr.Error()
	}
	healthErrStr := ""
	if healthErr != nil {
		healthErrStr = healthErr.Error()
	}
	cfgErrStr := ""
	if loadErr := s.cfgStore.LoadError(); loadErr != nil {
		cfgErrStr = loadErr.Error()
	}
	return configResponse{
		Config:            cfg,
		ResolvedPython:    cfg.ResolvePython(),
		ResolvedExportDir: cfg.ResolveExportDir(""),
		NeedsOnboarding:   cfg.NeedsOnboarding(),
		HasDesktop:        s.dialogs != nil,
		OS:                runtime.GOOS,
		ConfigError:       cfgErrStr,
		CorruptBackupPath: s.cfgStore.CorruptBackupPath(),
		WorkerRunning:     running,
		WorkerStarting:    s.worker.Starting(),
		WorkerError:       lastErr,
		Health:            health,
		HealthError:       healthErrStr,
	}
}

func librariesEqual(a, b []config.Library) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Name != b[i].Name || a[i].Path != b[i].Path {
			return false
		}
	}
	return true
}

func extractModelID(w http.ResponseWriter, r *http.Request) (string, bool) {
	modelID := r.PathValue("id")
	if modelID == "" || len(modelID) > 256 {
		writeError(w, http.StatusBadRequest, "invalid_model_id", "Invalid model id")
		return "", false
	}
	for i := 0; i < len(modelID); i++ {
		if modelID[i] < 32 || modelID[i] == 127 {
			writeError(w, http.StatusBadRequest, "invalid_model_id", "Invalid model id")
			return "", false
		}
	}
	if strings.ContainsAny(modelID, `\:`) {
		writeError(w, http.StatusBadRequest, "invalid_model_id", "Invalid model id")
		return "", false
	}
	if strings.HasPrefix(modelID, "/") || strings.HasSuffix(modelID, "/") || strings.Contains(modelID, "//") {
		writeError(w, http.StatusBadRequest, "invalid_model_id", "Invalid model id")
		return "", false
	}
	parts := strings.Split(modelID, "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "invalid_model_id", "Invalid model id")
		return "", false
	}
	if !validLibraryIDPattern.MatchString(parts[0]) || parts[0] == "." || parts[0] == ".." {
		writeError(w, http.StatusBadRequest, "invalid_model_id", "Invalid model id")
		return "", false
	}
	for _, seg := range parts {
		if seg == "" || seg == "." || seg == ".." {
			writeError(w, http.StatusBadRequest, "invalid_model_id", "Invalid model id")
			return "", false
		}
	}
	return modelID, true
}

func isWindowsReservedDeviceName(name string) bool {
	head, _, _ := strings.Cut(name, ".")
	switch strings.ToUpper(head) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func sanitizeExportStem(raw, fallback string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 {
		normalized = normalized[idx+1:]
	}
	base := strings.TrimSpace(normalized)
	lower := strings.ToLower(base)
	for _, ext := range []string{".step", ".stp", ".stl", ".3mf"} {
		if strings.HasSuffix(lower, ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	base = strings.TrimSpace(base)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	clean := strings.Trim(b.String(), "._- ")
	if len(clean) > 120 {
		clean = strings.TrimRight(clean[:120], "._- ")
	}
	if clean == "" {
		return fallback
	}
	if isWindowsReservedDeviceName(clean) {
		clean = "export_" + clean
		if len(clean) > 120 {
			clean = strings.TrimRight(clean[:120], "._- ")
		}
	}
	return clean
}

func fallbackStemFromID(modelID string) string {
	s := strings.TrimSpace(modelID)
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	return sanitizeExportStem(s, "model")
}

func isValidExportFormat(fmtName string) bool {
	switch fmtName {
	case "step", "stp", "stl", "3mf":
		return true
	default:
		return false
	}
}

func validateExportQuality(quality string, lin, ang *float64) (string, error) {
	q := strings.ToLower(strings.TrimSpace(quality))
	if q == "" {
		q = "standard"
	}
	switch q {
	case "draft", "standard", "fine":
		return q, nil
	case "custom":
		if lin == nil || ang == nil {
			return "", fmt.Errorf("custom export quality requires linear_deflection and angular_deflection")
		}
		if math.IsNaN(*lin) || math.IsInf(*lin, 0) || math.IsNaN(*ang) || math.IsInf(*ang, 0) {
			return "", fmt.Errorf("custom export deflection values must be finite")
		}
		if *lin < 1e-5 || *lin > 10.0 || *ang < 1e-3 || *ang > 3.15 {
			return "", fmt.Errorf("custom export deflection values out of allowed range")
		}
		return q, nil
	default:
		return "", fmt.Errorf("unsupported export quality %q", quality)
	}
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgStore.Get()
	writeJSON(w, http.StatusOK, s.buildConfigResponse(cfg, nil, nil, nil))
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Libraries         *[]config.Library `json:"libraries"`
		PythonPath        *string           `json:"python_path"`
		ExportDir         *string           `json:"export_dir"`
		Theme             *string           `json:"theme"`
		PresetSaveScope   *string           `json:"preset_save_scope"`
		OnboardingSkipped *bool             `json:"onboarding_skipped"`
		EditorCommand     *string           `json:"editor_command"`
		SlicerCommand     *string           `json:"slicer_command"`
		ExportQuality     *string           `json:"export_quality"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}

	if req.ExportQuality != nil {
		q := strings.ToLower(strings.TrimSpace(*req.ExportQuality))
		if q != "" && q != "draft" && q != "standard" && q != "fine" && q != "custom" {
			writeError(w, http.StatusBadRequest, "invalid_export_quality", "Unsupported export quality")
			return
		}
	}

	prevCfg := s.cfgStore.Get()
	prevPython := prevCfg.ResolvePython()

	cfg, err := s.cfgStore.Update(func(c *config.Config) {
		if req.Libraries != nil {
			libs := config.NormalizeLibraries(*req.Libraries)
			c.Libraries = libs
			if len(libs) > 0 {
				c.OnboardingCompleted = true
			}
		}
		if req.PythonPath != nil {
			c.PythonPath = setup.ExpandPath(*req.PythonPath)
		}
		if req.ExportDir != nil {
			c.ExportDir = setup.ExpandPath(*req.ExportDir)
		}
		if req.Theme != nil {
			t := strings.TrimSpace(*req.Theme)
			if t == "" {
				t = config.DefaultTheme
			}
			c.Theme = t
		}
		if req.PresetSaveScope != nil {
			scope := strings.TrimSpace(*req.PresetSaveScope)
			if scope == "project" || scope == "user" {
				c.PresetSaveScope = scope
			}
		}
		if req.OnboardingSkipped != nil {
			c.OnboardingSkipped = *req.OnboardingSkipped
		}
		if req.EditorCommand != nil {
			c.EditorCommand = strings.TrimSpace(*req.EditorCommand)
		}
		if req.SlicerCommand != nil {
			c.SlicerCommand = strings.TrimSpace(*req.SlicerCommand)
		}
		if req.ExportQuality != nil {
			q := strings.ToLower(strings.TrimSpace(*req.ExportQuality))
			if q == "" {
				q = "standard"
			}
			c.ExportQuality = q
		}
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_save_error", err.Error())
		return
	}

	libsChanged := !librariesEqual(prevCfg.Libraries, cfg.Libraries)
	pythonChanged := prevPython != cfg.ResolvePython() || prevCfg.PythonPath != cfg.PythonPath
	runningBefore, _ := s.worker.Status()
	shouldStartStoppedWorker := !runningBefore && len(cfg.Libraries) > 0 && (req.Libraries != nil || req.PythonPath != nil)

	var workerErr error
	var health *worker.HealthResponse
	var healthErr error

	if libsChanged || pythonChanged || shouldStartStoppedWorker {
		if roots := cfg.LibraryRoots(); len(roots) > 0 {
			workerErr = s.worker.ConfigureOrRestart(r.Context(), cfg.ResolvePython(), librarySpecs(cfg))
			_ = s.watcher.Watch(roots)
		} else {
			if running, _ := s.worker.Status(); running {
				_, _ = s.worker.Configure(r.Context(), []worker.LibrarySpec{})
			}
			s.watcher.Stop()
		}
		if workerErr != nil {
			s.broadcastSSE("worker-error", map[string]string{"error": workerErr.Error()})
		}
		if running, _ := s.worker.Status(); running {
			health, healthErr = s.worker.Health(r.Context())
		}
	} else if runningBefore {
		health, healthErr = s.worker.Health(r.Context())
	}

	writeJSON(w, http.StatusOK, s.buildConfigResponse(cfg, health, workerErr, healthErr))
}

func (s *Server) handleSaveTheme(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Theme string `json:"theme"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}
	theme := strings.TrimSpace(req.Theme)
	if theme == "" {
		theme = config.DefaultTheme
	}
	if _, err := s.cfgStore.Update(func(c *config.Config) {
		c.Theme = theme
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "config_save_error", err.Error())
		return
	}
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

func isDialogCancelError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch msg {
	case "cancelled", "canceled", "cancelled by user", "canceled by user":
		return true
	}
	return strings.Contains(msg, "cancelled by user") ||
		strings.Contains(msg, "canceled by user") ||
		strings.Contains(msg, "user cancelled") ||
		strings.Contains(msg, "user canceled") ||
		strings.HasSuffix(msg, ": cancelled") ||
		strings.HasSuffix(msg, ": canceled")
}

func (s *Server) handleDialogProject(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		writeError(w, http.StatusBadRequest, "desktop_unavailable", "Native dialogs are unavailable in server mode")
		return
	}
	var req struct {
		Current string `json:"current"`
	}
	if !decodeJSON(w, r, &req, true) {
		return
	}
	startDir := strings.TrimSpace(req.Current)
	if startDir == "" {
		startDir = firstLibraryPath(s.cfgStore.Get())
	}
	startDir = nearestExistingDir(startDir)
	dir, err := s.dialogs.ChooseDirectory("Select Model Library Folder", startDir)
	if err != nil {
		if isDialogCancelError(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"path":      "",
				"cancelled": true,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "dialog_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      dir,
		"cancelled": dir == "",
	})
}

func (s *Server) handleDialogPython(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		writeError(w, http.StatusBadRequest, "desktop_unavailable", "Native dialogs are unavailable in server mode")
		return
	}
	var req struct {
		Current string `json:"current"`
	}
	if !decodeJSON(w, r, &req, true) {
		return
	}
	startDir := strings.TrimSpace(req.Current)
	if startDir == "" {
		startDir = firstLibraryPath(s.cfgStore.Get())
	}
	startDir = nearestExistingDir(startDir)
	p, err := s.dialogs.ChooseFile("Select Python Interpreter", startDir)
	if err != nil {
		if isDialogCancelError(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"path":      "",
				"cancelled": true,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "dialog_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      p,
		"cancelled": p == "",
	})
}

func (s *Server) handleDialogExportDir(w http.ResponseWriter, r *http.Request) {
	if s.dialogs == nil {
		writeError(w, http.StatusBadRequest, "desktop_unavailable", "Native dialogs are unavailable in server mode")
		return
	}
	var req struct {
		Current string `json:"current"`
	}
	if !decodeJSON(w, r, &req, true) {
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
		if isDialogCancelError(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"path":      "",
				"cancelled": true,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "dialog_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      dir,
		"cancelled": dir == "",
	})
}

func (s *Server) handleWindowMinimize(w http.ResponseWriter, r *http.Request) {
	if win := s.getWindow(); win != nil {
		win.Minimise()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWindowToggleMaximize(w http.ResponseWriter, r *http.Request) {
	maximized := false
	if win := s.getWindow(); win != nil {
		maximized = win.ToggleMaximise()
	}
	writeJSON(w, http.StatusOK, map[string]any{"maximized": maximized})
}

func (s *Server) handleWindowState(w http.ResponseWriter, r *http.Request) {
	maximized := false
	if win := s.getWindow(); win != nil {
		maximized = win.IsMaximised()
	}
	writeJSON(w, http.StatusOK, map[string]any{"maximized": maximized})
}

func (s *Server) handleWindowClose(w http.ResponseWriter, r *http.Request) {
	if win := s.getWindow(); win != nil {
		win.Close()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelRender(w http.ResponseWriter, r *http.Request) {
	if err := s.worker.CancelAndRestart(r.Context()); err != nil {
		if err.Error() == "python interpreter not configured" {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":        true,
				"cancelled": false,
			})
			return
		}
		writeError(w, http.StatusBadGateway, "worker_cancel_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"cancelled": true,
	})
}

func defaultDemoTargetDir() (string, error) {
	baseDir := setup.DefaultLibrariesBaseDir()
	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("cannot determine home directory for demo project")
	}
	for i := 1; ; i++ {
		name := "demo"
		if i > 1 {
			name = fmt.Sprintf("demo-%d", i)
		}
		cand := filepath.Join(baseDir, name)
		st, statErr := os.Stat(cand)
		if os.IsNotExist(statErr) {
			return cand, nil
		}
		if statErr == nil && st.IsDir() {
			if entries, rerr := os.ReadDir(cand); rerr == nil && len(entries) == 0 {
				return cand, nil
			}
		}
	}
}

var errSymlinkNotAllowed = errors.New("refusing to write through symlink")

func rejectSymlinkTarget(target string) error {
	cur := filepath.Clean(target)
	for {
		info, err := os.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %s", errSymlinkNotAllowed, cur)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil
		}
		cur = parent
	}
}

func ensureNoSymlinkTree(root, outPath string) error {
	if err := rejectSymlinkTarget(root); err != nil {
		return err
	}
	rel, err := filepath.Rel(root, outPath)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	cur := root
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", errSymlinkNotAllowed, cur)
		}
	}
	return nil
}

func copyEmbeddedFS(src fs.FS, targetDir string) error {
	if err := rejectSymlinkTarget(targetDir); err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(src, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(targetDir, filepath.FromSlash(rel))
		if err := ensureNoSymlinkTree(targetDir, dstPath); err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		data, err := fs.ReadFile(src, rel)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0o644)
	})
}

func validateScaffoldModelName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if strings.HasSuffix(strings.ToLower(name), ".py") {
		name = strings.TrimSpace(name[:len(name)-3])
	}
	if name == "" || len(name) > 64 {
		return "", fmt.Errorf("Model name must be 1–64 characters.")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return "", fmt.Errorf("Model name cannot start or end with a dot.")
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("Model name cannot contain '..'.")
	}
	hasAlphaNum := false
	for _, r := range name {
		if r < 32 || r == 127 {
			return "", fmt.Errorf("Model name contains control characters.")
		}
		if strings.ContainsRune(`/\\:*?"<>|`, r) {
			return "", fmt.Errorf("Model name cannot contain path separators or OS-reserved characters (/ \\ : * ? \" < > |).")
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			hasAlphaNum = true
		}
	}
	if !hasAlphaNum {
		return "", fmt.Errorf("Model name must include at least one letter or number.")
	}
	if strings.HasPrefix(name, "_") {
		return "", fmt.Errorf("Model name cannot start with '_' (ignored by model discovery).")
	}
	if name == "setup" {
		return "", fmt.Errorf("Model name cannot be 'setup' (ignored by model discovery).")
	}
	baseUpper := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if windowsReservedNames[baseUpper] {
		return "", fmt.Errorf("Model name %q is a reserved Windows filename.", name)
	}
	return name, nil
}

func splitIdentifierWords(name string) []string {
	var words []string
	var cur []rune
	runes := []rune(name)
	isLower := func(r rune) bool { return r >= 'a' && r <= 'z' }
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	isDigit := func(r rune) bool { return r >= '0' && r <= '9' }
	isAlphaNum := func(r rune) bool { return isLower(r) || isUpper(r) || isDigit(r) }

	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	for i, r := range runes {
		if !isAlphaNum(r) {
			flush()
			continue
		}
		if len(cur) > 0 && isUpper(r) {
			prev := cur[len(cur)-1]
			nextIsLower := i+1 < len(runes) && isLower(runes[i+1])
			if isLower(prev) || isDigit(prev) || (isUpper(prev) && nextIsLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

func toSnakeIdentifier(name string) string {
	words := splitIdentifierWords(name)
	if len(words) == 0 {
		return "model"
	}
	parts := make([]string, 0, len(words))
	for _, w := range words {
		parts = append(parts, strings.ToLower(w))
	}
	return strings.Join(parts, "_")
}

func toPascalCase(name string) string {
	words := splitIdentifierWords(name)
	if len(words) == 0 {
		return "Model"
	}
	var b strings.Builder
	for _, w := range words {
		if w == "" {
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]))
		if len(w) > 1 {
			b.WriteString(strings.ToLower(w[1:]))
		}
	}
	out := b.String()
	if out == "" {
		return "Model"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "Model" + out
	}
	return out
}

func buildStarterModelSource(name, template string) string {
	pascal := toPascalCase(name)
	snake := toSnakeIdentifier(name)
	if template == "script" {
		return fmt.Sprintf(`"""%s — standalone build123d script."""

import build123d as b3d

with b3d.BuildPart() as bp:
    b3d.Box(40.0, 30.0, 10.0)

part = bp.part
part.label = "%s"
`, name, pascal)
	}

	return fmt.Sprintf(`"""%s — parametric build123d model."""

from dataclasses import dataclass
from pathlib import Path

try:
    from utils.params import default_preset, group, param
except ImportError:
    import sys

    for _p in Path(__file__).resolve().parents:
        if (_p / "utils").is_dir():
            sys.path.insert(0, str(_p))
            break
    from utils.params import default_preset, group, param


EXPORT_STL: bool = False
EXPORT_STEP: bool = False


@dataclass
class CoreParams:
    """Core dimensions."""

    width: float = param(
        40.0,
        desc="Overall width",
        unit="mm", min=5.0, max=300.0, step=1.0,
    )
    depth: float = param(
        30.0,
        desc="Overall depth",
        unit="mm", min=5.0, max=300.0, step=1.0,
    )
    height: float = param(
        10.0,
        desc="Overall height",
        unit="mm", min=1.0, max=200.0, step=0.5,
    )


@default_preset("Standard Block", desc="Default starter dimensions")
@dataclass
class %sParams:
    core: CoreParams = group(CoreParams)


def export_filename(params: %sParams) -> str:
    return f"%s_{int(params.core.width)}x{int(params.core.depth)}x{int(params.core.height)}mm"


def create_%s(params: %sParams):
    import build123d as b3d

    with b3d.BuildPart() as bp:
        b3d.Box(params.core.width, params.core.depth, params.core.height)
    part = bp.part
    part.label = "%s"
    return part


if __name__ == "__main__":
    from ocp_vscode import show

    params = %sParams()
    part = create_%s(params)
    show(part)
`, name, pascal, pascal, snake, snake, pascal, pascal, pascal, snake)
}

func writeExclusiveFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (s *Server) handleScaffoldModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode      string `json:"mode"`
		TargetDir string `json:"target_dir"`
		LibraryID string `json:"library_id"`
		Name      string `json:"name"`
		Template  string `json:"template"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "demo":
		if s.demoFS == nil {
			writeError(w, http.StatusInternalServerError, "scaffold_unavailable", "Embedded demo project assets are not configured")
			return
		}
		targetDir := setup.ExpandPath(req.TargetDir)
		if targetDir == "" {
			var err error
			targetDir, err = defaultDemoTargetDir()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
				return
			}
		}
		absTarget, err := filepath.Abs(targetDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_path", err.Error())
			return
		}
		targetDir = absTarget

		if err := rejectSymlinkTarget(targetDir); err != nil {
			if errors.Is(err, errSymlinkNotAllowed) {
				writeError(w, http.StatusConflict, "symlink_not_allowed", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
			return
		}

		if st, err := os.Stat(targetDir); err == nil {
			if !st.IsDir() {
				writeError(w, http.StatusConflict, "target_not_dir", "Target path exists and is not a directory")
				return
			}
			entries, rerr := os.ReadDir(targetDir)
			if rerr != nil {
				writeError(w, http.StatusInternalServerError, "scaffold_failed", rerr.Error())
				return
			}
			if len(entries) > 0 {
				writeError(w, http.StatusConflict, "target_not_empty", "Target directory is not empty")
				return
			}
		} else if !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
			return
		}

		if err := copyEmbeddedFS(s.demoFS, targetDir); err != nil {
			if errors.Is(err, errSymlinkNotAllowed) {
				writeError(w, http.StatusConflict, "symlink_not_allowed", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
			return
		}

		cfg, err := s.cfgStore.Update(func(c *config.Config) {
			found := false
			for _, lib := range c.Libraries {
				if config.SamePath(lib.Path, targetDir) {
					found = true
					break
				}
			}
			if !found {
				c.Libraries = append(c.Libraries, config.Library{
					Name: filepath.Base(targetDir),
					Path: targetDir,
				})
			}
			c.OnboardingCompleted = true
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "config_save_error", err.Error())
			return
		}

		var libID string
		for _, lib := range cfg.Libraries {
			if config.SamePath(lib.Path, targetDir) {
				libID = lib.ID
				break
			}
		}

		workerErr := s.worker.ConfigureOrRestart(r.Context(), cfg.ResolvePython(), librarySpecs(cfg))
		if roots := cfg.LibraryRoots(); len(roots) > 0 {
			_ = s.watcher.Watch(roots)
		}
		health, healthErr := s.worker.Health(r.Context())
		cfgResp := s.buildConfigResponse(cfg, health, workerErr, healthErr)

		pyPath := filepath.Join(targetDir, "models", "spool_cap", "spool_cap.py")
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"target_dir": targetDir,
			"library_id": libID,
			"model_id":   libID + "/spool_cap",
			"py_path":    pyPath,
			"path":       pyPath,
			"config":     cfgResp,
		})

	case "new":
		name, err := validateScaffoldModelName(req.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_model_name", err.Error())
			return
		}
		tmpl := strings.ToLower(strings.TrimSpace(req.Template))
		if tmpl == "" {
			tmpl = "parametric"
		}
		if tmpl != "parametric" && tmpl != "script" {
			writeError(w, http.StatusBadRequest, "invalid_template", "Template must be 'parametric' or 'script'")
			return
		}

		cfg := s.cfgStore.Get()
		if len(cfg.Libraries) == 0 {
			writeError(w, http.StatusBadRequest, "no_library", "Configure a model library before creating a new model")
			return
		}
		var targetLib config.Library
		if libID := strings.TrimSpace(req.LibraryID); libID != "" {
			lib, ok := cfg.FindLibrary(libID)
			if !ok {
				writeError(w, http.StatusNotFound, "library_not_found", fmt.Sprintf("Library %q not found", libID))
				return
			}
			targetLib = lib
		} else {
			targetLib = cfg.Libraries[0]
		}

		if err := rejectSymlinkTarget(targetLib.Path); err != nil {
			if errors.Is(err, errSymlinkNotAllowed) {
				writeError(w, http.StatusConflict, "symlink_not_allowed", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
			return
		}

		if tmpl == "parametric" && len(s.paramsPy) > 0 {
			utilsDir := filepath.Join(targetLib.Path, "utils")
			paramsPath := filepath.Join(utilsDir, "params.py")
			if err := ensureNoSymlinkTree(targetLib.Path, paramsPath); err != nil {
				if errors.Is(err, errSymlinkNotAllowed) {
					writeError(w, http.StatusConflict, "symlink_not_allowed", err.Error())
					return
				}
				writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
				return
			}
			if err := os.MkdirAll(utilsDir, 0o755); err != nil {
				writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
				return
			}
			if err := writeExclusiveFile(paramsPath, s.paramsPy); err != nil && !os.IsExist(err) {
				writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
				return
			}
		}

		modelDir := filepath.Join(targetLib.Path, "models", name)
		pyPath := filepath.Join(modelDir, name+".py")
		if err := ensureNoSymlinkTree(targetLib.Path, pyPath); err != nil {
			if errors.Is(err, errSymlinkNotAllowed) {
				writeError(w, http.StatusConflict, "symlink_not_allowed", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
			return
		}
		if err := os.MkdirAll(modelDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
			return
		}
		src := buildStarterModelSource(name, tmpl)
		if err := writeExclusiveFile(pyPath, []byte(src)); err != nil {
			if os.IsExist(err) {
				writeError(w, http.StatusConflict, "model_exists", fmt.Sprintf("Model already exists at %s", pyPath))
				return
			}
			writeError(w, http.StatusInternalServerError, "scaffold_failed", err.Error())
			return
		}

		if running, _ := s.worker.Status(); running {
			_ = s.worker.Reload(r.Context())
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"target_dir": targetLib.Path,
			"library_id": targetLib.ID,
			"model_id":   targetLib.ID + "/" + name,
			"py_path":    pyPath,
			"path":       pyPath,
		})

	default:
		writeError(w, http.StatusBadRequest, "invalid_mode", "Scaffold mode must be 'demo' or 'new'")
	}
}

func splitCommandTokens(raw string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inToken := false
	inSingle := false
	inDouble := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(ch)
			}
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(raw) && (raw[i+1] == '"' || raw[i+1] == '\\') {
				i++
				cur.WriteByte(raw[i])
			} else if ch == '"' {
				inDouble = false
			} else {
				cur.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case ' ', '\t', '\r', '\n':
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}
		case '\'':
			inSingle = true
			inToken = true
		case '"':
			inDouble = true
			inToken = true
		default:
			cur.WriteByte(ch)
			inToken = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote in command")
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

func resolveContainmentRoots(cfg config.Config, includeExports bool) []string {
	var rawRoots []string
	rawRoots = append(rawRoots, cfg.LibraryRoots()...)
	if includeExports {
		if exp := strings.TrimSpace(cfg.ExportDir); exp != "" {
			rawRoots = append(rawRoots, config.ExpandPath(exp))
		}
		for _, lib := range cfg.Libraries {
			if lib.Path != "" {
				rawRoots = append(rawRoots, filepath.Join(lib.Path, "exports"))
			}
		}
	}
	var resolved []string
	seen := make(map[string]struct{}, len(rawRoots))
	for _, r := range rawRoots {
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		eval, err := filepath.EvalSymlinks(abs)
		if err != nil {
			continue
		}
		if _, ok := seen[eval]; ok {
			continue
		}
		seen[eval] = struct{}{}
		resolved = append(resolved, eval)
	}
	return resolved
}

func isPathContainedInAny(target string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, target)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *Server) validateDesktopPath(w http.ResponseWriter, r *http.Request, includeExports bool, requireFile bool) (config.Config, string, bool) {
	if s.dialogs == nil {
		writeError(w, http.StatusBadRequest, "desktop_unavailable", "Desktop actions are unavailable in server mode")
		return config.Config{}, "", false
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &req, false) {
		return config.Config{}, "", false
	}
	rawPath := strings.TrimSpace(req.Path)
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "invalid_path", "Path is required")
		return config.Config{}, "", false
	}
	expanded := setup.ExpandPath(rawPath)
	absPath, err := filepath.Abs(expanded)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_path", "Invalid path")
		return config.Config{}, "", false
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "path_not_found", "Target path does not exist")
			return config.Config{}, "", false
		}
		writeError(w, http.StatusBadRequest, "invalid_path", err.Error())
		return config.Config{}, "", false
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "path_not_found", err.Error())
		return config.Config{}, "", false
	}
	if requireFile && info.IsDir() {
		writeError(w, http.StatusBadRequest, "invalid_path", "Target path must be a file")
		return config.Config{}, "", false
	}

	cfg := s.cfgStore.Get()
	roots := resolveContainmentRoots(cfg, includeExports)
	if !isPathContainedInAny(resolvedPath, roots) {
		writeError(w, http.StatusForbidden, "forbidden_path", "Path is outside configured library and export directories")
		return config.Config{}, "", false
	}
	return cfg, resolvedPath, true
}

func buildDesktopCommand(customCmd, targetPath string, reveal bool) (string, []string, error) {
	if strings.TrimSpace(customCmd) != "" {
		tokens, err := splitCommandTokens(customCmd)
		if err != nil {
			return "", nil, err
		}
		if len(tokens) > 0 {
			replaced := false
			for i := 1; i < len(tokens); i++ {
				if strings.Contains(tokens[i], "{file}") || strings.Contains(tokens[i], "{path}") {
					tokens[i] = strings.ReplaceAll(tokens[i], "{file}", targetPath)
					tokens[i] = strings.ReplaceAll(tokens[i], "{path}", targetPath)
					replaced = true
				}
			}
			if !replaced {
				tokens = append(tokens, targetPath)
			}
			return tokens[0], tokens[1:], nil
		}
	}

	switch runtime.GOOS {
	case "darwin":
		if reveal {
			return "open", []string{"-R", targetPath}, nil
		}
		return "open", []string{targetPath}, nil
	case "windows":
		if reveal {
			return "explorer.exe", []string{"/select," + targetPath}, nil
		}
		return "explorer.exe", []string{targetPath}, nil
	default:
		if reveal {
			dir := targetPath
			if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
				dir = filepath.Dir(targetPath)
			}
			return "xdg-open", []string{dir}, nil
		}
		return "xdg-open", []string{targetPath}, nil
	}
}

func (s *Server) handleDesktopOpenEditor(w http.ResponseWriter, r *http.Request) {
	cfg, resolvedPath, ok := s.validateDesktopPath(w, r, false, true)
	if !ok {
		return
	}
	cmdName, args, err := buildDesktopCommand(cfg.EditorCommand, resolvedPath, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_editor_command", err.Error())
		return
	}
	if err := s.desktopRunner(cmdName, args...); err != nil {
		writeError(w, http.StatusInternalServerError, "desktop_launch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDesktopRevealFile(w http.ResponseWriter, r *http.Request) {
	_, resolvedPath, ok := s.validateDesktopPath(w, r, true, false)
	if !ok {
		return
	}
	cmdName, args, err := buildDesktopCommand("", resolvedPath, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_command", err.Error())
		return
	}
	if err := s.desktopRunner(cmdName, args...); err != nil {
		writeError(w, http.StatusInternalServerError, "desktop_launch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDesktopOpenSlicer(w http.ResponseWriter, r *http.Request) {
	cfg, resolvedPath, ok := s.validateDesktopPath(w, r, true, true)
	if !ok {
		return
	}
	ext := strings.ToLower(filepath.Ext(resolvedPath))
	if ext != ".stl" && ext != ".3mf" && ext != ".step" && ext != ".stp" {
		writeError(w, http.StatusBadRequest, "invalid_slicer_file", "Slicer actions only support .stl, .3mf, .step, or .stp files")
		return
	}
	cmdName, args, err := buildDesktopCommand(cfg.SlicerCommand, resolvedPath, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_slicer_command", err.Error())
		return
	}
	if err := s.desktopRunner(cmdName, args...); err != nil {
		writeError(w, http.StatusInternalServerError, "desktop_launch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}

	resp, err := s.worker.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "worker_error", err.Error())
		return
	}
	if !resp.OK {
		writeError(w, http.StatusUnprocessableEntity, "worker_models_error", resp.Error)
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

func writeWorkerSchemaError(w http.ResponseWriter, schema *worker.SchemaResponse, err error) {
	if err != nil {
		writeError(w, http.StatusBadGateway, "worker_error", err.Error())
		return
	}
	msg := "failed to load model schema"
	tb := ""
	if schema != nil {
		if schema.Error != "" {
			msg = schema.Error
		}
		tb = schema.Traceback
	}
	if strings.HasPrefix(msg, "Unknown model:") {
		writeError(w, http.StatusNotFound, "model_not_found", msg, tb)
		return
	}
	writeError(w, http.StatusUnprocessableEntity, "model_error", msg, tb)
}

func (s *Server) handleGetModel(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeWorkerSchemaError(w, schema, err)
		return
	}

	schemaShape := buildSchemaShape(schema.Defaults, schema.Fields)
	presetList, err := presets.ListStrict(modelID, schema.Model.Dir, schemaShape, schemaDefaultPresetMeta(schema))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_presets_file", err.Error())
		return
	}
	fields := schema.Fields
	if fields == nil {
		fields = []worker.ParamField{}
	}
	defaults := schema.Defaults
	if defaults == nil {
		defaults = map[string]any{}
	}
	componentBundles := s.loadComponentBundles(r.Context(), modelID, fields)

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
		"components":         componentBundles,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleRenderModel(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	var req struct {
		Params map[string]any `json:"params"`
	}
	if !decodeJSON(w, r, &req, true) {
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}

	statusCode, raw, err := s.worker.RenderRaw(r.Context(), modelID, req.Params)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeError(w, http.StatusBadGateway, "worker_error", err.Error())
		return
	}

	if statusCode == http.StatusOK {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}

	var errPayload struct {
		Error     string `json:"error"`
		Traceback string `json:"traceback"`
	}
	_ = json.Unmarshal(raw, &errPayload)
	msg := errPayload.Error
	if msg == "" {
		msg = fmt.Sprintf("worker render failed (HTTP %d)", statusCode)
	}
	switch statusCode {
	case http.StatusNotFound:
		writeError(w, http.StatusNotFound, "model_not_found", msg, errPayload.Traceback)
	case http.StatusBadRequest:
		writeError(w, http.StatusBadRequest, "bad_request", msg, errPayload.Traceback)
	case http.StatusUnprocessableEntity, http.StatusInternalServerError:
		writeError(w, http.StatusUnprocessableEntity, "model_build_error", msg, errPayload.Traceback)
	default:
		writeError(w, http.StatusBadGateway, "worker_error", msg, errPayload.Traceback)
	}
}

func (s *Server) handleExportModel(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	var req struct {
		Params            map[string]any `json:"params"`
		Format            string         `json:"format"`
		Quality           string         `json:"quality"`
		LinearDeflection  *float64       `json:"linear_deflection"`
		AngularDeflection *float64       `json:"angular_deflection"`
		Filename          string         `json:"filename"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}

	fmtName := strings.ToLower(strings.TrimSpace(req.Format))
	if fmtName == "" {
		fmtName = "step"
	}
	if !isValidExportFormat(fmtName) {
		writeError(w, http.StatusBadRequest, "invalid_export_format", fmt.Sprintf("Unsupported export format: %s", req.Format))
		return
	}

	cfg := s.cfgStore.Get()
	qualityInput := req.Quality
	if strings.TrimSpace(qualityInput) == "" {
		qualityInput = cfg.ExportQuality
	}
	quality, err := validateExportQuality(qualityInput, req.LinearDeflection, req.AngularDeflection)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_export_quality", err.Error())
		return
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}

	var stem string
	var libraryID string
	if customStem := strings.TrimSpace(req.Filename); customStem != "" {
		stem = sanitizeExportStem(customStem, fallbackStemFromID(modelID))
		if lib, _, found := strings.Cut(modelID, "/"); found {
			libraryID = lib
		}
	} else {
		var nameErr error
		stem, libraryID, nameErr = s.worker.ExportName(r.Context(), modelID, req.Params)
		if nameErr != nil || strings.TrimSpace(stem) == "" {
			stem = fallbackStemFromID(modelID)
		} else {
			stem = sanitizeExportStem(stem, fallbackStemFromID(modelID))
		}
		if libraryID == "" {
			if lib, _, found := strings.Cut(modelID, "/"); found {
				libraryID = lib
			}
		}
	}
	defaultFile := fmt.Sprintf("%s.%s", stem, fmtName)

	opts := worker.ExportOptions{
		Format:            fmtName,
		Quality:           quality,
		LinearDeflection:  req.LinearDeflection,
		AngularDeflection: req.AngularDeflection,
	}

	if s.dialogs == nil {
		raw, err := s.worker.ExportBytesWithOptions(r.Context(), modelID, req.Params, opts)
		if err != nil {
			if strings.Contains(err.Error(), "export failed (HTTP 404)") || strings.Contains(err.Error(), "Unknown model:") {
				writeError(w, http.StatusNotFound, "model_not_found", err.Error())
				return
			}
			if strings.Contains(err.Error(), "export failed (HTTP ") {
				writeError(w, http.StatusUnprocessableEntity, "export_failed", err.Error())
				return
			}
			writeError(w, http.StatusBadGateway, "worker_error", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if disp := mime.FormatMediaType("attachment", map[string]string{"filename": defaultFile}); disp != "" {
			w.Header().Set("Content-Disposition", disp)
		} else {
			w.Header().Set("Content-Disposition", "attachment")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}

	filterName := strings.ToUpper(fmtName) + " Files"
	filterPattern := "*." + fmtName

	exportDir := cfg.ResolveExportDir(libraryID)
	if exportDir != "" {
		_ = os.MkdirAll(exportDir, 0o755)
	}

	outPath, err := s.dialogs.SaveFile("Export "+strings.ToUpper(fmtName), exportDir, defaultFile, filterName, filterPattern)
	if err != nil {
		if isDialogCancelError(err) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": true})
			return
		}
		writeError(w, http.StatusInternalServerError, "dialog_error", err.Error())
		return
	}
	if outPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": true})
		return
	}

	opts.OutputPath = outPath
	resp, err := s.worker.ExportToPathWithOptions(r.Context(), modelID, req.Params, opts)
	if err != nil {
		writeError(w, http.StatusBadGateway, "worker_error", err.Error())
		return
	}
	if !resp.OK {
		if strings.HasPrefix(resp.Error, "Unknown model:") {
			writeError(w, http.StatusNotFound, "model_not_found", resp.Error, resp.Traceback)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "export_failed", resp.Error, resp.Traceback)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"cancelled": false,
		"path":      resp.Path,
		"size":      resp.Size,
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
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeWorkerSchemaError(w, schema, err)
		return
	}
	schemaShape := buildSchemaShape(schema.Defaults, schema.Fields)
	presetList, err := presets.ListStrict(modelID, schema.Model.Dir, schemaShape, schemaDefaultPresetMeta(schema))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_presets_file", err.Error())
		return
	}
	componentBundles := s.loadComponentBundles(r.Context(), modelID, schema.Fields)
	cfg := s.cfgStore.Get()
	defaultPresetKey := ""
	if cfg.DefaultPresets != nil {
		defaultPresetKey = cfg.DefaultPresets[modelID]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"presets":            presetList,
		"components":         componentBundles,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleSavePreset(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	var req struct {
		Label            string                `json:"label"`
		Description      string                `json:"description"`
		Scope            string                `json:"scope"`
		FullSnapshot     bool                  `json:"full_snapshot"`
		Params           map[string]any        `json:"params"`
		ComponentPresets map[string]string     `json:"component_presets,omitempty"`
		Verification     *presets.Verification `json:"verification,omitempty"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		writeError(w, http.StatusBadRequest, "invalid_preset_label", "Preset name is required")
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}

	var verification *presets.Verification
	if req.Verification != nil {
		status, err := presets.ParseVerificationStatus(string(req.Verification.Status))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_verification_status", err.Error())
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
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}

	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeWorkerSchemaError(w, schema, err)
		return
	}

	schemaShape := buildSchemaShape(schema.Defaults, schema.Fields)
	componentBundles := s.loadComponentBundles(r.Context(), modelID, schema.Fields)
	normalizedCompPresets := normalizeModelComponentPresets(
		req.ComponentPresets,
		req.Params,
		schema.Defaults,
		schema.Fields,
	)
	effectiveDefaults := buildEffectiveSaveDefaults(
		schema.Defaults,
		schema.Fields,
		normalizedCompPresets,
		componentBundles,
	)

	key, err := presets.Save(modelID, schema.Model.Dir, presets.SaveOptions{
		Label:            label,
		Description:      strings.TrimSpace(req.Description),
		Scope:            scope,
		FullSnapshot:     req.FullSnapshot,
		CurrentParams:    req.Params,
		Defaults:         effectiveDefaults,
		SchemaShape:      schemaShape,
		ComponentPresets: normalizedCompPresets,
		Verification:     verification,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "preset_save_error", err.Error())
		return
	}

	_, _ = s.cfgStore.Update(func(c *config.Config) {
		c.PresetSaveScope = scope
	})

	presetList, err := presets.ListStrict(modelID, schema.Model.Dir, schemaShape, schemaDefaultPresetMeta(schema))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_presets_file", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"selected_key":      key,
		"presets":           presetList,
		"components":        componentBundles,
		"preset_save_scope": scope,
	})
}

func (s *Server) handleSetPresetVerification(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	var req struct {
		Scope  string `json:"scope"`
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}

	status, err := presets.ParseVerificationStatus(req.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_verification_status", err.Error())
		return
	}

	scope := strings.TrimSpace(req.Scope)
	if key == "default" {
		scope = "default"
	} else if scope != "project" {
		scope = "user"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeWorkerSchemaError(w, schema, err)
		return
	}

	err = presets.SetVerification(modelID, schema.Model.Dir, key, scope, &presets.Verification{
		Status: status,
		Note:   req.Note,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "preset_verification_error", err.Error())
		return
	}

	presetList, err := presets.ListStrict(modelID, schema.Model.Dir, buildSchemaShape(schema.Defaults, schema.Fields), schemaDefaultPresetMeta(schema))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_presets_file", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"presets": presetList,
	})
}

func (s *Server) handleRenamePreset(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	oldKey := strings.TrimSpace(r.PathValue("key"))
	var req struct {
		Scope       string `json:"scope"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope != "project" {
		scope = "user"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeWorkerSchemaError(w, schema, err)
		return
	}

	newKey, err := presets.Rename(modelID, schema.Model.Dir, oldKey, scope, req.Label, req.Description)
	if err != nil {
		writeError(w, http.StatusBadRequest, "preset_rename_error", err.Error())
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

	presetList, err := presets.ListStrict(modelID, schema.Model.Dir, buildSchemaShape(schema.Defaults, schema.Fields), schemaDefaultPresetMeta(schema))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_presets_file", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"key":                newKey,
		"presets":            presetList,
		"default_preset_key": defaultPresetKey,
	})
}

func (s *Server) handleDeletePreset(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope != "project" {
		scope = "user"
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}
	schema, err := s.worker.GetSchema(r.Context(), modelID)
	if err != nil || !schema.OK {
		writeWorkerSchemaError(w, schema, err)
		return
	}

	if err := presets.Delete(modelID, schema.Model.Dir, key, scope); err != nil {
		writeError(w, http.StatusBadRequest, "preset_delete_error", err.Error())
		return
	}

	presetList, err := presets.ListStrict(modelID, schema.Model.Dir, buildSchemaShape(schema.Defaults, schema.Fields), schemaDefaultPresetMeta(schema))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_presets_file", err.Error())
		return
	}
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
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &req, false) {
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
		writeError(w, http.StatusInternalServerError, "config_save_error", err.Error())
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
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	var req struct {
		Params         map[string]any `json:"params"`
		DryRun         bool           `json:"dry_run"`
		ExpectedSHA256 string         `json:"expected_sha256"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}

	resp, err := s.worker.UpdateDefaults(r.Context(), modelID, req.Params, req.DryRun, strings.TrimSpace(req.ExpectedSHA256))
	if err != nil {
		writeError(w, http.StatusBadGateway, "worker_error", err.Error())
		return
	}
	if !req.DryRun && resp.OK && resp.Updated > 0 && resp.File != "" {
		_ = presets.DowngradeDefaultVerificationForFile(resp.File)
	}
	if resp.Changes == nil {
		resp.Changes = []worker.DefaultChange{}
	}
	if resp.Unsupported == nil {
		resp.Unsupported = []worker.UnsupportedDefaultChange{}
	}
	if !resp.OK {
		if strings.HasPrefix(resp.Error, "Unknown model:") {
			writeError(w, http.StatusNotFound, "model_not_found", resp.Error, resp.Traceback)
			return
		}
		if strings.Contains(resp.Error, "expected_sha256 is required") {
			writeError(w, http.StatusBadRequest, "missing_sha256", resp.Error)
			return
		}
		if len(resp.Unsupported) == 0 && len(resp.Changes) == 0 && resp.SHA256 == "" {
			writeError(w, http.StatusUnprocessableEntity, "update_defaults_failed", resp.Error, resp.Traceback)
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRenameModel(w http.ResponseWriter, r *http.Request) {
	modelID, ok := extractModelID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		FileStem *string `json:"file_stem"`
	}
	if !decodeJSON(w, r, &req, false) {
		return
	}
	if req.Name == nil && req.FileStem == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "name or file_stem is required")
		return
	}

	if err := s.ensureWorkerRunning(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "worker_unavailable", err.Error())
		return
	}

	resp, err := s.worker.RenameModel(r.Context(), modelID, req.Name, req.FileStem)
	if err != nil {
		writeError(w, http.StatusBadGateway, "worker_error", err.Error())
		return
	}
	if !resp.OK {
		status := http.StatusBadRequest
		code := "invalid_name"
		switch {
		case strings.HasPrefix(resp.Error, "Unknown model:"):
			status = http.StatusNotFound
			code = "model_not_found"
		case strings.Contains(resp.Error, "already exists"):
			status = http.StatusConflict
			code = "already_exists"
		case strings.Contains(resp.Error, "outside its configured library"):
			status = http.StatusForbidden
			code = "forbidden_path"
		}
		writeError(w, status, code, resp.Error, resp.Traceback)
		return
	}
	if resp.ModelID != "" && resp.ModelID != modelID {
		_ = presets.MoveUserPresets(modelID, resp.ModelID)
	}
	writeJSON(w, http.StatusOK, resp)
}
