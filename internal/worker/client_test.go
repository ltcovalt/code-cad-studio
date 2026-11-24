package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func findTestPython(t *testing.T) string {
	t.Helper()
	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "python3"}
	}
	for _, name := range candidates {
		p, err := exec.LookPath(name)
		if err != nil || p == "" {
			continue
		}
		norm := strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
		if strings.Contains(norm, "/microsoft/windowsapps/") {
			continue
		}
		cmd := exec.Command(p, "--version")
		configureCmd(cmd)
		if err := cmd.Run(); err == nil {
			return p
		}
	}
	t.Skip("python interpreter not available for worker tests")
	return ""
}

// testStubWorkerScript is a pure-stdlib Python worker implementing READY, bearer auth,
// long pipe output, an infinite-loop render model, and echo export routes for Go supervision tests.
const testStubWorkerScript = `
import json, os, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import unquote, urlparse

TOKEN = os.environ.get("CODE_CAD_WORKER_TOKEN", "")

# Emit >128 KiB single lines before READY to prove unbounded pipe readers never deadlock.
sys.stderr.write("E" * (256 * 1024) + "\n")
sys.stderr.flush()
sys.stdout.write("O" * (256 * 1024) + "\n")
sys.stdout.flush()

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def _check_auth(self):
        auth = self.headers.get("Authorization", "")
        if auth != f"Bearer {TOKEN}":
            body = json.dumps({"ok": False, "error": "unauthorized"}).encode("utf-8")
            self.send_response(401)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return False
        return True

    def _send_json(self, obj, status=200):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if not self._check_auth():
            return
        path = urlparse(self.path).path
        if path == "/health":
            self._send_json({"ok": True, "python": sys.executable, "pid": os.getpid(), "libraries": []})
            return
        self._send_json({"ok": False, "error": "not found"}, 404)

    def do_POST(self):
        if not self._check_auth():
            return
        length = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(length) if length > 0 else b"{}"
        body = json.loads(raw.decode("utf-8"))
        path = urlparse(self.path).path

        if path.startswith("/models/") and path.endswith("/render"):
            model_id = unquote(path[len("/models/"):-len("/render")])
            params = body.get("params") or {}
            if model_id == "lib/loop":
                marker = params.get("marker_file")
                if marker:
                    with open(marker, "w", encoding="utf-8") as fh:
                        fh.write(str(os.getpid()))
                while True:
                    time.sleep(0.1)
            if model_id == "lib/big_logs":
                sys.stderr.write("S" * (256 * 1024) + "\n")
                sys.stderr.flush()
                sys.stdout.write("P" * (256 * 1024) + "\n")
                sys.stdout.flush()
            self._send_json({"ok": True, "model_id": model_id, "shapes": []})
            return

        if path.startswith("/models/") and path.endswith("/export"):
            model_id = unquote(path[len("/models/"):-len("/export")])
            if body.get("output_path"):
                self._send_json({
                    "ok": True,
                    "path": body["output_path"],
                    "size": 42,
                    "quality": body.get("quality", ""),
                    "linear_deflection": body.get("linear_deflection"),
                    "angular_deflection": body.get("angular_deflection"),
                })
                return
            payload = json.dumps({
                "model_id": model_id,
                "format": body.get("format"),
                "quality": body.get("quality"),
                "linear_deflection": body.get("linear_deflection"),
                "angular_deflection": body.get("angular_deflection"),
            }).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        self._send_json({"ok": False, "error": "not found"}, 404)

def main():
    srv = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = srv.server_address[1]
    print(f"READY {port}", flush=True)
    srv.serve_forever()

if __name__ == "__main__":
    main()
`

func TestWorkerTokenAuthAgainstRealWorker(t *testing.T) {
	py := findTestPython(t)
	scriptBytes, err := os.ReadFile(filepath.Join("..", "..", "worker", "worker.py"))
	if err != nil {
		t.Fatalf("read worker.py: %v", err)
	}

	mgr := NewManager(scriptBytes)
	defer mgr.Stop()

	if err := mgr.Start(py, nil); err != nil {
		t.Fatalf("Start real worker.py: %v", err)
	}

	// Authenticated Go client request succeeds
	health, err := mgr.Health(context.Background())
	if err != nil {
		t.Fatalf("Health with valid token failed: %v", err)
	}
	if !health.OK {
		t.Fatalf("expected Health OK=true, got %+v", health)
	}

	baseURL, token, err := mgr.getConnection()
	if err != nil {
		t.Fatalf("getConnection: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("expected 64-char hex token, got len=%d", len(token))
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}

	// Missing Authorization header must be rejected with 401
	respNoAuth, err := httpClient.Get(baseURL + "/models")
	if err != nil {
		t.Fatalf("GET /models without token: %v", err)
	}
	_ = respNoAuth.Body.Close()
	if respNoAuth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without Authorization header, got %d", respNoAuth.StatusCode)
	}

	// Wrong Bearer token must be rejected with 401
	reqWrong, _ := http.NewRequest(http.MethodGet, baseURL+"/models", nil)
	reqWrong.Header.Set("Authorization", "Bearer deadbeef")
	respWrong, err := httpClient.Do(reqWrong)
	if err != nil {
		t.Fatalf("GET /models wrong token: %v", err)
	}
	_ = respWrong.Body.Close()
	if respWrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", respWrong.StatusCode)
	}

	// Valid Bearer token succeeds with 200
	reqValid, _ := http.NewRequest(http.MethodGet, baseURL+"/models", nil)
	reqValid.Header.Set("Authorization", "Bearer "+token)
	respValid, err := httpClient.Do(reqValid)
	if err != nil {
		t.Fatalf("GET /models valid token: %v", err)
	}
	_ = respValid.Body.Close()
	if respValid.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", respValid.StatusCode)
	}
}

func TestWorkerCrashDetectionAndAutoRecovery(t *testing.T) {
	py := findTestPython(t)
	mgr := NewManager([]byte(testStubWorkerScript))
	defer mgr.Stop()

	unexpectedCh := make(chan string, 1)
	mgr.SetOnUnexpectedExit(func(errMsg string) {
		select {
		case unexpectedCh <- errMsg:
		default:
		}
	})

	if err := mgr.Start(py, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	running, lastErr := mgr.Status()
	if !running || lastErr != "" {
		t.Fatalf("expected running=(true, ''), got (%v, %q)", running, lastErr)
	}

	// Grab underlying process and kill it mid-session
	mgr.stateMu.RLock()
	proc := mgr.cmd.Process
	mgr.stateMu.RUnlock()
	if proc == nil {
		t.Fatalf("expected non-nil worker process")
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill worker: %v", err)
	}

	select {
	case msg := <-unexpectedCh:
		if msg == "" {
			t.Fatalf("expected non-empty crash message")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for crash monitor to detect worker exit")
	}

	running, lastErr = mgr.Status()
	if running || lastErr == "" {
		t.Fatalf("expected running=(false, non-empty) after crash, got (%v, %q)", running, lastErr)
	}

	// Subsequent EnsureStarted auto-recovers cleanly
	if err := mgr.EnsureStarted(py, nil); err != nil {
		t.Fatalf("EnsureStarted recovery failed: %v", err)
	}
	running, lastErr = mgr.Status()
	if !running || lastErr != "" {
		t.Fatalf("expected running=(true, '') after recovery, got (%v, %q)", running, lastErr)
	}
	if _, err := mgr.Health(context.Background()); err != nil {
		t.Fatalf("Health after recovery failed: %v", err)
	}
}

func TestWorkerLargeOutputLinesDoNotDeadlock(t *testing.T) {
	py := findTestPython(t)
	mgr := NewManager([]byte(testStubWorkerScript))
	defer mgr.Stop()

	// Startup itself emits 256 KiB single lines on both stdout and stderr before READY
	if err := mgr.Start(py, nil); err != nil {
		t.Fatalf("Start with 256 KiB startup lines failed: %v", err)
	}

	// Trigger another 256 KiB single line on both stdout and stderr during a render
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, raw, err := mgr.RenderRaw(ctx, "lib/big_logs", nil)
	if err != nil {
		t.Fatalf("RenderRaw failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", status, string(raw))
	}
	var parsed RenderResponse
	if err := json.Unmarshal(raw, &parsed); err != nil || !parsed.OK || parsed.ModelID != "lib/big_logs" {
		t.Fatalf("unexpected RenderRaw response: err=%v body=%s", err, string(raw))
	}
}

func TestWorkerCancelAndRestartKillsInfiniteLoop(t *testing.T) {
	py := findTestPython(t)
	mgr := NewManager([]byte(testStubWorkerScript))
	defer mgr.Stop()

	if err := mgr.Start(py, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	markerFile := filepath.Join(t.TempDir(), "loop_started.txt")
	renderErrCh := make(chan error, 1)
	go func() {
		_, _, err := mgr.RenderRaw(context.Background(), "lib/loop", map[string]any{
			"marker_file": markerFile,
		})
		renderErrCh <- err
	}()

	// Wait deterministically until Python handler enters the infinite loop
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(markerFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for infinite-loop render to start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := mgr.CancelAndRestart(ctx); err != nil {
		t.Fatalf("CancelAndRestart failed: %v", err)
	}

	select {
	case err := <-renderErrCh:
		if err == nil {
			t.Fatalf("expected aborted render to return an error after worker kill")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for cancelled RenderRaw goroutine to unblock")
	}

	// Subsequent render succeeds cleanly on the respawned worker
	status, raw, err := mgr.RenderRaw(ctx, "lib/fast", nil)
	if err != nil {
		t.Fatalf("subsequent RenderRaw failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected HTTP 200 after restart, got %d (%s)", status, string(raw))
	}
	var resp RenderResponse
	if err := json.Unmarshal(raw, &resp); err != nil || !resp.OK || resp.ModelID != "lib/fast" {
		t.Fatalf("unexpected subsequent RenderRaw payload: err=%v body=%s", err, string(raw))
	}
}

func TestWorkerStopRemovesTempScriptDir(t *testing.T) {
	py := findTestPython(t)
	mgr := NewManager([]byte(testStubWorkerScript))

	if err := mgr.Start(py, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	mgr.lifecycleMu.Lock()
	scriptDir := mgr.scriptDir
	mgr.lifecycleMu.Unlock()

	if scriptDir == "" {
		t.Fatalf("expected non-empty scriptDir while running")
	}
	if _, err := os.Stat(scriptDir); err != nil {
		t.Fatalf("expected scriptDir %q to exist: %v", scriptDir, err)
	}

	mgr.Stop()

	if _, err := os.Stat(scriptDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected scriptDir %q to be deleted on Stop(), got err=%v", scriptDir, err)
	}
}

func TestWorkerExportWithOptionsAndFallbackStem(t *testing.T) {
	py := findTestPython(t)
	mgr := NewManager([]byte(testStubWorkerScript))
	defer mgr.Stop()

	if err := mgr.Start(py, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	lin := 0.0025
	ang := 0.15
	raw, err := mgr.ExportBytesWithOptions(context.Background(), "code-cad/sub/part", map[string]any{"x": 1}, ExportOptions{
		Format:            "stl",
		Quality:           "custom",
		LinearDeflection:  &lin,
		AngularDeflection: &ang,
	})
	if err != nil {
		t.Fatalf("ExportBytesWithOptions: %v", err)
	}
	var echoed struct {
		ModelID           string   `json:"model_id"`
		Format            string   `json:"format"`
		Quality           string   `json:"quality"`
		LinearDeflection  *float64 `json:"linear_deflection"`
		AngularDeflection *float64 `json:"angular_deflection"`
	}
	if err := json.Unmarshal(raw, &echoed); err != nil {
		t.Fatalf("unmarshal echoed export: %v", err)
	}
	if echoed.ModelID != "code-cad/sub/part" || echoed.Quality != "custom" ||
		echoed.LinearDeflection == nil || *echoed.LinearDeflection != lin ||
		echoed.AngularDeflection == nil || *echoed.AngularDeflection != ang {
		t.Fatalf("unexpected export options forwarded: %+v", echoed)
	}

	cases := map[string]string{
		"code-cad/container_lid": "container_lid",
		"lib/sub/part":           "part",
		"code-cad::sub__part":    "part",
		"demo/my part (v2)?":     "my_part__v2",
		"":                       "model",
		"///":                    "model",
	}
	for in, want := range cases {
		if got := fallbackStemFromID(in); got != want {
			t.Errorf("fallbackStemFromID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkerConcurrentConfigureOrRestartAndCancelAndRestart(t *testing.T) {
	py := findTestPython(t)
	stateDir := t.TempDir()
	hangFlag := filepath.Join(stateDir, "hang_once")
	enteredFlag := filepath.Join(stateDir, "entered_configure")
	if err := os.WriteFile(hangFlag, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	stub := strings.ReplaceAll(strings.ReplaceAll(`
import json, os, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOKEN = os.environ.get("CODE_CAD_WORKER_TOKEN", "")
HANG_FLAG = __HANG_FLAG__
ENTERED_FLAG = __ENTERED_FLAG__

def parse_libs():
    if "--libraries-json" in sys.argv:
        idx = sys.argv.index("--libraries-json")
        if idx + 1 < len(sys.argv):
            return json.loads(sys.argv[idx + 1])
    return []

LIBS = parse_libs()

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def _auth(self):
        return self.headers.get("Authorization", "") == f"Bearer {TOKEN}"
    def _send(self, obj, status=200):
        b = json.dumps(obj).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    def do_GET(self):
        if not self._auth():
            return self._send({"ok": False}, 401)
        if self.path == "/health":
            return self._send({"ok": True, "python": sys.executable, "libraries": LIBS})
        self._send({"ok": False}, 404)
    def do_POST(self):
        global LIBS
        if not self._auth():
            return self._send({"ok": False}, 401)
        n = int(self.headers.get("Content-Length", "0") or "0")
        body = json.loads(self.rfile.read(n).decode("utf-8")) if n > 0 else {}
        if self.path == "/configure":
            if os.path.exists(HANG_FLAG):
                try:
                    os.remove(HANG_FLAG)
                except OSError:
                    pass
                with open(ENTERED_FLAG, "w", encoding="utf-8") as fh:
                    fh.write("1")
                time.sleep(10.0)
            LIBS = body.get("libraries") or []
            return self._send({"ok": True, "models": [], "library_errors": {}})
        self._send({"ok": False}, 404)

srv = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
print(f"READY {srv.server_address[1]}", flush=True)
srv.serve_forever()
`, "__HANG_FLAG__", `"`+strings.ReplaceAll(hangFlag, `\`, `\\`)+`"`), "__ENTERED_FLAG__", `"`+strings.ReplaceAll(enteredFlag, `\`, `\\`)+`"`)

	mgr := NewManager([]byte(stub))
	defer mgr.Stop()

	initialLibs := []LibrarySpec{{ID: "init", Name: "Init", Path: filepath.Join(stateDir, "init")}}
	targetLibs := []LibrarySpec{{ID: "updated", Name: "Updated", Path: filepath.Join(stateDir, "updated")}}

	if err := mgr.Start(py, initialLibs); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cfgErrCh := make(chan error, 1)
	go func() {
		cfgErrCh <- mgr.ConfigureOrRestart(context.Background(), py, targetLibs)
	}()

	// Wait until ConfigureOrRestart is actively blocked inside /configure
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(enteredFlag); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for /configure to start")
		}
		time.Sleep(15 * time.Millisecond)
	}

	// CancelAndRestart must NOT deadlock on the wedged /configure, and must not leave stale libraries
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mgr.CancelAndRestart(cancelCtx); err != nil {
		t.Fatalf("CancelAndRestart failed: %v", err)
	}

	select {
	case err := <-cfgErrCh:
		if err != nil {
			t.Fatalf("ConfigureOrRestart failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for ConfigureOrRestart to complete")
	}

	health, err := mgr.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if len(health.Libraries) != 1 || health.Libraries[0].ID != "updated" {
		t.Fatalf("expected worker libraries to be updated=%v, got %+v", targetLibs, health.Libraries)
	}

	mgr.stateMu.RLock()
	mgrLibs := cloneLibraries(mgr.libraries)
	mgr.stateMu.RUnlock()
	if len(mgrLibs) != 1 || mgrLibs[0].ID != "updated" {
		t.Fatalf("expected manager libraries to be updated=%v, got %+v", targetLibs, mgrLibs)
	}
}
