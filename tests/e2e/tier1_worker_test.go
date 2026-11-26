//go:build e2e

package e2e_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTempModelLib(t *testing.T, files map[string]string) string {
	t.Helper()
	libDir := filepath.Join(t.TempDir(), "mylib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	for rel, src := range files {
		dst := filepath.Join(libDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dst, err)
		}
		if src != "" {
			if err := os.WriteFile(dst, []byte(src), 0o644); err != nil {
				t.Fatalf("write %s: %v", dst, err)
			}
		} else {
			// Copy from fixtures/models/<rel>
			data, err := os.ReadFile(fixturePath("models", rel))
			if err != nil {
				t.Fatalf("read fixture %s: %v", rel, err)
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				t.Fatalf("write fixture copy %s: %v", dst, err)
			}
		}
	}
	return libDir
}

func decodeSTLTriangleCount(t *testing.T, data []byte) int {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("STL payload is empty")
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("solid")) && bytes.Contains(trimmed, []byte("endsolid")) && bytes.Contains(trimmed, []byte("facet")) {
		return bytes.Count(trimmed, []byte("facet normal"))
	}
	if len(data) < 84 {
		t.Fatalf("binary STL too short: %d bytes", len(data))
	}
	count := int(binary.LittleEndian.Uint32(data[80:84]))
	expectedLen := 84 + count*50
	if len(data) != expectedLen {
		t.Fatalf("binary STL length %d != 84 + 50*%d (%d)", len(data), count, expectedLen)
	}
	return count
}

func countEdgesInRender(t *testing.T, renderResp map[string]any) int {
	t.Helper()
	shapes := jsonMap(t, renderResp["shapes"], "shapes")
	parts := jsonSlice(t, shapes["parts"], "shapes.parts")
	if len(parts) == 0 {
		t.Fatalf("expected at least 1 part in shapes.parts")
	}
	totalEdges := 0
	for _, p := range parts {
		pm := jsonMap(t, p, "part")
		shp := jsonMap(t, pm["shape"], "part.shape")
		switch seg := shp["segments_per_edge"].(type) {
		case map[string]any:
			bufStr := jsonStr(seg, "buffer")
			raw, err := base64.StdEncoding.DecodeString(bufStr)
			if err != nil {
				t.Fatalf("decode segments_per_edge b64: %v", err)
			}
			if len(raw)%4 != 0 {
				t.Fatalf("segments_per_edge uint32 byte length %d not divisible by 4", len(raw))
			}
			totalEdges += len(raw) / 4
		case []any:
			totalEdges += len(seg)
		default:
			t.Fatalf("unexpected segments_per_edge type %T", shp["segments_per_edge"])
		}
	}
	return totalEdges
}

func findField(fields []any, id string) map[string]any {
	for _, f := range fields {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if jsonStr(fm, "path") == id || jsonStr(fm, "name") == id {
			return fm
		}
		if children, ok := fm["children"].([]any); ok {
			if hit := findField(children, id); hit != nil {
				return hit
			}
		}
	}
	return nil
}

// ---------- Area A: Worker IPC Security & Safe Source Updates (F1, F2) ----------

func TestT1_F1_01_WorkerStartupHandshake(t *testing.T) {
	py := resolvePython(t)
	w := startWorker(t, py, workerOpts{})
	if w.port <= 0 || w.port > 65535 {
		t.Fatalf("expected valid TCP port from READY handshake, got %d", w.port)
	}
	if !strings.Contains(w.stdout.String(), fmt.Sprintf("READY %d", w.port)) {
		t.Fatalf("expected stdout to contain READY %d, got:\n%s", w.port, w.stdout.String())
	}
}

func TestT1_F1_02_ValidBearerAndLoopbackHealth(t *testing.T) {
	py := resolvePython(t)
	w := startWorker(t, py, workerOpts{})
	status, body := w.getJSON(t, "/health")
	if status != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200", status)
	}
	if ok, _ := jsonBool(body, "ok"); !ok {
		t.Fatalf("GET /health ok != true: %v", body)
	}
	for _, key := range []string{"python_version", "build123d_version", "ocp_tessellate_version"} {
		if _, exists := body[key]; !exists {
			t.Fatalf("GET /health missing required contract key %q in %v", key, body)
		}
	}
}

func TestT1_F1_03_AlternateLoopbackHosts(t *testing.T) {
	py := resolvePython(t)
	w := startWorker(t, py, workerOpts{})
	hosts := []string{
		fmt.Sprintf("localhost:%d", w.port),
		fmt.Sprintf("LOCALHOST:%d", w.port),
		fmt.Sprintf("[::1]:%d", w.port),
	}
	for _, host := range hosts {
		resp, raw, err := w.doRaw(http.MethodGet, "/health", nil, nil, host)
		if err != nil {
			t.Fatalf("Host %q request failed: %v", host, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Host %q status = %d, want 200 (body: %s)", host, resp.StatusCode, string(raw))
		}
	}
}

func TestT1_F1_04_MissingOrWrongBearerTokenRejected(t *testing.T) {
	py := resolvePython(t)
	w := startWorker(t, py, workerOpts{})

	cases := []struct {
		name   string
		header string
	}{
		{"missing", ""},
		{"wrong_bearer", "Bearer wrong-token-value"},
		{"basic_auth", "Basic dXNlcjpwYXNz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, w.baseURL+"/health", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := w.client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
				t.Fatalf("%s: status = %d, want 401 or 403", tc.name, resp.StatusCode)
			}
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("%s: expected JSON error envelope, got decode err: %v", tc.name, err)
			}
			if ok, exists := jsonBool(body, "ok"); !exists || ok {
				t.Fatalf("%s: expected ok=false, got %v", tc.name, body)
			}
		})
	}
}

func TestT1_F1_05_NonLoopbackHostRejected(t *testing.T) {
	py := resolvePython(t)
	w := startWorker(t, py, workerOpts{})
	resp, raw, err := w.doRaw(http.MethodGet, "/health", nil, nil, "evil.example:8080")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-loopback Host status = %d, want 403 (body: %s)", resp.StatusCode, string(raw))
	}
}

func TestT1_F2_01_02_03_UpdateDefaultsDryRunApplyAndSyntax(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")
	origBytes, err := os.ReadFile(pyFile)
	if err != nil {
		t.Fatal(err)
	}
	origSHA := sha256HexFile(t, pyFile)

	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})

	route := encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/update-defaults")

	// T1-F2-01: dry_run=true returns sha256 matching disk and leaves bytes untouched
	code, dry := w.postJSON(t, route, map[string]any{
		"params":  map[string]any{"width": 24.5},
		"dry_run": true,
	})
	if code != http.StatusOK {
		t.Fatalf("dry-run status = %d, body = %v", code, dry)
	}
	if ok, _ := jsonBool(dry, "ok"); !ok {
		t.Fatalf("dry-run ok != true: %v", dry)
	}
	if gotSHA := jsonStr(dry, "sha256"); gotSHA != origSHA {
		t.Fatalf("dry-run sha256 = %q, want %q", gotSHA, origSHA)
	}
	afterDryBytes, _ := os.ReadFile(pyFile)
	if !bytes.Equal(origBytes, afterDryBytes) {
		t.Fatalf("dry-run mutated file on disk")
	}

	// T1-F2-02: apply with valid expected_sha256
	code, applied := w.postJSON(t, route, map[string]any{
		"params":          map[string]any{"width": 24.5},
		"expected_sha256": origSHA,
		"dry_run":         false,
	})
	if code != http.StatusOK {
		t.Fatalf("apply status = %d, body = %v", code, applied)
	}
	if ok, _ := jsonBool(applied, "ok"); !ok {
		t.Fatalf("apply ok != true: %v", applied)
	}

	schemaRoute := encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/schema")
	sCode, schema := w.getJSON(t, schemaRoute)
	if sCode != http.StatusOK {
		t.Fatalf("schema status = %d", sCode)
	}
	defaults := jsonMap(t, schema["defaults"], "defaults")
	if defaults["width"] != 24.5 {
		t.Fatalf("updated schema width = %v, want 24.5", defaults["width"])
	}

	// T1-F2-03: valid Python syntax + render succeeds
	compileCmd := exec.Command(py, "-m", "py_compile", pyFile)
	if out, err := compileCmd.CombinedOutput(); err != nil {
		t.Fatalf("py_compile failed after update-defaults: %v\n%s", err, string(out))
	}
	rCode, render := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{
		"params": map[string]any{},
	})
	if rCode != http.StatusOK {
		t.Fatalf("render status = %d", rCode)
	}
	if ok, _ := jsonBool(render, "ok"); !ok {
		t.Fatalf("render after update-defaults failed: %v", render)
	}
}

// ---------- Area B: Python 3.10-3.14 Typing, BOM & Render Performance (F3, F4) ----------

func TestT1_F3_01_02_03_04_05_TypingAndUTF8BOM(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{
		"typing_bom.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})

	// T1-F3-05: UTF-8 BOM discovered as parametric
	mCode, mResp := w.getJSON(t, "/models")
	if mCode != http.StatusOK {
		t.Fatalf("GET /models = %d", mCode)
	}
	models := jsonSlice(t, mResp["models"], "models")
	var found map[string]any
	for _, item := range models {
		im := jsonMap(t, item, "model")
		if jsonStr(im, "id") == "mylib/typing_bom" {
			found = im
			break
		}
	}
	if found == nil {
		t.Fatalf("typing_bom not discovered in %v", models)
	}
	if jsonStr(found, "kind") != "parametric" {
		t.Fatalf("typing_bom kind = %q, want parametric (parse_error=%q)", jsonStr(found, "kind"), jsonStr(found, "parse_error"))
	}

	// T1-F3-01: PEP 604 float | None unwrapped cleanly
	_, schema := w.getJSON(t, encodeModelRoute("", "/models/", "mylib/typing_bom", "/schema"))
	fields := jsonSlice(t, schema["fields"], "fields")
	optFloat := findField(fields, "opt_float")
	if optFloat == nil {
		t.Fatalf("opt_float missing from schema fields: %v", fields)
	}
	if nullable, _ := jsonBool(optFloat, "nullable"); !nullable {
		t.Fatalf("opt_float expected nullable=true, got %v", optFloat)
	}
	if fType := jsonStr(optFloat, "type"); fType != "float" && fType != "number" {
		t.Fatalf("opt_float expected unwrapped float/number type, got %q (%v)", fType, optFloat)
	}

	// T1-F3-02 & T1-F3-03: str | None, int | None, bare list coercion
	renderRoute := encodeModelRoute("", "/models/", "mylib/typing_bom", "/render")
	rCode, r1 := w.postJSON(t, renderRoute, map[string]any{
		"params": map[string]any{
			"opt_str": nil,
			"opt_int": nil,
			"tags":    []string{"a", "b"},
		},
	})
	if rCode != http.StatusOK {
		t.Fatalf("render null/list status = %d", rCode)
	}
	if ok, _ := jsonBool(r1, "ok"); !ok {
		t.Fatalf("render null/list failed: %v", r1)
	}

	rCode2, r2 := w.postJSON(t, renderRoute, map[string]any{
		"params": map[string]any{
			"opt_str": "hello",
			"opt_int": 7,
			"tags":    []string{"x"},
		},
	})
	if rCode2 != http.StatusOK || !r2["ok"].(bool) {
		t.Fatalf("render concrete values failed (%d): %v", rCode2, r2)
	}

	// T1-F3-04: Optional[bool] / bool | None empty string "" -> None (not False!)
	_, stemResp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/typing_bom", "/export-name"), map[string]any{
		"params": map[string]any{"opt_bool": ""},
	})
	if stem := jsonStr(stemResp, "export_filename"); !strings.Contains(stem, "flag_none") {
		t.Fatalf("opt_bool=\"\" coerced unexpectedly; export_filename = %q, want *flag_none*", stem)
	}
}

func TestT1_F4_01_RenderCacheHitAndDeterministicCacheTag(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})
	route := encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/render")

	_, first := w.postJSON(t, route, map[string]any{"params": map[string]any{"width": 12.0}})
	if ok, _ := jsonBool(first, "ok"); !ok {
		t.Fatalf("first render failed: %v", first)
	}
	if cached, exists := jsonBool(first, "cached"); exists && cached {
		t.Fatalf("first render should have cached=false, got true")
	}

	_, second := w.postJSON(t, route, map[string]any{"params": map[string]any{"width": 12.0}})
	if ok, _ := jsonBool(second, "ok"); !ok {
		t.Fatalf("second render failed: %v", second)
	}
	if cached, _ := jsonBool(second, "cached"); !cached {
		t.Fatalf("second identical render should have cached=true, got %v", second)
	}

	_, changed := w.postJSON(t, route, map[string]any{"params": map[string]any{"width": 30.0}})
	if ok, _ := jsonBool(changed, "ok"); !ok {
		t.Fatalf("changed render failed: %v", changed)
	}
	if cached, exists := jsonBool(changed, "cached"); exists && cached {
		t.Fatalf("changed param render should have cached=false, got true")
	}
	bb1 := jsonMap(t, first["bbox"], "first.bbox")
	bb2 := jsonMap(t, changed["bbox"], "changed.bbox")
	if bb1["dx"] == bb2["dx"] {
		t.Fatalf("changing width 12 -> 30 did not update bbox dx (%v == %v)", bb1["dx"], bb2["dx"])
	}
}

func TestT1_F4_02_GoRawJSONRenderForwarding(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	route := encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render")
	code, resp := srv.postJSON(t, route, map[string]any{"params": map[string]any{}})
	if code != http.StatusOK {
		t.Fatalf("POST %s = %d: %v", route, code, resp)
	}
	if ok, _ := jsonBool(resp, "ok"); !ok {
		t.Fatalf("render ok != true: %v", resp)
	}
	for _, k := range []string{"shapes", "build_time_ms", "cached"} {
		if _, exists := resp[k]; !exists {
			t.Fatalf("Go render response missing key %q: %v", k, resp)
		}
	}
}

func TestT1_F4_03_RealCADEdgeDeduplication(t *testing.T) {
	py := requireRealCAD(t)
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries: []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
	})
	code, resp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{
		"params": map[string]any{},
	})
	if code != http.StatusOK || !resp["ok"].(bool) {
		t.Fatalf("real CAD render failed (%d): %v", code, resp)
	}
	edges := countEdgesInRender(t, resp)
	if edges != 12 {
		t.Fatalf("Box unique edge count = %d, want 12 (duplicate half-edges present if 24)", edges)
	}
}

func TestT1_F4_04_RealCADRenderCacheSpeedSanity(t *testing.T) {
	py := requireRealCAD(t)
	demoCopy := filepath.Join(t.TempDir(), "demo")
	copyDir(t, repoPath("examples", "demo_project"), demoCopy)

	w := startWorker(t, py, workerOpts{
		libraries: []map[string]string{{"id": "demo", "name": "Demo", "path": demoCopy}},
	})
	route := encodeModelRoute("", "/models/", "demo/spool_cap", "/render")
	_, first := w.postJSON(t, route, map[string]any{"params": map[string]any{}})
	if ok, _ := jsonBool(first, "ok"); !ok {
		t.Fatalf("spool_cap first render failed: %v", first)
	}

	start := time.Now()
	_, second := w.postJSON(t, route, map[string]any{"params": map[string]any{}})
	elapsed := time.Since(start)
	if ok, _ := jsonBool(second, "ok"); !ok {
		t.Fatalf("spool_cap second render failed: %v", second)
	}
	if cached, _ := jsonBool(second, "cached"); !cached {
		t.Fatalf("second spool_cap render expected cached=true, got %v", second)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("cached spool_cap render took %v, want < 500ms", elapsed)
	}
}

// ---------- Area C: Progressive Discovery, params.py Parity & Contract Doc (F5, F6) ----------

func TestT1_F5_01_to_06_ProgressiveDiscoveryAndASTCache(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{
		"arbitrary_script.py": "",
		"stdlib_dataclass.py": "",
		"syntax_error.py":     "",
		"full_meta/full_meta.py": `from dataclasses import dataclass
from utils.params import default_preset, group, param, param_list
import build123d as b3d

@dataclass
class SubGroup:
    """Nested group doc."""
    enabled: bool = param(True, desc="Enable feature")
    count: int = param(2, desc="Count", when={"enabled": True})

@default_preset("Bench Fixture", desc="Canonical bench defaults")
@dataclass
class FullMetaParams:
    mode: str = param("a", desc="Mode")
    sub: SubGroup = group(SubGroup)
    holes: list[float] = param_list(lambda: [3.0, 5.0], desc="Hole diameters", unit="mm")

def create_full_meta(params: FullMetaParams):
    return b3d.Box(10, 10, 10)
`,
		"utils/helper_not_model.py":             "def helper(): return 1\n",
		"components/comp_helper_not_cad.py":     "CONST = 123\n",
		"components/sample_comp/sample_comp.py": "def create_sample_comp(): pass\n",
		"exports/exported_not_model.py":         "def create_x(): pass\n",
		"__pycache__/cached_not_model.py":       "def create_y(): pass\n",
		".venv/venv_not_model.py":               "def create_z(): pass\n",
		"tests/test_not_model.py":               "def create_w(): pass\n",
	})
	// Copy canonical params.py into libDir/utils/params.py
	paramsSrc, err := os.ReadFile(repoPath("examples", "demo_project", "utils", "params.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "utils", "params.py"), paramsSrc, 0o644); err != nil {
		t.Fatal(err)
	}

	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})

	_, listResp := w.getJSON(t, "/models")
	models := jsonSlice(t, listResp["models"], "models")
	byID := map[string]map[string]any{}
	for _, m := range models {
		mm := jsonMap(t, m, "model")
		byID[jsonStr(mm, "id")] = mm
	}

	// T1-F5-01: arbitrary script
	arb, ok := byID["mylib/arbitrary_script"]
	if !ok {
		t.Fatalf("arbitrary_script missing from models: %v", byID)
	}
	if jsonStr(arb, "kind") != "script" {
		t.Fatalf("arbitrary_script kind = %q, want script", jsonStr(arb, "kind"))
	}
	_, arbRender := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/arbitrary_script", "/render"), map[string]any{"params": map[string]any{}})
	if rOK, _ := jsonBool(arbRender, "ok"); !rOK {
		t.Fatalf("arbitrary_script render failed: %v", arbRender)
	}

	// T1-F5-02: stdlib dataclass without params.py
	stdModel, ok := byID["mylib/stdlib_dataclass"]
	if !ok || jsonStr(stdModel, "kind") != "parametric" {
		t.Fatalf("stdlib_dataclass expected kind=parametric, got %v", stdModel)
	}
	_, stdSchema := w.getJSON(t, encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/schema"))
	if findField(jsonSlice(t, stdSchema["fields"], "fields"), "width") == nil {
		t.Fatalf("stdlib_dataclass missing width field in schema")
	}

	// T1-F5-03: Tier 3 params.py full metadata
	fullModel, ok := byID["mylib/full_meta"]
	if !ok || jsonStr(fullModel, "kind") != "parametric" {
		t.Fatalf("full_meta expected kind=parametric, got %v", fullModel)
	}
	_, fullSchema := w.getJSON(t, encodeModelRoute("", "/models/", "mylib/full_meta", "/schema"))
	dp := jsonMap(t, fullSchema["default_preset"], "default_preset")
	if jsonStr(dp, "label") != "Bench Fixture" {
		t.Fatalf("default_preset label = %q, want Bench Fixture", jsonStr(dp, "label"))
	}

	// T1-F5-04: syntax-errored script stays visible
	synModel, ok := byID["mylib/syntax_error"]
	if !ok {
		t.Fatalf("syntax_error script omitted from GET /models (should stay visible with kind=error)")
	}
	if jsonStr(synModel, "kind") != "error" || strings.TrimSpace(jsonStr(synModel, "parse_error")) == "" {
		t.Fatalf("syntax_error expected kind=error & non-empty parse_error, got %v", synModel)
	}

	// T1-F5-05: non-model dir filtering & component discovery
	for _, forbidden := range []string{"helper_not_model", "comp_helper_not_cad", "exported_not_model", "cached_not_model", "venv_not_model", "test_not_model"} {
		for id := range byID {
			if strings.Contains(id, forbidden) {
				t.Fatalf("non-model file %q leaked into GET /models as %q", forbidden, id)
			}
		}
	}
	compItem, ok := byID["mylib/components/sample_comp"]
	if !ok || jsonStr(compItem, "category") != "component" {
		t.Fatalf("expected components/sample_comp discovered with category=component, got %v", compItem)
	}

	// T1-F5-06: AST cache invalidation by mtime/size
	target := filepath.Join(libDir, "stdlib_dataclass.py")
	orig, _ := os.ReadFile(target)
	updated := strings.Replace(string(orig), "height: float = field(", "extra_rib: float = 2.0\n    height: float = field(", 1)
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(target, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = w.postJSON(t, "/reload", map[string]any{})
	_, newSchema := w.getJSON(t, encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/schema"))
	if findField(jsonSlice(t, newSchema["fields"], "fields"), "extra_rib") == nil {
		t.Fatalf("AST cache did not invalidate after file edit; extra_rib missing from schema")
	}
}

func TestT1_F6_01_ThreeWayParamsPyByteParity(t *testing.T) {
	demoParams := repoPath("examples", "demo_project", "utils", "params.py")
	codeCadParams := "/home/luke/repos/code-cad/utils/params.py"
	demoHash := sha256HexFile(t, demoParams)
	codeCadHash := sha256HexFile(t, codeCadParams)
	if demoHash != codeCadHash {
		t.Fatalf("params.py mismatch: demo_project (%s) != code-cad (%s)", demoHash, codeCadHash)
	}

	// Extract Studio's embedded params.py via POST /api/models/scaffold into an empty temp dir
	srv := startServer(t, serverOpts{})
	targetDir := filepath.Join(t.TempDir(), "scaffolded_lib")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv.configure(t, []map[string]string{{"id": "scaffolded", "name": "Scaffolded", "path": targetDir}}, "")
	code, resp := srv.postJSON(t, "/api/models/scaffold", map[string]any{
		"mode":       "new",
		"library_id": "scaffolded",
		"name":       "parity_check",
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/models/scaffold status = %d: %v", code, resp)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "models", "parity_check", "parity_check.py")); err != nil {
		t.Fatalf("scaffolded models/parity_check/parity_check.py missing: %v", err)
	}
	embeddedParams := filepath.Join(targetDir, "utils", "params.py")
	embeddedHash := sha256HexFile(t, embeddedParams)
	if embeddedHash != demoHash {
		t.Fatalf("Studio embedded params.py (%s) != demo_project (%s)", embeddedHash, demoHash)
	}
}

func TestT1_F6_02_ModelContractDocCompleteness(t *testing.T) {
	docPath := repoPath("docs", "MODEL_CONTRACT.md")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("missing docs/MODEL_CONTRACT.md: %v", err)
	}
	text := string(raw)
	for _, needle := range []string{"modelID", "presets.json", "schema_version", ".presets.json", "params.py", "default_preset", "export_filename"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("docs/MODEL_CONTRACT.md missing required topic %q", needle)
		}
	}
}

func TestT1_F6_03_04_ParamsPyStandaloneRuntime(t *testing.T) {
	py := resolvePython(t)
	demoUtils := repoPath("examples", "demo_project", "utils")
	script := `
import json, sys
sys.path.insert(0, sys.argv[1])
from dataclasses import dataclass, fields
from params import default_preset, param, param_list, group

@dataclass
class Child:
    enabled: bool = param(True, desc="Toggle")

@default_preset("Test", desc="Desc")
@dataclass
class Root:
    child: Child = group(Child, label="Child")
    items: list[int] = param_list(lambda: [1, 2], unit="mm")

r = Root()
assert Root.__default_preset__ == {"label": "Test", "description": "Desc"}, Root.__default_preset__
assert r.child.enabled is True
assert r.items == [1, 2]
fmap = {f.name: dict(f.metadata) for f in fields(Root)}
assert fmap["child"]["label"] == "Child"
assert fmap["items"]["unit"] == "mm"
print("OK")
`
	cmd := exec.Command(py, "-c", script, demoUtils)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("standalone params.py test failed: %v\n%s", err, string(out))
	}
}

// ---------- Area D: Canonical modelID, Export Quality Presets & Worker pytest (F7) ----------

func TestT1_F7_01_CanonicalModelIDPercent2FRouting(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{
		"sub/widget.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})

	_, list := w.getJSON(t, "/models")
	models := jsonSlice(t, list["models"], "models")
	found := false
	for _, m := range models {
		if jsonStr(jsonMap(t, m, "m"), "id") == "mylib/sub/widget" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected canonical modelID mylib/sub/widget in %v", models)
	}

	wCode, wSchema := w.getJSON(t, encodeModelRoute("", "/models/", "mylib/sub/widget", "/schema"))
	if wCode != http.StatusOK || !wSchema["ok"].(bool) {
		t.Fatalf("worker %%2F route failed (%d): %v", wCode, wSchema)
	}

	// Also verify through Go server
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, py)
	sCode, sDetail := srv.getJSON(t, encodeModelRoute("", "/api/models/", "mylib/sub/widget", ""))
	if sCode != http.StatusOK || !sDetail["ok"].(bool) {
		t.Fatalf("server %%2F route failed (%d): %v", sCode, sDetail)
	}
}

func TestT1_F7_02_ExportNameStemHook(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{
		"sub/widget.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})
	code, resp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/sub/widget", "/export-name"), map[string]any{
		"params": map[string]any{"size": 10.0},
	})
	if code != http.StatusOK || !resp["ok"].(bool) {
		t.Fatalf("export-name failed (%d): %v", code, resp)
	}
	if stem := jsonStr(resp, "export_filename"); stem != "widget_10mm" {
		t.Fatalf("export_filename = %q, want widget_10mm", stem)
	}
}

func TestT1_F7_03_RealCADSTLExport(t *testing.T) {
	py := requireRealCAD(t)
	libDir := setupTempModelLib(t, map[string]string{
		"curved_export.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries: []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
	})
	outPath := filepath.Join(t.TempDir(), "out.stl")
	code, resp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/curved_export", "/export"), map[string]any{
		"params":      map[string]any{},
		"format":      "stl",
		"quality":     "standard",
		"output_path": outPath,
	})
	if code != http.StatusOK || !resp["ok"].(bool) {
		t.Fatalf("STL export failed (%d): %v", code, resp)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	tris := decodeSTLTriangleCount(t, data)
	if tris <= 0 {
		t.Fatalf("STL triangle count = %d, want > 0", tris)
	}
}

func TestT1_F7_04_RealCADSTEPAnd3MFExport(t *testing.T) {
	py := requireRealCAD(t)
	libDir := setupTempModelLib(t, map[string]string{
		"curved_export.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries: []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
	})
	tmp := t.TempDir()

	stepPath := filepath.Join(tmp, "out.step")
	sCode, sResp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/curved_export", "/export"), map[string]any{
		"params":      map[string]any{},
		"format":      "step",
		"output_path": stepPath,
	})
	if sCode != http.StatusOK || !sResp["ok"].(bool) {
		t.Fatalf("STEP export failed (%d): %v", sCode, sResp)
	}
	stepText := string(func() []byte { b, _ := os.ReadFile(stepPath); return b }())
	if !strings.HasPrefix(strings.TrimSpace(stepText), "ISO-10303-21;") || !strings.Contains(stepText, "END-ISO-10303-21;") {
		t.Fatalf("STEP file invalid header/footer")
	}

	mfPath := filepath.Join(tmp, "out.3mf")
	mCode, mResp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/curved_export", "/export"), map[string]any{
		"params":      map[string]any{},
		"format":      "3mf",
		"quality":     "standard",
		"output_path": mfPath,
	})
	if mCode != http.StatusOK || !mResp["ok"].(bool) {
		t.Fatalf("3MF export failed (%d): %v", mCode, mResp)
	}
	zr, err := zip.OpenReader(mfPath)
	if err != nil {
		t.Fatalf("3MF is not valid ZIP: %v", err)
	}
	defer zr.Close()
	has3DModel := false
	for _, f := range zr.File {
		if f.Name == "3D/3dmodel.model" {
			has3DModel = true
			break
		}
	}
	if !has3DModel {
		t.Fatalf("3MF archive missing 3D/3dmodel.model")
	}
}

func TestT1_F7_05_RealCADExportQualityPresetsOrdering(t *testing.T) {
	py := requireRealCAD(t)
	libDir := setupTempModelLib(t, map[string]string{
		"curved_export.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries: []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
	})
	tmp := t.TempDir()
	exportQuality := func(q string, extra map[string]any) int {
		outPath := filepath.Join(tmp, q+".stl")
		payload := map[string]any{
			"params":      map[string]any{},
			"format":      "stl",
			"quality":     q,
			"output_path": outPath,
		}
		for k, v := range extra {
			payload[k] = v
		}
		code, resp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/curved_export", "/export"), payload)
		if code != http.StatusOK || !resp["ok"].(bool) {
			t.Fatalf("export %s failed (%d): %v", q, code, resp)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		return decodeSTLTriangleCount(t, data)
	}

	draftTris := exportQuality("draft", nil)
	stdTris := exportQuality("standard", nil)
	fineTris := exportQuality("fine", nil)
	customTris := exportQuality("custom", map[string]any{
		"linear_deflection":  0.005,
		"angular_deflection": 0.05,
	})

	if !(draftTris <= stdTris && stdTris <= fineTris && draftTris < fineTris) {
		t.Fatalf("expected triangle ordering draft (%d) <= standard (%d) <= fine (%d) with draft < fine", draftTris, stdTris, fineTris)
	}
	if customTris <= draftTris {
		t.Fatalf("custom tight deflection triangles (%d) should exceed draft (%d)", customTris, draftTris)
	}
}

func TestT1_F7_06_WorkerPytestSuite(t *testing.T) {
	py := resolvePython(t)
	requirePytest(t, py)
	cmd := exec.Command(py, "-m", "pytest", repoPath("worker", "tests"), "-v")
	cmd.Dir = suiteRepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pytest worker/tests failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "passed") {
		t.Fatalf("expected pytest to collect and pass >= 1 tests, output:\n%s", string(out))
	}
}
