# Docker Scout scan of the images a pull request changes

Three composite actions in `.github/actions/` scan the images a pull request builds with Docker Scout and post **one**
comment for all of them: per image the totals of critical, high and medium vulnerabilities, split into what is
inherited from the base image and what is in the repo's own layers, with the latter listed and linked to the
Dockerfile line (and the packages that pull it in, on Debian images). The scan **reports and never fails** the build.

| Action | Does |
|---|---|
| `docker-scout-scan` | Scans one image that is loaded in the runner's Docker daemon (full scan for the job summary, a second one with the base image ignored), works out where each finding comes from and uploads a small result artifact. |
| `docker-scout-report` | In a job of its own after the scans: downloads the result artifacts, builds the comment and creates, updates or (when no image was scanned) deletes it. |
| `pr-comment` | One comment per tag and pull request, created or updated in place, or deleted. Plain `curl` and `jq` (the runners have no `gh`). Used by `docker-scout-report`, usable on its own. |

The scout action keeps a single comment per workflow and command, so a matrix of images would overwrite its own
comments; that is why the comment is built from the results of all images.

## Use it

Build each image **with `load: true`** (the scan reads it from the local daemon), then scan it. A matrix job per image:

```yaml
  image-scan:
    runs-on: gha-runners-convidera-m
    strategy:
      fail-fast: false
      matrix:
        include:
          - { name: backend, context: backend, image: my-org/app-backend:ci }
          - { name: frontend, context: frontend, image: my-org/app-frontend:ci }
    steps:
      - uses: actions/checkout@v7
      - uses: docker/setup-buildx-action@v4
      - uses: docker/login-action@v4
        with:
          username: ${{ secrets.DOCKER_HUB_USERNAME }}
          password: ${{ secrets.DOCKER_HUB_PASSWORD }}
      - uses: docker/build-push-action@v7
        with:
          context: ${{ matrix.context }}
          target: production
          tags: ${{ matrix.image }}
          load: true
      - uses: convidera/devops-cli/.github/actions/docker-scout-scan@v0.5.0
        with:
          image: ${{ matrix.image }}
          name: ${{ matrix.name }}
          dockerfile: ${{ matrix.context }}/Dockerfile
          dockerhub-user: ${{ secrets.DOCKER_HUB_USERNAME }}
          dockerhub-password: ${{ secrets.DOCKER_HUB_PASSWORD }}

  scout-report:
    needs: [changes, image-scan]
    if: always() && github.event.pull_request.head.repo.full_name == github.repository
    runs-on: gha-runners-convidera-tiny
    permissions:
      contents: read
      pull-requests: write
    steps:
      - uses: convidera/devops-cli/.github/actions/docker-scout-report@v0.5.0
        with:
          # remove the comment only when no image changed, not when a scan failed
          remove-when-empty: ${{ needs.changes.outputs.images_any != 'true' }}
```

Notes:

- Pin the actions to a release tag like the workflows (`@v0.5.0`); they ship from the same tags.
- `docker-scout-report` needs no checkout. Run it only for pull requests of the same repo: a fork's token cannot write comments.
- `name` is the key of the image in this run (artifact and file names), unique per matrix entry.
- A pull request that changes no image: skip `image-scan` (a `changes` path filter) but let `scout-report` still run (do not
  gate it by the filter): with no result and `remove-when-empty` it removes the comment of an earlier run, for example
  after a push that reverts the image changes. When images changed and a scan failed, pass `false` so the last comment stays.
- Keep the scan out of the aggregating required check: it reports, it does not decide.
- The origin of a finding (Dockerfile line, `neededBy`) works for Debian-based images and is best effort; Alpine and others get less
  detail. The raw inputs are kept as the artifact `sarif-<name>` for seven days.
- Private repositories: the repo's Actions settings must allow access from the other repositories of the organization, as
  for the reusable workflows.
