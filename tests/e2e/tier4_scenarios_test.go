//go:build e2e

package e2e_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func copyCodeCadRepo(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".venv", "venv", "__pycache__", "exports":
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyCodeCadRepo(%s -> %s): %v", src, dst, err)
	}
}

func assertValidSTLBytes(t *testing.T, data []byte) {
	t.Helper()
	tris := decodeSTLTriangleCount(t, data)
	if tris <= 0 {
		t.Fatalf("STL has 0 triangles")
	}
}

func assertValidSTEPBytes(t *testing.T, data []byte) {
	t.Helper()
	text := strings.TrimSpace(string(data))
	if !strings.HasPrefix(text, "ISO-10303-21;") || !strings.Contains(text, "END-ISO-10303-21;") {
		t.Fatalf("invalid STEP payload (len=%d)", len(data))
	}
}

func assertValid3MFBytes(t *testing.T, data []byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("invalid 3MF ZIP: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "3D/3dmodel.model" {
			return
		}
	}
	t.Fatalf("3MF missing 3D/3dmodel.model")
}

// T4-01: Real-World examples/demo_project End-to-End Maker Workflow
func TestT4_01_DemoProjectMakerWorkflow(t *testing.T) {
	demoDir := filepath.Join(t.TempDir(), "demo_project")
	copyDir(t, repoPath("examples", "demo_project"), demoDir)

	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "demo", "name": "Demo", "path": demoDir}}, "")

	// (a) Always-runnable discovery, schema, presets v1 & persistence checks
	_, list := srv.getJSON(t, "/api/models")
	models := jsonSlice(t, list["models"], "models")
	var spoolInfo map[string]any
	for _, item := range models {
		im := jsonMap(t, item, "m")
		if jsonStr(im, "id") == "demo/spool_cap" {
			spoolInfo = im
			break
		}
	}
	if spoolInfo == nil || jsonStr(spoolInfo, "kind") != "parametric" {
		t.Fatalf("demo/spool_cap not discovered as kind=parametric: %v", models)
	}

	_, detail := srv.getJSON(t, encodeModelRoute("", "/api/models/", "demo/spool_cap", ""))
	if ok, _ := jsonBool(detail, "ok"); !ok {
		t.Fatalf("GET demo/spool_cap failed: %v", detail)
	}
	fields := jsonSlice(t, detail["fields"], "fields")
	for _, grpName := range []string{"core", "flutes", "base"} {
		if findField(fields, grpName) == nil {
			t.Fatalf("spool_cap schema missing group %q: %v", grpName, fields)
		}
	}

	presets := jsonSlice(t, detail["presets"], "presets")
	var prusaPreset map[string]any
	for _, p := range presets {
		pm := jsonMap(t, p, "preset")
		if jsonStr(pm, "key") == "prusa_spool_holder" {
			prusaPreset = pm
			break
		}
	}
	if prusaPreset == nil {
		t.Fatalf("spool_cap missing prusa_spool_holder preset: %v", presets)
	}

	// Update flutes.enabled=false, save new project preset, and mark verified
	defaults := jsonMap(t, detail["defaults"], "defaults")
	flutes := jsonMap(t, defaults["flutes"], "defaults.flutes")
	flutes["enabled"] = false

	pCode, pResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "demo/spool_cap", "/presets"), map[string]any{
		"label":        "Smooth Sleeve",
		"description":  "No grip flutes",
		"scope":        "project",
		"params":       defaults,
		"verification": map[string]any{"status": "verified", "note": "Printed cleanly"},
	})
	if pCode != http.StatusOK || !pResp["ok"].(bool) {
		t.Fatalf("save Smooth Sleeve preset failed (%d): %v", pCode, pResp)
	}

	presetsRaw, err := os.ReadFile(filepath.Join(demoDir, "models", "spool_cap", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	if err := json.Unmarshal(presetsRaw, &disk); err != nil {
		t.Fatal(err)
	}
	if sv, ok := disk["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("spool_cap presets.json schema_version = %v, want 1", disk["schema_version"])
	}

	// (b) Real-CAD geometry render + STL/STEP/3MF browser download export
	t.Run("RealCAD", func(t *testing.T) {
		py := requireRealCAD(t)
		realSrv := startServer(t, serverOpts{})
		realSrv.configure(t, []map[string]string{{"id": "demo", "name": "Demo", "path": demoDir}}, py)

		for _, tc := range []struct {
			name   string
			params map[string]any
		}{
			{"default", map[string]any{}},
			{"prusa_spool_holder", jsonMap(t, prusaPreset["params"], "prusa.params")},
		} {
			rCode, rResp := realSrv.postJSON(t, encodeModelRoute("", "/api/models/", "demo/spool_cap", "/render"), map[string]any{"params": tc.params})
			if rCode != http.StatusOK || !rResp["ok"].(bool) {
				t.Fatalf("real CAD render spool_cap (%s) failed (%d): %v", tc.name, rCode, rResp)
			}
			shapes := jsonMap(t, rResp["shapes"], "shapes")
			if parts := jsonSlice(t, shapes["parts"], "parts"); len(parts) == 0 {
				t.Fatalf("spool_cap (%s) returned 0 parts", tc.name)
			}
		}

		expRoute := encodeModelRoute("", "/api/models/", "demo/spool_cap", "/export")
		for _, exp := range []struct {
			format  string
			quality string
			check   func(*testing.T, []byte)
		}{
			{"stl", "fine", assertValidSTLBytes},
			{"step", "standard", assertValidSTEPBytes},
			{"3mf", "standard", assertValid3MFBytes},
		} {
			b, _ := json.Marshal(map[string]any{"params": map[string]any{}, "format": exp.format, "quality": exp.quality})
			resp, raw, err := realSrv.doRaw(http.MethodPost, expRoute, b, nil, "")
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("demo/spool_cap %s export failed (%v, %v): %s", exp.format, err, resp, string(raw))
			}
			exp.check(t, raw)
		}
	})
}

// T4-02: Real-World /home/luke/repos/code-cad All-7-Models Workflow
func TestT4_02_CodeCadAll7ModelsWorkflow(t *testing.T) {
	codeCadCopy := filepath.Join(t.TempDir(), "code-cad")
	copyCodeCadRepo(t, "/home/luke/repos/code-cad", codeCadCopy)

	py := resolvePython(t)
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "code-cad", "name": "Code CAD", "path": codeCadCopy}}, py)

	// (a) Always-runnable: all 7 models parametric, 6 presets.json v1, export_filename snake_case
	expectedModels := []string{
		"c_clip", "container_lid", "gridfinity_bin", "magnetic_label",
		"rack_bracket", "shaft_end_cap", "spacer",
	}
	_, list := srv.getJSON(t, "/api/models")
	models := jsonSlice(t, list["models"], "models")
	kinds := map[string]string{}
	for _, item := range models {
		im := jsonMap(t, item, "m")
		kinds[jsonStr(im, "id")] = jsonStr(im, "kind")
		if jsonStr(im, "kind") == "error" {
			t.Fatalf("model %q has kind=error (%q)", jsonStr(im, "id"), jsonStr(im, "parse_error"))
		}
	}
	for _, name := range expectedModels {
		id := "code-cad/" + name
		if kinds[id] != "parametric" {
			t.Fatalf("model %s kind = %q, want parametric (all=%v)", id, kinds[id], kinds)
		}
		dCode, detail := srv.getJSON(t, encodeModelRoute("", "/api/models/", id, ""))
		if dCode != http.StatusOK || !detail["ok"].(bool) {
			t.Fatalf("GET %s failed (%d): %v", id, dCode, detail)
		}
	}

	// Verify all 6 presets.json are schema_version: 1
	for _, name := range []string{"c_clip", "container_lid", "gridfinity_bin", "rack_bracket", "shaft_end_cap", "spacer"} {
		raw, err := os.ReadFile(filepath.Join(codeCadCopy, "models", name, "presets.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if sv, ok := doc["schema_version"].(float64); !ok || int(sv) != 1 {
			t.Fatalf("%s presets.json schema_version = %v, want 1", name, doc["schema_version"])
		}
	}

	// Check export-name returns lowercase snake_case stems via worker IPC
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "code-cad", "name": "Code CAD", "path": codeCadCopy}},
		useMockCAD: true,
	})
	snakeRe := regexp.MustCompile(`^[a-z0-9_]+$`)
	for _, name := range expectedModels {
		id := "code-cad/" + name
		eCode, eResp := w.postJSON(t, encodeModelRoute("", "/models/", id, "/export-name"), map[string]any{"params": map[string]any{}})
		if eCode != http.StatusOK || !eResp["ok"].(bool) {
			t.Fatalf("export-name %s failed (%d): %v", id, eCode, eResp)
		}
		stem := jsonStr(eResp, "export_filename")
		if stem == "" || !snakeRe.MatchString(strings.ReplaceAll(stem, ".", "_")) {
			t.Fatalf("model %s export_filename stem %q is not lowercase snake_case", id, stem)
		}
	}

	// (b) Real-CAD geometry renders & STL/STEP exports for representative models
	t.Run("RealCAD", func(t *testing.T) {
		realPy := requireRealCAD(t)
		realSrv := startServer(t, serverOpts{})
		realSrv.configure(t, []map[string]string{{"id": "code-cad", "name": "Code CAD", "path": codeCadCopy}}, realPy)

		targets := []struct {
			modelID   string
			presetKey string
		}{
			{"code-cad/c_clip", "prusa_core_one_l_spool_clip"},
			{"code-cad/container_lid", "anchor_round_6_cup"},
			{"code-cad/rack_bracket", ""},
			{"code-cad/gridfinity_bin", "gridfinity_1x1x4_bin"},
		}
		for _, tgt := range targets {
			params := map[string]any{}
			if tgt.presetKey != "" {
				_, detail := realSrv.getJSON(t, encodeModelRoute("", "/api/models/", tgt.modelID, ""))
				for _, p := range jsonSlice(t, detail["presets"], "presets") {
					pm := jsonMap(t, p, "preset")
					if jsonStr(pm, "key") == tgt.presetKey {
						params = jsonMap(t, pm["params"], "preset.params")
						break
					}
				}
			}
			rCode, rResp := realSrv.postJSON(t, encodeModelRoute("", "/api/models/", tgt.modelID, "/render"), map[string]any{"params": params})
			if rCode != http.StatusOK || !rResp["ok"].(bool) {
				t.Fatalf("real CAD render %s (%s) failed (%d): %v", tgt.modelID, tgt.presetKey, rCode, rResp)
			}
			shapes := jsonMap(t, rResp["shapes"], "shapes")
			if len(jsonSlice(t, shapes["parts"], "parts")) == 0 {
				t.Fatalf("%s returned 0 parts", tgt.modelID)
			}

			for _, fmtName := range []string{"stl", "step"} {
				b, _ := json.Marshal(map[string]any{"params": params, "format": fmtName, "quality": "standard"})
				resp, raw, err := realSrv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", tgt.modelID, "/export"), b, nil, "")
				if err != nil || resp.StatusCode != http.StatusOK {
					t.Fatalf("%s %s export failed (%v, %v): %s", tgt.modelID, fmtName, err, resp, string(raw))
				}
				if fmtName == "stl" {
					assertValidSTLBytes(t, raw)
				} else {
					assertValidSTEPBytes(t, raw)
				}
			}
		}
	})
}

// T4-03: Full Build & Release Smoke Gates
func TestT4_03_FullBuildAndReleaseSmokeGates(t *testing.T) {
	// (a) Frontend check, Vitest, build
	requireNPM(t)
	for _, args := range [][]string{
		{"--prefix", repoPath("frontend"), "run", "check"},
		{"--prefix", repoPath("frontend"), "test"},
		{"--prefix", repoPath("frontend"), "run", "build"},
	} {
		cmd := exec.Command("npm", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("npm %v failed: %v\n%s", args, err, string(out))
		}
	}

	// (b) go test -race ./...
	raceCmd := exec.Command("go", "test", "-race", "./...")
	raceCmd.Dir = suiteRepoRoot
	if out, err := raceCmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -race ./... failed: %v\n%s", err, string(out))
	}

	// (c) go build . (desktop binary)
	tmpBin := t.TempDir()
	deskCmd := exec.Command("go", "build", "-o", filepath.Join(tmpBin, "code-cad-studio"), ".")
	deskCmd.Dir = suiteRepoRoot
	if out, err := deskCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build . (desktop) failed: %v\n%s", err, string(out))
	}

	// (d) go build -tags server . + GET / + GET /api/config + clean SIGTERM exit
	srv := startServer(t, serverOpts{})
	rootResp, _, err := srv.doRaw(http.MethodGet, "/", nil, nil, "")
	if err != nil || rootResp.StatusCode != http.StatusOK {
		t.Fatalf("server GET / failed: %v %v", err, rootResp)
	}
	_, cfg := srv.getJSON(t, "/api/config")
	if hd, ok := jsonBool(cfg, "has_desktop"); !ok || hd {
		t.Fatalf("server has_desktop = %v, want false", cfg["has_desktop"])
	}
	srv.stop()

	// (e) pytest worker/tests
	py := resolvePython(t)
	requirePytest(t, py)
	pyCmd := exec.Command(py, "-m", "pytest", repoPath("worker", "tests"), "-v")
	pyCmd.Dir = suiteRepoRoot
	if out, err := pyCmd.CombinedOutput(); err != nil {
		t.Fatalf("pytest worker/tests failed: %v\n%s", err, string(out))
	}
}
