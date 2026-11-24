package presets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ltcovalt/code-cad-studio/internal/config"
)

// SchemaVersion is the current persisted presets.json envelope schema version.
const SchemaVersion = 1

var storeMu sync.Mutex

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

// DefaultSection holds metadata-only overrides for the canonical default preset.
type DefaultSection struct {
	Verification *Verification `json:"verification,omitempty"`
}

// PresetEntry is the JSON schema stored for each named preset inside presets.json.
type PresetEntry struct {
	Label        string         `json:"label,omitempty"`
	Description  string         `json:"description,omitempty"`
	Verification *Verification  `json:"verification,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
}

// PresetFile is the v1 envelope stored on disk in presets.json / <stem>.presets.json / user preset files.
type PresetFile struct {
	SchemaVersion int                    `json:"schema_version"`
	Default       *DefaultSection        `json:"default,omitempty"`
	Presets       map[string]PresetEntry `json:"presets,omitempty"`
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

var (
	nonSlug             = regexp.MustCompile(`[^a-z0-9]+`)
	nonSafeFilenameChar = regexp.MustCompile(`[^a-z0-9_-]+`)
)

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

func sanitizeSlugForFilename(raw, fallback string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = nonSafeFilenameChar.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_-")
	if s == "" || s == "." || s == ".." {
		return fallback
	}
	if len(s) > 64 {
		s = strings.Trim(s[:64], "_-")
		if s == "" {
			return fallback
		}
	}
	return s
}

func extractModelStem(modelID string) string {
	s := strings.TrimSpace(modelID)
	if s == "" {
		return ""
	}
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	} else if _, local, ok := strings.Cut(s, "::"); ok {
		if idx := strings.LastIndex(local, "__"); idx >= 0 {
			s = local[idx+2:]
		} else {
			s = local
		}
	}
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `/\:`) {
		return ""
	}
	return s
}

// ProjectPresetPath resolves the project-scope preset file path for a model:
// - Canonical directory (<parent>/<stem>.py where dir base == stem): <modelDir>/presets.json
// - Shared-directory standalone script (dir base != stem): <modelDir>/<stem>.presets.json
func ProjectPresetPath(modelID, modelDir string) string {
	if strings.TrimSpace(modelDir) == "" {
		return ""
	}
	cleanDir := filepath.Clean(modelDir)
	stem := extractModelStem(modelID)
	if stem != "" && filepath.Base(cleanDir) != stem {
		return filepath.Join(cleanDir, stem+".presets.json")
	}
	return filepath.Join(cleanDir, "presets.json")
}

// ProjectPresetPathForScript resolves the project-scope preset file path directly from a .py file path.
func ProjectPresetPathForScript(pyPath string) string {
	trimmed := strings.TrimSpace(pyPath)
	if trimmed == "" {
		return ""
	}
	clean := filepath.Clean(trimmed)
	dir := filepath.Dir(clean)
	base := filepath.Base(clean)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem != "" && filepath.Base(dir) != stem {
		return filepath.Join(dir, stem+".presets.json")
	}
	return filepath.Join(dir, "presets.json")
}

func userPresetPath(modelID string) (string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	baseDir := filepath.Join(cfgDir, "presets")

	var libID, relLocal string
	if l, r, ok := strings.Cut(modelID, "/"); ok {
		libID = l
		relLocal = r
	} else if l, r, ok := strings.Cut(modelID, "::"); ok {
		libID = l
		relLocal = r
	} else {
		libID = ""
		relLocal = modelID
	}

	safeLib := "_default"
	if strings.TrimSpace(libID) != "" {
		safeLib = sanitizeSlugForFilename(libID, "_default")
	}
	safeSlug := sanitizeSlugForFilename(relLocal, "model")
	sum := sha256.Sum256([]byte(modelID))
	hex8 := hex.EncodeToString(sum[:4])

	libDir := filepath.Join(baseDir, safeLib)
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(libDir, fmt.Sprintf("%s--%s.json", safeSlug, hex8)), nil
}

func readPresetFile(path string) (PresetFile, error) {
	empty := PresetFile{
		SchemaVersion: SchemaVersion,
		Presets:       map[string]PresetEntry{},
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		return PresetFile{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return PresetFile{}, fmt.Errorf("preset file %s is empty", path)
	}
	var rawObj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawObj); err != nil {
		return PresetFile{}, fmt.Errorf("invalid presets JSON in %s: %w", path, err)
	}
	if rawObj == nil {
		return PresetFile{}, fmt.Errorf("presets file %s must contain a JSON object", path)
	}

	var file PresetFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return PresetFile{}, fmt.Errorf("invalid presets JSON in %s: %w", path, err)
	}
	if file.SchemaVersion != SchemaVersion {
		return PresetFile{}, fmt.Errorf("unsupported presets schema_version %d in %s (expected %d)", file.SchemaVersion, path, SchemaVersion)
	}
	if file.Presets == nil {
		file.Presets = map[string]PresetEntry{}
	}
	return file, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := renameWithRetry(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func renameWithRetry(src, dst string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" && !os.IsPermission(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 15 * time.Millisecond)
	}
	return err
}

func writePresetFile(path string, file PresetFile) error {
	if file.Default != nil {
		file.Default.Verification = NormalizeVerification(file.Default.Verification)
		if file.Default.Verification == nil {
			file.Default = nil
		}
	}
	delete(file.Presets, "default")

	if file.Default == nil && len(file.Presets) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	file.SchemaVersion = SchemaVersion
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
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

func buildPresetList(projFile, userFile PresetFile, defaults map[string]any, defaultMeta *DefaultMeta) []Preset {
	byKey := make(map[string]Preset)
	var defaultVerification *Verification

	if projFile.Default != nil {
		defaultVerification = NormalizeVerification(projFile.Default.Verification)
	}

	for key, entry := range projFile.Presets {
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
			Source:       "project",
			Verification: NormalizeVerification(entry.Verification),
			Params:       FilterParams(entry.Params, defaults),
		}
	}

	for key, entry := range userFile.Presets {
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

// ListStrict returns the synthesized default preset followed by project and user presets,
// returning an explicit error if either preset file is malformed or has an unsupported schema version.
func ListStrict(modelID, modelDir string, defaults map[string]any, defaultMeta *DefaultMeta) ([]Preset, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	var projFile PresetFile
	if projPath := ProjectPresetPath(modelID, modelDir); projPath != "" {
		f, err := readPresetFile(projPath)
		if err != nil {
			return nil, err
		}
		projFile = f
	}

	var userFile PresetFile
	if upath, err := userPresetPath(modelID); err == nil {
		f, rerr := readPresetFile(upath)
		if rerr != nil {
			return nil, rerr
		}
		userFile = f
	}

	return buildPresetList(projFile, userFile, defaults, defaultMeta), nil
}

// List returns the synthesized default preset followed by project and user presets.
// If a preset file is malformed, List logs the error and falls back to any valid scope
// without touching files on disk.
func List(modelID, modelDir string, defaults map[string]any, defaultMeta *DefaultMeta) []Preset {
	storeMu.Lock()
	defer storeMu.Unlock()

	var projFile PresetFile
	if projPath := ProjectPresetPath(modelID, modelDir); projPath != "" {
		f, err := readPresetFile(projPath)
		if err != nil {
			log.Printf("presets.List(%q): %v", modelID, err)
		} else {
			projFile = f
		}
	}

	var userFile PresetFile
	if upath, err := userPresetPath(modelID); err == nil {
		f, rerr := readPresetFile(upath)
		if rerr != nil {
			log.Printf("presets.List(%q): %v", modelID, rerr)
		} else {
			userFile = f
		}
	}

	return buildPresetList(projFile, userFile, defaults, defaultMeta)
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

// Save writes a preset (sparse diff or full snapshot) to either "project" (presets.json / <stem>.presets.json)
// or "user" (<ConfigDir>/presets/...). If a preset with the same name (or slugified key) already exists, its
// existing key is preserved and updated in-place rather than creating a duplicate entry. Saving to
// "project" also removes any identically keyed or identically named user preset so a stale local
// copy cannot silently shadow or duplicate the shared project preset.
func Save(modelID, modelDir, label, description, scope string, fullSnapshot bool, currentParams, defaults map[string]any, verification *Verification) (string, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	trimmedLabel := strings.TrimSpace(label)
	projPath := ProjectPresetPath(modelID, modelDir)
	isProject := scope == "project" && projPath != ""

	var projFile PresetFile
	var projErr error
	if projPath != "" {
		projFile, projErr = readPresetFile(projPath)
		if isProject && projErr != nil {
			return "", projErr
		}
	}

	userPath, userPathErr := userPresetPath(modelID)
	if !isProject && userPathErr != nil {
		return "", userPathErr
	}
	var userFile PresetFile
	var userReadErr error
	if userPathErr == nil {
		userFile, userReadErr = readPresetFile(userPath)
		if !isProject && userReadErr != nil {
			return "", userReadErr
		}
	}

	var key string
	if isProject {
		if k, ok := findMatchingKey(projFile.Presets, trimmedLabel); ok {
			key = k
		} else if userReadErr == nil {
			if k, ok := findMatchingKey(userFile.Presets, trimmedLabel); ok {
				key = k
			} else {
				key = Slugify(trimmedLabel)
			}
		} else {
			key = Slugify(trimmedLabel)
		}
	} else {
		if k, ok := findMatchingKey(userFile.Presets, trimmedLabel); ok {
			key = k
		} else if projErr == nil {
			if k, ok := findMatchingKey(projFile.Presets, trimmedLabel); ok {
				key = k
			} else {
				key = Slugify(trimmedLabel)
			}
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
		if projFile.Presets == nil {
			projFile.Presets = map[string]PresetEntry{}
		}
		projFile.Presets[key] = entry
		if err := writePresetFile(projPath, projFile); err != nil {
			return "", err
		}

		if userPathErr == nil && userReadErr == nil && len(userFile.Presets) > 0 {
			changed := false
			for uk, ue := range userFile.Presets {
				if uk == "default" {
					continue
				}
				if uk == key || strings.EqualFold(strings.TrimSpace(ue.Label), trimmedLabel) {
					delete(userFile.Presets, uk)
					changed = true
				}
			}
			if changed {
				_ = writePresetFile(userPath, userFile)
			}
		}
	} else {
		if userFile.Presets == nil {
			userFile.Presets = map[string]PresetEntry{}
		}
		userFile.Presets[key] = entry
		if err := writePresetFile(userPath, userFile); err != nil {
			return "", err
		}
	}

	return key, nil
}

// SetVerification updates verification metadata for a preset in its owning scope.
// For the canonical "default" preset, verification is stored in the top-level "default"
// section of the project preset file and removed when cleared.
func SetVerification(modelID, modelDir, key, scope string, v *Verification) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("preset key is required")
	}

	norm := NormalizeVerification(v)

	if key == "default" {
		targetPath := ProjectPresetPath(modelID, modelDir)
		if targetPath == "" {
			return errors.New("project model directory is unavailable")
		}
		existing, err := readPresetFile(targetPath)
		if err != nil {
			return err
		}
		if norm == nil {
			existing.Default = nil
		} else {
			existing.Default = &DefaultSection{
				Verification: norm,
			}
		}
		return writePresetFile(targetPath, existing)
	}

	var targetPath string
	if scope == "project" {
		targetPath = ProjectPresetPath(modelID, modelDir)
		if targetPath == "" {
			return errors.New("project model directory is unavailable")
		}
	} else {
		p, err := userPresetPath(modelID)
		if err != nil {
			return err
		}
		targetPath = p
	}

	existing, err := readPresetFile(targetPath)
	if err != nil {
		return err
	}
	entry, ok := existing.Presets[key]
	if !ok {
		return errors.New("preset not found in target scope")
	}
	entry.Verification = norm
	existing.Presets[key] = entry
	return writePresetFile(targetPath, existing)
}

func downgradeDefaultVerificationAtPathLocked(targetPath string) error {
	if targetPath == "" {
		return nil
	}
	existing, err := readPresetFile(targetPath)
	if err != nil {
		return err
	}
	if existing.Default == nil {
		return nil
	}
	norm := NormalizeVerification(existing.Default.Verification)
	if norm == nil || norm.Status == StatusUnverified {
		return nil
	}
	if norm.Note == "" {
		existing.Default = nil
	} else {
		existing.Default = &DefaultSection{
			Verification: &Verification{
				Status: StatusUnverified,
				Note:   norm.Note,
			},
		}
	}
	return writePresetFile(targetPath, existing)
}

// DowngradeDefaultVerification resets the canonical "default" preset's verification status
// in <modelDir>/presets.json to "unverified" while preserving any print note.
func DowngradeDefaultVerification(modelDir string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	if strings.TrimSpace(modelDir) == "" {
		return nil
	}
	return downgradeDefaultVerificationAtPathLocked(filepath.Join(filepath.Clean(modelDir), "presets.json"))
}

// DowngradeDefaultVerificationForFile resets the canonical "default" preset's verification status
// in the project preset file corresponding to pyPath (presets.json or <stem>.presets.json).
func DowngradeDefaultVerificationForFile(pyPath string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	return downgradeDefaultVerificationAtPathLocked(ProjectPresetPathForScript(pyPath))
}

// Delete removes a preset key from either "project" or "user" storage.
func Delete(modelID, modelDir, key, scope string) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	key = strings.TrimSpace(key)
	if key == "" || key == "default" {
		return errors.New("cannot delete canonical default preset")
	}

	var targetPath string
	if scope == "project" {
		targetPath = ProjectPresetPath(modelID, modelDir)
		if targetPath == "" {
			return errors.New("project model directory is unavailable")
		}
	} else {
		p, err := userPresetPath(modelID)
		if err != nil {
			return err
		}
		targetPath = p
	}

	existing, err := readPresetFile(targetPath)
	if err != nil {
		return err
	}
	if _, ok := existing.Presets[key]; !ok {
		return errors.New("preset not found in target scope")
	}
	delete(existing.Presets, key)
	return writePresetFile(targetPath, existing)
}

// Rename updates a preset's label (and optional description) within its scope,
// migrating its JSON key to the slugified new label while preserving its params.
func Rename(modelID, modelDir, oldKey, scope, newLabel, newDescription string) (string, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

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
		targetPath = ProjectPresetPath(modelID, modelDir)
		if targetPath == "" {
			return "", errors.New("project model directory is unavailable")
		}
	} else {
		p, err := userPresetPath(modelID)
		if err != nil {
			return "", err
		}
		targetPath = p
	}

	existing, err := readPresetFile(targetPath)
	if err != nil {
		return "", err
	}
	entry, ok := existing.Presets[oldKey]
	if !ok {
		return "", errors.New("preset not found in target scope")
	}

	newKey := oldKey
	if !strings.EqualFold(strings.TrimSpace(entry.Label), trimmedLabel) {
		newKey = Slugify(trimmedLabel)
	}
	for k, e := range existing.Presets {
		if k == oldKey || k == "default" {
			continue
		}
		if k == newKey || strings.EqualFold(strings.TrimSpace(e.Label), trimmedLabel) {
			return "", errors.New("a preset with that name already exists")
		}
	}
	if newKey != oldKey {
		delete(existing.Presets, oldKey)
	}

	entry.Label = trimmedLabel
	entry.Description = strings.TrimSpace(newDescription)
	existing.Presets[newKey] = entry

	if err := writePresetFile(targetPath, existing); err != nil {
		return "", err
	}
	return newKey, nil
}
