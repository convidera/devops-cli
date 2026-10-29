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
    uses: convidera/devops-cli/.github/workflows/secret-changes.yml@v1
    permissions:
      contents: read
      pull-requests: write
    secrets:
      GPG_PRIVATE_KEY: ${{ secrets.GPG_PRIVATE_KEY }}
```

| Input / secret | Default | Meaning |
|---|---|---|
| `runner` | `gha-runners-convidera-xs` | `runs-on` label |
| `GPG_PRIVATE_KEY` (secret) | required | Key that decrypts the git-secret files; map your repo's secret name to it |

## Versions

Callers pin a major tag (`@v1`, bumped by Renovate). A change to the workflow is released by moving the tag, so test
it on a PR in a consuming repo with `@<branch>` first.
