package presets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func setTestPresetEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg-data"))
	t.Setenv("AppData", filepath.Join(root, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "AppData", "Local"))
	return root
}

func makeCanonicalModelDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

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

func TestListSynthesizesDefaultAndReadsProjectPresetsV1(t *testing.T) {
	setTestPresetEnv(t)
	dir := makeCanonicalModelDir(t, "test_model_unique_123")
	raw := `{
		"schema_version": 1,
		"presets": {
			"prusa": {
				"label": "Prusa Rod",
				"description": "12.7mm rod",
				"params": {"shaft_diameter": 12.7, "stale": true}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "presets.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	defaults := map[string]any{"shaft_diameter": 10.0, "cap_style": "chamfer"}
	list, err := ListStrict("lib/test_model_unique_123", dir, defaults, nil)
	if err != nil {
		t.Fatalf("ListStrict failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 presets (default + prusa), got %d", len(list))
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

func TestListUsesCustomDefaultMetaAndSkipsDefaultKeyInPresetsMap(t *testing.T) {
	setTestPresetEnv(t)
	dir := makeCanonicalModelDir(t, "test_custom_default_model")
	raw := `{
		"schema_version": 1,
		"default": {
			"verification": {"status": "verified", "note": "Printed well"}
		},
		"presets": {
			"default": {
				"label": "Should Be Ignored",
				"params": {"shaft_diameter": 99.0}
			},
			"mini": {
				"label": "Mini",
				"params": {"shaft_diameter": 8.0}
			}
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
	list := List("lib/test_custom_default_model", dir, defaults, meta)
	if len(list) != 2 {
		t.Fatalf("expected 2 presets (no duplicate default), got %d: %#v", len(list), list)
	}
	if list[0].Key != "default" || list[0].Label != "Prusa Core One Spool" || list[0].Description != "44 mm spool hub retaining clip" || list[0].Source != "default" {
		t.Fatalf("unexpected custom default preset: %#v", list[0])
	}
	if list[0].Verification == nil || list[0].Verification.Status != StatusVerified || list[0].Verification.Note != "Printed well" {
		t.Fatalf("expected default verification from envelope, got %#v", list[0].Verification)
	}
	if len(list[0].Params) != 0 {
		t.Fatalf("default preset params must stay empty, got %#v", list[0].Params)
	}
	if list[1].Key != "mini" {
		t.Fatalf("expected second preset 'mini', got %#v", list[1])
	}
}

func TestSaveSparseVsFullAndDelete(t *testing.T) {
	setTestPresetEnv(t)
	dir := makeCanonicalModelDir(t, "clip")
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
	key, err := Save("lib/clip", dir, "Thin Clip", "Half width", "user", false, current, defaults, nil)
	if err != nil || key != "thin_clip" {
		t.Fatalf("Save sparse user failed: key=%q err=%v", key, err)
	}
	list := List("lib/clip", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "user" {
		t.Fatalf("expected user preset, got %#v", list)
	}
	coreMap := list[1].Params["core"].(map[string]any)
	if _, hasDia := coreMap["diameter"]; hasDia || coreMap["thickness"] != 6.0 {
		t.Fatalf("expected sparse diff with only thickness=6.0, got %#v", coreMap)
	}

	// Full snapshot project save stores all fields and clears shadowed user copy
	key2, err := Save("lib/clip", dir, "Thin Clip", "Shared full", "project", true, current, defaults, &Verification{
		Status: StatusVerified,
		Note:   "Printed in PETG, snug fit",
	})
	if err != nil || key2 != "thin_clip" {
		t.Fatalf("Save full project failed: key=%q err=%v", key2, err)
	}
	list = List("lib/clip", dir, defaults, nil)
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

	// Verify saved presets.json has schema_version: 1 envelope
	diskBytes, err := os.ReadFile(filepath.Join(dir, "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var env PresetFile
	if err := json.Unmarshal(diskBytes, &env); err != nil {
		t.Fatal(err)
	}
	if env.SchemaVersion != 1 || env.Presets["thin_clip"].Label != "Thin Clip" {
		t.Fatalf("unexpected v1 envelope on disk: %#v", env)
	}

	// Rename project preset and verify params and verification are preserved under the new key
	renamedKey, err := Rename("lib/clip", dir, "thin_clip", "project", "Slim Clip", "Updated note")
	if err != nil || renamedKey != "slim_clip" {
		t.Fatalf("Rename failed: key=%q err=%v", renamedKey, err)
	}
	list = List("lib/clip", dir, defaults, nil)
	if len(list) != 2 || list[1].Key != "slim_clip" || list[1].Label != "Slim Clip" || list[1].Description != "Updated note" {
		t.Fatalf("unexpected preset after rename: %#v", list)
	}
	if list[1].Verification == nil || list[1].Verification.Status != StatusVerified || list[1].Verification.Note != "Printed in PETG, snug fit" {
		t.Fatalf("expected verification preserved after rename, got %#v", list[1].Verification)
	}

	// Delete project preset and verify canonical default cannot be deleted
	if err := Delete("lib/clip", dir, "default", "project"); err == nil {
		t.Fatalf("expected deleting default preset to fail")
	}
	if err := Delete("lib/clip", dir, "slim_clip", "project"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	list = List("lib/clip", dir, defaults, nil)
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

	merged := MergeWithDefaults(defaults, map[string]any{})
	mergedItems := merged["cutouts"].(map[string]any)["items"].([]any)
	mergedItems[0].(map[string]any)["leg"] = "y"
	origLeg := defaults["cutouts"].(map[string]any)["items"].([]any)[0].(map[string]any)["leg"]
	if origLeg != "x" {
		t.Fatalf("MergeWithDefaults mutated original defaults slice: got leg=%v", origLeg)
	}
}

func TestVerificationLifecycleAndDefaultMetadata(t *testing.T) {
	setTestPresetEnv(t)
	dir := makeCanonicalModelDir(t, "lid")
	defaults := map[string]any{"diameter": 150.0}

	list := List("lib/lid", dir, defaults, &DefaultMeta{Label: "Base Lid", Description: "Canonical"})
	if len(list) != 1 || list[0].Verification != nil {
		t.Fatalf("expected single unverified default preset, got %#v", list)
	}

	if err := SetVerification("lib/lid", dir, "default", "default", &Verification{
		Status: StatusVerified,
		Note:   "TPU 95A, 0.2mm layers",
	}); err != nil {
		t.Fatalf("SetVerification default failed: %v", err)
	}
	list = List("lib/lid", dir, defaults, &DefaultMeta{Label: "Base Lid", Description: "Canonical"})
	if len(list) != 1 || list[0].Label != "Base Lid" || list[0].Verification == nil || list[0].Verification.Status != StatusVerified || list[0].Verification.Note != "TPU 95A, 0.2mm layers" {
		t.Fatalf("unexpected default preset after verification: %#v", list)
	}

	if err := DowngradeDefaultVerification(dir); err != nil {
		t.Fatalf("DowngradeDefaultVerification failed: %v", err)
	}
	list = List("lib/lid", dir, defaults, &DefaultMeta{Label: "Base Lid"})
	if list[0].Verification == nil || list[0].Verification.Status != StatusUnverified || list[0].Verification.Note != "TPU 95A, 0.2mm layers" {
		t.Fatalf("expected downgraded status with preserved note, got %#v", list[0].Verification)
	}

	if err := SetVerification("lib/lid", dir, "default", "default", &Verification{Status: StatusUnverified, Note: ""}); err != nil {
		t.Fatalf("clearing default verification failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "presets.json")); !os.IsNotExist(err) {
		t.Fatalf("expected empty presets.json to be removed after clearing default verification")
	}

	if _, err := Save("lib/lid", dir, "Small Bowl", "", "user", false, map[string]any{"diameter": 98.0}, defaults, nil); err != nil {
		t.Fatalf("Save user failed: %v", err)
	}
	if err := SetVerification("lib/lid", dir, "small_bowl", "user", &Verification{Status: StatusUsable, Note: "Slightly loose"}); err != nil {
		t.Fatalf("SetVerification user failed: %v", err)
	}
	list = List("lib/lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Verification == nil || list[1].Verification.Status != StatusUsable || list[1].Verification.Note != "Slightly loose" {
		t.Fatalf("unexpected user preset verification: %#v", list)
	}

	if err := SetVerification("lib/lid", dir, "small_bowl", "user", &Verification{Status: StatusUnusable, Note: "Skirt too short"}); err != nil {
		t.Fatalf("SetVerification unusable failed: %v", err)
	}
	list = List("lib/lid", dir, defaults, nil)
	if list[1].Verification == nil || list[1].Verification.Status != StatusUnusable {
		t.Fatalf("expected unusable status, got %#v", list[1].Verification)
	}

	if _, err := ParseVerificationStatus("bogus"); err == nil {
		t.Fatalf("expected invalid status to fail parsing")
	}
}

func TestSaveOverwritesHandKeyedAndCrossScopePresetsWithoutDuplicates(t *testing.T) {
	setTestPresetEnv(t)
	dir := makeCanonicalModelDir(t, "lid")
	defaults := map[string]any{
		"diameter": 150.0,
		"height":   10.0,
	}

	initial := `{
  "schema_version": 1,
  "presets": {
    "anchor_round_6_cup": {
      "label": "Anchor Hocking Round 6-Cup (1.5 qt)",
      "description": "Original note",
      "params": {
        "diameter": 187.3
      }
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "presets.json"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	key, err := Save(
		"lib/lid",
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
	list := List("lib/lid", dir, defaults, nil)
	if len(list) != 2 {
		t.Fatalf("expected 2 presets (no duplicate), got %d: %#v", len(list), list)
	}
	if list[1].Key != "anchor_round_6_cup" || list[1].Description != "Updated fit" || list[1].Params["diameter"] != 186.5 {
		t.Fatalf("unexpected overwritten project preset: %#v", list[1])
	}

	key2, err := Save(
		"lib/lid",
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
	list = List("lib/lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Params["diameter"] != 186.0 {
		t.Fatalf("expected single updated preset after case-insensitive save, got %#v", list)
	}

	userKey, err := Save(
		"lib/lid",
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
	list = List("lib/lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "user" || list[1].Params["diameter"] != 185.0 {
		t.Fatalf("expected single user-shadowed preset, got %#v", list)
	}

	if err := Delete("lib/lid", dir, "anchor_round_6_cup", "user"); err != nil {
		t.Fatalf("delete user override failed: %v", err)
	}
	list = List("lib/lid", dir, defaults, nil)
	if len(list) != 2 || list[1].Source != "project" || list[1].Params["diameter"] != 186.0 {
		t.Fatalf("expected underlying project preset restored, got %#v", list)
	}

	newKey, err := Save(
		"lib/lid",
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
	list = List("lib/lid", dir, defaults, nil)
	if len(list) != 3 {
		t.Fatalf("expected 3 presets (default + original + new), got %d: %#v", len(list), list)
	}

	keptKey, err := Rename("lib/lid", dir, "anchor_round_6_cup", "project", "Anchor Hocking Round 6-Cup (1.5 qt)", "Edited description")
	if err != nil || keptKey != "anchor_round_6_cup" {
		t.Fatalf("expected Rename with unchanged label to keep key 'anchor_round_6_cup', got key=%q err=%v", keptKey, err)
	}
	if _, err := Rename("lib/lid", dir, "anchor_hocking_round_6_cup_tight", "project", "Anchor Hocking Round 6-Cup (1.5 qt)", ""); err == nil {
		t.Fatalf("expected Rename to duplicate existing label to fail")
	}
}

func TestSharedDirectoryStemPresetScoping(t *testing.T) {
	setTestPresetEnv(t)
	sharedDir := filepath.Join(t.TempDir(), "shared_scripts")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	alphaID := "code-cad/shared_scripts/alpha"
	betaID := "code-cad/shared_scripts/beta"
	defaults := map[string]any{"size": 10.0}

	if got := ProjectPresetPath(alphaID, sharedDir); got != filepath.Join(sharedDir, "alpha.presets.json") {
		t.Fatalf("ProjectPresetPath(alpha) = %q", got)
	}
	if got := ProjectPresetPath(betaID, sharedDir); got != filepath.Join(sharedDir, "beta.presets.json") {
		t.Fatalf("ProjectPresetPath(beta) = %q", got)
	}
	canonicalDir := filepath.Join(sharedDir, "gamma")
	if got := ProjectPresetPath("code-cad/shared_scripts/gamma", canonicalDir); got != filepath.Join(canonicalDir, "presets.json") {
		t.Fatalf("ProjectPresetPath(canonical gamma) = %q", got)
	}
	if got := ProjectPresetPathForScript(filepath.Join(sharedDir, "alpha.py")); got != filepath.Join(sharedDir, "alpha.presets.json") {
		t.Fatalf("ProjectPresetPathForScript(alpha.py) = %q", got)
	}
	if got := ProjectPresetPathForScript(filepath.Join(canonicalDir, "gamma.py")); got != filepath.Join(canonicalDir, "presets.json") {
		t.Fatalf("ProjectPresetPathForScript(gamma.py) = %q", got)
	}

	// Save presets for alpha and beta in the same directory; verify complete isolation
	if _, err := Save(alphaID, sharedDir, "Alpha Large", "", "project", false, map[string]any{"size": 40.0}, defaults, nil); err != nil {
		t.Fatalf("Save alpha failed: %v", err)
	}
	if _, err := Save(betaID, sharedDir, "Beta Small", "", "project", false, map[string]any{"size": 5.0}, defaults, nil); err != nil {
		t.Fatalf("Save beta failed: %v", err)
	}

	alphaList, err := ListStrict(alphaID, sharedDir, defaults, nil)
	if err != nil {
		t.Fatalf("ListStrict alpha failed: %v", err)
	}
	if len(alphaList) != 2 || alphaList[1].Key != "alpha_large" || alphaList[1].Params["size"] != 40.0 {
		t.Fatalf("unexpected alpha presets: %#v", alphaList)
	}

	betaList, err := ListStrict(betaID, sharedDir, defaults, nil)
	if err != nil {
		t.Fatalf("ListStrict beta failed: %v", err)
	}
	if len(betaList) != 2 || betaList[1].Key != "beta_small" || betaList[1].Params["size"] != 5.0 {
		t.Fatalf("unexpected beta presets: %#v", betaList)
	}

	// Default verification and DowngradeDefaultVerificationForFile scope to alpha.presets.json only
	if err := SetVerification(alphaID, sharedDir, "default", "default", &Verification{Status: StatusVerified, Note: "Alpha print OK"}); err != nil {
		t.Fatalf("SetVerification default alpha failed: %v", err)
	}
	if err := SetVerification(betaID, sharedDir, "default", "default", &Verification{Status: StatusVerified, Note: "Beta print OK"}); err != nil {
		t.Fatalf("SetVerification default beta failed: %v", err)
	}
	if err := DowngradeDefaultVerificationForFile(filepath.Join(sharedDir, "alpha.py")); err != nil {
		t.Fatalf("DowngradeDefaultVerificationForFile failed: %v", err)
	}
	alphaList, _ = ListStrict(alphaID, sharedDir, defaults, nil)
	betaList, _ = ListStrict(betaID, sharedDir, defaults, nil)
	if alphaList[0].Verification == nil || alphaList[0].Verification.Status != StatusUnverified || alphaList[0].Verification.Note != "Alpha print OK" {
		t.Fatalf("expected alpha default downgraded with note preserved, got %#v", alphaList[0].Verification)
	}
	if betaList[0].Verification == nil || betaList[0].Verification.Status != StatusVerified {
		t.Fatalf("expected beta default untouched, got %#v", betaList[0].Verification)
	}
}

func TestMalformedOrWrongSchemaPresetsPreservedOnDisk(t *testing.T) {
	setTestPresetEnv(t)
	dir := makeCanonicalModelDir(t, "corrupt_model")
	presetPath := filepath.Join(dir, "presets.json")
	defaults := map[string]any{"width": 10.0}

	malformed := []byte("{broken json: [1, 2")
	if err := os.WriteFile(presetPath, malformed, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ListStrict("lib/corrupt_model", dir, defaults, nil); err == nil {
		t.Fatalf("expected ListStrict to error on malformed JSON")
	}
	if _, err := Save("lib/corrupt_model", dir, "New", "", "project", false, map[string]any{"width": 20.0}, defaults, nil); err == nil {
		t.Fatalf("expected Save to error on malformed project presets.json")
	}
	if err := SetVerification("lib/corrupt_model", dir, "default", "default", &Verification{Status: StatusVerified}); err == nil {
		t.Fatalf("expected SetVerification to error on malformed presets.json")
	}
	if err := Delete("lib/corrupt_model", dir, "x", "project"); err == nil {
		t.Fatalf("expected Delete to error on malformed presets.json")
	}
	if _, err := Rename("lib/corrupt_model", dir, "x", "project", "Y", ""); err == nil {
		t.Fatalf("expected Rename to error on malformed presets.json")
	}
	if err := DowngradeDefaultVerification(dir); err == nil {
		t.Fatalf("expected DowngradeDefaultVerification to error on malformed presets.json")
	}

	afterMalformed, err := os.ReadFile(presetPath)
	if err != nil || string(afterMalformed) != string(malformed) {
		t.Fatalf("malformed presets.json must never be wiped or modified, got %q err=%v", afterMalformed, err)
	}

	// Wrong schema_version (v0 flat or v99) must also error and remain untouched
	wrongSchema := []byte(`{"schema_version": 2, "presets": {"a": {"label": "A", "params": {"width": 15}}}}`)
	if err := os.WriteFile(presetPath, wrongSchema, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ListStrict("lib/corrupt_model", dir, defaults, nil); err == nil {
		t.Fatalf("expected ListStrict to reject schema_version 2")
	}
	if _, err := Save("lib/corrupt_model", dir, "New", "", "project", false, map[string]any{"width": 20.0}, defaults, nil); err == nil {
		t.Fatalf("expected Save to reject schema_version 2")
	}
	afterWrong, err := os.ReadFile(presetPath)
	if err != nil || string(afterWrong) != string(wrongSchema) {
		t.Fatalf("wrong-schema presets.json must remain untouched on disk")
	}
}

func TestUserPresetsCanonicalModelIDWindowsSafeAndCollisionFree(t *testing.T) {
	setTestPresetEnv(t)
	dirA := makeCanonicalModelDir(t, "bin")
	dirB := makeCanonicalModelDir(t, "bin")
	defaults := map[string]any{"width": 42.0}

	idA := "code-cad/gridfinity/bin"
	idB := "personal/gridfinity/bin"
	idUnderscore := "code-cad/gridfinity_bin"

	if _, err := Save(idA, dirA, "Wide", "Lib A wide", "user", false, map[string]any{"width": 84.0}, defaults, nil); err != nil {
		t.Fatalf("Save lib A failed: %v", err)
	}
	if _, err := Save(idB, dirB, "Wide", "Lib B wide", "user", false, map[string]any{"width": 126.0}, defaults, nil); err != nil {
		t.Fatalf("Save lib B failed: %v", err)
	}
	if _, err := Save(idUnderscore, dirA, "Wide", "Underscore wide", "user", false, map[string]any{"width": 168.0}, defaults, nil); err != nil {
		t.Fatalf("Save underscore ID failed: %v", err)
	}

	listA, err := ListStrict(idA, dirA, defaults, nil)
	if err != nil {
		t.Fatal(err)
	}
	listB, err := ListStrict(idB, dirB, defaults, nil)
	if err != nil {
		t.Fatal(err)
	}
	listUnderscore, err := ListStrict(idUnderscore, dirA, defaults, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(listA) != 2 || listA[1].Params["width"] != 84.0 {
		t.Fatalf("unexpected lib A presets: %#v", listA)
	}
	if len(listB) != 2 || listB[1].Params["width"] != 126.0 {
		t.Fatalf("unexpected lib B presets: %#v", listB)
	}
	if len(listUnderscore) != 2 || listUnderscore[1].Params["width"] != 168.0 {
		t.Fatalf("unexpected underscore ID presets (collision?): %#v", listUnderscore)
	}

	pathA, err := userPresetPath(idA)
	if err != nil {
		t.Fatal(err)
	}
	pathUnderscore, err := userPresetPath(idUnderscore)
	if err != nil {
		t.Fatal(err)
	}
	if pathA == pathUnderscore {
		t.Fatalf("expected distinct user preset files for %q and %q, both mapped to %q", idA, idUnderscore, pathA)
	}
	sumA := sha256.Sum256([]byte(idA))
	wantNameA := "gridfinity_bin--" + hex.EncodeToString(sumA[:4]) + ".json"
	if filepath.Base(filepath.Dir(pathA)) != "code-cad" || filepath.Base(pathA) != wantNameA {
		t.Fatalf("unexpected user preset path %q, want dir=code-cad base=%q", pathA, wantNameA)
	}

	// Ensure Windows-unsafe characters in modelID never reach filename
	unsafePath, err := userPresetPath(`my:lib/sub\part?name*<1>`)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(unsafePath)
	if strings.ContainsAny(base, `\/:*?"<>|`) {
		t.Fatalf("user preset filename %q contains Windows-unsafe characters", base)
	}
}

func TestConcurrentPresetMutationsUnderRace(t *testing.T) {
	setTestPresetEnv(t)
	dir := makeCanonicalModelDir(t, "concurrent_part")
	modelID := "lib/concurrent_part"
	defaults := map[string]any{"val": 1.0}

	const workers = 10
	var wg sync.WaitGroup
	errCh := make(chan error, workers*3)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			label := fmt.Sprintf("Variant %d", idx)
			key, err := Save(modelID, dir, label, "concurrent save", "project", false, map[string]any{"val": float64(idx + 2)}, defaults, nil)
			if err != nil {
				errCh <- fmt.Errorf("Save(%d): %w", idx, err)
				return
			}
			if err := SetVerification(modelID, dir, key, "project", &Verification{Status: StatusVerified, Note: label}); err != nil {
				errCh <- fmt.Errorf("SetVerification(%d): %w", idx, err)
				return
			}
			if _, err := ListStrict(modelID, dir, defaults, nil); err != nil {
				errCh <- fmt.Errorf("ListStrict(%d): %w", idx, err)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	finalList, err := ListStrict(modelID, dir, defaults, nil)
	if err != nil {
		t.Fatalf("final ListStrict failed: %v", err)
	}
	if len(finalList) != workers+1 {
		t.Fatalf("expected %d presets (default + %d concurrent saves), got %d", workers+1, workers, len(finalList))
	}
}
