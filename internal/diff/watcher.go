package diff

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	watcher     *fsnotify.Watcher
	projectRoot string
	onChange    func()
	done        chan struct{}
	mu          sync.Mutex
}

// StartWatcher starts a recursive filesystem watcher that debounces events
// and invokes onChange when relevant files are modified.
func StartWatcher(projectRoot string, onChange func()) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	sw := &Watcher{
		watcher:     w,
		projectRoot: projectRoot,
		onChange:    onChange,
		done:        make(chan struct{}),
	}

	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if shouldIgnoreDir(base) {
				return filepath.SkipDir
			}
			_ = w.Add(path)
		}
		return nil
	})

	go sw.loop()
	return sw, nil
}

func shouldIgnoreDir(name string) bool {
	switch name {
	case ".git", ".cadr", "node_modules", ".venv", "venv", "dist", "build", "__pycache__", ".idea", ".vscode", "target":
		return true
	}
	return false
}

func (w *Watcher) loop() {
	var timer *time.Timer
	var timerChan <-chan time.Time

	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			// Watch newly created directories
			if event.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					if !shouldIgnoreDir(filepath.Base(event.Name)) {
						_ = w.watcher.Add(event.Name)
					}
				}
			}

			// Debounce 150ms
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(150 * time.Millisecond)
			timerChan = timer.C

		case <-timerChan:
			if w.onChange != nil {
				w.onChange()
			}

		case <-w.watcher.Errors:
			// ignore watcher errors
		}
	}
}

// Close terminates the filesystem watcher.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	default:
		close(w.done)
		return w.watcher.Close()
	}
}
