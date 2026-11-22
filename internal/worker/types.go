package worker

import "encoding/json"

type HealthResponse struct {
	OK             bool   `json:"ok"`
	Python         string `json:"python"`
	Build123d      bool   `json:"build123d"`
	Build123dError string `json:"build123d_error"`
	OCPTessellate  bool   `json:"ocp_tessellate"`
	ProjectRoot    string `json:"project_root"`
}

type ModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	RelPath     string `json:"rel_path"`
	Description string `json:"description"`
	ParamsClass string `json:"params_class"`
	Entrypoint  string `json:"entrypoint"`
	Dir         string `json:"dir"`
}

type ModelsResponse struct {
	OK     bool        `json:"ok"`
	Models []ModelInfo `json:"models"`
	Error  string      `json:"error,omitempty"`
}

type ParamField struct {
	Name        string         `json:"name"`
	Path        string         `json:"path"`
	Label       string         `json:"label"`
	Type        string         `json:"type"` // bool, int, float, str, enum, group
	Default     any            `json:"default"`
	Description string         `json:"description"`
	Unit        string         `json:"unit,omitempty"`
	Nullable    bool           `json:"nullable,omitempty"`
	When        map[string]any `json:"when,omitempty"`
	Choices     []string       `json:"choices,omitempty"`
	Min         *float64       `json:"min,omitempty"`
	Max         *float64       `json:"max,omitempty"`
	Step        *float64       `json:"step,omitempty"`
	Children    []ParamField   `json:"children,omitempty"`
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
	Error          string `json:"error,omitempty"`
}

type ExportResponse struct {
	OK        bool   `json:"ok"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Error     string `json:"error,omitempty"`
	Traceback string `json:"traceback,omitempty"`
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
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type UpdateDefaultsResponse struct {
	OK          bool                       `json:"ok"`
	File        string                     `json:"file,omitempty"`
	RelPath     string                     `json:"rel_path,omitempty"`
	SHA256      string                     `json:"sha256,omitempty"`
	Updated     int                        `json:"updated,omitempty"`
	Changes     []DefaultChange            `json:"changes"`
	Unsupported []UnsupportedDefaultChange `json:"unsupported"`
	Error       string                     `json:"error,omitempty"`
	Traceback   string                     `json:"traceback,omitempty"`
}
