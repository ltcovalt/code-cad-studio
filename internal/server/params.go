package server

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

// ParseFormParams converts HTML form values into a typed nested map matching schema.Fields.
// Checkboxes use hidden sentinel inputs or presence checks so unchecked bools become false.
func ParseFormParams(form url.Values, fields []worker.ParamField) map[string]any {
	out := make(map[string]any)
	for _, f := range fields {
		if f.Type == "group" {
			out[f.Name] = ParseFormParams(form, f.Children)
			continue
		}

		key := "param:" + f.Path
		vals, exists := form[key]

		switch f.Type {
		case "bool":
			// If a checkbox is paired with a hidden input value="false", vals may be ["false", "true"]
			checked := false
			for _, v := range vals {
				lv := strings.ToLower(strings.TrimSpace(v))
				if lv == "true" || lv == "on" || lv == "1" {
					checked = true
				}
			}
			out[f.Name] = checked

		case "int":
			if !exists || len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
				out[f.Name] = f.Default
				continue
			}
			if iv, err := strconv.Atoi(strings.TrimSpace(vals[0])); err == nil {
				out[f.Name] = iv
			} else if fv, err := strconv.ParseFloat(strings.TrimSpace(vals[0]), 64); err == nil {
				out[f.Name] = int(fv)
			} else {
				out[f.Name] = f.Default
			}

		case "float":
			if !exists || len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
				out[f.Name] = f.Default
				continue
			}
			if fv, err := strconv.ParseFloat(strings.TrimSpace(vals[0]), 64); err == nil {
				out[f.Name] = fv
			} else {
				out[f.Name] = f.Default
			}

		default: // str, enum
			if !exists || len(vals) == 0 {
				out[f.Name] = f.Default
			} else {
				out[f.Name] = vals[0]
			}
		}
	}
	return out
}

// FormFieldView is a template-friendly view of a ParamField with its current resolved value.
type FormFieldView struct {
	worker.ParamField
	ValueStr    string
	ValueBool   bool
	StepStr     string
	MinStr      string
	MaxStr      string
	HasRange    bool
	ToggleChild *FormFieldView
	Children    []FormFieldView
}

// BuildFieldViews binds current values onto the schema fields for HTML template rendering.
func BuildFieldViews(fields []worker.ParamField, values map[string]any) []FormFieldView {
	out := make([]FormFieldView, 0, len(fields))
	for _, f := range fields {
		v := FormFieldView{ParamField: f}

		rawVal, ok := values[f.Name]
		if !ok {
			rawVal = f.Default
		}

		if f.Type == "group" {
			subMap, _ := rawVal.(map[string]any)
			if subMap == nil {
				subMap = map[string]any{}
			}
			children := BuildFieldViews(f.Children, subMap)
			filtered := make([]FormFieldView, 0, len(children))
			for i := range children {
				if children[i].Name == "enabled" && children[i].Type == "bool" && v.ToggleChild == nil {
					tc := children[i]
					v.ToggleChild = &tc
					continue
				}
				filtered = append(filtered, children[i])
			}
			v.Children = filtered
			out = append(out, v)
			continue
		}

		switch f.Type {
		case "bool":
			if b, ok := rawVal.(bool); ok {
				v.ValueBool = b
			}
			v.ValueStr = strconv.FormatBool(v.ValueBool)
		case "int":
			v.ValueStr = formatNumber(rawVal, true)
			if f.Step != nil {
				v.StepStr = formatFloat(*f.Step)
			} else {
				v.StepStr = "1"
			}
		case "float":
			v.ValueStr = formatNumber(rawVal, false)
			if f.Step != nil {
				v.StepStr = formatFloat(*f.Step)
			} else {
				v.StepStr = "0.1"
			}
		default:
			if rawVal != nil {
				v.ValueStr = fmt.Sprint(rawVal)
			}
		}

		if f.Min != nil {
			v.MinStr = formatFloat(*f.Min)
		}
		if f.Max != nil {
			v.MaxStr = formatFloat(*f.Max)
		}
		v.HasRange = f.Min != nil && f.Max != nil

		out = append(out, v)
	}
	return out
}

// SplitFieldViews separates ungrouped top-level fields (Core Parameters) from explicit nested dataclass groups.
func SplitFieldViews(views []FormFieldView) (coreFields []FormFieldView, nestedGroups []FormFieldView) {
	for _, fv := range views {
		if fv.Type == "group" {
			nestedGroups = append(nestedGroups, fv)
		} else {
			coreFields = append(coreFields, fv)
		}
	}
	return coreFields, nestedGroups
}

func formatNumber(v any, asInt bool) string {
	switch n := v.(type) {
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		if asInt {
			return strconv.Itoa(int(n))
		}
		return formatFloat(n)
	case float32:
		if asInt {
			return strconv.Itoa(int(n))
		}
		return formatFloat(float64(n))
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
