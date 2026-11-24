//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// T3-01: Concurrent Preset Mutations + SSE (F9 + F10 + F14)
func TestT3_01_ConcurrentPresetMutationsAndSSE(t *testing.T) {
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

	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	route := encodeModelRoute("", "/api/models/", "mylib/box", "/presets")
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	keys := make([]string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]any{
				"label":  fmt.Sprintf("p%d", idx),
				"scope":  "project",
				"params": map[string]any{"width": float64(20 + idx)},
			})
			resp, raw, err := srv.doRaw(http.MethodPost, route, payload, nil, "")
			if err != nil {
				errs <- err
				return
			}
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("p%d status %d: %s", idx, resp.StatusCode, string(raw))
				return
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil || body["ok"] != true {
				errs <- fmt.Errorf("p%d bad response: %s", idx, string(raw))
				return
			}
			keys[idx] = jsonStr(body, "selected_key")
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent preset save failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(libDir, "box", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if sv, ok := disk["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("presets.json schema_version = %v, want 1", disk["schema_version"])
	}
	presetsMap := jsonMap(t, disk["presets"], "disk.presets")
	for i := 0; i < 10; i++ {
		if _, ok := presetsMap[keys[i]]; !ok {
			t.Fatalf("lost preset update for p%d (%q) in %v", i, keys[i], presetsMap)
		}
	}

	// Verify SSE receives presets-changed with strictly increasing integer IDs
	evt := sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "presets-changed" })
	prevID, err := strconv.Atoi(evt.ID)
	if err != nil {
		t.Fatalf("presets-changed SSE missing integer id: %+v", evt)
	}
	// Trigger one more presets edit to assert strictly increasing sequence ID
	_, _ = srv.postJSON(t, route, map[string]any{
		"label":  "p_seq_check",
		"scope":  "project",
		"params": map[string]any{"width": 99.0},
	})
	evt2 := sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "presets-changed" })
	nextID, err := strconv.Atoi(evt2.ID)
	if err != nil || nextID <= prevID {
		t.Fatalf("expected strictly increasing SSE id > %d, got %q", prevID, evt2.ID)
	}
}

// T3-02: Config Reload + Worker Reconfigure + Auth (F1 + F8 + F12 + F14)
func TestT3_02_ConfigReloadWorkerReconfigureAndRecovery(t *testing.T) {
	libA := setupTempModelLib(t, map[string]string{
		"part_a.py": `from dataclasses import dataclass
import build123d as b3d

@dataclass
class PartAParams:
    size: float = 10.0

def create_part_a(params: PartAParams):
    return b3d.Box(params.size, 10.0, 10.0)
`,
	})
	libB := setupTempModelLib(t, map[string]string{
		"part_b.py": `from dataclasses import dataclass
import build123d as b3d

@dataclass
class PartBParams:
    size: float = 20.0

def create_part_b(params: PartBParams):
    return b3d.Box(params.size, 20.0, 20.0)
`,
	})

	srv := startServer(t, serverOpts{useMockCAD: true})
	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	srv.configure(t, []map[string]string{{"id": "lib-a", "name": "LibA", "path": libA}}, "")
	_, mA := srv.getJSON(t, "/api/models")
	if !strings.Contains(fmt.Sprint(mA["models"]), "lib-a/part_a") {
		t.Fatalf("expected lib-a/part_a, got %v", mA)
	}

	// Switch libraries to lib-b
	srv.configure(t, []map[string]string{{"id": "lib-b", "name": "LibB", "path": libB}}, "")
	_, mB := srv.getJSON(t, "/api/models")
	mBText := fmt.Sprint(mB["models"])
	if !strings.Contains(mBText, "lib-b/part_b") || strings.Contains(mBText, "lib-a/part_a") {
		t.Fatalf("expected only lib-b/part_b after library switch, got %v", mB)
	}

	// Switch python_path to invalid -> worker-error SSE emitted
	_, _ = srv.postJSON(t, "/api/config", map[string]any{
		"libraries":   []map[string]string{{"id": "lib-b", "name": "LibB", "path": libB}},
		"python_path": "/nonexistent/bad-python",
		"export_dir":  srv.exportsDir,
	})
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "worker-error" })

	// Restore valid python_path -> auto-recovers and renders lib-b/part_b
	srv.configure(t, []map[string]string{{"id": "lib-b", "name": "LibB", "path": libB}}, resolvePython(t))
	rCode, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "lib-b/part_b", "/render"), map[string]any{"params": map[string]any{}})
	if rCode != http.StatusOK || !rResp["ok"].(bool) {
		t.Fatalf("lib-b/part_b render after python_path recovery failed (%d): %v", rCode, rResp)
	}
}

// T3-03: Scaffold -> Discover -> Render -> Export -> Preset Verify (F5 + F6 + F7 + F9 + F14)
func TestT3_03_ScaffoldDiscoverRenderExportPresetVerify(t *testing.T) {
	srv := startServer(t, serverOpts{useMockCAD: true})
	libDir := filepath.Join(t.TempDir(), "fresh_lib")
	_ = os.MkdirAll(libDir, 0o755)
	srv.configure(t, []map[string]string{{"id": "fresh", "name": "Fresh", "path": libDir}}, "")

	sCode, sResp := srv.postJSON(t, "/api/models/scaffold", map[string]any{
		"mode":       "new",
		"library_id": "fresh",
		"name":       "my_part",
	})
	if sCode != http.StatusOK {
		t.Fatalf("scaffold new model failed (%d): %v", sCode, sResp)
	}
	if _, err := os.Stat(filepath.Join(libDir, "models", "my_part", "my_part.py")); err != nil {
		t.Fatalf("scaffolded models/my_part/my_part.py missing: %v", err)
	}

	demoHash := sha256HexFile(t, repoPath("examples", "demo_project", "utils", "params.py"))
	scaffoldHash := sha256HexFile(t, filepath.Join(libDir, "utils", "params.py"))
	if scaffoldHash != demoHash {
		t.Fatalf("scaffolded params.py hash mismatch (%s != %s)", scaffoldHash, demoHash)
	}

	_, list := srv.getJSON(t, "/api/models")
	models := jsonSlice(t, list["models"], "models")
	if len(models) != 1 || jsonStr(jsonMap(t, models[0], "m"), "id") != "fresh/my_part" || jsonStr(jsonMap(t, models[0], "m"), "kind") != "parametric" {
		t.Fatalf("unexpected discovered models: %v", models)
	}

	dCode, detail := srv.getJSON(t, encodeModelRoute("", "/api/models/", "fresh/my_part", ""))
	if dCode != http.StatusOK || !detail["ok"].(bool) {
		t.Fatalf("GET detail failed (%d): %v", dCode, detail)
	}

	rCode, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "fresh/my_part", "/render"), map[string]any{"params": map[string]any{}})
	if rCode != http.StatusOK || !rResp["ok"].(bool) {
		t.Fatalf("render failed (%d): %v", rCode, rResp)
	}
	if _, hasShapes := rResp["shapes"]; !hasShapes {
		t.Fatalf("render missing shapes")
	}

	expPayload, _ := json.Marshal(map[string]any{"params": map[string]any{}, "format": "stl", "quality": "fine"})
	expResp, expRaw, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "fresh/my_part", "/export"), expPayload, nil, "")
	if err != nil || expResp.StatusCode != http.StatusOK || len(expRaw) == 0 {
		t.Fatalf("export STL failed (%v, %v): %s", err, expResp, string(expRaw))
	}
	_ = decodeSTLTriangleCount(t, expRaw)

	pCode, pResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "fresh/my_part", "/presets"), map[string]any{
		"label":        "Verified Fine",
		"scope":        "project",
		"params":       jsonMap(t, detail["defaults"], "defaults"),
		"verification": map[string]any{"status": "verified", "note": "E2E verified"},
	})
	if pCode != http.StatusOK || !pResp["ok"].(bool) {
		t.Fatalf("save verified preset failed (%d): %v", pCode, pResp)
	}
}

// T3-04: update-defaults -> Watcher -> Cache Invalidation -> Render (F2 + F4 + F5 + F10)
func TestT3_04_UpdateDefaultsWatcherCacheInvalidationRender(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")

	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	renderRoute := encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render")
	_, r1 := srv.postJSON(t, renderRoute, map[string]any{"params": map[string]any{}})
	if cached, exists := jsonBool(r1, "cached"); exists && cached {
		t.Fatalf("first render expected cached=false")
	}
	_, r2 := srv.postJSON(t, renderRoute, map[string]any{"params": map[string]any{}})
	if cached, _ := jsonBool(r2, "cached"); !cached {
		t.Fatalf("second render expected cached=true")
	}

	curSHA := sha256HexFile(t, pyFile)
	uCode, uResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/update-defaults"), map[string]any{
		"params":          map[string]any{"width": 25.0},
		"dry_run":         false,
		"expected_sha256": curSHA,
	})
	if uCode != http.StatusOK || !uResp["ok"].(bool) {
		t.Fatalf("update-defaults failed (%d): %v", uCode, uResp)
	}

	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "file-changed" })

	_, detail := srv.getJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", ""))
	defaults := jsonMap(t, detail["defaults"], "defaults")
	if defaults["width"] != 25.0 {
		t.Fatalf("detail defaults.width = %v, want 25.0", defaults["width"])
	}

	_, r3 := srv.postJSON(t, renderRoute, map[string]any{"params": map[string]any{}})
	if cached, exists := jsonBool(r3, "cached"); exists && cached {
		t.Fatalf("render after update-defaults should invalidate cache (cached=false), got true")
	}
	bb := jsonMap(t, r3["bbox"], "r3.bbox")
	if bb["dx"] != 25.0 {
		t.Fatalf("expected updated bbox dx=25.0 after update-defaults, got %v", bb)
	}
}

// T3-05: Infinite-Loop Build Cancel -> Respawn -> Subsequent Render & SSE Health
func TestT3_05_InfiniteLoopCancelRespawnAndSSEHealth(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"infinite_loop.py":    "",
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	goroutineADone := make(chan int, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"params": map[string]any{"loop": true}})
		resp, _, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/infinite_loop", "/render"), b, nil, "")
		if err != nil {
			goroutineADone <- -1
			return
		}
		goroutineADone <- resp.StatusCode
	}()

	time.Sleep(400 * time.Millisecond)
	cCode, _ := srv.postJSON(t, "/api/render/cancel", map[string]any{})
	if cCode != http.StatusOK {
		t.Fatalf("cancel status = %d", cCode)
	}

	select {
	case <-goroutineADone:
	case <-time.After(5 * time.Second):
		t.Fatalf("goroutine A did not unblock within 5s of cancel")
	}

	// Verify SSE stayed connected and receives subsequent file-changed event
	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")
	orig, _ := os.ReadFile(pyFile)
	_ = os.WriteFile(pyFile, append(orig, []byte("\n# after-cancel\n")...), 0o644)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "file-changed" })

	// Goroutine B renders valid model immediately
	bDone := make(chan bool, 1)
	go func() {
		code, resp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{"params": map[string]any{}})
		bDone <- (code == http.StatusOK && resp["ok"] == true)
	}()
	if !<-bDone {
		t.Fatalf("goroutine B render after cancel failed")
	}
}

// T3-06: UTF-8 BOM + PEP 604 + Optional[bool] "" + Presets Round-Trip
func TestT3_06_UTF8BOMTypingEmptyBoolAndPresetsRoundTrip(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"typing_bom.py": ""})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	pRoute := encodeModelRoute("", "/api/models/", "mylib/typing_bom", "/presets")
	sCode, sResp := srv.postJSON(t, pRoute, map[string]any{
		"label":  "BOM Combo",
		"scope":  "project",
		"params": map[string]any{"opt_float": 12.5, "opt_bool": nil, "tags": []string{"m3"}},
	})
	if sCode != http.StatusOK || !sResp["ok"].(bool) {
		t.Fatalf("save BOM preset failed (%d): %v", sCode, sResp)
	}

	_, gResp := srv.getJSON(t, pRoute)
	if ok, _ := jsonBool(gResp, "ok"); !ok {
		t.Fatalf("reload presets failed: %v", gResp)
	}
	rawDisk, err := os.ReadFile(filepath.Join(libDir, "typing_bom.presets.json"))
	if err != nil {
		t.Fatalf("expected typing_bom.presets.json: %v", err)
	}
	var disk map[string]any
	_ = json.Unmarshal(rawDisk, &disk)
	if sv, ok := disk["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("typing_bom.presets.json schema_version = %v, want 1", disk["schema_version"])
	}

	// Render with opt_bool="" and opt_float=12.5 -> verify opt_bool coerced to None (not False!)
	rCode, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/typing_bom", "/render"), map[string]any{
		"params": map[string]any{"opt_float": 12.5, "opt_bool": "", "tags": []string{"m3"}},
	})
	if rCode != http.StatusOK || !rResp["ok"].(bool) {
		t.Fatalf("BOM render failed (%d): %v", rCode, rResp)
	}
	shapes := jsonMap(t, rResp["shapes"], "shapes")
	parts := jsonSlice(t, shapes["parts"], "parts")
	partName := jsonStr(jsonMap(t, parts[0], "part0"), "name")
	if !strings.Contains(partName, "flag_none") || !strings.Contains(partName, "tags_1") {
		t.Fatalf("expected part label reflecting opt_bool=None and 1 tag, got %q", partName)
	}
}
