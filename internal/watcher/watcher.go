package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Event struct {
	Path      string
	Op        string // "create", "write", "remove", "rename"
	IsDir     bool
}

type Watcher struct {
	fw       *fsnotify.Watcher
	Events   chan Event
	Errors   chan error
	workDirs map[string]string // local path -> remote prefix
	mu       sync.Mutex
	done     chan struct{}
}

func New() (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		fw:       fw,
		Events:   make(chan Event, 1000),
		Errors:   make(chan error, 10),
		workDirs: make(map[string]string),
		done:     make(chan struct{}),
	}, nil
}

func (w *Watcher) SetWorkDirs(dirs map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Remove old watches
	for path := range w.workDirs {
		w.fw.Remove(path)
	}
	w.workDirs = make(map[string]string)

	for localPath, remotePrefix := range dirs {
		if err := w.addRecursive(localPath); err != nil {
			return err
		}
		w.workDirs[localPath] = remotePrefix
	}

	return nil
}

func (w *Watcher) addRecursive(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() {
			if shouldIgnoreDir(info.Name()) {
				return filepath.SkipDir
			}
			if watchErr := w.fw.Add(path); watchErr != nil {
				return watchErr
			}
		}
		return nil
	})
}

func (w *Watcher) Start() {
	debounceTimer := time.NewTimer(0)
	if !debounceTimer.Stop() {
		<-debounceTimer.C
	}
	var pending []Event

	for {
		select {
		case <-w.done:
			return

		case fsEvent, ok := <-w.fw.Events:
			if !ok {
				return
			}

			ev := convertEvent(fsEvent)
			if ev == nil {
				continue
			}

			pending = append(pending, *ev)

			// Debounce: reset timer, flush when it fires
			debounceTimer.Reset(500 * time.Millisecond)

		case <-debounceTimer.C:
			deduped := dedupe(pending)
			for _, ev := range deduped {
				select {
				case w.Events <- ev:
				default:
				}
			}
			pending = nil

			// Handle new subdirectory creation
			for _, ev := range deduped {
				if ev.IsDir && ev.Op == "create" {
					w.mu.Lock()
					w.fw.Add(ev.Path)
					w.mu.Unlock()
				}
			}

		case fsErr, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			select {
			case w.Errors <- fsErr:
			default:
			}
		}
	}
}

func (w *Watcher) Stop() {
	close(w.done)
	w.fw.Close()
}

func convertEvent(e fsnotify.Event) *Event {
	path := filepath.Clean(e.Name)
	base := filepath.Base(path)

	if shouldIgnoreFile(base) {
		return nil
	}

	ev := &Event{Path: path}
	switch {
	case e.Has(fsnotify.Create):
		ev.Op = "create"
	case e.Has(fsnotify.Write):
		ev.Op = "write"
	case e.Has(fsnotify.Remove):
		ev.Op = "remove"
	case e.Has(fsnotify.Rename):
		ev.Op = "rename"
	default:
		return nil
	}
	return ev
}

func dedupe(events []Event) []Event {
	seen := make(map[string]Event)
	for _, ev := range events {
		existing, ok := seen[ev.Path]
		if !ok || evOpPriority(ev.Op) > evOpPriority(existing.Op) {
			seen[ev.Path] = ev
		}
	}
	var result []Event
	for _, ev := range seen {
		result = append(result, ev)
	}
	return result
}

func evOpPriority(op string) int {
	switch op {
	case "remove":
		return 3
	case "create":
		return 2
	case "write":
		return 1
	default:
		return 0
	}
}

func shouldIgnoreFile(name string) bool {
	if strings.HasPrefix(name, ".") && name != ".gitignore" {
		return true
	}
	switch {
	case strings.HasPrefix(name, "~$"):
		return true
	case strings.HasSuffix(name, ".tmp"):
		return true
	case strings.HasSuffix(name, ".swp"):
		return true
	case strings.HasSuffix(name, ".swx"):
		return true
	case strings.HasSuffix(name, ".sypora-conflict-"):
		return true // ignore our own conflict files
	}
	return false
}

func shouldIgnoreDir(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return false
}
