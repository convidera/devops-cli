# git-secret reveal action

`.github/actions/git-secret-reveal` is a composite action that reveals the git-secret files of the checked out repository
in a workflow (deploy jobs that need the decrypted `.env` files). It replaces the copies of this action and the
`crazy-max/ghaction-import-gpg` step that repositories used.

- The private key is imported with plain `gpg` from an environment variable: no third-party action sees it, and it is not
  interpolated into a script.
- The key lives in a **temporary GnuPG home** (`mktemp -d` in `RUNNER_TEMP`) that a trap removes when the step ends, also on
  failure, after the agent has been stopped. A later job on a persistent or self-hosted runner finds neither the key nor a running
  agent.
- Keys with a passphrase work without a terminal: `pinentry-mode loopback`, and the passphrase is read from a file in the
  temporary home (not a command line argument, where `ps` shows it).
- `git-secret` is installed with `apt-get` when it is missing.

## Use it

Check out the repository first, then:

```yaml
      - uses: actions/checkout@v7
      - name: Reveal the secrets
        uses: convidera/devops-cli/.github/actions/git-secret-reveal@v0.6.0
        with:
          gpg-key: ${{ secrets.GIT_SECRET_GPG_KEY }}
          # gpg-passphrase: ${{ secrets.GIT_SECRET_GPG_PASSPHRASE }}  # only for a key that has one
```

| Input | |
|---|---|
| `gpg-key` | Armored private key that can decrypt the repository's files (required) |
| `gpg-passphrase` | Passphrase of the key, if it has one |

The revealed files stay in the workspace after the action. Only the key is removed.

Notes:

- The temporary directory is below `RUNNER_TEMP` (falls back to `/tmp` when that path is long): the agent socket lives in
  it and a socket path is limited to about 100 characters.
- A repository with a second store (for example `.agent-secrets`) can reveal it by exporting `SECRETS_DIR` and
  `SECRETS_EXTENSION` in the job `env`; the action does not set them.
