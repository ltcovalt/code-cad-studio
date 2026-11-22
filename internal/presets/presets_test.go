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
	list := List("test_model_unique_123", dir, defaults, nil)
	if len(list) < 2 {
		t.Fatalf("expected at least 2 presets (default + prusa), got %d", len(list))
	}
	if list[0].Key != "default" || list[0].Label != "Base" {
		t.Fatalf("first preset must be synthesized Base, got %#v", list[0])
	}
	if list[1].Key != "prusa" || list[1].Params["shaft_diameter"] != 12.7 {
		t.Fatalf("unexpected project preset: %#v", list[1])
	}
	if _, hasStale := list[1].Params["stale"]; hasStale {
		t.Fatalf("stale key should have been stripped on load")
	}
}

func TestListUsesCustomDefaultMetaAndSkipsDefaultKeyInJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	raw := `{
		"default": {
			"label": "Should Be Ignored",
			"params": {"shaft_diameter": 99.0}
		},
		"mini": {
			"label": "Mini",
			"params": {"shaft_diameter": 8.0}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "presets.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	defaults := map[string]any{"shaft_diameter": 10.0}
	meta := &DefaultMeta{
		Label:       "Prusa Core One Spool",
		Description: "44 mm spool hub retaining clip",
	}
	list := List("test_custom_default_model", dir, defaults, meta)
	if len(list) != 2 {
		t.Fatalf("expected 2 presets (no duplicate default), got %d: %#v", len(list), list)
	}
	if list[0].Key != "default" || list[0].Label != "Prusa Core One Spool" || list[0].Description != "44 mm spool hub retaining clip" || list[0].Source != "default" {
		t.Fatalf("unexpected custom default preset: %#v", list[0])
	}
	if len(list[0].Params) != 0 {
		t.Fatalf("default preset params must stay empty, got %#v", list[0].Params)
	}
	if list[1].Key != "mini" {
		t.Fatalf("expected second preset 'mini', got %#v", list[1])
	}
}

func TestSaveSparseVsFullAndDelete(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	defaults := map[string]any{
		"core": map[string]any{
			"diameter":  44.0,
			"thickness": 12.0,
		},
	}
	current := map[string]any{
		"core": map[string]any{
			"diameter":  44.0,
			"thickness": 6.0,
		},
	}

	// Sparse user save stores only changed fields
	key, err := Save("clip", dir, "Thin Clip", "Half width", "user", false, current, defaults)
	if err != nil || key != "thin_clip" {
		t.Fatalf("Save sparse user failed: key=%q err=%v", key, err)
	}
	list := List("clip", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "user" {
		t.Fatalf("expected user preset, got %#v", list)
	}
	coreMap := list[1].Params["core"].(map[string]any)
	if _, hasDia := coreMap["diameter"]; hasDia || coreMap["thickness"] != 6.0 {
		t.Fatalf("expected sparse diff with only thickness=6.0, got %#v", coreMap)
	}

	// Full snapshot project save stores all fields and clears shadowed user copy
	key2, err := Save("clip", dir, "Thin Clip", "Shared full", "project", true, current, defaults)
	if err != nil || key2 != "thin_clip" {
		t.Fatalf("Save full project failed: key=%q err=%v", key2, err)
	}
	list = List("clip", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "project" {
		t.Fatalf("expected project preset without user shadow, got %#v", list)
	}
	coreMap = list[1].Params["core"].(map[string]any)
	if coreMap["diameter"] != 44.0 || coreMap["thickness"] != 6.0 {
		t.Fatalf("expected full snapshot with both fields, got %#v", coreMap)
	}

	// Rename project preset and verify params are preserved under the new key
	renamedKey, err := Rename("clip", dir, "thin_clip", "project", "Slim Clip", "Updated note")
	if err != nil || renamedKey != "slim_clip" {
		t.Fatalf("Rename failed: key=%q err=%v", renamedKey, err)
	}
	list = List("clip", dir, defaults, nil)
	if len(list) != 2 || list[1].Key != "slim_clip" || list[1].Label != "Slim Clip" || list[1].Description != "Updated note" {
		t.Fatalf("unexpected preset after rename: %#v", list)
	}

	// Delete project preset and verify canonical default cannot be deleted
	if err := Delete("clip", dir, "default", "project"); err == nil {
		t.Fatalf("expected deleting default preset to fail")
	}
	if err := Delete("clip", dir, "slim_clip", "project"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	list = List("clip", dir, defaults, nil)
	if len(list) != 1 {
		t.Fatalf("expected 1 preset (default) remaining after delete, got %#v", list)
	}
}
