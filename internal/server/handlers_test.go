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
	dirPath  string
	filePath string
	savePath string
	lastSave string
}

func (d *stubDialogs) ChooseDirectory(title, startDir string) (string, error) {
	return d.dirPath, nil
}

func (d *stubDialogs) ChooseFile(title, startDir string) (string, error) {
	return d.filePath, nil
}

func (d *stubDialogs) SaveFile(title, defaultFilename, filterName, filterPattern string) (string, error) {
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
}

func TestWorkerSchemaNullableWhenAndExportFilename(t *testing.T) {
	projectDir := t.TempDir()
	modelDir := filepath.Join(projectDir, "models", "demo_box")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	modelSrc := `from dataclasses import dataclass, field
from typing import Literal, Optional

@dataclass
class CoreParams:
    """Core shape settings."""
    mode: Literal["round", "rect"] = "round"  # Shape mode
    diameter: Optional[float] = field(default=None, metadata={"min": 10.0, "max": 200.0, "step": 1.0, "when": {"mode": "round"}})
    width: float = field(default=50.0, metadata={"when": {"mode": "rect"}})

@dataclass
class ExtraParams:
    """Optional extra feature."""
    enabled: Optional[bool] = None

@dataclass
class DemoBoxParams:
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
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &modelResp); err != nil {
		t.Fatalf("decode model schema: %v", err)
	}
	if !modelResp.OK || len(modelResp.Fields) != 2 {
		t.Fatalf("unexpected schema response: %+v", modelResp)
	}
	coreGroup := modelResp.Fields[0]
	if coreGroup.Description != "Core shape settings." {
		t.Fatalf("expected nested docstring fallback, got %q", coreGroup.Description)
	}
	var diaField worker.ParamField
	for _, c := range coreGroup.Children {
		if c.Name == "diameter" {
			diaField = c
		}
	}
	if !diaField.Nullable {
		t.Fatalf("expected diameter.nullable=true")
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

	// Verify export_filename resolution via SaveFile default filename
	rec = httptest.NewRecorder()
	exportBody := bytes.NewBufferString(`{"format":"stl","params":{"core":{"mode":"rect"}}}`)
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/demo_box/export", exportBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/demo_box/export status = %d", rec.Code)
	}
	if dialogs.lastSave != "demo_box_rect.stl" {
		t.Fatalf("expected SaveFile default filename 'demo_box_rect.stl', got %q", dialogs.lastSave)
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
}
