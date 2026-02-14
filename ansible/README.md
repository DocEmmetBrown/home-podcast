# Ansible Deployment

Ansible playbook and role for provisioning and deploying the home-podcast service on a remote Linux host.

## Prerequisites

- Ansible installed locally
- SSH access to the target host (with sudo)
- Go 1.26+ installed locally (the playbook cross-compiles the binary)

## Usage

Deploy to a host:

```bash
ansible-playbook ansible/playbook.yml -i <host>,
```

The trailing comma after the hostname is required — it tells Ansible to treat it as an inline inventory rather than a file path.

Dry run first:

```bash
ansible-playbook ansible/playbook.yml -i <host>, --check --diff
```

Specify a remote user:

```bash
ansible-playbook ansible/playbook.yml -i <host>, -u ubuntu
```

## What the playbook does

1. **Builds** the `linux/amd64` binary locally via `make build`
2. **Creates** a `home-podcast` system user and group
3. **Sets up directories**: `/opt/home-podcast` (binary), `/srv/home-podcast` (data), and the audio directory
4. **Uploads** the binary to `/opt/home-podcast/home-podcast`
5. **Templates** the systemd unit and environment file
6. **Creates** the token file (only if it doesn't already exist)
7. **Enables and starts** the `home-podcast.service`

Changes to the binary, systemd unit, or environment file automatically trigger a service restart.

## Variables

Override any of these with `-e` flags or in a vars file:

| Variable | Default | Description |
|---|---|---|
| `podcast_user` | `home-podcast` | System user running the service |
| `podcast_group` | `home-podcast` | System group |
| `podcast_install_dir` | `/opt/home-podcast` | Binary install path |
| `podcast_data_dir` | `/srv/home-podcast` | Data root directory |
| `podcast_audio_dir` | `/srv/home-podcast/audio` | Audio files directory |
| `podcast_listen_addr` | `127.0.0.1:8080` | HTTP listen address |
| `podcast_refresh_debounce_ms` | `500` | fsnotify debounce (ms) |
| `podcast_token_file` | `/srv/home-podcast/tokens.txt` | Token file path |
| `podcast_env_path` | `/etc/home-podcast.env` | Environment file path |
| `podcast_feed_config` | _(empty)_ | Path to feed YAML config on remote |
| `podcast_feed_title` | _(empty)_ | RSS feed title override |
| `podcast_feed_description` | _(empty)_ | RSS feed description override |
| `podcast_feed_language` | _(empty)_ | RSS feed language override |
| `podcast_feed_author` | _(empty)_ | RSS feed author override |

Example with overrides:

```bash
ansible-playbook ansible/playbook.yml -i podcast.example.com, \
  -e podcast_audio_dir=/mnt/audio \
  -e podcast_feed_title="Family Podcast"
```

## File structure

```
ansible/
├── playbook.yml
└── roles/
    └── home-podcast/
        ├── defaults/main.yml        # Default variables
        ├── handlers/main.yml        # Restart handler
        ├── tasks/main.yml           # Provisioning tasks
        └── templates/
            ├── home-podcast.service.j2
            └── home-podcast.env.j2
```
