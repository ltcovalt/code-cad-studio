package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

type stubDialogs struct {
	dirPath     string
	filePath    string
	savePath    string
	lastSaveDir string
	lastSave    string
}

func (d *stubDialogs) ChooseDirectory(title, startDir string) (string, error) {
	return d.dirPath, nil
}

func (d *stubDialogs) ChooseFile(title, startDir string) (string, error) {
	return d.filePath, nil
}

func (d *stubDialogs) SaveFile(title, defaultDir, defaultFilename, filterName, filterPattern string) (string, error) {
	d.lastSaveDir = defaultDir
	d.lastSave = defaultFilename
	return d.savePath, nil
}

func newTestServer(t *testing.T, dialogs DesktopDialogs) *Server {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfgStore, err := config.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	scriptBytes, err := os.ReadFile(filepath.Join("..", "..", "worker", "worker.py"))
	if err != nil {
		t.Fatalf("read worker.py: %v", err)
	}
	mgr := worker.NewManager(scriptBytes)
	srv := New(cfgStore, mgr, dialogs)
	t.Cleanup(func() { srv.Shutdown() })
	return srv
}

func TestConfigAndThemeHandlers(t *testing.T) {
	dialogs := &stubDialogs{dirPath: "/tmp/example-project", filePath: "/usr/bin/python3"}
	srv := newTestServer(t, dialogs)

	// GET /api/config
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
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
	if cfgResp.Config.Theme != "dark" {
		t.Fatalf("expected default theme 'dark', got %q", cfgResp.Config.Theme)
	}

	// POST /api/config/theme
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config/theme", bytes.NewBufferString(`{"theme":"dracula"}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /api/config/theme status = %d", rec.Code)
	}

	// Verify persisted theme
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode updated config: %v", err)
	}
	if cfgResp.Config.Theme != "dracula" {
		t.Fatalf("expected persisted theme 'dracula', got %q", cfgResp.Config.Theme)
	}

	// POST /api/dialogs/project
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/dialogs/project", bytes.NewBufferString(`{}`))
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
	req = httptest.NewRequest(http.MethodPost, "/api/dialogs/export-dir", bytes.NewBufferString(`{}`))
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
	saveBody, _ := json.Marshal(map[string]string{
		"project_root": projectDir,
		"python_path":  "python3",
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// GET /api/models
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	var listResp struct {
		OK     bool               `json:"ok"`
		Models []worker.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if !listResp.OK || len(listResp.Models) != 1 || listResp.Models[0].ID != "demo_box" {
		t.Fatalf("unexpected models list: %+v", listResp)
	}

	// GET /api/models/demo_box
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/demo_box", nil))
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

	// Verify export_filename & default export dir (<project_root>/exports) via SaveFile
	rec = httptest.NewRecorder()
	exportBody := bytes.NewBufferString(`{"format":"stl","params":{"core":{"mode":"rect"}}}`)
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/demo_box/export", exportBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/demo_box/export status = %d", rec.Code)
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
	saveBody, _ = json.Marshal(map[string]string{
		"project_root": projectDir,
		"python_path":  "python3",
		"export_dir":   customExportDir,
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/demo_box/export", exportBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/demo_box/export (custom dir) status = %d", rec.Code)
	}
	if dialogs.lastSaveDir != customExportDir {
		t.Fatalf("expected SaveFile custom defaultDir %q, got %q", customExportDir, dialogs.lastSaveDir)
	}
	if dialogs.lastSave != "demo_box_round.step" {
		t.Fatalf("expected SaveFile filename 'demo_box_round.step', got %q", dialogs.lastSave)
	}
}

func TestWorkerListCoercionAndEmptyStringHandling(t *testing.T) {
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
	saveBody, _ := json.Marshal(map[string]string{
		"project_root": projectDir,
		"python_path":  "python3",
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	// Empty string must coerce to None (not empty string)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/list_demo/export", bytes.NewBufferString(`{"format":"stl","params":{"slots":{"z_positions":""}}}`)))
	if dialogs.lastSave != "list_demo_auto.stl" {
		t.Fatalf("expected empty string to coerce to None -> 'list_demo_auto.stl', got %q", dialogs.lastSave)
	}

	// Comma-separated string must coerce to List[float]
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/list_demo/export", bytes.NewBufferString(`{"format":"stl","params":{"slots":{"z_positions":"10, 25.0, 40"}}}`)))
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/list_demo/export", bytes.NewBufferString(`{"format":"stl","params":{}}`)))
	if dialogs.lastSave != "list_demo_reloaded.stl" {
		t.Fatalf("expected warm reload to update module -> 'list_demo_reloaded.stl', got %q", dialogs.lastSave)
	}
}

func TestPresetsPinDeleteAndSurgicalUpdateDefaults(t *testing.T) {
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
	saveBody, _ := json.Marshal(map[string]string{
		"project_root": projectDir,
		"python_path":  "python3",
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	// 1. Save a local preset, pin as startup default, promote to project, then delete
	rec = httptest.NewRecorder()
	savePresetReq := `{"label":"Thin Clip","description":"6mm","scope":"user","full_snapshot":false,"params":{"core":{"thickness":6.0,"angle":95.0}}}`
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/clip_demo/presets", bytes.NewBufferString(savePresetReq)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST presets status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/clip_demo/presets/default", bytes.NewBufferString(`{"key":"thin_clip"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST presets/default status = %d", rec.Code)
	}

	// GET /api/models/clip_demo should return pinned default_preset_key
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/clip_demo", nil))
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/clip_demo/presets", bytes.NewBufferString(saveProjReq)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST project preset status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Rename pinned project preset and verify startup default pin follows the new key
	rec = httptest.NewRecorder()
	renameReq := `{"scope":"project","label":"Slim Clip","description":"6mm renamed"}`
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/models/clip_demo/presets/thin_clip", bytes.NewBufferString(renameReq)))
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/models/clip_demo/presets/slim_clip?scope=project", nil))
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

	// 2. Fail-closed test: attempting to update computed_gap (non-literal expression) must report unsupported and not write file
	rec = httptest.NewRecorder()
	badApply := `{"dry_run":false,"params":{"core":{"thickness":8.0,"computed_gap":5.0}}}`
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/clip_demo/update-defaults", bytes.NewBufferString(badApply)))
	var badResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &badResp); err != nil {
		t.Fatalf("decode badResp: %v", err)
	}
	if badResp.OK || len(badResp.Unsupported) != 1 || badResp.Unsupported[0].Path != "core.computed_gap" {
		t.Fatalf("expected fail-closed unsupported on core.computed_gap, got %+v", badResp)
	}
	afterBadBytes, _ := os.ReadFile(modelFile)
	if string(afterBadBytes) != modelSrc {
		t.Fatalf("file must remain untouched after failed update")
	}

	// 3. Dry-run preview on safe literal changes
	rec = httptest.NewRecorder()
	previewReq := `{"dry_run":true,"params":{"core":{"thickness":10.5,"angle":110.0,"label_text":"prusa","enabled":false,"override":4.2}}}`
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/clip_demo/update-defaults", bytes.NewBufferString(previewReq)))
	var previewResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &previewResp); err != nil {
		t.Fatalf("decode previewResp: %v", err)
	}
	if !previewResp.OK || len(previewResp.Changes) != 5 || len(previewResp.Unsupported) != 0 || previewResp.SHA256 == "" {
		t.Fatalf("unexpected preview response: %+v", previewResp)
	}

	// 4. Stale SHA-256 must abort without modifying file
	rec = httptest.NewRecorder()
	staleReq := `{"dry_run":false,"expected_sha256":"deadbeef","params":{"core":{"thickness":10.5}}}`
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/clip_demo/update-defaults", bytes.NewBufferString(staleReq)))
	var staleResp worker.UpdateDefaultsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &staleResp); err != nil || staleResp.OK {
		t.Fatalf("expected stale sha256 to fail, got %+v", staleResp)
	}

	// 5. Apply with matching SHA-256 and verify surgical edits + preserved comments + reloaded defaults
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/clip_demo/update-defaults", bytes.NewReader(applyPayload)))
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

	// Subsequent GET /api/models/clip_demo must reflect new canonical defaults immediately
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/clip_demo", nil))
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
	saveBody, _ := json.Marshal(map[string]string{
		"project_root": projectDir,
		"python_path":  "python3",
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/config status = %d", rec.Code)
	}

	// GET /api/models/cutout_demo -> verify list schema introspection
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/cutout_demo", nil))
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(
		http.MethodPost,
		"/api/models/cutout_demo/export",
		bytes.NewBufferString(`{"format":"stl","params":{"cutouts":{"enabled":true,"items":[{"leg":"x","length":20},{"leg":"y","length":55,"depth":5}]}}}`),
	))
	if dialogs.lastSave != "cutout_demo_2_x20_y55.stl" {
		t.Fatalf("expected structured list coercion -> 'cutout_demo_2_x20_y55.stl', got %q", dialogs.lastSave)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(
		http.MethodPost,
		"/api/models/cutout_demo/export",
		bytes.NewBufferString(`{"format":"stl","params":{"cutouts":{"enabled":true,"items":[]}}}`),
	))
	if dialogs.lastSave != "cutout_demo_0_none.stl" {
		t.Fatalf("expected empty list coercion -> 'cutout_demo_0_none.stl', got %q", dialogs.lastSave)
	}
}

func TestSetupOnboardingHandlers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := newTestServer(t, nil)

	// Fresh config should request onboarding
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
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

	// GET /api/setup/defaults
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/setup/defaults", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/setup/defaults status = %d", rec.Code)
	}
	var defResp struct {
		SuggestedProjectDir string `json:"suggested_project_dir"`
		SuggestedExportDir  string `json:"suggested_export_dir"`
		DefaultArchiveURL   string `json:"default_archive_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &defResp); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if defResp.SuggestedProjectDir == "" || defResp.SuggestedExportDir == "" || defResp.DefaultArchiveURL == "" {
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup/inspect-dir", bytes.NewReader(inspectBody)))
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
	probeBody, _ := json.Marshal(map[string]any{"project_root": projDir})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup/probe", bytes.NewReader(probeBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/setup/probe status = %d", rec.Code)
	}

	// POST /api/setup/complete marks onboarding completed and persists project_root
	completeBody, _ := json.Marshal(map[string]any{
		"project_root": projDir,
		"export_dir":   filepath.Join(projDir, "exports"),
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup/complete", bytes.NewReader(completeBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/setup/complete status = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if cfgResp.NeedsOnboarding || !cfgResp.Config.OnboardingCompleted {
		t.Fatalf("expected onboarding_completed=true and needs_onboarding=false after complete, got %+v", cfgResp)
	}
	if cfgResp.Config.ProjectRoot != projDir {
		t.Fatalf("expected project_root=%q, got %q", projDir, cfgResp.Config.ProjectRoot)
	}
	// Default <project_root>/exports should normalize to empty export_dir while resolving cleanly
	if cfgResp.Config.ExportDir != "" || cfgResp.ResolvedExportDir != filepath.Join(projDir, "exports") {
		t.Fatalf("unexpected export dir config=%q resolved=%q", cfgResp.Config.ExportDir, cfgResp.ResolvedExportDir)
	}
}
