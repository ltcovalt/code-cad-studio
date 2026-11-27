package worker

import "encoding/json"

// LibrarySpec identifies a model library configured on the Python worker.
type LibrarySpec struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type HealthResponse struct {
	OK                   bool          `json:"ok"`
	Python               string        `json:"python"`
	PythonVersion        string        `json:"python_version,omitempty"`
	Build123d            bool          `json:"build123d"`
	Build123dVersion     string        `json:"build123d_version,omitempty"`
	Build123dError       string        `json:"build123d_error"`
	OCPTessellate        bool          `json:"ocp_tessellate"`
	OCPTessellateVersion string        `json:"ocp_tessellate_version,omitempty"`
	Libraries            []LibrarySpec `json:"libraries"`
}

type ModelInfo struct {
	ID           string   `json:"id"`
	LocalID      string   `json:"local_id"`
	LibraryID    string   `json:"library_id"`
	LibraryName  string   `json:"library_name"`
	Category     string   `json:"category,omitempty"`
	Folders      []string `json:"folders"`
	FolderLabels []string `json:"folder_labels"`
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	RelPath      string   `json:"rel_path"`
	PresetsPath  string   `json:"presets_path,omitempty"`
	Description  string   `json:"description"`
	ParamsClass  string   `json:"params_class"`
	Entrypoint   string   `json:"entrypoint"`
	Kind         string   `json:"kind,omitempty"`
	ParseError   string   `json:"parse_error,omitempty"`
	Dir          string   `json:"dir"`
}

type ModelsResponse struct {
	OK            bool              `json:"ok"`
	Models        []ModelInfo       `json:"models"`
	LibraryErrors map[string]string `json:"library_errors,omitempty"`
	Error         string            `json:"error,omitempty"`
}

type ParamField struct {
	Name          string         `json:"name"`
	Path          string         `json:"path"`
	Label         string         `json:"label"`
	Type          string         `json:"type"` // bool, int, float, str, enum, group, list
	Default       any            `json:"default"`
	GroupDefaults map[string]any `json:"group_defaults,omitempty"`
	ComponentID     string         `json:"component_id,omitempty"`
	ComponentName   string         `json:"component_name,omitempty"`
	ComponentPreset string         `json:"component_preset,omitempty"`
	Description   string         `json:"description"`
	Unit          string         `json:"unit,omitempty"`
	Nullable      bool           `json:"nullable,omitempty"`
	When          map[string]any `json:"when,omitempty"`
	Choices       []string       `json:"choices,omitempty"`
	Min           *float64       `json:"min,omitempty"`
	Max           *float64       `json:"max,omitempty"`
	Step          *float64       `json:"step,omitempty"`
	Children      []ParamField   `json:"children,omitempty"`
	Item          *ParamField    `json:"item,omitempty"`
}

type DefaultPresetMeta struct {
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type SchemaResponse struct {
	OK            bool               `json:"ok"`
	Model         ModelInfo          `json:"model"`
	Description   string             `json:"description"`
	Fields        []ParamField       `json:"fields"`
	Defaults      map[string]any     `json:"defaults"`
	DefaultPreset *DefaultPresetMeta `json:"default_preset,omitempty"`
	Error         string             `json:"error,omitempty"`
	Traceback     string             `json:"traceback,omitempty"`
}

type BBox struct {
	XMin float64 `json:"xmin"`
	XMax float64 `json:"xmax"`
	YMin float64 `json:"ymin"`
	YMax float64 `json:"ymax"`
	ZMin float64 `json:"zmin"`
	ZMax float64 `json:"zmax"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
	DZ   float64 `json:"dz"`
}

type RenderResponse struct {
	OK               bool            `json:"ok"`
	Superseded       bool            `json:"superseded,omitempty"`
	Cached           bool            `json:"cached,omitempty"`
	ModelID          string          `json:"model_id"`
	BuildTimeMs      float64         `json:"build_time_ms"`
	TessellateTimeMs float64         `json:"tessellate_time_ms"`
	BBox             *BBox           `json:"bbox,omitempty"`
	Shapes           json.RawMessage `json:"shapes,omitempty"`
	ExportFilename   string          `json:"export_filename,omitempty"`
	Error            string          `json:"error,omitempty"`
	Traceback        string          `json:"traceback,omitempty"`
}

type ExportNameResponse struct {
	OK             bool   `json:"ok"`
	ExportFilename string `json:"export_filename,omitempty"`
	LibraryID      string `json:"library_id,omitempty"`
	Error          string `json:"error,omitempty"`
}

type ExportResponse struct {
	OK        bool   `json:"ok"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Error     string `json:"error,omitempty"`
	Traceback string `json:"traceback,omitempty"`
}

// ExportOptions configures CAD export format, destination, and tessellation quality.
type ExportOptions struct {
	Format            string   `json:"format"`
	OutputPath        string   `json:"output_path,omitempty"`
	Quality           string   `json:"quality,omitempty"`
	LinearDeflection  *float64 `json:"linear_deflection,omitempty"`
	AngularDeflection *float64 `json:"angular_deflection,omitempty"`
}

type DefaultChange struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	OldValue   any    `json:"old_value"`
	NewValue   any    `json:"new_value"`
	OldLiteral string `json:"old_literal"`
	NewLiteral string `json:"new_literal"`
}

type UnsupportedDefaultChange struct {
	Path          string `json:"path"`
	Reason        string `json:"reason"`
	ReasonCode    string `json:"reason_code,omitempty"`
	OwnerClass    string `json:"owner_class,omitempty"`
	SourceFile    string `json:"source_file,omitempty"`
	SourceRelPath string `json:"source_rel_path,omitempty"`
	InLibrary     bool   `json:"in_library,omitempty"`
}

type DefaultPresetUpdate struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type DefaultPresetInfo struct {
	Label             string `json:"label"`
	Description       string `json:"description"`
	CanEdit           bool   `json:"can_edit"`
	UnsupportedReason string `json:"unsupported_reason,omitempty"`
}

type DefaultPresetChange struct {
	Action         string `json:"action"`
	Line           int    `json:"line"`
	OldLabel       string `json:"old_label"`
	NewLabel       string `json:"new_label"`
	OldDescription string `json:"old_description"`
	NewDescription string `json:"new_description"`
}

type UpdateDefaultsResponse struct {
	OK                   bool                       `json:"ok"`
	File                 string                     `json:"file,omitempty"`
	RelPath              string                     `json:"rel_path,omitempty"`
	SHA256               string                     `json:"sha256,omitempty"`
	Updated              int                        `json:"updated,omitempty"`
	DefaultPreset        *DefaultPresetInfo         `json:"default_preset,omitempty"`
	DefaultPresetChange  *DefaultPresetChange       `json:"default_preset_change,omitempty"`
	DefaultPresetUpdated bool                       `json:"default_preset_updated,omitempty"`
	Changes              []DefaultChange            `json:"changes"`
	Unsupported          []UnsupportedDefaultChange `json:"unsupported"`
	Error                string                     `json:"error,omitempty"`
	Traceback            string                     `json:"traceback,omitempty"`
}

type RenameModelResponse struct {
	OK         bool   `json:"ok"`
	OldModelID string `json:"old_model_id,omitempty"`
	ModelID    string `json:"model_id,omitempty"`
	Name       string `json:"name,omitempty"`
	RelPath    string `json:"rel_path,omitempty"`
	Path       string `json:"path,omitempty"`
	Changed    bool   `json:"changed"`
	Error      string `json:"error,omitempty"`
	Traceback  string `json:"traceback,omitempty"`
}
