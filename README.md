# devops

A zero-config task runner for monorepos that discovers modules via `.devops/commands.yaml` files and runs commands across them — sequentially or in a side-by-side parallel TUI.

## How it works

Drop a `.devops/commands.yaml` file in any subdirectory (up to 3 levels deep). Each file defines commands and the Docker Compose containers (or `host`) they run in. When you invoke `devops <command>`, it finds every module that defines that command and runs them, respecting priority ordering and parallelising same-priority groups in a split-screen terminal UI.

## Installation

### Download a release binary

Grab the latest binary for your platform from the [releases page](../../releases) and place it somewhere on your `$PATH`:

```bash
# macOS (Apple Silicon)
curl -L https://github.com/convidera/devops-parallel-runner/releases/latest/download/devops-darwin-arm64 -o /usr/local/bin/devops
chmod +x /usr/local/bin/devops

# macOS (Intel)
curl -L https://github.com/convidera/devops-parallel-runner/releases/latest/download/devops-darwin-amd64 -o /usr/local/bin/devops
chmod +x /usr/local/bin/devops

# Linux (amd64)
curl -L https://github.com/convidera/devops-parallel-runner/releases/latest/download/devops-linux-amd64 -o /usr/local/bin/devops
chmod +x /usr/local/bin/devops
```

### Build from source

Requires Go 1.22+.

```bash
go install github.com/convidera/devops-parallel-runner@latest
```

Or clone and build:

```bash
git clone https://github.com/convidera/devops-parallel-runner
cd devops-parallel-runner
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
devops help                        Show this help
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

## Parallel TUI

When multiple modules share the same priority for a command they run in parallel inside a split-screen terminal UI:

- **Tab / ← →** or **h / l** — switch focus between panels
- **↑ ↓** or **j / k** — scroll one line
- **PgUp / PgDn** or **Ctrl+U / Ctrl+D** — scroll by page
- **g / G** — jump to top / bottom (G re-enables auto-scroll)
- **q / Q / Ctrl+C** — quit

After all panels finish, a summary shows which modules succeeded or failed.

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
