package presets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSparseDiffAndFilter(t *testing.T) {
	defaults := map[string]any{
		"shaft_diameter": 10.0,
		"cap_style":      "chamfer",
		"flutes_enabled": true,
		"base": map[string]any{
			"flange_diameter": 32.0,
			"chamfer_size":    0.8,
		},
	}

	current := map[string]any{
		"shaft_diameter": 12.7,
		"cap_style":      "chamfer",
		"flutes_enabled": true,
		"obsolete_key":   99,
		"base": map[string]any{
			"flange_diameter": 36.0,
			"chamfer_size":    0.8,
		},
	}

	diff := SparseDiff(current, defaults)
	if diff["shaft_diameter"] != 12.7 {
		t.Fatalf("expected shaft_diameter=12.7, got %v", diff["shaft_diameter"])
	}
	if _, exists := diff["cap_style"]; exists {
		t.Fatalf("unchanged cap_style should not be in sparse diff")
	}
	if _, exists := diff["obsolete_key"]; exists {
		t.Fatalf("unknown key should not be in sparse diff")
	}
	baseDiff, ok := diff["base"].(map[string]any)
	if !ok || baseDiff["flange_diameter"] != 36.0 || len(baseDiff) != 1 {
		t.Fatalf("unexpected nested base diff: %#v", diff["base"])
	}

	// Verify FilterParams drops stale keys and MergeWithDefaults restores canonical defaults
	filtered := FilterParams(map[string]any{
		"shaft_diameter": 12.7,
		"removed_field":  "old",
	}, defaults)
	if _, exists := filtered["removed_field"]; exists {
		t.Fatalf("stale key should be filtered out")
	}

	merged := MergeWithDefaults(defaults, filtered)
	if merged["shaft_diameter"] != 12.7 || merged["cap_style"] != "chamfer" {
		t.Fatalf("unexpected merged result: %#v", merged)
	}
}

func TestListSynthesizesDefaultAndReadsProjectPresets(t *testing.T) {
	dir := t.TempDir()
	raw := `{
		"prusa": {
			"label": "Prusa Rod",
			"description": "12.7mm rod",
			"params": {"shaft_diameter": 12.7, "stale": true}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "presets.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	defaults := map[string]any{"shaft_diameter": 10.0, "cap_style": "chamfer"}
	list := List("test_model_unique_123", dir, defaults)
	if len(list) < 2 {
		t.Fatalf("expected at least 2 presets (default + prusa), got %d", len(list))
	}
	if list[0].Key != "default" || list[0].Label != "Default" {
		t.Fatalf("first preset must be synthesized Default, got %#v", list[0])
	}
	if list[1].Key != "prusa" || list[1].Params["shaft_diameter"] != 12.7 {
		t.Fatalf("unexpected project preset: %#v", list[1])
	}
	if _, hasStale := list[1].Params["stale"]; hasStale {
		t.Fatalf("stale key should have been stripped on load")
	}
}
