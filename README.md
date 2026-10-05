# devops

A zero-config task runner for monorepos that discovers modules via `.devops/commands.yaml` files and runs commands across them — sequentially or in a side-by-side parallel TUI.

## How it works

Drop a `.devops/commands.yaml` file in any subdirectory (up to 3 levels deep). Each file defines commands and the Docker Compose containers (or `host`) they run in. When you invoke `devops <command>`, it finds every module that defines that command and runs them, respecting priority ordering and parallelising same-priority groups in a split-screen terminal UI.

## Installation

### Download a release binary

Grab the latest binary for your platform from the [releases page](../../releases) and place it somewhere on your `$PATH`:

```bash
# macOS (Apple Silicon)
curl -L https://github.com/convidera/devops-cli/releases/latest/download/devops-darwin-arm64 -o /usr/local/bin/devops
chmod +x /usr/local/bin/devops

# macOS (Intel)
curl -L https://github.com/convidera/devops-cli/releases/latest/download/devops-darwin-amd64 -o /usr/local/bin/devops
chmod +x /usr/local/bin/devops

# Linux (amd64)
curl -L https://github.com/convidera/devops-cli/releases/latest/download/devops-linux-amd64 -o /usr/local/bin/devops
chmod +x /usr/local/bin/devops
```

### Build from source

Requires Go 1.25+.

```bash
go install github.com/convidera/devops-cli@latest
```

Or clone and build:

```bash
git clone https://github.com/convidera/devops-cli
cd devops-cli
go build -o devops .
```

## Configuration

Each module needs a `.devops/commands.yaml` file. The structure is:

```yaml
<command>:
  <container>:
    - <script>
    - <script>
```

`<container>` is either a Docker Compose service name or `host` (runs directly on the machine without Docker).

All scripts listed under the same `<container>` run in a single shell process, in order, so `cd`, `export`, and other shell state carry over from one line to the next (a failing line aborts the rest, like `set -e`). Different containers — and different modules — still run as separate processes.

### Example

```
my-monorepo/
├── backend/
│   └── .devops/
│       └── commands.yaml
└── frontend/
    └── .devops/
        └── commands.yaml
```

**backend/.devops/commands.yaml**
```yaml
migrate:
  app:
    - php artisan migrate

seed:
  app:
    - php artisan db:seed

test:
  app:
    - php artisan test
```

**frontend/.devops/commands.yaml**
```yaml
test:
  node:
    - npm test
```

Run all tests across all modules:

```bash
devops test
```

## Priority

By default every entry has priority `100`. Lower numbers run first. Modules with the same effective priority for a command run in parallel. Use the map form to set a custom priority:

```yaml
migrate:
  app:
    - script: php artisan migrate
      priority: 10   # runs before priority-100 modules
```

## Usage

```
devops <command>                   Run command across all modules
devops <module> <command>          Run command for a specific module
devops all <command>               Run command across all modules (explicit)
devops <module> exec [cmd...]      Open interactive shell in module's container
devops <module> shell              Alias for exec
devops agents <command>            Run an agent command (see Agent commands)
devops help                        Show this help
devops version                     Print the version
devops reinstall                   Download and install the latest release
```

### Examples

```bash
# Run migrations across all modules (respects priority order)
devops migrate

# Run tests only for the backend module
devops backend test

# Open a shell in the backend module's container
devops backend exec

# Run an arbitrary command in a container
devops backend exec php artisan tinker

# Fall through to docker compose if no module defines the command
devops ps
```

## Agent commands

To make a project agent-ready end to end (compose, `agents.yaml`, `AGENTS.md`), follow [docs/agent-ready.md](docs/agent-ready.md).

AI agents get a separate command surface in `.devops/agents.yaml` (same schema as `commands.yaml`, priority included). Keep it to commands that work in a sandbox — no `mkcert -install`, `/etc/hosts` edits, secret decryption or TTY prompts. A module may have only an `agents.yaml`.

**backend/.devops/agents.yaml**
```yaml
test:
  description: PHPUnit in app
  app:
    - php artisan test
```

```
devops agents                       List agent commands per module
devops agents <command> [args]      Run an agent command across all modules
devops agents <module> <command>    Run an agent command for one module
```

Unknown agent commands are an error; they never fall through to docker compose.

Agent mode is on when `DEVOPS_AGENT=1`, or when `CLAUDECODE=1` (set by Claude Code) and `DEVOPS_AGENT` is unset. `DEVOPS_AGENT=0` turns it off. In agent mode, everything except `devops agents ...`, `devops help` and `devops version` exits with code 2, and parallel output is always plain (no TUI). Outside agent mode, `devops agents ...` still works so you can try it.

## Parallel TUI

When multiple modules share the same priority for a command they run in parallel inside a split-screen terminal UI:

- **Tab / ← →** or **h / l** — switch focus between panels
- **↑ ↓** or **j / k** — scroll one line
- **PgUp / PgDn** or **Ctrl+U / Ctrl+D** — scroll by page
- **g / G** — jump to top / bottom (G re-enables auto-scroll)
- **q / Q / Ctrl+C** — quit

After all panels finish, a summary shows which modules succeeded or failed.

### Non-interactive runs

The TUI needs a terminal. Without one — in CI, when output is piped or redirected, or when there is no controlling terminal — modules still run in parallel, but their output is streamed as plain lines prefixed with the module name, followed by the same summary:

```
[api]      starting
[frontend] starting
[api]      PHPStan: no errors
[frontend] done
[api]      done

=== Parallel Summary ===
  ✓  api
  ✓  frontend
```

Set `DEVOPS_NO_TUI=1` to force this mode even in an interactive terminal.

## Rootless Docker

Rootless Docker (and classic userns-remap) map the invoking host user to container uid 0,
so anything the daemon creates on a bind mount — and the container's own default user —
ends up owned by uid 0 from inside any container, not by whatever non-root service account
an image normally runs as (e.g. `www-data`). devops detects this once per run and sets
`DEVOPS_DOCKER_ROOTLESS=1` for every `host:` script, so a module can react without each
reimplementing the `docker info` check itself — for example, renumbering a build-time
service account to uid 0 so the mechanisms that already drop privileges to it by name
(a `php-fpm` pool's `user =`, `chpst -u`, `gosu`, ...) pick it up automatically:

```yaml
bootstrap:
  host:
    - |
      if [ "$DEVOPS_DOCKER_ROOTLESS" = 1 ]; then
        export USER_UID=0 USER_GID=0
      fi
      docker compose build app
```

Set `DEVOPS_DOCKER_ROOTLESS=0` to force it off (or `=1` to force it on, e.g. if `docker
info` is slow or unavailable but the answer is already known).

## YAML reference

```yaml
# Simple form — uses default priority (100)
build:
  app:
    - npm run build

# Map form — explicit priority
setup:
  db:
    - script: php artisan migrate
      priority: 10
  app:
    - script: php artisan db:seed
      priority: 20
  host:
    - script: echo "done"
      priority: 30
```

A command can target multiple containers; they run in the order they appear in the file.

An optional `description` string next to the containers is shown by `devops help` / `devops agents help` and is not treated as a container:

```yaml
test:
  description: PHPUnit in app-test
  app-test:
    - php artisan test
```

devops's own status lines (`Running ...`, `=== [module] ===`, `Executing: ...`) go to stderr, so stdout carries only the command output (e.g. `devops agents tinker --execute=... > out.txt`). If stdout is closed early (`devops agents routes | head`), devops exits quietly with the command's exit code.
