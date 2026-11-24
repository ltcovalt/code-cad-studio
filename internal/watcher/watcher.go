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

type watchSession struct {
	fsw      *fsnotify.Watcher
	stopCh   chan struct{}
	stopped  bool
	loopWG   sync.WaitGroup
	inFlight sync.WaitGroup
}

// Watcher monitors one or more library roots recursively for .py, presets.json, and *.presets.json changes.
type Watcher struct {
	mu             sync.Mutex
	session        *watchSession
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

func isPresetFilename(name string) bool {
	if name == "presets.json" {
		return true
	}
	return strings.HasSuffix(name, ".presets.json") && len(name) > len(".presets.json") && !strings.HasPrefix(name, ".")
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
		} else if isPresetFilename(name) {
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
	sess := &watchSession{
		fsw:    fsw,
		stopCh: stopCh,
	}

	w.mu.Lock()
	prev := w.session
	if prev != nil {
		w.stopSessionLocked(prev)
	}
	w.session = sess
	sess.loopWG.Add(1)
	w.mu.Unlock()

	if prev != nil {
		prev.loopWG.Wait()
		prev.inFlight.Wait()
	}

	go w.loop(sess, fsw, stopCh, watchedDirs)
	log.Printf("Watching %d library root(s) for changes: %s", len(cleanRoots), strings.Join(cleanRoots, ", "))
	return nil
}

func (w *Watcher) stopSessionLocked(sess *watchSession) {
	if sess == nil || sess.stopped {
		return
	}
	sess.stopped = true
	if sess.stopCh != nil {
		close(sess.stopCh)
	}
	if sess.fsw != nil {
		_ = sess.fsw.Close()
	}
}

// Stop tears down the active watcher and waits until its event loop and any in-flight callbacks finish.
func (w *Watcher) Stop() {
	w.mu.Lock()
	sess := w.session
	w.session = nil
	w.stopSessionLocked(sess)
	w.mu.Unlock()

	if sess != nil {
		sess.loopWG.Wait()
		sess.inFlight.Wait()
	}
}

func (w *Watcher) loop(sess *watchSession, fsw *fsnotify.Watcher, stopCh chan struct{}, watchedDirs map[string]bool) {
	var pyTimer *time.Timer
	var presetTimer *time.Timer
	defer func() {
		if pyTimer != nil {
			pyTimer.Stop()
		}
		if presetTimer != nil {
			presetTimer.Stop()
		}
		sess.loopWG.Done()
	}()

	runCallback := func(fn func(string), p string) {
		if fn == nil {
			return
		}
		w.mu.Lock()
		if w.session != sess || sess.stopped {
			w.mu.Unlock()
			return
		}
		sess.inFlight.Add(1)
		w.mu.Unlock()
		defer sess.inFlight.Done()
		fn(p)
	}

	schedulePy := func(p string) {
		pathCopy := p
		if pyTimer != nil {
			pyTimer.Stop()
		}
		pyTimer = time.AfterFunc(250*time.Millisecond, func() {
			runCallback(w.onPyChange, pathCopy)
		})
	}
	schedulePreset := func(p string) {
		pathCopy := p
		if presetTimer != nil {
			presetTimer.Stop()
		}
		presetTimer = time.AfterFunc(250*time.Millisecond, func() {
			runCallback(w.onPresetChange, pathCopy)
		})
	}

	for {
		select {
		case <-stopCh:
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
			} else if isPresetFilename(name) {
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
