# Make a project agent-ready

You are preparing a Convidera project (usually Docker Compose; see "Host-only projects" for ML/Python and other projects without compose) so Claude Code agents on our Kubernetes runners can start it, test it and click through it without help or workarounds. Human developers must see no change. Open one PR with the changes. Trax (convidera/t-rax2: `.devops/agents.yaml`, `.devops/agents/bootstrap.sh`, `AGENTS.md`) is the reference implementation; read it before you start -- including its `.agent-secrets/` setup (step 9) for `auth.json`, if your project also needs a real secret to bootstrap.

The `devops` CLI that runs all of this lives in [convidera/devops-cli](https://github.com/convidera/devops-cli) (this repo). It is a separate binary installed on the runner image and on developer machines, never vendored into your project. See "The `devops` CLI" below.

## What the runner gives you

Design for exactly this environment; don't try to change it from the project.

| | |
|---|---|
| User | UID 65532, no root, no sudo, no TTY, no `/etc/hosts` edits, no apt-get |
| Docker | dind sidecar via `DOCKER_HOST`; `/workspace` is shared, so bind mounts work. userns-remap: **container root = the agent's UID**, other container users map to foreign UIDs |
| Registry | Docker Hub pulls go through an in-cluster pull-through mirror (logged in read-only) |
| Network | Egress on 80, 443 and 22; published ports answer on `127.0.0.1` in the session |
| Tools | `devops` CLI (agent mode), docker compose + buildx, mkcert (no CA install), yq, jq, gh, node 22, git-secret (no key for the project's regular `.gitsecret` store; see "`.agent-secrets/`" below for the opt-in exception) |
| Browser | Playwright MCP, headless Chromium: `*.test` → 127.0.0.1, HTTPS errors ignored. The shell can't resolve `*.test`; use `curl -k --resolve host:443:127.0.0.1` |
| Preview | In a managed-coding-service (MCS) session the user can open the running app at `https://<session>.preview.devcon.team`. The session sets `MCS_PREVIEW_HOST` (and `MCS_PREVIEW_URL`) in the agent's environment; the app only has to answer to that host too, see "Session preview" below. That host does not resolve in the agent's browser, so the agent keeps using `https://<project>.test` |
| devops CLI | Built from [convidera/devops-cli](https://github.com/convidera/devops-cli), **v0.2.0+** (agent mode since v0.0.10, the built-ins below since v0.1.0, per-module `test`/`lint` in `doctor` and `init --trace` since v0.2.0), on `PATH` on runners. With `CLAUDECODE=1`/`DEVOPS_AGENT=1` it only runs `devops agents <cmd>` from `.devops/agents.yaml`; everything else exits 2. Its own status lines go to stderr |
| Session start | For the project, its direct subdirectories and sibling repos checked out next to it (directories with a `.git`), the hook runs `devops agents init` in the background wherever `.devops/agents.yaml` exists; repos without one are ignored, and module directories of a monorepo are not scanned (only the repo root bootstraps). State lives under `/tmp/devops-agents/<dir>-<hash>/` (`log`, `pid`, `done` = exit code). Use `devops agents status` / `wait` to follow it |
| Secrets | None by default -- agents only ever use `.env.example` placeholders. A project can opt specific non-prod files into `.agent-secrets/` if it genuinely can't bootstrap without them; see step 9 |

## The `devops` CLI

`devops` is the Go binary from [convidera/devops-cli](https://github.com/convidera/devops-cli). It discovers `.devops/commands.yaml` (humans) and `.devops/agents.yaml` (agents) in the project and its subdirectories and runs the commands. Projects don't carry their own copy: the runner image installs it, and the project's `./devops` launcher (step 6) downloads it for humans, and `devops reinstall` upgrades it.

- Agents only use `devops agents <command>`. In agent mode everything else exits 2.
- A project only contributes YAML and scripts (`.devops/agents.yaml`, `.devops/agents/*.sh`). Don't copy or reimplement the CLI in the project.
- If `devops` is missing on an agent's `PATH`, the runner image is broken: the agent stops and reports it, and doesn't work around it with `./devops` or raw `docker compose`.
- Need a CLI change (new agent-mode behavior, a bug)? Open a PR in convidera/devops-cli, not a workaround in the project.

### Built-in agent commands

Besides the commands from `agents.yaml`, `devops agents` has four built-ins (`agents.yaml` must not define a command with one of these names: `devops agents` aborts with a warning until it is renamed, and `doctor` fails):

| Command | What it does |
|---|---|
| `init [--retry] [dir]` | Runs the repo's `bootstrap` once and records `log`, `pid` and `done` (the exit code) under `/tmp/devops-agents/<dir>-<hash>/`. A successful or still-running bootstrap is left alone without `--retry`; a failed one is rerun by the next `init`. `--trace` logs every shell command (`set -x`, inherited by bash scripts) to find where a script aborted silently. |
| `status [dir]` | Prints `ready`, `failed`, `running` or `not started`; exit 0 ready, 1 failed, 2 running, 3 not started. |
| `wait [--timeout 10m] [dir]` | Blocks until bootstrap finishes; exit 0 ready, 1 failed (prints the log tail), 3 never started, 124 timeout. |
| `doctor [--json] [dir]` | Static check of `agents.yaml` (needs `bootstrap`, `test`, `lint`; in a monorepo `test`/`lint` may live in module-level `agents.yaml` files instead of the root one), referenced scripts, `CLAUDE.md`/`AGENTS.md`, `.claude/settings.json` (no `env` block, no blanket allow rules) and the `.agent-secrets` opt-in. Exit 1 on any failure. |

To keep a project agent-ready in CI, call the reusable workflow:

```yaml
jobs:
  agent-ready:
    uses: convidera/devops-cli/.github/workflows/agent-ready.yml@v0.2.0
```

It takes optional `devops-version` and `working-directory` inputs. If the caller repo is private and devops-cli is not public to it, the workflow may need its repository access setting enabled under the devops-cli repo's Actions settings.

## Done means

In a fresh session, with nothing done by hand:
1. The hook's bootstrap finishes with exit code 0.
2. The app loads in the Playwright browser, and the seeded dev login works.
3. `devops agents test` and `lint` pass (the same checks as CI).
4. `devops agents down -v` then `bootstrap` gives a working fresh database.
5. Files containers write into the checkout are owned by the agent.
6. `git status` is clean afterwards.

## Host-only projects (no Docker Compose)

Some projects (ML training pipelines, libraries, CLIs) run on the host and have no compose file. They are agent-ready too; the contract is the same, minus the Docker parts:

- `agents.yaml` still needs `bootstrap`, `test` and `lint`. Entries are plain `host` commands, for example `uv run --directory training pytest tests "$@"`. `down`, `logs` and `recreate` only make sense when there is a stack, so skip them.
- `bootstrap.sh` just installs dependencies and prepares local config: for example `uv sync`, then `[ -f .env ] || cp .env.example .env`. It must stay idempotent, headless and secret-free, and it must exit. The runner image has `uv`, python3, node 22, yq and jq; don't download toolchains in bootstrap.
- Skip steps 2 (full-stack `.env.example`), 3 (compose conventions) and the compose parts of steps 4 and 5: no mkcert, ports, healthchecks or seeding. `devops agents doctor` does not require a compose file when `agents.yaml` and the bootstrap script don't use docker.
- Keep commands fast and CPU-only. Anything that needs a GPU or a large model download (full training, export, serving) stays human-only; give agents a smoke variant or a stubbed test instead, and say in `AGENTS.md` which commands are which.
- Steps 6 to 9 (launcher, `AGENTS.md`, housekeeping, optional `.agent-secrets/`) apply unchanged.

## Steps

### 1. Survey
Read `.devops/commands.yaml`, `docker-compose.yml`, `.env.example`, the Dockerfiles and entrypoints, the seeders and **the CI workflows**. CI is the closest thing to a headless setup: every `sed`, `cp` or extra env var it needs is a gap agents will hit too. Check whether the repo still vendors a legacy bash `./devops` with embedded commands; if so, migrate it to `commands.yaml` first (step 6).

### 2. `.env.example` boots the whole stack
- By default, no secrets and no manual steps; if the project genuinely cannot bootstrap without a real credential, use the `.agent-secrets/` opt-in in step 9. External services are faked or logged: `MAIL_*=log`, empty Sentry DSN, MinIO or local storage.
- Seeders must not need real mail, APIs or keys.
- If CI seds values into `.env`, fix the root cause and drop the sed.

### 3. Compose conventions
Change these only in ways that keep today's defaults for humans:
- **Dev containers run as root** (`user: "0:0"`). Under userns-remap that is the agent's UID. `www-data` or other users can't write to `storage/`, `vendor/` or caches. This also applies to `docker compose run --user …` in commands.
- **Ports come from env vars** with the current values as defaults: `"${HTTP_PORT:-80}:80"`, `"${DB_FORWARD_PORT:-3306}:3306"`, and so on.
- **Healthchecks** on the app and the DB, `depends_on: condition: service_healthy`, so `docker compose up --wait` works. Give the app a long `start_period` if it installs dependencies on first boot.
- **DB init scripts are idempotent**: `CREATE DATABASE/USER IF NOT EXISTS`. MySQL `GRANT` doesn't create users.
- **Bind mounts stay inside the repo.** No host paths outside the checkout.
- `env_file` is read when a container is created. Generate keys (`APP_KEY` and similar) **before** the first `up`, and recreate containers after changing `.env`.
- Test-only services shouldn't start on a plain `up` if they race the app's first install. Name the services in `bootstrap`, or use profiles.

### 4. `.devops/agents.yaml`
Same schema as `commands.yaml`. **Only list commands you have seen work headless.** A listed command that fails is worse than a missing one.

Required: `bootstrap`, `down`, `test`, `lint` (whatever CI runs), `logs`, `recreate`. In a monorepo the root `agents.yaml` must define `bootstrap`; `test` and `lint` may instead live in `<module>/.devops/agents.yaml` (up to 3 levels deep), which `doctor` accepts. Don't name a command `init`, `status`, `wait` or `doctor`: they are built-ins and `devops agents` aborts on a clash. Add framework pass-throughs as needed (`artisan`, `composer`, `yarn`/`npm`, `console`, …).

```yaml
bootstrap:
  description: Start the stack, seed an empty DB (~2 min cold)
  host:
    - .devops/agents/bootstrap.sh

down:
  description: Stop the stack; -v also drops the database
  host:
    - docker compose down "$@"

test:
  description: PHPUnit in app-test (~90 s); args go to phpunit
  host:
    - docker compose run --rm -T app-test sh -c 'php artisan migrate && exec ./vendor/bin/phpunit --fail-on-empty-test-suite "$@"' sh "$@"

lint:
  description: PHPStan, as in CI
  host:
    - docker compose exec -T app ./vendor/bin/phpstan analyse --no-progress --memory-limit=2G "$@"

artisan:
  description: php artisan in the app container
  host:
    - docker compose exec -T app php artisan "$@"

logs:
  description: Recent container logs [service]
  host:
    - docker compose logs --no-color --tail=200 "$@"

recreate:
  description: 'Recreate containers after .env changes (default: app)'
  host:
    - if [ $# -eq 0 ]; then set -- app; fi; docker compose up -d --force-recreate --wait "$@"
```

Rules for entries:
- Give every command a `description:` with its arguments and rough duration; `devops agents help` shows it.
- Use `host` entries with `docker compose exec -T` or `run --rm -T`, never interactive forms.
- Pass arguments as `sh -c '… "$@"' sh "$@"`, never `sh -c "… $@"`, which splits multiple arguments.
- No `mkcert -install`, `/etc/hosts`, `git secret reveal`, sudo or prompts. This holds even if the project uses `.agent-secrets/` (step 9): reveal happens automatically in a SessionStart hook, never as an `agents.yaml` command.
- Fail loudly. A test filter that matches nothing must fail, and don't swallow errors with `2>/dev/null`.

### 5. `.devops/agents/bootstrap.sh`
It must be idempotent: cold on the first run, a fast no-op-ish restart after that. Skeleton:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

[ -f .env ] || cp .env.example .env

# Under `set -euo pipefail` a grep with no match aborts the script silently.
# When reading an optional value, add `|| true`:
#   port="$(grep '^HTTPS_PORT=' .env | tail -n 1 | cut -d= -f2-)" || true

append_env() {
    [ -z "$(tail -c 1 .env)" ] || echo >> .env
    echo "$1" >> .env
}

# keys before containers exist (env_file is read at creation)
if ! grep -q '^APP_KEY=base64:' .env; then
    key="base64:$(head -c 32 /dev/urandom | base64)"
    if grep -q '^APP_KEY=' .env; then
        sed -i "s|^APP_KEY=.*|APP_KEY=${key}|" .env
    else
        append_env "APP_KEY=${key}"
    fi
fi

# dev cert without installing a CA (the browser ignores TLS errors)
certs=.docker/traefik/local/certs
if [ ! -f "$certs/<project>.test.pem" ] && command -v mkcert >/dev/null 2>&1; then
    (cd "$certs" && CAROOT="$PWD" mkcert -cert-file <project>.test.pem -key-file <project>.test-key.pem <project>.test "*.<project>.test")
fi

docker compose up -d --build --wait traefik db app

# seed once; fail if the check itself fails
users="$(docker compose exec -T app php artisan tinker --execute='echo App\User::count();' | tail -n 1 | tr -d '[:space:]')"
case "$users" in ''|*[!0-9]*) echo "Could not count users (got: $users)" >&2; exit 1 ;; esac
if [ "$users" = "0" ]; then
    docker compose exec -T -e MAIL_DRIVER=log app php artisan db:seed --force
fi

echo "Stack is up: https://<project>.test (login: <user> / <password>)"
```

If a published port can clash, pick a free one and persist it in `.env` (Trax's `choose_port` helper does this for the Traefik dashboard and DB ports, and skips it when the service is already running); copy it from Trax's `bootstrap.sh`.

### 6. `./devops` launcher
The project must not embed the CLI or its commands. Ship this thin launcher as `./devops`: it runs the `devops` from [convidera/devops-cli](https://github.com/convidera/devops-cli) if it is at least `MIN_VERSION`, looking on `PATH` first and then in its own install directory (`~/.local/bin`, override with `DEVOPS_INSTALL_DIR`), and otherwise downloads that release there for humans (`./devops install` does it explicitly). Humans normally go through `./devops`, so the CLI doesn't need to be on their `PATH`; on runners it is on `PATH` from the image. Agents never download: on a runner without a suitable CLI it exits 127 with "stop and report". Keep `MIN_VERSION` at **v0.0.10 or newer**, the first release with agent mode; bump it only when the project needs a newer CLI, for example a command or `doctor` behaviour added later (per-module `test`/`lint` needs v0.2.0).

**Legacy `./devops` with embedded commands:** if the project still has the old bash `./devops` that contains the commands themselves, migrate it first. Move every command into `.devops/commands.yaml` (run `devops <cmd>` for each to check it behaves the same), delete the bash logic, and only then replace `./devops` with the launcher. Don't keep the old script as a fallback.

```bash
#!/usr/bin/env bash
# Thin launcher for the devops CLI (https://github.com/convidera/devops-cli).
# Runs a new-enough `devops` (from PATH or the install dir); humans get it downloaded if missing.
# `./devops install` installs or upgrades it explicitly.
set -u

MIN_VERSION="v0.0.10"   # first release with agent mode
INSTALL_DIR="${DEVOPS_INSTALL_DIR:-$HOME/.local/bin}"

is_agent() {
    local agent="${DEVOPS_AGENT:-}"
    [ -z "$agent" ] && [ "${CLAUDECODE:-}" = "1" ] && agent=1
    [ -n "$agent" ] && [ "$agent" != "0" ]
}

# A local "dev" build is always accepted.
version_ok() {
    local v
    v="$("$1" version 2>/dev/null)" || return 1
    [ "$v" = "dev" ] && return 0
    [ "$(printf '%s\n%s\n' "$MIN_VERSION" "$v" | sort -V | head -n 1)" = "$MIN_VERSION" ]
}

find_cli() {
    local bin
    while IFS= read -r bin; do
        [ "$bin" -ef "$0" ] && continue
        version_ok "$bin" && { echo "$bin"; return 0; }
    done < <(type -aP devops)
    [ -x "$INSTALL_DIR/devops" ] && version_ok "$INSTALL_DIR/devops" && { echo "$INSTALL_DIR/devops"; return 0; }
    return 1
}

install_cli() {
    local os arch tmp
    case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "devops: unsupported OS $(uname -s)" >&2; return 1 ;; esac
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "devops: unsupported architecture $(uname -m)" >&2; return 1 ;; esac
    mkdir -p "$INSTALL_DIR"
    tmp="$(mktemp "$INSTALL_DIR/.devops-install-XXXXXX")" || return 1
    echo "devops: installing $MIN_VERSION to $INSTALL_DIR/devops" >&2
    if ! curl -fsSL "https://github.com/convidera/devops-cli/releases/download/$MIN_VERSION/devops-$os-$arch" -o "$tmp"; then
        rm -f "$tmp"; echo "devops: download failed" >&2; return 1
    fi
    chmod +x "$tmp" && mv "$tmp" "$INSTALL_DIR/devops"
}

if [ "${1:-}" = "install" ] && ! is_agent; then
    install_cli
    exit $?
fi

if ! cli="$(find_cli)"; then
    if is_agent; then
        echo "devops: CLI >= $MIN_VERSION not installed; agents must use \"devops agents <command>\". Stop and report that the runner image lacks the devops CLI." >&2
        exit 127
    fi
    install_cli || exit 127
    cli="$INSTALL_DIR/devops"
fi
exec "$cli" "$@"
```

Point humans to `./devops install` (they can also put the CLI on their `PATH`, see the CLI's [installation instructions](https://github.com/convidera/devops-cli#installation)) in the project README, and update any docs that describe a project-local installer.

### 7. `AGENTS.md` (under ~50 lines; Claude Code reads it when there is no `CLAUDE.md`)
- One line on what the project is.
- The rule: only use `devops agents <command>`. If `devops` is missing (`command -v devops` fails), stop and report that the runner image lacks the devops CLI; don't fall back to `./devops` or `docker compose up`.
- Any project git conventions (Trax asks for Conventional Commits).
- The command list with example arguments and rough durations.
- How to wait for the hook: `devops agents wait` (blocks; exit 0 ready, 1 failed, 124 timeout) or `devops agents status` (0 ready, 1 failed, 2 running, 3 not started). On failure, `devops agents init --retry --trace` reruns bootstrap and logs every command, which finds silent aborts.
- If the project is previewable (see "Session preview"): one short section saying the agent keeps using the `.test` URL.
- Access: the URL in the Playwright browser, the curl `--resolve` form, the dev login, and to save screenshots under `/tmp/playwright/`.
- Gotchas: never decrypt the regular `.gitsecret` store or ask for GPG keys (if the project uses `.agent-secrets/`, say so and note it's already revealed automatically -- see step 9), use `recreate` after `.env` changes, how to reset the DB, and any log noise.

Keep long findings in the PR description, not in this file.

### 8. Housekeeping
- Gitignore anything bootstrap or builds generate (asset manifests, certs, `.env`).
- Leave the human `commands.yaml` and scripts alone. Don't add headless `if` branches to them; the agent path lives only in `agents.yaml`.
- Don't commit `.claude/settings.json` with an `env` block or blanket `Edit`/`Write` allow rules. Don't add a project `.mcp.json` unless it is project-specific.
- Fix real bugs you find in shared code (for example `$@` quoting in `commands.yaml`) in the same PR, and call them out.

### 9. `.agent-secrets/` for secrets the agent genuinely needs (optional)

Default is still "no secrets" (above): use it only if the project truly
cannot bootstrap without a real credential (e.g. Composer/npm auth against a
private registry). The org's shared runner image supports opting specific
files into a second, independent `git-secret` store that's automatically
revealed before the agent's first turn -- never the project's regular
`.gitsecret` store, and never anything you don't explicitly list here.

```bash
SECRETS_DIR=.agent-secrets git secret init
SECRETS_DIR=.agent-secrets git secret tell agents@convidera.com
SECRETS_DIR=.agent-secrets git secret add <path>   # e.g. auth.json
SECRETS_DIR=.agent-secrets SECRETS_EXTENSION=.agent.secret git secret hide
```

- **Always pass `SECRETS_EXTENSION=.agent.secret`** when hiding. git-secret's
  ciphertext filename (`<path>.secret`) is independent of `SECRETS_DIR` -- a
  project whose regular `.gitsecret` store already tracks the same path (as
  Trax's `auth.json` does) would otherwise collide and silently overwrite the
  wrong store's encrypted blob.
- Add `.devops/agent-secrets.yaml` with `enabled: true` and
  `secrets_extension: .agent.secret` (matching what you hid with) -- the
  opt-in marker the SessionStart hook checks for; without it, nothing is
  revealed even if `.agent-secrets/` exists.
- The plaintext reveals back to whatever path you `add`ed it from (repo root
  for `auth.json`, not `.agent-secrets/`) -- pick the path the tooling that
  needs it (Composer, npm, ...) actually expects, same as any other file.
- This only works when the environment's `ClaudeRunnerEnvironment` has
  `agentSecretsKeyRef` set -- an operator-side, cluster-level setting, not
  something a project PR controls. If it isn't set for your environment yet,
  ask whoever manages that cluster's operator config.
- Only put narrow, rotatable, non-production credentials here. The reveal
  happens inside the same container the agent runs in, so this is a temporal
  boundary (revealed before the agent's first turn, key wiped immediately
  after) -- not filesystem isolation.
- Reference: Trax's `auth.json` (Composer auth for private package repos).

## Session preview (optional, Compose projects with a web UI)

MCS can show the stack a session started to the user, in their own browser. A proxy forwards `https://<session>.preview.devcon.team` to the session pod's published HTTPS port (Traefik on 443) with the preview host as `Host` header and SNI, and rewrites nothing. So the stack has to be willing to be reached under that name **in addition to** `<project>.test`, which the agent keeps using (the Playwright browser only maps `*.test`, `curl --resolve` targets the local name, tests and screenshots use it). Design and contracts: `docs/architecture/preview.md` in [convidera/managed-coding-service](https://github.com/convidera/managed-coding-service).

What the runner/session provides: `MCS_PREVIEW_HOST` (for example `as-<uuid>.preview.devcon.team`) and `MCS_PREVIEW_URL` (`https://<host>`) in the environment of `devops agents bootstrap` and of every agent command, only when MCS has the preview enabled for that environment. Unset everywhere else, so the project must behave exactly as before without it.

What the project adds (one small PR; Trax is the reference, convidera/t-rax2 `.devops/agents/compose.preview.yml`):

1. **`.devops/preview.yaml`**: tells the proxy where to connect. The host is not configured here.
   ```yaml
   scheme: https   # default https
   port: 443       # default 443 for https, 80 for http
   ```
   MCS shows the preview only for repositories that have this file, and only when the adapter's probe (`127.0.0.1:<port>` with `Host: $MCS_PREVIEW_HOST`) gets an answer other than a refused connection, a timeout, Traefik's plain-text 404 or a 502/503/504.
2. **`.devops/agents/compose.preview.yml`**: a compose override that extends every Traefik router rule that serves the UI or API with the preview host and keeps the local one, and passes `MCS_PREVIEW_HOST` to services that need it (a Vite dev server, for example):
   ```yaml
   services:
     app:
       labels:
         - "traefik.http.routers.frontend.rule=(Host(`<project>.test`) || Host(`${MCS_PREVIEW_HOST:?set by the session}`)) && PathPrefix(`/`)"
   ```
   Compose merges `labels` by key, so the override replaces the base rule. For several routers (SPA on `/`, API on `/api`) override each one.
3. **`bootstrap.sh`** loads the override only inside a session, and persists the choice so `recreate`, `up` and `down` keep it (they run plain `docker compose`):
   ```bash
   if [ -n "${MCS_PREVIEW_HOST:-}" ]; then
       grep -q '^MCS_PREVIEW_HOST=' .env || append_env "MCS_PREVIEW_HOST=${MCS_PREVIEW_HOST}"
       grep -q '^COMPOSE_FILE=' .env || append_env "COMPOSE_FILE=docker-compose.yml:.devops/agents/compose.preview.yml"
   fi
   ```
   Put it before the first `docker compose up`, because `.env` and `env_file` are read at container creation.
4. **Audit what derives from the host** and make each of these accept both names. Nothing needs rewriting if all of them follow the request host:
   - the framework trusts `X-Forwarded-Host/Proto` (Laravel: `trustProxies` with the forwarded headers) or builds URLs from the request, not from a fixed `APP_URL`;
   - session cookies are host-only (`SESSION_DOMAIN` null); Sanctum stateful domains and CORS origins list both hosts if used;
   - a Vite dev server: `server.allowedHosts` includes the preview host and `server.origin` is not fixed to the local name (use relative asset URLs when `MCS_PREVIEW_HOST` is set); HMR follows the page origin;
   - websockets use the page origin, not a hard-coded host.

   URLs that the app builds from its own configuration (mail links, queued jobs, the public disk URL) keep showing the local name in the preview; OAuth redirects that must be registered per host do not work on a preview host. Note both in `AGENTS.md`.
5. **`AGENTS.md`**: one short section: the preview host is not for the agent, keep using `https://<project>.test`, what the override does.

Validate without a session: `MCS_PREVIEW_HOST=as-x.preview.devcon.team docker compose -f docker-compose.yml -f .devops/agents/compose.preview.yml config` must show the merged rules (and fail loudly without the variable); a plain `docker compose config` must be unchanged. The real check is a session on a runner with the preview enabled.

## Validate before pushing
- `docker compose config -q` (with a temporary `.env` from `.env.example`), `bash -n` and shellcheck on scripts, and `yq` parses every YAML file.
- With `CLAUDECODE=1` and the CLI installed: `devops agents help` lists exactly your commands with descriptions, and `devops up` exits 2.
- If your session is on a runner, run the full "Done means" list and record timings. Otherwise say so in the PR, and ask for a trial run with the Trax trial prompt adapted to this project.

## PR description
Keep it short: what changed for agents, what changed for humans (ideally nothing), anything you couldn't verify, and the results of the "Done means" checks with timings.
