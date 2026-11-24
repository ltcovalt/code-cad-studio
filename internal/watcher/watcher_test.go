package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsPresetFilename(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"presets.json", true},
		{"widget.presets.json", true},
		{"my_part.presets.json", true},
		{".presets.json", false},
		{"presets.json.tmp", false},
		{"widget.presets.json.tmp", false},
		{"presets.json.corrupt.20261123T000000Z", false},
		{".presets.json.123.tmp", false},
		{"other.json", false},
	}
	for _, tc := range cases {
		if got := isPresetFilename(tc.name); got != tc.want {
			t.Errorf("isPresetFilename(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWatcherMultiRootAndNestedDirCreation(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	missingRoot := filepath.Join(t.TempDir(), "does-not-exist")

	pyEvents := make(chan string, 16)
	presetEvents := make(chan string, 16)

	w := New(
		func(p string) { pyEvents <- p },
		func(p string) { presetEvents <- p },
	)
	defer w.Stop()

	if err := w.Watch([]string{rootA, rootB, missingRoot}); err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// 1. Nested directory creation in rootA followed by writing a .py file inside
	nestedDir := filepath.Join(rootA, "models", "gridfinity", "bin")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)

	pyPath := filepath.Join(nestedDir, "bin.py")
	if err := os.WriteFile(pyPath, []byte("def create_bin(): return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-pyEvents:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for py change event in nested newly created dir")
	}

	// 2. Writing presets.json in rootB triggers onPresetChange
	presetPath := filepath.Join(rootB, "presets.json")
	if err := os.WriteFile(presetPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-presetEvents:
		if got != presetPath {
			t.Fatalf("expected preset event %q, got %q", presetPath, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for preset change event in second root")
	}

	// 3. Writing <stem>.presets.json in rootB also triggers onPresetChange
	stemPresetPath := filepath.Join(rootB, "standalone.presets.json")
	if err := os.WriteFile(stemPresetPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-presetEvents:
		if got != stemPresetPath {
			t.Fatalf("expected stem preset event %q, got %q", stemPresetPath, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for <stem>.presets.json change event")
	}

	// 4. Temp/corrupt preset files must NOT trigger onPresetChange
	if err := os.WriteFile(filepath.Join(rootB, "presets.json.tmp"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "presets.json.corrupt.20261123T000000Z"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case unexpected := <-presetEvents:
		t.Fatalf("unexpected preset event for ignored temp/corrupt file: %q", unexpected)
	case <-time.After(400 * time.Millisecond):
	}
}

func TestWatcherRapidConcurrentWritesNoDataRace(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var pySeen []string
	var presetSeen []string

	w := New(
		func(p string) {
			mu.Lock()
			pySeen = append(pySeen, p)
			mu.Unlock()
		},
		func(p string) {
			mu.Lock()
			presetSeen = append(presetSeen, p)
			mu.Unlock()
		},
	)
	defer w.Stop()

	if err := w.Watch([]string{root}); err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Rapidly overwrite .py, presets.json, and <stem>.presets.json across debounce boundaries
	for i := 0; i < 15; i++ {
		if err := os.WriteFile(filepath.Join(root, "part.py"), []byte(fmt.Sprintf("x = %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "presets.json"), []byte(fmt.Sprintf(`{"schema_version":1,"i":%d}`, i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "part.presets.json"), []byte(fmt.Sprintf(`{"schema_version":1,"i":%d}`, i)), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(45 * time.Millisecond)
	}

	// Wait for final debounce timers to fire cleanly
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		gotPy := len(pySeen)
		gotPreset := len(presetSeen)
		mu.Unlock()
		if gotPy > 0 && gotPreset > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(pySeen) == 0 {
		t.Fatalf("expected at least one debounced py callback")
	}
	if len(presetSeen) == 0 {
		t.Fatalf("expected at least one debounced preset callback")
	}
}

func TestWatcherStopWaitsForInFlightCallback(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var callbacks atomic.Int32

	w := New(
		func(p string) {
			callbacks.Add(1)
			once.Do(func() {
				close(entered)
				<-release
			})
		},
		nil,
	)

	if err := w.Watch([]string{root}); err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	pyFile := filepath.Join(root, "part.py")
	if err := os.WriteFile(pyFile, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for in-flight watcher callback")
	}

	// Trigger another pending debounce timer while callback is blocked, then call Stop()
	if err := os.WriteFile(pyFile, []byte("x = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stopDone := make(chan struct{})
	go func() {
		w.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		t.Fatal("Stop() returned before in-flight callback finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Stop() to return after callback released")
	}

	countAtStop := callbacks.Load()
	// Wait past the 250ms debounce window to ensure no queued timer fires after Stop()
	time.Sleep(350 * time.Millisecond)
	if got := callbacks.Load(); got != countAtStop {
		t.Fatalf("expected 0 callbacks after Stop(), got %d -> %d", countAtStop, got)
	}
}
