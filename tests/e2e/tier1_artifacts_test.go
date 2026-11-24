//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ---------- Area F: Reference Repo code-cad & Demo Migration (F11) ----------

func TestT1_F11_01_CodeCadPresetsV1(t *testing.T) {
	models := []string{"c_clip", "container_lid", "gridfinity_bin", "rack_bracket", "shaft_end_cap", "spacer"}
	for _, m := range models {
		p := filepath.Join("/home/luke/repos/code-cad/models", m, "presets.json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if sv, ok := doc["schema_version"].(float64); !ok || int(sv) != 1 {
			t.Fatalf("%s schema_version = %v, want 1", p, doc["schema_version"])
		}
		presetsMap := jsonMap(t, doc["presets"], m+".presets")
		if len(presetsMap) == 0 {
			t.Fatalf("%s presets map is empty", p)
		}
		if m == "container_lid" {
			cup := jsonMap(t, presetsMap["anchor_round_6_cup"], "anchor_round_6_cup")
			ver := jsonMap(t, cup["verification"], "anchor_round_6_cup.verification")
			if jsonStr(ver, "status") != "usable" {
				t.Fatalf("container_lid anchor_round_6_cup status = %q, want usable", jsonStr(ver, "status"))
			}
		}
	}
}

func TestT1_F11_02_DemoProjectPresetsV1(t *testing.T) {
	p := repoPath("examples", "demo_project", "models", "spool_cap", "presets.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	if sv, ok := doc["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("%s schema_version = %v, want 1", p, doc["schema_version"])
	}
	presetsMap := jsonMap(t, doc["presets"], "demo.presets")
	if _, ok := presetsMap["prusa_spool_holder"]; !ok {
		t.Fatalf("demo spool_cap missing prusa_spool_holder preset: %v", presetsMap)
	}
}

func TestT1_F11_03_LargePDFRemovedFromCodeCad(t *testing.T) {
	pdfPath := "/home/luke/repos/code-cad/docs/build123d-readthedocs-io-en-latest.pdf"
	if _, err := os.Stat(pdfPath); err == nil {
		t.Fatalf("16.7 MB PDF still exists at %s", pdfPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", pdfPath, err)
	}
}

func TestT1_F11_04_CodeCadAll7ModelsDiscoverCleanly(t *testing.T) {
	py := resolvePython(t)
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "code-cad", "name": "Code CAD", "path": "/home/luke/repos/code-cad"}},
		useMockCAD: true,
	})
	code, resp := w.getJSON(t, "/models")
	if code != http.StatusOK || !resp["ok"].(bool) {
		t.Fatalf("GET /models failed (%d): %v", code, resp)
	}
	models := jsonSlice(t, resp["models"], "models")
	found := map[string]string{}
	for _, item := range models {
		im := jsonMap(t, item, "model")
		id := jsonStr(im, "id")
		kind := jsonStr(im, "kind")
		found[id] = kind
		if kind == "error" {
			t.Fatalf("code-cad model %q has kind=error (%q)", id, jsonStr(im, "parse_error"))
		}
	}
	expected := []string{
		"code-cad/c_clip",
		"code-cad/container_lid",
		"code-cad/gridfinity_bin",
		"code-cad/magnetic_label",
		"code-cad/rack_bracket",
		"code-cad/shaft_end_cap",
		"code-cad/spacer",
	}
	for _, id := range expected {
		if found[id] != "parametric" {
			t.Fatalf("expected %q to be kind=parametric, got %q (all=%v)", id, found[id], found)
		}
	}
}

// ---------- Area H: Frontend Checks, Vitest, Bundle/CSS & 3D Viewport / UX (F15, F16, F17) ----------

func TestT1_F15_01_FrontendPackageJSONHygiene(t *testing.T) {
	raw, err := os.ReadFile(repoPath("frontend", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	deps := jsonMap(t, pkg["dependencies"], "dependencies")
	devDeps := jsonMap(t, pkg["devDependencies"], "devDependencies")

	tcv := jsonStr(deps, "three-cad-viewer")
	if tcv == "" || tcv == "latest" || tcv == "*" {
		t.Fatalf("three-cad-viewer must be pinned to a specific version range, got %q", tcv)
	}
	if _, hasTypo := deps["@tailwindcss/typography"]; hasTypo {
		t.Fatalf("@tailwindcss/typography must be removed from dependencies")
	}
	if _, hasTypo := devDeps["@tailwindcss/typography"]; hasTypo {
		t.Fatalf("@tailwindcss/typography must be removed from devDependencies")
	}
}

func TestT1_F15_02_FrontendSvelteCheck(t *testing.T) {
	requireNPM(t)
	cmd := exec.Command("npm", "--prefix", repoPath("frontend"), "run", "check")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm run check failed: %v\n%s", err, string(out))
	}
	text := string(out)
	if !strings.Contains(text, "0 errors") || !strings.Contains(text, "0 warnings") {
		t.Fatalf("expected svelte-check 0 errors and 0 warnings, got:\n%s", text)
	}
}

func TestT1_F15_03_FrontendVitestSuite(t *testing.T) {
	requireNPM(t)
	cmd := exec.Command("npm", "--prefix", repoPath("frontend"), "test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm --prefix frontend test failed: %v\n%s", err, string(out))
	}
}

func TestT1_F15_04_05_FrontendBuildCSSBudgetLazyChunkAndCSP(t *testing.T) {
	requireNPM(t)
	cmd := exec.Command("npm", "--prefix", repoPath("frontend"), "run", "build")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm --prefix frontend run build failed: %v\n%s", err, string(out))
	}

	// Check entry CSS (trimmed DaisyUI) < 160 KiB, separate lazy cadViewer-*.css chunk > 0, and total CSS < 264 KiB baseline
	assetsDir := repoPath("frontend", "dist", "assets")
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatalf("readdir %s: %v", assetsDir, err)
	}
	var totalCSS int64
	var indexCSS int64
	var cadViewerCSS int64
	var jsFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(e.Name(), ".css") {
			totalCSS += info.Size()
			if strings.HasPrefix(e.Name(), "index-") {
				indexCSS += info.Size()
			} else if strings.HasPrefix(e.Name(), "cadViewer-") {
				cadViewerCSS += info.Size()
			}
		} else if strings.HasSuffix(e.Name(), ".js") {
			jsFiles = append(jsFiles, e.Name())
		}
	}
	if indexCSS == 0 || indexCSS >= 160*1024 {
		t.Fatalf("entry CSS (index-*.css) size = %d bytes, want >0 and < %d (trimmed DaisyUI themes)", indexCSS, 160*1024)
	}
	if cadViewerCSS == 0 {
		t.Fatalf("expected separately chunked lazy viewer CSS (cadViewer-*.css) with size > 0, got 0")
	}
	if totalCSS == 0 || totalCSS >= 264*1024 {
		t.Fatalf("total built CSS size = %d bytes, want >0 and < %d (vs 264 KiB baseline)", totalCSS, 264*1024)
	}

	// Check theme-init.js & no inline <script> body in dist/index.html
	if _, err := os.Stat(repoPath("frontend", "dist", "theme-init.js")); err != nil {
		t.Fatalf("missing frontend/dist/theme-init.js: %v", err)
	}
	indexRaw, err := os.ReadFile(repoPath("frontend", "dist", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	inlineScriptRe := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	for _, match := range inlineScriptRe.FindAllStringSubmatch(string(indexRaw), -1) {
		attrs, body := match[1], strings.TrimSpace(match[2])
		if body != "" || !strings.Contains(strings.ToLower(attrs), "src=") {
			t.Fatalf("CSP violation: found inline <script> in dist/index.html: %s", match[0])
		}
	}

	// Verify three-cad-viewer is split into a dedicated lazy JS chunk (separate from entry index chunk)
	if len(jsFiles) < 2 {
		t.Fatalf("expected >= 2 JS chunks (entry + lazy three-cad-viewer chunk), got %v", jsFiles)
	}
	entryMatch := regexp.MustCompile(`src="/assets/([^"]+\.js)"`).FindStringSubmatch(string(indexRaw))
	if len(entryMatch) < 2 {
		t.Fatalf("could not locate entry JS script in dist/index.html:\n%s", string(indexRaw))
	}
	entryJS := entryMatch[1]
	entryBytes, _ := os.ReadFile(filepath.Join(assetsDir, entryJS))
	if strings.Contains(string(entryBytes), "three-cad-viewer") && len(entryBytes) > 800*1024 {
		t.Fatalf("entry JS chunk %s appears to statically bundle three-cad-viewer (%d bytes)", entryJS, len(entryBytes))
	}
}

func TestT1_F16_01_02_ViewportPreservationAndOfflineHDRIBlocking(t *testing.T) {
	requireNPM(t)
	cmd := exec.Command("npm", "--prefix", repoPath("frontend"), "test", "--", "--reporter=verbose")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("frontend Vitest failed: %v\n%s", err, string(out))
	}
	text := strings.ToLower(string(out))
	if !strings.Contains(text, "viewport") && !strings.Contains(text, "cadviewer") {
		t.Fatalf("Vitest output did not run CadViewportController specs:\n%s", string(out))
	}
	if !strings.Contains(text, "polyhaven") && !strings.Contains(text, "hdri") {
		t.Fatalf("Vitest output did not verify Poly Haven / HDRI external fetch blocking:\n%s", string(out))
	}
}

func TestT1_F16_03_ServerServesBuiltSPAAssets(t *testing.T) {
	srv := startServer(t, serverOpts{})
	resp, body, err := srv.doRaw(http.MethodGet, "/", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	html := string(body)
	assetRe := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+|/theme-init\.js)"`)
	matches := assetRe.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		t.Fatalf("no /assets/ or /theme-init.js references found in GET / HTML:\n%s", html)
	}
	for _, m := range matches {
		assetURL := m[1]
		aResp, _, aErr := srv.doRaw(http.MethodGet, assetURL, nil, nil, "")
		if aErr != nil {
			t.Fatalf("GET %s failed: %v", assetURL, aErr)
		}
		if aResp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", assetURL, aResp.StatusCode)
		}
	}
}

func TestT1_F17_01_FrontendStateRacesAndDialogShowModal(t *testing.T) {
	requireNPM(t)
	cmd := exec.Command("npm", "--prefix", repoPath("frontend"), "test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("frontend Vitest failed: %v\n%s", err, string(out))
	}

	// Verify built bundle uses native .showModal() for dialogs
	var foundShowModal bool
	_ = filepath.WalkDir(repoPath("frontend", "dist", "assets"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".js") {
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), "showModal(") {
				foundShowModal = true
			}
		}
		return nil
	})
	if !foundShowModal {
		t.Fatalf("built frontend bundle does not call native <dialog>.showModal()")
	}
}

func TestT1_F17_02_UXConfigFieldsRoundTrip(t *testing.T) {
	srv := startServer(t, serverOpts{})
	code, _ := srv.postJSON(t, "/api/config", map[string]any{
		"libraries":          []map[string]string{},
		"export_dir":         srv.exportsDir,
		"onboarding_skipped": true,
		"editor_command":     "nvim",
		"slicer_command":     "prusaslicer",
		"export_quality":     "draft",
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/config = %d", code)
	}
	_, got := srv.getJSON(t, "/api/config")
	cfg := extractConfigMap(t, got)
	if skipped, _ := jsonBool(cfg, "onboarding_skipped"); !skipped {
		t.Fatalf("onboarding_skipped not persisted: %v", cfg)
	}
	if jsonStr(cfg, "editor_command") != "nvim" || jsonStr(cfg, "slicer_command") != "prusaslicer" || jsonStr(cfg, "export_quality") != "draft" {
		t.Fatalf("UX config fields mismatch: %v", cfg)
	}
}

func TestT1_F17_03_GlobalWorkerErrorSSEPropagation(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	_, _ = srv.postJSON(t, "/api/config", map[string]any{
		"libraries":   []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		"python_path": "/nonexistent/broken-python-for-sse",
		"export_dir":  srv.exportsDir,
	})

	evt := sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "worker-error" })
	var payload map[string]any
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		t.Fatalf("invalid worker-error JSON: %v (%s)", err, evt.Data)
	}
	if strings.TrimSpace(jsonStr(payload, "error")) == "" {
		t.Fatalf("worker-error SSE missing readable error message: %v", payload)
	}
}

func TestT1_F17_04_ExportQualityPresetsAcceptedByServer(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"curved_export.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	route := encodeModelRoute("", "/api/models/", "mylib/curved_export", "/export")
	for _, tc := range []map[string]any{
		{"params": map[string]any{}, "format": "stl", "quality": "draft", "filename": "custom_draft"},
		{"params": map[string]any{}, "format": "stl", "quality": "standard"},
		{"params": map[string]any{}, "format": "stl", "quality": "fine"},
		{"params": map[string]any{}, "format": "stl", "quality": "custom", "linear_deflection": 0.01, "angular_deflection": 0.1},
	} {
		b, _ := json.Marshal(tc)
		resp, raw, err := srv.doRaw(http.MethodPost, route, b, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || len(raw) == 0 {
			t.Fatalf("export %v failed (%d): %s", tc["quality"], resp.StatusCode, string(raw))
		}
		if fn, ok := tc["filename"].(string); ok {
			cd := resp.Header.Get("Content-Disposition")
			if !strings.Contains(cd, fn) {
				t.Fatalf("expected custom filename %q in Content-Disposition %q", fn, cd)
			}
		}
	}
}

// ---------- Area I: Cross-Platform Packaging, Licensing & CI (F18) ----------

func TestT1_F18_01_DeadBuildFilesRemoved(t *testing.T) {
	deadPaths := []string{
		"build/android",
		"build/ios",
		"build/windows/msix",
		"build/linux/appimage/build.sh",
		"build/linux/desktop",
	}
	for _, rel := range deadPaths {
		if _, err := os.Stat(repoPath(rel)); err == nil {
			t.Fatalf("dead packaging artifact still exists: %s", rel)
		}
	}
}

func TestT1_F18_02_BuildConfigYmlRealMetadata(t *testing.T) {
	raw, err := os.ReadFile(repoPath("build", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, bad := range []string{"My Company", "My Product", "com.mycompany.myproduct", `"0.0.1"`} {
		if strings.Contains(text, bad) {
			t.Fatalf("build/config.yml still contains placeholder %q", bad)
		}
	}
	if !strings.Contains(text, "Code CAD Studio") {
		t.Fatalf("build/config.yml missing 'Code CAD Studio' productName")
	}
}

func TestT1_F18_03_WindowsManifestAndNSISAlignment(t *testing.T) {
	manifestRaw, err := os.ReadFile(repoPath("build", "windows", "wails.exe.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestRaw), `version="0.1.0.0"`) {
		t.Fatalf("wails.exe.manifest missing 4-part version=\"0.1.0.0\":\n%s", string(manifestRaw))
	}

	nsiRaw, err := os.ReadFile(repoPath("build", "windows", "nsis", "project.nsi"))
	if err != nil {
		t.Fatal(err)
	}
	winTaskRaw, err := os.ReadFile(repoPath("build", "windows", "Taskfile.yml"))
	if err != nil {
		t.Fatal(err)
	}
	nsiText := string(nsiRaw)
	taskText := string(winTaskRaw)
	if !strings.Contains(nsiText, "OutFile") {
		t.Fatalf("project.nsi missing OutFile")
	}
	// Verify sign:installer path matches project.nsi output location (bin/ or installer pattern)
	if strings.Contains(taskText, `build/windows/nsis/{{.APP_NAME}}-installer.exe`) && strings.Contains(nsiText, `..\..\..\bin\`) {
		t.Fatalf("build/windows/Taskfile.yml sign:installer path does not match project.nsi OutFile")
	}
}

func TestT1_F18_04_MacOSIconConfig(t *testing.T) {
	plistRaw, err := os.ReadFile(repoPath("build", "darwin", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	cfgRaw, err := os.ReadFile(repoPath("build", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// If Assets.car exists in build/darwin, cfBundleIconName / CFBundleIconFile must match or stock Assets.car must be replaced/removed
	plist := string(plistRaw)
	cfg := string(cfgRaw)
	if !strings.Contains(plist, "CFBundleIconFile") && !strings.Contains(cfg, "cfBundleIconName") {
		t.Fatalf("macOS icon configuration missing in Info.plist and build/config.yml")
	}
	if _, err := os.Stat(repoPath("build", "darwin", "Assets.car")); err == nil {
		// If Assets.car is kept, verify it is not the untouched Wails stock 1631800-byte file OR Info.plist explicitly configures custom icon cleanly
		info, _ := os.Stat(repoPath("build", "darwin", "Assets.car"))
		if info != nil && info.Size() == 1631800 && !strings.Contains(plist, "icons.icns") {
			t.Fatalf("stock Wails Assets.car (1631800 bytes) still present without custom icon override fix")
		}
	}
}

func TestT1_F18_05_NfpmApache20License(t *testing.T) {
	raw, err := os.ReadFile(repoPath("build", "linux", "nfpm", "nfpm.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `license: "Apache-2.0"`) && !strings.Contains(text, "license: Apache-2.0") {
		t.Fatalf("nfpm.yaml does not declare Apache-2.0 license:\n%s", text)
	}
}

func TestT1_F18_06_RootApache20AndReleaseDocs(t *testing.T) {
	licRaw, err := os.ReadFile(repoPath("LICENSE"))
	if err != nil {
		t.Fatalf("missing LICENSE: %v", err)
	}
	if !strings.Contains(string(licRaw), "Apache License") || !strings.Contains(string(licRaw), "Version 2.0") {
		t.Fatalf("LICENSE is not Apache-2.0")
	}

	for _, name := range []string{"THIRD_PARTY_NOTICES.txt", "SECURITY.md", "CHANGELOG.md"} {
		b, err := os.ReadFile(repoPath(name))
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			t.Fatalf("missing or empty %s: %v", name, err)
		}
		if name == "CHANGELOG.md" && !strings.Contains(string(b), "0.1.0-alpha") {
			t.Fatalf("CHANGELOG.md does not mention 0.1.0-alpha")
		}
	}
}

func TestT1_F18_07_CIWorkflowAndDockerfile(t *testing.T) {
	ciRaw, err := os.ReadFile(repoPath(".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("missing .github/workflows/ci.yml: %v", err)
	}
	ci := string(ciRaw)
	for _, needle := range []string{"ubuntu-24.04", "macos-14", "windows-latest", "pytest", "go test", "npm"} {
		if !strings.Contains(ci, needle) {
			t.Fatalf(".github/workflows/ci.yml missing %q", needle)
		}
	}

	dfRaw, err := os.ReadFile(repoPath("build", "docker", "Dockerfile.server"))
	if err != nil {
		t.Fatalf("missing Dockerfile.server: %v", err)
	}
	df := string(dfRaw)
	if !strings.Contains(df, "WAILS_SERVER_HOST") {
		t.Fatalf("Dockerfile.server missing WAILS_SERVER_HOST")
	}
	if strings.Contains(df, "distroless/static") && !strings.Contains(strings.ToLower(df), "python") {
		t.Fatalf("Dockerfile.server uses distroless/static with zero Python/CAD runtime")
	}
	_ = fmt.Sprintf("")
}
