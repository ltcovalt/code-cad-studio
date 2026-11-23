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

// Watcher monitors one or more library roots recursively for .py and presets.json changes.
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

func isSkippedDir(name string) bool {
	return skipDirs[name] || strings.HasPrefix(name, ".")
}

func addDirRecursive(fsw *fsnotify.Watcher, rootDir, targetDir string, watchedDirs map[string]bool) (hasPy bool, hasPreset bool) {
	_ = filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != rootDir && isSkippedDir(name) {
				return filepath.SkipDir
			}
			clean := filepath.Clean(path)
			if !watchedDirs[clean] {
				if err := fsw.Add(clean); err == nil {
					watchedDirs[clean] = true
				}
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".py") {
			hasPy = true
		} else if name == "presets.json" {
			hasPreset = true
		}
		return nil
	})
	return hasPy, hasPreset
}

// Watch starts watching all valid directories in roots (stopping any previous watch first).
func (w *Watcher) Watch(roots []string) error {
	w.Stop()

	cleanRoots := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		clean := filepath.Clean(r)
		if _, dup := seen[clean]; dup {
			continue
		}
		st, err := os.Stat(clean)
		if err != nil || !st.IsDir() {
			log.Printf("Skipping watch on unavailable library root: %s", clean)
			continue
		}
		seen[clean] = struct{}{}
		cleanRoots = append(cleanRoots, clean)
	}

	if len(cleanRoots) == 0 {
		return nil
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	watchedDirs := make(map[string]bool)
	for _, root := range cleanRoots {
		addDirRecursive(fsw, root, root, watchedDirs)
	}

	stopCh := make(chan struct{})
	w.mu.Lock()
	w.fsw = fsw
	w.stopCh = stopCh
	w.mu.Unlock()

	go w.loop(fsw, stopCh, watchedDirs)
	log.Printf("Watching %d library root(s) for changes: %s", len(cleanRoots), strings.Join(cleanRoots, ", "))
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

func (w *Watcher) loop(fsw *fsnotify.Watcher, stopCh chan struct{}, watchedDirs map[string]bool) {
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

	schedulePy := func(p string) {
		lastPyPath = p
		if pyTimer != nil {
			pyTimer.Stop()
		}
		pyTimer = time.AfterFunc(250*time.Millisecond, triggerPy)
	}
	schedulePreset := func(p string) {
		lastPresetPath = p
		if presetTimer != nil {
			presetTimer.Stop()
		}
		presetTimer = time.AfterFunc(250*time.Millisecond, triggerPreset)
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
			cleanName := filepath.Clean(event.Name)

			// Recursively watch newly created or moved-in directories
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(cleanName); err == nil && info.IsDir() {
					base := filepath.Base(cleanName)
					if !isSkippedDir(base) {
						hasPy, hasPreset := addDirRecursive(fsw, "", cleanName, watchedDirs)
						if hasPy {
							schedulePy(cleanName)
						}
						if hasPreset {
							schedulePreset(cleanName)
						}
					}
					continue
				}
			}

			// Detect removal/rename of a watched directory (e.g. moving/deleting a category folder)
			if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				if watchedDirs[cleanName] {
					prefix := cleanName + string(os.PathSeparator)
					for dir := range watchedDirs {
						if dir == cleanName || strings.HasPrefix(dir, prefix) {
							delete(watchedDirs, dir)
						}
					}
					schedulePy(cleanName)
					continue
				}
			}

			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
				continue
			}

			name := filepath.Base(cleanName)
			if strings.HasSuffix(name, ".py") {
				schedulePy(cleanName)
			} else if name == "presets.json" {
				schedulePreset(cleanName)
			}

		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}
