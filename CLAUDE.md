# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Home Podcast is a Go 1.26 service that scans a directory of audio files and serves podcast metadata over HTTP, including an RSS 2.0 feed with iTunes extensions. It auto-refreshes when files change via fsnotify.

## Common Commands

```bash
make build-local    # Build for current platform → bin/home-podcast
make build          # Cross-compile for Linux/amd64
make test           # Run all tests with coverage
make coverage       # Show coverage summary (runs tests first)
go test ./internal/server/  # Run tests for a single package
```

No linters are configured; use `gofmt` for formatting.

## Architecture

Single binary under `cmd/home-podcast/main.go` with internal packages:

- **server** — HTTP handler serving `/health`, `/episodes`, `/feed` (`/feed.xml`, `/rss`), `/audio/<path>`, and `/ui`. Feed enclosures always use `https://` and embed the caller's token. Path traversal is rejected.
- **library** — Recursively watches the audio directory with fsnotify. Maintains a sorted in-memory episode list behind `sync.RWMutex`. Debounced refresh prevents excessive rescans.
- **auth** — `TokenStore` watches a single token file (newline-delimited). Tokens are validated via `X-Podcast-Token` header, `Authorization: Bearer`, `?token=` query param, or `podcast_token` cookie (in that priority).
- **config** — Resolves settings via: defaults → optional YAML file (`PODCAST_FEED_CONFIG`) → environment variable overrides. Use config helpers (`ResolveAudioRoot`, `RefreshDebounce`, etc.) instead of reading env vars directly.
- **metadata** — Extracts tags via `dhowden/tag`, estimates MP3 duration via `tcolgate/mp3`. Falls back to filename as title.
- **models** — `Episode` struct used across packages.

Key interfaces: `server.New()` accepts an `EpisodeProvider` interface (not concrete library) and a `TokenValidator` interface (nil disables auth).

## Concurrency Patterns

All long-lived goroutines use `done` channels + `sync.WaitGroup`. Cleanup uses `sync.Once` (`closeOnce`). Debounce timers are mutex-guarded and stopped before watcher closure. Follow these patterns when adding background work.

## Key Constraints

- **Localhost-only binding** enforced by `config.ValidateListenAddr()` — reverse proxy required for external access.
- **Single token file** — never reintroduce directory-based tokens. `config.ResolveTokenFile` must not rewrite existing files (service may run on read-only FS).
- **Allowed extensions** (`.mp3`, `.m4a`, `.aac`, `.wav`, `.flac`, `.ogg`) are defined in `config.AllowedExtensions()` — add new formats there plus tests before scanning new types.
- **Relative paths** must stay slash-normalised via `filepath.ToSlash`.

## Testing

Each internal package has `_test.go` files. Server tests parse XML to verify tokens and https in RSS output. When changing endpoints, config, or file semantics, update the corresponding test file. Config tests use environment variable isolation.

## Adding Configuration

When adding a new config variable: add a helper in `internal/config`, update the env var table in `README.md`, update the Ansible env template in `ansible/roles/home-podcast/templates/home-podcast.env.j2`, add a default in `ansible/roles/home-podcast/defaults/main.yml`, and add tests.

## Deployment

Deployment uses Ansible. See `ansible/README.md` for full details.

```bash
ansible-playbook ansible/playbook.yml -i <host>,    # Build + deploy (trailing comma required)
ansible-playbook ansible/playbook.yml -i <host>, --check --diff  # Dry run
```

The playbook cross-compiles the binary locally, then provisions the remote host (system user, directories, binary, systemd unit, env file, token file) and starts the service. Variables are in `ansible/roles/home-podcast/defaults/main.yml`.
