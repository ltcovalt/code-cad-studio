package watcher

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var skipDirs = map[string]bool{
	".git":          true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	"node_modules":  true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	"dist":          true,
	"build":         true,
	"exports":       true,
}

// Watcher monitors a project root recursively for .py and presets.json changes.
type Watcher struct {
	mu             sync.Mutex
	fsw            *fsnotify.Watcher
	stopCh         chan struct{}
	onPyChange     func(path string)
	onPresetChange func(path string)
}

func New(onPyChange, onPresetChange func(path string)) *Watcher {
	return &Watcher{
		onPyChange:     onPyChange,
		onPresetChange: onPresetChange,
	}
}

// Watch starts watching root (stopping any previous watch first).
func (w *Watcher) Watch(root string) error {
	w.Stop()
	if root == "" {
		return nil
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			_ = fsw.Add(path)
		}
		return nil
	})
	if err != nil {
		_ = fsw.Close()
		return err
	}

	stopCh := make(chan struct{})
	w.mu.Lock()
	w.fsw = fsw
	w.stopCh = stopCh
	w.mu.Unlock()

	go w.loop(fsw, stopCh)
	log.Printf("Watching project root for changes: %s", root)
	return nil
}

// Stop tears down the active watcher.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopCh != nil {
		close(w.stopCh)
		w.stopCh = nil
	}
	if w.fsw != nil {
		_ = w.fsw.Close()
		w.fsw = nil
	}
}

func (w *Watcher) loop(fsw *fsnotify.Watcher, stopCh chan struct{}) {
	var pyTimer *time.Timer
	var presetTimer *time.Timer
	var lastPyPath, lastPresetPath string

	triggerPy := func() {
		if w.onPyChange != nil {
			w.onPyChange(lastPyPath)
		}
	}
	triggerPreset := func() {
		if w.onPresetChange != nil {
			w.onPresetChange(lastPresetPath)
		}
	}

	for {
		select {
		case <-stopCh:
			if pyTimer != nil {
				pyTimer.Stop()
			}
			if presetTimer != nil {
				presetTimer.Stop()
			}
			return

		case event, ok := <-fsw.Events:
			if !ok {
				return
			}
			// Watch newly created directories
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					base := filepath.Base(event.Name)
					if !skipDirs[base] && !strings.HasPrefix(base, ".") {
						_ = fsw.Add(event.Name)
					}
				}
			}

			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
				continue
			}

			name := filepath.Base(event.Name)
			if strings.HasSuffix(name, ".py") {
				lastPyPath = event.Name
				if pyTimer != nil {
					pyTimer.Stop()
				}
				pyTimer = time.AfterFunc(250*time.Millisecond, triggerPy)
			} else if name == "presets.json" {
				lastPresetPath = event.Name
				if presetTimer != nil {
					presetTimer.Stop()
				}
				presetTimer = time.AfterFunc(250*time.Millisecond, triggerPreset)
			}

		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}
