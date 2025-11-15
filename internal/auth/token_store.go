package auth

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TokenStore manages a set of authorized feed tokens backed by a single file on disk.
type TokenStore struct {
	file         string
	logger       *log.Logger
	watcher      *fsnotify.Watcher
	refreshDelay time.Duration

	mu     sync.RWMutex
	tokens map[string]struct{}

	refreshMu    sync.Mutex
	refreshTimer *time.Timer
	done         chan struct{}
	wg           sync.WaitGroup
	closeOnce    sync.Once
	closeErr     error
}

// NewTokenStore creates a TokenStore backed by the provided token file path.
// Each non-empty trimmed line inside the file is treated as a valid token.
func NewTokenStore(filePath string, debounce time.Duration, logger *log.Logger) (*TokenStore, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = log.Default()
	}

	s := &TokenStore{
		file:         filepath.Clean(filePath),
		logger:       logger,
		watcher:      watcher,
		refreshDelay: debounce,
		tokens:       make(map[string]struct{}),
		done:         make(chan struct{}),
	}

	if err := s.refresh(); err != nil {
		watcher.Close()
		return nil, err
	}

	dir := filepath.Dir(s.file)
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return nil, err
	}

	if err := watcher.Add(s.file); err != nil {
		s.logger.Printf("token watcher could not watch file directly: %v", err)
	}

	s.wg.Add(1)
	go s.run()

	return s, nil
}

// Close stops the file watcher and releases resources.
func (s *TokenStore) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)

		s.refreshMu.Lock()
		if s.refreshTimer != nil {
			s.refreshTimer.Stop()
			s.refreshTimer = nil
		}
		s.refreshMu.Unlock()

		s.closeErr = s.watcher.Close()
		s.wg.Wait()
	})
	return s.closeErr
}

// IsValidToken reports whether the provided token is authorized.
func (s *TokenStore) IsValidToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.tokens[token]
	return ok
}

func (s *TokenStore) run() {
	defer s.wg.Done()

	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			s.handleEvent(event)
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			s.logger.Printf("token watcher error: %v", err)
		case <-s.done:
			return
		}
	}
}

func (s *TokenStore) handleEvent(event fsnotify.Event) {
	cleanName := filepath.Clean(event.Name)
	if cleanName != s.file {
		return
	}

	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
		s.scheduleRefresh()
	}
}

func (s *TokenStore) scheduleRefresh() {
	select {
	case <-s.done:
		return
	default:
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	if s.refreshTimer != nil {
		s.refreshTimer.Stop()
	}

	s.refreshTimer = time.AfterFunc(s.refreshDelay, func() {
		if err := s.refresh(); err != nil {
			s.logger.Printf("token refresh error: %v", err)
		}

		s.refreshMu.Lock()
		if s.refreshTimer != nil {
			s.refreshTimer = nil
		}
		s.refreshMu.Unlock()
	})
}

func (s *TokenStore) refresh() error {
	data, err := os.ReadFile(s.file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.mu.Lock()
			s.tokens = make(map[string]struct{})
			s.mu.Unlock()
			s.logger.Printf("token file %s missing; no tokens loaded", s.file)
			return nil
		}
		return err
	}

	lines := strings.Split(string(data), "\n")
	tokens := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		token := strings.TrimSpace(line)
		if token != "" {
			tokens[token] = struct{}{}
		}
	}

	s.mu.Lock()
	s.tokens = tokens
	s.mu.Unlock()

	s.logger.Printf("loaded %d feed tokens", len(tokens))
	return nil
}
