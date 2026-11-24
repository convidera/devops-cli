#!/usr/bin/env bash
# One pull request comment per tag, created or updated in place, or deleted (replaces thollander/actions-comment-pull-request,
# a Node 20 action). Keeps the marker of that action, so comments it wrote earlier are found and updated.
#   usage: pr-comment.sh <tag> <markdown file>   create or update
#          pr-comment.sh <tag> --delete          delete when present
# Used by the action next to it (action.yml). Needs GH_TOKEN, PR_NUMBER and GITHUB_REPOSITORY in the environment.
set -euo pipefail

tag="${1:?tag}"
what="${2:?markdown file or --delete}"
marker="<!-- thollander/actions-comment-pull-request \"${tag}\" -->"
api="${GITHUB_API_URL:-https://api.github.com}/repos/${GITHUB_REPOSITORY:?}/issues"

# curl and jq only: the self-hosted runners have no gh
call() { curl -fsS -H "Authorization: Bearer ${GH_TOKEN:?}" -H "Accept: application/vnd.github+json" "$@"; }

id=""
page=1
while [ -z "$id" ]; do
    comments="$(call "${api}/${PR_NUMBER:?}/comments?per_page=100&page=${page}")"
    [ "$(jq length <<< "$comments")" -gt 0 ] || break
    id="$(jq -r --arg marker "$marker" '[.[] | select(.body | contains($marker)) | .id] | first // empty' <<< "$comments")"
    page=$((page + 1))
done

if [ "$what" = "--delete" ]; then
    [ -z "$id" ] || call -X DELETE "${api}/comments/${id}" > /dev/null
    exit 0
fi

body="$(jq -Rs --arg marker "$marker" '{body: (. + "\n" + $marker)}' "$what")"
if [ -n "$id" ]; then
    call -X PATCH "${api}/comments/${id}" -d "$body" > /dev/null
else
    call -X POST "${api}/${PR_NUMBER}/comments" -d "$body" > /dev/null
fi
