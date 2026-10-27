#!/usr/bin/env bash
# Build one release platform without deleting other platform outputs.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
version=${1:?Usage: release-target.sh TAG OS/ARCH [OUTPUT]}
target=${2:?Usage: release-target.sh TAG OS/ARCH [OUTPUT]}
out=${3:-$root/dist}
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid stable version' >&2; exit 2; }
case "$target" in linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;; *) echo 'Unsupported release target' >&2; exit 2;; esac
mkdir -p "$out"
out=$(cd -- "$out" && pwd -P)
suffix=""; [[ "$target" != windows/* ]] || suffix=.exe
GOOS=${target%/*} GOARCH=${target#*/} LAOWANGBOT_VERSION=$version bash "$root/scripts/build.sh" "$out/laowangbot-${target/\//-}$suffix"
