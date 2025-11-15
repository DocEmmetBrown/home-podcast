package library

import (
	"io"
	"log"
	"os"
	"path/filepath"
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
