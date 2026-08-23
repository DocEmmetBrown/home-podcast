package library

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLibraryWatchesAndRefreshes(t *testing.T) {
	root := t.TempDir()
	initial := filepath.Join(root, "initial.wav")
	if err := os.WriteFile(initial, []byte("one"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	lib, err := NewLibrary(root, []string{".wav"}, 10*time.Millisecond, logger)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	t.Cleanup(func() {
		if err := lib.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 1 }, "initial scan")

	second := filepath.Join(root, "second.wav")
	if err := os.WriteFile(second, []byte("two"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 2 }, "detect second file")

	subdir := filepath.Join(root, "nested")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	nested := filepath.Join(subdir, "third.wav")
	if err := os.WriteFile(nested, []byte("three"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}
	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 3 }, "detect nested file")

	renamePath := filepath.Join(root, "initial-renamed.wav")
	if err := os.Rename(initial, renamePath); err != nil {
		t.Fatalf("rename file: %v", err)
	}
	waitFor(t, func() bool {
		for _, ep := range lib.ListEpisodes() {
			if ep.Filename == "initial-renamed.wav" {
				return true
			}
		}
		return false
	}, "detect rename")

	if err := os.Remove(second); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 2 }, "reflect removal")

	eps := lib.ListEpisodes()
	if len(eps) == 0 {
		t.Fatalf("expected episodes to be present")
	}
	eps[0].Title = "mutated"
	if lib.ListEpisodes()[0].Title == "mutated" {
		t.Fatalf("expected ListEpisodes to return a defensive copy")
	}
}

func TestLibraryIgnoresNonAudioFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("text"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "song.wav"), []byte("audio"), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	lib, err := NewLibrary(root, []string{".wav"}, 10*time.Millisecond, logger)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 1 }, "initial scan")

	eps := lib.ListEpisodes()
	if eps[0].Filename != "song.wav" {
		t.Fatalf("expected song.wav, got %s", eps[0].Filename)
	}

	// Adding another non-audio file should not change the count.
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("doc"), 0o644); err != nil {
		t.Fatalf("write md: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if len(lib.ListEpisodes()) != 1 {
		t.Fatalf("expected still 1 episode, got %d", len(lib.ListEpisodes()))
	}
}

func TestLibraryMultipleExtensions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.mp3"), []byte("mp3"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.flac"), []byte("flac"), 0o644); err != nil {
		t.Fatalf("write flac: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "c.txt"), []byte("text"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	lib, err := NewLibrary(root, []string{".mp3", ".flac"}, 10*time.Millisecond, logger)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 2 }, "scan mp3 and flac")
}

func TestLibraryEmptyDirectory(t *testing.T) {
	root := t.TempDir()

	logger := log.New(io.Discard, "", 0)
	lib, err := NewLibrary(root, []string{".wav"}, 10*time.Millisecond, logger)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	time.Sleep(50 * time.Millisecond)

	if len(lib.ListEpisodes()) != 0 {
		t.Fatalf("expected 0 episodes for empty dir, got %d", len(lib.ListEpisodes()))
	}

	// Adding a file to an initially empty library should be detected.
	if err := os.WriteFile(filepath.Join(root, "new.wav"), []byte("audio"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 1 }, "detect new file")
}

func TestLibraryPreexistingSubdirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "albums", "jazz")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "track.wav"), []byte("jazz"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	lib, err := NewLibrary(root, []string{".wav"}, 10*time.Millisecond, logger)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 1 }, "scan pre-existing nested file")

	eps := lib.ListEpisodes()
	if eps[0].Filename != "track.wav" {
		t.Fatalf("expected track.wav, got %s", eps[0].Filename)
	}
}

func TestLibraryOrdersChaptersNaturally(t *testing.T) {
	root := t.TempDir()
	book := filepath.Join(root, "book")
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	names := []string{"chapter 10.wav", "chapter 2.wav", "chapter 1.wav", "chapter 20.wav"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(book, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	logger := log.New(io.Discard, "", 0)
	lib, err := NewLibrary(root, []string{".wav"}, 10*time.Millisecond, logger)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	waitFor(t, func() bool { return len(lib.ListEpisodes()) == len(names) }, "scan chapters")

	want := []string{"chapter 1.wav", "chapter 2.wav", "chapter 10.wav", "chapter 20.wav"}
	eps := lib.ListEpisodes()
	for i := range want {
		if eps[i].Filename != want[i] {
			t.Fatalf("episode %d = %s, want %s", i, eps[i].Filename, want[i])
		}
	}
}

func TestLibraryKeepsExistingEpisodesWhileImporting(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.wav"), []byte("known"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	lib, err := NewLibrary(root, []string{".wav"}, 10*time.Millisecond, logger)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	waitFor(t, func() bool { return len(lib.ListEpisodes()) == 1 }, "initial scan")

	const bulk = 40
	for i := 0; i < bulk; i++ {
		name := filepath.Join(root, "bulk-"+strconv.Itoa(i)+".wav")
		if err := os.WriteFile(name, []byte(name), 0o644); err != nil {
			t.Fatalf("write bulk file: %v", err)
		}
	}

	// The known episode must stay served for the whole duration of the import.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		eps := lib.ListEpisodes()
		found := false
		for _, ep := range eps {
			if ep.Filename == "known.wav" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("known episode disappeared during import (%d episodes served)", len(eps))
		}
		if len(eps) == bulk+1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timeout waiting for bulk import, got %d episodes", len(lib.ListEpisodes()))
}

func waitFor(t *testing.T, predicate func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", label)
}
