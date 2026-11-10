# Secret changes workflow

`.github/workflows/secret-changes.yml` is a reusable workflow that comments on a pull request which keys of which
git-secret file changed (key names only, never values) and how the `git secret whoknows` access list changed. It
posts no comment when nothing changed, and deletes its earlier comment when a later push leaves nothing to report.

It reveals the secrets of the PR head and of its base on the runner to compare them, so the caller must pass the GPG
key that can decrypt the repo's files.

## Use it

Add a job to a workflow that runs on `pull_request`. A repo with a `changes` path-filter job and an aggregating `ci`
job runs it as a job there, so a path-skipped run still reports the required check; a repo without one can call it from
a workflow with `on.pull_request.paths` (`.gitsecret/**`, `**/*.secret`).

```yaml
  secret-changes:
    needs: changes
    if: needs.changes.outputs.secrets == 'true' || needs.changes.outputs.workflow == 'true'
    uses: convidera/devops-cli/.github/workflows/secret-changes.yml@v0.2.4
    permissions:
      contents: read
      pull-requests: write
    secrets:
      GPG_PRIVATE_KEY: ${{ secrets.GPG_PRIVATE_KEY }}
```

| Input / secret | Default | Meaning |
|---|---|---|
| `runner` | `gha-runners-convidera-xs` | `runs-on` label |
| `secrets-dir` | `.gitsecret` | git-secret store directory (`SECRETS_DIR`) |
| `secrets-extension` | `.secret` | Extension of the encrypted files, with the leading dot (`SECRETS_EXTENSION`) |
| `comment-tag` | `secret-changes` | Tag of the PR comment; use a different one per call |
| `GPG_PRIVATE_KEY` (secret) | required | Key that decrypts the git-secret files; map your repo's secret name to it |

## A second store

A repo with a second git-secret store (the agent store `.agent-secrets`, extension `.agent.secret`) calls the workflow
once per store, each with its own `comment-tag` so the two comments stay apart. The `GPG_PRIVATE_KEY` must decrypt the
files of that store (Not verified: whether one key serves both stores; pass a different secret per call if not).

```yaml
  secret-changes-agent:
    needs: changes
    if: needs.changes.outputs.agent-secrets == 'true'
    uses: convidera/devops-cli/.github/workflows/secret-changes.yml@<release>
    with:
      secrets-dir: .agent-secrets
      secrets-extension: .agent.secret
      comment-tag: secret-changes-agent
    permissions:
      contents: read
      pull-requests: write
    secrets:
      GPG_PRIVATE_KEY: ${{ secrets.AGENT_SECRET_GPG_KEY }}
```

## Versions

Callers pin a release tag (`@v0.2.4`, bumped by Renovate). A change to the workflow is released with the next
devops-cli release; test it first on a PR in a consuming repo with `@<branch>`.
