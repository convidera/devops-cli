#!/usr/bin/env python3
"""Adds to each finding of a Docker Scout result file where it comes from, as far as the image data tells.

usage: scout-origin.py <image> <Dockerfile> <full sarif> <result json> [<raw dir>]

<result json> is {"image": ..., "findings": [{"id", "severity", "package", "own"}]} (see the docker-scout-scan action) and is updated in place:
  layer       "backend/Dockerfile:74": the Dockerfile line of the layer that added the files Scout reports the finding for (the
              file paths of the SARIF locations and the dpkg file list of the package, found in the layers of `docker save`);
              left out for the base image
  neededBy    the packages installed on purpose (apt-mark showmanual) that depend, directly or through others, on the package:
              what to change to get rid of it, e.g. ["nginx-light"]
Best effort: every part that cannot be found is left out. The raw inputs go to <raw dir> for inspection. Standard library only.
"""
import json, os, re, subprocess, sys, tarfile, tempfile
from collections import deque

image, dockerfile, sarif_path, result_path = sys.argv[1:5]
raw_dir = sys.argv[5] if len(sys.argv) > 5 else None


def run(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True).stdout


def save(name, text):
    if raw_dir:
        os.makedirs(raw_dir, exist_ok=True)
        with open(os.path.join(raw_dir, name), "w") as f:
            f.write(text)


# --- packages installed on purpose and the dependency graph (Debian images) -----------------------------------------------
manual, reverse, installed = set(), {}, set()
try:
    out = run("docker", "run", "--rm", "-u", "0", "--entrypoint", "sh", image, "-c",
              "apt-mark showmanual; echo ===; dpkg-query -W -f='${Package}|${Depends}|${Pre-Depends}\\n'")
    save("dpkg.txt", out)
    head, _, tail = out.partition("===\n")
    manual = {l.strip() for l in head.splitlines() if l.strip()}
    for row in tail.splitlines():
        name, _, rest = row.partition("|")
        installed.add(name)
        for dep in re.split(r"[,|]", rest.replace("|", ",")):
            dep = re.sub(r"\(.*?\)", "", dep).split(":")[0].strip()
            if dep:
                reverse.setdefault(dep, set()).add(name)
except Exception as error:
    print(f"packages: skipped ({error})", file=sys.stderr)


def needed_by(package):
    """The manually installed packages that need `package`, nearest first (at most 3)."""
    name = package.split(" ")[0]
    start = {p for p in installed if p == name or name in p} if installed else set()
    start = {p for p in start if p not in manual} or start
    seen, queue, found = set(start), deque((p, 0) for p in start), []
    while queue and len(found) < 3:
        current, depth = queue.popleft()
        for parent in sorted(reverse.get(current, ())):
            if parent in seen:
                continue
            seen.add(parent)
            if parent in manual and parent not in found:
                found.append(parent)
            queue.append((parent, depth + 1))
    return found


# --- layers: the Dockerfile line behind each layer of `docker save`, and the first layer that contains a file -------------------
layers = []  # oldest first: {"created_by", "line"} per layer; the base image's layers have no line


def read_archive(archive):
    """Layer tar names and the image history (config) of a docker save archive, oldest first."""
    with tarfile.open(archive) as outer:
        names = set(outer.getnames())
        if "manifest.json" in names:
            manifest = json.load(outer.extractfile("manifest.json"))[0]
            return manifest["Layers"], json.load(outer.extractfile(manifest["Config"]))
        blob = lambda digest: "blobs/sha256/" + digest.split(":")[1]  # OCI layout (containerd image store)
        index = json.load(outer.extractfile("index.json"))
        manifest = json.load(outer.extractfile(blob(index["manifests"][0]["digest"])))
        return [blob(l["digest"]) for l in manifest["layers"]], json.load(outer.extractfile(blob(manifest["config"]["digest"])))


def clean(cmd):
    cmd = re.sub(r"\s*#\s*buildkit\s*$", "", cmd)
    cmd = re.sub(r"^(RUN\s+)?(\|\d+\s+(\S+=\S*\s+)*)?", "", cmd)
    cmd = re.sub(r"^/bin/(ba)?sh -c\s+(#\(nop\)\s*)?", "", cmd)
    cmd = re.sub(r"^(COPY|ADD)\s+", "", cmd)
    cmd = re.sub(r"^(--\S+\s+)+", "", cmd)
    return " ".join(cmd.split())


def first_layers(wanted):
    """{path without leading slash: index of the first layer containing it}; fills `layers`."""
    found = {}
    with tempfile.TemporaryDirectory() as tmp:
        archive = os.path.join(tmp, "image.tar")
        subprocess.run(["docker", "save", "-o", archive, image], check=True)
        members, config = read_archive(archive)
        # the instructions of the Dockerfile (continuation lines joined), without the keyword and --flags, whitespace removed
        with open(dockerfile) as f:
            text = f.read().replace("\\\n", " ")
        instructions = []
        for number, raw in enumerate(text.splitlines(), 1):
            body = re.sub(r"^\s*(RUN|COPY|ADD|ENV|ARG|WORKDIR|USER|SHELL)\s+((--\S+\s+)*)", "", raw)
            if body != raw:
                instructions.append((number, re.sub(r"[\s\\]+", "", body)))
        entries = [h for h in config.get("history", []) if not h.get("empty_layer")]
        if len(entries) != len(members):
            raise RuntimeError(f"{len(members)} layers but {len(entries)} history entries")
        for entry in entries:
            created_by = clean(entry.get("created_by", ""))
            key = re.sub(r"[\s\\]+", "", created_by)[:50]
            line = next((n for n, body in instructions if len(key) >= 8 and body.startswith(key)), None)
            layers.append({"created_by": created_by[:80], "line": f"{dockerfile}:{line}" if line else None})
        save("layers.json", json.dumps(layers, indent=1))
        if not wanted:
            return found
        with tarfile.open(archive) as outer:
            for index, member in enumerate(members):
                with tarfile.open(fileobj=outer.extractfile(member), mode="r|*") as layer:
                    for entry in layer:
                        name = entry.name.lstrip("./")
                        if name in wanted and name not in found:
                            found[name] = index
    return found


# --- the findings -----------------------------------------------------------------------------------------------------------
with open(result_path) as f:
    result = json.load(f)
with open(sarif_path) as f:
    sarif = json.load(f)

paths_by_id = {}
for run_ in sarif.get("runs", []):
    for res in run_.get("results", []):
        for location in res.get("locations", []):
            uri = location.get("physicalLocation", {}).get("artifactLocation", {}).get("uri", "")
            if uri.startswith("/"):
                paths_by_id.setdefault(res.get("ruleId"), []).append(uri.lstrip("/"))


def dpkg_lists(package):
    name = package.split(" ")[0]
    return [f"var/lib/dpkg/info/{p}.list" for p in installed if p == name or p.startswith(name + ":")] or \
           [f"var/lib/dpkg/info/{name}.list", f"var/lib/dpkg/info/{name}:amd64.list"]


wanted = set()
for finding in result["findings"]:
    wanted.update(paths_by_id.get(finding["id"], []))
    if finding.get("package"):
        wanted.update(dpkg_lists(finding["package"]))
try:
    layer_of = first_layers(wanted)
except Exception as error:
    print(f"layers: skipped ({error})", file=sys.stderr)
    layer_of = {}
save("layer_of.json", json.dumps(layer_of, indent=1))

for finding in result["findings"]:
    candidates = paths_by_id.get(finding["id"], [])[:3] + (dpkg_lists(finding["package"]) if finding.get("package") else [])
    indexes = [layer_of[c] for c in candidates if c in layer_of]
    if indexes:
        line = layers[min(indexes)]["line"]
        if line:
            finding["layer"] = line
    if finding.get("package"):
        owners = needed_by(finding["package"])
        if owners:
            finding["neededBy"] = owners

with open(result_path, "w") as f:
    json.dump(result, f, separators=(",", ":"))
print(f"origin: {sum('layer' in x for x in result['findings'])} with a layer, "
      f"{sum('neededBy' in x for x in result['findings'])} with a depending package, of {len(result['findings'])}")
