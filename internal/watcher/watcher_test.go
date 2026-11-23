package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	// Give fsnotify a moment to register the recursive watches
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
	case <-presetEvents:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for preset change event in second root")
	}
}
