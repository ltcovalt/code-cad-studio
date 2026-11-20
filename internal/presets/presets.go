package presets

import (
	"encoding/json"
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
			merged[k] = v
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

func deepCopyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if m, ok := v.(map[string]any); ok {
			dst[k] = deepCopyMap(m)
		} else {
			dst[k] = v
		}
	}
	return dst
}

// List returns the synthesized "Default" preset followed by project and user presets.
func List(modelID, modelDir string, defaults map[string]any) []Preset {
	byKey := make(map[string]Preset)

	if modelDir != "" {
		projEntries := readPresetFile(filepath.Join(modelDir, "presets.json"))
		for key, entry := range projEntries {
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

	result := make([]Preset, 0, len(keys)+1)
	result = append(result, Preset{
		Key:         "default",
		Label:       "Default",
		Description: "Canonical defaults from Params dataclass",
		Source:      "default",
		Params:      map[string]any{},
	})
	for _, k := range keys {
		result = append(result, byKey[k])
	}
	return result
}

// Save writes a sparse preset diff to either "project" (<modelDir>/presets.json) or "user" (~/.config/...).
func Save(modelID, modelDir, label, description, scope string, currentParams, defaults map[string]any) (string, error) {
	key := Slugify(label)
	var targetPath string
	if scope == "project" && modelDir != "" {
		targetPath = filepath.Join(modelDir, "presets.json")
	} else {
		p, err := userPresetPath(modelID)
		if err != nil {
			return "", err
		}
		targetPath = p
	}

	existing := readPresetFile(targetPath)
	existing[key] = PresetEntry{
		Label:       strings.TrimSpace(label),
		Description: strings.TrimSpace(description),
		Params:      SparseDiff(currentParams, defaults),
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return key, nil
}
