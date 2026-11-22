package presets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/config"
)

// PresetEntry is the JSON schema stored inside presets.json.
type PresetEntry struct {
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Params      map[string]any `json:"params"`
}

// DefaultMeta carries optional display metadata for the synthesized canonical default preset.
type DefaultMeta struct {
	Label       string
	Description string
}

// Preset is the resolved preset returned to the UI.
type Preset struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Source      string         `json:"source"` // "default", "project", "user"
	Params      map[string]any `json:"params"`
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a human label into a clean preset key.
func Slugify(label string) string {
	s := strings.ToLower(strings.TrimSpace(label))
	s = nonSlug.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" || s == "default" {
		return "custom_preset"
	}
	return s
}

func userPresetPath(modelID string) (string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	pDir := filepath.Join(cfgDir, "presets")
	if err := os.MkdirAll(pDir, 0o755); err != nil {
		return "", err
	}
	safeID := strings.ReplaceAll(modelID, "/", "__")
	return filepath.Join(pDir, safeID+".json"), nil
}

func readPresetFile(path string) map[string]PresetEntry {
	out := map[string]PresetEntry{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// FilterParams removes keys not present in canonical defaults (recursively for nested maps).
func FilterParams(raw map[string]any, defaults map[string]any) map[string]any {
	res := make(map[string]any)
	for k, defVal := range defaults {
		val, ok := raw[k]
		if !ok {
			continue
		}
		defMap, defIsMap := defVal.(map[string]any)
		valMap, valIsMap := val.(map[string]any)
		if defIsMap && valIsMap {
			nested := FilterParams(valMap, defMap)
			if len(nested) > 0 {
				res[k] = nested
			}
		} else {
			res[k] = val
		}
	}
	return res
}

// MergeWithDefaults overlays sparse preset overrides onto canonical defaults.
func MergeWithDefaults(defaults map[string]any, overrides map[string]any) map[string]any {
	merged := deepCopyMap(defaults)
	for k, v := range overrides {
		defVal, exists := merged[k]
		if !exists {
			continue
		}
		defMap, defIsMap := defVal.(map[string]any)
		valMap, valIsMap := v.(map[string]any)
		if defIsMap && valIsMap {
			merged[k] = MergeWithDefaults(defMap, valMap)
		} else {
			merged[k] = deepCopyValue(v)
		}
	}
	return merged
}

// SparseDiff computes only the fields that differ from canonical defaults.
func SparseDiff(current map[string]any, defaults map[string]any) map[string]any {
	diff := make(map[string]any)
	for k, defVal := range defaults {
		curVal, ok := current[k]
		if !ok {
			continue
		}
		defMap, defIsMap := defVal.(map[string]any)
		curMap, curIsMap := curVal.(map[string]any)
		if defIsMap && curIsMap {
			sub := SparseDiff(curMap, defMap)
			if len(sub) > 0 {
				diff[k] = sub
			}
			continue
		}
		if !valuesEqual(curVal, defVal) {
			diff[k] = curVal
		}
	}
	return diff
}

func valuesEqual(a, b any) bool {
	// Normalize int/float64 comparison since JSON numbers decode as float64
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	am, amok := a.(map[string]any)
	bm, bmok := b.(map[string]any)
	if amok && bmok {
		if len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			bv, exists := bm[k]
			if !exists || !valuesEqual(av, bv) {
				return false
			}
		}
		return true
	}
	as, asok := a.([]any)
	bs, bsok := b.([]any)
	if asok && bsok {
		if len(as) != len(bs) {
			return false
		}
		for i := range as {
			if !valuesEqual(as[i], bs[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = deepCopyValue(item)
		}
		return out
	default:
		return v
	}
}

func deepCopyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopyValue(v)
	}
	return dst
}

// List returns the synthesized default preset followed by project and user presets.
func List(modelID, modelDir string, defaults map[string]any, defaultMeta *DefaultMeta) []Preset {
	byKey := make(map[string]Preset)

	if modelDir != "" {
		projEntries := readPresetFile(filepath.Join(modelDir, "presets.json"))
		for key, entry := range projEntries {
			if key == "default" {
				continue
			}
			label := entry.Label
			if label == "" {
				label = key
			}
			byKey[key] = Preset{
				Key:         key,
				Label:       label,
				Description: entry.Description,
				Source:      "project",
				Params:      FilterParams(entry.Params, defaults),
			}
		}
	}

	if upath, err := userPresetPath(modelID); err == nil {
		userEntries := readPresetFile(upath)
		for key, entry := range userEntries {
			if key == "default" {
				continue
			}
			label := entry.Label
			if label == "" {
				label = key
			}
			byKey[key] = Preset{
				Key:         key,
				Label:       label,
				Description: entry.Description,
				Source:      "user",
				Params:      FilterParams(entry.Params, defaults),
			}
		}
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(byKey[keys[i]].Label) < strings.ToLower(byKey[keys[j]].Label)
	})

	defaultLabel := "Base"
	defaultDesc := "Parameters defined in the model script"
	if defaultMeta != nil {
		if l := strings.TrimSpace(defaultMeta.Label); l != "" {
			defaultLabel = l
		}
		if d := strings.TrimSpace(defaultMeta.Description); d != "" {
			defaultDesc = d
		}
	}

	result := make([]Preset, 0, len(keys)+1)
	result = append(result, Preset{
		Key:         "default",
		Label:       defaultLabel,
		Description: defaultDesc,
		Source:      "default",
		Params:      map[string]any{},
	})
	for _, k := range keys {
		result = append(result, byKey[k])
	}
	return result
}

func writePresetFile(path string, entries map[string]PresetEntry) error {
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Save writes a preset (sparse diff or full snapshot) to either "project" (<modelDir>/presets.json)
// or "user" (~/.config/...). Saving to "project" also removes any identically keyed user preset
// so a stale local copy cannot silently shadow the shared project preset.
func Save(modelID, modelDir, label, description, scope string, fullSnapshot bool, currentParams, defaults map[string]any) (string, error) {
	key := Slugify(label)
	var targetPath string
	isProject := scope == "project" && modelDir != ""
	if isProject {
		targetPath = filepath.Join(modelDir, "presets.json")
	} else {
		p, err := userPresetPath(modelID)
		if err != nil {
			return "", err
		}
		targetPath = p
	}

	var storedParams map[string]any
	if fullSnapshot {
		storedParams = FilterParams(currentParams, defaults)
	} else {
		storedParams = SparseDiff(currentParams, defaults)
	}

	existing := readPresetFile(targetPath)
	existing[key] = PresetEntry{
		Label:       strings.TrimSpace(label),
		Description: strings.TrimSpace(description),
		Params:      storedParams,
	}

	if err := writePresetFile(targetPath, existing); err != nil {
		return "", err
	}

	if isProject {
		if upath, err := userPresetPath(modelID); err == nil {
			userEntries := readPresetFile(upath)
			if _, shadowed := userEntries[key]; shadowed {
				delete(userEntries, key)
				_ = writePresetFile(upath, userEntries)
			}
		}
	}

	return key, nil
}

// Delete removes a preset key from either "project" or "user" storage.
func Delete(modelID, modelDir, key, scope string) error {
	key = strings.TrimSpace(key)
	if key == "" || key == "default" {
		return errors.New("cannot delete canonical default preset")
	}

	var targetPath string
	if scope == "project" {
		if modelDir == "" {
			return errors.New("project model directory is unavailable")
		}
		targetPath = filepath.Join(modelDir, "presets.json")
	} else {
		p, err := userPresetPath(modelID)
		if err != nil {
			return err
		}
		targetPath = p
	}

	existing := readPresetFile(targetPath)
	if _, ok := existing[key]; !ok {
		return errors.New("preset not found in target scope")
	}
	delete(existing, key)
	return writePresetFile(targetPath, existing)
}

// Rename updates a preset's label (and optional description) within its scope,
// migrating its JSON key to the slugified new label while preserving its params.
func Rename(modelID, modelDir, oldKey, scope, newLabel, newDescription string) (string, error) {
	oldKey = strings.TrimSpace(oldKey)
	if oldKey == "" || oldKey == "default" {
		return "", errors.New("cannot rename canonical default preset")
	}
	trimmedLabel := strings.TrimSpace(newLabel)
	if trimmedLabel == "" {
		return "", errors.New("preset name is required")
	}

	var targetPath string
	if scope == "project" {
		if modelDir == "" {
			return "", errors.New("project model directory is unavailable")
		}
		targetPath = filepath.Join(modelDir, "presets.json")
	} else {
		p, err := userPresetPath(modelID)
		if err != nil {
			return "", err
		}
		targetPath = p
	}

	existing := readPresetFile(targetPath)
	entry, ok := existing[oldKey]
	if !ok {
		return "", errors.New("preset not found in target scope")
	}

	newKey := Slugify(trimmedLabel)
	if newKey != oldKey {
		if _, collision := existing[newKey]; collision {
			return "", errors.New("a preset with that name already exists")
		}
		delete(existing, oldKey)
	}

	entry.Label = trimmedLabel
	entry.Description = strings.TrimSpace(newDescription)
	existing[newKey] = entry

	if err := writePresetFile(targetPath, existing); err != nil {
		return "", err
	}
	return newKey, nil
}
