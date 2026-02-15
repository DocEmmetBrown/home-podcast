package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAllowedExtensionsIsolation(t *testing.T) {
	first := AllowedExtensions()
	second := AllowedExtensions()

	if len(first) == 0 {
		t.Fatalf("expected allowed extensions to be non-empty")
	}

	first[0] = ".doesnotexist"
	if first[0] == second[0] {
		t.Fatalf("mutating returned slice should not affect internal configuration")
	}
}

func TestResolveAudioRootDefaultAndCustom(t *testing.T) {
	temp := t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	if err := os.Chdir(temp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Setenv("PODCAST_AUDIO_DIR", "")

	path, err := ResolveAudioRoot()
	if err != nil {
		t.Fatalf("ResolveAudioRoot default: %v", err)
	}

	expected := filepath.Join(temp, "audio")
	assertSamePath(t, path, expected)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat default dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected audio root to be directory")
	}

	tempHome := filepath.Join(temp, "home")
	if err := os.Mkdir(tempHome, 0o755); err != nil {
		t.Fatalf("mkdir temp home: %v", err)
	}

	t.Setenv("HOME", tempHome)
	t.Setenv("PODCAST_AUDIO_DIR", "~/episodes")

	path, err = ResolveAudioRoot()
	if err != nil {
		t.Fatalf("ResolveAudioRoot tilde: %v", err)
	}

	expected = filepath.Join(tempHome, "episodes")
	assertSamePath(t, path, expected)
}

func TestResolveTokenFile(t *testing.T) {
	temp := t.TempDir()

	t.Setenv("PODCAST_TOKEN_FILE", "")
	if path, ok, err := ResolveTokenFile(); err != nil || ok || path != "" {
		t.Fatalf("expected no file when env unset, got %q %t %v", path, ok, err)
	}

	tokenFile := filepath.Join(temp, "tokens", "feed.tokens")
	t.Setenv("PODCAST_TOKEN_FILE", tokenFile)

	path, ok, err := ResolveTokenFile()
	if err != nil {
		t.Fatalf("ResolveTokenFile: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok flag when env set")
	}
	assertSamePath(t, path, tokenFile)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("expected token path to be a regular file")
	}
}

func TestListenAddr(t *testing.T) {
	t.Setenv("PODCAST_LISTEN_ADDR", "")
	if ListenAddr() != "127.0.0.1:8080" {
		t.Fatalf("expected default listen address")
	}

	t.Setenv("PODCAST_LISTEN_ADDR", "localhost:9000")
	if ListenAddr() != "localhost:9000" {
		t.Fatalf("expected custom listen address")
	}
}

func TestRefreshDebounce(t *testing.T) {
	t.Setenv("PODCAST_REFRESH_DEBOUNCE_MS", "")
	if RefreshDebounce() != 500*time.Millisecond {
		t.Fatalf("expected default debounce")
	}

	t.Setenv("PODCAST_REFRESH_DEBOUNCE_MS", "1500")
	if RefreshDebounce() != 1500*time.Millisecond {
		t.Fatalf("expected custom debounce")
	}

	t.Setenv("PODCAST_REFRESH_DEBOUNCE_MS", "not-a-number")
	if RefreshDebounce() != 500*time.Millisecond {
		t.Fatalf("expected fallback debounce on parse error")
	}

	t.Setenv("PODCAST_REFRESH_DEBOUNCE_MS", "-10")
	if RefreshDebounce() != 500*time.Millisecond {
		t.Fatalf("expected fallback debounce on negative value")
	}
}

func TestValidateListenAddr(t *testing.T) {
	valid := []string{"127.0.0.1:8080", "localhost:9000", "[::1]:7000"}
	for _, addr := range valid {
		if err := ValidateListenAddr(addr); err != nil {
			t.Fatalf("expected %s to be valid: %v", addr, err)
		}
	}

	invalid := []string{"0.0.0.0:80", "192.168.1.1:1234", ":8080"}
	for _, addr := range invalid {
		if err := ValidateListenAddr(addr); err == nil {
			t.Fatalf("expected %s to be rejected", addr)
		}
	}
}

func TestResolveFeedMetadataDefaultsAndEnv(t *testing.T) {
	t.Setenv("PODCAST_FEED_CONFIG", "")
	t.Setenv("PODCAST_FEED_TITLE", "")
	t.Setenv("PODCAST_FEED_DESCRIPTION", "")
	t.Setenv("PODCAST_FEED_LANGUAGE", "")
	t.Setenv("PODCAST_FEED_AUTHOR", "")

	meta, err := ResolveFeedMetadata()
	if err != nil {
		t.Fatalf("ResolveFeedMetadata: %v", err)
	}

	if meta.Title != defaultFeedTitle || meta.Description != defaultFeedDescription || meta.Language != defaultFeedLanguage || meta.Author != "" {
		t.Fatalf("expected defaults, got %+v", meta)
	}

	t.Setenv("PODCAST_FEED_TITLE", "My Cast")
	t.Setenv("PODCAST_FEED_DESCRIPTION", "All the episodes")
	t.Setenv("PODCAST_FEED_LANGUAGE", "fr")
	t.Setenv("PODCAST_FEED_AUTHOR", "Jane Doe")

	meta, err = ResolveFeedMetadata()
	if err != nil {
		t.Fatalf("ResolveFeedMetadata overrides: %v", err)
	}

	if meta.Title != "My Cast" || meta.Description != "All the episodes" || meta.Language != "fr" || meta.Author != "Jane Doe" {
		t.Fatalf("expected env overrides, got %+v", meta)
	}
}

func TestResolveFeedMetadataFromFile(t *testing.T) {
	temp := t.TempDir()
	configPath := filepath.Join(temp, "feed.yaml")
	content := "" +
		"title: File Title\n" +
		"description: File Description\n" +
		"language: es\n" +
		"author: File Author\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("PODCAST_FEED_CONFIG", configPath)
	t.Setenv("PODCAST_FEED_TITLE", "")
	t.Setenv("PODCAST_FEED_DESCRIPTION", "")
	t.Setenv("PODCAST_FEED_LANGUAGE", "")
	t.Setenv("PODCAST_FEED_AUTHOR", "")

	meta, err := ResolveFeedMetadata()
	if err != nil {
		t.Fatalf("ResolveFeedMetadata: %v", err)
	}

	if meta.Title != "File Title" || meta.Description != "File Description" || meta.Language != "es" || meta.Author != "File Author" {
		t.Fatalf("expected file-derived metadata, got %+v", meta)
	}

	t.Setenv("PODCAST_FEED_TITLE", "Env Title")
	meta, err = ResolveFeedMetadata()
	if err != nil {
		t.Fatalf("ResolveFeedMetadata env override: %v", err)
	}
	if meta.Title != "Env Title" {
		t.Fatalf("expected env override to win, got %s", meta.Title)
	}
}

func TestResolveTokenFileExistingFileNotOverwritten(t *testing.T) {
	temp := t.TempDir()
	tokenFile := filepath.Join(temp, "tokens.txt")
	if err := os.WriteFile(tokenFile, []byte("existing-token\n"), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	t.Setenv("PODCAST_TOKEN_FILE", tokenFile)

	path, ok, err := ResolveTokenFile()
	if err != nil {
		t.Fatalf("ResolveTokenFile: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok flag")
	}
	assertSamePath(t, path, tokenFile)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if string(data) != "existing-token\n" {
		t.Fatalf("expected existing content to be preserved, got %q", data)
	}
}

func TestResolveTokenFileTildeExpansion(t *testing.T) {
	temp := t.TempDir()
	fakeHome := filepath.Join(temp, "fakehome")
	if err := os.MkdirAll(fakeHome, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("PODCAST_TOKEN_FILE", "~/tokens.txt")

	path, ok, err := ResolveTokenFile()
	if err != nil {
		t.Fatalf("ResolveTokenFile: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok flag")
	}

	expected := filepath.Join(fakeHome, "tokens.txt")
	assertSamePath(t, path, expected)
}

func TestResolveTokenFileCreatesParentDir(t *testing.T) {
	temp := t.TempDir()
	tokenFile := filepath.Join(temp, "deep", "nested", "tokens.txt")
	t.Setenv("PODCAST_TOKEN_FILE", tokenFile)

	path, ok, err := ResolveTokenFile()
	if err != nil {
		t.Fatalf("ResolveTokenFile: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok flag")
	}
	assertSamePath(t, path, tokenFile)

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("parent dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected parent to be a directory")
	}
}

func TestResolveConfigPath(t *testing.T) {
	temp := t.TempDir()
	fakeHome := filepath.Join(temp, "fakehome")
	if err := os.MkdirAll(fakeHome, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("HOME", fakeHome)

	resolved, err := resolveConfigPath("~/config.yaml")
	if err != nil {
		t.Fatalf("resolveConfigPath tilde: %v", err)
	}
	expected := filepath.Join(fakeHome, "config.yaml")
	if resolved != expected {
		t.Fatalf("expected %s, got %s", expected, resolved)
	}

	absPath := filepath.Join(temp, "absolute.yaml")
	resolved, err = resolveConfigPath(absPath)
	if err != nil {
		t.Fatalf("resolveConfigPath abs: %v", err)
	}
	if resolved != absPath {
		t.Fatalf("expected %s, got %s", absPath, resolved)
	}
}

func assertSamePath(t *testing.T, got, want string) {
	t.Helper()
	resolvedGot, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("eval symlinks for %s: %v", got, err)
	}
	resolvedWant, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("eval symlinks for %s: %v", want, err)
	}
	if resolvedGot != resolvedWant {
		t.Fatalf("expected %s, got %s", resolvedWant, resolvedGot)
	}
}
