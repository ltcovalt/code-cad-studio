//go:build e2e

package e2e_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// T2-01: Direct worker POST with Content-Length exceeding cap (50MB) rejected immediately.
func TestT2_01_WorkerContentLengthCap(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", w.port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial worker: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	route := encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/render")
	rawReq := fmt.Sprintf(
		"POST %s HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 50000000\r\nConnection: close\r\n\r\n{\"params\":",
		route, w.port, w.token,
	)
	if _, err := conn.Write([]byte(rawReq)); err != nil {
		t.Fatalf("write raw request: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("expected immediate 413/400 response without reading 50MB, got err: %v", err)
	}
	if !strings.Contains(statusLine, "413") && !strings.Contains(statusLine, "400") {
		t.Fatalf("status line = %q, want 413 or 400", strings.TrimSpace(statusLine))
	}

	// Worker must remain healthy afterwards
	hCode, hResp := w.getJSON(t, "/health")
	if hCode != http.StatusOK || !hResp["ok"].(bool) {
		t.Fatalf("worker unhealthy after oversized Content-Length: %d %v", hCode, hResp)
	}
}

// T2-02: Worker rejects missing/empty Host or empty Bearer token.
func TestT2_02_WorkerMissingHostOrEmptyBearerToken(t *testing.T) {
	py := resolvePython(t)
	w := startWorker(t, py, workerOpts{})

	// 1. Empty Bearer token
	emptyTok := ""
	req, _ := http.NewRequest(http.MethodGet, w.baseURL+"/health", nil)
	req.Header.Set("Authorization", "Bearer ")
	resp, err := w.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("empty Bearer token status = %d, want 401/403 (body: %s)", resp.StatusCode, string(raw))
	}
	_ = emptyTok

	// 2. Missing/empty Host via raw HTTP/1.1 TCP
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", w.port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	rawReq := fmt.Sprintf("GET /health HTTP/1.1\r\nHost:\r\nAuthorization: Bearer %s\r\nConnection: close\r\n\r\n", w.token)
	_, _ = conn.Write([]byte(rawReq))
	respBytes, _ := io.ReadAll(conn)
	respStr := string(respBytes)
	if !strings.Contains(respStr, "400") && !strings.Contains(respStr, "403") {
		t.Fatalf("empty Host expected 400/403, got:\n%s", respStr)
	}
}

// T2-03: update-defaults with dry_run=false and missing/empty expected_sha256 rejected.
func TestT2_03_UpdateDefaultsMissingExpectedSHA256Rejected(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")
	origSHA := sha256HexFile(t, pyFile)

	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})
	route := encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/update-defaults")

	b, _ := json.Marshal(map[string]any{
		"params":          map[string]any{"width": 99.0},
		"dry_run":         false,
		"expected_sha256": "",
	})
	resp, raw, err := w.doRaw(http.MethodPost, route, b, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	ok, _ := jsonBool(body, "ok")
	if resp.StatusCode == http.StatusOK && ok {
		t.Fatalf("expected non-dry-run update-defaults without expected_sha256 to be rejected, got 200 ok=true: %s", string(raw))
	}
	if gotSHA := sha256HexFile(t, pyFile); gotSHA != origSHA {
		t.Fatalf("file on disk mutated despite missing expected_sha256")
	}
}

// T2-04: update-defaults with stale expected_sha256 rejected (409 / ok=false) and preserves file.
func TestT2_04_UpdateDefaultsStaleSHA256Rejected(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")
	staleSHA := sha256HexFile(t, pyFile)

	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})

	// Externally edit file on disk
	origBytes, _ := os.ReadFile(pyFile)
	modified := append(origBytes, []byte("\n# external edit\n")...)
	if err := os.WriteFile(pyFile, modified, 0o644); err != nil {
		t.Fatal(err)
	}
	modifiedSHA := sha256HexFile(t, pyFile)

	route := encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/update-defaults")
	b, _ := json.Marshal(map[string]any{
		"params":          map[string]any{"width": 77.0},
		"dry_run":         false,
		"expected_sha256": staleSHA,
	})
	resp, raw, err := w.doRaw(http.MethodPost, route, b, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	ok, _ := jsonBool(body, "ok")
	if resp.StatusCode == http.StatusOK && ok {
		t.Fatalf("expected stale expected_sha256 to fail, got 200 ok=true: %s", string(raw))
	}
	if sha256HexFile(t, pyFile) != modifiedSHA {
		t.Fatalf("file on disk overwritten despite stale SHA-256")
	}
}

// T2-05: update-defaults on UTF-8 BOM model preserves valid UTF-8 and Python AST.
func TestT2_05_UpdateDefaultsUTF8BOMPreservesAST(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{"typing_bom.py": ""})
	pyFile := filepath.Join(libDir, "typing_bom.py")
	origSHA := sha256HexFile(t, pyFile)

	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})
	route := encodeModelRoute("", "/models/", "mylib/typing_bom", "/update-defaults")
	code, resp := w.postJSON(t, route, map[string]any{
		"params":          map[string]any{"width": 22.0},
		"dry_run":         false,
		"expected_sha256": origSHA,
	})
	if code != http.StatusOK || !resp["ok"].(bool) {
		t.Fatalf("BOM update-defaults failed (%d): %v", code, resp)
	}

	compileCmd := exec.Command(py, "-m", "py_compile", pyFile)
	if out, err := compileCmd.CombinedOutput(); err != nil {
		t.Fatalf("py_compile failed on BOM-updated file: %v\n%s", err, string(out))
	}
	_, schema := w.getJSON(t, encodeModelRoute("", "/models/", "mylib/typing_bom", "/schema"))
	defaults := jsonMap(t, schema["defaults"], "defaults")
	if defaults["width"] != 22.0 {
		t.Fatalf("BOM updated width = %v, want 22.0", defaults["width"])
	}
}

// T2-06: bool | None coercion across "", "true", "false", true, null.
func TestT2_06_BoolUnionCoercionMatrix(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{"typing_bom.py": ""})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})
	route := encodeModelRoute("", "/models/", "mylib/typing_bom", "/export-name")
	cases := []struct {
		input    any
		wantStem string
	}{
		{"", "flag_none"},
		{nil, "flag_none"},
		{"true", "flag_true"},
		{true, "flag_true"},
		{"false", "flag_false"},
		{false, "flag_false"},
	}
	for _, tc := range cases {
		code, resp := w.postJSON(t, route, map[string]any{
			"params": map[string]any{"opt_bool": tc.input},
		})
		if code != http.StatusOK || !resp["ok"].(bool) {
			t.Fatalf("export-name for %#v failed (%d): %v", tc.input, code, resp)
		}
		if stem := jsonStr(resp, "export_filename"); !strings.Contains(stem, tc.wantStem) {
			t.Fatalf("opt_bool=%#v -> export_filename=%q, want substring %q", tc.input, stem, tc.wantStem)
		}
	}
}

// T2-07: Render 25+ distinct parameterized geometries exceeding _render_cache budget.
func TestT2_07_RenderCacheEvictionAndMemoryBound(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})
	route := encodeModelRoute("", "/models/", "mylib/stdlib_dataclass", "/render")
	for i := 0; i < 30; i++ {
		code, resp := w.postJSON(t, route, map[string]any{
			"params": map[string]any{"width": float64(10 + i)},
		})
		if code != http.StatusOK || !resp["ok"].(bool) {
			t.Fatalf("render %d failed (%d): %v", i, code, resp)
		}
	}
	// Oldest entry (width=10.0) should have been evicted from bounded cache
	_, again := w.postJSON(t, route, map[string]any{
		"params": map[string]any{"width": 10.0},
	})
	if ok, _ := jsonBool(again, "ok"); !ok {
		t.Fatalf("re-render oldest entry failed: %v", again)
	}
	if cached, exists := jsonBool(again, "cached"); exists && cached {
		t.Fatalf("expected oldest entry (width=10.0) to be evicted after 30 distinct renders, got cached=true")
	}
}

// T2-08: SyntaxError vs RuntimeError (ZeroDivisionError) isolation.
func TestT2_08_SyntaxErrorVsRuntimeErrorIsolation(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{
		"syntax_error.py":  "",
		"runtime_error.py": "",
	})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})

	_, mResp := w.getJSON(t, "/models")
	models := jsonSlice(t, mResp["models"], "models")
	byID := map[string]map[string]any{}
	for _, m := range models {
		mm := jsonMap(t, m, "m")
		byID[jsonStr(mm, "id")] = mm
	}
	if jsonStr(byID["mylib/syntax_error"], "kind") != "error" || jsonStr(byID["mylib/syntax_error"], "parse_error") == "" {
		t.Fatalf("syntax_error model did not report kind=error + parse_error: %v", byID["mylib/syntax_error"])
	}

	_, rResp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/runtime_error", "/render"), map[string]any{
		"params": map[string]any{"divisor": 0.0},
	})
	if ok, _ := jsonBool(rResp, "ok"); ok {
		t.Fatalf("ZeroDivisionError expected ok=false, got %v", rResp)
	}
	if jsonStr(rResp, "error") == "" || jsonStr(rResp, "traceback") == "" {
		t.Fatalf("runtime exception missing error/traceback: %v", rResp)
	}

	// Worker stays healthy for valid params on same model
	_, okResp := w.postJSON(t, encodeModelRoute("", "/models/", "mylib/runtime_error", "/render"), map[string]any{
		"params": map[string]any{"divisor": 2.0},
	})
	if ok, _ := jsonBool(okResp, "ok"); !ok {
		t.Fatalf("worker failed to recover after runtime error: %v", okResp)
	}
}

// T2-09: Empty library directory & non-existent library directory.
func TestT2_09_EmptyAndMissingLibraryDirs(t *testing.T) {
	py := resolvePython(t)
	emptyDir := filepath.Join(t.TempDir(), "empty")
	_ = os.MkdirAll(emptyDir, 0o755)
	missingDir := filepath.Join(t.TempDir(), "does_not_exist")

	w := startWorker(t, py, workerOpts{
		libraries: []map[string]string{
			{"id": "empty", "name": "Empty", "path": emptyDir},
			{"id": "missing", "name": "Missing", "path": missingDir},
		},
	})
	code, resp := w.getJSON(t, "/models")
	if code != http.StatusOK || !resp["ok"].(bool) {
		t.Fatalf("GET /models failed (%d): %v", code, resp)
	}
	if models := jsonSlice(t, resp["models"], "models"); len(models) != 0 {
		t.Fatalf("expected 0 models, got %v", models)
	}
	libErrs := jsonMap(t, resp["library_errors"], "library_errors")
	if jsonStr(libErrs, "missing") == "" {
		t.Fatalf("expected missing library recorded in library_errors, got %v", libErrs)
	}
}

// T2-10: Path-traversal / illegal modelID rejected on both Go and Python routes.
func TestT2_10_IllegalModelIDRejected(t *testing.T) {
	py := resolvePython(t)
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	w := startWorker(t, py, workerOpts{
		libraries:  []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}},
		useMockCAD: true,
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, py)

	badIDs := []string{"../etc/passwd", "lib/a:b", `lib\a`, "lib//a", "lib/\x00bad"}
	for _, id := range badIDs {
		wResp, wRaw, err := w.doRaw(http.MethodGet, encodeModelRoute("", "/models/", id, "/schema"), nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if wResp.StatusCode != http.StatusBadRequest && wResp.StatusCode != http.StatusNotFound {
			t.Fatalf("worker illegal id %q status = %d, want 400/404 (body: %s)", id, wResp.StatusCode, string(wRaw))
		}

		sResp, sRaw, err := srv.doRaw(http.MethodGet, encodeModelRoute("", "/api/models/", id, ""), nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if sResp.StatusCode != http.StatusBadRequest && sResp.StatusCode != http.StatusNotFound {
			t.Fatalf("server illegal id %q status = %d, want 400/404 (body: %s)", id, sResp.StatusCode, string(sRaw))
		}
	}
}

// T2-11: Export with invalid format or invalid quality rejected with 400.
func TestT2_11_InvalidExportFormatOrQualityRejected(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	route := encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/export")
	for _, payload := range []map[string]any{
		{"params": map[string]any{}, "format": "obj"},
		{"params": map[string]any{}, "format": "stl", "quality": "ultra"},
	} {
		b, _ := json.Marshal(payload)
		resp, raw, err := srv.doRaw(http.MethodPost, route, b, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid export %v status = %d, want 400 (body: %s)", payload, resp.StatusCode, string(raw))
		}
		var errBody map[string]any
		if err := json.Unmarshal(raw, &errBody); err != nil || errBody["ok"] != false {
			t.Fatalf("expected JSON {ok:false}, got %s", string(raw))
		}
	}
}

// T2-12: Corrupt config.json is backed up to config.json.corrupt.<ts>, never silently wiped.
func TestT2_12_CorruptConfigBackedUpNotWiped(t *testing.T) {
	badBytes := []byte("{broken_json")
	srv := startServer(t, serverOpts{preConfigJSON: badBytes})

	_, _ = srv.postJSON(t, "/api/config", map[string]any{
		"libraries":  []map[string]string{},
		"export_dir": srv.exportsDir,
	})

	cfgDir := filepath.Dir(srv.configPath)
	entries, err := os.ReadDir(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	var foundBackup bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.json.corrupt.") {
			got, _ := os.ReadFile(filepath.Join(cfgDir, e.Name()))
			if bytes.Equal(got, badBytes) {
				foundBackup = true
				break
			}
		}
	}
	if !foundBackup {
		t.Fatalf("corrupt config.json was not backed up to config.json.corrupt.<ts> in %s", cfgDir)
	}
}

// T2-13: 20 concurrent goroutines GET + POST /api/config updating default_presets.
func TestT2_13_ConcurrentConfigGetAndPost(t *testing.T) {
	srv := startServer(t, serverOpts{})
	var wg sync.WaitGroup
	errs := make(chan error, 40)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]any{
				"libraries":       []map[string]string{},
				"export_dir":      srv.exportsDir,
				"default_presets": map[string]string{"lib/model": fmt.Sprintf("p%d", idx)},
			})
			resp, raw, err := srv.doRaw(http.MethodPost, "/api/config", payload, nil, "")
			if err != nil {
				errs <- err
				return
			}
			var m map[string]any
			if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &m) != nil {
				errs <- fmt.Errorf("POST %d: %s", resp.StatusCode, string(raw))
			}

			gResp, gRaw, gErr := srv.doRaw(http.MethodGet, "/api/config", nil, nil, "")
			if gErr != nil {
				errs <- gErr
				return
			}
			var gm map[string]any
			if gResp.StatusCode != http.StatusOK || json.Unmarshal(gRaw, &gm) != nil {
				errs <- fmt.Errorf("GET %d: %s", gResp.StatusCode, string(gRaw))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent config failure: %v", err)
	}

	raw, err := os.ReadFile(srv.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("final config.json invalid JSON: %v", err)
	}
	if sv, ok := onDisk["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Fatalf("final config.json schema_version = %v, want 1", onDisk["schema_version"])
	}
}

// T2-14: Malformed presets.json returns explicit error and preserves original bytes on disk.
func TestT2_14_MalformedPresetsNotWiped(t *testing.T) {
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
	presetsPath := filepath.Join(libDir, "box", "presets.json")
	badBytes := []byte("{bad")
	if err := os.WriteFile(presetsPath, badBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	route := encodeModelRoute("", "/api/models/", "mylib/box", "/presets")
	gResp, gRaw, err := srv.doRaw(http.MethodGet, route, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var gMap map[string]any
	_ = json.Unmarshal(gRaw, &gMap)
	if gResp.StatusCode < 400 && gMap["ok"] != false {
		t.Fatalf("GET presets on malformed file expected error, got %d: %s", gResp.StatusCode, string(gRaw))
	}

	pBody, _ := json.Marshal(map[string]any{"label": "Overwrite Attempt", "scope": "project", "params": map[string]any{"width": 20.0}})
	pResp, pRaw, err := srv.doRaw(http.MethodPost, route, pBody, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var pMap map[string]any
	_ = json.Unmarshal(pRaw, &pMap)
	if pResp.StatusCode < 400 && pMap["ok"] != false {
		t.Fatalf("POST presets on malformed file expected error, got %d: %s", pResp.StatusCode, string(pRaw))
	}

	after, _ := os.ReadFile(presetsPath)
	if !bytes.Equal(after, badBytes) {
		t.Fatalf("malformed presets.json was overwritten: got %q, want %q", string(after), string(badBytes))
	}
}

// T2-15: Invalid verification.status or invalid preset key rejected with 400; presets.json untouched.
func TestT2_15_InvalidPresetVerificationOrKeyRejected(t *testing.T) {
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
	presetsPath := filepath.Join(libDir, "box", "presets.json")
	initial := []byte(`{"schema_version":1,"presets":{"valid":{"label":"Valid","params":{"width":12}}}}`)
	_ = os.WriteFile(presetsPath, initial, 0o644)

	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	// Invalid verification status on POST save
	b1, _ := json.Marshal(map[string]any{
		"label":        "Bad Ver",
		"scope":        "project",
		"params":       map[string]any{"width": 15.0},
		"verification": map[string]any{"status": "bogus", "note": ""},
	})
	r1, raw1, _ := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/box", "/presets"), b1, nil, "")
	if r1.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid verification status returned %d, want 400 (%s)", r1.StatusCode, string(raw1))
	}

	// Invalid key "../../evil" on PUT verification
	b2, _ := json.Marshal(map[string]any{"scope": "project", "status": "verified", "note": ""})
	verRoute := encodeModelRoute("", "/api/models/", "mylib/box", "/presets/") + "../../evil/verification"
	r2, raw2, _ := srv.doRaw(http.MethodPut, verRoute, b2, nil, "")
	if r2.StatusCode < 400 {
		t.Fatalf("invalid preset key returned %d, want 4xx (%s)", r2.StatusCode, string(raw2))
	}

	after, _ := os.ReadFile(presetsPath)
	if !bytes.Equal(after, initial) {
		t.Fatalf("presets.json modified on invalid input:\n%s", string(after))
	}
}

// T2-16: Burst-write 30 edits across .py and *.presets.json within 200ms while SSE connected.
func TestT2_16_BurstWatcherEditsWithSSE(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	sse := srv.connectSSE(t)
	sse.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")
	origPy, _ := os.ReadFile(pyFile)
	presetFile := filepath.Join(libDir, "stdlib_dataclass.presets.json")

	for i := 0; i < 15; i++ {
		_ = os.WriteFile(pyFile, append(append([]byte{}, origPy...), []byte(fmt.Sprintf("\n# burst %d\n", i))...), 0o644)
		_ = os.WriteFile(presetFile, []byte(fmt.Sprintf(`{"schema_version":1,"presets":{"p%d":{"label":"P%d","params":{"width":%d}}}}`, i, i, 10+i)), 0o644)
		time.Sleep(10 * time.Millisecond)
	}

	gotFile := false
	gotPreset := false
	deadline := time.After(6 * time.Second)
	for !(gotFile && gotPreset) {
		select {
		case evt := <-sse.events:
			if evt.Event == "file-changed" {
				gotFile = true
			} else if evt.Event == "presets-changed" {
				gotPreset = true
			}
		case <-deadline:
			t.Fatalf("missing debounced SSE events after burst (gotFile=%v, gotPreset=%v)", gotFile, gotPreset)
		}
	}

	// Final state healthy
	code, mResp := srv.getJSON(t, "/api/models")
	if code != http.StatusOK || !mResp["ok"].(bool) {
		t.Fatalf("server unhealthy after burst watcher edits: %d %v", code, mResp)
	}
}

// T2-17: 256 KB single line to stdout AND stderr never deadlocks worker pipe.
func TestT2_17_LargeStdoutStderrLinesNoDeadlock(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"stdout_flood.py": ""})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	done := make(chan struct {
		code int
		resp map[string]any
	}, 1)
	go func() {
		c, r := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdout_flood", "/render"), map[string]any{"params": map[string]any{}})
		done <- struct {
			code int
			resp map[string]any
		}{c, r}
	}()

	select {
	case res := <-done:
		if res.code != http.StatusOK || !res.resp["ok"].(bool) {
			t.Fatalf("stdout_flood render failed (%d): %v", res.code, res.resp)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("worker pipe deadlocked on 256 KB stdout/stderr lines (>10s)")
	}
}

// T2-18: SIGKILL worker during active render -> error returned (no hang) + next render auto-respawns.
func TestT2_18_SIGKILLDuringActiveRenderRecovers(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{
		"infinite_loop.py":    "",
		"stdlib_dataclass.py": "",
	})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	pids := srv.findWorkerPIDs(t)
	if len(pids) == 0 {
		t.Fatalf("expected running worker PID")
	}

	errCh := make(chan int, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"params": map[string]any{"loop": true}})
		resp, _, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/infinite_loop", "/render"), b, nil, "")
		if err != nil {
			errCh <- -1
			return
		}
		errCh <- resp.StatusCode
	}()

	time.Sleep(300 * time.Millisecond)
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}

	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("in-flight render hung after worker SIGKILL")
	}

	code, resp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{"params": map[string]any{}})
	if code != http.StatusOK || !resp["ok"].(bool) {
		t.Fatalf("auto-respawn render after SIGKILL failed (%d): %v", code, resp)
	}
}

// T2-19: POST /api/render/cancel when idle returns 200/204 cleanly.
func TestT2_19_CancelWhenIdleSafe(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	resp, raw, err := srv.doRaw(http.MethodPost, "/api/render/cancel", []byte("{}"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("idle cancel status = %d, want 200/204 (body: %s)", resp.StatusCode, string(raw))
	}

	code, rResp := srv.postJSON(t, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render"), map[string]any{"params": map[string]any{}})
	if code != http.StatusOK || !rResp["ok"].(bool) {
		t.Fatalf("render after idle cancel failed (%d): %v", code, rResp)
	}
}

// T2-20: Runtime-generated malicious zip fixtures (symlink, ../../escape, zip-bomb) + setup security tests.
func TestT2_20_ArchiveExtractionSecurity(t *testing.T) {
	tmp := t.TempDir()

	// 1. Symlink zip
	symZip := filepath.Join(tmp, "symlink.zip")
	func() {
		f, _ := os.Create(symZip)
		defer f.Close()
		zw := zip.NewWriter(f)
		hdr := &zip.FileHeader{Name: "link.txt", Method: zip.Deflate}
		hdr.SetMode(os.ModeSymlink | 0o777)
		w, _ := zw.CreateHeader(hdr)
		_, _ = w.Write([]byte("/etc/passwd"))
		_ = zw.Close()
	}()

	// 2. Path traversal zip
	travZip := filepath.Join(tmp, "traversal.zip")
	func() {
		f, _ := os.Create(travZip)
		defer f.Close()
		zw := zip.NewWriter(f)
		w, _ := zw.Create("../../escape.txt")
		_, _ = w.Write([]byte("pwn"))
		_ = zw.Close()
	}()

	// 3. Compressible zero-byte zip bomb (> 150 MiB uncompressed)
	bombZip := filepath.Join(tmp, "bomb.zip")
	func() {
		f, _ := os.Create(bombZip)
		defer f.Close()
		zw := zip.NewWriter(f)
		w, _ := zw.Create("big_zero.bin")
		zeros := make([]byte, 1024*1024)
		for i := 0; i < 160; i++ {
			_, _ = w.Write(zeros)
		}
		_ = zw.Close()
	}()

	// Serve locally to verify HTTP download endpoint also blocks malicious zips if loopback allowed
	srv := startServer(t, serverOpts{})
	for _, zp := range []string{symZip, travZip, bombZip} {
		data, err := os.ReadFile(zp)
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(data)
		}))
		targetDir := filepath.Join(tmp, "extract_"+filepath.Base(zp))
		_ = os.MkdirAll(targetDir, 0o755)
		_, resp := srv.postJSON(t, "/api/setup/download-project", map[string]any{
			"target_dir":  targetDir,
			"archive_url": ts.URL + "/" + filepath.Base(zp),
		})
		ts.Close()
		if ok, _ := jsonBool(resp, "ok"); ok {
			t.Fatalf("malicious zip %s unexpectedly succeeded extraction: %v", filepath.Base(zp), resp)
		}
		if _, err := os.Stat(filepath.Join(tmp, "escape.txt")); err == nil {
			t.Fatalf("path traversal zip wrote outside targetDir!")
		}
	}

	// Also verify internal/setup race & security test suite
	cmd := exec.Command("go", "test", "-race", "./internal/setup/...")
	cmd.Dir = suiteRepoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("internal/setup security tests failed: %v\n%s", err, string(out))
	}
}

// T2-21: Non-HTTPS http:// non-loopback URL rejected & SHA-256 verification covered in setup tests.
func TestT2_21_NonHTTPSDownloadRejected(t *testing.T) {
	srv := startServer(t, serverOpts{})
	targetDir := filepath.Join(t.TempDir(), "dl_target")
	_ = os.MkdirAll(targetDir, 0o755)

	_, resp := srv.postJSON(t, "/api/setup/download-project", map[string]any{
		"target_dir":  targetDir,
		"archive_url": "http://example.com/archive.zip",
	})
	if ok, _ := jsonBool(resp, "ok"); ok {
		t.Fatalf("non-HTTPS archive_url should be rejected, got ok=true: %v", resp)
	}
}

// T2-22: Go API request with disallowed Host rejected 403.
func TestT2_22_DisallowedHostRejected403(t *testing.T) {
	srv := startServer(t, serverOpts{})
	resp, raw, err := srv.doRaw(http.MethodGet, "/api/config", nil, nil, "attacker.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Host attacker.com:8080 status = %d, want 403 (body: %s)", resp.StatusCode, string(raw))
	}
}

// T2-23: Cross-site Origin / Sec-Fetch-Site rejected 403.
func TestT2_23_CrossSiteOriginAndSecFetchSiteRejected403(t *testing.T) {
	srv := startServer(t, serverOpts{})
	for _, hdrs := range []map[string]string{
		{"Origin": "https://evil.example"},
		{"Sec-Fetch-Site": "cross-site"},
	} {
		resp, raw, err := srv.doRaw(http.MethodPost, "/api/config", []byte(`{"libraries":[]}`), hdrs, "")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("headers %v status = %d, want 403 (body: %s)", hdrs, resp.StatusCode, string(raw))
		}
	}
}

// T2-24: JSON POST with wrong or omitted Content-Type rejected 415.
func TestT2_24_InvalidContentTypeRejected415(t *testing.T) {
	srv := startServer(t, serverOpts{})
	for _, ct := range []string{"text/plain", ""} {
		req, _ := http.NewRequest(http.MethodPost, srv.baseURL+"/api/config", strings.NewReader(`{"libraries":[]}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, err := srv.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("Content-Type=%q status = %d, want 415 (body: %s)", ct, resp.StatusCode, string(raw))
		}
	}
}

// T2-25: > 2 MiB JSON body rejected 413.
func TestT2_25_OversizedBodyRejected413(t *testing.T) {
	srv := startServer(t, serverOpts{})
	bigStr := strings.Repeat("a", (2*1024*1024)+(512*1024))
	body := []byte(fmt.Sprintf(`{"libraries":[],"editor_command":"%s"}`, bigStr))
	resp, raw, err := srv.doRaw(http.MethodPost, "/api/config", body, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("2.5 MiB body status = %d, want 413 (body: %s)", resp.StatusCode, string(raw))
	}
}

// T2-26: Export Content-Disposition filename injection sanitized.
func TestT2_26_ExportContentDispositionSanitized(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	payload, _ := json.Marshal(map[string]any{
		"params":   map[string]any{},
		"format":   "stl",
		"filename": "../../bad\"\r\nX-Evil: 1",
	})
	resp, _, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/export"), payload, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Evil") != "" {
		t.Fatalf("CRLF header injection succeeded!")
	}
	cd := resp.Header.Get("Content-Disposition")
	if strings.Contains(cd, "..") || strings.Contains(cd, "/") || strings.ContainsAny(cd, "\r\n") {
		t.Fatalf("unsafe Content-Disposition header: %q", cd)
	}
}

// T2-27: Stalled SSE subscriber alongside rapid events still delivers file-changed control event.
func TestT2_27_StalledSSESubscriberDoesNotDropControlEvent(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	// Stalled subscriber: connect and read initial bytes only, then stop draining
	slowConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", srv.port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer slowConn.Close()
	_, _ = slowConn.Write([]byte(fmt.Sprintf("GET /events HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", srv.port)))
	buf := make([]byte, 256)
	_, _ = slowConn.Read(buf)

	fastSSE := srv.connectSSE(t)
	fastSSE.waitForEvent(t, 5*time.Second, func(e sseEvent) bool { return e.Event == "connected" })

	// Generate rapid preset/setup burst
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = srv.postJSON(t, "/api/setup/probe", map[string]any{"library_roots": []string{libDir}})
			_ = os.WriteFile(
				filepath.Join(libDir, "stdlib_dataclass.presets.json"),
				[]byte(fmt.Sprintf(`{"schema_version":1,"presets":{"k%d":{"label":"K%d","params":{"width":%d}}}}`, idx%5, idx, 10+idx)),
				0o644,
			)
		}(i)
	}
	wg.Wait()

	// Now trigger file-changed control event
	pyFile := filepath.Join(libDir, "stdlib_dataclass.py")
	orig, _ := os.ReadFile(pyFile)
	_ = os.WriteFile(pyFile, append(orig, []byte("\n# control-event\n")...), 0o644)

	evt := fastSSE.waitForEvent(t, 6*time.Second, func(e sseEvent) bool { return e.Event == "file-changed" })
	if _, err := strconv.Atoi(evt.ID); err != nil {
		t.Fatalf("expected integer id on delivered file-changed event, got %+v", evt)
	}
}

// T2-28: Prototype pollution JSON payload safely ignored by server & Vitest params.ts.
func TestT2_28_PrototypePollutionPayloadIgnored(t *testing.T) {
	libDir := setupTempModelLib(t, map[string]string{"stdlib_dataclass.py": ""})
	srv := startServer(t, serverOpts{useMockCAD: true})
	srv.configure(t, []map[string]string{{"id": "mylib", "name": "MyLib", "path": libDir}}, "")

	// Send raw JSON with __proto__ and constructor keys to render & preset endpoints
	rawRender := []byte(`{"params":{"width":15,"__proto__":{"polluted":true},"constructor":{"prototype":{"x":1}}}}`)
	resp, body, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/render"), rawRender, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= 500 {
		t.Fatalf("server 500 on __proto__ render payload: %s", string(body))
	}

	rawPreset := []byte(`{"label":"Proto Test","scope":"project","params":{"width":15,"__proto__":{"polluted":true},"constructor":{"prototype":{"x":1}}}}`)
	pResp, pBody, err := srv.doRaw(http.MethodPost, encodeModelRoute("", "/api/models/", "mylib/stdlib_dataclass", "/presets"), rawPreset, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pResp.StatusCode >= 500 {
		t.Fatalf("server 500 on __proto__ preset payload: %s", string(pBody))
	}
	presetsPath := filepath.Join(libDir, "stdlib_dataclass.presets.json")
	if diskBytes, err := os.ReadFile(presetsPath); err == nil {
		if bytes.Contains(diskBytes, []byte("polluted")) {
			t.Fatalf("__proto__ keys persisted into presets.json: %s", string(diskBytes))
		}
	}

	requireNPM(t)
	cmd := exec.Command("npm", "--prefix", repoPath("frontend"), "test", "--", "--reporter=verbose")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("frontend Vitest failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(strings.ToLower(string(out)), "proto") {
		t.Fatalf("frontend Vitest output did not exercise prototype pollution guards:\n%s", string(out))
	}
}
