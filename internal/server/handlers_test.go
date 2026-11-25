package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

type stubDialogs struct {
	dirPath     string
	dirErr      error
	filePath    string
	fileErr     error
	savePath    string
	saveErr     error
	lastSaveDir string
	lastSave    string
}

func (d *stubDialogs) ChooseDirectory(title, startDir string) (string, error) {
	if d.dirErr != nil {
		return "", d.dirErr
	}
	return d.dirPath, nil
}

func (d *stubDialogs) ChooseFile(title, startDir string) (string, error) {
	if d.fileErr != nil {
		return "", d.fileErr
	}
	return d.filePath, nil
}

func (d *stubDialogs) SaveFile(title, defaultDir, defaultFilename, filterName, filterPattern string) (string, error) {
	d.lastSaveDir = defaultDir
	d.lastSave = defaultFilename
	if d.saveErr != nil {
		return "", d.saveErr
	}
	return d.savePath, nil
}

type stubWindow struct {
	mu        sync.Mutex
	minimised int
	maximised bool
	closed    int
}

func (w *stubWindow) Minimise() {
	w.mu.Lock()
	w.minimised++
	w.mu.Unlock()
}

func (w *stubWindow) ToggleMaximise() bool {
	w.mu.Lock()
	w.maximised = !w.maximised
	val := w.maximised
	w.mu.Unlock()
	return val
}

func (w *stubWindow) IsMaximised() bool {
	w.mu.Lock()
	val := w.maximised
	w.mu.Unlock()
	return val
}

func (w *stubWindow) Close() {
	w.mu.Lock()
	w.closed++
	w.mu.Unlock()
}

func setIsolatedTestEnv(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv("USERPROFILE", base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "xdg-data"))
	t.Setenv("AppData", filepath.Join(base, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(base, "localappdata"))
	return base
}

func findTestPython(t *testing.T) string {
	t.Helper()
	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "python3"}
	}
	for _, name := range candidates {
		p, err := exec.LookPath(name)
		if err != nil || p == "" {
			continue
		}
		norm := strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
		if strings.Contains(norm, "/microsoft/windowsapps/") {
			continue
		}
		if err := exec.Command(p, "--version").Run(); err == nil {
			return p
		}
	}
	t.Skip("python interpreter not available for server tests")
	return ""
}

func newAPIRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = "localhost"
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func modelPath(modelID string, suffix ...string) string {
	p := "/api/models/" + url.PathEscape(modelID)
	for _, s := range suffix {
		p += s
	}
	return p
}

func newTestServer(t *testing.T, dialogs DesktopDialogs) *Server {
	t.Helper()
	setIsolatedTestEnv(t)
	cfgStore, err := config.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	scriptBytes, err := os.ReadFile(filepath.Join("..", "..", "worker", "worker.py"))
	if err != nil {
		t.Fatalf("read worker.py: %v", err)
	}
	paramsBytes, err := os.ReadFile(filepath.Join("..", "..", "worker", "params.py"))
	if err != nil {
		t.Fatalf("read params.py: %v", err)
	}
	mgr := worker.NewManager(scriptBytes)
	srv := New(cfgStore, mgr, dialogs)
	srv.SetScaffoldAssets(os.DirFS(filepath.Join("..", "..", "examples", "demo_project")), paramsBytes)
	t.Cleanup(func() { srv.Shutdown() })
	return srv
}

func TestConfigAndThemeHandlers(t *testing.T) {
	dialogs := &stubDialogs{dirPath: "/tmp/example-project", filePath: "/usr/bin/python3"}
	srv := newTestServer(t, dialogs)

	// GET /api/config
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d", rec.Code)
	}
	var cfgResp configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	if !cfgResp.HasDesktop {
		t.Fatalf("expected has_desktop=true")
	}
	if cfgResp.OS != runtime.GOOS {
		t.Fatalf("expected os=%q, got %q", runtime.GOOS, cfgResp.OS)
	}
	if cfgResp.Config.SchemaVersion != config.SchemaVersion {
		t.Fatalf("expected schema_version=%d, got %d", config.SchemaVersion, cfgResp.Config.SchemaVersion)
	}
	if cfgResp.Config.Theme != "dark" {
		t.Fatalf("expected default theme 'dark', got %q", cfgResp.Config.Theme)
	}

	// POST /api/config/theme
	rec = httptest.NewRecorder()
	req := newAPIRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(`{"theme":"dracula"}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /api/config/theme status = %d", rec.Code)
	}

	// Verify persisted theme
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/config", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode updated config: %v", err)
	}
	if cfgResp.Config.Theme != "dracula" {
		t.Fatalf("expected persisted theme 'dracula', got %q", cfgResp.Config.Theme)
	}

	// POST /api/dialogs/project
	rec = httptest.NewRecorder()
	req = newAPIRequest(http.MethodPost, "/api/dialogs/project", bytes.NewBufferString(`{}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/dialogs/project status = %d", rec.Code)
	}
	var dlgResp struct {
		Path      string `json:"path"`
		Cancelled bool   `json:"cancelled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dlgResp); err != nil {
		t.Fatalf("decode dialog resp: %v", err)
	}
	if dlgResp.Path != "/tmp/example-project" || dlgResp.Cancelled {
		t.Fatalf("unexpected dialog resp: %+v", dlgResp)
	}

	// POST /api/dialogs/export-dir
	rec = httptest.NewRecorder()
	req = newAPIRequest(http.MethodPost, "/api/dialogs/export-dir", bytes.NewBufferString(`{}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/dialogs/export-dir status = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dlgResp); err != nil {
		t.Fatalf("decode export-dir dialog resp: %v", err)
	}
	if dlgResp.Path != "/tmp/example-project" || dlgResp.Cancelled {
		t.Fatalf("unexpected export-dir dialog resp: %+v", dlgResp)
	}
}

func TestWorkerSchemaNullableWhenAndExportFilename(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "demo_box")
	utilsDir := filepath.Join(projectDir, "utils")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(utilsDir, 0o755); err != nil {
		t.Fatalf("mkdir utils: %v", err)
	}
	// Utility scripts with create_* helpers must be skipped from model discovery
	if err := os.WriteFile(filepath.Join(utilsDir, "helper.py"), []byte("def create_helper():\n    return None\n"), 0o644); err != nil {
		t.Fatalf("write utils/helper.py: %v", err)
	}

	modelSrc := `from dataclasses import dataclass, field
from typing import Literal, Optional

@dataclass
class CoreParams:
    """Core shape settings."""
    mode: Literal["round", "rect"] = "round"  # Developer note that should NOT become UI desc
    diameter: Optional[float] = field(
        default=None,
        metadata={
            "label": "Outer Diameter",
            "description": "Nominal diameter of round body",
            "unit": "mm",
            "min": 10.0,
            "max": 200.0,
            "step": 1.0,
            "when": {"mode": "round"},
        },
    )
    width: float = field(default=50.0, metadata={"when": {"mode": "rect"}})

@dataclass
class ExtraParams:
    """Optional extra feature."""
    enabled: Optional[bool] = None

@dataclass
class DemoBoxParams:
    __default_preset__ = {"label": "Bench Demo Box", "description": "Standard benchtop enclosure"}
    core: CoreParams = field(default_factory=CoreParams)
    extra: ExtraParams = field(default_factory=ExtraParams)

def export_filename(params: DemoBoxParams) -> str:
    return f"demo_box_{params.core.mode}"

def create_demo_box(params: DemoBoxParams):
    return None
`
	if err := os.WriteFile(filepath.Join(modelDir, "demo_box.py"), []byte(modelSrc), 0o644); err != nil {
		t.Fatalf("write demo_box.py: %v", err)
	}

	dialogs := &stubDialogs{}
	srv := newTestServer(t, dialogs)

	// Save project config to start worker
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// GET /api/models
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/models", nil))
	var listResp struct {
		OK     bool               `json:"ok"`
		Models []worker.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if !listResp.OK || len(listResp.Models) != 1 || listResp.Models[0].ID != "test-lib/demo_box" {
		t.Fatalf("unexpected models list: %+v", listResp)
	}

	// GET /api/models/test-lib%2Fdemo_box
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath("test-lib/demo_box"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET model status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var modelResp struct {
		OK       bool                `json:"ok"`
		Fields   []worker.ParamField `json:"fields"`
		Defaults map[string]any      `json:"defaults"`
		Presets  []struct {
			Key         string `json:"key"`
			Label       string `json:"label"`
			Description string `json:"description"`
			Source      string `json:"source"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &modelResp); err != nil {
		t.Fatalf("decode model schema: %v", err)
	}
	if !modelResp.OK || len(modelResp.Fields) != 2 {
		t.Fatalf("unexpected schema response: %+v", modelResp)
	}
	if len(modelResp.Presets) != 1 || modelResp.Presets[0].Key != "default" || modelResp.Presets[0].Label != "Bench Demo Box" || modelResp.Presets[0].Description != "Standard benchtop enclosure" {
		t.Fatalf("expected custom default preset from __default_preset__, got %+v", modelResp.Presets)
	}
	coreGroup := modelResp.Fields[0]
	if coreGroup.Description != "Core shape settings." {
		t.Fatalf("expected nested docstring fallback, got %q", coreGroup.Description)
	}
	var modeField, diaField worker.ParamField
	for _, c := range coreGroup.Children {
		if c.Name == "mode" {
			modeField = c
		}
		if c.Name == "diameter" {
			diaField = c
		}
	}
	if modeField.Description != "" {
		t.Fatalf("expected inline comment not to leak into mode.description, got %q", modeField.Description)
	}
	if !diaField.Nullable {
		t.Fatalf("expected diameter.nullable=true")
	}
	if diaField.Label != "Outer Diameter" {
		t.Fatalf("expected diameter.label='Outer Diameter', got %q", diaField.Label)
	}
	if diaField.Description != "Nominal diameter of round body" {
		t.Fatalf("expected diameter.description='Nominal diameter of round body', got %q", diaField.Description)
	}
	if diaField.Unit != "mm" {
		t.Fatalf("expected diameter.unit='mm', got %q", diaField.Unit)
	}
	if diaField.When["mode"] != "round" {
		t.Fatalf("expected diameter.when.mode='round', got %+v", diaField.When)
	}

	extraGroup := modelResp.Fields[1]
	if extraGroup.Description != "Optional extra feature." {
		t.Fatalf("expected extra docstring fallback, got %q", extraGroup.Description)
	}
	if len(extraGroup.Children) != 1 || !extraGroup.Children[0].Nullable {
		t.Fatalf("expected extra.enabled nullable=true, got %+v", extraGroup.Children)
	}

	// Verify export_filename & default export dir (<library>/exports) via SaveFile
	rec = httptest.NewRecorder()
	exportBody := bytes.NewBufferString(`{"format":"stl","params":{"core":{"mode":"rect"}}}`)
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/demo_box", "/export"), exportBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST export status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if dialogs.lastSave != "demo_box_rect.stl" {
		t.Fatalf("expected SaveFile default filename 'demo_box_rect.stl', got %q", dialogs.lastSave)
	}
	wantDefaultExportDir := filepath.Join(projectDir, "exports")
	if dialogs.lastSaveDir != wantDefaultExportDir {
		t.Fatalf("expected SaveFile defaultDir %q, got %q", wantDefaultExportDir, dialogs.lastSaveDir)
	}

	// Configure a custom export_dir and verify SaveFile uses it
	customExportDir := filepath.Join(t.TempDir(), "custom-exports")
	saveBody, _ = json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
		"export_dir":  customExportDir,
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config with export_dir status = %d", rec.Code)
	}
	var customCfgResp configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &customCfgResp); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	if customCfgResp.Config.ExportDir != customExportDir || customCfgResp.ResolvedExportDir != customExportDir {
		t.Fatalf("unexpected export_dir config response: %+v", customCfgResp)
	}

	rec = httptest.NewRecorder()
	exportBody = bytes.NewBufferString(`{"format":"step","params":{"core":{"mode":"round"}}}`)
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/demo_box", "/export"), exportBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST export (custom dir) status = %d", rec.Code)
	}
	if dialogs.lastSaveDir != customExportDir {
		t.Fatalf("expected SaveFile custom defaultDir %q, got %q", customExportDir, dialogs.lastSaveDir)
	}
	if dialogs.lastSave != "demo_box_round.step" {
		t.Fatalf("expected SaveFile filename 'demo_box_round.step', got %q", dialogs.lastSave)
	}
}

func TestWorkerListCoercionAndEmptyStringHandling(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "list_demo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	modelSrc := `from dataclasses import dataclass, field
from typing import List, Optional

@dataclass
class SlotsParams:
    z_positions: Optional[List[float]] = None

@dataclass
class ListDemoParams:
    slots: SlotsParams = field(default_factory=SlotsParams)

def export_filename(params: ListDemoParams) -> str:
    if params.slots.z_positions is None:
        return "list_demo_auto"
    return "list_demo_" + "_".join(str(int(v)) for v in params.slots.z_positions)

def create_list_demo(params: ListDemoParams):
    return None
`
	if err := os.WriteFile(filepath.Join(modelDir, "list_demo.py"), []byte(modelSrc), 0o644); err != nil {
		t.Fatalf("write list_demo.py: %v", err)
	}

	dialogs := &stubDialogs{}
	srv := newTestServer(t, dialogs)
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	// Empty string must coerce to None (not empty string)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/list_demo", "/export"), bytes.NewBufferString(`{"format":"stl","params":{"slots":{"z_positions":""}}}`)))
	if dialogs.lastSave != "list_demo_auto.stl" {
		t.Fatalf("expected empty string to coerce to None -> 'list_demo_auto.stl', got %q", dialogs.lastSave)
	}

	// Comma-separated string must coerce to List[float]
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/list_demo", "/export"), bytes.NewBufferString(`{"format":"stl","params":{"slots":{"z_positions":"10, 25.0, 40"}}}`)))
	if dialogs.lastSave != "list_demo_10_25_40.stl" {
		t.Fatalf("expected CSV list coercion -> 'list_demo_10_25_40.stl', got %q", dialogs.lastSave)
	}

	// Verify warm reload picks up edited .py without restarting worker
	editedSrc := `from dataclasses import dataclass, field
from typing import List, Optional

@dataclass
class SlotsParams:
    z_positions: Optional[List[float]] = None

@dataclass
class ListDemoParams:
    slots: SlotsParams = field(default_factory=SlotsParams)

def export_filename(params: ListDemoParams) -> str:
    return "list_demo_reloaded"

def create_list_demo(params: ListDemoParams):
    return None
`
	if err := os.WriteFile(filepath.Join(modelDir, "list_demo.py"), []byte(editedSrc), 0o644); err != nil {
		t.Fatalf("rewrite list_demo.py: %v", err)
	}
	if err := srv.worker.Reload(t.Context()); err != nil {
		t.Fatalf("warm Reload failed: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/list_demo", "/export"), bytes.NewBufferString(`{"format":"stl","params":{}}`)))
	if dialogs.lastSave != "list_demo_reloaded.stl" {
		t.Fatalf("expected warm reload to update module -> 'list_demo_reloaded.stl', got %q", dialogs.lastSave)
	}
}

func TestPresetsPinDeleteAndSurgicalUpdateDefaults(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "clip_demo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	modelSrc := `from dataclasses import dataclass, field
from typing import Optional

BASE_GAP = 1.0

def param(default, **kwargs):
    return field(default=default, metadata=kwargs)

@dataclass
class CoreParams:
    """Core dimensions — UTF-8 check (°)."""
    thickness: float = param(
        12.0,
        desc="Thickness — keep this comment intact",
        unit="mm",
    )
    angle: float = 95.0  # Opening angle in °
    label_text: str = "spools"
    enabled: bool = True
    override: Optional[float] = None
    computed_gap: float = BASE_GAP * 2.0

@dataclass
class ClipDemoParams:
    core: CoreParams = field(default_factory=CoreParams)

def create_clip_demo(params: ClipDemoParams):
    return None
`
	modelFile := filepath.Join(modelDir, "clip_demo.py")
	if err := os.WriteFile(modelFile, []byte(modelSrc), 0o644); err != nil {
		t.Fatalf("write clip_demo.py: %v", err)
	}

	dialogs := &stubDialogs{}
	srv := newTestServer(t, dialogs)
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	// 1. Save a local preset, pin as startup default, promote to project, then delete
	rec = httptest.NewRecorder()
	savePresetReq := `{"label":"Thin Clip","description":"6mm","scope":"user","full_snapshot":false,"params":{"core":{"thickness":6.0,"angle":95.0}}}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/presets"), bytes.NewBufferString(savePresetReq)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST presets status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/presets/default"), bytes.NewBufferString(`{"key":"thin_clip"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST presets/default status = %d", rec.Code)
	}

	// GET /api/models/test-lib%2Fclip_demo should return pinned default_preset_key
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath("test-lib/clip_demo"), nil))
	var detailResp struct {
		OK               bool   `json:"ok"`
		DefaultPresetKey string `json:"default_preset_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detailResp); err != nil || !detailResp.OK || detailResp.DefaultPresetKey != "thin_clip" {
		t.Fatalf("expected default_preset_key='thin_clip', got %+v", detailResp)
	}

	// Save same preset to project scope (replaces/clears shadowed user copy)
	rec = httptest.NewRecorder()
	saveProjReq := `{"label":"Thin Clip","description":"6mm shared","scope":"project","full_snapshot":true,"params":{"core":{"thickness":6.0,"angle":95.0}}}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/presets"), bytes.NewBufferString(saveProjReq)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST project preset status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Rename pinned project preset and verify startup default pin follows the new key
	rec = httptest.NewRecorder()
	renameReq := `{"scope":"project","label":"Slim Clip","description":"6mm renamed"}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPatch, modelPath("test-lib/clip_demo", "/presets/thin_clip"), bytes.NewBufferString(renameReq)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH rename preset status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var renameResp struct {
		OK               bool   `json:"ok"`
		Key              string `json:"key"`
		DefaultPresetKey string `json:"default_preset_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &renameResp); err != nil || !renameResp.OK || renameResp.Key != "slim_clip" || renameResp.DefaultPresetKey != "slim_clip" {
		t.Fatalf("expected rename to migrate default pin to 'slim_clip', got %+v", renameResp)
	}

	// Delete project preset clears pinned default_preset_key
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodDelete, modelPath("test-lib/clip_demo", "/presets/slim_clip?scope=project"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE preset status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var delResp struct {
		OK               bool   `json:"ok"`
		DefaultPresetKey string `json:"default_preset_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &delResp); err != nil || !delResp.OK || delResp.DefaultPresetKey != "" {
		t.Fatalf("expected cleared default_preset_key after delete, got %+v", delResp)
	}

	// 2. Fail-closed test: attempting to update only computed_gap (non-literal expression) must report unsupported and not write file
	rec = httptest.NewRecorder()
	badApply := `{"dry_run":false,"params":{"core":{"computed_gap":5.0}}}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/update-defaults"), bytes.NewBufferString(badApply)))
	var badResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &badResp); err != nil {
		t.Fatalf("decode badResp: %v", err)
	}
	if badResp.OK || len(badResp.Unsupported) != 1 || badResp.Unsupported[0].Path != "core.computed_gap" || badResp.Unsupported[0].ReasonCode != "non_literal_expression" || badResp.Unsupported[0].OwnerClass != "CoreParams" {
		t.Fatalf("expected fail-closed unsupported on core.computed_gap, got %+v", badResp)
	}
	afterBadBytes, _ := os.ReadFile(modelFile)
	if string(afterBadBytes) != modelSrc {
		t.Fatalf("file must remain untouched after failed update")
	}

	// 2b. Partial apply preview: mixed local literal + non-literal expression reports both
	rec = httptest.NewRecorder()
	mixedPreviewReq := `{"dry_run":true,"params":{"core":{"thickness":8.0,"computed_gap":5.0}}}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/update-defaults"), bytes.NewBufferString(mixedPreviewReq)))
	var mixedPreview worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mixedPreview); err != nil {
		t.Fatalf("decode mixedPreview: %v", err)
	}
	if !mixedPreview.OK || len(mixedPreview.Changes) != 1 || len(mixedPreview.Unsupported) != 1 {
		t.Fatalf("expected 1 local change + 1 unsupported in mixed preview, got %+v", mixedPreview)
	}

	// 3. Dry-run preview on safe literal changes
	rec = httptest.NewRecorder()
	previewReq := `{"dry_run":true,"params":{"core":{"thickness":10.5,"angle":110.0,"label_text":"prusa","enabled":false,"override":4.2}}}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/update-defaults"), bytes.NewBufferString(previewReq)))
	var previewResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &previewResp); err != nil {
		t.Fatalf("decode previewResp: %v", err)
	}
	if !previewResp.OK || len(previewResp.Changes) != 5 || len(previewResp.Unsupported) != 0 || previewResp.SHA256 == "" {
		t.Fatalf("unexpected preview response: %+v", previewResp)
	}

	// 4. Missing expected_sha256 on non-dry-run must be rejected with 400
	rec = httptest.NewRecorder()
	missingShaReq := `{"dry_run":false,"params":{"core":{"thickness":10.5}}}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/update-defaults"), bytes.NewBufferString(missingShaReq)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when expected_sha256 is missing on dry_run=false, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 5. Stale SHA-256 must abort without modifying file
	rec = httptest.NewRecorder()
	staleReq := `{"dry_run":false,"expected_sha256":"deadbeef","params":{"core":{"thickness":10.5}}}`
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/update-defaults"), bytes.NewBufferString(staleReq)))
	var staleResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &staleResp); err != nil || staleResp.OK {
		t.Fatalf("expected stale sha256 to fail, got %+v", staleResp)
	}

	// 6. Apply with matching SHA-256 and verify surgical edits + preserved comments + reloaded defaults
	applyPayload, _ := json.Marshal(map[string]any{
		"dry_run":         false,
		"expected_sha256": previewResp.SHA256,
		"params": map[string]any{
			"core": map[string]any{
				"thickness":  10.5,
				"angle":      110.0,
				"label_text": "prusa",
				"enabled":    false,
				"override":   4.2,
			},
		},
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/clip_demo", "/update-defaults"), bytes.NewReader(applyPayload)))
	var applyResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &applyResp); err != nil {
		t.Fatalf("decode applyResp: %v", err)
	}
	if !applyResp.OK || applyResp.Updated != 5 {
		t.Fatalf("expected 5 updated defaults, got %+v", applyResp)
	}

	updatedBytes, err := os.ReadFile(modelFile)
	if err != nil {
		t.Fatalf("read updated model: %v", err)
	}
	updatedStr := string(updatedBytes)
	for _, mustContain := range []string{
		`desc="Thickness — keep this comment intact"`,
		`# Opening angle in °`,
		`10.5,`,
		`angle: float = 110.0  # Opening angle in °`,
		`label_text: str = "prusa"`,
		`enabled: bool = False`,
		`override: Optional[float] = 4.2`,
		`computed_gap: float = BASE_GAP * 2.0`,
	} {
		if !bytes.Contains(updatedBytes, []byte(mustContain)) {
			t.Fatalf("updated file missing expected snippet %q\n--- file ---\n%s", mustContain, updatedStr)
		}
	}

	// Subsequent GET /api/models/test-lib%2Fclip_demo must reflect new canonical defaults immediately
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath("test-lib/clip_demo"), nil))
	var refreshed struct {
		OK       bool           `json:"ok"`
		Defaults map[string]any `json:"defaults"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshed); err != nil || !refreshed.OK {
		t.Fatalf("failed to fetch refreshed schema: %s", rec.Body.String())
	}
	coreDefaults := refreshed.Defaults["core"].(map[string]any)
	if coreDefaults["thickness"] != 10.5 || coreDefaults["angle"] != 110.0 || coreDefaults["label_text"] != "prusa" || coreDefaults["enabled"] != false || coreDefaults["override"] != 4.2 {
		t.Fatalf("refreshed defaults did not match updated values: %#v", coreDefaults)
	}
}

func TestWorkerStructuredAndScalarListSchema(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "cutout_demo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	modelSrc := `from dataclasses import dataclass, field
from typing import List, Literal, Optional

@dataclass
class CutoutItem:
    """Single notch definition."""
    leg: Literal["x", "y"] = field(default="x", metadata={"description": "Target leg"})
    length: float = field(default=40.0, metadata={"unit": "mm", "min": 1.0, "max": 200.0, "step": 1.0})
    depth: Optional[float] = field(default=None, metadata={"unit": "mm"})

@dataclass
class CutoutsParams:
    """Cutouts section."""
    enabled: bool = False
    items: List[CutoutItem] = field(
        default_factory=lambda: [CutoutItem(leg="x", length=42.0)],
        metadata={"description": "Configured cutouts", "item_label": "Cutout"},
    )
    offsets: Optional[List[float]] = field(
        default=None,
        metadata={"description": "Optional Z offsets", "item_label": "Offset", "unit": "mm", "step": 0.5},
    )

@dataclass
class CutoutDemoParams:
    cutouts: CutoutsParams = field(default_factory=CutoutsParams)

def export_filename(params: CutoutDemoParams) -> str:
    legs = "_".join(f"{c.leg}{int(c.length)}" for c in params.cutouts.items)
    return f"cutout_demo_{len(params.cutouts.items)}_{legs or 'none'}"

def create_cutout_demo(params: CutoutDemoParams):
    return None
`
	if err := os.WriteFile(filepath.Join(modelDir, "cutout_demo.py"), []byte(modelSrc), 0o644); err != nil {
		t.Fatalf("write cutout_demo.py: %v", err)
	}

	dialogs := &stubDialogs{}
	srv := newTestServer(t, dialogs)
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath("test-lib/cutout_demo"), nil))
	var modelResp struct {
		OK       bool                `json:"ok"`
		Fields   []worker.ParamField `json:"fields"`
		Defaults map[string]any      `json:"defaults"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &modelResp); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if !modelResp.OK || len(modelResp.Fields) != 1 {
		t.Fatalf("unexpected schema response: %+v", modelResp)
	}

	var itemsField, offsetsField worker.ParamField
	for _, c := range modelResp.Fields[0].Children {
		if c.Name == "items" {
			itemsField = c
		}
		if c.Name == "offsets" {
			offsetsField = c
		}
	}

	if itemsField.Type != "list" || itemsField.Item == nil {
		t.Fatalf("expected items field type='list' with item schema, got %+v", itemsField)
	}
	if itemsField.Item.Type != "group" || itemsField.Item.Label != "Cutout" {
		t.Fatalf("expected structured item group labeled 'Cutout', got %+v", itemsField.Item)
	}
	if len(itemsField.Item.Children) != 3 {
		t.Fatalf("expected 3 item children, got %+v", itemsField.Item.Children)
	}
	if itemsField.Item.Children[0].Type != "enum" || len(itemsField.Item.Children[0].Choices) != 2 {
		t.Fatalf("expected leg enum child, got %+v", itemsField.Item.Children[0])
	}
	if itemsField.Item.Children[1].Unit != "mm" || itemsField.Item.Children[2].Nullable != true {
		t.Fatalf("unexpected item child metadata: %+v", itemsField.Item.Children)
	}

	if offsetsField.Type != "list" || !offsetsField.Nullable || offsetsField.Item == nil {
		t.Fatalf("expected nullable scalar list for offsets, got %+v", offsetsField)
	}
	if offsetsField.Item.Type != "float" || offsetsField.Item.Label != "Offset" || offsetsField.Item.Unit != "mm" {
		t.Fatalf("unexpected scalar item schema: %+v", offsetsField.Item)
	}

	// Verify structured list payload coercion (multiple items + empty list)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/cutout_demo", "/export"),
		bytes.NewBufferString(`{"format":"stl","params":{"cutouts":{"enabled":true,"items":[{"leg":"x","length":20},{"leg":"y","length":55,"depth":5}]}}}`),
	))
	if dialogs.lastSave != "cutout_demo_2_x20_y55.stl" {
		t.Fatalf("expected structured list coercion -> 'cutout_demo_2_x20_y55.stl', got %q", dialogs.lastSave)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/cutout_demo", "/export"),
		bytes.NewBufferString(`{"format":"stl","params":{"cutouts":{"enabled":true,"items":[]}}}`),
	))
	if dialogs.lastSave != "cutout_demo_0_none.stl" {
		t.Fatalf("expected empty list coercion -> 'cutout_demo_0_none.stl', got %q", dialogs.lastSave)
	}
}

func TestSetupOnboardingHandlers(t *testing.T) {
	srv := newTestServer(t, nil)

	// Fresh config should request onboarding
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d", rec.Code)
	}
	var cfgResp configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfgResp.NeedsOnboarding {
		t.Fatalf("expected needs_onboarding=true for fresh config")
	}
	if cfgResp.HasDesktop {
		t.Fatalf("expected has_desktop=false when dialogs=nil")
	}

	// GET /api/setup/defaults
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/setup/defaults", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/setup/defaults status = %d", rec.Code)
	}
	var defResp struct {
		DefaultDownloadDir string `json:"default_download_dir"`
		StudioVenvDir      string `json:"studio_venv_dir"`
		DefaultArchiveURL  string `json:"default_archive_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &defResp); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if defResp.DefaultDownloadDir == "" || defResp.StudioVenvDir == "" || defResp.DefaultArchiveURL == "" {
		t.Fatalf("unexpected defaults response: %+v", defResp)
	}

	// POST /api/setup/inspect-dir
	projDir := filepath.Join(t.TempDir(), "sample_proj")
	if err := os.MkdirAll(filepath.Join(projDir, "models", "demo"), 0o755); err != nil {
		t.Fatalf("mkdir demo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "models", "demo", "demo.py"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatalf("write demo.py: %v", err)
	}

	inspectBody, _ := json.Marshal(map[string]any{"path": projDir})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/setup/inspect-dir", bytes.NewReader(inspectBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/setup/inspect-dir status = %d", rec.Code)
	}
	var inspectResp struct {
		OK   bool `json:"ok"`
		Info struct {
			Exists     bool `json:"exists"`
			HasModels  bool `json:"has_models"`
			ModelCount int  `json:"model_count"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &inspectResp); err != nil {
		t.Fatalf("decode inspect: %v", err)
	}
	if !inspectResp.OK || !inspectResp.Info.Exists || !inspectResp.Info.HasModels || inspectResp.Info.ModelCount != 1 {
		t.Fatalf("unexpected inspect response: %+v", inspectResp)
	}

	// POST /api/setup/probe
	probeBody, _ := json.Marshal(map[string]any{"library_roots": []string{projDir}})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/setup/probe", bytes.NewReader(probeBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/setup/probe status = %d", rec.Code)
	}

	// POST /api/setup/complete marks onboarding completed and persists libraries
	completeBody, _ := json.Marshal(map[string]any{
		"libraries": []config.Library{{Name: "Sample Proj", Path: projDir}},
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/setup/complete", bytes.NewReader(completeBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/setup/complete status = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if cfgResp.NeedsOnboarding || !cfgResp.Config.OnboardingCompleted {
		t.Fatalf("expected onboarding_completed=true and needs_onboarding=false after complete, got %+v", cfgResp)
	}
	if len(cfgResp.Config.Libraries) != 1 || cfgResp.Config.Libraries[0].Path != projDir {
		t.Fatalf("expected persisted library %q, got %+v", projDir, cfgResp.Config.Libraries)
	}
	if cfgResp.Config.ExportDir != "" || cfgResp.ResolvedExportDir != filepath.Join(projDir, "exports") {
		t.Fatalf("unexpected export dir config=%q resolved=%q", cfgResp.Config.ExportDir, cfgResp.ResolvedExportDir)
	}
}

func TestPresetVerificationHandlers(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "ver_demo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	modelSrc := `from dataclasses import dataclass, field

@dataclass
class CoreParams:
    width: float = 40.0

@dataclass
class VerDemoParams:
    __default_preset__ = {"label": "Standard Block", "description": "40mm block"}
    core: CoreParams = field(default_factory=CoreParams)

def create_ver_demo(params: VerDemoParams):
    return None
`
	if err := os.WriteFile(filepath.Join(modelDir, "ver_demo.py"), []byte(modelSrc), 0o644); err != nil {
		t.Fatalf("write ver_demo.py: %v", err)
	}

	srv := newTestServer(t, &stubDialogs{})
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	type presetWire struct {
		Key          string `json:"key"`
		Label        string `json:"label"`
		Source       string `json:"source"`
		Verification *struct {
			Status string `json:"status"`
			Note   string `json:"note"`
		} `json:"verification"`
	}

	// 1. Mark canonical default preset verified via PUT .../presets/default/verification
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPut,
		modelPath("test-lib/ver_demo", "/presets/default/verification"),
		bytes.NewBufferString(`{"scope":"default","status":"verified","note":"Printed in PETG, exact fit"}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT default verification status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var verResp struct {
		OK      bool         `json:"ok"`
		Presets []presetWire `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &verResp); err != nil || !verResp.OK || len(verResp.Presets) != 1 {
		t.Fatalf("unexpected default verification response: %+v", verResp)
	}
	if verResp.Presets[0].Verification == nil || verResp.Presets[0].Verification.Status != "verified" || verResp.Presets[0].Verification.Note != "Printed in PETG, exact fit" {
		t.Fatalf("expected verified default preset, got %+v", verResp.Presets[0])
	}

	// 2. Reject invalid verification status with 400
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPut,
		modelPath("test-lib/ver_demo", "/presets/default/verification"),
		bytes.NewBufferString(`{"scope":"default","status":"bogus","note":""}`),
	))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 3. Save a project preset with verification metadata
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/ver_demo", "/presets"),
		bytes.NewBufferString(`{"label":"Wide Block","description":"60mm","scope":"project","params":{"core":{"width":60.0}},"verification":{"status":"usable","note":"Slightly tight on first layer"}}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST preset with verification status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var saveResp struct {
		OK      bool         `json:"ok"`
		Presets []presetWire `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saveResp); err != nil || !saveResp.OK || len(saveResp.Presets) != 2 {
		t.Fatalf("unexpected save preset response: %+v", saveResp)
	}
	if saveResp.Presets[1].Verification == nil || saveResp.Presets[1].Verification.Status != "usable" {
		t.Fatalf("expected usable status on saved project preset, got %+v", saveResp.Presets[1])
	}

	// 4. Update saved project preset verification to unusable
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPut,
		modelPath("test-lib/ver_demo", "/presets/wide_block/verification"),
		bytes.NewBufferString(`{"scope":"project","status":"unusable","note":"Interferes with bracket"}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT project verification status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// 5. Applying update-defaults must downgrade canonical "default" status to unverified while preserving note
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/ver_demo", "/update-defaults"),
		bytes.NewBufferString(`{"dry_run":true,"params":{"core":{"width":45.0}}}`),
	))
	var previewResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &previewResp); err != nil || !previewResp.OK {
		t.Fatalf("preview update-defaults failed: %s", rec.Body.String())
	}

	applyBody, _ := json.Marshal(map[string]any{
		"dry_run":         false,
		"expected_sha256": previewResp.SHA256,
		"params":          map[string]any{"core": map[string]any{"width": 45.0}},
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("test-lib/ver_demo", "/update-defaults"), bytes.NewReader(applyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply update-defaults status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath("test-lib/ver_demo", "/presets"), nil))
	var afterUpdate struct {
		OK      bool         `json:"ok"`
		Presets []presetWire `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &afterUpdate); err != nil || !afterUpdate.OK {
		t.Fatalf("GET presets failed: %s", rec.Body.String())
	}
	if afterUpdate.Presets[0].Verification == nil || afterUpdate.Presets[0].Verification.Status != "unverified" || afterUpdate.Presets[0].Verification.Note != "Printed in PETG, exact fit" {
		t.Fatalf("expected default verification downgraded to unverified with preserved note, got %+v", afterUpdate.Presets[0].Verification)
	}
	if afterUpdate.Presets[1].Verification == nil || afterUpdate.Presets[1].Verification.Status != "unusable" {
		t.Fatalf("expected project preset verification intact, got %+v", afterUpdate.Presets[1].Verification)
	}
}

func TestSavePresetOverwritesHandKeyedPresetAndPreservesDefaultPin(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "lid_demo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	modelSrc := `from dataclasses import dataclass, field

@dataclass
class CoreParams:
    diameter: float = 150.0

@dataclass
class LidDemoParams:
    core: CoreParams = field(default_factory=CoreParams)

def create_lid_demo(params: LidDemoParams):
    return None
`
	if err := os.WriteFile(filepath.Join(modelDir, "lid_demo.py"), []byte(modelSrc), 0o644); err != nil {
		t.Fatalf("write lid_demo.py: %v", err)
	}

	seedPresets := `{
  "schema_version": 1,
  "presets": {
    "anchor_round_6_cup": {
      "label": "Anchor Hocking Round 6-Cup (1.5 qt)",
      "description": "Initial bowl",
      "params": {
        "core": {
          "diameter": 187.3
        }
      }
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(modelDir, "presets.json"), []byte(seedPresets), 0o644); err != nil {
		t.Fatalf("write presets.json: %v", err)
	}

	srv := newTestServer(t, &stubDialogs{})
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	// Pin the hand-keyed preset as model default
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/lid_demo", "/presets/default"),
		bytes.NewBufferString(`{"key":"anchor_round_6_cup"}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST presets/default status = %d", rec.Code)
	}

	// Overwrite by saving with the exact same name
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/lid_demo", "/presets"),
		bytes.NewBufferString(`{"label":"Anchor Hocking Round 6-Cup (1.5 qt)","description":"Tighter fit","scope":"project","params":{"core":{"diameter":186.2}}}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST overwrite preset status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var saveResp struct {
		OK          bool   `json:"ok"`
		SelectedKey string `json:"selected_key"`
		Presets     []struct {
			Key         string         `json:"key"`
			Label       string         `json:"label"`
			Description string         `json:"description"`
			Source      string         `json:"source"`
			Params      map[string]any `json:"params"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("decode saveResp: %v", err)
	}
	if !saveResp.OK || saveResp.SelectedKey != "anchor_round_6_cup" {
		t.Fatalf("expected selected_key='anchor_round_6_cup', got %+v", saveResp)
	}
	if len(saveResp.Presets) != 2 {
		t.Fatalf("expected 2 presets (default + overwritten), got %d: %+v", len(saveResp.Presets), saveResp.Presets)
	}
	if saveResp.Presets[1].Description != "Tighter fit" {
		t.Fatalf("expected updated description, got %+v", saveResp.Presets[1])
	}

	// Saving with a distinct name creates a new preset
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/lid_demo", "/presets"),
		bytes.NewBufferString(`{"label":"Anchor Hocking Round 6-Cup (Loose)","description":"Loose variant","scope":"project","params":{"core":{"diameter":188.0}}}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST new preset status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("decode new saveResp: %v", err)
	}
	if !saveResp.OK || saveResp.SelectedKey != "anchor_hocking_round_6_cup_loose" || len(saveResp.Presets) != 3 {
		t.Fatalf("expected 3 presets with new key, got %+v", saveResp)
	}
}

func TestMultiLibraryNestedFoldersIsolationAndExports(t *testing.T) {
	pyExe := findTestPython(t)
	base := t.TempDir()
	libA := filepath.Join(base, "repos", "code-cad")
	libB := filepath.Join(base, "downloads", "code-cad")
	missingLib := filepath.Join(base, "offline", "unplugged")

	for _, dir := range []string{
		filepath.Join(libA, "components"),
		filepath.Join(libA, "models", "c_clip"),
		filepath.Join(libA, "models", "gridfinity", "bin"),
		filepath.Join(libA, "models", "gridfinity", "spacers"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(libA, "components", "tag.py"), []byte("TAG = 'lib_a'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clipSrc := `from dataclasses import dataclass
from components.tag import TAG

@dataclass
class CClipParams:
    width: float = 10.0

def export_filename(params: CClipParams) -> str:
    return f"c_clip_{TAG}_{int(params.width)}"

def create_c_clip(params: CClipParams):
    return None
`
	if err := os.WriteFile(filepath.Join(libA, "models", "c_clip", "c_clip.py"), []byte(clipSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libA, "root_part.py"), []byte("def create_root_part(): return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libA, "models", "gridfinity", "bin", "bin.py"), []byte("def create_bin(): return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libA, "models", "gridfinity", "spacers", "corner.py"), []byte("def create_corner(): return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{
		filepath.Join(libB, "components"),
		filepath.Join(libB, "models", "c_clip"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(libB, "components", "tag.py"), []byte("TAG = 'lib_b'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libB, "models", "c_clip", "c_clip.py"), []byte(clipSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	dialogs := &stubDialogs{}
	srv := newTestServer(t, dialogs)

	saveBody, _ := json.Marshal(map[string]any{
		"libraries": []config.Library{
			{Path: libA},
			{Path: libB},
			{ID: "offline", Name: "Offline", Path: missingLib},
		},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfgResp configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatal(err)
	}
	if len(cfgResp.Config.Libraries) != 3 {
		t.Fatalf("expected 3 libraries, got %+v", cfgResp.Config.Libraries)
	}
	libAID := cfgResp.Config.Libraries[0].ID
	libBID := cfgResp.Config.Libraries[1].ID
	if libAID == libBID {
		t.Fatalf("expected distinct IDs for same-named folders, got %+v", cfgResp.Config.Libraries)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/models", nil))
	var listResp struct {
		OK            bool               `json:"ok"`
		Models        []worker.ModelInfo `json:"models"`
		LibraryErrors map[string]string  `json:"library_errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if !listResp.OK || len(listResp.Models) != 5 {
		t.Fatalf("expected 5 discovered models across 2 valid libs, got %+v", listResp)
	}
	if listResp.LibraryErrors["offline"] == "" {
		t.Fatalf("expected library_errors entry for missing 'offline' library, got %+v", listResp.LibraryErrors)
	}

	byID := map[string]worker.ModelInfo{}
	for _, m := range listResp.Models {
		byID[m.ID] = m
	}

	rootPart, ok := byID[libAID+"/root_part"]
	if !ok || len(rootPart.Folders) != 0 || rootPart.LocalID != "root_part" {
		t.Fatalf("unexpected root_part metadata: %+v", rootPart)
	}
	binModel, ok := byID[libAID+"/gridfinity/bin"]
	if !ok || len(binModel.Folders) != 1 || binModel.Folders[0] != "gridfinity" || binModel.FolderLabels[0] != "Gridfinity" || binModel.LocalID != "gridfinity/bin" {
		t.Fatalf("unexpected nested canonical bin metadata: %+v", binModel)
	}
	cornerModel, ok := byID[libAID+"/gridfinity/spacers/corner"]
	if !ok || len(cornerModel.Folders) != 2 || cornerModel.LocalID != "gridfinity/spacers/corner" {
		t.Fatalf("unexpected deep corner metadata: %+v", cornerModel)
	}

	// Verify %2F nested model schema routing works cleanly
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath(libAID+"/gridfinity/spacers/corner"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET nested %%2F model status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Verify per-library components/ import isolation AND per-library default export folders
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath(libAID+"/c_clip", "/export"), bytes.NewBufferString(`{"format":"stl","params":{"width":12}}`)))
	if dialogs.lastSave != "c_clip_lib_a_12.stl" || dialogs.lastSaveDir != filepath.Join(libA, "exports") {
		t.Fatalf("lib A export mismatch: file=%q dir=%q", dialogs.lastSave, dialogs.lastSaveDir)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath(libBID+"/c_clip", "/export"), bytes.NewBufferString(`{"format":"stl","params":{"width":14}}`)))
	if dialogs.lastSave != "c_clip_lib_b_14.stl" || dialogs.lastSaveDir != filepath.Join(libB, "exports") {
		t.Fatalf("lib B export mismatch (components isolation failed?): file=%q dir=%q", dialogs.lastSave, dialogs.lastSaveDir)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath(libAID+"/c_clip", "/export"), bytes.NewBufferString(`{"format":"stl","params":{"width":16}}`)))
	if dialogs.lastSave != "c_clip_lib_a_16.stl" || dialogs.lastSaveDir != filepath.Join(libA, "exports") {
		t.Fatalf("lib A second export mismatch: file=%q dir=%q", dialogs.lastSave, dialogs.lastSaveDir)
	}
}

func TestSecurityMiddleware(t *testing.T) {
	srv := newTestServer(t, nil)
	staticNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("static"))
	})

	// 1. Disallowed Host rejected with 403
	for _, badHost := range []string{"evil.com", "evil.com:8080", "192.168.1.50:8080", "wails", ""} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		req.Host = badHost
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for Handler() Host=%q, got %d", badHost, rec.Code)
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("expected security headers even on 403 response, got %v", rec.Header())
		}
	}

	// Allowed hosts
	for _, okHost := range []string{"localhost", "localhost:8080", "127.0.0.1:9000", "[::1]", "[::1]:8080", "wails.localhost"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		req.Host = okHost
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for Host=%q, got %d (%s)", okHost, rec.Code, rec.Body.String())
		}
	}

	// CODE_CAD_ALLOWED_HOSTS support
	t.Setenv("CODE_CAD_ALLOWED_HOSTS", "cad.local, 10.0.0.5")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Host = "cad.local:8443"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for CODE_CAD_ALLOWED_HOSTS host, got %d", rec.Code)
	}

	// Wails internal host & origin permitted ONLY in DesktopMiddleware, rejected in Handler and ServerMiddleware
	mw := srv.DesktopMiddleware(staticNext)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(`{"theme":"light"}`))
	req.Host = "wails"
	req.Header.Set("Origin", "wails://wails")
	req.Header.Set("Content-Type", "application/json")
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected DesktopMiddleware to allow wails host+origin, got %d (%s)", rec.Code, rec.Body.String())
	}

	serverMW := srv.ServerMiddleware(staticNext)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(`{"theme":"light"}`))
	req.Host = "wails"
	req.Header.Set("Origin", "wails://wails")
	req.Header.Set("Content-Type", "application/json")
	serverMW.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected ServerMiddleware to reject Host: wails, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(`{"theme":"light"}`))
	req.Host = "localhost"
	req.Header.Set("Origin", "wails://wails")
	req.Header.Set("Content-Type", "application/json")
	serverMW.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected ServerMiddleware to reject Origin: wails://wails, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Static assets through Middleware receive CSP, nosniff, Referrer-Policy
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "wails.localhost"
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on static asset, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") != cspPolicy {
		t.Fatalf("unexpected CSP header: %q", rec.Header().Get("Content-Security-Policy"))
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("unexpected Referrer-Policy: %q", rec.Header().Get("Referrer-Policy"))
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("did not expect no-store Cache-Control on static asset, got %q", rec.Header().Get("Cache-Control"))
	}

	// /api/* gets Cache-Control: no-store
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/config", nil))
	if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("expected no-store Cache-Control on /api/config, got %q", rec.Header().Get("Cache-Control"))
	}

	// Cross-site Sec-Fetch-Site & Origin checks on mutating methods
	for _, sfs := range []string{"cross-site", "same-site"} {
		rec = httptest.NewRecorder()
		req = newAPIRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(`{"theme":"dark"}`))
		req.Header.Set("Sec-Fetch-Site", sfs)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for Sec-Fetch-Site=%q, got %d", sfs, rec.Code)
		}
	}
	for _, badOrigin := range []string{"null", "https://evil.example", "wails://wails", "file://"} {
		rec = httptest.NewRecorder()
		req = newAPIRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(`{"theme":"dark"}`))
		req.Header.Set("Origin", badOrigin)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for Handler() Origin=%q, got %d", badOrigin, rec.Code)
		}
	}
	// DELETE with cross-site Origin rejected
	rec = httptest.NewRecorder()
	req = newAPIRequest(http.MethodDelete, modelPath("test/m", "/presets/foo"), nil)
	req.Header.Set("Origin", "https://attacker.test")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on cross-site DELETE, got %d", rec.Code)
	}

	// Missing or wrong Content-Type on POST/PUT/PATCH -> 415
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(method, "/api/config/theme", bytes.NewBufferString(`{"theme":"dark"}`))
		req.Host = "localhost"
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("expected 415 on %s without Content-Type, got %d", method, rec.Code)
		}

		rec = httptest.NewRecorder()
		req = httptest.NewRequest(method, "/api/config/theme", bytes.NewBufferString(`{"theme":"dark"}`))
		req.Host = "localhost"
		req.Header.Set("Content-Type", "text/plain")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("expected 415 on %s with text/plain, got %d", method, rec.Code)
		}
	}

	// Oversized JSON body (>2 MiB) -> 413 body_too_large
	huge := `{"theme":"` + strings.Repeat("a", (2<<20)+1024) + `"}`
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(huge)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 on >2MiB JSON body, got %d (%s)", rec.Code, rec.Body.String())
	}
	var errEnv struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil || errEnv.OK || errEnv.Code != "body_too_large" {
		t.Fatalf("unexpected 413 envelope: %s", rec.Body.String())
	}
}

func TestModelIDValidationAndStrictPresetsError(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "bad_presets")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "bad_presets.py"), []byte("def create_bad_presets(): return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	corruptJSON := "{not valid json"
	presetsFile := filepath.Join(modelDir, "presets.json")
	if err := os.WriteFile(presetsFile, []byte(corruptJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, &stubDialogs{})
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "lib", Name: "Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	// Invalid modelIDs rejected with 400 invalid_model_id
	for _, badID := range []string{"single", "lib/..", "../secret", "lib/sub/../x", "lib:part", "lib\\part", "lib/bad\x07id"} {
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath(badID), nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid modelID %q, got %d (%s)", badID, rec.Code, rec.Body.String())
		}
	}

	// Malformed presets.json returns 422 invalid_presets_file and is preserved untouched on disk
	for _, endpoint := range []string{modelPath("lib/bad_presets"), modelPath("lib/bad_presets", "/presets")} {
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, endpoint, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 on %s with malformed presets.json, got %d (%s)", endpoint, rec.Code, rec.Body.String())
		}
	}
	diskBytes, err := os.ReadFile(presetsFile)
	if err != nil || string(diskBytes) != corruptJSON {
		t.Fatalf("expected malformed presets.json to remain untouched, got %q (err=%v)", string(diskBytes), err)
	}
}

func TestSetWindowConcurrencyRace(t *testing.T) {
	srv := newTestServer(t, &stubDialogs{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				if j%2 == 0 {
					srv.SetWindow(&stubWindow{})
				} else {
					srv.SetWindow(nil)
				}
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/window/minimize", bytes.NewBufferString(`{}`)))
				rec = httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/window/toggle-maximize", bytes.NewBufferString(`{}`)))
				rec = httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/window/state", nil))
				rec = httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/window/close", bytes.NewBufferString(`{}`)))
			}
		}(i)
	}
	wg.Wait()
}

func TestConfigPartialUpdateAndCorruptInfo(t *testing.T) {
	setIsolatedTestEnv(t)
	cfgDir, err := config.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	scriptBytes, _ := os.ReadFile(filepath.Join("..", "..", "worker", "worker.py"))
	srv := New(cfgStore, worker.NewManager(scriptBytes), nil)
	t.Cleanup(func() { srv.Shutdown() })

	// Corrupt config surfaces config_error and corrupt_backup_path
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d", rec.Code)
	}
	var cfgResp configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatal(err)
	}
	if cfgResp.ConfigError == "" || cfgResp.CorruptBackupPath == "" {
		t.Fatalf("expected config_error and corrupt_backup_path on corrupt config, got %+v", cfgResp)
	}

	// Partial update (onboarding_skipped + editor_command + slicer_command + export_quality) does not start worker
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewBufferString(`{"onboarding_skipped":true,"editor_command":"code --wait","slicer_command":"prusaslicer","export_quality":"fine"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("partial POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatal(err)
	}
	if !cfgResp.Config.OnboardingSkipped || cfgResp.NeedsOnboarding {
		t.Fatalf("expected onboarding_skipped=true and needs_onboarding=false, got %+v", cfgResp)
	}
	if cfgResp.Config.EditorCommand != "code --wait" || cfgResp.Config.SlicerCommand != "prusaslicer" || cfgResp.Config.ExportQuality != "fine" {
		t.Fatalf("unexpected persisted preferences: %+v", cfgResp.Config)
	}
	if cfgResp.WorkerRunning {
		t.Fatalf("partial preference update should not start worker")
	}

	// Invalid export_quality rejected with 400
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewBufferString(`{"export_quality":"ultra"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid export_quality, got %d", rec.Code)
	}
}

func TestSSEReliableControlEventsKeepaliveAndWorkerCrash(t *testing.T) {
	srv := newTestServer(t, nil)
	srv.sseKeepaliveTimeout = 30 * time.Millisecond

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("connect /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", resp.StatusCode)
	}

	type sseFrame struct {
		id    string
		event string
		data  string
	}
	frames := make(chan sseFrame, 128)
	keepalives := make(chan struct{}, 32)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		var cur sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			if line == ": keepalive" {
				select {
				case keepalives <- struct{}{}:
				default:
				}
				continue
			}
			if line == "" {
				if cur.event != "" {
					frames <- cur
					cur = sseFrame{}
				}
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				cur.id = strings.TrimPrefix(line, "id: ")
			} else if strings.HasPrefix(line, "event: ") {
				cur.event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	// Wait for initial connected frame with monotonic id
	var first sseFrame
	select {
	case first = <-frames:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connected SSE event")
	}
	if first.event != "connected" || first.id == "" {
		t.Fatalf("unexpected initial SSE frame: %+v", first)
	}

	// Verify keepalive arrives
	select {
	case <-keepalives:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE keepalive")
	}

	// Flood 500 setup-log lines, then send critical control events
	for i := 0; i < 500; i++ {
		srv.broadcastSSE("setup-log", map[string]any{"line": i})
	}
	srv.broadcastSSE("setup-progress", map[string]string{"stage": "done"})
	srv.broadcastSSE("file-changed", map[string]string{"path": "/tmp/box.py"})
	srv.broadcastSSE("worker-error", map[string]string{"error": "boom"})

	seen := map[string]int{}
	prevID, _ := strconv.Atoi(first.id)
	deadline := time.After(3 * time.Second)
	for seen["setup-progress"] == 0 || seen["file-changed"] == 0 || seen["worker-error"] == 0 {
		select {
		case f := <-frames:
			idNum, err := strconv.Atoi(f.id)
			if err != nil || idNum <= prevID {
				t.Fatalf("expected strictly increasing SSE id > %d, got %q (frame %+v)", prevID, f.id, f)
			}
			prevID = idNum
			seen[f.event]++
		case <-deadline:
			t.Fatalf("dropped control events under log burst; seen=%v", seen)
		}
	}

	// Verify real unexpected worker exit triggers worker-error SSE event via SetOnUnexpectedExit
	pyExe := findTestPython(t)
	crashWorker := `
import os, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class H(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_GET(self):
        self.send_response(200)
        self.end_headers()

srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
print(f"READY {srv.server_address[1]}", flush=True)
threading.Timer(0.15, lambda: os._exit(1)).start()
srv.serve_forever()
`
	crashMgr := worker.NewManager([]byte(crashWorker))
	crashSrv := New(srv.cfgStore, crashMgr, nil)
	defer crashSrv.Shutdown()

	crashTS := httptest.NewServer(crashSrv.Handler())
	defer crashTS.Close()

	crashReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, crashTS.URL+"/events", nil)
	crashResp, err := crashTS.Client().Do(crashReq)
	if err != nil {
		t.Fatalf("connect crash SSE: %v", err)
	}
	defer crashResp.Body.Close()

	crashFrames := make(chan sseFrame, 16)
	go func() {
		sc := bufio.NewScanner(crashResp.Body)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if cur.event != "" {
					crashFrames <- cur
					cur = sseFrame{}
				}
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				cur.id = strings.TrimPrefix(line, "id: ")
			} else if strings.HasPrefix(line, "event: ") {
				cur.event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	if err := crashMgr.Start(pyExe, nil); err != nil {
		t.Fatalf("start crash worker: %v", err)
	}
	crashDeadline := time.After(3 * time.Second)
	gotWorkerError := false
	for !gotWorkerError {
		select {
		case f := <-crashFrames:
			if f.event == "worker-error" && strings.Contains(f.data, "exited unexpectedly") {
				gotWorkerError = true
			}
		case <-crashDeadline:
			t.Fatal("timed out waiting for worker-error SSE event after unexpected worker exit")
		}
	}
}

func TestCancelRenderAndRenderRawStreaming(t *testing.T) {
	pyExe := findTestPython(t)
	setIsolatedTestEnv(t)
	cfgStore, err := config.NewStore()
	if err != nil {
		t.Fatal(err)
	}

	stubWorker := `
import json, os, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import unquote

TOKEN = os.environ.get("CODE_CAD_WORKER_TOKEN", "")

class H(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def _ok(self):
        return self.headers.get("Authorization") == f"Bearer {TOKEN}"
    def _send(self, obj, code=200):
        b = json.dumps(obj, separators=(",", ":")).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    def do_GET(self):
        if not self._ok(): return self._send({"ok": False}, 401)
        if self.path == "/health": return self._send({"ok": True})
        if self.path == "/models": return self._send({"ok": True, "models": []})
        self._send({"ok": False}, 404)
    def do_POST(self):
        if not self._ok(): return self._send({"ok": False}, 401)
        n = int(self.headers.get("Content-Length", "0") or 0)
        body = json.loads(self.rfile.read(n)) if n else {}
        if self.path == "/configure": return self._send({"ok": True, "models": [], "library_errors": {}})
        if self.path == "/models/lib%2Fslow/render":
            time.sleep(30)
            return self._send({"ok": True})
        if self.path == "/models/lib%2Ffast/render":
            return self._send({"ok": True, "model_id": "lib/fast", "shapes": {"parts": []}, "raw_marker": 42})
        if self.path == "/models/lib%2Fbroken/render":
            return self._send({"ok": False, "error": "SyntaxError: bad", "traceback": "tb line 1"}, 422)
        self._send({"ok": False, "error": "Unknown model: missing"}, 404)

srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
print(f"READY {srv.server_address[1]}", flush=True)
srv.serve_forever()
`
	mgr := worker.NewManager([]byte(stubWorker))
	srv := New(cfgStore, mgr, nil)
	t.Cleanup(func() { srv.Shutdown() })

	libDir := t.TempDir()
	_, err = cfgStore.Update(func(c *config.Config) {
		c.Libraries = []config.Library{{ID: "lib", Name: "Lib", Path: libDir}}
		c.PythonPath = pyExe
	})
	if err != nil {
		t.Fatal(err)
	}

	// RenderRaw streams raw JSON directly including custom fields without re-marshaling
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("lib/fast", "/render"), bytes.NewBufferString(`{"params":{}}`)))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"raw_marker":42`)) {
		t.Fatalf("expected raw streamed JSON, got %d %s", rec.Code, rec.Body.String())
	}

	// Model error returns 422 with standardized error envelope & traceback
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("lib/broken", "/render"), bytes.NewBufferString(`{"params":{}}`)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on model error, got %d (%s)", rec.Code, rec.Body.String())
	}
	var errResp struct {
		OK        bool   `json:"ok"`
		Code      string `json:"code"`
		Error     string `json:"error"`
		Traceback string `json:"traceback"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp.OK || errResp.Code != "model_build_error" || errResp.Traceback != "tb line 1" {
		t.Fatalf("unexpected 422 envelope: %s", rec.Body.String())
	}

	// Start slow render in background and cancel it via POST /api/render/cancel
	slowDone := make(chan int, 1)
	go func() {
		r := httptest.NewRecorder()
		srv.Handler().ServeHTTP(r, newAPIRequest(http.MethodPost, modelPath("lib/slow", "/render"), bytes.NewBufferString(`{"params":{}}`)))
		slowDone <- r.Code
	}()
	time.Sleep(100 * time.Millisecond)

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/render/cancel", bytes.NewBufferString(`{}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/render/cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cancelResp struct {
		OK        bool `json:"ok"`
		Cancelled bool `json:"cancelled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cancelResp); err != nil || !cancelResp.OK || !cancelResp.Cancelled {
		t.Fatalf("unexpected cancel response: %s", rec.Body.String())
	}

	select {
	case <-slowDone:
	case <-time.After(3 * time.Second):
		t.Fatal("slow render did not unblock after /api/render/cancel")
	}

	// Subsequent render succeeds on the respawned worker
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("lib/fast", "/render"), bytes.NewBufferString(`{"params":{}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("render after cancel failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestScaffoldDemoAndNewModel(t *testing.T) {
	pyExe := findTestPython(t)
	srv := newTestServer(t, nil)

	// Configure python_path first so worker uses valid test Python
	initCfg, _ := json.Marshal(map[string]any{"python_path": pyExe})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(initCfg)))
	if rec.Code != http.StatusOK {
		t.Fatalf("init config status = %d", rec.Code)
	}

	// 1. Scaffold demo into fresh empty target dir
	demoDir := filepath.Join(t.TempDir(), "my-demo")
	demoBody, _ := json.Marshal(map[string]any{"mode": "demo", "target_dir": demoDir})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(demoBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/scaffold demo status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var demoResp struct {
		OK        bool   `json:"ok"`
		LibraryID string `json:"library_id"`
		ModelID   string `json:"model_id"`
		PyPath    string `json:"py_path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &demoResp); err != nil || !demoResp.OK {
		t.Fatalf("unexpected demo scaffold resp: %s", rec.Body.String())
	}
	if demoResp.ModelID != demoResp.LibraryID+"/spool_cap" {
		t.Fatalf("expected spool_cap model_id, got %+v", demoResp)
	}
	for _, rel := range []string{
		"components/chamfer_base.py",
		"models/spool_cap/spool_cap.py",
		"models/spool_cap/presets.json",
		"utils/params.py",
	} {
		if _, err := os.Stat(filepath.Join(demoDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing scaffolded demo file %s: %v", rel, err)
		}
	}

	// Non-empty target dir returns 409 target_not_empty
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(demoBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on non-empty demo target_dir, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 2. Scaffold new model validation + creation + conflict
	for _, badName := range []string{"", "../escape", "bad/name", `bad\name`, "bad:name", ".hidden", "_private", "setup", "CON"} {
		body, _ := json.Marshal(map[string]any{"mode": "new", "name": badName, "library_id": demoResp.LibraryID})
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid model name %q, got %d", badName, rec.Code)
		}
	}

	// Non-snake_case and test_* names preserve filename while generating valid Python identifiers
	for _, flexibleName := range []string{"TestParametricTemplate", "test_parametric_model", "cable-clip", "1U Shelf"} {
		body, _ := json.Marshal(map[string]any{
			"mode":       "new",
			"name":       flexibleName,
			"library_id": demoResp.LibraryID,
			"template":   "parametric",
		})
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST scaffold %q status = %d, body = %s", flexibleName, rec.Code, rec.Body.String())
		}
		var flexResp struct {
			OK      bool   `json:"ok"`
			ModelID string `json:"model_id"`
			PyPath  string `json:"py_path"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &flexResp); err != nil || !flexResp.OK || flexResp.ModelID != demoResp.LibraryID+"/"+flexibleName {
			t.Fatalf("unexpected flexible scaffold resp for %q: %s", flexibleName, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath(flexResp.ModelID), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET newly scaffolded model %q status = %d, body = %s", flexibleName, rec.Code, rec.Body.String())
		}
	}

	newBody, _ := json.Marshal(map[string]any{
		"mode":       "new",
		"name":       "cable_clip",
		"library_id": demoResp.LibraryID,
		"template":   "parametric",
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(newBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST scaffold new status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var newResp struct {
		OK      bool   `json:"ok"`
		ModelID string `json:"model_id"`
		PyPath  string `json:"py_path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &newResp); err != nil || !newResp.OK || newResp.ModelID != demoResp.LibraryID+"/cable_clip" {
		t.Fatalf("unexpected new model scaffold resp: %s", rec.Body.String())
	}
	if _, err := os.Stat(newResp.PyPath); err != nil {
		t.Fatalf("expected new model file at %s: %v", newResp.PyPath, err)
	}

	// Duplicate new model returns 409 model_exists
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(newBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate new model, got %d (%s)", rec.Code, rec.Body.String())
	}

	// New model is discovered and its schema loads cleanly
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, modelPath(newResp.ModelID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET newly scaffolded model status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestExportServerDownloadAndQualityValidation(t *testing.T) {
	pyExe := findTestPython(t)
	setIsolatedTestEnv(t)
	cfgStore, err := config.NewStore()
	if err != nil {
		t.Fatal(err)
	}

	stubWorker := `
import json, os, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOKEN = os.environ.get("CODE_CAD_WORKER_TOKEN", "")

class H(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def _ok(self):
        return self.headers.get("Authorization") == f"Bearer {TOKEN}"
    def _send(self, obj, code=200):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    def do_POST(self):
        if not self._ok(): return self._send({"ok": False}, 401)
        n = int(self.headers.get("Content-Length", "0") or 0)
        body = json.loads(self.rfile.read(n)) if n else {}
        if self.path == "/configure": return self._send({"ok": True, "models": [], "library_errors": {}})
        if self.path.endswith("/export-name"):
            return self._send({"ok": True, "export_filename": "bracket_part", "library_id": "lib"})
        if self.path.endswith("/export"):
            payload = ("MESH:" + str(body.get("quality")) + ":" + str(body.get("format"))).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return
        self._send({"ok": False}, 404)

srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
print(f"READY {srv.server_address[1]}", flush=True)
srv.serve_forever()
`
	mgr := worker.NewManager([]byte(stubWorker))
	srv := New(cfgStore, mgr, nil) // nil dialogs simulates -tags server mode
	t.Cleanup(func() { srv.Shutdown() })

	libDir := t.TempDir()
	_, _ = cfgStore.Update(func(c *config.Config) {
		c.Libraries = []config.Library{{ID: "lib", Name: "Lib", Path: libDir}}
		c.PythonPath = pyExe
	})

	// Server mode download streams attachment with sanitized Content-Disposition
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("lib/bracket", "/export"),
		bytes.NewBufferString(`{"format":"stl","quality":"fine","filename":"../../evil\"; hack.stl","params":{}}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 browser download, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("unexpected Content-Type: %q", rec.Header().Get("Content-Type"))
	}
	disp := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disp, "attachment;") || strings.Contains(disp, "..") || strings.Contains(disp, "/") {
		t.Fatalf("unsafe Content-Disposition header: %q", disp)
	}
	if rec.Body.String() != "MESH:fine:stl" {
		t.Fatalf("unexpected exported bytes: %q", rec.Body.String())
	}

	// Validation rejections (400)
	badPayloads := []string{
		`{"format":"obj","params":{}}`,
		`{"format":"stl","quality":"ultra","params":{}}`,
		`{"format":"stl","quality":"custom","params":{}}`,
		`{"format":"stl","quality":"custom","linear_deflection":-0.1,"angular_deflection":0.2,"params":{}}`,
		`{"format":"stl","quality":"custom","linear_deflection":0.1,"angular_deflection":9.9,"params":{}}`,
	}
	for _, body := range badPayloads {
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, modelPath("lib/bracket", "/export"), bytes.NewBufferString(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for export payload %s, got %d (%s)", body, rec.Code, rec.Body.String())
		}
	}
}

func TestDesktopActionsContainmentAndServerMode(t *testing.T) {
	base := t.TempDir()
	libDir := filepath.Join(base, "lib")
	exportDir := filepath.Join(base, "exports")
	outsideDir := filepath.Join(base, "outside")
	for _, d := range []string{libDir, exportDir, outsideDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	modelPy := filepath.Join(libDir, "box.py")
	exportStl := filepath.Join(exportDir, "box.stl")
	secretFile := filepath.Join(outsideDir, "secret.py")
	for _, f := range []string{modelPy, exportStl, secretFile} {
		if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symlinkEscape := filepath.Join(libDir, "escape.py")
	if err := os.Symlink(secretFile, symlinkEscape); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	// 1. Server mode (dialogs == nil) returns 400 desktop_unavailable
	srvNoDesktop := newTestServer(t, nil)
	for _, ep := range []string{"/api/desktop/open-editor", "/api/desktop/reveal-file", "/api/desktop/open-slicer"} {
		body, _ := json.Marshal(map[string]string{"path": modelPy})
		rec := httptest.NewRecorder()
		srvNoDesktop.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, ep, bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 in server mode for %s, got %d (%s)", ep, rec.Code, rec.Body.String())
		}
	}

	// 2. Desktop mode enforces containment and executes tokenized commands without shell
	srv := newTestServer(t, &stubDialogs{})
	var ranCmd string
	var ranArgs []string
	srv.desktopRunner = func(name string, args ...string) error {
		ranCmd = name
		ranArgs = append([]string(nil), args...)
		return nil
	}

	_, err := srv.cfgStore.Update(func(c *config.Config) {
		c.Libraries = []config.Library{{ID: "lib", Name: "Lib", Path: libDir}}
		c.ExportDir = exportDir
		c.EditorCommand = `code --wait "{file}"`
		c.SlicerCommand = `prusa-slicer`
	})
	if err != nil {
		t.Fatal(err)
	}

	// Outside file -> 403 forbidden_path
	for _, forbidden := range []string{secretFile, symlinkEscape} {
		body, _ := json.Marshal(map[string]string{"path": forbidden})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/desktop/open-editor", bytes.NewReader(body)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for path %q, got %d (%s)", forbidden, rec.Code, rec.Body.String())
		}
	}

	// open-editor must reject exportDir files (only library roots allowed)
	expBody, _ := json.Marshal(map[string]string{"path": exportStl})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/desktop/open-editor", bytes.NewReader(expBody)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for exportStl in open-editor, got %d", rec.Code)
	}

	// open-slicer rejects .py files -> 400 invalid_slicer_file
	pyBody, _ := json.Marshal(map[string]string{"path": modelPy})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/desktop/open-slicer", bytes.NewReader(pyBody)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for .py in open-slicer, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Valid open-editor runs tokenized command
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/desktop/open-editor", bytes.NewReader(pyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("open-editor status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resolvedPy, _ := filepath.EvalSymlinks(modelPy)
	if ranCmd != "code" || len(ranArgs) != 2 || ranArgs[0] != "--wait" || ranArgs[1] != resolvedPy {
		t.Fatalf("unexpected editor command: cmd=%q args=%v", ranCmd, ranArgs)
	}

	// Valid open-slicer on exportStl
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/desktop/open-slicer", bytes.NewReader(expBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("open-slicer status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resolvedStl, _ := filepath.EvalSymlinks(exportStl)
	if ranCmd != "prusa-slicer" || len(ranArgs) != 1 || ranArgs[0] != resolvedStl {
		t.Fatalf("unexpected slicer command: cmd=%q args=%v", ranCmd, ranArgs)
	}

	// Valid reveal-file on exportStl
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/desktop/reveal-file", bytes.NewReader(expBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal-file status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSaveConfigAndSetupCompleteBroadcastWorkerErrorSSE(t *testing.T) {
	srv := newTestServer(t, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("connect /events: %v", err)
	}
	defer resp.Body.Close()

	type sseFrame struct {
		event string
		data  string
	}
	frames := make(chan sseFrame, 32)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if cur.event != "" {
					frames <- cur
					cur = sseFrame{}
				}
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				cur.event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	select {
	case f := <-frames:
		if f.event != "connected" {
			t.Fatalf("expected connected frame first, got %+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connected SSE frame")
	}

	libDir := t.TempDir()
	badPython := filepath.Join(t.TempDir(), "does-not-exist-python")

	// 1. handleSaveConfig broadcasts worker-error SSE when worker startup fails
	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "lib", Name: "Lib", Path: libDir}},
		"python_path": badPython,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}

	waitWorkerErr := func(label string) {
		deadline := time.After(2 * time.Second)
		for {
			select {
			case f := <-frames:
				if f.event == "worker-error" {
					var payload map[string]string
					if err := json.Unmarshal([]byte(f.data), &payload); err != nil || payload["error"] == "" {
						t.Fatalf("%s: invalid worker-error SSE payload: %q", label, f.data)
					}
					return
				}
			case <-deadline:
				t.Fatalf("%s: timed out waiting for worker-error SSE event", label)
			}
		}
	}
	waitWorkerErr("handleSaveConfig")

	// 2. handleSetupComplete broadcasts worker-error SSE when worker startup fails
	completeBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "lib", Name: "Lib", Path: libDir}},
		"python_path": badPython + "-2",
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/setup/complete", bytes.NewReader(completeBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/setup/complete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	waitWorkerErr("handleSetupComplete")
}

func TestScaffoldRejectsSymlinks(t *testing.T) {
	srv := newTestServer(t, nil)
	base := t.TempDir()

	// 1. mode: "demo" rejects symlinked empty directory
	outsideEmpty := filepath.Join(base, "outside-empty")
	if err := os.MkdirAll(outsideEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	symDemo := filepath.Join(base, "sym-demo")
	if err := os.Symlink(outsideEmpty, symDemo); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	demoBody, _ := json.Marshal(map[string]any{"mode": "demo", "target_dir": symDemo})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(demoBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for symlinked demo target_dir, got %d (%s)", rec.Code, rec.Body.String())
	}
	if entries, err := os.ReadDir(outsideEmpty); err != nil || len(entries) != 0 {
		t.Fatalf("expected outside directory to remain untouched, entries=%v err=%v", entries, err)
	}

	// 2. mode: "demo" rejects non-existent subdir under a symlinked parent
	nestedUnderSym := filepath.Join(symDemo, "nested_demo")
	nestedBody, _ := json.Marshal(map[string]any{"mode": "demo", "target_dir": nestedUnderSym})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(nestedBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for demo target under symlinked parent, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 3. mode: "new" rejects symlinked models/ folder inside configured library
	libDir := filepath.Join(base, "lib")
	outsideModels := filepath.Join(base, "outside-models")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideModels, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideModels, filepath.Join(libDir, "models")); err != nil {
		t.Fatal(err)
	}
	_, err := srv.cfgStore.Update(func(c *config.Config) {
		c.Libraries = []config.Library{{ID: "lib", Name: "Lib", Path: libDir}}
	})
	if err != nil {
		t.Fatal(err)
	}

	newBody, _ := json.Marshal(map[string]any{
		"mode":       "new",
		"library_id": "lib",
		"name":       "my_part",
		"template":   "parametric",
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(newBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for symlinked models/ in mode=new, got %d (%s)", rec.Code, rec.Body.String())
	}
	if entries, err := os.ReadDir(outsideModels); err != nil || len(entries) != 0 {
		t.Fatalf("expected outsideModels to remain empty, entries=%v err=%v", entries, err)
	}
}

func TestSanitizeExportStem(t *testing.T) {
	// Windows reserved device names (case-insensitive, with or without extension/sub-extension)
	for _, reserved := range []string{"CON", "con", "CON.stl", "prn", "AUX.step", "nul.3mf", "COM1", "com9", "LPT1", "lpt9.stl", "CON.part"} {
		got := sanitizeExportStem(reserved, "model")
		if !strings.HasPrefix(got, "export_") {
			t.Errorf("sanitizeExportStem(%q) = %q, expected 'export_' prefix", reserved, got)
		}
	}

	// Non-reserved names remain unprefixed
	for in, want := range map[string]string{
		"CON0":        "CON0",
		"COM10":       "COM10",
		"config":      "config",
		"bracket.stl": "bracket",
		"../../a/b":   "b",
		"...":         "model",
	} {
		if got := sanitizeExportStem(in, "model"); got != want {
			t.Errorf("sanitizeExportStem(%q) = %q, want %q", in, got, want)
		}
	}

	// Length clamped to <= 120 chars
	long := strings.Repeat("a", 400)
	gotLong := sanitizeExportStem(long, "model")
	if len(gotLong) != 120 {
		t.Fatalf("expected length 120, got %d (%q)", len(gotLong), gotLong)
	}

	longReserved := "CON." + strings.Repeat("x", 150)
	gotLongReserved := sanitizeExportStem(longReserved, "model")
	if !strings.HasPrefix(gotLongReserved, "export_CON.") || len(gotLongReserved) > 120 {
		t.Fatalf("expected prefixed and <=120 chars for %q, got len=%d (%q)", longReserved, len(gotLongReserved), gotLongReserved)
	}
}

func TestServerShutdownWithActiveWatcherEdits(t *testing.T) {
	pyExe := findTestPython(t)
	srv := newTestServer(t, nil)

	libDir := t.TempDir()
	pyFile := filepath.Join(libDir, "box.py")
	if err := os.WriteFile(pyFile, []byte("def create_box(): return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "lib", Name: "Lib", Path: libDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Trigger watcher edits right before and concurrently with Shutdown()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(pyFile, []byte("x = "+strconv.Itoa(i)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}

	// Wait just past one debounce tick so handlePyFileChanged may be in-flight during Shutdown()
	time.Sleep(240 * time.Millisecond)
	srv.Shutdown()

	// Wait past another debounce window to verify no callback respawns the worker after Shutdown()
	time.Sleep(350 * time.Millisecond)
	running, _ := srv.worker.Status()
	if running {
		t.Fatal("expected worker to remain stopped after Server.Shutdown() despite active watcher edits")
	}
}

func TestDialogCancelErrorsReturnOKCancelled(t *testing.T) {
	pyExe := findTestPython(t)
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "cancel_box")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	modelSrc := `from dataclasses import dataclass

@dataclass
class CancelBoxParams:
    width: float = 10.0

def create_cancel_box(params: CancelBoxParams):
    return None
`
	if err := os.WriteFile(filepath.Join(modelDir, "cancel_box.py"), []byte(modelSrc), 0o644); err != nil {
		t.Fatalf("write cancel_box.py: %v", err)
	}

	cancelErr := errors.New("cancelled by user")
	dialogs := &stubDialogs{
		dirErr:  cancelErr,
		fileErr: cancelErr,
		saveErr: cancelErr,
	}
	srv := newTestServer(t, dialogs)

	saveBody, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "test-lib", Name: "Test Lib", Path: projectDir}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// 1. Project, python, and export-dir dialogs return HTTP 200 {path:"", cancelled:true} on cancel errors
	for _, endpoint := range []string{"/api/dialogs/project", "/api/dialogs/python", "/api/dialogs/export-dir"} {
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, endpoint, bytes.NewBufferString(`{}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s cancel status = %d, want 200 (body=%s)", endpoint, rec.Code, rec.Body.String())
		}
		var resp struct {
			Path      string `json:"path"`
			Cancelled bool   `json:"cancelled"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s cancel resp: %v", endpoint, err)
		}
		if resp.Path != "" || !resp.Cancelled {
			t.Fatalf("%s cancel resp = %+v, want {Path:\"\", Cancelled:true}", endpoint, resp)
		}
	}

	// 2. Export SaveFile dialog returns HTTP 200 {ok:true, cancelled:true} on cancel error
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/cancel_box", "/export"),
		bytes.NewBufferString(`{"format":"stl","params":{}}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("export SaveFile cancel status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var exportResp struct {
		OK        bool `json:"ok"`
		Cancelled bool `json:"cancelled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &exportResp); err != nil {
		t.Fatalf("decode export cancel resp: %v", err)
	}
	if !exportResp.OK || !exportResp.Cancelled {
		t.Fatalf("export cancel resp = %+v, want {OK:true, Cancelled:true}", exportResp)
	}

	// 3. Real dialog failures still propagate as HTTP 500 dialog_error
	realErr := errors.New("COM initialization failed")
	dialogs.dirErr = realErr
	dialogs.fileErr = realErr
	dialogs.saveErr = realErr

	for _, endpoint := range []string{"/api/dialogs/project", "/api/dialogs/python", "/api/dialogs/export-dir"} {
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, endpoint, bytes.NewBufferString(`{}`)))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s real error status = %d, want 500 (body=%s)", endpoint, rec.Code, rec.Body.String())
		}
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(
		http.MethodPost,
		modelPath("test-lib/cancel_box", "/export"),
		bytes.NewBufferString(`{"format":"stl","params":{}}`),
	))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("export SaveFile real error status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestDefaultDemoTargetDirAndDemoScaffoldAppendsExistingLibraries(t *testing.T) {
	pyExe := findTestPython(t)
	srv := newTestServer(t, nil)

	// Re-point home for deterministic Documents/code-cad-studio/demo assertions
	home := setIsolatedTestEnv(t)

	// Without Documents -> <home>/code-cad-studio/demo
	gotNoDocs, err := defaultDemoTargetDir()
	if err != nil {
		t.Fatalf("defaultDemoTargetDir (no Documents): %v", err)
	}
	wantNoDocs := filepath.Join(home, "code-cad-studio", "demo")
	if gotNoDocs != wantNoDocs {
		t.Fatalf("defaultDemoTargetDir (no Documents) = %q, want %q", gotNoDocs, wantNoDocs)
	}

	// With Documents -> <home>/Documents/code-cad-studio/demo
	docsDir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir Documents: %v", err)
	}
	wantDemo1 := filepath.Join(docsDir, "code-cad-studio", "demo")
	got1, err := defaultDemoTargetDir()
	if err != nil {
		t.Fatalf("defaultDemoTargetDir (with Documents): %v", err)
	}
	if got1 != wantDemo1 {
		t.Fatalf("defaultDemoTargetDir = %q, want %q", got1, wantDemo1)
	}

	// Seed an existing library first, then scaffold demo with default target_dir
	existingLib := filepath.Join(t.TempDir(), "existing-lib")
	if err := os.MkdirAll(existingLib, 0o755); err != nil {
		t.Fatalf("mkdir existingLib: %v", err)
	}
	initCfg, _ := json.Marshal(map[string]any{
		"libraries":   []config.Library{{ID: "existing", Name: "Existing", Path: existingLib}},
		"python_path": pyExe,
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/config", bytes.NewReader(initCfg)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewBufferString(`{"mode":"demo"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/scaffold demo status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var demoResp struct {
		OK        bool           `json:"ok"`
		TargetDir string         `json:"target_dir"`
		LibraryID string         `json:"library_id"`
		ModelID   string         `json:"model_id"`
		Config    configResponse `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &demoResp); err != nil || !demoResp.OK {
		t.Fatalf("decode demo scaffold resp: err=%v body=%s", err, rec.Body.String())
	}
	if !config.SamePath(demoResp.TargetDir, wantDemo1) {
		t.Fatalf("demo target_dir = %q, want %q", demoResp.TargetDir, wantDemo1)
	}
	if len(demoResp.Config.Config.Libraries) != 2 {
		t.Fatalf("expected existing library preserved and demo appended (2 total), got %+v", demoResp.Config.Config.Libraries)
	}
	if !config.SamePath(demoResp.Config.Config.Libraries[0].Path, existingLib) {
		t.Fatalf("expected existing library first, got %+v", demoResp.Config.Config.Libraries)
	}
	if !config.SamePath(demoResp.Config.Config.Libraries[1].Path, wantDemo1) {
		t.Fatalf("expected demo library second, got %+v", demoResp.Config.Config.Libraries)
	}

	// Now that demo is non-empty, defaultDemoTargetDir increments to demo-2, then demo-3
	wantDemo2 := filepath.Join(docsDir, "code-cad-studio", "demo-2")
	got2, err := defaultDemoTargetDir()
	if err != nil {
		t.Fatalf("defaultDemoTargetDir (second): %v", err)
	}
	if got2 != wantDemo2 {
		t.Fatalf("defaultDemoTargetDir (second) = %q, want %q", got2, wantDemo2)
	}

	if err := os.MkdirAll(wantDemo2, 0o755); err != nil {
		t.Fatalf("mkdir demo-2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wantDemo2, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write demo-2 marker: %v", err)
	}
	wantDemo3 := filepath.Join(docsDir, "code-cad-studio", "demo-3")
	got3, err := defaultDemoTargetDir()
	if err != nil {
		t.Fatalf("defaultDemoTargetDir (third): %v", err)
	}
	if got3 != wantDemo3 {
		t.Fatalf("defaultDemoTargetDir (third) = %q, want %q", got3, wantDemo3)
	}

	// Scaffolding demo into an already-configured empty library reuses its entry via config.SamePath
	emptyPreConfigured := filepath.Join(t.TempDir(), "PreConfiguredDemo")
	if err := os.MkdirAll(emptyPreConfigured, 0o755); err != nil {
		t.Fatalf("mkdir emptyPreConfigured: %v", err)
	}
	_, err = srv.cfgStore.Update(func(c *config.Config) {
		c.Libraries = append(c.Libraries, config.Library{
			ID:   "preconf",
			Name: "PreConfigured",
			Path: emptyPreConfigured,
		})
	})
	if err != nil {
		t.Fatalf("Update config: %v", err)
	}
	targetForRequest := emptyPreConfigured
	if runtime.GOOS == "windows" {
		targetForRequest = strings.ToLower(emptyPreConfigured)
	}
	reuseBody, _ := json.Marshal(map[string]any{"mode": "demo", "target_dir": targetForRequest})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/models/scaffold", bytes.NewReader(reuseBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/scaffold reuse status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var reuseResp struct {
		OK        bool           `json:"ok"`
		LibraryID string         `json:"library_id"`
		Config    configResponse `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reuseResp); err != nil || !reuseResp.OK {
		t.Fatalf("decode reuse resp: err=%v body=%s", err, rec.Body.String())
	}
	if reuseResp.LibraryID != "preconf" {
		t.Fatalf("expected reused library_id 'preconf', got %q", reuseResp.LibraryID)
	}
	if len(reuseResp.Config.Config.Libraries) != 3 {
		t.Fatalf("expected no duplicate library entry (3 total), got %+v", reuseResp.Config.Config.Libraries)
	}
}
