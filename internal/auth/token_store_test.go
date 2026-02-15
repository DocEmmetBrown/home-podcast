package auth

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTokenStoreLoadsAndWatchesTokens(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tokens.txt")
	writeTokenFile(t, file, "alpha\n")

	store, err := NewTokenStore(file, 20*time.Millisecond, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	if !store.IsValidToken("alpha") {
		t.Fatalf("expected initial token to be valid")
	}

	if store.IsValidToken("beta") {
		t.Fatalf("unexpected token accepted")
	}

	writeTokenFile(t, file, "alpha\n\n beta \n")
	waitForToken(t, store, "beta", true)

	writeTokenFile(t, file, "beta\n")
	waitForToken(t, store, "alpha", false)

	if store.IsValidToken("") {
		t.Fatalf("expected empty token to be rejected")
	}
}

func TestTokenStoreHandlesFileRemoval(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tokens.txt")
	writeTokenFile(t, file, "alpha\n")

	store, err := NewTokenStore(file, 5*time.Millisecond, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if !store.IsValidToken("alpha") {
		t.Fatalf("expected initial token to be valid")
	}

	if err := os.Remove(file); err != nil {
		t.Fatalf("remove token file: %v", err)
	}

	waitForToken(t, store, "alpha", false)
}

func writeTokenFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}
}

func waitForToken(t *testing.T, store *TokenStore, token string, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.IsValidToken(token) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for token %s to reach state %v", token, want)
}

func TestTokenStoreSiblingFileIgnored(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tokens.txt")
	writeTokenFile(t, file, "alpha\n")

	store, err := NewTokenStore(file, 5*time.Millisecond, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if !store.IsValidToken("alpha") {
		t.Fatalf("expected alpha to be valid")
	}

	sibling := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(sibling, []byte("noise"), 0o644); err != nil {
		t.Fatalf("write sibling: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if !store.IsValidToken("alpha") {
		t.Fatalf("expected alpha to remain valid after sibling write")
	}
}

func TestTokenStoreEmptyFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tokens.txt")
	writeTokenFile(t, file, "")

	store, err := NewTokenStore(file, 5*time.Millisecond, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if store.IsValidToken("anything") {
		t.Fatalf("expected no valid tokens from empty file")
	}
}

func TestTokenStoreConcurrentValidation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tokens.txt")
	writeTokenFile(t, file, "alpha\nbeta\n")

	store, err := NewTokenStore(file, 5*time.Millisecond, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				store.IsValidToken("alpha")
				store.IsValidToken("beta")
				store.IsValidToken("invalid")
			}
		}()
	}

	// Trigger refreshes while validating concurrently.
	writeTokenFile(t, file, "alpha\nbeta\ngamma\n")
	time.Sleep(50 * time.Millisecond)
	writeTokenFile(t, file, "alpha\n")

	wg.Wait()
}
