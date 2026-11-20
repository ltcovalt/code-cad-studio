package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

func TestMiddlewarePreservesPathValue(t *testing.T) {
	var gotID string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hx/models/{id}", func(w http.ResponseWriter, r *http.Request) {
		gotID = r.PathValue("id")
		w.WriteHeader(http.StatusOK)
	})
	s := &Server{mux: mux}
	wrapped := s.Middleware(http.NotFoundHandler())

	req := httptest.NewRequest(http.MethodGet, "/hx/models/c_clip_spacer", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotID != "c_clip_spacer" {
		t.Fatalf("expected PathValue(id)=c_clip_spacer, got %q", gotID)
	}
}

func TestParseFormParamsAndExplicitNestedGroups(t *testing.T) {
	fields := []worker.ParamField{
		{Name: "shaft_diameter", Path: "shaft_diameter", Label: "Shaft Diameter", Type: "float", Default: 10.0},
		{
			Name:  "flutes",
			Path:  "flutes",
			Label: "Flutes",
			Type:  "group",
			Children: []worker.ParamField{
				{Name: "enabled", Path: "flutes.enabled", Label: "Enabled", Type: "bool", Default: true},
				{Name: "count", Path: "flutes.count", Label: "Count", Type: "int", Default: 12},
			},
		},
		{
			Name:  "base",
			Path:  "base",
			Label: "Base",
			Type:  "group",
			Children: []worker.ParamField{
				{Name: "flange_diameter", Path: "base.flange_diameter", Label: "Flange Diameter", Type: "float", Default: 32.0},
			},
		},
	}

	form := url.Values{
		"param:shaft_diameter":       {"12.7"},
		"param:flutes.enabled":       {"false"}, // unchecked checkbox with hidden sentinel
		"param:flutes.count":         {"16"},
		"param:base.flange_diameter": {"36.5"},
	}

	parsed := ParseFormParams(form, fields)
	if parsed["shaft_diameter"] != 12.7 {
		t.Fatalf("expected 12.7, got %#v", parsed["shaft_diameter"])
	}
	flutes, ok := parsed["flutes"].(map[string]any)
	if !ok || flutes["enabled"] != false || flutes["count"] != 16 {
		t.Fatalf("unexpected nested flutes: %#v", parsed["flutes"])
	}
	base, ok := parsed["base"].(map[string]any)
	if !ok || base["flange_diameter"] != 36.5 {
		t.Fatalf("unexpected nested base: %#v", parsed["base"])
	}

	// Checked checkbox sends ["false", "true"]
	form["param:flutes.enabled"] = []string{"false", "true"}
	parsed2 := ParseFormParams(form, fields)
	if parsed2["flutes"].(map[string]any)["enabled"] != true {
		t.Fatalf("expected true for checked checkbox, got %#v", parsed2["flutes"])
	}

	// Verify SplitFieldViews + ToggleChild promotion
	views := BuildFieldViews(fields, parsed)
	core, nested := SplitFieldViews(views)
	if len(core) != 1 || core[0].Name != "shaft_diameter" {
		t.Fatalf("expected 1 core field, got %#v", core)
	}
	if len(nested) != 2 {
		t.Fatalf("expected 2 nested groups, got %d", len(nested))
	}
	if nested[0].ToggleChild == nil || nested[0].ToggleChild.Path != "flutes.enabled" || nested[0].ToggleChild.ValueBool != false {
		t.Fatalf("expected ToggleChild on flutes group, got %#v", nested[0].ToggleChild)
	}
	if len(nested[0].Children) != 1 || nested[0].Children[0].Name != "count" {
		t.Fatalf("expected enabled excluded from flutes children, got %#v", nested[0].Children)
	}
	if nested[1].ToggleChild != nil {
		t.Fatalf("base group without enabled bool should not have ToggleChild")
	}
}
