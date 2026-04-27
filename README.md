# deploy

A simple SSH deployment pipeline tool. Run a single command to stop, backup, clean, upload, extract, and start — each step is independently optional.

## Install

```bash
go build -o deploy .
```

## Pipeline order

```
connect → pre → backup → delete → upload → extract → post
```

| Step | Flag | Description |
|------|------|-------------|
| connect | `--host` `--user` | Establish SSH connection (required) |
| pre | `--pre` | Pre-flight command, e.g. stop service |
| backup | `--backup` | Back up remote files (repeatable) |
| delete | `--delete` | Delete remote paths (repeatable) |
| upload | `--upload` | Upload local files with progress bar (repeatable) |
| extract | `--extract` | Extract remote archives (repeatable) |
| post | `--post` | Post-flight command, e.g. start service |

Omitting a flag (or leaving it empty in the config file) skips that step.

## Config file

Place `deploy.yaml` or `deploy.yml` in the working directory — it is loaded automatically.  
**CLI flags always override config file values.**

```yaml
# ── SSH connection ─────────────────────────────────────────
host: 192.168.1.10
port: 22
user: root
password:             # plain-text password (prefer key-based auth)
# key: ~/.ssh/id_rsa  # path to private key

# ── Pipeline steps (leave empty to skip) ──────────────────
pre: "/opt/app/stop.sh"

backup:
  - ["/opt/app", "/opt/app.bak"]

delete:
  - /opt/app/app.tar.gz

upload:
  - ["./app.tar.gz", "/opt/app/app.tar.gz"]

extract:
  - ["/opt/app/app.tar.gz", "/opt/app"]

post: "/opt/app/start.sh"
```

Use `--config` to load a different file:

```bash
./deploy --config prod.yaml
```

## Authentication

| Method | Usage |
|--------|-------|
| Private key | `--key ~/.ssh/id_rsa` or set `key:` in config |
| Password (config) | `--password secret` or set `password:` in config |
| Password (interactive) | Leave both `password` and `key` unset — you will be prompted at runtime with masked input |

## Usage

### Full pipeline from config file

```bash
./deploy
```

### Override individual flags

```bash
./deploy --host 10.0.0.2 --pre "/opt/app/stop.sh"
```

### Upload a single file

```bash
./deploy -H 192.168.1.10 -u root --key ~/.ssh/id_rsa \
  --upload ./app.tar.gz:/opt/app/app.tar.gz
```

### Upload multiple files and extract multiple archives

```bash
./deploy -H 192.168.1.10 -u root --password secret \
  --upload ./app.tar.gz:/opt/app/app.tar.gz \
  --upload ./config.json:/opt/app/config.json \
  --extract /opt/app/app.tar.gz:/opt/app
```

### Full deploy with backup and service restart

```bash
./deploy -H 192.168.1.10 -u root --key ~/.ssh/id_rsa \
  --pre    "/opt/app/stop.sh" \
  --backup /opt/app:/opt/app.bak \
  --delete /opt/app \
  --upload ./app.tar.gz:/opt/app.tar.gz \
  --extract /opt/app.tar.gz:/opt/app \
  --post   "/opt/app/start.sh"
```

## Flags

```
  -c, --config string        Config file path (default: deploy.yaml / deploy.yml in cwd)
  -H, --host string          Remote host (required)
  -p, --port int             SSH port (default 22)
  -u, --user string          SSH user (required)
      --password string      SSH password
  -k, --key string           Path to private key file

      --pre string           Command to run before all steps
      --backup stringArray   Back up remote files, format: src:dst (repeatable)
      --delete stringArray   Remote paths to delete (repeatable)
      --upload stringArray   Upload file, format: local-path:remote-path (repeatable)
      --extract stringArray  Extract remote archive, format: archive:dest-dir (repeatable)
      --post string          Command to run after all steps
```

Supported archive formats: `.tar.gz` `.tgz` `.tar.bz2` `.tbz2` `.tar` `.zip`

## Example output

```
→ connecting  root@192.168.1.10:22
  ok
→ pre         /opt/app/stop.sh
→ backup      /opt/app → /opt/app.bak
→ delete      /opt/app/app.tar.gz
→ upload      ./app.tar.gz → /opt/app/app.tar.gz
  uploading  [===============================>        ] 18 MB / 24 MB
→ extract     /opt/app/app.tar.gz → /opt/app
→ post        /opt/app/start.sh
→ done
```

