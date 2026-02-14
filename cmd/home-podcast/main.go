package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"home-podcast/internal/auth"
	"home-podcast/internal/config"
	"home-podcast/internal/library"
	"home-podcast/internal/server"
)

func main() {
	logger := log.New(os.Stdout, "home-podcast ", log.LstdFlags|log.Lmsgprefix)

	audioRoot, err := config.ResolveAudioRoot()
	if err != nil {
		logger.Fatalf("resolve audio root: %v", err)
	}

	listenAddr := config.ListenAddr()
	if err := config.ValidateListenAddr(listenAddr); err != nil {
		logger.Fatalf("invalid listen address %q: %v", listenAddr, err)
	}

	debounce := config.RefreshDebounce()

	allowedExtensions := config.AllowedExtensions()
	lib, err := library.NewLibrary(audioRoot, allowedExtensions, debounce, logger)
	if err != nil {
		logger.Fatalf("initialise library: %v", err)
	}
	defer func() {
		if err := lib.Close(); err != nil {
			logger.Printf("error closing library: %v", err)
		}
	}()

	tokenFile, tokensEnabled, err := config.ResolveTokenFile()
	if err != nil {
		logger.Fatalf("resolve token file: %v", err)
	}

	var tokenStore *auth.TokenStore
	if tokensEnabled {
		tokenStore, err = auth.NewTokenStore(tokenFile, debounce, logger)
		if err != nil {
			logger.Fatalf("initialise token store: %v", err)
		}
		defer func() {
			if err := tokenStore.Close(); err != nil {
				logger.Printf("error closing token store: %v", err)
			}
		}()
	}

	feedConfig, err := config.ResolveFeedMetadata()
	if err != nil {
		logger.Fatalf("resolve feed metadata: %v", err)
	}

	feedMeta := server.FeedMetadata{
		Title:       feedConfig.Title,
		Description: feedConfig.Description,
		Language:    feedConfig.Language,
		Author:      feedConfig.Author,
	}

	handler := server.New(lib, tokenStore, audioRoot, allowedExtensions, feedMeta, logger)
	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("graceful shutdown error: %v", err)
		}
	}()

	logger.Printf("listening on %s (audio directory: %s)", listenAddr, audioRoot)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("http server error: %v", err)
	}
	logger.Println("shutdown complete")
}
