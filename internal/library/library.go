package library

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"home-podcast/internal/metadata"
	"home-podcast/internal/models"
)

// Library monitors an audio directory and keeps in-memory metadata for clients.
type Library struct {
	root    string
	allowed map[string]struct{}
	watcher *fsnotify.Watcher
	logger  *log.Logger

	mu       sync.RWMutex
	episodes []models.Episode

	refreshMu    sync.Mutex
	refreshTimer *time.Timer
	refreshDelay time.Duration

	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// NewLibrary creates a new Library and starts watching the provided root path.
func NewLibrary(root string, allowed []string, debounce time.Duration, logger *log.Logger) (*Library, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = log.Default()
	}

	lib := &Library{
		root:         root,
		allowed:      make(map[string]struct{}, len(allowed)),
		watcher:      watcher,
		logger:       logger,
		refreshDelay: debounce,
		done:         make(chan struct{}),
	}

	for _, ext := range allowed {
		lib.allowed[strings.ToLower(ext)] = struct{}{}
	}

	lib.addWatchRecursive(root)

	if err := lib.refresh(); err != nil {
		watcher.Close()
		return nil, err
	}

	lib.wg.Add(1)
	go lib.run()

	return lib, nil
}

// Close stops the watcher and cleans up resources.
func (l *Library) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)

		l.refreshMu.Lock()
		if l.refreshTimer != nil {
			l.refreshTimer.Stop()
			l.refreshTimer = nil
		}
		l.refreshMu.Unlock()

		l.closeErr = l.watcher.Close()
		l.wg.Wait()
	})
	return l.closeErr
}

// ListEpisodes returns a snapshot of the cached metadata.
func (l *Library) ListEpisodes() []models.Episode {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]models.Episode, len(l.episodes))
	copy(result, l.episodes)
	return result
}

func (l *Library) run() {
	defer l.wg.Done()

	for {
		select {
		case event, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			l.handleEvent(event)
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			l.logger.Printf("watcher error: %v", err)
		case <-l.done:
			return
		}
	}
}

func (l *Library) handleEvent(event fsnotify.Event) {
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			l.addWatchRecursive(event.Name)
		}
	}

	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
		if l.isAllowed(event.Name) || event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			l.scheduleRefresh()
		}
	}
}

func (l *Library) refresh() error {
	var episodes []models.Episode

	err := filepath.WalkDir(l.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			l.logger.Printf("walk error for %s: %v", path, err)
			return nil
		}

		if d.IsDir() {
			return nil
		}

		if !l.isAllowed(path) {
			return nil
		}

		episode, err := metadata.BuildEpisode(path, l.root)
		if err != nil {
			l.logger.Printf("metadata error for %s: %v", path, err)
			return nil
		}

		episodes = append(episodes, episode)
		return nil
	})
	if err != nil {
		return err
	}

	sort.SliceStable(episodes, func(i, j int) bool {
		if episodes[i].RelativePath == episodes[j].RelativePath {
			return episodes[i].Filename < episodes[j].Filename
		}
		return episodes[i].RelativePath < episodes[j].RelativePath
	})

	l.mu.Lock()
	l.episodes = episodes
	l.mu.Unlock()

	l.logger.Printf("library refreshed with %d episodes", len(episodes))
	return nil
}

func (l *Library) scheduleRefresh() {
	select {
	case <-l.done:
		return
	default:
	}

	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()

	if l.refreshTimer != nil {
		l.refreshTimer.Stop()
	}

	var timer *time.Timer
	timer = time.AfterFunc(l.refreshDelay, func() {
		if err := l.refresh(); err != nil {
			l.logger.Printf("refresh error: %v", err)
		}

		l.refreshMu.Lock()
		if l.refreshTimer == timer {
			l.refreshTimer = nil
		}
		l.refreshMu.Unlock()
	})

	l.refreshTimer = timer
}

func (l *Library) addWatchRecursive(path string) {
	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			l.logger.Printf("walk error for %s: %v", p, err)
			return nil
		}

		if d.IsDir() {
			if err := l.watcher.Add(p); err != nil {
				l.logger.Printf("watcher add failure for %s: %v", p, err)
			}
		}
		return nil
	})
}

func (l *Library) isAllowed(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := l.allowed[ext]
	return ok
}
