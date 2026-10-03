package diff

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	ignore "github.com/sabhiram/go-gitignore"
)

type Watcher struct {
	watcher     *fsnotify.Watcher
	projectRoot string
	onChange    func()
	done        chan struct{}
	ign         *ignore.GitIgnore
	cadrIgn     *ignore.GitIgnore
	mu          sync.Mutex
}

// StartWatcher starts a recursive filesystem watcher that debounces events
// and invokes onChange when relevant files are modified.
func StartWatcher(projectRoot string, onChange func()) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	var ign *ignore.GitIgnore
	ignPath := filepath.Join(projectRoot, ".gitignore")
	if _, err := os.Stat(ignPath); err == nil {
		ign, _ = ignore.CompileIgnoreFile(ignPath)
	}

	var cadrIgn *ignore.GitIgnore
	cadrIgnPath := filepath.Join(projectRoot, ".cadr", "ignore")
	if _, err := os.Stat(cadrIgnPath); err == nil {
		cadrIgn, _ = ignore.CompileIgnoreFile(cadrIgnPath)
	}

	sw := &Watcher{
		watcher:     w,
		projectRoot: projectRoot,
		onChange:    onChange,
		done:        make(chan struct{}),
		ign:         ign,
		cadrIgn:     cadrIgn,
	}

	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		rel, err := filepath.Rel(projectRoot, path)
		if err == nil && rel != "." {
			if (ign != nil && ign.MatchesPath(rel)) || (cadrIgn != nil && cadrIgn.MatchesPath(rel)) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
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

func isEphemeralFile(name string) bool {
	base := filepath.Base(name)
	if strings.HasPrefix(base, ".") && (strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".lock")) {
		return true
	}
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".bak") || strings.HasSuffix(base, ".crswap") {
		return true
	}
	if strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#") {
		return true
	}
	if base == ".git" || strings.Contains(name, ".git/") || strings.Contains(name, "index.lock") {
		return true
	}
	return false
}

func (w *Watcher) isIgnored(path string) bool {
	rel, err := filepath.Rel(w.projectRoot, path)
	if err != nil || rel == "." {
		return false
	}
	if w.ign != nil && w.ign.MatchesPath(rel) {
		return true
	}
	if w.cadrIgn != nil && w.cadrIgn.MatchesPath(rel) {
		return true
	}
	return false
}

func (w *Watcher) loop() {
	defer func() {
		if r := recover(); r != nil {
			// Recover from any unexpected panic in watcher thread
		}
	}()

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

			if isEphemeralFile(event.Name) || w.isIgnored(event.Name) {
				continue
			}

			// Watch newly created directories
			if event.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					if !shouldIgnoreDir(filepath.Base(event.Name)) && !w.isIgnored(event.Name) {
						_ = w.watcher.Add(event.Name)
					}
				}
			}

			// Debounce 200ms to allow file writes and atomic renames to settle
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(200 * time.Millisecond)
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
