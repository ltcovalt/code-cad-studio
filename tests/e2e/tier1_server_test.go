//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func extractConfigMap(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if cfg, ok := resp["config"].(map[string]any); ok {
		return cfg
	}
	return resp
}

// ---------- Area E: Config v1, Presets v1, <stem>.presets.json & Watcher (F8, F9, F10) ----------

func TestT1_F8_01_FreshGetConfigSchema(t *testing.T) {
	srv := startServer(t, serverOpts{})
	code, resp := srv.getJSON(t, "/api/config")
	if code != http.StatusOK {
		t.Fatalf("GET /api/config = %d: %v", code, resp)
	}
	if hasDesktop, ok := jsonBool(resp, "has_desktop"); !ok || hasDesktop {
		t.Fatalf("expected has_desktop=false in -tags server mode, got %v", resp["has_desktop"])
	}
	if strings.TrimSpace(jsonStr(resp, "os")) == "" {
		t.Fatalf("expected non-empty top-level 'os' field in GET /api/config: %v", resp)
	}
	cfg := extractConfigMap(t, resp)
	if sv, ok := cfg["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("expected config.schema_version == 1, got %v (%v)", cfg["schema_version"], cfg)
	}
	for _, key := range []string{
		"libraries", "python_path", "theme", "export_quality",
	} {
		if _, exists := cfg[key]; !exists {
			t.Fatalf("GET /api/config missing key %q in config: %v", key, cfg)
		}
	}
}

func TestT1_F8_02_PostConfigPersistenceAcrossRestart(t *testing.T) {
	srv := startServer(t, serverOpts{})
	code, _ := srv.postJSON(t, "/api/config", map[string]any{
		"libraries":          []map[string]string{},
		"python_path":        "",
		"export_dir":         srv.exportsDir,
		"onboarding_skipped": true,
		"editor_command":     "code",
		"slicer_command":     "orca-slicer",
		"export_quality":     "fine",
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/config = %d", code)
	}
	dpCode, _ := srv.postJSON(t, encodeModelRoute("", "/api/models/", "lib/m", "/presets/default"), map[string]any{
		"key": "p1",
	})
	if dpCode != http.StatusOK {
		t.Fatalf("POST /api/models/lib%%2Fm/presets/default = %d", dpCode)
	}

	raw, err := os.ReadFile(srv.configPath)
	if err != nil {
		t.Fatalf("read config.json on disk: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal config.json: %v", err)
	}
	if sv, ok := onDisk["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("disk config.json schema_version = %v, want 1", onDisk["schema_version"])
	}

	srv2 := srv.restart(t, serverOpts{})
	_, after := srv2.getJSON(t, "/api/config")
	cfg := extractConfigMap(t, after)
	if skipped, _ := jsonBool(cfg, "onboarding_skipped"); !skipped {
		t.Fatalf("onboarding_skipped did not persist across restart: %v", cfg)
	}
	if jsonStr(cfg, "editor_command") != "code" || jsonStr(cfg, "slicer_command") != "orca-slicer" || jsonStr(cfg, "export_quality") != "fine" {
		t.Fatalf("UX config fields did not persist: %v", cfg)
	}
	dp := jsonMap(t, cfg["default_presets"], "default_presets")
	if jsonStr(dp, "lib/m") != "p1" {
		t.Fatalf("default_presets did not persist: %v", dp)
	}
}

func TestT1_F8_03_StudioVenvDirPackagesPass(t *testing.T) {
	cmd := exec.Command("go", "test", "-race", "./internal/config/...", "./internal/setup/...")
	cmd.Dir = suiteRepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race ./internal/config/... ./internal/setup/... failed: %v\n%s", err, string(out))
	}
}

func TestT1_F8_04_ConfigAtomicWriteConcurrency(t *testing.T) {
	srv := startServer(t, serverOpts{})
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]any{
				"libraries":      []map[string]string{},
				"export_dir":     srv.exportsDir,
				"editor_command": fmt.Sprintf("ed-%d", idx),
			})
			resp, raw, err := srv.doRaw(http.MethodPost, "/api/config", payload, nil, "")
			if err != nil {
				errs <- err
				return
			}
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("status %d: %s", resp.StatusCode, string(raw))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent POST /api/config failed: %v", err)
	}
	raw, err := os.ReadFile(srv.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("config.json corrupted after concurrent writes: %v\n%s", err, string(raw))
	}
}

func TestT1_F9_01_02_05_PresetsV1EnvelopeVerificationAndDelete(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"box/box.py": `from dataclasses import dataclass
import build123d as b3d

@dataclass
class BoxParams:
    width: float = 10.0

def create_box(params: BoxParams):
    return b3d.Box(params.width, 10.0, 10.0)
`,
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	// T1-F9-01: save project-scoped preset
	saveRoute := encodeModelRoute("", "/api/models/", "mylib/box", "/presets")
	code, saved := srv.postJSON(t, saveRoute, map[string]any{
		"label":       "Wide Box",
		"description": "25mm wide",
		"scope":       "project",
		"params":      map[string]any{"width": 25.0},
	})
	if code != http.StatusOK || !saved["ok"].(bool) {
		t.Fatalf("save preset failed (%d): %v", code, saved)
	}
	selectedKey := jsonStr(saved, "selected_key")
	if selectedKey == "" {
		t.Fatalf("missing selected_key in save preset response: %v", saved)
	}

	presetsPath := filepath.Join(libDir, "box", "presets.json")
	raw, err := os.ReadFile(presetsPath)
	if err != nil {
		t.Fatalf("read %s: %v", presetsPath, err)
	}
	var disk map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("unmarshal presets.json: %v", err)
	}
	if sv, ok := disk["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("presets.json schema_version = %v, want 1", disk["schema_version"])
	}
	presetsMap := jsonMap(t, disk["presets"], "disk.presets")
	if _, ok := presetsMap[selectedKey]; !ok {
		t.Fatalf("saved key %q missing from disk presets.json: %v", selectedKey, presetsMap)
	}

	// T1-F9-02: verification metadata on "default"
	verRoute := encodeModelRoute("", "/api/models/", "mylib/box", "/presets/default/verification")
	vCode, vResp := srv.requestJSON(t, http.MethodPut, verRoute, map[string]any{
		"scope":  "default",
		"status": "verified",
		"note":   "Printed cleanly in PETG",
	})
	if vCode != http.StatusOK || !vResp["ok"].(bool) {
		t.Fatalf("set default verification failed (%d): %v", vCode, vResp)
	}
	raw2, _ := os.ReadFile(presetsPath)
	var disk2 map[string]any
	_ = json.Unmarshal(raw2, &disk2)
	defEntry := jsonMap(t, disk2["default"], "disk.default")
	defVer := jsonMap(t, defEntry["verification"], "disk.default.verification")
	if jsonStr(defVer, "status") != "verified" || jsonStr(defVer, "note") != "Printed cleanly in PETG" {
		t.Fatalf("default verification mismatch on disk: %v", defVer)
	}

	// T1-F9-05: delete saved preset & verify v1 envelope remains intact
	delRoute := encodeModelRoute("", "/api/models/", "mylib/box", "/presets/"+selectedKey+"?scope=project")
	dCode, dResp := srv.requestJSON(t, http.MethodDelete, delRoute, nil)
	if dCode != http.StatusOK || !dResp["ok"].(bool) {
		t.Fatalf("delete preset failed (%d): %v", dCode, dResp)
	}
	raw3, _ := os.ReadFile(presetsPath)
	var disk3 map[string]any
	if err := json.Unmarshal(raw3, &disk3); err != nil {
		t.Fatal(err)
	}
	if sv, ok := disk3["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("schema_version lost after delete: %v", disk3)
	}
	if p3, ok := disk3["presets"].(map[string]any); ok {
		if _, stillThere := p3[selectedKey]; stillThere {
			t.Fatalf("deleted preset %q still present on disk", selectedKey)
		}
	}
}

func TestT1_F9_03_SharedFolderStemPresetsScoping(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"shared/alpha.py": "",
		"shared/beta.py":  "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	cA, rA := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/shared/alpha", "/presets"), map[string]any{
		"label":  "Preset A",
		"scope":  "project",
		"params": map[string]any{"width": 42.0},
	})
	if cA != http.StatusOK || !rA["ok"].(bool) {
		t.Fatalf("save preset alpha failed (%d): %v", cA, rA)
	}
	cB, rB := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/shared/beta", "/presets"), map[string]any{
		"label":  "Preset B",
		"scope":  "project",
		"params": map[string]any{"radius": 15.0},
	})
	if cB != http.StatusOK || !rB["ok"].(bool) {
		t.Fatalf("save preset beta failed (%d): %v", cB, rB)
	}

	alphaFile := filepath.Join(libDir, "shared", "alpha.presets.json")
	betaFile := filepath.Join(libDir, "shared", "beta.presets.json")
	if _, err := os.Stat(filepath.Join(libDir, "shared", "presets.json")); err == nil {
		t.Fatalf("shared folder should use <stem>.presets.json, not shared/presets.json")
	}
	rawA, err := os.ReadFile(alphaFile)
	if err != nil {
		t.Fatalf("missing %s: %v", alphaFile, err)
	}
	rawB, err := os.ReadFile(betaFile)
	if err != nil {
		t.Fatalf("missing %s: %v", betaFile, err)
	}
	if strings.Contains(string(rawA), "Preset B") || strings.Contains(string(rawB), "Preset A") {
		t.Fatalf("cross-contamination between alpha.presets.json and beta.presets.json")
	}
}

func TestT1_F9_04_UserScopedWindowsSafePresetFilenames(t *testing.T) {
	modelSrc := `from dataclasses import dataclass
import build123d as b3d

@dataclass
class PartParams:
    size: float = 10.0

def create_part(params: PartParams):
    return b3d.Box(params.size, 10.0, 10.0)
`
	libDir := setupTempModelLib(t, map[string]string{
		"sub/part.py":   modelSrc,
		"other/part.py": modelSrc,
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "lib", "name": "Lib", "path": libDir}}, "")

	c1, r1 := srv.postJSON(t, encodeModelRoute("", "/api/models/", "lib/sub/part", "/presets"), map[string]any{
		"label":  "Sub Preset",
		"scope":  "user",
		"params": map[string]any{"size": 21.0},
	})
	if c1 != http.StatusOK || !r1["ok"].(bool) {
		t.Fatalf("save user preset 1 failed (%d): %v", c1, r1)
	}
	c2, r2 := srv.postJSON(t, encodeModelRoute("", "/api/models/", "lib/other/part", "/presets"), map[string]any{
		"label":  "Other Preset",
		"scope":  "user",
		"params": map[string]any{"size": 33.0},
	})
	if c2 != http.StatusOK || !r2["ok"].(bool) {
		t.Fatalf("save user preset 2 failed (%d): %v", c2, r2)
	}

	userLibDir := filepath.Join(srv.xdgConfigHome, "code-cad-studio", "presets", "lib")
	entries, err := os.ReadDir(userLibDir)
	if err != nil {
		t.Fatalf("readdir %s: %v", userLibDir, err)
	}
	pat := regexp.MustCompile(`^[a-zA-Z0-9._-]+--[0-9a-f]{8}\.json$`)
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !pat.MatchString(e.Name()) {
			t.Fatalf("user preset filename %q does not match <safe_slug>--<8hex>.json", e.Name())
		}
		files = append(files, e.Name())
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 non-colliding user preset files for lib/sub/part and lib/other/part, got %v", files)
	}
}

func TestT1_F10_01_02_03_04_WatcherSSEAndFiltering(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"box/box.py": `from dataclasses import dataclass
import build123d as b3d
from components.helper import make_dim

@dataclass
class BoxParams:
    size: float = 10.0

def create_box(params: BoxParams):
    w = make_dim(params.size)
    return b3d.Box(w, 10.0, 10.0)
`,
		"components/helper.py": "def make_dim(v: float) -> float:\n    return v\n",
		"shared/alpha.py":      "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	// T1-F10-04: writes in exports/ and __pycache__/ must NOT emit file-changed
	_ = os.MkdirAll(filepath.Join(libDir, "exports"), 0o755)
	_ = os.MkdirAll(filepath.Join(libDir, "__pycache__"), 0o755)
	_ = os.WriteFile(filepath.Join(libDir, "exports", "part.stl"), []byte("solid x\nendsolid x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(libDir, "__pycache__", "box.cpython-314.pyc"), []byte("pyc"), 0o644)
	select {
	case evt := <-sse.events:
		if evt.Event == "file-changed" {
			t.Fatalf("unexpected file-changed event for ignored directory: %+v", evt)
		}
	case <-time.After(1200 * time.Millisecond):
	}

	// T1-F10-01: .py edit emits file-changed with id: <seq>
	boxFile := filepath.Join(libDir, "box", "box.py")
	origBox, _ := os.ReadFile(boxFile)
	_ = os.WriteFile(boxFile, append(origBox, []byte("\n# edit\n")...), 0o644)
	evt1 := sse.waitForEvent(t, 4*time.Second, func(e sseEvent) bool { return e.Event == "file-changed" })
	if evt1.ID == "" {
		t.Fatalf("file-changed SSE missing id: field: %+v", evt1)
	}

	// T1-F10-02: components/ edit triggers reload & reflected in render
	helperFile := filepath.Join(libDir, "components", "helper.py")
	_ = os.WriteFile(helperFile, []byte("def make_dim(v: float) -> float:\n    return v * 3.0\n"), 0o644)
	_ = sse.waitForEvent(t, 4*time.Second, func(e sseEvent) bool { return e.Event == "file-changed" })
	_, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/box", "/render"), map[string]any{"params": map[string]any{"size": 10.0}})
	bb := jsonMap(t, rResp["bbox"], "bbox")
	if dx, _ := bb["dx"].(float64); dx != 30.0 {
		t.Fatalf("after components/helper.py edit, expected bbox dx=30, got %v", bb)
	}

	// T1-F10-03: presets.json and <stem>.presets.json edits emit presets-changed
	_ = os.WriteFile(filepath.Join(libDir, "box", "presets.json"), []byte(`{"schema_version":1,"presets":{}}`), 0o644)
	_ = sse.waitForEvent(t, 4*time.Second, func(e sseEvent) bool { return e.Event == "presets-changed" })
	_ = os.WriteFile(filepath.Join(libDir, "shared", "alpha.presets.json"), []byte(`{"schema_version":1,"presets":{}}`), 0o644)
	_ = sse.waitForEvent(t, 4*time.Second, func(e sseEvent) bool { return e.Event == "presets-changed" })
}

// ---------- Area G: Supervision, Setup Hardening, Security Middleware, SSE & -tags server (F12, F13, F14) ----------

func TestT1_F12_01_WorkerKillAutoRecovery(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	pids := srv.findWorkerPIDs(t)
	if len(pids) == 0 {
		t.Fatalf("expected running child Python worker PID")
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	time.Sleep(300 * time.Millisecond)

	mCode, mResp := srv.getJSON(t, "/api/models")
	if mCode != http.StatusOK || !mResp["ok"].(bool) {
		t.Fatalf("GET /api/models after worker SIGKILL failed (%d): %v", mCode, mResp)
	}
	rCode, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{"params": map[string]any{}})
	if rCode != http.StatusOK || !rResp["ok"].(bool) {
		t.Fatalf("render after worker SIGKILL failed (%d): %v", rCode, rResp)
	}
}

func TestT1_F12_02_CleanShutdownReapsWorker(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	pids := srv.findWorkerPIDs(t)
	if len(pids) == 0 {
		t.Fatalf("expected child worker PID before shutdown")
	}
	srv.stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		anyAlive := false
		for _, pid := range pids {
			if isProcessAlive(pid) {
				anyAlive = true
				break
			}
		}
		if !anyAlive {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("worker PIDs %v still alive 5s after server SIGTERM", pids)
}

func TestT1_F12_03_CancelInfiniteLoopAndRecover(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"infinite_loop.py":    "",
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	loopDone := make(chan int, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"params": map[string]any{"loop": true}})
		resp, _, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/infinite_loop", "/render"), b, nil, "")
		if err != nil {
			loopDone <- -1
			return
		}
		loopDone <- resp.StatusCode
	}()

	time.Sleep(500 * time.Millisecond)
	cCode, cResp := srv.postJSON(t, "/api/render/cancel", map[string]any{})
	if cCode != http.StatusOK {
		t.Fatalf("POST /api/render/cancel = %d: %v", cCode, cResp)
	}

	select {
	case <-loopDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("in-flight infinite-loop render did not unblock within 5s after cancel")
	}

	rCode, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{"params": map[string]any{}})
	if rCode != http.StatusOK || !rResp["ok"].(bool) {
		t.Fatalf("subsequent valid render after cancel failed (%d): %v", rCode, rResp)
	}
}

func TestT1_F12_04_And_F13_03_SetupRaceSuite(t *testing.T) {
	cmd := exec.Command("go", "test", "-race", "./internal/setup/...")
	cmd.Dir = suiteRepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race ./internal/setup/... failed: %v\n%s", err, string(out))
	}
}

func TestT1_F12_05_InvalidPythonPathRecovery(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	code, badResp := srv.postJSON(t, "/api/config", map[string]any{
		"libraries":   []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		"python_path": "/nonexistent/python-missing-e2e",
		"export_dir":  srv.exportsDir,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/config with bad python crashed or returned %d: %v", code, badResp)
	}
	if strings.TrimSpace(jsonStr(badResp, "worker_error")) == "" {
		t.Fatalf("expected non-empty worker_error for invalid python_path, got %v", badResp)
	}

	// Restore valid python_path and confirm recovery
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, resolvePython(t))
	rCode, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{"params": map[string]any{}})
	if rCode != http.StatusOK || !rResp["ok"].(bool) {
		t.Fatalf("render after restoring valid python_path failed (%d): %v", rCode, rResp)
	}
}

func TestT1_F13_01_AllGoPackagesRaceFree(t *testing.T) {
	cmd := exec.Command("go", "test", "-race", "./...")
	cmd.Dir = suiteRepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race ./... failed: %v\n%s", err, string(out))
	}
}

func TestT1_F13_02_FreshServerConfigAndSetupProbe(t *testing.T) {
	srv := startServer(t, serverOpts{})
	code, cfg := srv.getJSON(t, "/api/config")
	if code != http.StatusOK {
		t.Fatalf("GET /api/config = %d: %v", code, cfg)
	}
	pCode, pResp := srv.postJSON(t, "/api/setup/probe", map[string]any{"library_roots": []string{}})
	if pCode != http.StatusOK {
		t.Fatalf("POST /api/setup/probe = %d: %v", pCode, pResp)
	}
}

func TestT1_F14_01_AllowedHostHeaders(t *testing.T) {
	srv := startServer(t, serverOpts{})
	hosts := []string{
		fmt.Sprintf("localhost:%d", srv.port),
		fmt.Sprintf("127.0.0.1:%d", srv.port),
		fmt.Sprintf("[::1]:%d", srv.port),
	}
	for _, h := range hosts {
		resp, raw, err := srv.doRaw(http.MethodGet, "/api/config", nil, nil, h)
		if err != nil {
			t.Fatalf("Host %q failed: %v", h, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Host %q status = %d, want 200 (body: %s)", h, resp.StatusCode, string(raw))
		}
	}

	// Desktop-only internal host must be rejected with 403 on -tags server
	// (wails.localhost is verified against DesktopMiddleware in internal/server/handlers_test.go).
	for _, disallowed := range []string{"wails", fmt.Sprintf("wails:%d", srv.port)} {
		resp, raw, err := srv.doRaw(http.MethodGet, "/api/config", nil, nil, disallowed)
		if err != nil {
			t.Fatalf("Host %q failed: %v", disallowed, err)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Host %q status = %d, want 403 (body: %s)", disallowed, resp.StatusCode, string(raw))
		}
	}
}

func TestT1_F14_02_CSPSecurityHeaders(t *testing.T) {
	srv := startServer(t, serverOpts{})
	for _, route := range []string{"/", "/api/config"} {
		resp, _, err := srv.doRaw(http.MethodGet, route, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", route, resp.StatusCode)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		for _, directive := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, directive) {
				t.Fatalf("GET %s CSP %q missing %q", route, csp, directive)
			}
		}
	}
}

func TestT1_F14_03_StandardizedJSONErrorEnvelope(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	resp, raw, err := srv.doRaw(http.MethodGet, encodeModelRoute("", "/api/models/", "missing/part", ""), nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("missing model status = %d, want 4xx (body: %s)", resp.StatusCode, string(raw))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("error Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("error body not valid JSON: %v", err)
	}
	if ok, exists := jsonBool(body, "ok"); !exists || ok {
		t.Fatalf("expected ok=false in error envelope, got %v", body)
	}
	if strings.TrimSpace(jsonStr(body, "error")) == "" {
		t.Fatalf("expected non-empty error string in envelope: %v", body)
	}
}

func TestT1_F14_04_SSEIDsAndKeepalive(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	// Trigger file change and check id: <integer>
	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")
	orig, _ := os.ReadFile(pyFile)
	_ = os.WriteFile(pyFile, append(orig, []byte("\n# sse-id-check\n")...), 0o644)
	evt := sse.waitForEvent(t, 4*time.Second, func(e sseEvent) bool { return e.Event == "file-changed" })
	if _, err := strconv.Atoi(evt.ID); err != nil {
		t.Fatalf("expected integer SSE id, got %q", evt.ID)
	}

	// Verify : keepalive heartbeat arrives within 17s (15s contract)
	if os.Getenv("E2E_SKIP_SSE_KEEPALIVE") != "1" {
		ka := sse.waitForEvent(t, 17*time.Second, func(e sseEvent) bool {
			return strings.Contains(e.Comment, "keepalive")
		})
		if !strings.Contains(ka.Comment, "keepalive") {
			t.Fatalf("unexpected keepalive comment: %+v", ka)
		}
	}
}

func TestT1_F14_05_06_07_ServerModeDesktopDisabledAndDownload(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	// T1-F14-05: has_desktop: false
	_, cfgResp := srv.getJSON(t, "/api/config")
	if hd, ok := jsonBool(cfgResp, "has_desktop"); !ok || hd {
		t.Fatalf("expected has_desktop=false, got %v", cfgResp["has_desktop"])
	}

	// T1-F14-06: browser download export
	body, _ := json.Marshal(map[string]any{
		"params":  map[string]any{},
		"format":  "stl",
		"quality": "standard",
	})
	resp, raw, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/export"), body, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browser export status = %d: %s", resp.StatusCode, string(raw))
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".stl") {
		t.Fatalf("unexpected Content-Disposition: %q", cd)
	}
	if len(raw) == 0 {
		t.Fatalf("exported STL download was empty")
	}

	// T1-F14-07: desktop endpoints gated cleanly in -tags server
	for _, route := range []string{"/api/desktop/open-editor", "/api/desktop/reveal-file", "/api/desktop/open-slicer"} {
		payload, _ := json.Marshal(map[string]any{"path": filepath.Join(libDir, "stdlib_dataclass.py")})
		dResp, dRaw, dErr := srv.doRaw(http.MethodPost, route, payload, nil, "")
		if dErr != nil {
			t.Fatalf("%s failed: %v", route, dErr)
		}
		if dResp.StatusCode < 400 {
			t.Fatalf("%s in -tags server mode returned %d, want 4xx error envelope (body: %s)", route, dResp.StatusCode, string(dRaw))
		}
		var errEnv map[string]any
		if err := json.Unmarshal(dRaw, &errEnv); err != nil {
			t.Fatalf("%s did not return JSON error envelope: %v", route, err)
		}
		if ok, exists := jsonBool(errEnv, "ok"); !exists || ok {
			t.Fatalf("%s expected ok=false, got %v", route, errEnv)
		}
	}
}

func TestT1_F14_08_ScaffoldOfflineDemo(t *testing.T) {
	srv := startServer(t, serverOpts{useMockCAD: true})
	target := filepath.Join(t.TempDir(), "offline_demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	code, resp := srv.postJSON(t, "/api/models/scaffold", map[string]any{
		"mode":       "demo",
		"target_dir": target,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/models/scaffold (demo) = %d: %v", code, resp)
	}

	for _, rel := range []string{
		"models/spool_cap/spool_cap.py",
		"models/spool_cap/presets.json",
		"components/chamfer_base.py",
		"utils/params.py",
	} {
		if _, err := os.Stat(filepath.Join(target, rel)); err != nil {
			t.Fatalf("demo scaffold missing %s: %v", rel, err)
		}
	}
	rawPresets, _ := os.ReadFile(filepath.Join(target, "models", "spool_cap", "presets.json"))
	var pJSON map[string]any
	if err := json.Unmarshal(rawPresets, &pJSON); err != nil || int(pJSON["schema_version"].(float64)) != 1 {
		t.Fatalf("scaffolded spool_cap presets.json not schema_version 1: %s", string(rawPresets))
	}

	srv.configure(t, []map[string]string{{"id": "demo", "name": "Demo", "path": target}}, "")
	_, models := srv.getJSON(t, "/api/models")
	if !strings.Contains(fmt.Sprint(models["models"]), "demo/spool_cap") {
		t.Fatalf("scaffolded demo/spool_cap not discovered: %v", models)
	}
}

func TestT1_F14_09_ScaffoldNewCustomModel(t *testing.T) {
	srv := startServer(t, serverOpts{useMockCAD: true})
	target := filepath.Join(t.TempDir(), "custom_lib")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	srv.configure(t, []map[string]string{{"id": "custom", "name": "Custom", "path": target}}, "")

	code, resp := srv.postJSON(t, "/api/models/scaffold", map[string]any{
		"mode":       "new",
		"library_id": "custom",
		"name":       "my_bracket",
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/models/scaffold (new) = %d: %v", code, resp)
	}

	for _, rel := range []string{"models/my_bracket/my_bracket.py", "utils/params.py"} {
		if _, err := os.Stat(filepath.Join(target, rel)); err != nil {
			t.Fatalf("new model scaffold missing %s: %v", rel, err)
		}
	}

	_, mResp := srv.getJSON(t, "/api/models")
	models := jsonSlice(t, mResp["models"], "models")
	found := false
	for _, item := range models {
		im := jsonMap(t, item, "m")
		if jsonStr(im, "id") == "custom/my_bracket" && jsonStr(im, "kind") == "parametric" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("custom/my_bracket not discovered as kind=parametric: %v", models)
	}
}
