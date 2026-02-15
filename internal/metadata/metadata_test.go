package metadata

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildEpisodeWithFallbackMetadata(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	path := filepath.Join(sub, "Episode One.wav")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	episode, err := BuildEpisode(path, root)
	if err != nil {
		t.Fatalf("BuildEpisode: %v", err)
	}

	relative := filepath.ToSlash(filepath.Join("sub", "Episode One.wav"))
	if episode.ID != relative {
		t.Fatalf("expected id %s, got %s", relative, episode.ID)
	}
	if episode.Title != "Episode One" {
		t.Fatalf("expected title fallback to file stem, got %s", episode.Title)
	}
	if episode.DurationSeconds != nil {
		t.Fatalf("expected duration to be nil for non-mp3")
	}
	if episode.BitrateKbps != nil {
		t.Fatalf("expected bitrate to be nil for non-mp3")
	}

	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	expectedTime := stat.ModTime().UTC().Round(time.Second)
	if !episode.ModifiedAt.Equal(expectedTime) {
		t.Fatalf("expected modified time %s, got %s", expectedTime, episode.ModifiedAt)
	}
}

func TestBuildEpisodeWithInvalidMP3(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "broken.mp3")
	if err := os.WriteFile(path, []byte("not really an mp3"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	episode, err := BuildEpisode(path, root)
	if err != nil {
		t.Fatalf("BuildEpisode unexpected error: %v", err)
	}

	if episode.DurationSeconds != nil {
		t.Fatalf("expected duration to be nil on decode error")
	}
	if episode.BitrateKbps != nil {
		t.Fatalf("expected bitrate to remain nil on decode error")
	}
}

func TestReadTagsAndOptionalString(t *testing.T) {
	title, artist, album := readTags("/no/such/file.wav")
	if title != "" || artist != nil || album != nil {
		t.Fatalf("expected empty metadata on failure")
	}

	if optionalString("   ") != nil {
		t.Fatalf("expected nil for whitespace input")
	}

	value := optionalString("value")
	if value == nil || *value != "value" {
		t.Fatalf("expected pointer to trimmed value")
	}
}

func TestComputeMP3DurationErrors(t *testing.T) {
	if _, err := computeMP3Duration("/does/not/exist.mp3"); err == nil {
		t.Fatalf("expected error when file is missing")
	}

	root := t.TempDir()
	path := filepath.Join(root, "bad.mp3")
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	duration, err := computeMP3Duration(path)
	if err == nil {
		t.Fatalf("expected decode error for invalid mp3 data")
	}
	if duration != 0 {
		t.Fatalf("expected zero duration on error, got %f", duration)
	}
}

func TestBuildEpisodeNonexistentFile(t *testing.T) {
	root := t.TempDir()
	_, err := BuildEpisode(filepath.Join(root, "missing.wav"), root)
	if err == nil {
		t.Fatalf("expected error for nonexistent file")
	}
}

func TestBuildEpisodeNonMP3HasNoDuration(t *testing.T) {
	root := t.TempDir()
	for _, ext := range []string{".m4a", ".aac", ".flac", ".ogg"} {
		path := filepath.Join(root, "track"+ext)
		if err := os.WriteFile(path, []byte("audio data"), 0o644); err != nil {
			t.Fatalf("write %s: %v", ext, err)
		}

		ep, err := BuildEpisode(path, root)
		if err != nil {
			t.Fatalf("BuildEpisode(%s): %v", ext, err)
		}
		if ep.DurationSeconds != nil {
			t.Fatalf("expected nil duration for %s", ext)
		}
		if ep.BitrateKbps != nil {
			t.Fatalf("expected nil bitrate for %s", ext)
		}
	}
}

func TestBuildEpisodeFilenameFallbackTitle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "My Great Episode.flac")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ep, err := BuildEpisode(path, root)
	if err != nil {
		t.Fatalf("BuildEpisode: %v", err)
	}
	if ep.Title != "My Great Episode" {
		t.Fatalf("expected title 'My Great Episode', got %q", ep.Title)
	}
}

func TestBuildEpisodeFilesizeAndFilename(t *testing.T) {
	root := t.TempDir()
	content := []byte("some audio content here")
	path := filepath.Join(root, "clip.wav")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ep, err := BuildEpisode(path, root)
	if err != nil {
		t.Fatalf("BuildEpisode: %v", err)
	}
	if ep.FilesizeBytes != int64(len(content)) {
		t.Fatalf("expected filesize %d, got %d", len(content), ep.FilesizeBytes)
	}
	if ep.Filename != "clip.wav" {
		t.Fatalf("expected filename 'clip.wav', got %q", ep.Filename)
	}
	if ep.RelativePath != "clip.wav" {
		t.Fatalf("expected relative path 'clip.wav', got %q", ep.RelativePath)
	}
}
