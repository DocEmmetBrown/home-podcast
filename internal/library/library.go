package library

import (
	"io/fs"
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
	"home-podcast/internal/naturalorder"
)

// Newly parsed episodes become visible to clients as soon as one of these
// thresholds is reached, so a large import never hides the existing library.
const (
	publishBatchSize = 8
	publishInterval  = 2 * time.Second
)

// Library monitors an audio directory and keeps in-memory metadata for clients.
type Library struct {
	root    string
	allowed map[string]struct{}
	watcher *fsnotify.Watcher
	logger  *log.Logger

	mu       sync.RWMutex
	index    map[string]models.Episode
	episodes []models.Episode

	refreshMu    sync.Mutex
	refreshTimer *time.Timer
	refreshDelay time.Duration

	scanRequests chan struct{}

	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

type fileRef struct {
	path     string
	relative string
	size     int64
	modTime  time.Time
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
		index:        make(map[string]models.Episode),
		refreshDelay: debounce,
		scanRequests: make(chan struct{}, 1),
		done:         make(chan struct{}),
	}

	for _, ext := range allowed {
		lib.allowed[strings.ToLower(ext)] = struct{}{}
	}

	lib.addWatchRecursive(root)

	lib.wg.Add(2)
	go lib.run()
	go lib.scanLoop()

	// The first scan runs in the background so the HTTP server can start serving
	// immediately, even with a large or slow library.
	lib.requestScan()

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

// scanLoop serialises scans so bursts of file events cannot stack up concurrent
// metadata parses.
func (l *Library) scanLoop() {
	defer l.wg.Done()

	for {
		select {
		case <-l.done:
			return
		case <-l.scanRequests:
			l.refresh()
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

// refresh walks the library and only parses files that are new or changed,
// publishing intermediate results so clients keep seeing the known episodes
// while a large import is still being processed.
func (l *Library) refresh() {
	files := l.collectFiles()

	seen := make(map[string]struct{}, len(files))
	pending := 0
	parsed := 0
	lastPublish := time.Now()

	for _, f := range files {
		if l.closed() {
			return
		}

		seen[f.relative] = struct{}{}
		if l.isCurrent(f) {
			continue
		}

		episode, err := metadata.BuildEpisode(f.path, l.root)
		if err != nil {
			l.logger.Printf("metadata error for %s: %v", f.path, err)
			continue
		}

		l.store(episode)
		pending++
		parsed++

		if pending >= publishBatchSize || time.Since(lastPublish) >= publishInterval {
			l.publish()
			pending = 0
			lastPublish = time.Now()
		}
	}

	removed := l.prune(seen)
	if pending > 0 || removed > 0 {
		l.publish()
	}

	if parsed > 0 || removed > 0 {
		l.logger.Printf("library updated: %d parsed, %d removed, %d episodes total", parsed, removed, l.count())
	}
}

func (l *Library) collectFiles() []fileRef {
	var files []fileRef

	err := filepath.WalkDir(l.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			l.logger.Printf("walk error for %s: %v", path, err)
			return nil
		}

		if d.IsDir() || !l.isAllowed(path) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			l.logger.Printf("stat error for %s: %v", path, err)
			return nil
		}

		relative, err := filepath.Rel(l.root, path)
		if err != nil {
			relative = filepath.Base(path)
		}

		files = append(files, fileRef{
			path:     path,
			relative: filepath.ToSlash(relative),
			size:     info.Size(),
			modTime:  info.ModTime().UTC().Round(time.Second),
		})
		return nil
	})
	if err != nil {
		l.logger.Printf("library walk error: %v", err)
	}

	return files
}

// isCurrent reports whether cached metadata still matches the file on disk.
func (l *Library) isCurrent(f fileRef) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	episode, ok := l.index[f.relative]
	return ok && episode.FilesizeBytes == f.size && episode.ModifiedAt.Equal(f.modTime)
}

func (l *Library) store(episode models.Episode) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.index[episode.RelativePath] = episode
}

func (l *Library) prune(seen map[string]struct{}) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	removed := 0
	for relative := range l.index {
		if _, ok := seen[relative]; !ok {
			delete(l.index, relative)
			removed++
		}
	}
	return removed
}

func (l *Library) publish() {
	l.mu.Lock()
	defer l.mu.Unlock()

	episodes := make([]models.Episode, 0, len(l.index))
	for _, episode := range l.index {
		episodes = append(episodes, episode)
	}

	sort.SliceStable(episodes, func(i, j int) bool {
		return naturalorder.Less(episodes[i].RelativePath, episodes[j].RelativePath)
	})

	l.episodes = episodes
}

func (l *Library) count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.episodes)
}

func (l *Library) closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

func (l *Library) requestScan() {
	if l.closed() {
		return
	}

	select {
	case l.scanRequests <- struct{}{}:
	default:
		// A scan is already queued and will pick up the latest state.
	}
}

func (l *Library) scheduleRefresh() {
	if l.closed() {
		return
	}

	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()

	if l.refreshTimer != nil {
		l.refreshTimer.Stop()
	}

	var timer *time.Timer
	timer = time.AfterFunc(l.refreshDelay, func() {
		l.requestScan()

		l.refreshMu.Lock()
		if l.refreshTimer == timer {
			l.refreshTimer = nil
		}
		l.refreshMu.Unlock()
	})

	l.refreshTimer = timer
}

func (l *Library) addWatchRecursive(path string) {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
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
