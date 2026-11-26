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
	"github.com/ltcovalt/code-cad-studio/internal/presets"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

func TestComponentPresetResolutionAndSaveNormalization(t *testing.T) {
	fields := []worker.ParamField{
		{
			Name: "core",
			Path: "core",
			Type: "group",
			Children: []worker.ParamField{
				{Name: "rim_diameter", Path: "core.rim_diameter", Type: "float", Default: 54.0},
			},
		},
		{
			Name:          "pull_tab",
			Path:          "pull_tab",
			Type:          "group",
			Nullable:      true,
			ComponentID:   "lib/components/pull_tab",
			ComponentName: "Pull Tab",
			Default:       map[string]any{"length": 6.0, "width": 16.0, "hanging_hole": false},
			GroupDefaults: map[string]any{"length": 6.0, "width": 16.0, "hanging_hole": false},
			Children: []worker.ParamField{
				{Name: "length", Path: "pull_tab.length", Type: "float", Default: 6.0},
				{Name: "width", Path: "pull_tab.width", Type: "float", Default: 16.0},
				{Name: "hanging_hole", Path: "pull_tab.hanging_hole", Type: "bool", Default: false},
			},
		},
		{
			Name:          "gridfinity",
			Path:          "gridfinity",
			Type:          "group",
			Nullable:      true,
			ComponentID:   "lib/components/gridfinity_base",
			ComponentName: "Gridfinity Base",
			Default:       nil,
			GroupDefaults: map[string]any{"magnets": true, "magnet_diameter": 6.1},
			Children: []worker.ParamField{
				{Name: "magnets", Path: "gridfinity.magnets", Type: "bool", Default: true},
				{Name: "magnet_diameter", Path: "gridfinity.magnet_diameter", Type: "float", Default: 6.1},
			},
		},
	}

	modelDefaults := map[string]any{
		"core":       map[string]any{"rim_diameter": 54.0},
		"pull_tab":   map[string]any{"length": 6.0, "width": 16.0, "hanging_hole": false},
		"gridfinity": nil,
	}

	bundles := map[string]ComponentPresetBundle{
		"lib/components/pull_tab": {
			ID:       "lib/components/pull_tab",
			Name:     "Pull Tab",
			Defaults: map[string]any{"length": 6.0, "width": 16.0, "hanging_hole": false},
			Fields:   fields[1].Children,
			Presets: []presets.Preset{
				{Key: "default", Label: "Standard", Source: "default", Params: map[string]any{}},
				{Key: "heavy_duty", Label: "Heavy Duty", Source: "project", Params: map[string]any{"length": 8.0, "width": 20.0}},
			},
		},
		"lib/components/gridfinity_base": {
			ID:       "lib/components/gridfinity_base",
			Name:     "Gridfinity Base",
			Defaults: map[string]any{"magnets": true, "magnet_diameter": 6.1},
			Fields:   fields[2].Children,
			Presets: []presets.Preset{
				{Key: "default", Label: "With Magnets", Source: "default", Params: map[string]any{}},
				{Key: "no_magnets", Label: "No Magnets", Source: "project", Params: map[string]any{"magnets": false}},
			},
		},
	}

	// Normalize drops redundant "default" for already-enabled pull_tab, but keeps explicit "default" for disabled-by-default gridfinity.
	norm := normalizeModelComponentPresets(
		map[string]string{
			"pull_tab":   "default",
			"gridfinity": "default",
			"unknown":    "foo",
		},
		map[string]any{
			"core":       map[string]any{"rim_diameter": 54.0},
			"pull_tab":   map[string]any{"length": 6.0, "width": 16.0, "hanging_hole": false},
			"gridfinity": map[string]any{"magnets": true, "magnet_diameter": 6.1},
		},
		modelDefaults,
		fields,
	)
	if _, hasPullTab := norm["pull_tab"]; hasPullTab {
		t.Fatalf("expected implicit default for enabled pull_tab to be omitted, got %#v", norm)
	}
	if norm["gridfinity"] != "default" {
		t.Fatalf("expected explicit default for disabled-by-default gridfinity, got %#v", norm)
	}
	if _, hasUnknown := norm["unknown"]; hasUnknown {
		t.Fatalf("expected unknown path stripped, got %#v", norm)
	}

	// Resolve preset with component_presets + sparse override
	resolved := resolvePresetParams(
		modelDefaults,
		fields,
		presets.Preset{
			Key: "soup",
			ComponentPresets: map[string]string{
				"pull_tab":   "heavy_duty",
				"gridfinity": "no_magnets",
			},
			Params: map[string]any{
				"core":     map[string]any{"rim_diameter": 75.0},
				"pull_tab": map[string]any{"hanging_hole": true},
			},
		},
		bundles,
		map[string]bool{},
	)
	pt := resolved["pull_tab"].(map[string]any)
	if pt["length"] != 8.0 || pt["width"] != 20.0 || pt["hanging_hole"] != true {
		t.Fatalf("unexpected resolved pull_tab: %#v", pt)
	}
	gf := resolved["gridfinity"].(map[string]any)
	if gf["magnets"] != false || gf["magnet_diameter"] != 6.1 {
		t.Fatalf("unexpected resolved gridfinity: %#v", gf)
	}

	// Effective save defaults overlay component preset params so SparseDiff stays minimal
	effective := buildEffectiveSaveDefaults(
		modelDefaults,
		fields,
		map[string]string{"pull_tab": "heavy_duty"},
		bundles,
	)
	effTab := effective["pull_tab"].(map[string]any)
	if effTab["length"] != 8.0 || effTab["width"] != 20.0 || effTab["hanging_hole"] != false {
		t.Fatalf("unexpected effective save defaults for pull_tab: %#v", effTab)
	}
}

func TestServerComponentDiscoveryAndPresetSaveFlow(t *testing.T) {
	py := findTestPython(t)
	srv := newTestServer(t, nil)

	libDir := filepath.Join(t.TempDir(), "my_cad")
	for _, sub := range []string{
		filepath.Join(libDir, "utils"),
		filepath.Join(libDir, "components", "pull_tab"),
		filepath.Join(libDir, "models", "can_lid"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	paramsBytes, err := os.ReadFile(filepath.Join("..", "..", "worker", "params.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "utils", "params.py"), paramsBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "components", "__init__.py"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "components", "pull_tab", "__init__.py"), []byte("from .pull_tab import PullTabParams, create_pull_tab\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pullTabSrc := `from __future__ import annotations
from dataclasses import dataclass
from utils.params import default_preset, param

@default_preset("Standard Tab", desc="Default pull tab")
@dataclass
class PullTabParams:
    length: float = param(6.0, unit="mm")
    width: float = param(16.0, unit="mm")
    hanging_hole: bool = param(False)

def create_pull_tab(params: PullTabParams):
    return params
`
	if err := os.WriteFile(filepath.Join(libDir, "components", "pull_tab", "pull_tab.py"), []byte(pullTabSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	pullTabPresets := `{
  "schema_version": 1,
  "presets": {
    "heavy_duty": {
      "label": "Heavy Duty",
      "description": "Longer wider tab",
      "params": {
        "length": 8.0,
        "width": 20.0
      }
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(libDir, "components", "pull_tab", "presets.json"), []byte(pullTabPresets), 0o644); err != nil {
		t.Fatal(err)
	}

	canLidSrc := `from __future__ import annotations
from dataclasses import dataclass
from components.pull_tab import PullTabParams
from utils.params import group, param

@dataclass
class CoreParams:
    rim_diameter: float = param(54.0, unit="mm")

@dataclass
class CanLidParams:
    core: CoreParams = group(CoreParams)
    pull_tab: PullTabParams | None = group(PullTabParams)

def create_can_lid(params: CanLidParams):
    return params
`
	if err := os.WriteFile(filepath.Join(libDir, "models", "can_lid", "can_lid.py"), []byte(canLidSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.cfgStore.Update(func(c *config.Config) {
		c.Libraries = []config.Library{{ID: "lib", Name: "My CAD", Path: libDir}}
		c.PythonPath = py
		c.OnboardingCompleted = true
	}); err != nil {
		t.Fatal(err)
	}

	handler := srv.Handler()

	// 1. GET /api/models discovers both model and component with categories
	recList := httptest.NewRecorder()
	handler.ServeHTTP(recList, newAPIRequest(http.MethodGet, "/api/models", nil))
	if recList.Code != http.StatusOK {
		t.Fatalf("GET /api/models status=%d body=%s", recList.Code, recList.Body.String())
	}
	var listResp struct {
		OK     bool               `json:"ok"`
		Models []worker.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(recList.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Models) != 2 {
		t.Fatalf("expected 2 discovered items (1 model + 1 component), got %#v", listResp.Models)
	}

	// 2. GET /api/models/lib%2Fcan_lid returns component bundle for pull_tab
	mPath := modelPath("lib/can_lid")
	recGet := httptest.NewRecorder()
	handler.ServeHTTP(recGet, newAPIRequest(http.MethodGet, mPath, nil))
	if recGet.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", mPath, recGet.Code, recGet.Body.String())
	}
	var detailResp struct {
		OK         bool                             `json:"ok"`
		Fields     []worker.ParamField              `json:"fields"`
		Components map[string]ComponentPresetBundle `json:"components"`
	}
	if err := json.Unmarshal(recGet.Body.Bytes(), &detailResp); err != nil {
		t.Fatal(err)
	}
	bundle, ok := detailResp.Components["lib/components/pull_tab"]
	if !ok {
		t.Fatalf("expected pull_tab component bundle in model detail, got %#v", detailResp.Components)
	}
	if len(bundle.Presets) != 2 || bundle.Presets[1].Key != "heavy_duty" {
		t.Fatalf("expected default + heavy_duty component presets, got %#v", bundle.Presets)
	}

	// 3. POST /api/models/lib%2Fcan_lid/presets referencing heavy_duty omits duplicated pull_tab fields
	saveBody, _ := json.Marshal(map[string]any{
		"label":             "Food Can 28oz",
		"description":       "Uses heavy duty pull tab",
		"scope":             "project",
		"component_presets": map[string]string{"pull_tab": "heavy_duty"},
		"params": map[string]any{
			"core": map[string]any{"rim_diameter": 101.5},
			"pull_tab": map[string]any{
				"length":       8.0,
				"width":        20.0,
				"hanging_hole": false,
			},
		},
	})
	recSave := httptest.NewRecorder()
	handler.ServeHTTP(recSave, newAPIRequest(http.MethodPost, mPath+"/presets", bytes.NewReader(saveBody)))
	if recSave.Code != http.StatusOK {
		t.Fatalf("POST presets status=%d body=%s", recSave.Code, recSave.Body.String())
	}

	rawPresetDisk, err := os.ReadFile(filepath.Join(libDir, "models", "can_lid", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk presets.PresetFile
	if err := json.Unmarshal(rawPresetDisk, &onDisk); err != nil {
		t.Fatal(err)
	}
	entry := onDisk.Presets["food_can_28oz"]
	if entry.ComponentPresets["pull_tab"] != "heavy_duty" {
		t.Fatalf("expected component_presets.pull_tab=heavy_duty on disk, got %#v", entry)
	}
	if _, hasTab := entry.Params["pull_tab"]; hasTab {
		t.Fatalf("expected pull_tab omitted from on-disk params when matching component preset, got %#v", entry.Params)
	}
}
