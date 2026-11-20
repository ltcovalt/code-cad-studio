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
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	Label       string       `json:"label"`
	Type        string       `json:"type"` // bool, int, float, str, enum, group
	Default     any          `json:"default"`
	Description string       `json:"description"`
	Choices     []string     `json:"choices,omitempty"`
	Min         *float64     `json:"min,omitempty"`
	Max         *float64     `json:"max,omitempty"`
	Step        *float64     `json:"step,omitempty"`
	Children    []ParamField `json:"children,omitempty"`
}

type SchemaResponse struct {
	OK          bool           `json:"ok"`
	Model       ModelInfo      `json:"model"`
	Description string         `json:"description"`
	Fields      []ParamField   `json:"fields"`
	Defaults    map[string]any `json:"defaults"`
	Error       string         `json:"error,omitempty"`
	Traceback   string         `json:"traceback,omitempty"`
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
	ModelID          string          `json:"model_id"`
	BuildTimeMs      float64         `json:"build_time_ms"`
	TessellateTimeMs float64         `json:"tessellate_time_ms"`
	BBox             *BBox           `json:"bbox,omitempty"`
	Shapes           json.RawMessage `json:"shapes,omitempty"`
	Error            string          `json:"error,omitempty"`
	Traceback        string          `json:"traceback,omitempty"`
}

type ExportResponse struct {
	OK        bool   `json:"ok"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Error     string `json:"error,omitempty"`
	Traceback string `json:"traceback,omitempty"`
}
