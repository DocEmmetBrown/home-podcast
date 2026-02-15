# Home Podcast Metadata Server

This service is a Go binary that scans a directory of audio files and serves podcast-style metadata over HTTP. The catalog refreshes automatically whenever files are added, removed, or modified, so downstream clients (and the reverse proxy in front of the service) always see the current list without needing restarts.

## Features

- **Go 1.26+ compiled binary** suitable for Linux/amd64 deployment.
- **Directory watching** backed by `fsnotify`, with debounce handling to fold bursts of file events into single rescan operations.
- **Tag extraction** via `github.com/dhowden/tag` (title/artist/album) and MP3 duration estimation using `github.com/tcolgate/mp3`.
- **Local-only listener** (defaults to `127.0.0.1:8080`) for use behind a reverse proxy.
- **Ansible-based deployment** with roles, templates, and handlers under `ansible/`.
- **Podcast-compatible RSS feed** with iTunes extensions plus signed enclosure URLs for private distribution.

## Configuration

Environment variables control runtime behaviour:

| Variable                      | Default          | Description                                                                                                            |
| ----------------------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `PODCAST_AUDIO_DIR`           | `<repo>/audio`   | Absolute or relative path to the directory containing audio files. Automatically created if missing.                   |
| `PODCAST_LISTEN_ADDR`         | `127.0.0.1:8080` | Address for the HTTP listener. Validation enforces binding to localhost.                                               |
| `PODCAST_REFRESH_DEBOUNCE_MS` | `500`            | Debounce duration (in milliseconds) applied to file-system events before triggering a rescan.                          |
| `PODCAST_TOKEN_FILE`          | _(unset)_        | Optional file containing newline-delimited feed tokens. Each non-empty trimmed line is treated as an authorized token. |
| `PODCAST_FEED_CONFIG`         | _(unset)_        | Optional path to a YAML file providing feed metadata (`title`, `description`, `language`, `author`).                   |
| `PODCAST_FEED_TITLE`          | `Home Podcast`   | Title emitted in the RSS feed.                                                                                         |
| `PODCAST_FEED_DESCRIPTION`    | _see above_      | Description text for the RSS feed.                                                                                     |
| `PODCAST_FEED_LANGUAGE`       | `en`             | RFC 5646 language tag used in the RSS feed.                                                                            |
| `PODCAST_FEED_AUTHOR`         | _(unset)_        | Optional author credited via iTunes metadata (falls back to episode artist when available).                            |


When token-based access control is enabled, populate the file pointed to by `PODCAST_TOKEN_FILE` with newline-delimited tokens. Ensure the file is owned by the service account (default `home-podcast`) and not world-readable, for example:

```bash
sudo install -d -o home-podcast -g home-podcast -m 750 /srv/home-podcast
sudo install -o home-podcast -g home-podcast -m 600 /dev/null /srv/home-podcast/tokens.txt
sudo chown home-podcast:home-podcast /srv/home-podcast/tokens.txt
```

Clients must supply a valid token as a `token` query parameter, `Authorization: Bearer <token>` header, or `X-Podcast-Token` header to access `/episodes`.

To manage feed metadata in one place, set `PODCAST_FEED_CONFIG` to a YAML file containing `title`, `description`, `language`, and `author` fields (see `config/feed.example.yaml` for a ready-to-copy template). Environment variables continue to override individual fields when both are supplied.

Supported audio extensions are: `.mp3`, `.m4a`, `.aac`, `.wav`, `.flac`, `.ogg`.

1. Install Go 1.26 or newer.
2. Fetch dependencies and build the binary (or run `make build-local`):

   ```bash
   go build -o bin/home-podcast ./cmd/home-podcast
   ```

3. Optionally adjust environment variables, then start the server:

   ```bash
   export PODCAST_AUDIO_DIR="$PWD/audio"
   export PODCAST_LISTEN_ADDR=127.0.0.1:8080
   ./bin/home-podcast
   ```

4. Query the API:

   ```bash
   curl http://127.0.0.1:8080/episodes | jq
   ```

Endpoints:

- `GET /health` — returns `{ "status": "ok" }`.
- `GET /episodes` — returns a JSON array of episode metadata. Requires a valid token when `PODCAST_TOKEN_FILE` is configured (via query parameter `token`, `Authorization: Bearer <token>`, or `X-Podcast-Token` header).
- `GET /feed` (also `/feed.xml` or `/rss`) — returns an RSS 2.0 podcast feed including iTunes extensions. When tokens are enabled the request must include a valid token; the resulting enclosure URLs embed the same token for convenience and are always emitted with `https://` links suitable for public consumption.
- `GET /audio/<relative-path>` — streams the underlying audio file with sensible MIME types. The handler enforces token checks when configured and rejects path traversal attempts.

## Makefile Targets

The provided `Makefile` streamlines common workflows:

- `make build` — cross-compiles a Linux/amd64 binary into `bin/home-podcast`.
- `make build-local` — builds a binary for the current platform.
- `make test` — runs the full test suite with coverage collection.
- `make coverage` — shows the coverage summary after `make test`.

## Deployment

Deployment is managed via Ansible. The `ansible/` directory contains a playbook and role that handle building, provisioning, and service management. See [ansible/README.md](ansible/README.md) for full usage instructions.

Quick deploy example:

```bash
cd ansible
ansible-playbook playbook.yml -i podcast.example.com,
```

The Ansible role handles: system user/group creation, directory setup, binary upload, systemd unit and env file templating, token file creation, and service enable/start. The unit binds to localhost, making it safe to expose through an nginx, Caddy, or Apache reverse proxy on the same host.

## Development Notes

- Extend or customise metadata extraction in `internal/metadata` if additional fields are required.
- Add integration tests under a future `internal/test` or `pkg/` directory as the project evolves.
