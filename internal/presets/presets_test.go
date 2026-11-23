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
	key, err := Save("clip", dir, "Thin Clip", "Half width", "user", false, current, defaults, nil)
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
	key2, err := Save("clip", dir, "Thin Clip", "Shared full", "project", true, current, defaults, &Verification{
		Status: StatusVerified,
		Note:   "Printed in PETG, snug fit",
	})
	if err != nil || key2 != "thin_clip" {
		t.Fatalf("Save full project failed: key=%q err=%v", key2, err)
	}
	list = List("clip", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "project" {
		t.Fatalf("expected project preset without user shadow, got %#v", list)
	}
	if list[1].Verification == nil || list[1].Verification.Status != StatusVerified || list[1].Verification.Note != "Printed in PETG, snug fit" {
		t.Fatalf("expected verification preserved on project preset, got %#v", list[1].Verification)
	}
	coreMap = list[1].Params["core"].(map[string]any)
	if coreMap["diameter"] != 44.0 || coreMap["thickness"] != 6.0 {
		t.Fatalf("expected full snapshot with both fields, got %#v", coreMap)
	}

	// Rename project preset and verify params and verification are preserved under the new key
	renamedKey, err := Rename("clip", dir, "thin_clip", "project", "Slim Clip", "Updated note")
	if err != nil || renamedKey != "slim_clip" {
		t.Fatalf("Rename failed: key=%q err=%v", renamedKey, err)
	}
	list = List("clip", dir, defaults, nil)
	if len(list) != 2 || list[1].Key != "slim_clip" || list[1].Label != "Slim Clip" || list[1].Description != "Updated note" {
		t.Fatalf("unexpected preset after rename: %#v", list)
	}
	if list[1].Verification == nil || list[1].Verification.Status != StatusVerified || list[1].Verification.Note != "Printed in PETG, snug fit" {
		t.Fatalf("expected verification preserved after rename, got %#v", list[1].Verification)
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

func TestSparseDiffWithListItems(t *testing.T) {
	defaults := map[string]any{
		"cutouts": map[string]any{
			"enabled": false,
			"items": []any{
				map[string]any{"leg": "x", "length": 42.0, "height": 16.0},
			},
		},
	}

	// Identical values with int vs float64 must NOT appear in sparse diff
	same := map[string]any{
		"cutouts": map[string]any{
			"enabled": false,
			"items": []any{
				map[string]any{"leg": "x", "length": 42, "height": 16},
			},
		},
	}
	if diff := SparseDiff(same, defaults); len(diff) != 0 {
		t.Fatalf("expected empty sparse diff for equivalent list items, got %#v", diff)
	}

	// Modified list items MUST appear in sparse diff
	changed := map[string]any{
		"cutouts": map[string]any{
			"enabled": true,
			"items": []any{
				map[string]any{"leg": "x", "length": 42.0, "height": 16.0},
				map[string]any{"leg": "y", "length": 25.0, "height": 10.0},
			},
		},
	}
	diff := SparseDiff(changed, defaults)
	cutoutsDiff, ok := diff["cutouts"].(map[string]any)
	if !ok {
		t.Fatalf("expected cutouts diff map, got %#v", diff)
	}
	itemsDiff, ok := cutoutsDiff["items"].([]any)
	if !ok || len(itemsDiff) != 2 {
		t.Fatalf("expected 2 items in cutouts diff, got %#v", cutoutsDiff["items"])
	}

	// MergeWithDefaults must deep-copy slices so mutating merged does not mutate defaults
	merged := MergeWithDefaults(defaults, map[string]any{})
	mergedItems := merged["cutouts"].(map[string]any)["items"].([]any)
	mergedItems[0].(map[string]any)["leg"] = "y"
	origLeg := defaults["cutouts"].(map[string]any)["items"].([]any)[0].(map[string]any)["leg"]
	if origLeg != "x" {
		t.Fatalf("MergeWithDefaults mutated original defaults slice: got leg=%v", origLeg)
	}
}

func TestVerificationLifecycleAndDefaultMetadata(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	defaults := map[string]any{"diameter": 150.0}

	// Initially unverified with no presets.json file
	list := List("lid", dir, defaults, &DefaultMeta{Label: "Base Lid", Description: "Canonical"})
	if len(list) != 1 || list[0].Verification != nil {
		t.Fatalf("expected single unverified default preset, got %#v", list)
	}

	// Setting verification on canonical "default" creates metadata-only entry in presets.json
	if err := SetVerification("lid", dir, "default", "default", &Verification{
		Status: StatusVerified,
		Note:   "TPU 95A, 0.2mm layers",
	}); err != nil {
		t.Fatalf("SetVerification default failed: %v", err)
	}
	list = List("lid", dir, defaults, &DefaultMeta{Label: "Base Lid", Description: "Canonical"})
	if len(list) != 1 || list[0].Label != "Base Lid" || list[0].Verification == nil || list[0].Verification.Status != StatusVerified || list[0].Verification.Note != "TPU 95A, 0.2mm layers" {
		t.Fatalf("unexpected default preset after verification: %#v", list)
	}

	// DowngradeDefaultVerification resets status to unverified while keeping print note
	if err := DowngradeDefaultVerification(dir); err != nil {
		t.Fatalf("DowngradeDefaultVerification failed: %v", err)
	}
	list = List("lid", dir, defaults, &DefaultMeta{Label: "Base Lid"})
	if list[0].Verification == nil || list[0].Verification.Status != StatusUnverified || list[0].Verification.Note != "TPU 95A, 0.2mm layers" {
		t.Fatalf("expected downgraded status with preserved note, got %#v", list[0].Verification)
	}

	// Clearing default verification removes the "default" entry and cleans up empty presets.json
	if err := SetVerification("lid", dir, "default", "default", &Verification{Status: StatusUnverified, Note: ""}); err != nil {
		t.Fatalf("clearing default verification failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "presets.json")); !os.IsNotExist(err) {
		t.Fatalf("expected empty presets.json to be removed after clearing default verification")
	}

	// Verify user & project verification updates
	if _, err := Save("lid", dir, "Small Bowl", "", "user", false, map[string]any{"diameter": 98.0}, defaults, nil); err != nil {
		t.Fatalf("Save user failed: %v", err)
	}
	if err := SetVerification("lid", dir, "small_bowl", "user", &Verification{Status: StatusUsable, Note: "Slightly loose"}); err != nil {
		t.Fatalf("SetVerification user failed: %v", err)
	}
	list = List("lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Verification == nil || list[1].Verification.Status != StatusUsable || list[1].Verification.Note != "Slightly loose" {
		t.Fatalf("unexpected user preset verification: %#v", list)
	}

	if err := SetVerification("lid", dir, "small_bowl", "user", &Verification{Status: StatusUnusable, Note: "Skirt too short"}); err != nil {
		t.Fatalf("SetVerification unusable failed: %v", err)
	}
	list = List("lid", dir, defaults, nil)
	if list[1].Verification == nil || list[1].Verification.Status != StatusUnusable {
		t.Fatalf("expected unusable status, got %#v", list[1].Verification)
	}

	// Invalid status parsing must fail
	if _, err := ParseVerificationStatus("bogus"); err == nil {
		t.Fatalf("expected invalid status to fail parsing")
	}
}

func TestSaveOverwritesHandKeyedAndCrossScopePresetsWithoutDuplicates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	defaults := map[string]any{
		"diameter": 150.0,
		"height":   10.0,
	}

	// Seed a hand-authored project preset whose key does NOT equal Slugify(label)
	initial := `{
  "anchor_round_6_cup": {
    "label": "Anchor Hocking Round 6-Cup (1.5 qt)",
    "description": "Original note",
    "params": {
      "diameter": 187.3
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "presets.json"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Saving with the exact same name in project scope must overwrite anchor_round_6_cup in-place
	key, err := Save(
		"lid",
		dir,
		"Anchor Hocking Round 6-Cup (1.5 qt)",
		"Updated fit",
		"project",
		false,
		map[string]any{"diameter": 186.5, "height": 10.0},
		defaults,
		&Verification{Status: StatusVerified, Note: "Snug fit"},
	)
	if err != nil {
		t.Fatalf("Save overwrite failed: %v", err)
	}
	if key != "anchor_round_6_cup" {
		t.Fatalf("expected existing key 'anchor_round_6_cup' to be preserved, got %q", key)
	}
	list := List("lid", dir, defaults, nil)
	if len(list) != 2 {
		t.Fatalf("expected 2 presets (no duplicate), got %d: %#v", len(list), list)
	}
	if list[1].Key != "anchor_round_6_cup" || list[1].Description != "Updated fit" || list[1].Params["diameter"] != 186.5 {
		t.Fatalf("unexpected overwritten project preset: %#v", list[1])
	}

	// 2. Case-insensitive / whitespace-trimmed label match also overwrites in-place
	key2, err := Save(
		"lid",
		dir,
		"  anchor hocking round 6-cup (1.5 qt)  ",
		"Trimmed case update",
		"project",
		false,
		map[string]any{"diameter": 186.0, "height": 10.0},
		defaults,
		nil,
	)
	if err != nil || key2 != "anchor_round_6_cup" {
		t.Fatalf("case-insensitive overwrite failed: key=%q err=%v", key2, err)
	}
	list = List("lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Params["diameter"] != 186.0 {
		t.Fatalf("expected single updated preset after case-insensitive save, got %#v", list)
	}

	// 3. Saving the same name to user scope creates a local override under the same key (no duplicate)
	userKey, err := Save(
		"lid",
		dir,
		"Anchor Hocking Round 6-Cup (1.5 qt)",
		"Local override",
		"user",
		false,
		map[string]any{"diameter": 185.0, "height": 10.0},
		defaults,
		nil,
	)
	if err != nil || userKey != "anchor_round_6_cup" {
		t.Fatalf("user override save failed: key=%q err=%v", userKey, err)
	}
	list = List("lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "user" || list[1].Params["diameter"] != 185.0 {
		t.Fatalf("expected single user-shadowed preset, got %#v", list)
	}

	// Deleting the user override restores the underlying project preset cleanly
	if err := Delete("lid", dir, "anchor_round_6_cup", "user"); err != nil {
		t.Fatalf("delete user override failed: %v", err)
	}
	list = List("lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "project" || list[1].Params["diameter"] != 186.0 {
		t.Fatalf("expected underlying project preset restored, got %#v", list)
	}

	// 4. Saving with a different name creates a new preset while preserving the existing one
	newKey, err := Save(
		"lid",
		dir,
		"Anchor Hocking Round 6-Cup (Tight)",
		"Tighter variant",
		"project",
		false,
		map[string]any{"diameter": 184.5, "height": 10.0},
		defaults,
		nil,
	)
	if err != nil || newKey != "anchor_hocking_round_6_cup_tight" {
		t.Fatalf("new preset save failed: key=%q err=%v", newKey, err)
	}
	list = List("lid", dir, defaults, nil)
	if len(list) != 3 {
		t.Fatalf("expected 3 presets (default + original + new), got %d: %#v", len(list), list)
	}

	// 5. Rename preserves hand-crafted key when only description/casing changes and blocks label collisions
	keptKey, err := Rename("lid", dir, "anchor_round_6_cup", "project", "Anchor Hocking Round 6-Cup (1.5 qt)", "Edited description")
	if err != nil || keptKey != "anchor_round_6_cup" {
		t.Fatalf("expected Rename with unchanged label to keep key 'anchor_round_6_cup', got key=%q err=%v", keptKey, err)
	}
	if _, err := Rename("lid", dir, "anchor_hocking_round_6_cup_tight", "project", "Anchor Hocking Round 6-Cup (1.5 qt)", ""); err == nil {
		t.Fatalf("expected Rename to duplicate existing label to fail")
	}
}

func TestUserPresetsIsolatedAcrossLibrariesWithNamespacedIDs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dirA := t.TempDir()
	dirB := t.TempDir()
	defaults := map[string]any{"width": 42.0}

	idA := "code-cad::gridfinity__bin"
	idB := "personal::gridfinity__bin"

	if _, err := Save(idA, dirA, "Wide", "Lib A wide", "user", false, map[string]any{"width": 84.0}, defaults, nil); err != nil {
		t.Fatalf("Save lib A failed: %v", err)
	}
	if _, err := Save(idB, dirB, "Wide", "Lib B wide", "user", false, map[string]any{"width": 126.0}, defaults, nil); err != nil {
		t.Fatalf("Save lib B failed: %v", err)
	}

	listA := List(idA, dirA, defaults, nil)
	listB := List(idB, dirB, defaults, nil)
	if len(listA) != 2 || listA[1].Params["width"] != 84.0 {
		t.Fatalf("unexpected lib A presets: %#v", listA)
	}
	if len(listB) != 2 || listB[1].Params["width"] != 126.0 {
		t.Fatalf("unexpected lib B presets: %#v", listB)
	}

	pathA, err := userPresetPath(idA)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(pathA)) != "code-cad" || filepath.Base(pathA) != "gridfinity__bin.json" {
		t.Fatalf("expected partitioned OS-safe preset path, got %q", pathA)
	}
}
