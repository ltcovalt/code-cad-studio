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

// VerificationStatus describes the print-testing state of a preset.
type VerificationStatus string

const (
	StatusUnverified VerificationStatus = "unverified"
	StatusUnusable   VerificationStatus = "unusable"
	StatusUsable     VerificationStatus = "usable"
	StatusVerified   VerificationStatus = "verified"
)

// Verification tracks physical print-testing status and free-text notes for a preset.
type Verification struct {
	Status VerificationStatus `json:"status"`
	Note   string             `json:"note,omitempty"`
}

// ParseVerificationStatus validates a raw status string. Empty string maps to StatusUnverified.
func ParseVerificationStatus(raw string) (VerificationStatus, error) {
	switch VerificationStatus(strings.ToLower(strings.TrimSpace(raw))) {
	case "", StatusUnverified:
		return StatusUnverified, nil
	case StatusUnusable:
		return StatusUnusable, nil
	case StatusUsable:
		return StatusUsable, nil
	case StatusVerified:
		return StatusVerified, nil
	default:
		return "", errors.New("invalid verification status (must be unverified, unusable, usable, or verified)")
	}
}

// NormalizeVerification trims fields and returns nil when unverified with no note
// so empty verification metadata is omitted cleanly from JSON.
func NormalizeVerification(v *Verification) *Verification {
	if v == nil {
		return nil
	}
	status, err := ParseVerificationStatus(string(v.Status))
	if err != nil {
		status = StatusUnverified
	}
	note := strings.TrimSpace(v.Note)
	if status == StatusUnverified && note == "" {
		return nil
	}
	return &Verification{
		Status: status,
		Note:   note,
	}
}

// PresetEntry is the JSON schema stored inside presets.json.
// For the special "default" key, only Verification is used (Label/Description/Params are omitted).
type PresetEntry struct {
	Label        string         `json:"label,omitempty"`
	Description  string         `json:"description,omitempty"`
	Verification *Verification  `json:"verification,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
}

// DefaultMeta carries optional display metadata for the synthesized canonical default preset.
type DefaultMeta struct {
	Label       string
	Description string
}

// Preset is the resolved preset returned to the UI.
type Preset struct {
	Key          string         `json:"key"`
	Label        string         `json:"label"`
	Description  string         `json:"description"`
	Source       string         `json:"source"` // "default", "project", "user"
	Verification *Verification  `json:"verification,omitempty"`
	Params       map[string]any `json:"params"`
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

func sanitizePresetFilenameSegment(s string) string {
	r := strings.NewReplacer("/", "__", `\`, "__", ":", "__")
	cleaned := strings.TrimSpace(r.Replace(s))
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "model"
	}
	return cleaned
}

func userPresetPath(modelID string) (string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	baseDir := filepath.Join(cfgDir, "presets")

	if libID, localKey, ok := strings.Cut(modelID, "::"); ok {
		libDir := filepath.Join(baseDir, sanitizePresetFilenameSegment(libID))
		if err := os.MkdirAll(libDir, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(libDir, sanitizePresetFilenameSegment(localKey)+".json"), nil
	}

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(baseDir, sanitizePresetFilenameSegment(modelID)+".json"), nil
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
	var defaultVerification *Verification

	if modelDir != "" {
		projEntries := readPresetFile(filepath.Join(modelDir, "presets.json"))
		for key, entry := range projEntries {
			if key == "default" {
				defaultVerification = NormalizeVerification(entry.Verification)
				continue
			}
			label := entry.Label
			if label == "" {
				label = key
			}
			byKey[key] = Preset{
				Key:          key,
				Label:        label,
				Description:  entry.Description,
				Source:       "project",
				Verification: NormalizeVerification(entry.Verification),
				Params:       FilterParams(entry.Params, defaults),
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
				Key:          key,
				Label:        label,
				Description:  entry.Description,
				Source:       "user",
				Verification: NormalizeVerification(entry.Verification),
				Params:       FilterParams(entry.Params, defaults),
			}
		}
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		li := strings.ToLower(byKey[keys[i]].Label)
		lj := strings.ToLower(byKey[keys[j]].Label)
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
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
		Key:          "default",
		Label:        defaultLabel,
		Description:  defaultDesc,
		Source:       "default",
		Verification: defaultVerification,
		Params:       map[string]any{},
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

// findMatchingKey locates an existing non-default preset key matching the given label
// (case-insensitive trimmed label match first, then slugified key match).
func findMatchingKey(entries map[string]PresetEntry, label string) (string, bool) {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" || len(entries) == 0 {
		return "", false
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		if k == "default" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if strings.EqualFold(strings.TrimSpace(entries[k].Label), trimmed) {
			return k, true
		}
	}

	slug := Slugify(trimmed)
	if slug != "default" {
		if _, ok := entries[slug]; ok {
			return slug, true
		}
	}
	return "", false
}

// Save writes a preset (sparse diff or full snapshot) to either "project" (<modelDir>/presets.json)
// or "user" (~/.config/...). If a preset with the same name (or slugified key) already exists, its
// existing key is preserved and updated in-place rather than creating a duplicate entry. Saving to
// "project" also removes any identically keyed or identically named user preset so a stale local
// copy cannot silently shadow or duplicate the shared project preset.
func Save(modelID, modelDir, label, description, scope string, fullSnapshot bool, currentParams, defaults map[string]any, verification *Verification) (string, error) {
	trimmedLabel := strings.TrimSpace(label)
	isProject := scope == "project" && modelDir != ""

	var projPath string
	var projEntries map[string]PresetEntry
	if modelDir != "" {
		projPath = filepath.Join(modelDir, "presets.json")
		projEntries = readPresetFile(projPath)
	}

	userPath, userErr := userPresetPath(modelID)
	if !isProject && userErr != nil {
		return "", userErr
	}
	var userEntries map[string]PresetEntry
	if userErr == nil {
		userEntries = readPresetFile(userPath)
	}

	var key string
	if isProject {
		if k, ok := findMatchingKey(projEntries, trimmedLabel); ok {
			key = k
		} else if k, ok := findMatchingKey(userEntries, trimmedLabel); ok {
			key = k
		} else {
			key = Slugify(trimmedLabel)
		}
	} else {
		if k, ok := findMatchingKey(userEntries, trimmedLabel); ok {
			key = k
		} else if k, ok := findMatchingKey(projEntries, trimmedLabel); ok {
			key = k
		} else {
			key = Slugify(trimmedLabel)
		}
	}

	var storedParams map[string]any
	if fullSnapshot {
		storedParams = FilterParams(currentParams, defaults)
	} else {
		storedParams = SparseDiff(currentParams, defaults)
	}

	entry := PresetEntry{
		Label:        trimmedLabel,
		Description:  strings.TrimSpace(description),
		Verification: NormalizeVerification(verification),
		Params:       storedParams,
	}

	if isProject {
		if projEntries == nil {
			projEntries = map[string]PresetEntry{}
		}
		projEntries[key] = entry
		if err := writePresetFile(projPath, projEntries); err != nil {
			return "", err
		}

		if userErr == nil && len(userEntries) > 0 {
			changed := false
			for uk, ue := range userEntries {
				if uk == "default" {
					continue
				}
				if uk == key || strings.EqualFold(strings.TrimSpace(ue.Label), trimmedLabel) {
					delete(userEntries, uk)
					changed = true
				}
			}
			if changed {
				_ = writePresetFile(userPath, userEntries)
			}
		}
	} else {
		if userEntries == nil {
			userEntries = map[string]PresetEntry{}
		}
		userEntries[key] = entry
		if err := writePresetFile(userPath, userEntries); err != nil {
			return "", err
		}
	}

	return key, nil
}

// SetVerification updates verification metadata for a preset in its owning scope.
// For the canonical "default" preset, verification is stored as a metadata-only entry
// under key "default" in <modelDir>/presets.json and removed when cleared.
func SetVerification(modelID, modelDir, key, scope string, v *Verification) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("preset key is required")
	}

	norm := NormalizeVerification(v)

	if key == "default" {
		if modelDir == "" {
			return errors.New("project model directory is unavailable")
		}
		targetPath := filepath.Join(modelDir, "presets.json")
		existing := readPresetFile(targetPath)
		if norm == nil {
			delete(existing, "default")
		} else {
			existing["default"] = PresetEntry{
				Verification: norm,
			}
		}
		return writePresetFile(targetPath, existing)
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
	entry, ok := existing[key]
	if !ok {
		return errors.New("preset not found in target scope")
	}
	entry.Verification = norm
	existing[key] = entry
	return writePresetFile(targetPath, existing)
}

// DowngradeDefaultVerification resets the canonical "default" preset's verification status
// to "unverified" while preserving any print note (or pruning the entry if no note exists).
// Called after modifying model default parameters in the Python source file.
func DowngradeDefaultVerification(modelDir string) error {
	if modelDir == "" {
		return nil
	}
	targetPath := filepath.Join(modelDir, "presets.json")
	existing := readPresetFile(targetPath)
	entry, ok := existing["default"]
	if !ok {
		return nil
	}
	norm := NormalizeVerification(entry.Verification)
	if norm == nil || norm.Status == StatusUnverified {
		return nil
	}
	if norm.Note == "" {
		delete(existing, "default")
	} else {
		existing["default"] = PresetEntry{
			Verification: &Verification{
				Status: StatusUnverified,
				Note:   norm.Note,
			},
		}
	}
	return writePresetFile(targetPath, existing)
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

	newKey := oldKey
	if !strings.EqualFold(strings.TrimSpace(entry.Label), trimmedLabel) {
		newKey = Slugify(trimmedLabel)
	}
	for k, e := range existing {
		if k == oldKey || k == "default" {
			continue
		}
		if k == newKey || strings.EqualFold(strings.TrimSpace(e.Label), trimmedLabel) {
			return "", errors.New("a preset with that name already exists")
		}
	}
	if newKey != oldKey {
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
