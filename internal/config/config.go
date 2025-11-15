package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var allowedExtensions = []string{
	".mp3",
	".m4a",
	".aac",
	".wav",
	".flac",
	".ogg",
}

const (
	defaultListenAddr        = "127.0.0.1:8080"
	defaultRefreshDebounceMS = 500
	defaultFeedTitle         = "Home Podcast"
	defaultFeedDescription   = "Private podcast feed generated from the local audio library."
	defaultFeedLanguage      = "en"
)

// AllowedExtensions returns the list of supported audio file extensions (lowercase).
func AllowedExtensions() []string {
	result := make([]string, len(allowedExtensions))
	copy(result, allowedExtensions)
	return result
}

// ResolveAudioRoot returns the directory that should be scanned for audio files.
// The directory is created when it does not yet exist.
func ResolveAudioRoot() (string, error) {
	dir := strings.TrimSpace(os.Getenv("PODCAST_AUDIO_DIR"))
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cwd, "audio")
	}

	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}

	return abs, nil
}

// ListenAddr returns the TCP address the HTTP server should bind to.
func ListenAddr() string {
	addr := strings.TrimSpace(os.Getenv("PODCAST_LISTEN_ADDR"))
	if addr == "" {
		return defaultListenAddr
	}
	return addr
}

// RefreshDebounce returns the duration to wait before refreshing the library
// after file-system change events.
func RefreshDebounce() time.Duration {
	value := strings.TrimSpace(os.Getenv("PODCAST_REFRESH_DEBOUNCE_MS"))
	if value == "" {
		return time.Duration(defaultRefreshDebounceMS) * time.Millisecond
	}

	ms, err := strconv.Atoi(value)
	if err != nil || ms < 0 {
		return time.Duration(defaultRefreshDebounceMS) * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

// ValidateListenAddr ensures the configured listen address is restricted to localhost.
func ValidateListenAddr(addr string) error {
	addr = strings.TrimSpace(strings.ToLower(addr))
	if strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:") || strings.HasPrefix(addr, "[::1]:") {
		return nil
	}
	return errors.New("listen address must bind to localhost for security")
}

// ResolveTokenFile returns the absolute path to the feed token file when configured.
// The file is created if it does not already exist. When no file is configured the
// second return value will be false.
func ResolveTokenFile() (string, bool, error) {
	path := strings.TrimSpace(os.Getenv("PODCAST_TOKEN_FILE"))
	if path == "" {
		return "", false, nil
	}

	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", false, err
	}

	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				return "", false, err
			}
			if err := file.Close(); err != nil {
				return "", false, err
			}
		} else {
			return "", false, err
		}
	}

	return abs, true, nil
}

// FeedMetadata represents the static metadata used to render the podcast RSS feed.
type FeedMetadata struct {
	Title       string
	Description string
	Language    string
	Author      string
}

type feedMetadataYAML struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Language    string `yaml:"language"`
	Author      string `yaml:"author"`
}

// ResolveFeedMetadata returns the podcast feed metadata after applying defaults,
// YAML configuration (when enabled), and environment variable overrides.
func ResolveFeedMetadata() (FeedMetadata, error) {
	meta := FeedMetadata{
		Title:       defaultFeedTitle,
		Description: defaultFeedDescription,
		Language:    defaultFeedLanguage,
	}

	configPath := strings.TrimSpace(os.Getenv("PODCAST_FEED_CONFIG"))
	if configPath != "" {
		resolved, err := resolveConfigPath(configPath)
		if err != nil {
			return FeedMetadata{}, err
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return FeedMetadata{}, err
		}
		var yamlConfig feedMetadataYAML
		if err := yaml.Unmarshal(data, &yamlConfig); err != nil {
			return FeedMetadata{}, err
		}
		if value := strings.TrimSpace(yamlConfig.Title); value != "" {
			meta.Title = value
		}
		if value := strings.TrimSpace(yamlConfig.Description); value != "" {
			meta.Description = value
		}
		if value := strings.TrimSpace(yamlConfig.Language); value != "" {
			meta.Language = value
		}
		if value := strings.TrimSpace(yamlConfig.Author); value != "" {
			meta.Author = value
		}
	}

	if value := strings.TrimSpace(os.Getenv("PODCAST_FEED_TITLE")); value != "" {
		meta.Title = value
	}
	if value := strings.TrimSpace(os.Getenv("PODCAST_FEED_DESCRIPTION")); value != "" {
		meta.Description = value
	}
	if value := strings.TrimSpace(os.Getenv("PODCAST_FEED_LANGUAGE")); value != "" {
		meta.Language = value
	}
	if value := strings.TrimSpace(os.Getenv("PODCAST_FEED_AUTHOR")); value != "" {
		meta.Author = value
	}

	return meta, nil
}

func resolveConfigPath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	}

	return filepath.Abs(path)
}
