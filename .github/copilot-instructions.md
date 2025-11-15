# Home Podcast Coding Agent Guide

- **Architecture**: A single Go 1.25 service under `cmd/home-podcast` orchestrates packages in `internal/`: `config` (env/yaml resolution), `library` (fsnotify-backed scanner), `metadata` (tag extraction), `auth` (token watcher), and `server` (HTTP + RSS). Any change in one layer usually affects its tests under the same package.
- **HTTP Surface**: `internal/server/server.go` defines `/health`, `/episodes`, `/feed|/feed.xml|/rss`, and `/audio/<path>`. Feed enclosures must stay `https://` and always echo the caller’s token when validation is on—keep tests in `internal/server/server_test.go` updated.
- **Tokens**: Access control uses a _single token file_ (`PODCAST_TOKEN_FILE`); `auth.TokenStore` watches it, and `config.ResolveTokenFile` must not rewrite existing files (service often runs on read-only FS). Never reintroduce directory-based tokens.
- **Feed Metadata**: `config.ResolveFeedMetadata` merges defaults, optional YAML (`PODCAST_FEED_CONFIG`), then env overrides. Preserve that precedence and include new fields in `config/feed.example.yaml` plus tests.
- **File Watching**: Both library and token store rely on `fsnotify` with debounce timers and graceful shutdown (`Close`). If you add new watchers, mirror the existing `run/scheduleRefresh` patterns and guard timers with mutexes to avoid races.
- **Build & Format**: Use `go build ./...` (or `make build-local`) and run `gofmt` on touched Go files. The repo has no additional linters; keep imports sorted by `gofmt`.
- **Tests**: Run `go test ./...` or `make test`. Each package has targeted tests—update `internal/.../*_test.go` when endpoints, config, or file semantics change. RSS tests parse XML to assert tokens/https; keep them passing.
- **Configuration**: Documented env vars live in `README.md`. Favor `config` helpers (e.g., `ResolveAudioRoot`, `RefreshDebounce`) instead of reading env vars directly. When adding config, extend the table, env example, and tests.
- **Deployment**: `make first-deploy` handles initial provisioning (binary + service files + optional token file via `sudo install`), while subsequent pushes use `make deploy` to upload only the binary and restart the systemd service. Keep the `REMOTE_TMP_DIR` staging flow intact and preserve token ownership semantics.
- **Data Paths**: `library.Library` only indexes extensions from `config.AllowedExtensions()`. Add formats there plus tests before scanning new types. Keep relative paths slash-normalised via `filepath.ToSlash` semantics.
- **Concurrency & Shutdown**: Long-lived goroutines use `done` channels and `sync.WaitGroup`; if you add background work, follow the existing locking + `closeOnce` conventions to avoid leaked goroutines.

Please flag unclear sections so we can refine this guide. Thank you!.
