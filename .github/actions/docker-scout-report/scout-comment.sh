#!/usr/bin/env bash
# Builds the single Docker Scout pull request comment from the per-image results of the docker-scout-scan action.
#   usage: scout-comment.sh <directory with <image>.json files> <run url>
# Environment: GITHUB_REPOSITORY and SCOUT_COMMIT (head commit of the pull request) turn the Dockerfile lines into links.
# Each file is {"image": "<name>", "findings": [{"id": "CVE-...", "severity": "critical|high|medium", "package": "openssl 3.5.7", "layer": "backend/Dockerfile:74", "neededBy": ["nginx-light"], "own": true|false}, ...]}.
# own: still reported when Scout ignores the base image, i.e. it comes from our layers; the rest is inherited.
# (The scout action keeps one comment per workflow and command, so the scans of the matrix would overwrite each other.)
set -euo pipefail

dir="${1:?directory with the per-image results}"
run_url="${2:-}"

# badge <label> <count> <colour> [prefix]: shields.io badge, grey when the count is 0
badge() {
    local colour="$3"
    [ "$2" -gt 0 ] || colour="lightgrey"
    echo -n "![$1 $2](https://img.shields.io/badge/$1-${4:-}$2-${colour}?labelColor=555) "
}

# count <file> <jq filter selecting entries>
count() { jq "[$2] | length" "$1"; }

echo "## 🔍 Docker Scout: critical, high and medium vulnerabilities"
echo
echo "Image total = inherited from the base image + in our layers. Our layers are what Scout still reports with the base image ignored: the part we can change."
echo
echo "| Image | Image total | Base image | Our layers |"
echo "|---|---|---|---|"

total=0
details=""
shopt -s nullglob
for file in "$dir"/*.json; do
    image="$(jq -r '.image' "$file")"
    all_row=""; base_row=""; own_row=""; own_total=0
    for sev in critical high medium; do
        case "$sev" in critical) colour=A94442;; high) colour=D9534F;; medium) colour=F0AD4E;; esac
        n="$(count "$file" ".findings[] | select(.severity == \"$sev\")")"
        o="$(count "$file" ".findings[] | select(.severity == \"$sev\" and .own)")"
        all_row="${all_row}$(badge "$sev" "$n" "$colour")"
        base_row="${base_row}$(badge "$sev" "$((n - o))" "$colour")"
        own_row="${own_row}$(badge "$sev" "$o" "$colour" "%2B")"
        own_total=$((own_total + o)); total=$((total + n))
    done
    echo "| \`${image}\` | ${all_row}| ${base_row}| ${own_row}|"
    if [ "$own_total" -gt 0 ]; then
        links="$(jq -r --arg repo "${GITHUB_REPOSITORY:-}" --arg commit "${SCOUT_COMMIT:-}" '
            # "backend/Dockerfile:12" as a link to that line at the commit of the pull request
            def layer_link($layer):
                if $repo == "" or $commit == "" then "`" + $layer + "`"
                else ($layer | capture("^(?<file>.+):(?<line>[0-9]+)$")) as $l
                    | "[`" + $layer + "`](https://github.com/" + $repo + "/blob/" + $commit + "/" + $l.file + "#L" + $l.line + ")"
                end;
            .findings | map(select(.own)) | sort_by(({"critical":0,"high":1,"medium":2}[.severity]), .id)
            | map("- [`" + .id + "`](https://scout.docker.com/v/" + .id + ") " + .severity + (if (.package // "") != "" then " in `" + .package + "`" else "" end)
                + (if (.neededBy // []) != [] then ", needed by " + (.neededBy | map("`" + . + "`") | join(", ")) else "" end)
                + (if (.layer // "") != "" then ", layer " + layer_link(.layer) else "" end)) | join("\n")' "$file")"
        details="${details}<details><summary><code>${image}</code>: ${own_total} in our layers</summary>

${links}

</details>

"
    fi
done

echo
[ -z "$details" ] || printf '%s' "$details"
if [ "$total" -eq 0 ]; then
    echo "No critical, high or medium vulnerabilities in the scanned images."
fi
echo "Every image built by this pull request is scanned. The scan reports and does not fail the build."
[ -z "$run_url" ] || echo "Full reports: the job summaries of [this run](${run_url})."
