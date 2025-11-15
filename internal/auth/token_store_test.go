package auth

import (
	"io"
	"log"
	"os"
	"path/filepath"
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
