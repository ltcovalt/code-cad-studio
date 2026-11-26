package server

import (
	"context"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/presets"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

// ComponentPresetBundle packages a referenced component/model's presets and defaults
// for parent model preset resolution and inline component preset pickers.
type ComponentPresetBundle struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Defaults     map[string]any      `json:"defaults"`
	Fields       []worker.ParamField `json:"fields,omitempty"`
	Presets      []presets.Preset    `json:"presets"`
	PresetsError string              `json:"presets_error,omitempty"`
}

func deepCopyAny(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyAnyMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = deepCopyAny(item)
		}
		return out
	default:
		return v
	}
}

func deepCopyAnyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopyAny(v)
	}
	return dst
}

// buildSchemaShape returns a defaults map where any nil nullable groups are populated
// with their schema group_defaults so FilterParams/SparseDiff know all valid nested keys.
func buildSchemaShape(defaults map[string]any, fields []worker.ParamField) map[string]any {
	shape := deepCopyAnyMap(defaults)
	applyFieldGroupDefaults(shape, fields)
	return shape
}

func applyFieldGroupDefaults(target map[string]any, fields []worker.ParamField) {
	for _, f := range fields {
		if f.Type != "group" {
			continue
		}
		var sub map[string]any
		if existing, ok := target[f.Name].(map[string]any); ok {
			sub = existing
		} else if len(f.GroupDefaults) > 0 {
			sub = deepCopyAnyMap(f.GroupDefaults)
			target[f.Name] = sub
		} else {
			sub = map[string]any{}
			target[f.Name] = sub
		}
		if len(f.Children) > 0 {
			applyFieldGroupDefaults(sub, f.Children)
		}
	}
}

func collectComponentLinks(fields []worker.ParamField) map[string]worker.ParamField {
	links := map[string]worker.ParamField{}
	var walk func(list []worker.ParamField)
	walk = func(list []worker.ParamField) {
		for _, f := range list {
			if f.Type == "group" {
				if strings.TrimSpace(f.ComponentID) != "" {
					links[f.Path] = f
				}
				if len(f.Children) > 0 {
					walk(f.Children)
				}
			}
		}
	}
	walk(fields)
	return links
}

func (s *Server) loadComponentBundles(
	ctx context.Context,
	rootModelID string,
	fields []worker.ParamField,
) map[string]ComponentPresetBundle {
	bundles := map[string]ComponentPresetBundle{}
	visited := map[string]bool{rootModelID: true}

	var walk func(list []worker.ParamField)
	walk = func(list []worker.ParamField) {
		for _, f := range list {
			if f.Type != "group" {
				continue
			}
			compID := strings.TrimSpace(f.ComponentID)
			if compID != "" && !visited[compID] {
				visited[compID] = true
				compSchema, err := s.worker.GetSchema(ctx, compID)
				if err == nil && compSchema != nil && compSchema.OK {
					compShape := buildSchemaShape(compSchema.Defaults, compSchema.Fields)
					compPresets, pErr := presets.ListStrict(
						compID,
						compSchema.Model.Dir,
						compShape,
						schemaDefaultPresetMeta(compSchema),
					)
					presetsErrMsg := ""
					if pErr != nil {
						presetsErrMsg = pErr.Error()
						compPresets = presets.List(
							compID,
							compSchema.Model.Dir,
							compShape,
							schemaDefaultPresetMeta(compSchema),
						)
					}
					compDefaults := compSchema.Defaults
					if compDefaults == nil {
						compDefaults = map[string]any{}
					}
					bundles[compID] = ComponentPresetBundle{
						ID:           compID,
						Name:         compSchema.Model.Name,
						Defaults:     compDefaults,
						Fields:       compSchema.Fields,
						Presets:      compPresets,
						PresetsError: presetsErrMsg,
					}
					walk(compSchema.Fields)
				}
			}
			if len(f.Children) > 0 {
				walk(f.Children)
			}
		}
	}
	walk(fields)
	return bundles
}

func getValueAtPath(root map[string]any, path string) (any, bool) {
	if root == nil || path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[p]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func setValueAtPath(root map[string]any, path string, val any) {
	if root == nil || path == "" {
		return
	}
	parts := strings.Split(path, ".")
	cur := root
	for i := 0; i < len(parts)-1; i++ {
		next, ok := cur[parts[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[parts[i]] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = val
}

func resolveComponentPresetParams(
	compID string,
	presetKey string,
	fallbackDefaults map[string]any,
	bundles map[string]ComponentPresetBundle,
	visited map[string]bool,
) map[string]any {
	bundle, ok := bundles[compID]
	if !ok {
		return deepCopyAnyMap(fallbackDefaults)
	}
	baseDefaults := bundle.Defaults
	if len(baseDefaults) == 0 {
		baseDefaults = fallbackDefaults
	}
	if presetKey == "" || presetKey == "default" {
		return deepCopyAnyMap(baseDefaults)
	}
	if visited[compID] {
		return deepCopyAnyMap(baseDefaults)
	}
	var matched *presets.Preset
	for i := range bundle.Presets {
		if bundle.Presets[i].Key == presetKey {
			matched = &bundle.Presets[i]
			break
		}
	}
	if matched == nil {
		return deepCopyAnyMap(baseDefaults)
	}
	nextVisited := make(map[string]bool, len(visited)+1)
	for k, v := range visited {
		nextVisited[k] = v
	}
	nextVisited[compID] = true
	return resolvePresetParams(baseDefaults, bundle.Fields, *matched, bundles, nextVisited)
}

func resolvePresetParams(
	defaults map[string]any,
	fields []worker.ParamField,
	preset presets.Preset,
	bundles map[string]ComponentPresetBundle,
	visited map[string]bool,
) map[string]any {
	resolved := deepCopyAnyMap(defaults)
	applyComponentPresetLayer(resolved, fields, preset.ComponentPresets, preset.Params, bundles, visited)
	return resolved
}

func applyComponentPresetLayer(
	target map[string]any,
	fields []worker.ParamField,
	compPresets map[string]string,
	overrides map[string]any,
	bundles map[string]ComponentPresetBundle,
	visited map[string]bool,
) {
	for _, f := range fields {
		var overrideVal any
		hasOverride := false
		if overrides != nil {
			overrideVal, hasOverride = overrides[f.Name]
		}

		if f.Type == "group" {
			if hasOverride && overrideVal == nil {
				target[f.Name] = nil
				continue
			}

			compKey := ""
			if f.ComponentID != "" && compPresets != nil {
				compKey = strings.TrimSpace(compPresets[f.Path])
			}
			overrideMap, overrideIsMap := overrideVal.(map[string]any)

			if compKey != "" {
				var fallback map[string]any
				if len(f.GroupDefaults) > 0 {
					fallback = f.GroupDefaults
				} else if existing, ok := target[f.Name].(map[string]any); ok {
					fallback = existing
				}
				base := resolveComponentPresetParams(f.ComponentID, compKey, fallback, bundles, visited)
				if hasOverride && overrideIsMap {
					applyComponentPresetLayer(base, f.Children, nil, overrideMap, bundles, visited)
				}
				target[f.Name] = base
				continue
			}

			if hasOverride && overrideIsMap {
				var base map[string]any
				if existing, ok := target[f.Name].(map[string]any); ok {
					base = deepCopyAnyMap(existing)
				} else if len(f.GroupDefaults) > 0 {
					base = deepCopyAnyMap(f.GroupDefaults)
				} else {
					base = map[string]any{}
				}
				applyComponentPresetLayer(base, f.Children, compPresets, overrideMap, bundles, visited)
				target[f.Name] = base
				continue
			}

			if existing, ok := target[f.Name].(map[string]any); ok {
				applyComponentPresetLayer(existing, f.Children, compPresets, nil, bundles, visited)
			}
			continue
		}

		if hasOverride {
			target[f.Name] = deepCopyAny(overrideVal)
		}
	}
}

// normalizeModelComponentPresets filters component_presets to valid component-backed groups
// that are currently enabled, omitting redundant "default" entries when the model default is already enabled.
func normalizeModelComponentPresets(
	raw map[string]string,
	currentParams map[string]any,
	modelDefaults map[string]any,
	fields []worker.ParamField,
) map[string]string {
	links := collectComponentLinks(fields)
	if len(links) == 0 {
		return nil
	}
	out := map[string]string{}
	for path, f := range links {
		curVal, hasCur := getValueAtPath(currentParams, path)
		if hasCur && curVal == nil {
			// Disabled nullable component group should not carry a component_presets reference.
			continue
		}
		defVal, hasDef := getValueAtPath(modelDefaults, path)
		defaultIsNil := hasDef && defVal == nil

		rawKey := ""
		if raw != nil {
			rawKey = strings.TrimSpace(raw[path])
		}
		if rawKey == "" {
			if defaultIsNil && hasCur && curVal != nil {
				// Group is disabled by default in the model, so store explicit "default" to record activation.
				out[path] = "default"
			}
			continue
		}
		if rawKey == "default" && !defaultIsNil {
			// Implicit default for an already-enabled component group; omit to keep presets.json clean.
			continue
		}
		if f.ComponentID != "" {
			out[path] = rawKey
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildEffectiveSaveDefaults overlays each selected component preset's resolved params onto
// modelDefaults so SparseDiff records only per-field overrides beyond the referenced component preset.
func buildEffectiveSaveDefaults(
	modelDefaults map[string]any,
	fields []worker.ParamField,
	compPresets map[string]string,
	bundles map[string]ComponentPresetBundle,
) map[string]any {
	effective := deepCopyAnyMap(modelDefaults)
	if len(compPresets) == 0 {
		return effective
	}
	links := collectComponentLinks(fields)
	for path, compKey := range compPresets {
		f, ok := links[path]
		if !ok || f.ComponentID == "" {
			continue
		}
		var fallback map[string]any
		if len(f.GroupDefaults) > 0 {
			fallback = f.GroupDefaults
		} else if existing, ok := getValueAtPath(effective, path); ok {
			if m, mok := existing.(map[string]any); mok {
				fallback = m
			}
		}
		resolved := resolveComponentPresetParams(f.ComponentID, compKey, fallback, bundles, map[string]bool{})
		setValueAtPath(effective, path, resolved)
	}
	return effective
}
